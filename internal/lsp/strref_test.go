package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestFindDefinition_EnvKey(t *testing.T) {
	root := writeStrRefFixture(t)
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("APP_NAME=Demo\nAPP_KEY=base64:xyz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	strs := strindex.Walk(root)

	src := []byte(`<?php return ['key' => env('APP_KEY')];`)
	offset := bytes.Index(src, []byte("APP_KEY")) + 2
	locs := findDefinition(src, "/fake/config/app.php", offset, container.NewBindingIndex(), eloquent.NewModelIndex(), strs, newDocumentStore())
	if len(locs) != 1 {
		t.Fatalf("locations = %d, want 1", len(locs))
	}
	if got := URIToPath(locs[0].URI); got != envPath {
		t.Errorf("jump target = %q, want %q", got, envPath)
	}
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("line = %d, want 1 (0-based second line)", locs[0].Range.Start.Line)
	}

	// Completion inside env('') offers the keys.
	src2 := []byte(`<?php $x = env('');`)
	items := stringRefCompletions(src2, bytes.IndexByte(src2, '\'')+1, strs)
	labels := make([]string, 0, len(items))
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	if len(labels) != 2 || labels[0] != "APP_KEY" || labels[1] != "APP_NAME" {
		t.Errorf("env completions = %v, want [APP_KEY APP_NAME]", labels)
	}
}

func TestStringRefHover(t *testing.T) {
	root := writeStrRefFixture(t)
	strs := strindex.Walk(root)
	docs := newDocumentStore()

	tests := []struct {
		name    string
		call    string
		wantKey string
		want    string
	}{
		{"config key", `config('app.name')`, "app.name", "config key"},
		{"view name", `view('users.index')`, "users.index", "view"},
		{"route name", `route('home')`, "home", "route"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("<?php\n$x = " + tt.call + ";\n")
			offset := bytes.IndexByte(src, '\'') + 2

			ref := identifyStringRef(src, "/fake/Ctrl.php", offset, strs)
			if ref == nil || !ref.found {
				t.Fatalf("identifyStringRef = %+v, want found ref", ref)
			}
			md := stringRefHoverMD(ref, root, docs)
			if !strings.Contains(md, tt.wantKey) {
				t.Errorf("hover missing key %q; got:\n%s", tt.wantKey, md)
			}
			if !strings.Contains(md, tt.want) {
				t.Errorf("hover missing label %q; got:\n%s", tt.want, md)
			}
		})
	}
}

func TestStringRefHover_EnvShowsValue(t *testing.T) {
	root := writeStrRefFixture(t)
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("APP_NAME=Demo\nAPP_KEY=base64:xyz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	strs := strindex.Walk(root)
	docs := newDocumentStore()

	src := []byte(`<?php return ['key' => env('APP_KEY')];`)
	offset := bytes.Index(src, []byte("APP_KEY")) + 2

	ref := identifyStringRef(src, "/fake/config/app.php", offset, strs)
	if ref == nil || !ref.found {
		t.Fatalf("identifyStringRef = %+v, want found ref", ref)
	}
	md := stringRefHoverMD(ref, root, docs)
	if !strings.Contains(md, "base64:xyz") {
		t.Errorf("hover missing env value; got:\n%s", md)
	}
}

func TestStringRefHover_UnknownKey(t *testing.T) {
	root := writeStrRefFixture(t)
	strs := strindex.Walk(root)
	docs := newDocumentStore()

	src := []byte(`<?php $x = config('app.missing');`)
	offset := bytes.IndexByte(src, '\'') + 2

	ref := identifyStringRef(src, "/fake.php", offset, strs)
	if ref == nil {
		t.Fatal("expected a stringRef even when unresolved")
	}
	if ref.found {
		t.Fatal("expected found = false for unknown key")
	}
	if md := stringRefHoverMD(ref, root, docs); md != "" {
		t.Errorf("expected empty hover for unknown key, got:\n%s", md)
	}
}

func TestCollectDiagnostics_StringRefs(t *testing.T) {
	root := writeStrRefFixture(t)
	strs := strindex.Walk(root)

	t.Run("known keys emit nothing", func(t *testing.T) {
		src := []byte(`<?php
$a = config('app.name');
$b = view('users.index');
$c = route('home');
`)
		diags := collectDiagnostics(src, "/fake/Ctrl.php", nil, strs, defaultDiagOptions())
		for _, d := range diags {
			t.Errorf("unexpected diagnostic: %s", d.Message)
		}
	})

	t.Run("unknown keys are warned", func(t *testing.T) {
		src := []byte(`<?php
$a = config('app.missing');
$b = view('missing.view');
$c = route('missing.route');
`)
		diags := collectDiagnostics(src, "/fake/Ctrl.php", nil, strs, defaultDiagOptions())
		want := []string{
			"unknown config key 'app.missing'",
			"unknown view 'missing.view'",
			"unknown route 'missing.route'",
		}
		if len(diags) != len(want) {
			t.Fatalf("diags = %d, want %d: %v", len(diags), len(want), diags)
		}
		for i, w := range want {
			if diags[i].Message != w {
				t.Errorf("diags[%d] = %q, want %q", i, diags[i].Message, w)
			}
		}
	})

	t.Run("dynamic argument is not flagged", func(t *testing.T) {
		src := []byte(`<?php $key = 'app.name'; config($key);`)
		diags := collectDiagnostics(src, "/fake.php", nil, strs, defaultDiagOptions())
		for _, d := range diags {
			t.Errorf("unexpected diagnostic for dynamic config() argument: %s", d.Message)
		}
	})

	t.Run("nil string index emits nothing", func(t *testing.T) {
		src := []byte(`<?php config('app.missing');`)
		diags := collectDiagnostics(src, "/fake.php", nil, nil, defaultDiagOptions())
		if diags != nil {
			t.Errorf("expected nil diagnostics with nil strindex, got %v", diags)
		}
	})
}
