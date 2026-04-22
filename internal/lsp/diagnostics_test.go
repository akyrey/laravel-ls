package lsp

import (
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
