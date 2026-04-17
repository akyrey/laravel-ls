package eloquent_test

import (
	"testing"

	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

const fixtureRoot = "../../../testdata/models"

func walkFixtures(t *testing.T) *eloquent.ModelIndex {
	t.Helper()
	idx, err := eloquent.Walk(fixtureRoot, []string{"."})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return idx
}

func TestModernAccessor(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User model not indexed")
	}

	entries := cat.ByExposed["email_address"]
	found := false
	for _, a := range entries {
		if a.Kind == eloquent.ModernAccessor && a.MethodName == "emailAddress" {
			found = true
			if a.Source != eloquent.SourceAST {
				t.Errorf("Source = %v, want SourceAST", a.Source)
			}
			if a.Location.Zero() {
				t.Error("Location is zero")
			}
		}
	}
	if !found {
		t.Errorf("ModernAccessor for email_address not found; got %+v", entries)
	}
}

func TestLegacyAccessor(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User model not indexed")
	}

	entries := cat.ByExposed["first_name"]
	found := false
	for _, a := range entries {
		if a.Kind == eloquent.LegacyAccessor {
			found = true
			if a.Source != eloquent.SourceAST {
				t.Errorf("Source = %v, want SourceAST", a.Source)
			}
		}
	}
	if !found {
		t.Errorf("LegacyAccessor for first_name not found; got %+v", entries)
	}
}

func TestLegacyMutator(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\Post")
	if cat == nil {
		t.Fatal("Post model not indexed")
	}

	entries := cat.ByExposed["title"]
	found := false
	for _, a := range entries {
		if a.Kind == eloquent.LegacyMutator {
			found = true
		}
	}
	if !found {
		t.Errorf("LegacyMutator for title not found; got %+v", entries)
	}
}

func TestFillableArray(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User model not indexed")
	}

	for _, name := range []string{"email_address", "first_name"} {
		entries := cat.ByExposed[name]
		found := false
		for _, a := range entries {
			if a.Kind == eloquent.FillableArray {
				found = true
			}
		}
		if !found {
			t.Errorf("FillableArray for %q not found; got %+v", name, entries)
		}
	}
}

func TestCastArray(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User model not indexed")
	}

	entries := cat.ByExposed["email_verified_at"]
	found := false
	for _, a := range entries {
		if a.Kind == eloquent.CastArray {
			found = true
		}
	}
	if !found {
		t.Errorf("CastArray for email_verified_at not found; got %+v", entries)
	}
}

func TestAppendsArray(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\Post")
	if cat == nil {
		t.Fatal("Post model not indexed")
	}

	entries := cat.ByExposed["slug_url"]
	found := false
	for _, a := range entries {
		if a.Kind == eloquent.AppendsArray {
			found = true
		}
	}
	if !found {
		t.Errorf("AppendsArray for slug_url not found; got %+v", entries)
	}
}

func TestHiddenArray(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\Post")
	if cat == nil {
		t.Fatal("Post model not indexed")
	}

	entries := cat.ByExposed["secret_token"]
	found := false
	for _, a := range entries {
		if a.Kind == eloquent.HiddenArray {
			found = true
		}
	}
	if !found {
		t.Errorf("HiddenArray for secret_token not found; got %+v", entries)
	}
}

func TestRelationship(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User model not indexed")
	}

	// posts() HasMany → exposed as "posts" (no case translation)
	entries := cat.ByExposed["posts"]
	found := false
	for _, a := range entries {
		if a.Kind == eloquent.Relationship && a.MethodName == "posts" {
			found = true
			if a.ExposedName != "posts" {
				t.Errorf("ExposedName = %q, want %q", a.ExposedName, "posts")
			}
			if a.Source != eloquent.SourceAST {
				t.Errorf("Source = %v, want SourceAST", a.Source)
			}
			if a.Location.Zero() {
				t.Error("Location is zero")
			}
		}
	}
	if !found {
		t.Errorf("Relationship for posts not found; got %+v", entries)
	}
}

func TestNonModelNotIndexed(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	if cat := idx.Lookup(phputil.FQN("App\\Services\\NotAModel")); cat != nil {
		t.Errorf("NotAModel should not be in index, got %+v", cat)
	}
}

func TestMultipleEntriesForSameName(t *testing.T) {
	t.Parallel()
	idx := walkFixtures(t)

	cat := idx.Lookup("App\\Models\\User")
	if cat == nil {
		t.Fatal("User model not indexed")
	}

	// email_address appears as both ModernAccessor and FillableArray.
	entries := cat.ByExposed["email_address"]
	if len(entries) < 2 {
		t.Errorf("expected >= 2 entries for email_address, got %d: %+v", len(entries), entries)
	}
}
