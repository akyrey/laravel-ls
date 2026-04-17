package phputil

import (
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
)

// ClassName extracts the string name from an ast.Identifier or ast.Name node.
// Returns empty string if the node is not a recognized name type.
func ClassName(node ast.Vertex) string {
	switch n := node.(type) {
	case *ast.Identifier:
		return string(n.Value)
	case *ast.Name:
		return joinNameParts(n.Parts)
	case *ast.NameFullyQualified:
		return joinNameParts(n.Parts)
	case *ast.NameRelative:
		return joinNameParts(n.Parts)
	}
	return ""
}

// NameToString converts an ast.Name (or NameFullyQualified / NameRelative)
// to a string using backslash as separator.
func NameToString(node ast.Vertex) string {
	switch n := node.(type) {
	case *ast.Name:
		return joinNameParts(n.Parts)
	case *ast.NameFullyQualified:
		return "\\" + joinNameParts(n.Parts)
	case *ast.NameRelative:
		return joinNameParts(n.Parts)
	case *ast.Identifier:
		return string(n.Value)
	}
	return ""
}

// AddUsesToContext adds use-declaration items into fc.Uses.
// prefix is prepended for group uses (StmtGroupUseList); pass "" for regular uses.
func AddUsesToContext(fc *FileContext, uses []ast.Vertex, prefix string) {
	for _, item := range uses {
		u, ok := item.(*ast.StmtUse)
		if !ok {
			continue
		}
		name := NameToString(u.Use)
		if name == "" {
			continue
		}
		var fqn string
		if prefix != "" {
			fqn = prefix + "\\" + name
		} else {
			fqn = name
		}
		var alias string
		if u.Alias != nil {
			alias = NameToString(u.Alias)
		} else {
			parts := strings.Split(name, "\\")
			alias = parts[len(parts)-1]
		}
		fc.Uses[alias] = FQN(fqn)
	}
}

// ClassNodeFQN returns the fully-qualified name for a class or interface
// declaration node, using fc for namespace context.
// Returns "" for anonymous classes (nil or non-Identifier name node).
func ClassNodeFQN(name ast.Vertex, fc *FileContext) FQN {
	if name == nil {
		return ""
	}
	id, ok := name.(*ast.Identifier)
	if !ok {
		return ""
	}
	short := string(id.Value)
	if fc.Namespace == "" {
		return FQN(short)
	}
	return FQN(string(fc.Namespace) + "\\" + short)
}

func joinNameParts(parts []ast.Vertex) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += "\\"
		}
		if id, ok := part.(*ast.NamePart); ok {
			result += string(id.Value)
		}
	}
	return result
}
