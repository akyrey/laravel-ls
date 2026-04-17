package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestPositionToByteOffset_ASCII(t *testing.T) {
	src := []byte("<?php\n$user->name;\n")
	// Line 1 (0-based), character 0 → byte offset of '$'
	off := positionToByteOffset(src, protocol.Position{Line: 1, Character: 0})
	if src[off] != '$' {
		t.Errorf("want '$', got %q at offset %d", src[off], off)
	}
}

func TestPositionToByteOffset_MidLine(t *testing.T) {
	src := []byte("<?php\n$user->name;\n")
	// Line 1, character 5 → 'name'[0] offset (after "$user->")? No — "$user->" is 7 chars.
	// "$user->name" → character 7 is 'n'
	off := positionToByteOffset(src, protocol.Position{Line: 1, Character: 7})
	if src[off] != 'n' {
		t.Errorf("want 'n', got %q at offset %d", src[off], off)
	}
}

func TestPositionToByteOffset_UTF16Surrogate(t *testing.T) {
	// 𝄞 is U+1D11E (musical symbol G clef) — takes 4 bytes in UTF-8, 2 UTF-16 units.
	// Line: "𝄞x" — UTF-16 column 2 = 'x'
	src := []byte("\xf0\x9d\x84\x9ex") // 𝄞x
	off := positionToByteOffset(src, protocol.Position{Line: 0, Character: 2})
	if src[off] != 'x' {
		t.Errorf("want 'x', got %q at offset %d", src[off], off)
	}
}

func TestCountUTF16Units_ASCII(t *testing.T) {
	if n := countUTF16Units([]byte("hello")); n != 5 {
		t.Errorf("want 5, got %d", n)
	}
}

func TestCountUTF16Units_Multibyte(t *testing.T) {
	// é = U+00E9, 2 UTF-8 bytes, 1 UTF-16 unit
	if n := countUTF16Units([]byte("\xc3\xa9")); n != 1 {
		t.Errorf("want 1, got %d", n)
	}
}

func TestCountUTF16Units_Supplementary(t *testing.T) {
	// 𝄞 = U+1D11E, 4 UTF-8 bytes, 2 UTF-16 units
	if n := countUTF16Units([]byte("\xf0\x9d\x84\x9e")); n != 2 {
		t.Errorf("want 2, got %d", n)
	}
}

func TestUTF16ColFromFileOffset(t *testing.T) {
	// File: "<?php\nreturn 'hello';\n"
	// Line 2 (1-based), 'hello' starts at byte offset 14 in the file ("<?php\nreturn '")
	src := []byte("<?php\nreturn 'hello';\n")
	fileOffset := 14 // byte offset of 'h' in 'hello'
	col := utf16ColFromFileOffset(src, 2, fileOffset)
	if col != 8 { // "return '" is 8 UTF-16 units
		t.Errorf("want 8, got %d", col)
	}
}

func TestUTF16ColFromFileOffset_FirstLine(t *testing.T) {
	src := []byte("<?php echo 'hi';")
	// byte offset 11 = 'h' in 'hi'
	col := utf16ColFromFileOffset(src, 1, 11)
	if col != 11 {
		t.Errorf("want 11, got %d", col)
	}
}
