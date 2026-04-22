package eloquent

import (
	"regexp"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/akyrey/laravel-lsp/internal/phputil"
)

// isRelationType returns true if fqn is one of the built-in Eloquent relation
// classes (all live under Illuminate\Database\Eloquent\Relations\).
func isRelationType(fqn phputil.FQN) bool {
	return strings.HasPrefix(string(fqn), eloquentRelationsPrefix)
}

// relationBuilderMethods is the set of Eloquent $this->method() calls whose
// first argument is the related model class.
var relationBuilderMethods = map[string]bool{
	"hasOne": true, "hasMany": true,
	"belongsTo": true, "belongsToMany": true,
	"hasOneThrough": true, "hasManyThrough": true,
	"morphOne": true, "morphMany": true,
	"morphTo": true, "morphToMany": true, "morphedByMany": true,
}

// extractRelatedFQN inspects the method body for the pattern
// `return $this->relationMethod(RelatedClass::class, ...)` and returns the
// resolved FQN of RelatedClass. Returns "" when the pattern is not matched.
func extractRelatedFQN(method *ast.StmtClassMethod, fc *phputil.FileContext) phputil.FQN {
	stmtList, ok := method.Stmt.(*ast.StmtStmtList)
	if !ok || stmtList == nil {
		return ""
	}
	for _, stmt := range stmtList.Stmts {
		ret, ok := stmt.(*ast.StmtReturn)
		if !ok || ret.Expr == nil {
			continue
		}
		call, ok := ret.Expr.(*ast.ExprMethodCall)
		if !ok {
			continue
		}
		varExpr, ok := call.Var.(*ast.ExprVariable)
		if !ok {
			continue
		}
		varID, ok := varExpr.Name.(*ast.Identifier)
		if !ok {
			continue
		}
		if string(varID.Value) != "$this" && string(varID.Value) != "this" {
			continue
		}
		methodID, ok := call.Method.(*ast.Identifier)
		if !ok || !relationBuilderMethods[string(methodID.Value)] {
			continue
		}
		if len(call.Args) == 0 {
			continue
		}
		arg, ok := call.Args[0].(*ast.Argument)
		if !ok {
			continue
		}
		fetch, ok := arg.Expr.(*ast.ExprClassConstFetch)
		if !ok {
			continue
		}
		constID, ok := fetch.Const.(*ast.Identifier)
		if !ok || string(constID.Value) != "class" {
			continue
		}
		name := phputil.NameToString(fetch.Class)
		if name == "" {
			continue
		}
		return fc.Resolve(name)
	}
	return ""
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
		// Typed relationship: return type is in the Eloquent Relations namespace.
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
						RelatedFQN:  extractRelatedFQN(m, fc),
					})
					continue
				}
			}
		}

		// Untyped relationship: no return-type annotation, but the method body
		// contains `return $this->relationMethod(RelatedClass::class, ...)`.
		if relatedFQN := extractRelatedFQN(m, fc); relatedFQN != "" {
			out = append(out, ModelAttribute{
				ExposedName: methodName,
				MethodName:  methodName,
				Kind:        Relationship,
				Source:      SourceAST,
				Location:    loc,
				RelatedFQN:  relatedFQN,
			})
			continue
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
