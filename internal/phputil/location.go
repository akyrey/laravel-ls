package phputil

// Location is a source position used as a jump target or source reference.
// Line numbers are 1-indexed. StartByte is the byte offset of the first byte
// of the node (inclusive). EndByte is one past the last byte (exclusive) —
// matching tree-sitter's EndByte convention and LSP's exclusive range end.
// They are converted to UTF-16 columns when constructing LSP responses.
type Location struct {
	Path      string
	StartLine int
	StartByte int
	EndLine   int
	EndByte   int
}

// Zero reports whether the location is unset.
func (l Location) Zero() bool { return l.Path == "" }
