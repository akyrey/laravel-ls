package phputil

import "strings"

// ClassFQN computes the fully-qualified class name from a short name and the
// current file context. Equivalent to the former ClassNodeFQN helper but
// operates on a plain string rather than an AST node.
func ClassFQN(shortName string, fc *FileContext) FQN {
	if shortName == "" {
		return ""
	}
	if fc.Namespace == "" {
		return FQN(shortName)
	}
	return FQN(string(fc.Namespace) + "\\" + shortName)
}

// LastSegment returns the last backslash-separated segment of a FQN string,
// which is the short alias used in use-statements without an explicit alias.
func LastSegment(name string) string {
	parts := strings.Split(name, "\\")
	return parts[len(parts)-1]
}
