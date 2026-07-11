package eloquent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// ReindexFile updates idx for a single changed file without re-walking the
// whole project. It clones the retained symbol table, removes the file's old
// declarations, re-scans it, re-resolves the model set, then rebuilds the
// index by dropping the file's old catalogs and adding the newly extracted ones.
// Returns a new *ModelIndex; the caller swaps it in atomically.
func ReindexFile(path string, old *ModelIndex) (*ModelIndex, error) {
	if old == nil || old.syms == nil {
		return nil, fmt.Errorf("eloquent: old index has no symbol table")
	}

	newSyms := old.syms.clone()
	newSyms.removeFile(path)

	src, tree, err := phpnode.ParseFile(path)
	if err != nil {
		// File deleted or parse error — remove its entries, keep the rest.
		newIdx := NewModelIndex()
		newIdx.syms = newSyms
		carryOverExcept(newIdx, old, path)
		return newIdx, nil
	}
	defer tree.Close()

	sv := newScanVisitor(path, newSyms)
	phpwalk.Walk(path, src, tree, sv)
	newSyms.resolveModels()

	newIdx := NewModelIndex()
	newIdx.syms = newSyms
	carryOverExcept(newIdx, old, path)
	models, traits := extractFileModels(path, src, tree, newSyms)
	for _, catalog := range models {
		newIdx.Add(catalog)
	}
	for _, catalog := range traits {
		newIdx.AddTrait(catalog)
	}
	return newIdx, nil
}

// carryOverExcept copies every model and trait catalog from old into dst,
// skipping the ones declared in path (which is being reindexed or removed).
func carryOverExcept(dst, old *ModelIndex, path string) {
	for fqn, cat := range old.byFQN {
		if cat.Path != path {
			dst.byFQN[fqn] = cat
		}
	}
	for fqn, cat := range old.traits {
		if cat.Path != path {
			dst.traits[fqn] = cat
		}
	}
}

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
	idx.syms = syms
	for _, dir := range dirs {
		scanDir := filepath.Join(root, dir)
		err := filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(path, ".php") {
				return nil
			}
			src, tree, parseErr := phpnode.ParseFile(path)
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "laravel-lsp: skipping %s: %v\n", path, parseErr)
				return nil
			}
			defer tree.Close()

			sv := newScanVisitor(path, syms)
			phpwalk.Walk(path, src, tree, sv)

			models, traits := extractFileModels(path, src, tree, syms)
			for _, catalog := range models {
				idx.Add(catalog)
			}
			for _, catalog := range traits {
				idx.AddTrait(catalog)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return idx, nil
}
