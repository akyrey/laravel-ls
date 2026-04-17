package resolver

import "github.com/akyrey/laravel-ls/internal/phputil"

// ResolveThis resolves $this->propName within a method, returning the
// FQN of the enclosing class. It always succeeds when a scope is available.
func ResolveThis(scope *Scope) phputil.FQN {
	if scope == nil {
		return ""
	}
	return scope.EnclosingClass()
}

// ResolveVar resolves an arbitrary variable expression to a type FQN.
// Returns "" when resolution is not possible with our limited syntactic rules.
// Callers must treat "" as "unknown" and fall through to sibling LSPs.
//
// Handles:
//   - "$this" → enclosing class FQN
//   - named variable → scope lookup (populated from typed params/props/assignments)
//
// Does NOT handle: collections, query builder chains, closures capturing
// variables, ternaries, method-chain returns, etc.
func ResolveVar(varName string, scope *Scope) phputil.FQN {
	if scope == nil {
		return ""
	}
	if varName == "this" {
		return scope.EnclosingClass()
	}
	return scope.Get(varName)
}

// SupportedAssignmentSources lists the static method names we recognise as
// returning the model class itself (intraprocedural, same-class only).
// E.g. User::find(...) → "App\\Models\\User".
var SupportedAssignmentSources = []string{
	"find",
	"findOrFail",
	"first",
	"firstOrFail",
}
