package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

func TestHover_EloquentPropertyAccess(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	bindings := container.NewBindingIndex()
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	userPath := filepath.Join(modelsRoot, "User.php")
	src, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

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

	md := hoverMarkdown(sym, bindings, models)
	if md == "" {
		t.Fatal("hoverMarkdown returned empty string")
	}
	if !strings.Contains(md, "email_address") {
		t.Errorf("hover markdown missing property name; got:\n%s", md)
	}
	if !strings.Contains(md, "accessor") {
		t.Errorf("hover markdown missing kind label; got:\n%s", md)
	}
	if !strings.Contains(md, "App\\Models\\User") {
		t.Errorf("hover markdown missing model FQN; got:\n%s", md)
	}
}

func TestHover_ContainerBinding(t *testing.T) {
	bindingsRoot := filepath.Join("..", "..", "testdata", "bindings")
	bindings, err := container.Walk(bindingsRoot, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	models := eloquent.NewModelIndex()

	spPath := filepath.Join(bindingsRoot, "AppServiceProvider.php")
	src, err := os.ReadFile(spPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	needle := []byte("PaymentGateway::class")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found")
	}

	sym := identifySymbol(src, spPath, idx, bindings, models)
	if sym == nil {
		t.Fatal("identifySymbol returned nil")
	}

	md := hoverMarkdown(sym, bindings, models)
	if md == "" {
		t.Fatal("hoverMarkdown returned empty string")
	}
	if !strings.Contains(md, "PaymentGateway") {
		t.Errorf("hover markdown missing abstract name; got:\n%s", md)
	}
	if !strings.Contains(md, "StripeGateway") {
		t.Errorf("hover markdown missing concrete name; got:\n%s", md)
	}
}

func TestHover_NilOnUnknown(t *testing.T) {
	bindings := container.NewBindingIndex()
	models := eloquent.NewModelIndex()
	src := []byte("<?php echo 'hello';")
	sym := identifySymbol(src, "/fake.php", 10, bindings, models)
	if sym != nil {
		t.Errorf("expected nil sym for unknown, got %+v", sym)
	}
}
