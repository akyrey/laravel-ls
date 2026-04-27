package lsp

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

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

	locs := scanReferences(root, s.effectiveReferenceDirs(root), sym, s.docs, models)

	if p.Context.IncludeDeclaration {
		locs = append(locs, declarationLocations(sym, bindings, models)...)
	}
	if len(locs) == 0 {
		return nil, nil
	}
	return locs, nil
}

// — Symbol identification —

// refSymbol describes what we are searching for.
type refSymbol struct {
	modelFQN    phputil.FQN
	propName    string // snake_case
	abstractFQN phputil.FQN
}

func (r *refSymbol) isEloquent() bool  { return r.modelFQN != "" && r.propName != "" }
func (r *refSymbol) isContainer() bool { return r.abstractFQN != "" }

// identifySymbol determines what refSymbol is at offset in src.
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
// location.
func identifySymbolWithLoc(
	src []byte,
	path string,
	offset int,
	bindings *container.BindingIndex,
	models *eloquent.ModelIndex,
) (*refSymbol, phputil.Location) {
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return nil, phputil.Location{}
	}
	defer tree.Close()

	sv := &symFinder{
		src:      src,
		fc:       &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
		offset:   offset,
		bindings: bindings,
		models:   models,
	}
	phpwalk.Walk(path, src, tree, sv)
	return sv.sym, sv.tokenLoc
}

// symFinder identifies the symbol under the cursor.
type symFinder struct {
	phpwalk.NullVisitor
	src      []byte
	fc       *phputil.FileContext
	offset   int
	bindings *container.BindingIndex
	models   *eloquent.ModelIndex

	encClass     phputil.FQN
	encMethod    *phpwalk.MethodInfo
	assignedVars map[string]phputil.FQN

	sym      *refSymbol
	tokenLoc phputil.Location
}

func (v *symFinder) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *symFinder) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *symFinder) VisitClass(n phpwalk.ClassInfo) {
	if !cursorOnNode(v.offset, n.Raw) {
		return
	}
	v.encClass = v.fc.Resolve(n.NameText)
}

func (v *symFinder) VisitClassMethod(n phpwalk.MethodInfo) {
	if v.offset < n.StartByte || v.offset >= n.EndByte {
		return
	}
	v.encMethod = &n
	v.assignedVars = collectAssignments(n, v.fc)

	// Cursor on the method name → find the exposed attribute name.
	nameNode := n.Raw.ChildByFieldName("name")
	if nameNode == nil || !cursorOnNode(v.offset, nameNode) {
		return
	}
	if v.encClass == "" {
		return
	}
	cat := v.models.Lookup(v.encClass)
	if cat == nil {
		return
	}
	methodName := phpnode.NodeText(nameNode, v.src)
	for exposed, attrs := range cat.ByExposed {
		for _, a := range attrs {
			if a.MethodName == methodName && a.Source == eloquent.SourceAST {
				v.sym = &refSymbol{modelFQN: v.encClass, propName: exposed}
				v.tokenLoc = phpnode.FromNode("", nameNode)
				return
			}
		}
	}
}

func (v *symFinder) VisitPropertyFetch(n phpwalk.PropertyFetchInfo) {
	if v.offset < n.PropLocation.StartByte || v.offset >= n.PropLocation.EndByte {
		return
	}
	var params []phpwalk.ParamInfo
	if v.encMethod != nil {
		params = v.encMethod.Params
	}
	modelFQN := resolveExprType(n.VarRaw, n.Src, v.encClass, params, v.assignedVars, v.fc, v.models)
	if modelFQN == "" {
		return
	}
	cat := v.models.Lookup(modelFQN)
	if cat == nil {
		return
	}
	if _, ok := cat.ByExposed[n.PropName]; !ok {
		return
	}
	v.sym = &refSymbol{modelFQN: modelFQN, propName: n.PropName}
	v.tokenLoc = n.PropLocation
}

func (v *symFinder) VisitClassConstFetch(n phpwalk.ClassConstFetchInfo) {
	if n.ConstName != "class" {
		return
	}
	if v.offset < n.ClassLocation.StartByte || v.offset >= n.ClassLocation.EndByte {
		return
	}
	fqn := v.fc.Resolve(n.ClassName)
	if len(v.bindings.Lookup(fqn)) > 0 {
		v.sym = &refSymbol{abstractFQN: fqn}
		v.tokenLoc = n.ClassLocation
	}
}

// — Reference scanning —

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
			tree, err := phpnode.ParseBytes(src)
			if err != nil {
				return nil
			}
			defer tree.Close()

			rv := &refsVisitor{
				fc:     &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
				sym:    sym,
				path:   path,
				src:    src,
				models: models,
			}
			phpwalk.Walk(path, src, tree, rv)
			for _, loc := range rv.locations {
				locs = append(locs, toLSPLocation(loc))
			}
			return nil
		})
	}
	return locs
}

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

// — refsVisitor —

type refsVisitor struct {
	phpwalk.NullVisitor
	fc           *phputil.FileContext
	sym          *refSymbol
	path         string
	src          []byte
	models       *eloquent.ModelIndex
	encClass     phputil.FQN
	encMethod    *phpwalk.MethodInfo
	assignedVars map[string]phputil.FQN
	locations    []phputil.Location
}

func (v *refsVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *refsVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *refsVisitor) VisitClass(n phpwalk.ClassInfo) {
	v.encClass = v.fc.Resolve(n.NameText)
	v.encMethod = nil
}

func (v *refsVisitor) VisitClassMethod(n phpwalk.MethodInfo) {
	v.encMethod = &n
	v.assignedVars = collectAssignments(n, v.fc)
}

func (v *refsVisitor) VisitPropertyFetch(n phpwalk.PropertyFetchInfo) {
	if !v.sym.isEloquent() || n.PropName != v.sym.propName {
		return
	}
	var params []phpwalk.ParamInfo
	if v.encMethod != nil {
		params = v.encMethod.Params
	}
	modelFQN := resolveExprType(n.VarRaw, n.Src, v.encClass, params, v.assignedVars, v.fc, v.models)
	if modelFQN != v.sym.modelFQN {
		return
	}
	v.locations = append(v.locations, n.PropLocation)
}

func (v *refsVisitor) VisitClassConstFetch(n phpwalk.ClassConstFetchInfo) {
	if !v.sym.isContainer() || n.ConstName != "class" {
		return
	}
	if v.fc.Resolve(n.ClassName) != v.sym.abstractFQN {
		return
	}
	v.locations = append(v.locations, phpnode.FromNode(v.path, n.Raw))
}
