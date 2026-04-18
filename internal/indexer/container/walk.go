package container

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/akyrey/laravel-ls/internal/phpparse"
)

// ReindexFile updates idx for a single changed file. It clones the retained
// symbol table, removes the file's old declarations, re-scans the file,
// re-resolves the service provider set, then rebuilds the index by dropping
// the file's old bindings and adding newly extracted ones.
// Returns a new *BindingIndex; the caller swaps it in atomically.
func ReindexFile(path string, old *BindingIndex) (*BindingIndex, error) {
	if old == nil || old.syms == nil {
		return nil, fmt.Errorf("container: old index has no symbol table")
	}

	newSyms := old.syms.clone()
	newSyms.removeFile(path)

	astRoot, err := phpparse.File(path)
	if err != nil {
		// File deleted or parse error — remove its entries, keep the rest.
		newIdx := NewBindingIndex()
		newIdx.syms = newSyms
		for abstract, bindings := range old.byAbstract {
			for _, b := range bindings {
				if b.Source.Path != path {
					newIdx.byAbstract[abstract] = append(newIdx.byAbstract[abstract], b)
				}
			}
		}
		return newIdx, nil
	}

	sv := newScanVisitor(path, newSyms)
	traverser.NewTraverser(sv).Traverse(astRoot)
	newSyms.resolveServiceProviders()

	newIdx := NewBindingIndex()
	newIdx.syms = newSyms
	for abstract, bindings := range old.byAbstract {
		for _, b := range bindings {
			if b.Source.Path != path {
				newIdx.byAbstract[abstract] = append(newIdx.byAbstract[abstract], b)
			}
		}
	}
	for _, b := range extractFileBindings(path, astRoot, newSyms) {
		newIdx.Add(b)
	}
	return newIdx, nil
}

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
	idx.syms = syms
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
