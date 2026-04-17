package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

func TestReferences_EloquentFromPropertyAccess(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	bindings := container.NewBindingIndex()
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "email_address" in "$this->email_address" (the return statement)
	needle := []byte("'hi ' . $this->email_address")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found in fixture")
	}
	offset := idx + len("'hi ' . $this->")

	sym := identifySymbol(src, userPath, offset, bindings, models)
	if sym == nil {
		t.Fatal("identifySymbol returned nil")
	}
	if !sym.isEloquent() {
		t.Fatalf("expected Eloquent symbol, got %+v", sym)
	}
	if sym.propName != "email_address" {
		t.Errorf("want propName=email_address, got %q", sym.propName)
	}

	// scanReferences: scan inside testdata/models (using User.php itself as root)
	docs := newDocumentStore()
	docs.Set(PathToURI(userPath), src)
	locs := scanReferences(modelsRoot, []string{"."}, sym, docs, models)

	if len(locs) == 0 {
		t.Fatal("scanReferences returned no locations")
	}
	// Should find the $this->email_address access in greet()
	gotFile := filepath.Base(URIToPath(locs[0].URI))
	if gotFile != "User.php" {
		t.Errorf("want User.php, got %s", gotFile)
	}
}

func TestReferences_EloquentFromMethodName(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	bindings := container.NewBindingIndex()
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "emailAddress" (the method name in its declaration)
	needle := []byte("function emailAddress()")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("function emailAddress() not found in fixture")
	}
	offset := idx + len("function ") // on 'e' of emailAddress

	sym := identifySymbol(src, userPath, offset, bindings, models)
	if sym == nil {
		t.Fatal("identifySymbol returned nil")
	}
	if !sym.isEloquent() {
		t.Fatalf("expected Eloquent symbol, got %+v", sym)
	}
	if sym.propName != "email_address" {
		t.Errorf("want propName=email_address, got %q", sym.propName)
	}
}

func TestReferences_ContainerFromClassConst(t *testing.T) {
	bindingsRoot := filepath.Join("..", "..", "testdata", "bindings")
	bindings, err := container.Walk(bindingsRoot, []string{"."})
	if err != nil {
		t.Fatalf("container.Walk: %v", err)
	}
	models := eloquent.NewModelIndex()

	spPath := filepath.Join(bindingsRoot, "AppServiceProvider.php")
	src, err := os.ReadFile(spPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cursor on "PaymentGateway" in "PaymentGateway::class"
	needle := []byte("PaymentGateway::class")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("PaymentGateway::class not found in fixture")
	}
	offset := idx // on 'P'

	sym := identifySymbol(src, spPath, offset, bindings, models)
	if sym == nil {
		t.Fatal("identifySymbol returned nil")
	}
	if !sym.isContainer() {
		t.Fatalf("expected Container symbol, got %+v", sym)
	}

	// scanReferences over the bindings testdata directory
	docs := newDocumentStore()
	docs.Set(PathToURI(spPath), src)
	locs := scanReferences(bindingsRoot, []string{"."}, sym, docs, models)

	// The AppServiceProvider itself uses PaymentGateway::class as both
	// the abstract and potentially in other service providers.
	if len(locs) == 0 {
		t.Fatal("scanReferences returned no locations")
	}
}

func TestReferences_NilSymbolOnUnknown(t *testing.T) {
	bindings := container.NewBindingIndex()
	models := eloquent.NewModelIndex()
	src := []byte("<?php echo 'hello';")
	sym := identifySymbol(src, "/fake.php", 10, bindings, models)
	if sym != nil {
		t.Errorf("expected nil symbol for unknown position, got %+v", sym)
	}
}
