package eloquent

import "github.com/akyrey/laravel-ls/internal/phputil"

const modelBaseFQN phputil.FQN = "Illuminate\\Database\\Eloquent\\Model"

// eloquentAttributeTypeFQN is the return type of modern accessor methods.
const eloquentAttributeTypeFQN phputil.FQN = "Illuminate\\Database\\Eloquent\\Casts\\Attribute"

// eloquentRelationsPrefix is the namespace for all built-in Eloquent relation classes.
const eloquentRelationsPrefix = "Illuminate\\Database\\Eloquent\\Relations\\"

type classDecl struct {
	Extends  phputil.FQN
	Location phputil.Location
}

type symbolTable struct {
	classes map[phputil.FQN]*classDecl
	models  map[phputil.FQN]struct{} // populated by resolveModels
	byFile  map[string][]phputil.FQN // path → FQNs declared in that file
}

func newSymbolTable() *symbolTable {
	return &symbolTable{
		classes: make(map[phputil.FQN]*classDecl),
		models:  make(map[phputil.FQN]struct{}),
		byFile:  make(map[string][]phputil.FQN),
	}
}

func (st *symbolTable) addClass(path string, fqn phputil.FQN, d *classDecl) {
	st.classes[fqn] = d
	st.byFile[path] = append(st.byFile[path], fqn)
}

// removeFile removes all class declarations contributed by path and re-resolves
// the model set. Called before re-scanning a changed file.
func (st *symbolTable) removeFile(path string) {
	for _, fqn := range st.byFile[path] {
		delete(st.classes, fqn)
	}
	delete(st.byFile, path)
	st.models = make(map[phputil.FQN]struct{})
	st.resolveModels()
}

// clone returns a shallow copy of st with independent maps (classDecl values are
// shared since they are never mutated after construction).
func (st *symbolTable) clone() *symbolTable {
	c := &symbolTable{
		classes: make(map[phputil.FQN]*classDecl, len(st.classes)),
		models:  make(map[phputil.FQN]struct{}),
		byFile:  make(map[string][]phputil.FQN, len(st.byFile)),
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

func (st *symbolTable) isModel(fqn phputil.FQN) bool {
	_, ok := st.models[fqn]
	return ok
}

// resolveModels walks all extends chains and marks every class that eventually
// reaches modelBaseFQN. Called once after phase 1 has populated st.classes.
func (st *symbolTable) resolveModels() {
	memo := make(map[phputil.FQN]bool)

	var check func(fqn phputil.FQN) bool
	check = func(fqn phputil.FQN) bool {
		if v, seen := memo[fqn]; seen {
			return v
		}
		if fqn == modelBaseFQN {
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
			st.models[fqn] = struct{}{}
		}
	}
}
