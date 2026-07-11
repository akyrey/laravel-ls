package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
)

func symbolNames(syms []protocol.SymbolInformation) map[string]bool {
	out := make(map[string]bool, len(syms))
	for _, s := range syms {
		out[s.Name] = true
	}
	return out
}

func TestWorkspaceSymbol_EmptyQueryReturnsEverything(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindingsRoot := filepath.Join("..", "..", "testdata", "bindings")
	bindings, err := container.Walk(bindingsRoot, []string{"."})
	if err != nil {
		t.Fatalf("container.Walk: %v", err)
	}

	s := newTestServer(bindings, models)
	result, err := s.WorkspaceSymbol(&glsp.Context{}, &protocol.WorkspaceSymbolParams{Query: ""})
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected symbols for empty query, got none")
	}

	names := symbolNames(result)
	// A model attribute...
	if !names["email_address"] {
		t.Errorf("expected email_address in results, got %v", names)
	}
	// ...and a container binding, both surfaced by one query.
	if !names["App\\Contracts\\PaymentGateway"] {
		t.Errorf("expected App\\Contracts\\PaymentGateway in results, got %v", names)
	}
}

func TestWorkspaceSymbol_FiltersByQuery(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	s := newTestServer(bindings, models)
	result, err := s.WorkspaceSymbol(&glsp.Context{}, &protocol.WorkspaceSymbolParams{Query: "email"})
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected at least one match for query 'email'")
	}
	for _, sym := range result {
		if !strings.Contains(strings.ToLower(sym.Name), "email") {
			t.Errorf("unexpected match %q for query 'email'", sym.Name)
		}
	}
}

func TestWorkspaceSymbol_QueryMatchesContainerClassName(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	s := newTestServer(bindings, models)
	// "Post" is a model class name, not an attribute name — should still
	// surface every attribute belonging to the Post model.
	result, err := s.WorkspaceSymbol(&glsp.Context{}, &protocol.WorkspaceSymbolParams{Query: "Post"})
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	names := symbolNames(result)
	if !names["slug_url"] {
		t.Errorf("expected slug_url (a Post attribute) when querying by model class name, got %v", names)
	}
}

func TestWorkspaceSymbol_NilIndexesReturnNil(t *testing.T) {
	s := newTestServer(nil, nil)
	result, err := s.WorkspaceSymbol(&glsp.Context{}, &protocol.WorkspaceSymbolParams{Query: ""})
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when both indexes are nil, got %v", result)
	}
}

func TestWorkspaceSymbol_NoMatchesReturnsNil(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	s := newTestServer(bindings, models)
	result, err := s.WorkspaceSymbol(&glsp.Context{}, &protocol.WorkspaceSymbolParams{Query: "definitely-not-a-real-symbol"})
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for no matches, got %v", result)
	}
}
