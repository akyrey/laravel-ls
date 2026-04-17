package eloquent

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

// extractFileModels parses a single PHP file's AST and returns a ModelCatalog
// for each Model subclass found in it.
func extractFileModels(path string, root ast.Vertex, syms *symbolTable) []*ModelCatalog {
	fc := &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)}
	ev := &extractVisitor{path: path, fc: fc, syms: syms}
	traverser.NewTraverser(ev).Traverse(root)
	return ev.catalogs
}

type extractVisitor struct {
	visitor.Null
	path     string
	fc       *phputil.FileContext
	syms     *symbolTable
	catalogs []*ModelCatalog
}

func (v *extractVisitor) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *extractVisitor) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *extractVisitor) StmtGroupUse(n *ast.StmtGroupUseList) {
	prefix := phputil.NameToString(n.Prefix)
	phputil.AddUsesToContext(v.fc, n.Uses, prefix)
}

func (v *extractVisitor) StmtClass(n *ast.StmtClass) {
	fqn := phputil.ClassNodeFQN(n.Name, v.fc)
	if fqn == "" || !v.syms.isModel(fqn) {
		return
	}
	var extends phputil.FQN
	if n.Extends != nil {
		extends = v.fc.Resolve(phputil.NameToString(n.Extends))
	}

	catalog := &ModelCatalog{
		Class:     fqn,
		Extends:   extends,
		ByExposed: make(map[string][]ModelAttribute),
	}

	attrs := extractMethods(v.path, n, v.fc)
	attrs = append(attrs, extractArrayProperties(v.path, n)...)
	for _, a := range attrs {
		catalog.ByExposed[a.ExposedName] = append(catalog.ByExposed[a.ExposedName], a)
	}

	v.catalogs = append(v.catalogs, catalog)
}
