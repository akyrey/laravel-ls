package lsp

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phputil"
)

// modelStaticMethods is the set of Eloquent static methods that return a model
// instance (or collection element). We treat the return type as the receiver class.
var modelStaticMethods = map[string]bool{
	"find":            true,
	"findOrFail":      true,
	"findOrNew":       true,
	"first":           true,
	"firstOrFail":     true,
	"firstOrCreate":   true,
	"firstOrNew":      true,
	"updateOrCreate":  true,
	"create":          true,
	"make":            true,
	"forceCreate":     true,
	"sole":            true,
}

// collectAssignments walks the body of method and records variable → model FQN
// mappings inferred from:
//
//	$var = new ClassName(...)
//	$var = ClassName::staticMethod(...)   where staticMethod is in modelStaticMethods
func collectAssignments(method *ast.StmtClassMethod, fc *phputil.FileContext) map[string]phputil.FQN {
	stmtList, ok := method.Stmt.(*ast.StmtStmtList)
	if !ok || stmtList == nil {
		return nil
	}
	av := &assignVisitor{fc: fc, vars: make(map[string]phputil.FQN)}
	traverser.NewTraverser(av).Traverse(stmtList)
	return av.vars
}

// assignVisitor collects $var → FQN from assignment expressions.
type assignVisitor struct {
	visitor.Null
	fc   *phputil.FileContext
	vars map[string]phputil.FQN
}

func (v *assignVisitor) ExprAssign(n *ast.ExprAssign) {
	lhsVar, ok := n.Var.(*ast.ExprVariable)
	if !ok {
		return
	}
	lhsID, ok := lhsVar.Name.(*ast.Identifier)
	if !ok {
		return
	}
	varName := string(lhsID.Value) // includes "$"

	fqn := v.rhsFQN(n.Expr)
	if fqn != "" {
		v.vars[varName] = fqn
	}
}

func (v *assignVisitor) rhsFQN(expr ast.Vertex) phputil.FQN {
	switch rhs := expr.(type) {
	case *ast.ExprNew:
		name := phputil.NameToString(rhs.Class)
		if name == "" {
			return ""
		}
		return v.fc.Resolve(name)

	case *ast.ExprStaticCall:
		callID, ok := rhs.Call.(*ast.Identifier)
		if !ok {
			return ""
		}
		if !modelStaticMethods[string(callID.Value)] {
			return ""
		}
		name := phputil.NameToString(rhs.Class)
		if name == "" {
			return ""
		}
		return v.fc.Resolve(name)
	}
	return ""
}

// resolveVarFQN resolves a variable name to its model FQN by checking, in order:
//  1. Typed method parameters ($var User).
//  2. Assignment-inferred types collected by collectAssignments.
func resolveVarFQN(varVal string, method *ast.StmtClassMethod, assignedVars map[string]phputil.FQN, fc *phputil.FileContext) phputil.FQN {
	if fqn := resolveParamType(varVal, method, fc); fqn != "" {
		return fqn
	}
	return assignedVars[varVal]
}

// resolveExprType recursively resolves an arbitrary expression to a model FQN.
// Handles:
//   - *ast.ExprVariable          → variable lookup via resolveVarFQN / $this
//   - *ast.ExprPropertyFetch     → chain: resolve LHS, look up Relationship.RelatedFQN
//
// Returns "" when resolution is not possible (unknown variable, non-model
// property, missing RelatedFQN, etc.). Callers must treat "" as "unknown".
func resolveExprType(
	expr ast.Vertex,
	encClass phputil.FQN,
	method *ast.StmtClassMethod,
	assignedVars map[string]phputil.FQN,
	fc *phputil.FileContext,
	models *eloquent.ModelIndex,
) phputil.FQN {
	switch e := expr.(type) {
	case *ast.ExprVariable:
		id, ok := e.Name.(*ast.Identifier)
		if !ok {
			return ""
		}
		varVal := string(id.Value)
		if varVal == "$this" || varVal == "this" {
			return encClass
		}
		return resolveVarFQN(varVal, method, assignedVars, fc)

	case *ast.ExprPropertyFetch:
		prop, ok := e.Prop.(*ast.Identifier)
		if !ok {
			return ""
		}
		lhsFQN := resolveExprType(e.Var, encClass, method, assignedVars, fc, models)
		if lhsFQN == "" {
			return ""
		}
		cat := models.Lookup(lhsFQN)
		if cat == nil {
			return ""
		}
		for _, a := range cat.ByExposed[string(prop.Value)] {
			if a.Kind == eloquent.Relationship && a.RelatedFQN != "" {
				return a.RelatedFQN
			}
		}
		return ""
	}
	return ""
}
