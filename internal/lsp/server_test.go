package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// mkTestTree creates root/app, root/routes, root/Modules/Blog/app, and
// root/Modules/Shop/app under a fresh temp directory and returns root.
func mkTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dirs := []string{
		"app",
		"routes",
		filepath.Join("Modules", "Blog", "app"),
		filepath.Join("Modules", "Shop", "app"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}
	return root
}

func TestExpandDirs_LiteralPattern(t *testing.T) {
	root := mkTestTree(t)
	got := expandDirs(root, []string{"app"})
	want := []string{"app"}
	if !equalStrings(got, want) {
		t.Errorf("expandDirs(literal) = %v, want %v", got, want)
	}
}

func TestExpandDirs_LiteralPatternIncludedEvenWhenMissing(t *testing.T) {
	root := mkTestTree(t)
	// "vendor" does not exist on disk; non-glob patterns are included as-is
	// (Walk handles missing dirs silently per the doc comment).
	got := expandDirs(root, []string{"vendor"})
	want := []string{"vendor"}
	if !equalStrings(got, want) {
		t.Errorf("expandDirs(missing literal) = %v, want %v", got, want)
	}
}

func TestExpandDirs_GlobExpansion(t *testing.T) {
	root := mkTestTree(t)
	got := expandDirs(root, []string{filepath.Join("Modules", "*", "app")})
	want := []string{
		filepath.Join("Modules", "Blog", "app"),
		filepath.Join("Modules", "Shop", "app"),
	}
	sort.Strings(got)
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Errorf("expandDirs(glob) = %v, want %v", got, want)
	}
}

func TestExpandDirs_GlobWithNoMatches(t *testing.T) {
	root := mkTestTree(t)
	got := expandDirs(root, []string{filepath.Join("Nonexistent", "*", "app")})
	if len(got) != 0 {
		t.Errorf("expandDirs(no-match glob) = %v, want empty", got)
	}
}

func TestExpandDirs_MixedLiteralAndGlob(t *testing.T) {
	root := mkTestTree(t)
	got := expandDirs(root, []string{"app", filepath.Join("Modules", "*", "app")})
	want := []string{
		"app",
		filepath.Join("Modules", "Blog", "app"),
		filepath.Join("Modules", "Shop", "app"),
	}
	if !equalStrings(got, want) {
		t.Errorf("expandDirs(mixed) = %v, want %v", got, want)
	}
}

func TestExpandDirs_DedupesLiteralPatterns(t *testing.T) {
	root := mkTestTree(t)
	got := expandDirs(root, []string{"app", "app", "routes", "app"})
	want := []string{"app", "routes"}
	if !equalStrings(got, want) {
		t.Errorf("expandDirs(dupes) = %v, want %v", got, want)
	}
}

func TestExpandDirs_DedupesAcrossOverlappingGlobs(t *testing.T) {
	root := mkTestTree(t)
	got := expandDirs(root, []string{
		filepath.Join("Modules", "*", "app"),
		filepath.Join("Modules", "Blog", "app"), // literal, overlaps a glob match
	})
	want := []string{
		filepath.Join("Modules", "Blog", "app"),
		filepath.Join("Modules", "Shop", "app"),
	}
	sort.Strings(got)
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Errorf("expandDirs(overlap) = %v, want %v", got, want)
	}
}

func TestExpandDirs_MalformedGlobIsSkipped(t *testing.T) {
	root := mkTestTree(t)
	// "[" is an unterminated character class — filepath.Glob returns
	// ErrBadPattern; expandDirs must skip it rather than panic or abort.
	got := expandDirs(root, []string{"[", "app"})
	want := []string{"app"}
	if !equalStrings(got, want) {
		t.Errorf("expandDirs(malformed glob) = %v, want %v", got, want)
	}
}

func TestExpandDirs_EmptyPatterns(t *testing.T) {
	root := mkTestTree(t)
	got := expandDirs(root, nil)
	if len(got) != 0 {
		t.Errorf("expandDirs(nil) = %v, want empty", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
