package idehelper

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"

	"github.com/akyrey/laravel-ls/internal/indexer/eloquent"
	"github.com/akyrey/laravel-ls/internal/phpparse"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

var (
	// @property[-read|-write] <type> $<name>
	propertyTagRe = regexp.MustCompile(`@property(?:-read|-write)?[^\$]*\$(\w+)`)
	// @method [static] <returnType> <name>(...)
	methodTagRe = regexp.MustCompile(`@method\b[^\(]+\b(\w+)\s*\(`)
)

// Merge parses _ide_helper_models.php (if present at path) and adds
// SourceIdeHelper entries into idx for any attribute name not already present
// from SourceAST.
//
// Conflict-resolution rule: SourceAST wins. IdeHelper entries for a name that
// already has at least one SourceAST entry are silently dropped.
//
// Jump-target policy (option b from the plan): IdeHelper-only entries have a
// zero Location so the LSP layer returns nothing for them.
func Merge(path string, idx *eloquent.ModelIndex) error {
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("idehelper: read %s: %w", path, err)
	}

	root, err := phpparse.Bytes(src, path)
	if err != nil || root == nil {
		return nil
	}

	mv := &mergeVisitor{
		src: src,
		idx: idx,
		fc:  &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
	}
	traverser.NewTraverser(mv).Traverse(root)
	return nil
}

// mergeVisitor walks the ide-helper AST and applies doc-comment entries.
type mergeVisitor struct {
	visitor.Null
	src []byte
	idx *eloquent.ModelIndex
	fc  *phputil.FileContext
}

func (v *mergeVisitor) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *mergeVisitor) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *mergeVisitor) StmtGroupUse(n *ast.StmtGroupUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, phputil.NameToString(n.Prefix))
}

func (v *mergeVisitor) StmtClass(n *ast.StmtClass) {
	fqn := phputil.ClassNodeFQN(n.Name, v.fc)
	if fqn == "" {
		return
	}

	pos := n.GetPosition()
	if pos == nil {
		return
	}

	doc := extractDocComment(v.src, pos.StartPos)
	if doc == "" {
		return
	}
	props, methods := parseDocComment(doc)

	cat := v.idx.Lookup(fqn)
	if cat == nil {
		cat = &eloquent.ModelCatalog{
			Class:     fqn,
			ByExposed: make(map[string][]eloquent.ModelAttribute),
		}
		v.idx.Add(cat)
	}

	for _, name := range props {
		if hasSourceAST(cat, name) {
			continue
		}
		cat.ByExposed[name] = append(cat.ByExposed[name], eloquent.ModelAttribute{
			ExposedName: name,
			Kind:        eloquent.IdeHelperProperty,
			Source:      eloquent.SourceIdeHelper,
			// Zero Location: policy is "return nothing for ide-helper-only names".
		})
	}

	for _, name := range methods {
		if hasSourceAST(cat, name) {
			continue
		}
		cat.ByExposed[name] = append(cat.ByExposed[name], eloquent.ModelAttribute{
			ExposedName: name,
			MethodName:  name,
			Kind:        eloquent.IdeHelperMethod,
			Source:      eloquent.SourceIdeHelper,
		})
	}
}

// hasSourceAST reports whether cat already has a SourceAST entry for name.
func hasSourceAST(cat *eloquent.ModelCatalog, name string) bool {
	for _, a := range cat.ByExposed[name] {
		if a.Source == eloquent.SourceAST {
			return true
		}
	}
	return false
}

// extractDocComment returns the `/** ... */` block immediately preceding the
// byte at position before in src, or "" if none is found.
func extractDocComment(src []byte, before int) string {
	if before <= 0 || before > len(src) {
		return ""
	}
	chunk := src[:before]

	end := bytes.LastIndex(chunk, []byte("*/"))
	if end < 0 {
		return ""
	}
	end += 2 // advance past */

	// Only whitespace may separate the comment from the class keyword.
	for _, b := range chunk[end:] {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return ""
		}
	}

	start := bytes.LastIndex(chunk[:end-2], []byte("/**"))
	if start < 0 {
		return ""
	}

	return string(chunk[start:end])
}

// parseDocComment extracts @property and @method names from a doc-comment string.
func parseDocComment(doc string) (properties, methods []string) {
	for _, m := range propertyTagRe.FindAllStringSubmatch(doc, -1) {
		properties = append(properties, m[1])
	}
	for _, m := range methodTagRe.FindAllStringSubmatch(doc, -1) {
		methods = append(methods, m[1])
	}
	return
}
