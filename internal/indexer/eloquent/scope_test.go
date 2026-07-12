package eloquent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
)

func TestLookup_ScopesFromClassTraitAndParent(t *testing.T) {
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
	write("app/Models/Concerns/Publishable.php", `<?php
namespace App\Models\Concerns;
trait Publishable {
    public function scopePublished($q) { return $q->whereNotNull('published_at'); }
}`)
	write("app/Models/BaseModel.php", `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
abstract class BaseModel extends Model {
    public function scopeForTenant($q, $t) { return $q->where('tenant_id', $t); }
}`)
	write("app/Models/Article.php", `<?php
namespace App\Models;
use App\Models\Concerns\Publishable;
class Article extends BaseModel {
    use Publishable;
    public function scopeFeatured($q) { return $q->where('featured', true); }
}`)

	idx, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	cat := idx.Lookup("App\\Models\\Article")
	if cat == nil {
		t.Fatal("Article not indexed")
	}
	for _, name := range []string{"featured", "published", "forTenant"} {
		loc, ok := cat.Scopes[name]
		if !ok {
			t.Errorf("scope %q missing from merged view; got %v", name, cat.Scopes)
			continue
		}
		if loc.Zero() {
			t.Errorf("scope %q has zero location", name)
		}
	}
}
