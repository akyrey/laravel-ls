package container

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

// bindingMethods maps ServiceProvider method names to their Lifetime string.
var bindingMethods = map[string]string{
	"bind":      "transient",
	"singleton": "singleton",
	"scoped":    "scoped",
	"instance":  "instance",
}

// bindingAttributes maps fully-qualified PHP 8 attribute names to Lifetime.
var bindingAttributes = map[phputil.FQN]string{
	"Illuminate\\Container\\Attributes\\Bind":      "transient",
	"Illuminate\\Container\\Attributes\\Singleton": "singleton",
	"Illuminate\\Container\\Attributes\\ScopedBind": "scoped",
}

// appFacadeFQN is the Laravel App facade.
const appFacadeFQN phputil.FQN = "Illuminate\\Support\\Facades\\App"

// extractFileBindings parses path and extracts all Binding records it can
// determine syntactically. It uses syms to check ServiceProvider inheritance
// and to resolve Binding.Location (the concrete class declaration site).
func extractFileBindings(path string, root ast.Vertex, syms *symbolTable) []Binding {
	ev := &extractVisitor{
		path: path,
		fc:   &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
		syms: syms,
	}
	traverser.NewTraverser(ev).Traverse(root)
	// Fill in Binding.Location for any concrete FQN we know about.
	for i := range ev.bindings {
		if ev.bindings[i].Concrete != "" && ev.bindings[i].Location.Zero() {
			ev.bindings[i].Location = syms.classLocation(ev.bindings[i].Concrete)
		}
	}
	return ev.bindings
}

// extractVisitor is the top-level per-file binding extraction visitor.
// It builds a FileContext as it goes, then delegates to sub-visitors for
// ServiceProvider class bodies.
type extractVisitor struct {
	visitor.Null

	path     string
	fc       *phputil.FileContext
	syms     *symbolTable
	bindings []Binding
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

	// PHP 8 attribute bindings on the class itself.
	v.bindings = append(v.bindings, extractAttrBindings(n.AttrGroups, fqn, v.fc, v.path)...)

	// ServiceProvider binding calls in method bodies.
	if fqn != "" && v.syms.isServiceProvider(fqn) {
		cv := &bindingCallVisitor{fc: v.fc, path: v.path}
		tr := traverser.NewTraverser(cv)
		for _, stmt := range n.Stmts {
			tr.Traverse(stmt)
		}
		v.bindings = append(v.bindings, cv.bindings...)
	}
}

func (v *extractVisitor) StmtInterface(n *ast.StmtInterface) {
	fqn := phputil.ClassNodeFQN(n.Name, v.fc)
	v.bindings = append(v.bindings, extractAttrBindings(n.AttrGroups, fqn, v.fc, v.path)...)
}

// bindingCallVisitor traverses a ServiceProvider class body looking for
// $this->app->bind/singleton/scoped/instance(...) and App::bind(...) calls.
type bindingCallVisitor struct {
	visitor.Null

	path     string
	fc       *phputil.FileContext
	bindings []Binding
}

func (v *bindingCallVisitor) ExprMethodCall(n *ast.ExprMethodCall) {
	method, ok := thisAppMethod(n)
	if !ok {
		return
	}
	lifetime, ok := bindingMethods[method]
	if !ok {
		return
	}
	v.recordCallBinding(n.Args, lifetime, phputil.FromPosition(v.path, n.GetPosition()))
}

func (v *bindingCallVisitor) ExprStaticCall(n *ast.ExprStaticCall) {
	method, ok := appFacadeMethod(n, v.fc)
	if !ok {
		return
	}
	lifetime, ok := bindingMethods[method]
	if !ok {
		return
	}
	v.recordCallBinding(n.Args, lifetime, phputil.FromPosition(v.path, n.GetPosition()))
}

func (v *bindingCallVisitor) recordCallBinding(rawArgs []ast.Vertex, lifetime string, source phputil.Location) {
	args := stripArgumentWrappers(rawArgs)
	if len(args) < 1 {
		return
	}
	abstract := extractClassConst(args[0], v.fc)
	if abstract == "" {
		return
	}

	b := Binding{
		Abstract: abstract,
		Lifetime: lifetime,
		Source:   source,
	}

	if len(args) < 2 {
		// Single-arg form: e.g. ->bind(Foo::class) binds Foo to itself.
		b.Kind = BindCall
		b.Concrete = abstract
		v.bindings = append(v.bindings, b)
		return
	}

	switch second := args[1].(type) {
	case *ast.ExprClassConstFetch:
		b.Kind = BindCall
		b.Concrete = extractClassConst(second, v.fc)
	case *ast.ExprClosure:
		b.Kind = BindClosure
		b.Concrete = concreteFromClosure(second, v.fc)
		if b.Concrete == "" {
			// Closure body not reducible; jump target is the closure itself.
			b.Location = phputil.FromPosition(v.path, second.GetPosition())
		}
	case *ast.ExprArrowFunction:
		b.Kind = BindClosure
		b.Concrete = concreteFromArrowFn(second, v.fc)
		if b.Concrete == "" {
			b.Location = phputil.FromPosition(v.path, second.GetPosition())
		}
	default:
		// Variable, string literal, or other expression: treat as opaque.
		b.Kind = BindCall
	}

	v.bindings = append(v.bindings, b)
}

// extractAttrBindings reads PHP 8 attribute groups on a class/interface node
// and returns any Bind/Singleton/ScopedBind bindings found.
// abstract is the FQN of the annotated class/interface.
func extractAttrBindings(attrGroups []ast.Vertex, abstract phputil.FQN, fc *phputil.FileContext, path string) []Binding {
	var out []Binding
	for _, ag := range attrGroups {
		attrGroup, ok := ag.(*ast.AttributeGroup)
		if !ok {
			continue
		}
		for _, attr := range attrGroup.Attrs {
			a, ok := attr.(*ast.Attribute)
			if !ok {
				continue
			}
			attrFQN := fc.Resolve(phputil.NameToString(a.Name))
			lifetime, ok := bindingAttributes[attrFQN]
			if !ok {
				continue
			}
			if len(a.Args) < 1 {
				continue
			}
			args := stripArgumentWrappers(a.Args)
			if len(args) < 1 {
				continue
			}
			concrete := extractClassConst(args[0], fc)
			if concrete == "" {
				continue
			}
			out = append(out, Binding{
				Abstract: abstract,
				Concrete: concrete,
				Kind:     BindAttribute,
				Lifetime: lifetime,
				Source:   phputil.FromPosition(path, a.GetPosition()),
				// Location filled in later from symbolTable.
			})
		}
	}
	return out
}

// thisAppMethod checks if n is a call of the form $this->app->METHOD(...)
// and returns (method, true) if so.
func thisAppMethod(n *ast.ExprMethodCall) (string, bool) {
	pf, ok := n.Var.(*ast.ExprPropertyFetch)
	if !ok {
		return "", false
	}
	ev, ok := pf.Var.(*ast.ExprVariable)
	if !ok {
		return "", false
	}
	varID, ok := ev.Name.(*ast.Identifier)
	if !ok {
		return "", false
	}
	if string(varID.Value) != "$this" {
		return "", false
	}
	propID, ok := pf.Prop.(*ast.Identifier)
	if !ok {
		return "", false
	}
	if string(propID.Value) != "app" {
		return "", false
	}
	methodID, ok := n.Method.(*ast.Identifier)
	if !ok {
		return "", false
	}
	return string(methodID.Value), true
}

// appFacadeMethod checks if n is App::METHOD(...) where App resolves to
// the Illuminate App facade, and returns (method, true) if so.
func appFacadeMethod(n *ast.ExprStaticCall, fc *phputil.FileContext) (string, bool) {
	className := phputil.NameToString(n.Class)
	if className == "" {
		return "", false
	}
	if fc.Resolve(className) != appFacadeFQN {
		return "", false
	}
	methodID, ok := n.Call.(*ast.Identifier)
	if !ok {
		return "", false
	}
	return string(methodID.Value), true
}

// extractClassConst extracts the FQN from an ExprClassConstFetch like
// `StripeGateway::class`. Returns "" for anything else.
func extractClassConst(expr ast.Vertex, fc *phputil.FileContext) phputil.FQN {
	cc, ok := expr.(*ast.ExprClassConstFetch)
	if !ok {
		return ""
	}
	constID, ok := cc.Const.(*ast.Identifier)
	if !ok {
		return ""
	}
	if string(constID.Value) != "class" {
		return ""
	}
	return fc.Resolve(phputil.NameToString(cc.Class))
}

// concreteFromClosure extracts the concrete FQN when a closure body is a
// single `return new X(...)`. Returns "" when the body is more complex.
func concreteFromClosure(c *ast.ExprClosure, fc *phputil.FileContext) phputil.FQN {
	if len(c.Stmts) != 1 {
		return ""
	}
	ret, ok := c.Stmts[0].(*ast.StmtReturn)
	if !ok || ret.Expr == nil {
		return ""
	}
	newExpr, ok := ret.Expr.(*ast.ExprNew)
	if !ok {
		return ""
	}
	return fc.Resolve(phputil.NameToString(newExpr.Class))
}

// concreteFromArrowFn extracts the concrete FQN when an arrow function is
// `fn($app) => new X(...)`. Returns "" otherwise.
func concreteFromArrowFn(fn *ast.ExprArrowFunction, fc *phputil.FileContext) phputil.FQN {
	if fn.Expr == nil {
		return ""
	}
	newExpr, ok := fn.Expr.(*ast.ExprNew)
	if !ok {
		return ""
	}
	return fc.Resolve(phputil.NameToString(newExpr.Class))
}

// stripArgumentWrappers unwraps the ast.Argument layer from each item in args,
// returning the inner expression. Named args keep the expression.
func stripArgumentWrappers(args []ast.Vertex) []ast.Vertex {
	out := make([]ast.Vertex, 0, len(args))
	for _, a := range args {
		if arg, ok := a.(*ast.Argument); ok {
			out = append(out, arg.Expr)
		} else {
			out = append(out, a)
		}
	}
	return out
}
