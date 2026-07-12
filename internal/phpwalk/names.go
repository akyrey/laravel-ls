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

// IsStringLiteral reports whether n is a PHP string literal node: a
// single-quoted "string" or a double-quoted "encapsed_string".
func IsStringLiteral(n *ts.Node) bool {
	return n != nil && (n.Kind() == "string" || n.Kind() == "encapsed_string")
}

// StringValue extracts the unquoted content of a PHP string literal node.
// tree-sitter-php represents 'email' as string { ' string_content ' } and
// "email" as encapsed_string { " string_content " }. Returns "" when n is
// not a string literal or contains interpolation ("$var") — an interpolated
// value is unknowable statically.
func StringValue(n *ts.Node, src []byte) string {
	if !IsStringLiteral(n) {
		return ""
	}
	content := ""
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		switch {
		case child.Kind() == "string_content":
			if content != "" {
				return "" // split content implies interpolation between parts
			}
			content = phpnode.NodeText(child, src)
		case child.IsNamed() && child.Kind() != "string_content":
			return "" // interpolation or escape sequence node
		}
	}
	if content != "" {
		return content
	}
	// Fallback: strip surrounding quote characters from the raw node text.
	v := phpnode.NodeText(n, src)
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') {
		return v[1 : len(v)-1]
	}
	return v
}
