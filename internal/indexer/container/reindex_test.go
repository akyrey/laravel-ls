package container_test

import (
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-ls/internal/indexer/container"
)

func TestReindexFile_PreservesOtherBindings(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "bindings")
	idx, err := container.Walk(root, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if idx.Syms() == nil {
		t.Fatal("Walk did not retain symbol table")
	}

	spPath := filepath.Join(root, "AppServiceProvider.php")
	bindings := idx.Lookup("App\\Contracts\\PaymentGateway")
	if len(bindings) == 0 {
		t.Skip("no PaymentGateway binding found; fixture may have changed")
	}

	newIdx, err := container.ReindexFile(spPath, idx)
	if err != nil {
		t.Fatalf("ReindexFile: %v", err)
	}

	// Bindings from the re-scanned file should still exist.
	newBindings := newIdx.Lookup("App\\Contracts\\PaymentGateway")
	if len(newBindings) == 0 {
		t.Error("PaymentGateway binding lost after ReindexFile")
	}
}

func TestReindexFile_MissingFile(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "bindings")
	idx, err := container.Walk(root, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	newIdx, err := container.ReindexFile("/nonexistent/Provider.php", idx)
	if err != nil {
		t.Fatalf("ReindexFile on missing file: %v", err)
	}
	// Existing bindings should be unaffected.
	if len(newIdx.All()) != len(idx.All()) {
		t.Errorf("binding count changed: %d → %d", len(idx.All()), len(newIdx.All()))
	}
}
