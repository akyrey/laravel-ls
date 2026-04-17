package lsp

import (
	"fmt"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

// Hover handles textDocument/hover. Returns a Markdown description of the
// Eloquent attribute or container binding under the cursor.
func (s *Server) Hover(_ *glsp.Context, p *protocol.HoverParams) (*protocol.Hover, error) {
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

	sym := identifySymbol(src, path, offset, bindings, models)
	if sym == nil {
		return nil, nil
	}

	md := hoverMarkdown(sym, bindings, models)
	if md == "" {
		return nil, nil
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: md,
		},
	}, nil
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
