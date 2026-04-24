package lsp

import (
	"sort"

	ts "github.com/tree-sitter/go-tree-sitter"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// Definition handles textDocument/definition requests.
func (s *Server) Definition(_ *glsp.Context, p *protocol.DefinitionParams) (any, error) {
	s.mu.RLock()
	bindings, models := s.bindings, s.models
	s.mu.RUnlock()
	if bindings == nil || models == nil {
		return nil, nil
	}

	src, err := s.docs.Read(p.TextDocument.URI)
	if err != nil {
		return nil, nil
	}

	path := URIToPath(p.TextDocument.URI)
	offset := positionToByteOffset(src, p.Position)
	locs := findDefinition(src, path, offset, bindings, models)
	if len(locs) == 0 {
		return nil, nil
	}
	return locs, nil
}

// findDefinition is the pure-function core of Definition, testable without an
// LSP client.
func findDefinition(
	src []byte,
	path string,
	offset int,
	bindings *container.BindingIndex,
	models *eloquent.ModelIndex,
) []protocol.Location {
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return nil
	}
	defer tree.Close()

	dv := &defVisitor{
		src:      src,
		path:     path,
		fc:       &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
		offset:   offset,
		bindings: bindings,
		models:   models,
	}
	phpwalk.Walk(path, src, tree, dv)
	return dv.locations
}

// defVisitor is a single-pass visitor that collects definition jump targets.
type defVisitor struct {
	phpwalk.NullVisitor
	src      []byte
	path     string
	fc       *phputil.FileContext
	offset   int
	bindings *container.BindingIndex
	models   *eloquent.ModelIndex

	encClass     phputil.FQN
	encMethod    *phpwalk.MethodInfo
	assignedVars map[string]phputil.FQN

	locations []protocol.Location
}

func (v *defVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *defVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *defVisitor) VisitClass(n phpwalk.ClassInfo) {
	if v.offset < int(n.Raw.StartByte()) || v.offset >= int(n.Raw.EndByte()) {
		return
	}
	v.encClass = v.fc.Resolve(n.NameText)
}

func (v *defVisitor) VisitClassMethod(n phpwalk.MethodInfo) {
	if v.offset < n.StartByte || v.offset >= n.EndByte {
		return
	}
	v.encMethod = &n
	v.assignedVars = collectAssignments(n, v.fc)
}

// — Flow 1: Service-container lookup —

func (v *defVisitor) VisitClassConstFetch(n phpwalk.ClassConstFetchInfo) {
	if n.ConstName != "class" {
		return
	}
	if v.offset < n.ClassLocation.StartByte || v.offset >= n.ClassLocation.EndByte {
		return
	}
	fqn := v.fc.Resolve(n.ClassName)
	v.appendContainerLocations(fqn)
}

func (v *defVisitor) VisitNew(n phpwalk.NewExprInfo) {
	if v.offset < n.ClassLocation.StartByte || v.offset >= n.ClassLocation.EndByte {
		return
	}
	fqn := v.fc.Resolve(n.ClassName)
	v.appendContainerLocations(fqn)
}

func (v *defVisitor) VisitStaticCall(n phpwalk.StaticCallInfo) {
	if v.offset < n.ClassLocation.StartByte || v.offset >= n.ClassLocation.EndByte {
		return
	}
	fqn := v.fc.Resolve(n.ClassName)
	v.appendContainerLocations(fqn)
}

func (v *defVisitor) VisitInstanceOf(n phpwalk.InstanceOfInfo) {
	if v.offset < n.ClassLocation.StartByte || v.offset >= n.ClassLocation.EndByte {
		return
	}
	fqn := v.fc.Resolve(n.ClassName)
	v.appendContainerLocations(fqn)
}

// — Flow 2: Eloquent model attribute lookup —

func (v *defVisitor) VisitPropertyFetch(n phpwalk.PropertyFetchInfo) {
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
	v.appendEloquentLocations(modelFQN, n.PropName)
}

// — Location appenders —

func (v *defVisitor) appendContainerLocations(fqn phputil.FQN) {
	if fqn == "" {
		return
	}
	for _, b := range v.bindings.Lookup(fqn) {
		if !b.Location.Zero() {
			v.locations = append(v.locations, toLSPLocation(b.Location))
		}
	}
}

func (v *defVisitor) appendEloquentLocations(modelFQN phputil.FQN, propName string) {
	cat := v.models.Lookup(modelFQN)
	if cat == nil {
		return
	}
	attrs := cat.ByExposed[propName]
	if len(attrs) == 0 {
		return
	}
	sorted := make([]eloquent.ModelAttribute, len(attrs))
	copy(sorted, attrs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Kind < sorted[j].Kind
	})
	for _, a := range sorted {
		if !a.Location.Zero() {
			v.locations = append(v.locations, toLSPLocation(a.Location))
		}
	}
}

// cursorOnNode returns true when offset falls within the byte range of n.
// Used for nodes whose range we need to check inline rather than via Info fields.
func cursorOnNode(offset int, n *ts.Node) bool {
	return n != nil && offset >= int(n.StartByte()) && offset < int(n.EndByte())
}
