package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

func newTestServer(bindings *container.BindingIndex, models *eloquent.ModelIndex) *Server {
	s := &Server{
		docs: newDocumentStore(),
		log:  commonlog.GetLogger("test"),
	}
	s.bindings = bindings
	s.models = models
	return s
}

func TestDocumentSymbol_ModelFile(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	s := newTestServer(bindings, models)
	uri := PathToURI(userPath)
	s.docs.Set(uri, src)

	result, err := s.DocumentSymbol(
		&glsp.Context{},
		&protocol.DocumentSymbolParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		},
	)
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for User.php")
	}

	syms, ok := result.([]protocol.DocumentSymbol)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}
	if len(syms) == 0 {
		t.Fatal("expected at least one symbol")
	}

	names := make(map[string]bool, len(syms))
	for _, sym := range syms {
		names[sym.Name] = true
	}
	for _, want := range []string{"email_address", "first_name"} {
		if !names[want] {
			t.Errorf("expected symbol %q in result; got: %v", want, syms)
		}
	}
}

func TestDocumentSymbol_NonModelFile(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	controllerPath := filepath.Join(modelsRoot, "UserController.php")
	src, err := os.ReadFile(controllerPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	s := newTestServer(bindings, models)
	uri := PathToURI(controllerPath)
	s.docs.Set(uri, src)

	result, err := s.DocumentSymbol(
		&glsp.Context{},
		&protocol.DocumentSymbolParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		},
	)
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for non-model file, got %v", result)
	}
}
