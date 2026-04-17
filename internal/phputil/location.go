package phputil

import "github.com/VKCOM/php-parser/pkg/position"

// Location is a source position used as a jump target or source reference.
// Line numbers are 1-indexed. StartByte / EndByte are file-level byte offsets
// (the raw StartPos / EndPos from the VKCOM parser); they are converted to
// UTF-16 columns when constructing LSP responses.
type Location struct {
	Path      string
	StartLine int
	StartByte int // file-level byte offset (pos.StartPos)
	EndLine   int
	EndByte   int // file-level byte offset (pos.EndPos)
}

// Zero reports whether the location is unset.
func (l Location) Zero() bool { return l.Path == "" }

// FromPosition builds a Location from a VKCOM parser position.
func FromPosition(path string, pos *position.Position) Location {
	if pos == nil {
		return Location{Path: path}
	}
	return Location{
		Path:      path,
		StartLine: pos.StartLine,
		StartByte: pos.StartPos,
		EndLine:   pos.EndLine,
		EndByte:   pos.EndPos,
	}
}
