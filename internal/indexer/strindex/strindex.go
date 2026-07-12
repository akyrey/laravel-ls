// Package strindex indexes Laravel's string-reference conventions: config
// keys (config/*.php), view names (resources/views/**/*.blade.php), and
// route names (->name('...') in routes/*.php). The LSP layer uses it to
// resolve config('app.name'), view('users.index'), and route('home') calls.
package strindex

import (
	"io/fs"
	"path/filepath"
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
}

// New returns an empty, ready-to-use index.
func New() *Index {
	return &Index{
		Config: make(map[string]phputil.Location),
		Views:  make(map[string]phputil.Location),
		Routes: make(map[string]phputil.Location),
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
