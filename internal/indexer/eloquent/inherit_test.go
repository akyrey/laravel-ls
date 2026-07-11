package eloquent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
)

func writeInheritFixture(t *testing.T) string {
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
	write("app/Models/BaseModel.php", `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Casts\Attribute;
abstract class BaseModel extends Model {
    protected $fillable = ['tenant_id'];
    public function displayName(): Attribute {
        return Attribute::make(get: fn($v) => $v);
    }
}`)
	write("app/Models/Invoice.php", `<?php
namespace App\Models;
class Invoice extends BaseModel {
    protected $fillable = ['number'];
}`)
	write("app/Models/CreditNote.php", `<?php
namespace App\Models;
class CreditNote extends Invoice {}`)
	return root
}

func TestLookup_MergesInheritedAttributes(t *testing.T) {
	root := writeInheritFixture(t)
	idx, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	cat := idx.Lookup("App\\Models\\Invoice")
	if cat == nil {
		t.Fatal("Invoice not indexed")
	}

	if _, ok := cat.ByExposed["number"]; !ok {
		t.Error("own attribute 'number' missing")
	}
	if _, ok := cat.ByExposed["tenant_id"]; !ok {
		t.Error("inherited fillable 'tenant_id' missing")
	}
	attrs, ok := cat.ByExposed["display_name"]
	if !ok {
		t.Fatal("inherited accessor 'display_name' missing")
	}
	if want := filepath.Join(root, "app", "Models", "BaseModel.php"); attrs[0].Location.Path != want {
		t.Errorf("inherited attribute location = %q, want %q", attrs[0].Location.Path, want)
	}

	// The view must keep the child's own identity.
	if want := filepath.Join(root, "app", "Models", "Invoice.php"); cat.Path != want {
		t.Errorf("cat.Path = %q, want %q", cat.Path, want)
	}
}

func TestLookup_MergesGrandparentAttributes(t *testing.T) {
	root := writeInheritFixture(t)
	idx, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	cat := idx.Lookup("App\\Models\\CreditNote")
	if cat == nil {
		t.Fatal("CreditNote not indexed")
	}
	for _, name := range []string{"number", "tenant_id", "display_name"} {
		if _, ok := cat.ByExposed[name]; !ok {
			t.Errorf("attribute %q missing from grandchild view", name)
		}
	}
}

func TestAll_ReturnsDeclaredAttributesOnly(t *testing.T) {
	root := writeInheritFixture(t)
	idx, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// All() feeds documentSymbol / workspaceSymbol; inherited entries must
	// not be duplicated onto every subclass there.
	for _, c := range idx.All() {
		if c.Class != "App\\Models\\Invoice" {
			continue
		}
		if _, ok := c.ByExposed["tenant_id"]; ok {
			t.Error("All() catalog for Invoice contains inherited 'tenant_id'")
		}
	}
}
