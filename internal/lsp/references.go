package lsp

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpparse"
	"github.com/akyrey/laravel-lsp/internal/phputil"
)

// referenceScanDirs lists subdirectories under root that are walked during a
// references scan. Covers controllers, jobs, policies, etc.
var referenceScanDirs = []string{"app", "routes"}

// References handles textDocument/references requests.
func (s *Server) References(_ *glsp.Context, p *protocol.ReferenceParams) ([]protocol.Location, error) {
	s.mu.RLock()
	bindings, models, root := s.bindings, s.models, s.root
	s.mu.RUnlock()
	if bindings == nil || models == nil || root == "" {
		return nil, nil
	}

	src, err := s.docs.Read(p.TextDocument.URI)
	if err != nil {
		return nil, nil
	}

	path := URIToPath(p.TextDocument.URI)
	offset := positionToByteOffset(src, p.Position)

	sym := identifySymbol(src, path, offset, bindings, models)
	if sym == nil {
		return nil, nil
	}

	locs := scanReferences(root, referenceScanDirs, sym, s.docs, models)

	if p.Context.IncludeDeclaration {
		locs = append(locs, declarationLocations(sym, bindings, models)...)
	}
	if len(locs) == 0 {
		return nil, nil
	}
	return locs, nil
}

// — Symbol identification —

// refSymbol describes what we are searching for. Exactly one of the two flows
// is set (the other is zero).
type refSymbol struct {
	// Eloquent attribute: all $model->propName accesses where $model: modelFQN.
	modelFQN phputil.FQN
	propName string // snake_case

	// Container abstract: all app(abstractFQN::class) usages.
	abstractFQN phputil.FQN
}

func (r *refSymbol) isEloquent() bool  { return r.modelFQN != "" && r.propName != "" }
func (r *refSymbol) isContainer() bool { return r.abstractFQN != "" }

// identifySymbol determines what refSymbol is at offset in src.
// Returns nil when no known symbol is found.
func identifySymbol(
	src []byte,
	path string,
	offset int,
	bindings *container.BindingIndex,
	models *eloquent.ModelIndex,
) *refSymbol {
	sym, _ := identifySymbolWithLoc(src, path, offset, bindings, models)
	return sym
}

// identifySymbolWithLoc is like identifySymbol but also returns the exact token
// location. tokenLoc is zero when sym is nil.
func identifySymbolWithLoc(
	src []byte,
	path string,
	offset int,
	bindings *container.BindingIndex,
	models *eloquent.ModelIndex,
) (*refSymbol, phputil.Location) {
	root, err := phpparse.Bytes(src, path)
	if err != nil || root == nil {
		return nil, phputil.Location{}
	}
	fc := &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)}
	sv := &symFinder{
		fc:       fc,
		offset:   offset,
		bindings: bindings,
		models:   models,
	}
	traverser.NewTraverser(sv).Traverse(root)
	return sv.sym, sv.tokenLoc
}

// symFinder is a single-pass visitor that identifies the symbol under the
// cursor. It recognises the same patterns as defVisitor plus method declarations.
type symFinder struct {
	visitor.Null
	fc       *phputil.FileContext
	offset   int
	bindings *container.BindingIndex
	models   *eloquent.ModelIndex

	encClass     phputil.FQN
	encMethod    *ast.StmtClassMethod
	assignedVars map[string]phputil.FQN

	sym      *refSymbol
	tokenLoc phputil.Location // exact range of the matched token
}

func (v *symFinder) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *symFinder) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *symFinder) StmtGroupUse(n *ast.StmtGroupUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, phputil.NameToString(n.Prefix))
}

func (v *symFinder) StmtClass(n *ast.StmtClass) {
	pos := n.GetPosition()
	if pos == nil || pos.StartPos > v.offset || v.offset >= pos.EndPos {
		return
	}
	v.encClass = phputil.ClassNodeFQN(n.Name, v.fc)
}

func (v *symFinder) StmtClassMethod(n *ast.StmtClassMethod) {
	pos := n.GetPosition()
	if pos == nil || pos.StartPos > v.offset || v.offset >= pos.EndPos {
		return
	}
	v.encMethod = n
	v.assignedVars = collectAssignments(n, v.fc)

	// Cursor on the method name itself → find the exposed attribute name.
	nameID, ok := n.Name.(*ast.Identifier)
	if !ok {
		return
	}
	namePos := nameID.GetPosition()
	if namePos == nil || v.offset < namePos.StartPos || v.offset >= namePos.EndPos {
		return
	}
	if v.encClass == "" {
		return
	}
	cat := v.models.Lookup(v.encClass)
	if cat == nil {
		return
	}
	methodName := string(nameID.Value)
	for exposed, attrs := range cat.ByExposed {
		for _, a := range attrs {
			if a.MethodName == methodName && a.Source == eloquent.SourceAST {
				v.sym = &refSymbol{modelFQN: v.encClass, propName: exposed}
				v.tokenLoc = phputil.FromPosition(v.fc.Path, namePos)
				return
			}
		}
	}
}

func (v *symFinder) ExprPropertyFetch(n *ast.ExprPropertyFetch) {
	prop, ok := n.Prop.(*ast.Identifier)
	if !ok {
		return
	}
	propPos := prop.GetPosition()
	if propPos == nil || v.offset < propPos.StartPos || v.offset >= propPos.EndPos {
		return
	}
	propName := string(prop.Value)

	modelFQN := resolveExprType(n.Var, v.encClass, v.encMethod, v.assignedVars, v.fc, v.models)
	if modelFQN == "" {
		return
	}
	cat := v.models.Lookup(modelFQN)
	if cat == nil {
		return
	}
	if _, ok := cat.ByExposed[propName]; !ok {
		return
	}
	v.sym = &refSymbol{modelFQN: modelFQN, propName: propName}
	v.tokenLoc = phputil.FromPosition(v.fc.Path, propPos)
}

func (v *symFinder) ExprClassConstFetch(n *ast.ExprClassConstFetch) {
	constID, ok := n.Const.(*ast.Identifier)
	if !ok || string(constID.Value) != "class" {
		return
	}
	classPos := n.Class.GetPosition()
	if classPos == nil || v.offset < classPos.StartPos || v.offset >= classPos.EndPos {
		return
	}
	fqn := v.fc.Resolve(phputil.NameToString(n.Class))
	if len(v.bindings.Lookup(fqn)) > 0 {
		v.sym = &refSymbol{abstractFQN: fqn}
		v.tokenLoc = phputil.FromPosition(v.fc.Path, classPos)
	}
}

// — Reference scanning —

// scanReferences walks dirs (relative to root) and collects all locations
// matching sym. Callers pass referenceScanDirs for production or "." for tests.
func scanReferences(root string, dirs []string, sym *refSymbol, docs *DocumentStore, models *eloquent.ModelIndex) []protocol.Location {
	var locs []protocol.Location
	for _, dir := range dirs {
		scanDir := filepath.Join(root, dir)
		if _, err := os.Stat(scanDir); err != nil {
			continue
		}
		_ = filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".php") {
				return nil
			}
			src, err := docs.Read(PathToURI(path))
			if err != nil {
				return nil
			}
			astRoot, err := phpparse.Bytes(src, path)
			if err != nil || astRoot == nil {
				return nil
			}
			rv := &refsVisitor{
				fc:     &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
				sym:    sym,
				path:   path,
				models: models,
			}
			traverser.NewTraverser(rv).Traverse(astRoot)
			for _, loc := range rv.locations {
				locs = append(locs, toLSPLocation(loc))
			}
			return nil
		})
	}
	return locs
}

// declarationLocations returns the declaration sites for sym (used when
// includeDeclaration is true).
func declarationLocations(sym *refSymbol, bindings *container.BindingIndex, models *eloquent.ModelIndex) []protocol.Location {
	var locs []protocol.Location
	if sym.isEloquent() {
		cat := models.Lookup(sym.modelFQN)
		if cat != nil {
			attrs := cat.ByExposed[sym.propName]
			sorted := make([]eloquent.ModelAttribute, len(attrs))
			copy(sorted, attrs)
			sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Kind < sorted[j].Kind })
			for _, a := range sorted {
				if !a.Location.Zero() {
					locs = append(locs, toLSPLocation(a.Location))
				}
			}
		}
	}
	if sym.isContainer() {
		for _, b := range bindings.Lookup(sym.abstractFQN) {
			if !b.Location.Zero() {
				locs = append(locs, toLSPLocation(b.Location))
			}
		}
	}
	return locs
}

// — refsVisitor: file-level reference collector —

type refsVisitor struct {
	visitor.Null
	fc           *phputil.FileContext
	sym          *refSymbol
	path         string
	models       *eloquent.ModelIndex
	encClass     phputil.FQN
	encMethod    *ast.StmtClassMethod
	assignedVars map[string]phputil.FQN
	locations    []phputil.Location
}

func (v *refsVisitor) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *refsVisitor) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *refsVisitor) StmtGroupUse(n *ast.StmtGroupUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, phputil.NameToString(n.Prefix))
}

func (v *refsVisitor) StmtClass(n *ast.StmtClass) {
	// Always update — refsVisitor scans the whole file, not just the cursor.
	// Reset encMethod so it doesn't bleed from the previous class.
	v.encClass = phputil.ClassNodeFQN(n.Name, v.fc)
	v.encMethod = nil
}

func (v *refsVisitor) StmtClassMethod(n *ast.StmtClassMethod) {
	v.encMethod = n
	v.assignedVars = collectAssignments(n, v.fc)
}

func (v *refsVisitor) ExprPropertyFetch(n *ast.ExprPropertyFetch) {
	if !v.sym.isEloquent() {
		return
	}
	prop, ok := n.Prop.(*ast.Identifier)
	if !ok || string(prop.Value) != v.sym.propName {
		return
	}
	modelFQN := resolveExprType(n.Var, v.encClass, v.encMethod, v.assignedVars, v.fc, v.models)
	if modelFQN != v.sym.modelFQN {
		return
	}
	v.locations = append(v.locations, phputil.FromPosition(v.path, prop.GetPosition()))
}

func (v *refsVisitor) ExprClassConstFetch(n *ast.ExprClassConstFetch) {
	if !v.sym.isContainer() {
		return
	}
	constID, ok := n.Const.(*ast.Identifier)
	if !ok || string(constID.Value) != "class" {
		return
	}
	fqn := v.fc.Resolve(phputil.NameToString(n.Class))
	if fqn != v.sym.abstractFQN {
		return
	}
	v.locations = append(v.locations, phputil.FromPosition(v.path, n.GetPosition()))
}
