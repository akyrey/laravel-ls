// Package phpwalk provides a visitor-pattern walker over tree-sitter PHP CSTs.
// It replaces the VKCOM visitor.Null + traverser pattern used throughout the
// indexer and LSP packages.
//
// Usage:
//
//	tree, err := phpnode.ParseBytes(src)
//	defer tree.Close()
//	phpwalk.Walk(src, tree, &myVisitor{})
//
// Implement any subset of Visitor methods; embed NullVisitor for no-op defaults.
package phpwalk

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phputil"
)

// ParamInfo describes a single method parameter.
type ParamInfo struct {
	VarName  string // "$user" — includes the $ sigil
	TypeText string // unwrapped type text, e.g. "User" or "Sku"; "" if untyped
}

// ClassInfo carries the pre-extracted fields of a class_declaration node.
type ClassInfo struct {
	NameText    string           // short class name, e.g. "User"
	ExtendsText string           // parent name as written (unresolved), e.g. "Model"
	Location    phputil.Location // position of the whole class node
	Raw         *ts.Node
	Src         []byte
}

// InterfaceInfo carries the pre-extracted fields of an interface_declaration node.
type InterfaceInfo struct {
	NameText string
	Location phputil.Location
	Raw      *ts.Node
	Src      []byte
}

// MethodInfo carries the pre-extracted fields of a method_declaration node.
type MethodInfo struct {
	Name           string           // method name, e.g. "emailAddress"
	ReturnTypeText string           // unwrapped return type text, e.g. "Attribute"; "" if none
	Params         []ParamInfo
	Location       phputil.Location // position of the whole method node
	StartByte      int              // same as Location.StartByte; duplicated for cursor checks
	EndByte        int
	Raw            *ts.Node
	Src            []byte
}

// PropertyInfo carries the pre-extracted fields of one property element inside
// a property_declaration. There is one PropertyInfo per element; callers that
// care about the full declaration node receive it as Raw on the first element.
type PropertyInfo struct {
	PropName string           // PHP property name without $, e.g. "fillable"
	Location phputil.Location // position of the property_declaration node
	ValueRaw *ts.Node         // the default_value expression node, nil if absent
	Src      []byte
}

// PropertyFetchInfo carries the pre-extracted fields of a member_access_expression.
type PropertyFetchInfo struct {
	PropName     string
	PropLocation phputil.Location // position of just the property name token
	VarRaw       *ts.Node         // the object (LHS) expression node
	Raw          *ts.Node
	Src          []byte
}

// ClassConstFetchInfo carries the pre-extracted fields of a
// class_constant_access_expression (e.g. Foo::class, Foo::BAR).
type ClassConstFetchInfo struct {
	ClassName     string
	ConstName     string           // "class" for Foo::class
	ClassLocation phputil.Location // position of the class name token
	Raw           *ts.Node
	Src           []byte
}

// NewExprInfo carries the pre-extracted fields of an object_creation_expression.
type NewExprInfo struct {
	ClassName     string
	ClassLocation phputil.Location
	Raw           *ts.Node
	Src           []byte
}

// StaticCallInfo carries the pre-extracted fields of a scoped_call_expression
// (e.g. Foo::bar(...)).
type StaticCallInfo struct {
	ClassName     string
	MethodName    string
	ClassLocation phputil.Location
	Location      phputil.Location
	Args          []*ts.Node // argument expression nodes (unwrapped from argument wrapper)
	Raw           *ts.Node
	Src           []byte
}

// MethodCallInfo carries the pre-extracted fields of a member_call_expression
// (e.g. $x->method(...)).
type MethodCallInfo struct {
	MethodName string
	VarRaw     *ts.Node // the object (receiver) expression node
	Args       []*ts.Node
	Location   phputil.Location
	Raw        *ts.Node
	Src        []byte
}

// InstanceOfInfo carries the pre-extracted fields of an instanceof_expression.
type InstanceOfInfo struct {
	ClassName     string
	ClassLocation phputil.Location
	Raw           *ts.Node
	Src           []byte
}

// AssignInfo carries the pre-extracted fields of an assignment_expression.
type AssignInfo struct {
	VarName string   // "$x" if LHS is a simple variable; "" otherwise
	RHSRaw  *ts.Node // right-hand side expression node
	Raw     *ts.Node
	Src     []byte
}

// Visitor is implemented by types that want to receive PHP AST events.
// Every method has a no-op default via NullVisitor — embed it and override
// only the methods you need.
type Visitor interface {
	// File-context builders — called in source order before any class/method nodes.
	VisitNamespace(ns string)
	VisitUseItem(alias, fqn string)

	// Top-level declarations.
	VisitClass(n ClassInfo)
	VisitInterface(n InterfaceInfo)

	// Class body.
	VisitClassMethod(n MethodInfo)
	VisitProperty(n PropertyInfo) // called once per property element

	// Expressions — fired at any depth.
	VisitPropertyFetch(n PropertyFetchInfo)
	VisitClassConstFetch(n ClassConstFetchInfo)
	VisitNew(n NewExprInfo)
	VisitStaticCall(n StaticCallInfo)
	VisitMethodCall(n MethodCallInfo)
	VisitInstanceOf(n InstanceOfInfo)
	VisitAssign(n AssignInfo)
}

// NullVisitor provides empty implementations of every Visitor method.
// Embed it in your visitor struct and override only what you need.
type NullVisitor struct{}

func (NullVisitor) VisitNamespace(string)           {}
func (NullVisitor) VisitUseItem(string, string)     {}
func (NullVisitor) VisitClass(ClassInfo)             {}
func (NullVisitor) VisitInterface(InterfaceInfo)     {}
func (NullVisitor) VisitClassMethod(MethodInfo)      {}
func (NullVisitor) VisitProperty(PropertyInfo)       {}
func (NullVisitor) VisitPropertyFetch(PropertyFetchInfo) {}
func (NullVisitor) VisitClassConstFetch(ClassConstFetchInfo) {}
func (NullVisitor) VisitNew(NewExprInfo)             {}
func (NullVisitor) VisitStaticCall(StaticCallInfo)   {}
func (NullVisitor) VisitMethodCall(MethodCallInfo)   {}
func (NullVisitor) VisitInstanceOf(InstanceOfInfo)   {}
func (NullVisitor) VisitAssign(AssignInfo)           {}
