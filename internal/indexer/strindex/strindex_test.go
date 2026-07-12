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
