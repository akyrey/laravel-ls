package phpnode

import (
	"fmt"
	"os"

	ts "github.com/tree-sitter/go-tree-sitter"
	php "github.com/tree-sitter/tree-sitter-php/bindings/go"

	"github.com/akyrey/laravel-lsp/internal/phputil"
)

var phpLang *ts.Language

func init() {
	phpLang = ts.NewLanguage(php.LanguagePHP())
}

// ParseBytes parses src as PHP and returns the syntax tree.
// The caller should call tree.Close() when done; nodes from the tree must not
// be used after Close is called.
func ParseBytes(src []byte) (*ts.Tree, error) {
	p := ts.NewParser()
	defer p.Close()
	if err := p.SetLanguage(phpLang); err != nil {
		return nil, fmt.Errorf("phpnode: set language: %w", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("phpnode: parse returned nil")
	}
	return tree, nil
}

// ParseFile reads path and parses it as PHP.
// Returns (src, tree, error); src is needed to extract node text.
func ParseFile(path string) ([]byte, *ts.Tree, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	tree, err := ParseBytes(src)
	return src, tree, err
}

// NodeText returns the UTF-8 source text spanned by n.
func NodeText(n *ts.Node, src []byte) string {
	return n.Utf8Text(src)
}

// FromNode builds a phputil.Location from a tree-sitter node's byte offsets.
// tree-sitter rows are 0-based; Location lines are 1-based.
func FromNode(path string, n *ts.Node) phputil.Location {
	sp := n.StartPosition()
	ep := n.EndPosition()
	return phputil.Location{
		Path:      path,
		StartLine: int(sp.Row) + 1,
		StartByte: int(n.StartByte()),
		EndLine:   int(ep.Row) + 1,
		EndByte:   int(n.EndByte()),
	}
}
