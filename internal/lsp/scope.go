package lsp

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// modelStaticMethods is the set of Eloquent static methods that return a model
// instance (or collection element). We treat the return type as the receiver class.
var modelStaticMethods = map[string]bool{
	"find":           true,
	"findOrFail":     true,
	"findOrNew":      true,
	"first":          true,
	"firstOrFail":    true,
	"firstOrCreate":  true,
	"firstOrNew":     true,
	"updateOrCreate": true,
	"create":         true,
	"make":           true,
	"forceCreate":    true,
	"sole":           true,
}

// collectAssignments walks the body of a method and records variable → model FQN
// mappings inferred from:
//
//	$var = new ClassName(...)
//	$var = ClassName::staticMethod(...)   where staticMethod is in modelStaticMethods
func collectAssignments(method phpwalk.MethodInfo, fc *phputil.FileContext) map[string]phputil.FQN {
	bodyNode := method.Raw.ChildByFieldName("body")
	if bodyNode == nil {
		return nil
	}
	av := &assignCollector{fc: fc, src: method.Src, vars: make(map[string]phputil.FQN)}
	phpwalk.WalkNode("", method.Src, bodyNode, av)
	return av.vars
}

type assignCollector struct {
	phpwalk.NullVisitor
	fc   *phputil.FileContext
	src  []byte
	vars map[string]phputil.FQN
}

func (v *assignCollector) VisitAssign(n phpwalk.AssignInfo) {
	if n.VarName == "" || n.RHSRaw == nil {
		return
	}
	fqn := assignRHSFQN(n.RHSRaw, n.Src, v.fc)
	if fqn != "" {
		v.vars[n.VarName] = fqn
	}
}

// assignRHSFQN extracts a model FQN from a right-hand side expression.
// Handles `new X(...)` and `X::modelStaticMethod(...)`.
func assignRHSFQN(rhs *ts.Node, src []byte, fc *phputil.FileContext) phputil.FQN {
	switch rhs.Kind() {
	case "object_creation_expression":
		for i := uint(0); i < rhs.ChildCount(); i++ {
			child := rhs.Child(i)
			if child.Kind() == "qualified_name" || child.Kind() == "name" {
				return fc.Resolve(phpnode.NodeText(child, src))
			}
		}
	case "scoped_call_expression":
		nameNode := rhs.ChildByFieldName("name")
		if nameNode == nil || !modelStaticMethods[phpnode.NodeText(nameNode, src)] {
			return ""
		}
		scopeNode := rhs.ChildByFieldName("scope")
		if scopeNode == nil {
			return ""
		}
		return fc.Resolve(phpnode.NodeText(scopeNode, src))
	}
	return ""
}

// resolveVarFQN resolves a variable name to its model FQN by checking, in order:
//  1. Typed method parameters ($var User).
//  2. Assignment-inferred types collected by collectAssignments.
func resolveVarFQN(varVal string, params []phpwalk.ParamInfo, assignedVars map[string]phputil.FQN, fc *phputil.FileContext) phputil.FQN {
	for _, p := range params {
		if p.VarName == varVal && p.TypeText != "" {
			return fc.Resolve(p.TypeText)
		}
	}
	return assignedVars[varVal]
}

// resolveExprType recursively resolves an arbitrary tree-sitter expression node
// to a model FQN.  Handles:
//   - variable_name        → variable lookup / $this
//   - member_access_expression → chain: resolve LHS, look up Relationship.RelatedFQN
//
// Returns "" when resolution is not possible. Callers must treat "" as unknown.
func resolveExprType(
	expr *ts.Node,
	src []byte,
	encClass phputil.FQN,
	params []phpwalk.ParamInfo,
	assignedVars map[string]phputil.FQN,
	fc *phputil.FileContext,
	models *eloquent.ModelIndex,
) phputil.FQN {
	if expr == nil {
		return ""
	}
	switch expr.Kind() {
	case "variable_name":
		varVal := phpnode.NodeText(expr, src)
		if varVal == "$this" {
			return encClass
		}
		return resolveVarFQN(varVal, params, assignedVars, fc)

	case "member_access_expression", "nullsafe_member_access_expression":
		propNode := expr.ChildByFieldName("name")
		if propNode == nil {
			return ""
		}
		objNode := expr.ChildByFieldName("object")
		if objNode == nil {
			return ""
		}
		lhsFQN := resolveExprType(objNode, src, encClass, params, assignedVars, fc, models)
		if lhsFQN == "" {
			return ""
		}
		cat := models.Lookup(lhsFQN)
		if cat == nil {
			return ""
		}
		propName := phpnode.NodeText(propNode, src)
		for _, a := range cat.ByExposed[propName] {
			if a.Kind == eloquent.Relationship && a.RelatedFQN != "" {
				return a.RelatedFQN
			}
		}
	}
	return ""
}
