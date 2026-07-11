package eloquent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
)

func writeTraitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app/Models/Concerns/HasSlug.php", `<?php
namespace App\Models\Concerns;
use Illuminate\Database\Eloquent\Casts\Attribute;
trait HasSlug {
    protected $fillable = ['slug'];
    public function slugPreview(): Attribute {
        return Attribute::make(get: fn($v) => $v);
    }
}`)
	write("app/Models/Concerns/HasMeta.php", `<?php
namespace App\Models\Concerns;
trait HasMeta {
    use HasSlug;
    protected $casts = ['meta' => 'array'];
}`)
	write("app/Models/Post.php", `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
use App\Models\Concerns\HasMeta;
class Post extends Model {
    use HasMeta;
    protected $fillable = ['title'];
}`)
	write("app/Models/BasePage.php", `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
use App\Models\Concerns\HasSlug;
abstract class BasePage extends Model {
    use HasSlug;
}`)
	write("app/Models/LandingPage.php", `<?php
namespace App\Models;
class LandingPage extends BasePage {}`)
	return root
}

func TestLookup_MergesTraitAttributes(t *testing.T) {
	root := writeTraitFixture(t)
	idx, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	cat := idx.Lookup("App\\Models\\Post")
	if cat == nil {
		t.Fatal("Post not indexed")
	}
	// Own attribute, direct trait attribute, nested trait attribute,
	// and the trait accessor.
	for _, name := range []string{"title", "meta", "slug", "slug_preview"} {
		if _, ok := cat.ByExposed[name]; !ok {
			t.Errorf("attribute %q missing from Post view", name)
		}
	}

	// Trait attribute location must point at the trait's own file.
	attrs := cat.ByExposed["slug_preview"]
	if len(attrs) > 0 {
		want := filepath.Join(root, "app", "Models", "Concerns", "HasSlug.php")
		if attrs[0].Location.Path != want {
			t.Errorf("trait attribute location = %q, want %q", attrs[0].Location.Path, want)
		}
	}
}

func TestLookup_MergesTraitAttributesViaParent(t *testing.T) {
	root := writeTraitFixture(t)
	idx, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	cat := idx.Lookup("App\\Models\\LandingPage")
	if cat == nil {
		t.Fatal("LandingPage not indexed")
	}
	// slug comes from the HasSlug trait used by the parent BasePage.
	for _, name := range []string{"slug", "slug_preview"} {
		if _, ok := cat.ByExposed[name]; !ok {
			t.Errorf("attribute %q missing from LandingPage view", name)
		}
	}
}

func TestReindexFile_TraitChangePropagates(t *testing.T) {
	root := writeTraitFixture(t)
	idx, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// Add a new fillable entry to the trait, then reindex just that file.
	traitPath := filepath.Join(root, "app", "Models", "Concerns", "HasSlug.php")
	updated := `<?php
namespace App\Models\Concerns;
trait HasSlug {
    protected $fillable = ['slug', 'slug_source'];
}`
	if err := os.WriteFile(traitPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	newIdx, err := eloquent.ReindexFile(traitPath, idx)
	if err != nil {
		t.Fatalf("ReindexFile: %v", err)
	}

	cat := newIdx.Lookup("App\\Models\\Post")
	if cat == nil {
		t.Fatal("Post not indexed after trait reindex")
	}
	if _, ok := cat.ByExposed["slug_source"]; !ok {
		t.Error("new trait attribute 'slug_source' missing after incremental reindex")
	}
	if _, ok := cat.ByExposed["slug_preview"]; ok {
		t.Error("removed trait accessor 'slug_preview' still present after incremental reindex")
	}
}
