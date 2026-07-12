package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
)

func TestFindDefinition_QueryScopeStaticCall(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app", "Models")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	model := `<?php
namespace App\Models;
use Illuminate\Database\Eloquent\Model;
class User extends Model {
    public function scopeActive($q) { return $q->where('active', true); }
}`
	userPath := filepath.Join(appDir, "User.php")
	if err := os.WriteFile(userPath, []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}

	models, err := eloquent.Walk(root, []string{"app"})
	if err != nil {
		t.Fatalf("eloquent.Walk: %v", err)
	}
	bindings := container.NewBindingIndex()

	src := []byte(`<?php
namespace App\Http\Controllers;
use App\Models\User;
class Ctrl {
    public function index(): mixed {
        return User::active()->get();
    }
}`)
	needle := []byte("User::active")
	idx := bytes.Index(src, needle)
	if idx < 0 {
		t.Fatal("needle not found")
	}
	// Cursor on "active".
	offset := idx + len("User::ac")

	locs := findDefinition(src, "/fake/Ctrl.php", offset, bindings, models, nil, newDocumentStore())
	if len(locs) != 1 {
		t.Fatalf("locations = %d, want 1", len(locs))
	}
	if got := URIToPath(locs[0].URI); got != userPath {
		t.Errorf("jump target = %q, want %q", got, userPath)
	}

	// Cursor on an unknown method must return nothing.
	src2 := bytes.Replace(src, []byte("User::active()"), []byte("User::missing()"), 1)
	idx2 := bytes.Index(src2, []byte("User::missing"))
	locs2 := findDefinition(src2, "/fake/Ctrl.php", idx2+len("User::mi"), bindings, models, nil, newDocumentStore())
	if len(locs2) != 0 {
		t.Errorf("expected no locations for unknown scope, got %v", locs2)
	}
}
