package container

import (
	"fmt"
	"testing"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
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
	path := "../../../testdata/bindings/AppServiceProvider.php"
	src, tree, err := phpnode.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	syms, _ := buildSymbolTable("../../../testdata/bindings", []string{"."})
	bindings := extractFileBindings(path, src, tree, syms)
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

// countingVisitor counts how many of each node kind it sees.
type countingVisitor struct {
	phpwalk.NullVisitor
	classes    int
	interfaces int
	uses       int
	methods    int
}

func (v *countingVisitor) VisitClass(phpwalk.ClassInfo)          { v.classes++ }
func (v *countingVisitor) VisitInterface(phpwalk.InterfaceInfo)  { v.interfaces++ }
func (v *countingVisitor) VisitUseItem(string, string)           { v.uses++ }
func (v *countingVisitor) VisitMethodCall(phpwalk.MethodCallInfo) { v.methods++ }

func TestDebugTraversal(t *testing.T) {
	path := "../../../testdata/bindings/AppServiceProvider.php"
	src, tree, err := phpnode.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	cv := &countingVisitor{}
	phpwalk.Walk("", src, tree, cv)
	fmt.Printf("classes=%d interfaces=%d uses=%d methods=%d\n", cv.classes, cv.interfaces, cv.uses, cv.methods)
	if cv.classes == 0 {
		t.Error("no class nodes visited")
	}
	if cv.uses == 0 {
		t.Error("no use items visited")
	}
}
