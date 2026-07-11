package phpwalk

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
)

// ClassConstFQN extracts the FQN from a class_constant_access_expression node
// like `StripeGateway::class`, resolving via fc. Returns "" for non-class-const
// expressions or when the constant is not "class".
func ClassConstFQN(n *ts.Node, src []byte, fc *phputil.FileContext) phputil.FQN {
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

// UnwrapTypeName returns the base type name from a type annotation node,
// stripping any nullable wrapper. E.g. optional_type(?Foo) → "Foo".
// This is the exported form of the internal unwrapTypeName used by the walker.
func UnwrapTypeName(n *ts.Node, src []byte) string {
	return unwrapTypeName(src, n)
}
