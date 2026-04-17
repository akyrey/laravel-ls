package eloquent

import (
	"regexp"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

// isRelationType returns true if fqn is one of the built-in Eloquent relation
// classes (all live under Illuminate\Database\Eloquent\Relations\).
func isRelationType(fqn phputil.FQN) bool {
	return strings.HasPrefix(string(fqn), eloquentRelationsPrefix)
}

var (
	legacyGetterRe = regexp.MustCompile(`^get([A-Z].+)Attribute$`)
	legacySetterRe = regexp.MustCompile(`^set([A-Z].+)Attribute$`)
)

// unwrapReturnType strips a leading Nullable wrapper to get the underlying
// type node (e.g. ?Attribute → Attribute name node).
func unwrapReturnType(t ast.Vertex) ast.Vertex {
	if n, ok := t.(*ast.Nullable); ok {
		return n.Expr
	}
	return t
}

// extractMethods inspects every StmtClassMethod in classNode and returns
// ModelAttribute entries for modern accessors and legacy accessor/mutators.
func extractMethods(path string, classNode *ast.StmtClass, fc *phputil.FileContext) []ModelAttribute {
	var out []ModelAttribute
	for _, stmt := range classNode.Stmts {
		m, ok := stmt.(*ast.StmtClassMethod)
		if !ok {
			continue
		}
		methodName := phputil.NameToString(m.Name)
		if methodName == "" {
			continue
		}
		loc := phputil.FromPosition(path, m.GetPosition())

		// Modern accessor: return type resolves to the Eloquent Attribute class.
		// Relationship method: return type is in the Eloquent Relations namespace.
		if m.ReturnType != nil {
			rtNode := unwrapReturnType(m.ReturnType)
			rtName := phputil.NameToString(rtNode)
			if rtName != "" {
				rtFQN := fc.Resolve(rtName)
				switch {
				case rtFQN == eloquentAttributeTypeFQN:
					out = append(out, ModelAttribute{
						ExposedName: phputil.Snake(methodName),
						MethodName:  methodName,
						Kind:        ModernAccessor,
						Source:      SourceAST,
						Location:    loc,
					})
					continue
				case isRelationType(rtFQN):
					out = append(out, ModelAttribute{
						ExposedName: methodName, // no case translation for relations
						MethodName:  methodName,
						Kind:        Relationship,
						Source:      SourceAST,
						Location:    loc,
					})
					continue
				}
			}
		}

		// Legacy accessor: getXxxAttribute()
		if m := legacyGetterRe.FindStringSubmatch(methodName); m != nil {
			exposed := phputil.Snake(strings.ToLower(m[1][:1]) + m[1][1:])
			out = append(out, ModelAttribute{
				ExposedName: exposed,
				Kind:        LegacyAccessor,
				Source:      SourceAST,
				Location:    loc,
			})
			continue
		}

		// Legacy mutator: setXxxAttribute()
		if m := legacySetterRe.FindStringSubmatch(methodName); m != nil {
			exposed := phputil.Snake(strings.ToLower(m[1][:1]) + m[1][1:])
			out = append(out, ModelAttribute{
				ExposedName: exposed,
				Kind:        LegacyMutator,
				Source:      SourceAST,
				Location:    loc,
			})
			continue
		}
	}
	return out
}
