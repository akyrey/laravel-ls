package phpparse

import (
	"fmt"
	"os"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	phperrors "github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
)

// php81 is the highest version the parser officially supports. Files using
// PHP 8.2+ syntax (e.g. typed class constants in PHP 8.3) may produce parse
// errors which are logged and skipped; the partial AST is still processed.
var php81 = &version.Version{Major: 8, Minor: 1}

// Bytes parses src as PHP 8.1. Parse errors are written to stderr using path
// for context; a partial AST is still returned on non-fatal errors.
func Bytes(src []byte, path string) (ast.Vertex, error) {
	cfg := conf.Config{
		Version: php81,
		ErrorHandlerFunc: func(e *phperrors.Error) {
			fmt.Fprintf(os.Stderr, "laravel-lsp: parse error in %s: %s\n", path, e.String())
		},
	}
	root, err := parser.Parse(src, cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return root, nil
}

// File reads path and parses it as PHP 8.1. Parse errors are logged to stderr
// but do not abort the call — the partial AST is still processed.
func File(path string) (ast.Vertex, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Bytes(src, path)
}
