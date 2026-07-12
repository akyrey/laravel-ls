package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/container"
	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/indexer/strindex"
)

func writeStrRefFixture(t *testing.T) string {
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
	write("config/app.php", `<?php
return ['name' => 'Demo', 'debug' => false];`)
	write("resources/views/users/index.blade.php", `<h1>users</h1>`)
	write("routes/web.php", `<?php
Route::get('/', fn () => 1)->name('home');`)
	return root
}

func TestFindDefinition_StringReferences(t *testing.T) {
	root := writeStrRefFixture(t)
	strs := strindex.Walk(root)
	bindings := container.NewBindingIndex()
	models := eloquent.NewModelIndex()

	tests := []struct {
		name     string
		call     string
		wantPath string
	}{
		{"config key", `config('app.name')`, filepath.Join(root, "config", "app.php")},
		{"view name", `view('users.index')`, filepath.Join(root, "resources", "views", "users", "index.blade.php")},
		{"route name", `route('home')`, filepath.Join(root, "routes", "web.php")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("<?php\n$x = " + tt.call + ";\n")
			// Cursor inside the string literal.
			offset := bytes.IndexByte(src, '\'') + 2
			locs := findDefinition(src, "/fake/Ctrl.php", offset, bindings, models, strs, newDocumentStore())
			if len(locs) != 1 {
				t.Fatalf("locations = %d, want 1", len(locs))
			}
			if got := URIToPath(locs[0].URI); got != tt.wantPath {
				t.Errorf("jump target = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

func TestFindDefinition_StringReferenceUnknownKey(t *testing.T) {
	root := writeStrRefFixture(t)
	strs := strindex.Walk(root)

	src := []byte(`<?php $x = config('app.missing');`)
	offset := bytes.IndexByte(src, '\'') + 2
	locs := findDefinition(src, "/fake.php", offset, container.NewBindingIndex(), eloquent.NewModelIndex(), strs, newDocumentStore())
	if len(locs) != 0 {
		t.Errorf("expected no locations for unknown key, got %v", locs)
	}
}

func TestStringRefCompletions(t *testing.T) {
	root := writeStrRefFixture(t)
	strs := strindex.Walk(root)

	src := []byte(`<?php $x = config('');`)
	offset := bytes.IndexByte(src, '\'') + 1
	items := stringRefCompletions(src, offset, strs)
	labels := make([]string, 0, len(items))
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	want := []string{"app.debug", "app.name"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Errorf("labels[%d] = %q, want %q", i, labels[i], want[i])
		}
	}

	// Outside any helper call: nothing.
	src2 := []byte(`<?php $x = other('');`)
	if items := stringRefCompletions(src2, bytes.IndexByte(src2, '\'')+1, strs); len(items) != 0 {
		t.Errorf("expected no completions in unrelated call, got %v", items)
	}
}
