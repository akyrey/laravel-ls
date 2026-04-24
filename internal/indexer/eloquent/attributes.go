package eloquent

import (
	"regexp"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
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

var (
	legacyGetterRe = regexp.MustCompile(`^get([A-Z].+)Attribute$`)
	legacySetterRe = regexp.MustCompile(`^set([A-Z].+)Attribute$`)
)

// extractMethods inspects every method_declaration in classNode and returns
// ModelAttribute entries for modern accessors, relationships, and legacy
// accessor/mutators.
func extractMethods(path string, classNode *ts.Node, src []byte, fc *phputil.FileContext) []ModelAttribute {
	bodyNode := classNode.ChildByFieldName("body")
	if bodyNode == nil {
		return nil
	}

	var out []ModelAttribute
	for i := uint(0); i < bodyNode.ChildCount(); i++ {
		m := bodyNode.Child(i)
		if m.Kind() != "method_declaration" {
			continue
		}

		nameNode := m.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		methodName := phpnode.NodeText(nameNode, src)
		if methodName == "" {
			continue
		}

		loc := phpnode.FromNode(path, m)

		// Modern accessor or typed relationship: inspect return type.
		if rtNode := m.ChildByFieldName("return_type"); rtNode != nil {
			rtText := phpwalk.UnwrapTypeName(rtNode, src)
			if rtText != "" {
				rtFQN := fc.Resolve(rtText)
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
						ExposedName: methodName,
						MethodName:  methodName,
						Kind:        Relationship,
						Source:      SourceAST,
						Location:    loc,
						RelatedFQN:  extractRelatedFQN(m, src, fc),
					})
					continue
				}
			}
		}

		// Untyped relationship: body contains $this->relationMethod(Class::class).
		if relFQN := extractRelatedFQN(m, src, fc); relFQN != "" {
			out = append(out, ModelAttribute{
				ExposedName: methodName,
				MethodName:  methodName,
				Kind:        Relationship,
				Source:      SourceAST,
				Location:    loc,
				RelatedFQN:  relFQN,
			})
			continue
		}

		// Legacy accessor: getXxxAttribute()
		if m2 := legacyGetterRe.FindStringSubmatch(methodName); m2 != nil {
			exposed := phputil.Snake(strings.ToLower(m2[1][:1]) + m2[1][1:])
			out = append(out, ModelAttribute{
				ExposedName: exposed,
				Kind:        LegacyAccessor,
				Source:      SourceAST,
				Location:    loc,
			})
			continue
		}

		// Legacy mutator: setXxxAttribute()
		if m2 := legacySetterRe.FindStringSubmatch(methodName); m2 != nil {
			exposed := phputil.Snake(strings.ToLower(m2[1][:1]) + m2[1][1:])
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

// extractRelatedFQN inspects the method body for the pattern
// `return $this->relationMethod(RelatedClass::class, ...)` and returns the
// resolved FQN of RelatedClass. Returns "" when the pattern is not matched.
func extractRelatedFQN(methodNode *ts.Node, src []byte, fc *phputil.FileContext) phputil.FQN {
	bodyNode := methodNode.ChildByFieldName("body")
	if bodyNode == nil {
		return ""
	}
	for i := uint(0); i < bodyNode.ChildCount(); i++ {
		stmt := bodyNode.Child(i)
		if stmt.Kind() != "return_statement" {
			continue
		}
		// Find the member_call_expression inside the return statement.
		for j := uint(0); j < stmt.ChildCount(); j++ {
			expr := stmt.Child(j)
			if expr.Kind() != "member_call_expression" {
				continue
			}
			// Check $this->relationMethod(...)
			objNode := expr.ChildByFieldName("object")
			if objNode == nil || objNode.Kind() != "variable_name" {
				continue
			}
			if phpnode.NodeText(objNode, src) != "$this" {
				continue
			}
			nameNode := expr.ChildByFieldName("name")
			if nameNode == nil || !relationBuilderMethods[phpnode.NodeText(nameNode, src)] {
				continue
			}
			argsNode := expr.ChildByFieldName("arguments")
			if argsNode == nil {
				continue
			}
			args := phpwalk.ArgExprs(argsNode, src)
			if len(args) == 0 {
				continue
			}
			if fqn := phpwalk.ClassConstFQN(args[0], src, fc); fqn != "" {
				return fqn
			}
		}
	}
	return ""
}
