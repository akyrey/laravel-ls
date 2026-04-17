package lsp

import (
	"sort"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
	"github.com/akyrey/laravel-ls/internal/phpparse"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

// Definition handles textDocument/definition requests.
// Returns nil when indexes are not ready, on parse failure, or when no match
// is found — letting sibling LSPs (Intelephense, Phpactor) answer.
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
// LSP client. It parses src, runs the definition visitor at offset, and returns
// all matching locations.
func findDefinition(
	src []byte,
	path string,
	offset int,
	bindings *container.BindingIndex,
	models *eloquent.ModelIndex,
) []protocol.Location {
	root, err := phpparse.Bytes(src, path)
	if err != nil || root == nil {
		return nil
	}
	fc := &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)}
	dv := &defVisitor{
		fc:       fc,
		offset:   offset,
		bindings: bindings,
		models:   models,
	}
	traverser.NewTraverser(dv).Traverse(root)
	return dv.locations
}

// defVisitor is a single-pass AST visitor that collects definition jump targets.
// It tracks the enclosing class/method as it traverses (DFS pre-order), then
// matches cursor-containing nodes for the two supported lookup flows.
type defVisitor struct {
	visitor.Null
	fc       *phputil.FileContext
	offset   int
	bindings *container.BindingIndex
	models   *eloquent.ModelIndex

	// traversal state: set when cursor falls within the containing node
	encClass     phputil.FQN
	encMethod    *ast.StmtClassMethod
	assignedVars map[string]phputil.FQN

	locations []protocol.Location
}

// — File context builders (mirrors scan.go) —

func (v *defVisitor) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *defVisitor) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *defVisitor) StmtGroupUse(n *ast.StmtGroupUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, phputil.NameToString(n.Prefix))
}

// — Scope trackers —

func (v *defVisitor) StmtClass(n *ast.StmtClass) {
	pos := n.GetPosition()
	if pos == nil || pos.StartPos > v.offset || v.offset >= pos.EndPos {
		return
	}
	v.encClass = phputil.ClassNodeFQN(n.Name, v.fc)
}

func (v *defVisitor) StmtClassMethod(n *ast.StmtClassMethod) {
	pos := n.GetPosition()
	if pos == nil || pos.StartPos > v.offset || v.offset >= pos.EndPos {
		return
	}
	v.encMethod = n
	v.assignedVars = collectAssignments(n, v.fc)
}

// — Flow 1: Service-container lookup —
//
// All four handlers below resolve a class name under the cursor to its FQN and
// delegate to appendContainerLocations. They are no-ops when the FQN has no
// container binding, so they are safe to fire on any class reference.

// ExprClassConstFetch handles `X::class` when cursor is on the class name.
func (v *defVisitor) ExprClassConstFetch(n *ast.ExprClassConstFetch) {
	constID, ok := n.Const.(*ast.Identifier)
	if !ok || string(constID.Value) != "class" {
		return
	}
	classPos := n.Class.GetPosition()
	if classPos == nil || v.offset < classPos.StartPos || v.offset >= classPos.EndPos {
		return
	}
	fqn := v.fc.Resolve(phputil.NameToString(n.Class))
	v.appendContainerLocations(fqn)
}

// ExprNew handles `new ClassName(...)` when cursor is on the class name.
func (v *defVisitor) ExprNew(n *ast.ExprNew) {
	classPos := n.Class.GetPosition()
	if classPos == nil || v.offset < classPos.StartPos || v.offset >= classPos.EndPos {
		return
	}
	fqn := v.fc.Resolve(phputil.NameToString(n.Class))
	v.appendContainerLocations(fqn)
}

// ExprStaticCall handles `ClassName::method(...)` when cursor is on the class name.
func (v *defVisitor) ExprStaticCall(n *ast.ExprStaticCall) {
	classPos := n.Class.GetPosition()
	if classPos == nil || v.offset < classPos.StartPos || v.offset >= classPos.EndPos {
		return
	}
	fqn := v.fc.Resolve(phputil.NameToString(n.Class))
	v.appendContainerLocations(fqn)
}

// ExprInstanceOf handles `$x instanceof ClassName` when cursor is on the class name.
func (v *defVisitor) ExprInstanceOf(n *ast.ExprInstanceOf) {
	classPos := n.Class.GetPosition()
	if classPos == nil || v.offset < classPos.StartPos || v.offset >= classPos.EndPos {
		return
	}
	fqn := v.fc.Resolve(phputil.NameToString(n.Class))
	v.appendContainerLocations(fqn)
}

// — Flow 2: Eloquent model attribute lookup —

// ExprPropertyFetch handles `$expr->propName` when cursor is on propName.
func (v *defVisitor) ExprPropertyFetch(n *ast.ExprPropertyFetch) {
	prop, ok := n.Prop.(*ast.Identifier)
	if !ok {
		return
	}
	propPos := prop.GetPosition()
	if propPos == nil || v.offset < propPos.StartPos || v.offset >= propPos.EndPos {
		return
	}
	propName := string(prop.Value)

	lhsVar, ok := n.Var.(*ast.ExprVariable)
	if !ok {
		return // chained access (e.g. $a->b->c): deferred to v0.2
	}
	lhsID, ok := lhsVar.Name.(*ast.Identifier)
	if !ok {
		return // dynamic variable ($$var): unsupported
	}
	varVal := string(lhsID.Value)

	var modelFQN phputil.FQN
	if varVal == "$this" || varVal == "this" {
		modelFQN = v.encClass
	} else {
		modelFQN = resolveVarFQN(varVal, v.encMethod, v.assignedVars, v.fc)
	}
	if modelFQN == "" {
		return
	}
	v.appendEloquentLocations(modelFQN, propName)
}

// — Location appenders —

func (v *defVisitor) appendContainerLocations(fqn phputil.FQN) {
	if fqn == "" {
		return
	}
	for _, b := range v.bindings.Lookup(fqn) {
		if b.Location.Zero() {
			continue
		}
		v.locations = append(v.locations, toLSPLocation(b.Location))
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
	// Sort by AttributeKind: ModernAccessor (0) < LegacyAccessor (1) < ... < array entries.
	sorted := make([]eloquent.ModelAttribute, len(attrs))
	copy(sorted, attrs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Kind < sorted[j].Kind
	})
	for _, a := range sorted {
		if a.Location.Zero() {
			continue
		}
		v.locations = append(v.locations, toLSPLocation(a.Location))
	}
}

// resolveParamType looks up varVal (e.g. "$user") in method's parameter list
// and returns the resolved FQN of its type hint, or "" if not found.
func resolveParamType(varVal string, method *ast.StmtClassMethod, fc *phputil.FileContext) phputil.FQN {
	if method == nil {
		return ""
	}
	for _, p := range method.Params {
		param, ok := p.(*ast.Parameter)
		if !ok || param.Type == nil || param.Var == nil {
			continue
		}
		pVar, ok := param.Var.(*ast.ExprVariable)
		if !ok {
			continue
		}
		pID, ok := pVar.Name.(*ast.Identifier)
		if !ok {
			continue
		}
		if string(pID.Value) != varVal {
			continue
		}
		typeName := phputil.NameToString(unwrapNullable(param.Type))
		if typeName == "" {
			continue
		}
		return fc.Resolve(typeName)
	}
	return ""
}

// unwrapNullable strips an *ast.Nullable wrapper, returning the inner type.
func unwrapNullable(t ast.Vertex) ast.Vertex {
	if n, ok := t.(*ast.Nullable); ok {
		return n.Expr
	}
	return t
}
