package lsp

import (
	"fmt"
	"sort"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// Completion handles textDocument/completion for Eloquent property access.
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

// lhsBeforeArrow scans backwards from offset to detect `$varName->` and
// returns the variable token. Returns "" when the pattern is not present.
func lhsBeforeArrow(src []byte, offset int) string {
	pos := offset
	for pos > 0 && isIdentByte(src[pos-1]) {
		pos--
	}
	if pos < 2 || src[pos-1] != '>' || src[pos-2] != '-' {
		return ""
	}
	pos -= 2
	for pos > 0 && (src[pos-1] == ' ' || src[pos-1] == '\t') {
		pos--
	}
	end := pos
	for pos > 0 && isIdentByte(src[pos-1]) {
		pos--
	}
	if pos == 0 || src[pos-1] != '$' || pos == end {
		return ""
	}
	pos--
	return string(src[pos:end])
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_'
}

// resolveVarTypeAtOffset parses src and returns the model FQN for varVal at
// the given cursor offset.
func resolveVarTypeAtOffset(src []byte, path string, varVal string, offset int) phputil.FQN {
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return ""
	}
	defer tree.Close()

	v := &varTypeVisitor{
		fc:     &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
		offset: offset,
		varVal: varVal,
	}
	phpwalk.Walk(v.fc.Path, src, tree, v)
	return v.fqn
}

// varTypeVisitor resolves a single variable name to its model FQN at a given
// byte offset by tracking the enclosing class/method scope.
type varTypeVisitor struct {
	phpwalk.NullVisitor
	fc     *phputil.FileContext
	offset int
	varVal string

	encClass phputil.FQN
	fqn      phputil.FQN
}

func (v *varTypeVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *varTypeVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *varTypeVisitor) VisitClass(n phpwalk.ClassInfo) {
	if !cursorOnNode(v.offset, n.Raw) {
		return
	}
	v.encClass = v.fc.Resolve(n.NameText)
}

func (v *varTypeVisitor) VisitClassMethod(n phpwalk.MethodInfo) {
	if v.offset < n.StartByte || v.offset >= n.EndByte {
		return
	}
	if v.varVal == "$this" || v.varVal == "this" {
		v.fqn = v.encClass
		return
	}
	assigned := collectAssignments(n, v.fc)
	v.fqn = resolveVarFQN(v.varVal, n.Params, assigned, v.fc)
}

// kindLabel maps AttributeKind to a short human-readable tag.
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
