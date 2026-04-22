package container

import (
	"fmt"
	"testing"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"

	"github.com/akyrey/laravel-lsp/internal/phpparse"
)

func TestDebugDump(t *testing.T) {
	idx, err := Walk("../../../testdata/bindings", []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	all := idx.All()
	fmt.Printf("Total bindings: %d\n", len(all))
	for _, b := range all {
		fmt.Printf("  abstract=%s concrete=%s kind=%d lifetime=%s loc=%v\n",
			b.Abstract, b.Concrete, b.Kind, b.Lifetime, b.Location)
	}
}

func TestDebugExtract(t *testing.T) {
	root, err := phpparse.File("../../../testdata/bindings/AppServiceProvider.php")
	if err != nil {
		t.Fatal(err)
	}
	syms, _ := buildSymbolTable("../../../testdata/bindings", []string{"."})
	bindings := extractFileBindings("../../../testdata/bindings/AppServiceProvider.php", root, syms)
	fmt.Printf("AppServiceProvider bindings: %d\n", len(bindings))
	for _, b := range bindings {
		fmt.Printf("  %+v\n", b)
	}
}

func TestDebugSymbols(t *testing.T) {
	syms, err := buildSymbolTable("../../../testdata/bindings", []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("Classes in symbol table: %d\n", len(syms.classes))
	for fqn, d := range syms.classes {
		fmt.Printf("  fqn=%s extends=%s isIface=%v isSP=%v\n",
			fqn, d.Extends, d.IsInterface, syms.isServiceProvider(fqn))
	}
}

// countingVisitor counts how many StmtClass/StmtInterface nodes it sees.
type countingVisitor struct {
	visitor.Null
	classes    int
	interfaces int
	uses       int
	methods    int
}

func (v *countingVisitor) StmtClass(_ *ast.StmtClass)         { v.classes++ }
func (v *countingVisitor) StmtInterface(_ *ast.StmtInterface) { v.interfaces++ }
func (v *countingVisitor) StmtUse(_ *ast.StmtUseList)         { v.uses++ }
func (v *countingVisitor) ExprMethodCall(_ *ast.ExprMethodCall) { v.methods++ }

func TestDebugTraversal(t *testing.T) {
	root, err := phpparse.File("../../../testdata/bindings/AppServiceProvider.php")
	if err != nil {
		t.Fatal(err)
	}
	cv := &countingVisitor{}
	traverser.NewTraverser(cv).Traverse(root)
	fmt.Printf("classes=%d interfaces=%d uses=%d methods=%d\n", cv.classes, cv.interfaces, cv.uses, cv.methods)
	if cv.classes == 0 {
		t.Error("no StmtClass nodes visited")
	}
	if cv.uses == 0 {
		t.Error("no StmtUse nodes visited")
	}
}
