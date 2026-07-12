// Package strindex indexes Laravel's string-reference conventions: config
// keys (config/*.php), view names (resources/views/**/*.blade.php), route
// names (->name('...') in routes/*.php), and environment keys (.env /
// .env.example). The LSP layer uses it to resolve config('app.name'),
// view('users.index'), route('home'), and env('APP_KEY') calls.
package strindex

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// Index maps Laravel string references to their declaration sites.
type Index struct {
	// Config maps dot-notation keys ("app.name", "mail.mailers.smtp.host")
	// to the key's string node in the config file. Intermediate keys are
	// indexed too: config('app.log') is valid Laravel.
	Config map[string]phputil.Location
	// Views maps dot-notation view names ("users.index") to the blade file.
	Views map[string]phputil.Location
	// Routes maps route names ("users.index") to the ->name(...) argument.
	Routes map[string]phputil.Location
	// Env maps environment keys ("APP_KEY") to their line in .env, falling
	// back to .env.example for keys defined only there.
	Env map[string]phputil.Location
}

// New returns an empty, ready-to-use index.
func New() *Index {
	return &Index{
		Config: make(map[string]phputil.Location),
		Views:  make(map[string]phputil.Location),
		Routes: make(map[string]phputil.Location),
		Env:    make(map[string]phputil.Location),
	}
}

// Walk builds the index from Laravel's conventional locations under root.
// Missing directories and unparseable files are skipped silently — the index
// is best-effort by design.
func Walk(root string) *Index {
	idx := New()
	idx.walkConfig(filepath.Join(root, "config"))
	idx.walkViews(filepath.Join(root, "resources", "views"))
	idx.walkRoutes(filepath.Join(root, "routes"))
	idx.walkEnv(root)
	return idx
}

// — config —

func (idx *Index) walkConfig(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".php") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		// config/services/stripe.php → prefix "services.stripe".
		prefix := strings.ReplaceAll(strings.TrimSuffix(rel, ".php"), string(filepath.Separator), ".")

		src, tree, parseErr := phpnode.ParseFile(path)
		if parseErr != nil {
			return nil
		}
		defer tree.Close()

		if arr := topLevelReturnArray(tree.RootNode()); arr != nil {
			idx.collectConfigKeys(prefix, arr, src, path)
		}
		return nil
	})
}

// topLevelReturnArray finds the array literal of the file's top-level
// `return [...]` statement, or nil.
func topLevelReturnArray(program *ts.Node) *ts.Node {
	for i := uint(0); i < program.ChildCount(); i++ {
		stmt := program.Child(i)
		if stmt.Kind() != "return_statement" {
			continue
		}
		for j := uint(0); j < stmt.ChildCount(); j++ {
			if child := stmt.Child(j); child.Kind() == "array_creation_expression" {
				return child
			}
		}
	}
	return nil
}

// collectConfigKeys records every string-keyed entry of arr under prefix,
// recursing into nested array values so deep keys resolve too.
func (idx *Index) collectConfigKeys(prefix string, arr *ts.Node, src []byte, path string) {
	for i := uint(0); i < arr.ChildCount(); i++ {
		item := arr.Child(i)
		if item.Kind() != "array_element_initializer" {
			continue
		}
		keyNode, valNode := keyAndValue(item)
		key := phpwalk.StringValue(keyNode, src)
		if key == "" {
			continue // list item or non-string key
		}
		full := prefix + "." + key
		idx.Config[full] = phpnode.FromNode(path, keyNode)
		if valNode != nil && valNode.Kind() == "array_creation_expression" {
			idx.collectConfigKeys(full, valNode, src, path)
		}
	}
}

// keyAndValue splits an array_element_initializer into its key string node
// (nil when the element has no `=>`) and the value expression node.
func keyAndValue(item *ts.Node) (key, value *ts.Node) {
	arrowSeen := false
	for i := uint(0); i < item.ChildCount(); i++ {
		child := item.Child(i)
		switch {
		case child.Kind() == "=>":
			arrowSeen = true
		case !arrowSeen && phpwalk.IsStringLiteral(child) && key == nil:
			key = child
		case arrowSeen && child.IsNamed() && value == nil:
			value = child
		}
	}
	if !arrowSeen {
		return nil, nil
	}
	return key, value
}

// — views —

func (idx *Index) walkViews(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".blade.php") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		name := strings.ReplaceAll(strings.TrimSuffix(rel, ".blade.php"), string(filepath.Separator), ".")
		idx.Views[name] = phputil.Location{Path: path, StartLine: 1, EndLine: 1}
		return nil
	})
}

// — env —

// envLineRe matches `KEY=` at the start of a line, with an optional
// `export ` prefix. Group 1 is the prefix (if any), group 2 the key.
var envLineRe = regexp.MustCompile(`^(\s*(?:export\s+)?)([A-Za-z_][A-Za-z0-9_]*)=`)

// walkEnv indexes .env and .env.example under root. .env.example is read
// first so real .env entries win per key.
func (idx *Index) walkEnv(root string) {
	for _, name := range []string{".env.example", ".env"} {
		path := filepath.Join(root, name)
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		collectEnvKeys(idx.Env, src, path)
	}
}

// collectEnvKeys records every KEY=value line of a dotenv file, keyed by
// name with the key token's exact byte range as the location.
func collectEnvKeys(out map[string]phputil.Location, src []byte, path string) {
	lineStart := 0
	for lineNo := 1; lineStart <= len(src); lineNo++ {
		lineEnd := len(src)
		if i := bytes.IndexByte(src[lineStart:], '\n'); i >= 0 {
			lineEnd = lineStart + i
		}
		line := src[lineStart:lineEnd]
		if m := envLineRe.FindSubmatch(line); m != nil {
			keyStart := lineStart + len(m[1])
			key := string(m[2])
			out[key] = phputil.Location{
				Path:      path,
				StartLine: lineNo,
				EndLine:   lineNo,
				StartByte: keyStart,
				EndByte:   keyStart + len(key),
			}
		}
		lineStart = lineEnd + 1
	}
}

// — routes —

func (idx *Index) walkRoutes(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".php") {
			return nil
		}
		src, tree, parseErr := phpnode.ParseFile(path)
		if parseErr != nil {
			return nil
		}
		defer tree.Close()

		rv := &routeNameVisitor{idx: idx, path: path, src: src}
		phpwalk.Walk(path, src, tree, rv)
		return nil
	})
}

// routeNameVisitor records every ->name('...') call. Route::name('prefix.')
// group prefixes are static calls, not member calls, so they are not matched.
type routeNameVisitor struct {
	phpwalk.NullVisitor
	idx  *Index
	path string
	src  []byte
}

func (v *routeNameVisitor) VisitMethodCall(n phpwalk.MethodCallInfo) {
	if n.MethodName != "name" || len(n.Args) == 0 {
		return
	}
	name := phpwalk.StringValue(n.Args[0], v.src)
	if name == "" {
		return
	}
	v.idx.Routes[name] = phpnode.FromNode(v.path, n.Args[0])
}
