package container

import "github.com/akyrey/laravel-lsp/internal/phputil"

const serviceProviderFQN phputil.FQN = "Illuminate\\Support\\ServiceProvider"

// classDecl holds the declaration info recorded during the first scan pass.
type classDecl struct {
	Extends     phputil.FQN
	Location    phputil.Location
	IsInterface bool
}

// symbolTable is the cross-file class declaration map built in phase 1.
type symbolTable struct {
	classes          map[phputil.FQN]*classDecl
	serviceProviders map[phputil.FQN]struct{} // populated by resolveServiceProviders
	byFile           map[string][]phputil.FQN // path → FQNs declared in that file
}

func newSymbolTable() *symbolTable {
	return &symbolTable{
		classes:          make(map[phputil.FQN]*classDecl),
		serviceProviders: make(map[phputil.FQN]struct{}),
		byFile:           make(map[string][]phputil.FQN),
	}
}

func (st *symbolTable) addClass(path string, fqn phputil.FQN, d *classDecl) {
	st.classes[fqn] = d
	st.byFile[path] = append(st.byFile[path], fqn)
}

// removeFile removes all class declarations contributed by path and re-resolves
// the service provider set.
func (st *symbolTable) removeFile(path string) {
	for _, fqn := range st.byFile[path] {
		delete(st.classes, fqn)
	}
	delete(st.byFile, path)
	st.serviceProviders = make(map[phputil.FQN]struct{})
	st.resolveServiceProviders()
}

// clone returns a shallow copy of st with independent maps.
func (st *symbolTable) clone() *symbolTable {
	c := &symbolTable{
		classes:          make(map[phputil.FQN]*classDecl, len(st.classes)),
		serviceProviders: make(map[phputil.FQN]struct{}),
		byFile:           make(map[string][]phputil.FQN, len(st.byFile)),
	}
	for k, v := range st.classes {
		c.classes[k] = v
	}
	for k, v := range st.byFile {
		cp := make([]phputil.FQN, len(v))
		copy(cp, v)
		c.byFile[k] = cp
	}
	return c
}

// classLocation returns the location of a class declaration, or zero if unknown.
func (st *symbolTable) classLocation(fqn phputil.FQN) phputil.Location {
	if d, ok := st.classes[fqn]; ok {
		return d.Location
	}
	return phputil.Location{}
}

// isServiceProvider returns true if fqn directly or transitively extends
// Illuminate\Support\ServiceProvider. Call only after resolveServiceProviders.
func (st *symbolTable) isServiceProvider(fqn phputil.FQN) bool {
	_, ok := st.serviceProviders[fqn]
	return ok
}

// resolveServiceProviders walks all extends chains and marks every class
// that eventually reaches serviceProviderFQN. Called once after phase 1
// has populated st.classes.
func (st *symbolTable) resolveServiceProviders() {
	// memo avoids re-walking the same chain twice.
	memo := make(map[phputil.FQN]bool)

	var check func(fqn phputil.FQN) bool
	check = func(fqn phputil.FQN) bool {
		if v, seen := memo[fqn]; seen {
			return v
		}
		if fqn == serviceProviderFQN {
			memo[fqn] = true
			return true
		}
		decl, ok := st.classes[fqn]
		if !ok || decl.Extends == "" {
			memo[fqn] = false
			return false
		}
		result := check(decl.Extends)
		memo[fqn] = result
		return result
	}

	for fqn := range st.classes {
		if check(fqn) {
			st.serviceProviders[fqn] = struct{}{}
		}
	}
}
