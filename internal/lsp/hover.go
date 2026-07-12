package lsp

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/indexer/strindex"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// Hover handles textDocument/hover. Returns a Markdown description of the
// Eloquent attribute, container binding, or Laravel string reference
// (config()/view()/route()/env()) under the cursor.
func (s *Server) Hover(_ *glsp.Context, p *protocol.HoverParams) (*protocol.Hover, error) {
	s.mu.RLock()
	bindings, models, strs, root := s.bindings, s.models, s.strIndex, s.root
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

	if sym := identifySymbol(src, path, offset, bindings, models); sym != nil {
		if md := hoverMarkdown(sym, bindings, models); md != "" {
			return markdownHover(md), nil
		}
	}

	if ref := identifyStringRef(src, path, offset, strs); ref != nil {
		if md := stringRefHoverMD(ref, root, s.docs); md != "" {
			return markdownHover(md), nil
		}
	}

	return nil, nil
}

func markdownHover(md string) *protocol.Hover {
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: md,
		},
	}
}

// hoverMarkdown builds the Markdown body for a refSymbol.
func hoverMarkdown(sym *refSymbol, bindings *container.BindingIndex, models *eloquent.ModelIndex) string {
	if sym.isEloquent() {
		return eloquentHoverMD(sym, models)
	}
	if sym.isContainer() {
		return containerHoverMD(sym, bindings)
	}
	return ""
}

// eloquentHoverMD renders hover text for an Eloquent attribute.
//
//	**`email_address`**
//
//	- accessor via `emailAddress()`
//	- fillable
//
//	_Model: `App\Models\User`_
func eloquentHoverMD(sym *refSymbol, models *eloquent.ModelIndex) string {
	cat := models.Lookup(sym.modelFQN)
	if cat == nil {
		return ""
	}
	attrs := cat.ByExposed[sym.propName]
	if len(attrs) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**`%s`**\n\n", sym.propName)

	seen := make(map[eloquent.AttributeKind]bool)
	for _, a := range attrs {
		if seen[a.Kind] {
			continue
		}
		seen[a.Kind] = true
		label := kindLabel[a.Kind]
		if a.MethodName != "" {
			fmt.Fprintf(&sb, "- %s via `%s()`\n", label, a.MethodName)
		} else {
			fmt.Fprintf(&sb, "- %s\n", label)
		}
	}

	fmt.Fprintf(&sb, "\n_Model: `%s`_", sym.modelFQN)
	return sb.String()
}

// containerHoverMD renders hover text for a container binding.
//
//	**`App\Contracts\PaymentGateway`**
//
//	- bound to `App\Services\StripeGateway`
func containerHoverMD(sym *refSymbol, bindings *container.BindingIndex) string {
	bs := bindings.Lookup(sym.abstractFQN)
	if len(bs) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**`%s`**\n\n", sym.abstractFQN)
	for _, b := range bs {
		if b.Concrete != "" {
			fmt.Fprintf(&sb, "- bound to `%s`\n", b.Concrete)
		}
	}
	return sb.String()
}

// — Laravel string references —

// stringRef describes a config()/view()/route()/env() call whose first
// argument (a string literal) contains the cursor.
type stringRef struct {
	fnName string // "config", "view", "route", "env"
	key    string
	loc    phputil.Location // zero when key is unresolved
	found  bool
}

// identifyStringRef finds the string-helper call under offset, if any.
func identifyStringRef(src []byte, path string, offset int, strs *strindex.Index) *stringRef {
	if strs == nil {
		return nil
	}
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return nil
	}
	defer tree.Close()

	v := &stringRefFinder{offset: offset, strs: strs}
	phpwalk.Walk(path, src, tree, v)
	return v.ref
}

// stringRefFinder locates the config/view/route/env call whose first string
// argument contains the cursor, and resolves its key against the index.
type stringRefFinder struct {
	phpwalk.NullVisitor
	offset int
	strs   *strindex.Index
	ref    *stringRef
}

func (v *stringRefFinder) VisitFunctionCall(n phpwalk.FunctionCallInfo) {
	targets := stringRefTargets(v.strs, n.Name)
	if targets == nil || len(n.Args) == 0 {
		return
	}
	arg := n.Args[0]
	if !phpwalk.IsStringLiteral(arg) || !cursorOnNode(v.offset, arg) {
		return
	}
	key := phpwalk.StringValue(arg, n.Src)
	loc, ok := targets[key]
	v.ref = &stringRef{fnName: n.Name, key: key, loc: loc, found: ok}
}

// stringRefHoverMD renders hover text for a resolved string reference.
//
//	**`app.name`**
//
//	Config key — defined in `config/app.php`
func stringRefHoverMD(ref *stringRef, root string, docs *DocumentStore) string {
	if !ref.found {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**`%s`**\n\n%s — defined in `%s`",
		ref.key, stringRefLabel[ref.fnName], relativeToRoot(root, ref.loc.Path))

	if ref.fnName == "env" {
		if val, ok := envValueAt(docs, ref.loc); ok {
			fmt.Fprintf(&sb, "\n\nValue: `%s`", val)
		}
	}
	return sb.String()
}

// relativeToRoot renders path relative to root for display, falling back to
// the absolute path when root is unknown or path lies outside it.
func relativeToRoot(root, path string) string {
	if root == "" {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

// envValueAt reads the value portion of the KEY=value line at loc, through
// the document store so unsaved edits to an open .env buffer are reflected.
func envValueAt(docs *DocumentStore, loc phputil.Location) (string, bool) {
	src, err := docs.Read(PathToURI(loc.Path))
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(src), "\n")
	if loc.StartLine < 1 || loc.StartLine > len(lines) {
		return "", false
	}
	line := strings.TrimSuffix(lines[loc.StartLine-1], "\r")
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", false
	}
	return strings.TrimSpace(line[eq+1:]), true
}
