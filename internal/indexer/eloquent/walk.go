package eloquent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/akyrey/laravel-ls/internal/phpparse"
)

// DefaultScanDirs are the directories scanned when no explicit list is given.
var DefaultScanDirs = []string{"app"}

// Walk scans dirs (relative to root) in two phases:
//  1. Build a symbol table mapping class FQNs to their extends chain.
//  2. Extract ModelCatalog entries from every Model subclass found.
func Walk(root string, dirs []string) (*ModelIndex, error) {
	syms, err := buildSymbolTable(root, dirs)
	if err != nil {
		return nil, fmt.Errorf("eloquent: symbol scan: %w", err)
	}

	idx := NewModelIndex()
	for _, dir := range dirs {
		scanDir := filepath.Join(root, dir)
		err := filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(path, ".php") {
				return nil
			}
			astRoot, err := phpparse.File(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "laravel-lsp: skipping %s: %v\n", path, err)
				return nil
			}
			sv := newScanVisitor(path, syms)
			traverser.NewTraverser(sv).Traverse(astRoot)

			for _, catalog := range extractFileModels(path, astRoot, syms) {
				idx.Add(catalog)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return idx, nil
}
