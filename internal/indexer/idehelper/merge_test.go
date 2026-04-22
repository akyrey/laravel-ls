package idehelper_test

import (
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/indexer/idehelper"
)

const fixtureFile = "../../../testdata/idehelper/_ide_helper_models.php"

func TestMerge_AddsIdeHelperProperties(t *testing.T) {
	idx := eloquent.NewModelIndex()
	if err := idehelper.Merge(fixtureFile, idx); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("App\\Models\\User not in index after merge")
	}

	// id, email_address, first_name, email_verified_at, created_at, updated_at
	// roles_count (property-read)
	for _, name := range []string{"id", "email_address", "first_name", "email_verified_at", "created_at", "updated_at", "roles_count"} {
		entries := cat.ByExposed[name]
		found := false
		for _, a := range entries {
			if a.Kind == eloquent.IdeHelperProperty && a.Source == eloquent.SourceIdeHelper {
				found = true
				if !a.Location.Zero() {
					t.Errorf("%s: expected zero location, got %+v", name, a.Location)
				}
			}
		}
		if !found {
			t.Errorf("IdeHelperProperty for %q not found; got %+v", name, entries)
		}
	}
}

func TestMerge_AddsIdeHelperMethods(t *testing.T) {
	idx := eloquent.NewModelIndex()
	if err := idehelper.Merge(fixtureFile, idx); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("App\\Models\\User not in index after merge")
	}

	for _, name := range []string{"active", "newModelQuery"} {
		entries := cat.ByExposed[name]
		found := false
		for _, a := range entries {
			if a.Kind == eloquent.IdeHelperMethod && a.Source == eloquent.SourceIdeHelper {
				found = true
			}
		}
		if !found {
			t.Errorf("IdeHelperMethod for %q not found; got %+v", name, entries)
		}
	}
}

func TestMerge_ASTWinsOverIdeHelper(t *testing.T) {
	// Pre-populate the index with an AST entry for email_address.
	idx := eloquent.NewModelIndex()
	modelsRoot := filepath.Join("..", "..", "..", "testdata", "models")
	astIdx, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	// Copy the AST catalog for User into idx.
	if c := astIdx.Lookup("App\\Models\\User"); c != nil {
		idx.Add(c)
	}

	if err := idehelper.Merge(fixtureFile, idx); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User catalog missing")
	}

	// email_address has SourceAST entry → the ide-helper entry should be absent.
	for _, a := range cat.ByExposed["email_address"] {
		if a.Source == eloquent.SourceIdeHelper {
			t.Error("ide-helper entry for email_address should be dropped when SourceAST entry exists")
		}
	}
}

func TestMerge_AbsentFileIsNoOp(t *testing.T) {
	idx := eloquent.NewModelIndex()
	if err := idehelper.Merge("/nonexistent/_ide_helper_models.php", idx); err != nil {
		t.Errorf("expected nil for missing file, got %v", err)
	}
}
