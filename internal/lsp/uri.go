package lsp

import (
	"bytes"
	"os"
	"unicode/utf8"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-ls/internal/phputil"
)

// boolPtr returns a pointer to b. Used when a protocol field requires *bool.
func boolPtr(b bool) *bool { return &b }

// URIToPath converts a file:// URI to an absolute filesystem path.
func URIToPath(uri protocol.DocumentUri) string {
	s := string(uri)
	if len(s) >= 7 && s[:7] == "file://" {
		return s[7:]
	}
	return s
}

// PathToURI converts an absolute filesystem path to a file:// URI.
func PathToURI(path string) protocol.DocumentUri {
	return protocol.DocumentUri("file://" + path)
}

// toLSPLocation converts a phputil.Location to an LSP protocol.Location.
// It reads the target file from disk to compute the correct UTF-16 column.
// On any I/O error the column falls back to 0.
func toLSPLocation(loc phputil.Location) protocol.Location {
	line := uint32(0)
	if loc.StartLine > 0 {
		line = uint32(loc.StartLine - 1)
	}

	var col uint32
	if loc.StartByte > 0 {
		if src, err := os.ReadFile(loc.Path); err == nil {
			col = utf16ColFromFileOffset(src, loc.StartLine, loc.StartByte)
		}
	}

	return protocol.Location{
		URI: PathToURI(loc.Path),
		Range: protocol.Range{
			Start: protocol.Position{Line: line, Character: col},
			End:   protocol.Position{Line: line, Character: col},
		},
	}
}

// toLSPRange converts a phputil.Location to an LSP Range.
// VKCOM parser EndPos is exclusive (one past the last byte), matching LSP's
// exclusive end convention. Reads src from disk when src is nil; on failure
// the range collapses to the start.
func toLSPRange(loc phputil.Location, src []byte) protocol.Range {
	if src == nil {
		var err error
		src, err = os.ReadFile(loc.Path)
		if err != nil {
			src = nil
		}
	}
	startLine := uint32(0)
	if loc.StartLine > 0 {
		startLine = uint32(loc.StartLine - 1)
	}
	endLine := uint32(startLine)
	if loc.EndLine > 0 {
		endLine = uint32(loc.EndLine - 1)
	}
	var startCol, endCol uint32
	if src != nil {
		startCol = utf16ColFromFileOffset(src, loc.StartLine, loc.StartByte)
		endCol = utf16ColFromFileOffset(src, loc.EndLine, loc.EndByte)
	}
	return protocol.Range{
		Start: protocol.Position{Line: startLine, Character: startCol},
		End:   protocol.Position{Line: endLine, Character: endCol},
	}
}

// positionToByteOffset converts an LSP Position (UTF-16 column) to a byte
// offset in src.
func positionToByteOffset(src []byte, pos protocol.Position) int {
	line := int(pos.Line)
	utf16Col := int(pos.Character)

	// Advance to the start of the target line.
	offset := 0
	for i := 0; i < line; i++ {
		idx := bytes.IndexByte(src[offset:], '\n')
		if idx < 0 {
			return len(src)
		}
		offset += idx + 1
	}

	// Walk rune-by-rune, consuming UTF-16 code units.
	remaining := utf16Col
	for remaining > 0 && offset < len(src) && src[offset] != '\n' {
		r, size := utf8.DecodeRune(src[offset:])
		units := 1
		if r >= 0x10000 {
			units = 2 // supplementary character = surrogate pair
		}
		if remaining < units {
			break
		}
		remaining -= units
		offset += size
	}

	if offset > len(src) {
		return len(src)
	}
	return offset
}

// utf16ColFromFileOffset computes the zero-based UTF-16 column for a
// file-level byte offset. lineNum is 1-based.
func utf16ColFromFileOffset(src []byte, lineNum int, fileOffset int) uint32 {
	if len(src) == 0 || fileOffset <= 0 {
		return 0
	}
	lineStart := 0
	for l := 1; l < lineNum; l++ {
		idx := bytes.IndexByte(src[lineStart:], '\n')
		if idx < 0 {
			return 0
		}
		lineStart += idx + 1
	}
	if fileOffset < lineStart || fileOffset > len(src) {
		return 0
	}
	return countUTF16Units(src[lineStart:fileOffset])
}

// countUTF16Units returns the number of UTF-16 code units needed to represent
// the UTF-8 encoded string in b.
func countUTF16Units(b []byte) uint32 {
	var n uint32
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		b = b[size:]
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}
