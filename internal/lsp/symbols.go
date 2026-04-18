package lsp

import (
	"sort"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

// kindSymbol maps an AttributeKind to an LSP SymbolKind.
func kindSymbol(k eloquent.AttributeKind) protocol.SymbolKind {
	switch k {
	case eloquent.ModernAccessor, eloquent.LegacyAccessor, eloquent.LegacyMutator:
		return protocol.SymbolKindMethod
	default:
		return protocol.SymbolKindProperty
	}
}

// kindDetail returns a short human-readable string for the attribute kind.
func kindDetail(k eloquent.AttributeKind) string {
	switch k {
	case eloquent.ModernAccessor:
		return "accessor"
	case eloquent.LegacyAccessor:
		return "get accessor"
	case eloquent.LegacyMutator:
		return "set mutator"
	case eloquent.FillableArray:
		return "$fillable"
	case eloquent.CastArray:
		return "$casts"
	case eloquent.AppendsArray:
		return "$appends"
	case eloquent.HiddenArray:
		return "$hidden"
	default:
		return ""
	}
}

// DocumentSymbol handles textDocument/documentSymbol. For Eloquent model files
// it returns the model's exposed attributes as document symbols. For all other
// files it returns nil so the editor falls back to other providers.
func (s *Server) DocumentSymbol(_ *glsp.Context, p *protocol.DocumentSymbolParams) (any, error) {
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

	filePath := URIToPath(p.TextDocument.URI)

	// Find the model whose source file matches this URI.
	var cat *eloquent.ModelCatalog
	for _, c := range models.All() {
		if c.Path == filePath {
			cat = c
			break
		}
	}
	if cat == nil {
		return nil, nil
	}

	// Collect deduplicated exposed names, picking the highest-ranked entry.
	type entry struct {
		name string
		attr eloquent.ModelAttribute
	}
	seen := map[string]eloquent.ModelAttribute{}
	for name, attrs := range cat.ByExposed {
		if len(attrs) == 0 {
			continue
		}
		seen[name] = attrs[0] // attrs are ranked; first is highest priority
	}

	// Sort by name for stable output.
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []protocol.DocumentSymbol
	for _, name := range names {
		attr := seen[name]
		if attr.Location.StartByte < 0 || attr.Location.StartByte >= attr.Location.EndByte {
			continue
		}
		rng := toLSPRange(attr.Location, src)
		detail := kindDetail(attr.Kind)
		sym := protocol.DocumentSymbol{
			Name:           name,
			Detail:         &detail,
			Kind:           kindSymbol(attr.Kind),
			Range:          rng,
			SelectionRange: rng,
		}
		out = append(out, sym)
	}

	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

