package container

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

type scanVisitor struct {
	phpwalk.NullVisitor
	path string
	fc   *phputil.FileContext
	syms *symbolTable
}

func newScanVisitor(path string, syms *symbolTable) *scanVisitor {
	return &scanVisitor{
		path: path,
		fc:   &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
		syms: syms,
	}
}

func (v *scanVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *scanVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *scanVisitor) VisitClass(n phpwalk.ClassInfo) {
	if n.NameText == "" {
		return
	}
	fqn := v.fc.Resolve(n.NameText)
	if fqn == "" {
		return
	}
	var extends phputil.FQN
	if n.ExtendsText != "" {
		extends = v.fc.Resolve(n.ExtendsText)
	}
	v.syms.addClass(v.path, fqn, &classDecl{
		Extends:  extends,
		Location: phpnode.FromNode(v.path, n.Raw),
	})
}

func (v *scanVisitor) VisitInterface(n phpwalk.InterfaceInfo) {
	if n.NameText == "" {
		return
	}
	fqn := v.fc.Resolve(n.NameText)
	if fqn == "" {
		return
	}
	v.syms.addClass(v.path, fqn, &classDecl{
		Location:    phpnode.FromNode(v.path, n.Raw),
		IsInterface: true,
	})
}

func buildSymbolTable(root string, dirs []string) (*symbolTable, error) {
	syms := newSymbolTable()
	for _, dir := range dirs {
		scanDir := filepath.Join(root, dir)
		err := filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
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
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	syms.resolveServiceProviders()
	return syms, nil
}
