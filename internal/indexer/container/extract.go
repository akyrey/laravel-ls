package container

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
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

// extractFileBindings parses path and extracts all Binding records.
// src and tree are the already-parsed result; tree is NOT closed here.
func extractFileBindings(path string, src []byte, tree *ts.Tree, syms *symbolTable) []Binding {
	ev := &extractVisitor{
		path: path,
		fc:   &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
		syms: syms,
	}
	phpwalk.Walk(path, src, tree, ev)

	// Fill in Binding.Location for any concrete FQN we know about.
	for i := range ev.bindings {
		if ev.bindings[i].Concrete != "" && ev.bindings[i].Location.Zero() {
			ev.bindings[i].Location = syms.classLocation(ev.bindings[i].Concrete)
		}
	}
	return ev.bindings
}

// — extractVisitor ────────────────────────────────────────────────────────

type extractVisitor struct {
	phpwalk.NullVisitor
	path     string
	fc       *phputil.FileContext
	syms     *symbolTable
	bindings []Binding
}

func (v *extractVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *extractVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *extractVisitor) VisitClass(n phpwalk.ClassInfo) {
	fqn := v.fc.Resolve(n.NameText)

	// PHP 8 attribute bindings on the class itself.
	v.bindings = append(v.bindings, extractAttrBindings(n.Raw, n.Src, fqn, v.fc, v.path)...)

	// ServiceProvider binding calls inside method bodies.
	if fqn != "" && v.syms.isServiceProvider(fqn) {
		cv := &bindingCallVisitor{fc: v.fc, path: v.path}
		if bodyNode := n.Raw.ChildByFieldName("body"); bodyNode != nil {
			phpwalk.WalkNode(v.path, n.Src, bodyNode, cv)
		}
		v.bindings = append(v.bindings, cv.bindings...)
	}
}

func (v *extractVisitor) VisitInterface(n phpwalk.InterfaceInfo) {
	fqn := v.fc.Resolve(n.NameText)
	v.bindings = append(v.bindings, extractAttrBindings(n.Raw, n.Src, fqn, v.fc, v.path)...)
}

// — bindingCallVisitor ────────────────────────────────────────────────────

type bindingCallVisitor struct {
	phpwalk.NullVisitor
	path     string
	fc       *phputil.FileContext
	bindings []Binding
}

func (v *bindingCallVisitor) VisitMethodCall(n phpwalk.MethodCallInfo) {
	if !isThisAppAccess(n.VarRaw, n.Src) {
		return
	}
	lifetime, ok := bindingMethods[n.MethodName]
	if !ok {
		return
	}
	v.recordCallBinding(n.Args, n.Src, lifetime, n.Location)
}

func (v *bindingCallVisitor) VisitStaticCall(n phpwalk.StaticCallInfo) {
	if v.fc.Resolve(n.ClassName) != appFacadeFQN {
		return
	}
	lifetime, ok := bindingMethods[n.MethodName]
	if !ok {
		return
	}
	v.recordCallBinding(n.Args, n.Src, lifetime, n.Location)
}

func (v *bindingCallVisitor) recordCallBinding(args []*ts.Node, src []byte, lifetime string, source phputil.Location) {
	if len(args) < 1 {
		return
	}
	abstract := extractClassConst(args[0], src, v.fc)
	if abstract == "" {
		return
	}

	b := Binding{Abstract: abstract, Lifetime: lifetime, Source: source}

	if len(args) < 2 {
		b.Kind = BindCall
		b.Concrete = abstract
		v.bindings = append(v.bindings, b)
		return
	}

	switch args[1].Kind() {
	case "class_constant_access_expression":
		b.Kind = BindCall
		b.Concrete = extractClassConst(args[1], src, v.fc)
	case "anonymous_function":
		b.Kind = BindClosure
		b.Concrete = concreteFromClosure(args[1], src, v.fc)
		if b.Concrete == "" {
			b.Location = phpnode.FromNode(v.path, args[1])
		}
	case "arrow_function":
		b.Kind = BindClosure
		b.Concrete = concreteFromArrowFn(args[1], src, v.fc)
		if b.Concrete == "" {
			b.Location = phpnode.FromNode(v.path, args[1])
		}
	default:
		b.Kind = BindCall
	}

	v.bindings = append(v.bindings, b)
}

// — PHP 8 attribute helpers ───────────────────────────────────────────────

// extractAttrBindings reads PHP 8 #[Bind/Singleton/ScopedBind(...)] attributes
// from a class_declaration or interface_declaration node and returns any
// Binding entries they produce.
func extractAttrBindings(raw *ts.Node, src []byte, abstract phputil.FQN, fc *phputil.FileContext, path string) []Binding {
	attrListNode := raw.ChildByFieldName("attributes")
	if attrListNode == nil {
		return nil
	}
	var out []Binding
	for i := uint(0); i < attrListNode.ChildCount(); i++ {
		attrGroup := attrListNode.Child(i)
		if attrGroup.Kind() != "attribute_group" {
			continue
		}
		for j := uint(0); j < attrGroup.ChildCount(); j++ {
			attr := attrGroup.Child(j)
			if attr.Kind() != "attribute" {
				continue
			}
			// First name-like child is the attribute class.
			var attrNameText string
			for k := uint(0); k < attr.ChildCount(); k++ {
				child := attr.Child(k)
				if child.Kind() == "qualified_name" || child.Kind() == "name" {
					attrNameText = phpnode.NodeText(child, src)
					break
				}
			}
			if attrNameText == "" {
				continue
			}
			attrFQN := fc.Resolve(attrNameText)
			lifetime, ok := bindingAttributes[attrFQN]
			if !ok {
				continue
			}
			argsNode := attr.ChildByFieldName("parameters")
			if argsNode == nil {
				continue
			}
			args := phpwalk.ArgExprs(argsNode, src)
			if len(args) == 0 {
				continue
			}
			concrete := extractClassConst(args[0], src, fc)
			if concrete == "" {
				continue
			}
			out = append(out, Binding{
				Abstract: abstract,
				Concrete: concrete,
				Kind:     BindAttribute,
				Lifetime: lifetime,
				Source:   phpnode.FromNode(path, attr),
			})
		}
	}
	return out
}

// — node-level helpers ────────────────────────────────────────────────────

// isThisAppAccess checks if node n is the expression $this->app.
func isThisAppAccess(n *ts.Node, src []byte) bool {
	if n == nil || n.Kind() != "member_access_expression" {
		return false
	}
	objNode := n.ChildByFieldName("object")
	if objNode == nil || objNode.Kind() != "variable_name" {
		return false
	}
	if phpnode.NodeText(objNode, src) != "$this" {
		return false
	}
	nameNode := n.ChildByFieldName("name")
	return nameNode != nil && phpnode.NodeText(nameNode, src) == "app"
}

// extractClassConst extracts the FQN from a class_constant_access_expression
// like `StripeGateway::class`. Returns "" for anything else.
func extractClassConst(n *ts.Node, src []byte, fc *phputil.FileContext) phputil.FQN {
	if n == nil || n.Kind() != "class_constant_access_expression" {
		return ""
	}
	var first, second string
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if child.Kind() == "name" || child.Kind() == "qualified_name" {
			if first == "" {
				first = phpnode.NodeText(child, src)
			} else {
				second = phpnode.NodeText(child, src)
				break
			}
		}
	}
	if second != "class" {
		return ""
	}
	return fc.Resolve(first)
}

// concreteFromClosure extracts the concrete FQN when an anonymous_function
// body is a single `return new X(...)`. Returns "" when more complex.
func concreteFromClosure(n *ts.Node, src []byte, fc *phputil.FileContext) phputil.FQN {
	bodyNode := n.ChildByFieldName("body")
	if bodyNode == nil {
		return ""
	}
	// Count named, non-comment statements.
	var stmts []*ts.Node
	for i := uint(0); i < bodyNode.ChildCount(); i++ {
		child := bodyNode.Child(i)
		if child.IsNamed() && child.Kind() != "comment" {
			stmts = append(stmts, child)
		}
	}
	if len(stmts) != 1 || stmts[0].Kind() != "return_statement" {
		return ""
	}
	ret := stmts[0]
	for i := uint(0); i < ret.ChildCount(); i++ {
		if child := ret.Child(i); child.Kind() == "object_creation_expression" {
			return concreteFromNew(child, src, fc)
		}
	}
	return ""
}

// concreteFromArrowFn extracts the concrete FQN when an arrow_function body
// is `new X(...)`. Returns "" otherwise.
func concreteFromArrowFn(n *ts.Node, src []byte, fc *phputil.FileContext) phputil.FQN {
	bodyNode := n.ChildByFieldName("body")
	if bodyNode == nil || bodyNode.Kind() != "object_creation_expression" {
		return ""
	}
	return concreteFromNew(bodyNode, src, fc)
}

// concreteFromNew extracts the class FQN from an object_creation_expression node.
func concreteFromNew(n *ts.Node, src []byte, fc *phputil.FileContext) phputil.FQN {
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if child.Kind() == "qualified_name" || child.Kind() == "name" {
			return fc.Resolve(phpnode.NodeText(child, src))
		}
	}
	return ""
}
