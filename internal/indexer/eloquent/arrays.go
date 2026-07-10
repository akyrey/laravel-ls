package eloquent

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
)

// ArrayPropKinds maps PHP property names to their AttributeKind.
// Exported so the LSP layer can detect these properties without duplicating the list.
var ArrayPropKinds = map[string]AttributeKind{
	"fillable": FillableArray,
	"casts":    CastArray,
	"appends":  AppendsArray,
	"hidden":   HiddenArray,
}

// extractArrayProperties inspects $fillable, $casts, $appends, $hidden on
// the class and returns a ModelAttribute for each string entry found.
func extractArrayProperties(path string, classNode *ts.Node, src []byte) []ModelAttribute {
	bodyNode := classNode.ChildByFieldName("body")
	if bodyNode == nil {
		return nil
	}

	var out []ModelAttribute
	for i := uint(0); i < bodyNode.ChildCount(); i++ {
		propDecl := bodyNode.Child(i)
		if propDecl.Kind() != "property_declaration" {
			continue
		}

		for j := uint(0); j < propDecl.ChildCount(); j++ {
			elem := propDecl.Child(j)
			if elem.Kind() != "property_element" {
				continue
			}

			nameNode := elem.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			// variable_name text includes "$", e.g. "$fillable" — strip it.
			propName := strings.TrimPrefix(phpnode.NodeText(nameNode, src), "$")

			kind, ok := ArrayPropKinds[propName]
			if !ok {
				continue
			}

			valNode := elem.ChildByFieldName("default_value")
			if valNode == nil || valNode.Kind() != "array_creation_expression" {
				continue
			}

			loc := phpnode.FromNode(path, propDecl)
			for k := uint(0); k < valNode.ChildCount(); k++ {
				item := valNode.Child(k)
				if item.Kind() != "array_element_initializer" {
					continue
				}
				name := arrayItemName(kind, item, src)
				if name == "" {
					continue
				}
				out = append(out, ModelAttribute{
					ExposedName: name,
					Kind:        kind,
					Source:      SourceAST,
					Location:    loc,
				})
			}
		}
	}
	return out
}

// arrayItemName extracts the exposed attribute name from an array element.
// For $casts the key is the exposed name (associative: 'col' => 'type').
// For $fillable / $appends / $hidden the value is the name (sequential list).
func arrayItemName(kind AttributeKind, item *ts.Node, src []byte) string {
	name, _ := arrayItemNameAndNode(kind, item, src)
	return name
}

// arrayItemNameAndNode is like arrayItemName but also returns the string node
// that contains the exposed attribute name (for offset-based lookups).
func arrayItemNameAndNode(kind AttributeKind, item *ts.Node, src []byte) (string, *ts.Node) {
	var strNodes []*ts.Node
	var vals []string
	for i := uint(0); i < item.ChildCount(); i++ {
		child := item.Child(i)
		if child.Kind() == "string" {
			strNodes = append(strNodes, child)
			vals = append(vals, stringValue(child, src))
		}
	}
	switch kind {
	case CastArray:
		if len(vals) >= 2 {
			return vals[0], strNodes[0]
		}
	default:
		if len(vals) > 0 {
			return vals[len(vals)-1], strNodes[len(strNodes)-1]
		}
	}
	return "", nil
}

// ArrayItemAtOffset returns the exposed attribute name and the string node if
// offset falls within a relevant string element of valueNode
// (an array_creation_expression). Returns ("", nil) when no match.
func ArrayItemAtOffset(kind AttributeKind, valueNode *ts.Node, src []byte, offset int) (string, *ts.Node) {
	if valueNode == nil || valueNode.Kind() != "array_creation_expression" {
		return "", nil
	}
	for i := uint(0); i < valueNode.ChildCount(); i++ {
		item := valueNode.Child(i)
		if item.Kind() != "array_element_initializer" {
			continue
		}
		name, strNode := arrayItemNameAndNode(kind, item, src)
		if name == "" || strNode == nil {
			continue
		}
		if offset >= int(strNode.StartByte()) && offset < int(strNode.EndByte()) {
			return name, strNode
		}
	}
	return "", nil
}

// stringValue extracts the unquoted content of a PHP string literal node.
// tree-sitter-php represents 'email' as: string { ' string_content "email" ' }.
func stringValue(n *ts.Node, src []byte) string {
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if child.Kind() == "string_content" {
			return phpnode.NodeText(child, src)
		}
	}
	// Fallback: strip surrounding quote characters from the raw node text.
	v := phpnode.NodeText(n, src)
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') {
		return v[1 : len(v)-1]
	}
	return v
}
