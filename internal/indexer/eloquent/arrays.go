package eloquent

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

// arrayProps maps PHP property names to their AttributeKind.
var arrayProps = map[string]AttributeKind{
	"fillable": FillableArray,
	"casts":    CastArray,
	"appends":  AppendsArray,
	"hidden":   HiddenArray,
}

// extractArrayProperties inspects $fillable, $casts, $appends, $hidden on
// the class and returns a ModelAttribute for each string entry found.
func extractArrayProperties(path string, classNode *ast.StmtClass) []ModelAttribute {
	var out []ModelAttribute
	for _, stmt := range classNode.Stmts {
		pl, ok := stmt.(*ast.StmtPropertyList)
		if !ok {
			continue
		}
		for _, prop := range pl.Props {
			p, ok := prop.(*ast.StmtProperty)
			if !ok {
				continue
			}
			ev, ok := p.Var.(*ast.ExprVariable)
			if !ok {
				continue
			}
			id, ok := ev.Name.(*ast.Identifier)
			if !ok {
				continue
			}
			propName := string(id.Value)
			// Identifier.Value for a property includes the $ sigil.
			if len(propName) > 0 && propName[0] == '$' {
				propName = propName[1:]
			}
			kind, ok := arrayProps[propName]
			if !ok {
				continue
			}
			arr, ok := p.Expr.(*ast.ExprArray)
			if !ok {
				continue
			}
			loc := phputil.FromPosition(path, p.GetPosition())
			for _, item := range arr.Items {
				ai, ok := item.(*ast.ExprArrayItem)
				if !ok {
					continue
				}
				name := arrayItemName(kind, ai)
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

// arrayItemName extracts the exposed attribute name from an array item.
// For $casts, the key is the exposed name (associative: 'col' => 'type').
// For $fillable / $appends / $hidden, the value is the name (sequential list).
func arrayItemName(kind AttributeKind, ai *ast.ExprArrayItem) string {
	switch kind {
	case CastArray:
		if ai.Key != nil {
			return stringLiteralValue(ai.Key)
		}
	default:
		return stringLiteralValue(ai.Val)
	}
	return ""
}

// stringLiteralValue returns the unquoted string value for a ScalarString node,
// or "" if the node is not a string literal.
func stringLiteralValue(node ast.Vertex) string {
	s, ok := node.(*ast.ScalarString)
	if !ok {
		return ""
	}
	v := string(s.Value)
	// Parser preserves quotes: strip surrounding ' or ".
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') {
		return v[1 : len(v)-1]
	}
	return v
}
