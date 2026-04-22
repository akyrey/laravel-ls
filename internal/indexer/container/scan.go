package container

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/akyrey/laravel-lsp/internal/phpparse"
	"github.com/akyrey/laravel-lsp/internal/phputil"
)

// scanVisitor builds a FileContext and records class/interface declarations
// into a symbolTable during a single traversal.
type scanVisitor struct {
	visitor.Null

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

func (v *scanVisitor) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *scanVisitor) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *scanVisitor) StmtGroupUse(n *ast.StmtGroupUseList) {
	prefix := phputil.NameToString(n.Prefix)
	phputil.AddUsesToContext(v.fc, n.Uses, prefix)
}

func (v *scanVisitor) StmtClass(n *ast.StmtClass) {
	fqn := phputil.ClassNodeFQN(n.Name, v.fc)
	if fqn == "" {
		return // anonymous class
	}
	var extends phputil.FQN
	if n.Extends != nil {
		extends = v.fc.Resolve(phputil.NameToString(n.Extends))
	}
	v.syms.addClass(v.path, fqn, &classDecl{
		Extends:  extends,
		Location: phputil.FromPosition(v.path, n.GetPosition()),
	})
}

func (v *scanVisitor) StmtInterface(n *ast.StmtInterface) {
	fqn := phputil.ClassNodeFQN(n.Name, v.fc)
	if fqn == "" {
		return
	}
	v.syms.addClass(v.path, fqn, &classDecl{
		Location:    phputil.FromPosition(v.path, n.GetPosition()),
		IsInterface: true,
	})
}

// buildSymbolTable walks all .php files in dirs (relative to root) and
// returns a populated symbolTable. After building, it resolves the
// ServiceProvider transitive set.
func buildSymbolTable(root string, dirs []string) (*symbolTable, error) {
	syms := newSymbolTable()
	for _, dir := range dirs {
		scanDir := filepath.Join(root, dir)
		err := filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
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
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	syms.resolveServiceProviders()
	return syms, nil
}
