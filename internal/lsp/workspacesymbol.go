package lsp

import (
	"sort"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
)

// WorkspaceSymbol handles workspace/symbol requests. Unlike DocumentSymbol,
// which is scoped to one open file, this searches every indexed Eloquent
// model attribute and container binding project-wide. An empty query (which
// clients may send to request everything) returns every known symbol.
func (s *Server) WorkspaceSymbol(_ *glsp.Context, p *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	s.mu.RLock()
	models, bindings, docs := s.models, s.bindings, s.docs
	s.mu.RUnlock()
	if models == nil && bindings == nil {
		return nil, nil
	}

	query := strings.ToLower(p.Query)
	var syms []protocol.SymbolInformation
	if models != nil {
		syms = append(syms, modelWorkspaceSymbols(models, query, docs)...)
	}
	if bindings != nil {
		syms = append(syms, bindingWorkspaceSymbols(bindings, query, docs)...)
	}

	sort.Slice(syms, func(i, j int) bool {
		if syms[i].Name != syms[j].Name {
			return syms[i].Name < syms[j].Name
		}
		return containerNameOf(syms[i]) < containerNameOf(syms[j])
	})

	if len(syms) == 0 {
		return nil, nil
	}
	return syms, nil
}

func containerNameOf(s protocol.SymbolInformation) string {
	if s.ContainerName == nil {
		return ""
	}
	return *s.ContainerName
}

// matchesQuery reports whether query is a case-insensitive substring of any
// candidate. An empty query matches everything.
func matchesQuery(query string, candidates ...string) bool {
	if query == "" {
		return true
	}
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c), query) {
			return true
		}
	}
	return false
}

// modelWorkspaceSymbols returns one symbol per exposed Eloquent attribute
// across every indexed model, picking the highest-ranked entry per name —
// the same ranking DocumentSymbol uses — and skipping entries with no jump
// target (e.g. ide-helper-only entries).
func modelWorkspaceSymbols(models *eloquent.ModelIndex, query string, docs *DocumentStore) []protocol.SymbolInformation {
	var out []protocol.SymbolInformation
	for _, cat := range models.All() {
		className := string(cat.Class)
		for name, attrs := range cat.ByExposed {
			if len(attrs) == 0 || !matchesQuery(query, name, className) {
				continue
			}
			best := attrs[0]
			for _, a := range attrs[1:] {
				if a.Kind < best.Kind {
					best = a
				}
			}
			if best.Location.Zero() {
				continue
			}
			containerName := className
			out = append(out, protocol.SymbolInformation{
				Name:          name,
				Kind:          kindSymbol(best.Kind),
				Location:      toLSPLocation(best.Location, docs),
				ContainerName: &containerName,
			})
		}
	}
	return out
}

// bindingWorkspaceSymbols returns one symbol per service-container binding
// that has a known jump target (the concrete class's declaration).
func bindingWorkspaceSymbols(bindings *container.BindingIndex, query string, docs *DocumentStore) []protocol.SymbolInformation {
	var out []protocol.SymbolInformation
	for _, b := range bindings.All() {
		if b.Location.Zero() || !matchesQuery(query, string(b.Abstract), string(b.Concrete)) {
			continue
		}
		concrete := string(b.Concrete)
		out = append(out, protocol.SymbolInformation{
			Name:          string(b.Abstract),
			Kind:          protocol.SymbolKindInterface,
			Location:      toLSPLocation(b.Location, docs),
			ContainerName: &concrete,
		})
	}
	return out
}
