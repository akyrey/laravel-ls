package phputil

// ResolveReachable returns the subset of fqns that, by following the chain
// returned by extendsOf, eventually reach baseFQN (fqns equal to baseFQN
// itself count as reachable). extendsOf returns the direct parent FQN for
// fqn, or "" when unknown or when fqn has no parent.
//
// Used to answer "does this class transitively extend X?" for an entire
// symbol table in one pass, memoizing each chain so no class is walked twice.
func ResolveReachable(fqns []FQN, extendsOf func(FQN) FQN, baseFQN FQN) map[FQN]struct{} {
	memo := make(map[FQN]bool, len(fqns))

	var check func(fqn FQN) bool
	check = func(fqn FQN) bool {
		if v, seen := memo[fqn]; seen {
			return v
		}
		if fqn == baseFQN {
			memo[fqn] = true
			return true
		}
		parent := extendsOf(fqn)
		if parent == "" {
			memo[fqn] = false
			return false
		}
		// Mark before recursing so a cyclic extends chain (A extends B,
		// B extends A) terminates as not-reachable instead of overflowing
		// the stack.
		memo[fqn] = false
		result := check(parent)
		memo[fqn] = result
		return result
	}

	out := make(map[FQN]struct{})
	for _, fqn := range fqns {
		if check(fqn) {
			out[fqn] = struct{}{}
		}
	}
	return out
}
