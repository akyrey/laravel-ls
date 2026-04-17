package container

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/akyrey/laravel-ls/internal/phpparse"
)

// DefaultScanDirs are the subdirectories walked when no explicit dirs are given.
// These are relative to the project root (the rootUri from LSP initialize).
var DefaultScanDirs = []string{"app"}

// Walk builds a BindingIndex from all PHP files in the given dirs under root.
//
// Two-phase approach:
//  1. Scan every .php file to build a symbolTable (class declarations +
//     ServiceProvider transitive set).
//  2. Re-read every .php file to extract bindings; use the symbolTable to
//     resolve Concrete class locations.
//
// Parse errors are logged to stderr and skipped — they do not abort the walk.
func Walk(root string, dirs []string) (*BindingIndex, error) {
	if len(dirs) == 0 {
		dirs = DefaultScanDirs
	}

	// Phase 1: symbol scan.
	syms, err := buildSymbolTable(root, dirs)
	if err != nil {
		return nil, fmt.Errorf("container scan: %w", err)
	}

	// Phase 2: binding extraction.
	idx := NewBindingIndex()
	for _, dir := range dirs {
		scanDir := filepath.Join(root, dir)
		walkErr := filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(path, ".php") {
				return nil
			}
			astRoot, parseErr := phpparse.File(path)
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "laravel-lsp: skipping %s: %v\n", path, parseErr)
				return nil
			}
			for _, b := range extractFileBindings(path, astRoot, syms) {
				idx.Add(b)
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("container extract: %w", walkErr)
		}
	}

	return idx, nil
}
