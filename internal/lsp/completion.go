package lsp

import (
	"fmt"
	"sort"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/indexer/strindex"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// Completion handles textDocument/completion: Eloquent property access after
// `->`, and Laravel string references inside config()/view()/route() calls.
func (s *Server) Completion(_ *glsp.Context, p *protocol.CompletionParams) (any, error) {
	s.mu.RLock()
	models, strs := s.models, s.strIndex
	s.mu.RUnlock()

	src, err := s.docs.Read(p.TextDocument.URI)
	if err != nil {
		return nil, nil
	}

	path := URIToPath(p.TextDocument.URI)
	offset := positionToByteOffset(src, p.Position)

	var items []protocol.CompletionItem
	if models != nil {
		items = eloquentCompletions(src, path, offset, models)
	}
	if len(items) == 0 && strs != nil {
		items = stringRefCompletions(src, offset, strs)
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items, nil
}

// stringRefCompletions offers config keys, view names, or route names when
// the cursor sits inside the first string argument of the matching helper
// call. The editor filters against what has been typed so far.
func stringRefCompletions(src []byte, offset int, strs *strindex.Index) []protocol.CompletionItem {
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return nil
	}
	defer tree.Close()

	v := &stringRefVisitor{offset: offset, strs: strs}
	phpwalk.Walk("", src, tree, v)
	if v.targets == nil {
		return nil
	}

	kind := protocol.CompletionItemKindValue
	items := make([]protocol.CompletionItem, 0, len(v.targets))
	for name := range v.targets {
		detail := v.fnName
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   &kind,
			Detail: &detail,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return items
}

// stringRefVisitor finds the innermost config/view/route call whose first
// string argument contains the cursor.
type stringRefVisitor struct {
	phpwalk.NullVisitor
	offset  int
	strs    *strindex.Index
	fnName  string
	targets map[string]phputil.Location
}

func (v *stringRefVisitor) VisitFunctionCall(n phpwalk.FunctionCallInfo) {
	targets := stringRefTargets(v.strs, n.Name)
	if targets == nil || len(n.Args) == 0 {
		return
	}
	arg := n.Args[0]
	if arg.Kind() != "string" || !cursorOnNode(v.offset, arg) {
		return
	}
	v.fnName = n.Name
	v.targets = targets
}

// eloquentCompletions is the pure-function core of Completion. It resolves
// the variable's model type and then follows any relationship hops in the
// chain before the cursor ($user->posts-> completes Post's attributes).
func eloquentCompletions(src []byte, path string, offset int, models *eloquent.ModelIndex) []protocol.CompletionItem {
	varVal, segs := chainBeforeArrow(src, offset)
	if varVal == "" {
		return nil
	}

	fqn := resolveVarTypeAtOffset(src, path, varVal, offset)
	if fqn == "" {
		return nil
	}
	for _, seg := range segs {
		cat := models.Lookup(fqn)
		if cat == nil {
			return nil
		}
		fqn = ""
		for _, a := range cat.ByExposed[seg] {
			if a.RelatedFQN != "" {
				fqn = a.RelatedFQN
				break
			}
		}
		if fqn == "" {
			return nil
		}
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
// returns the variable token. Returns "" when the pattern is not present or
// when the access is chained ($a->b->).
func lhsBeforeArrow(src []byte, offset int) string {
	varVal, segs := chainBeforeArrow(src, offset)
	if len(segs) > 0 {
		return ""
	}
	return varVal
}

// chainBeforeArrow scans backwards from offset to detect
// `$varName->seg1->seg2-> ...` and returns the variable token (with its $
// sigil) plus the property segments between it and the cursor, in source
// order. Returns ("", nil) when the text before the cursor is not a simple
// property-access chain (e.g. a method-call chain).
func chainBeforeArrow(src []byte, offset int) (string, []string) {
	pos := offset
	// Skip any partially typed identifier after the trigger.
	for pos > 0 && isIdentByte(src[pos-1]) {
		pos--
	}
	var segs []string
	for {
		if pos < 2 || src[pos-1] != '>' || src[pos-2] != '-' {
			return "", nil
		}
		pos -= 2
		for pos > 0 && (src[pos-1] == ' ' || src[pos-1] == '\t') {
			pos--
		}
		end := pos
		for pos > 0 && isIdentByte(src[pos-1]) {
			pos--
		}
		if pos == end {
			return "", nil // not an identifier (e.g. `)` from a call chain)
		}
		if pos > 0 && src[pos-1] == '$' {
			return string(src[pos-1 : end]), segs
		}
		segs = append([]string{string(src[pos:end])}, segs...)
		for pos > 0 && (src[pos-1] == ' ' || src[pos-1] == '\t') {
			pos--
		}
	}
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
