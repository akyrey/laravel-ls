package strindex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/indexer/strindex"
)

func writeFixture(t *testing.T) string {
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
return [
    'name' => env('APP_NAME', 'Laravel'),
    'providers' => [SomeProvider::class],
    'log' => [
        'channels' => [
            'slack' => ['url' => env('LOG_SLACK_URL')],
        ],
    ],
];`)
	write("config/services/stripe.php", `<?php
return ['key' => env('STRIPE_KEY')];`)
	write("resources/views/welcome.blade.php", `<h1>hi</h1>`)
	write("resources/views/users/index.blade.php", `<h1>users</h1>`)
	write("routes/web.php", `<?php
use Illuminate\Support\Facades\Route;
Route::get('/', fn () => view('welcome'))->name('home');
Route::get('/users', [UserController::class, 'index'])->middleware('auth')->name('users.index');`)
	return root
}

func TestWalk_ConfigKeys(t *testing.T) {
	idx := strindex.Walk(writeFixture(t))

	for _, key := range []string{
		"app.name",
		"app.providers",
		"app.log",
		"app.log.channels",
		"app.log.channels.slack",
		"app.log.channels.slack.url",
		"services/stripe.key",
	} {
		// Subdirectory config files use dots for the directory separator too.
		if key == "services/stripe.key" {
			key = "services.stripe.key"
		}
		loc, ok := idx.Config[key]
		if !ok {
			t.Errorf("config key %q not indexed", key)
			continue
		}
		if loc.Path == "" || loc.StartLine == 0 {
			t.Errorf("config key %q has empty location %+v", key, loc)
		}
	}
}

func TestWalk_Views(t *testing.T) {
	idx := strindex.Walk(writeFixture(t))

	for _, name := range []string{"welcome", "users.index"} {
		loc, ok := idx.Views[name]
		if !ok {
			t.Errorf("view %q not indexed", name)
			continue
		}
		if loc.Path == "" {
			t.Errorf("view %q has empty path", name)
		}
	}
}

func TestWalk_RouteNames(t *testing.T) {
	idx := strindex.Walk(writeFixture(t))

	for _, name := range []string{"home", "users.index"} {
		loc, ok := idx.Routes[name]
		if !ok {
			t.Errorf("route %q not indexed", name)
			continue
		}
		if loc.Path == "" || loc.StartLine == 0 {
			t.Errorf("route %q has empty location %+v", name, loc)
		}
	}
	// middleware('auth') must not be picked up as a route name.
	if _, ok := idx.Routes["auth"]; ok {
		t.Error("middleware argument indexed as a route name")
	}
}

func TestWalk_MissingDirs(t *testing.T) {
	// A root with none of the conventional dirs must not error or panic.
	idx := strindex.Walk(t.TempDir())
	if len(idx.Config)+len(idx.Views)+len(idx.Routes) != 0 {
		t.Errorf("expected empty index, got %+v", idx)
	}
}

func TestWalk_EnvKeys(t *testing.T) {
	root := t.TempDir()
	env := `APP_NAME=Demo
# comment line
APP_KEY=base64:xyz

export MAIL_HOST=smtp.example.com
INVALID LINE WITHOUT EQUALS
9BAD_KEY=nope
`
	example := `APP_NAME=ExampleOnlyValue
EXAMPLE_ONLY_KEY=
`
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := strindex.Walk(root)

	envPath := filepath.Join(root, ".env")
	tests := []struct {
		key      string
		wantPath string
		wantLine int
	}{
		{"APP_NAME", envPath, 1}, // present in both; .env wins
		{"APP_KEY", envPath, 3},
		{"MAIL_HOST", envPath, 5}, // export prefix stripped
		{"EXAMPLE_ONLY_KEY", filepath.Join(root, ".env.example"), 2},
	}
	src, _ := os.ReadFile(envPath)
	for _, tt := range tests {
		loc, ok := idx.Env[tt.key]
		if !ok {
			t.Errorf("env key %q not indexed", tt.key)
			continue
		}
		if loc.Path != tt.wantPath || loc.StartLine != tt.wantLine {
			t.Errorf("%q at %s:%d, want %s:%d", tt.key, loc.Path, loc.StartLine, tt.wantPath, tt.wantLine)
		}
		if loc.Path == envPath {
			if got := string(src[loc.StartByte:loc.EndByte]); got != tt.key {
				t.Errorf("%q byte range covers %q", tt.key, got)
			}
		}
	}
	for _, bad := range []string{"INVALID", "9BAD_KEY", "#"} {
		if _, ok := idx.Env[bad]; ok {
			t.Errorf("malformed entry %q must not be indexed", bad)
		}
	}
}

func TestWalk_EnvMissingFiles(t *testing.T) {
	idx := strindex.Walk(t.TempDir())
	if len(idx.Env) != 0 {
		t.Errorf("expected empty env index, got %v", idx.Env)
	}
}
