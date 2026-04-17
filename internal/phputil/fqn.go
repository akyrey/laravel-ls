package phputil

// FQN is a fully-qualified PHP class name, e.g. "App\\Models\\User".
// No leading backslash. Backslash is the namespace separator.
type FQN string

// UseMap maps import aliases to their FQN within a single file.
// Given `use App\Services\StripeGateway as Gateway`, the key is "Gateway".
// Given `use App\Models\User`, the key is "User" (short name).
type UseMap map[string]FQN

// FileContext holds per-file resolution data needed to turn unqualified
// names and import aliases into fully-qualified names.
type FileContext struct {
	Path      string
	Namespace FQN
	Uses      UseMap
}

// Resolve turns a name as it appears in source into its FQN, using the
// file's namespace and use-statements. name must not have a leading backslash.
//
// Resolution rules mirror PHP:
//  1. If name contains no backslash, look it up in UseMap first.
//  2. If not found in UseMap and name contains no backslash,
//     prepend the file namespace.
//  3. If name starts with a backslash it is already fully-qualified;
//     strip the leading backslash.
func (fc *FileContext) Resolve(name string) FQN {
	if len(name) == 0 {
		return ""
	}
	if name[0] == '\\' {
		return FQN(name[1:])
	}
	// Unqualified or partially-qualified names: try use-map first.
	// For partially-qualified names (containing \), split on the first segment.
	firstSegment := name
	rest := ""
	for i, c := range name {
		if c == '\\' {
			firstSegment = name[:i]
			rest = name[i:] // includes leading backslash
			break
		}
	}
	if resolved, ok := fc.Uses[firstSegment]; ok {
		return FQN(string(resolved) + rest)
	}
	// Prepend namespace.
	if fc.Namespace == "" {
		return FQN(name)
	}
	return FQN(string(fc.Namespace) + "\\" + name)
}
