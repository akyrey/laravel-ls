package resolver

import "github.com/akyrey/laravel-ls/internal/phputil"

// Scope tracks variable-to-FQN bindings within a single function/method body.
// It is built intraprocedurally (same function only) — no cross-function
// data flow. This deliberately limits resolution to the cases listed in the
// plan §"Expression type resolution".
type Scope struct {
	// enclosingClass is the FQN of the class containing this method, if any.
	// Used to resolve $this->.
	enclosingClass phputil.FQN

	// vars maps variable names (without $) to their known FQN type.
	// Only populated for the simple cases:
	//   - typed method parameters: `function show(User $user)` → vars["user"] = "App\\Models\\User"
	//   - typed properties: `private User $user` → vars["user"] = "App\\Models\\User"
	//   - direct assignments: `$user = new User(...)` / `User::find(...)` / etc.
	vars map[string]phputil.FQN
}

// NewScope creates a scope for a method inside the given class.
func NewScope(enclosingClass phputil.FQN) *Scope {
	return &Scope{
		enclosingClass: enclosingClass,
		vars:           make(map[string]phputil.FQN),
	}
}

// Set records that variable name (without $) has the given type FQN.
func (s *Scope) Set(name string, fqn phputil.FQN) {
	s.vars[name] = fqn
}

// Get returns the type FQN for variable name, or "" if unknown.
func (s *Scope) Get(name string) phputil.FQN {
	return s.vars[name]
}

// EnclosingClass returns the FQN of the class this scope belongs to.
func (s *Scope) EnclosingClass() phputil.FQN {
	return s.enclosingClass
}
