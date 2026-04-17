package lsp

import (
	"fmt"
	"sort"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
	"github.com/akyrey/laravel-ls/internal/phpparse"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

// Completion handles textDocument/completion for Eloquent property access.
// Returns a sorted []CompletionItem when the cursor is on a `$var->` expression
// whose LHS resolves to an indexed model.
func (s *Server) Completion(_ *glsp.Context, p *protocol.CompletionParams) (any, error) {
	s.mu.RLock()
	models := s.models
	s.mu.RUnlock()
	if models == nil {
		return nil, nil
	}

	src, err := s.docs.Read(p.TextDocument.URI)
	if err != nil {
		return nil, nil
	}

	path := URIToPath(p.TextDocument.URI)
	offset := positionToByteOffset(src, p.Position)

	items := eloquentCompletions(src, path, offset, models)
	if len(items) == 0 {
		return nil, nil
	}
	return items, nil
}

// eloquentCompletions is the pure-function core of Completion.
func eloquentCompletions(src []byte, path string, offset int, models *eloquent.ModelIndex) []protocol.CompletionItem {
	varVal := lhsBeforeArrow(src, offset)
	if varVal == "" {
		return nil
	}

	fqn := resolveVarTypeAtOffset(src, path, varVal, offset)
	if fqn == "" {
		return nil
	}
	cat := models.Lookup(fqn)
	if cat == nil {
		return nil
	}

	items := make([]protocol.CompletionItem, 0, len(cat.ByExposed))
	for name, attrs := range cat.ByExposed {
		items = append(items, makeCompletionItem(name, attrs, fqn))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return items
}

// lhsBeforeArrow scans backwards from offset to detect `$varName->` (with an
// optional partial property name at offset) and returns the variable token
// (e.g. "$user"). Returns "" when the pattern is not present.
func lhsBeforeArrow(src []byte, offset int) string {
	pos := offset
	// Skip any partial property name already typed after ->
	for pos > 0 && isIdentByte(src[pos-1]) {
		pos--
	}
	// Expect ->
	if pos < 2 || src[pos-1] != '>' || src[pos-2] != '-' {
		return ""
	}
	pos -= 2
	// Optional whitespace between variable and ->
	for pos > 0 && (src[pos-1] == ' ' || src[pos-1] == '\t') {
		pos--
	}
	// Collect the variable name chars (without $)
	end := pos
	for pos > 0 && isIdentByte(src[pos-1]) {
		pos--
	}
	if pos == 0 || src[pos-1] != '$' || pos == end {
		return ""
	}
	pos-- // include $
	return string(src[pos:end])
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_'
}

// resolveVarTypeAtOffset parses src and returns the model FQN for varVal at
// the given cursor offset. Does not require the models index — only uses the
// file context and method params/assignments for resolution.
func resolveVarTypeAtOffset(src []byte, path string, varVal string, offset int) phputil.FQN {
	root, err := phpparse.Bytes(src, path)
	if err != nil || root == nil {
		return ""
	}
	fc := &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)}
	v := &varTypeVisitor{fc: fc, offset: offset, varVal: varVal}
	traverser.NewTraverser(v).Traverse(root)
	return v.fqn
}

// varTypeVisitor resolves a single variable name to its model FQN at a given
// byte offset by tracking the enclosing class/method scope.
type varTypeVisitor struct {
	visitor.Null
	fc     *phputil.FileContext
	offset int
	varVal string

	encClass phputil.FQN
	fqn      phputil.FQN
}

func (v *varTypeVisitor) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *varTypeVisitor) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *varTypeVisitor) StmtGroupUse(n *ast.StmtGroupUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, phputil.NameToString(n.Prefix))
}

func (v *varTypeVisitor) StmtClass(n *ast.StmtClass) {
	pos := n.GetPosition()
	if pos == nil || pos.StartPos > v.offset || v.offset >= pos.EndPos {
		return
	}
	v.encClass = phputil.ClassNodeFQN(n.Name, v.fc)
}

func (v *varTypeVisitor) StmtClassMethod(n *ast.StmtClassMethod) {
	pos := n.GetPosition()
	if pos == nil || pos.StartPos > v.offset || v.offset >= pos.EndPos {
		return
	}
	if v.varVal == "$this" || v.varVal == "this" {
		v.fqn = v.encClass
		return
	}
	assigned := collectAssignments(n, v.fc)
	v.fqn = resolveVarFQN(v.varVal, n, assigned, v.fc)
}

// kindLabel maps AttributeKind to a short human-readable tag used in both
// completion details and hover text.
var kindLabel = map[eloquent.AttributeKind]string{
	eloquent.ModernAccessor:    "accessor",
	eloquent.LegacyAccessor:    "accessor",
	eloquent.LegacyMutator:     "mutator",
	eloquent.Relationship:      "relation",
	eloquent.FillableArray:     "fillable",
	eloquent.CastArray:         "cast",
	eloquent.AppendsArray:      "appends",
	eloquent.HiddenArray:       "hidden",
	eloquent.IdeHelperProperty: "property",
	eloquent.IdeHelperMethod:   "method",
}

// completionItemKind maps AttributeKind to an LSP CompletionItemKind.
func completionItemKind(k eloquent.AttributeKind) protocol.CompletionItemKind {
	switch k {
	case eloquent.ModernAccessor, eloquent.LegacyAccessor, eloquent.LegacyMutator,
		eloquent.IdeHelperMethod:
		return protocol.CompletionItemKindMethod
	case eloquent.Relationship:
		return protocol.CompletionItemKindReference
	case eloquent.IdeHelperProperty:
		return protocol.CompletionItemKindProperty
	default:
		return protocol.CompletionItemKindField
	}
}

// makeCompletionItem builds a single CompletionItem for an exposed attribute.
func makeCompletionItem(name string, attrs []eloquent.ModelAttribute, modelFQN phputil.FQN) protocol.CompletionItem {
	best := attrs[0]
	for _, a := range attrs[1:] {
		if a.Kind < best.Kind {
			best = a
		}
	}
	kind := completionItemKind(best.Kind)
	detail := fmt.Sprintf("%s  %s", kindLabel[best.Kind], modelFQN)
	return protocol.CompletionItem{
		Label:  name,
		Kind:   &kind,
		Detail: &detail,
	}
}
