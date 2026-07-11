package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
)

func TestCollectDiagnostics_NoWarningsForKnownProps(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	// User.php accesses $this->email_address which IS in the catalog.
	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\User;
class Ctrl {
    public function show(User $user): string {
        return $user->email_address;
    }
}`)
	diags := collectDiagnostics(src, "/fake/Ctrl.php", models)
	for _, d := range diags {
		t.Errorf("unexpected diagnostic: %s", d.Message)
	}
}

func TestCollectDiagnostics_WarnForUnknownProp(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\User;
class Ctrl {
    public function show(User $user): string {
        return $user->totally_unknown_prop;
    }
}`)
	diags := collectDiagnostics(src, "/fake/Ctrl.php", models)
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for unknown prop, got none")
	}
	found := false
	for _, d := range diags {
		if d.Message == "unknown property 'totally_unknown_prop' on App\\Models\\User" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected diagnostic message not found, got: %v", diags)
	}
}

func TestCollectDiagnostics_NilModels(t *testing.T) {
	src := []byte(`<?php $x->prop;`)
	// Should return nil without panic when models is nil.
	diags := collectDiagnostics(src, "/fake.php", nil)
	if diags != nil {
		t.Errorf("expected nil with nil models, got %v", diags)
	}
}

func TestCollectDiagnostics_UnresolvedVar(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	// $unknown has no type hint — can't resolve, so no diagnostic.
	src := []byte(`<?php
class Ctrl {
    public function show(): void {
        $unknown->any_prop;
    }
}`)
	diags := collectDiagnostics(src, "/fake/Ctrl.php", models)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for unresolved var, got %d", len(diags))
	}
}

func TestCollectDiagnostics_NoWarningForDynamicPropertyFetch(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	// $user->$attr is a dynamic access — the property name is unknowable
	// statically, so no diagnostic must be emitted.
	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\User;
class Ctrl {
    public function show(User $user, string $attr): mixed {
        return $user->$attr;
    }
}`)
	diags := collectDiagnostics(src, "/fake/Ctrl.php", models)
	for _, d := range diags {
		t.Errorf("unexpected diagnostic for dynamic fetch: %s", d.Message)
	}
}

func TestCollectDiagnostics_NoWarningForBuiltinModelAttributes(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	// None of these are in User's catalog, but every Eloquent model has
	// them: primary key, timestamps, soft-delete column, pivot, and the
	// base Model class's own PHP properties.
	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\User;
class Ctrl {
    public function show(User $user): void {
        $user->id;
        $user->created_at;
        $user->updated_at;
        $user->deleted_at;
        $user->pivot;
        $user->exists;
        $user->wasRecentlyCreated;
        $user->timestamps;
        $user->incrementing;
    }
}`)
	diags := collectDiagnostics(src, "/fake/Ctrl.php", models)
	for _, d := range diags {
		t.Errorf("unexpected diagnostic for built-in attribute: %s", d.Message)
	}
}

func TestCollectDiagnostics_BaseModelPropsInsideModel(t *testing.T) {
	modelsRoot := filepath.Join("..", "..", "testdata", "models")
	models, err := eloquent.Walk(modelsRoot, []string{"."})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	// $this-> access to inherited Model properties inside a model class.
	src := []byte(`<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
class User extends Model {
    public function tableName(): string {
        return $this->table ?? $this->getTable();
    }
    public function raw(): array {
        return $this->attributes;
    }
}`)
	diags := collectDiagnostics(src, "/fake/User.php", models)
	for _, d := range diags {
		t.Errorf("unexpected diagnostic for inherited Model property: %s", d.Message)
	}
}

func TestCollectDiagnostics_NoWarningForInheritedAttribute(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app", "Models")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
abstract class BaseModel extends Model {
    protected $fillable = ['tenant_id'];
}`
	child := `<?php
namespace App\Models;
class Invoice extends BaseModel {
    protected $fillable = ['number'];
}`
	if err := os.WriteFile(filepath.Join(appDir, "BaseModel.php"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "Invoice.php"), []byte(child), 0o644); err != nil {
		t.Fatal(err)
	}

	models, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\Invoice;
class Ctrl {
    public function show(Invoice $invoice): void {
        $invoice->tenant_id;
        $invoice->number;
    }
}`)
	diags := collectDiagnostics(src, "/fake/Ctrl.php", models)
	for _, d := range diags {
		t.Errorf("unexpected diagnostic: %s", d.Message)
	}
}

func TestCollectDiagnostics_NoWarningForTraitAttribute(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app", "Models")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	trait := `<?php
namespace App\Models;
trait HasSlug {
    protected $fillable = ['slug'];
}`
	model := `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
class Page extends Model {
    use HasSlug;
}`
	if err := os.WriteFile(filepath.Join(appDir, "HasSlug.php"), []byte(trait), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "Page.php"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}

	models, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}

	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\Page;
class Ctrl {
    public function show(Page $page): void {
        $page->slug;
    }
}`)
	diags := collectDiagnostics(src, "/fake/Ctrl.php", models)
	for _, d := range diags {
		t.Errorf("unexpected diagnostic: %s", d.Message)
	}
}
