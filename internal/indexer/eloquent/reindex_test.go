package eloquent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
)

func TestReindexFile_UpdatesExistingModel(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "models")
	idx, err := eloquent.Walk(root, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if idx.Syms() == nil {
		t.Fatal("Walk did not retain symbol table")
	}

	// Confirm User model is indexed.
	userPath := filepath.Join(root, "User.php")
	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User not found before reindex")
	}

	// Incremental reindex of the same file should preserve the catalog.
	newIdx, err := eloquent.ReindexFile(userPath, idx)
	if err != nil {
		t.Fatalf("ReindexFile: %v", err)
	}
	if newIdx.Lookup("App\\Models\\User") == nil {
		t.Error("User not found after ReindexFile")
	}
	// Other models should still be present.
	if newIdx.Lookup("App\\Models\\Post") == nil {
		t.Error("Post not found after ReindexFile (should be preserved)")
	}
}

func TestReindexFile_DeletedFile(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "models")
	idx, err := eloquent.Walk(root, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// Reindex a non-existent file — should remove its entries if any, not error.
	newIdx, err := eloquent.ReindexFile("/nonexistent/Gone.php", idx)
	if err != nil {
		t.Fatalf("ReindexFile on missing file returned error: %v", err)
	}
	// All existing models should still be present.
	if newIdx.Lookup("App\\Models\\User") == nil {
		t.Error("User removed after reindexing nonexistent file")
	}
}

func TestReindexFile_PathTracked(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "models")
	idx, err := eloquent.Walk(root, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User not found")
	}
	if cat.Path == "" {
		t.Error("User catalog has empty Path")
	}
	if _, statErr := os.Stat(cat.Path); statErr != nil {
		t.Errorf("User catalog Path %q is not a valid file: %v", cat.Path, statErr)
	}
}
