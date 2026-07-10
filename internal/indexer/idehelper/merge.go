package idehelper

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

var (
	// @property[-read|-write] <type> $<name>
	// Group 1 = type, Group 2 = name.
	propertyTagRe = regexp.MustCompile(`@property(?:-read|-write)?\s+(\S+)\s+\$(\w+)`)
	// @method [static] <returnType> <name>(...)
	methodTagRe = regexp.MustCompile(`@method\b[^\(]+\b(\w+)\s*\(`)
)

// phpPrimitives is the set of PHP built-in types that are never class references.
var phpPrimitives = map[string]bool{
	"string": true, "int": true, "integer": true, "float": true, "double": true,
	"bool": true, "boolean": true, "array": true, "null": true, "void": true,
	"mixed": true, "never": true, "object": true, "iterable": true, "callable": true,
	"false": true, "true": true, "self": true, "static": true, "parent": true,
	"resource": true,
}

// ideProperty holds the name and optional type string for a @property tag.
type ideProperty struct {
	name    string
	typeStr string
}

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

	tree, parseErr := phpnode.ParseBytes(src)
	if parseErr != nil {
		return nil
	}
	defer tree.Close()

	mv := &mergeVisitor{
		src: src,
		idx: idx,
		fc:  &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
	}
	phpwalk.Walk(path, src, tree, mv)
	return nil
}

// mergeVisitor walks the ide-helper AST and applies doc-comment entries.
type mergeVisitor struct {
	phpwalk.NullVisitor
	src []byte
	idx *eloquent.ModelIndex
	fc  *phputil.FileContext
}

func (v *mergeVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *mergeVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *mergeVisitor) VisitClass(n phpwalk.ClassInfo) {
	fqn := v.fc.Resolve(n.NameText)
	if fqn == "" {
		return
	}

	doc := extractDocComment(v.src, int(n.Raw.StartByte()))
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

	for _, pe := range props {
		if hasSourceAST(cat, pe.name) {
			continue
		}
		attr := eloquent.ModelAttribute{
			ExposedName: pe.name,
			Kind:        eloquent.IdeHelperProperty,
			Source:      eloquent.SourceIdeHelper,
			// Zero Location: policy is "return nothing for ide-helper-only names".
		}
		// For class-typed properties, resolve the FQN so the chain resolver
		// ($this->rel->attr) can follow the hop even without an AST relationship.
		if relFQN := resolveClassType(pe.typeStr, v.fc); relFQN != "" {
			attr.RelatedFQN = relFQN
		}
		cat.ByExposed[pe.name] = append(cat.ByExposed[pe.name], attr)
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

// resolveClassType returns the FQN for a PHP type string if it looks like a
// class reference (not a primitive, not a union/generic). Returns "" otherwise.
func resolveClassType(typeStr string, fc *phputil.FileContext) phputil.FQN {
	// Strip nullable prefix.
	typeStr = strings.TrimPrefix(typeStr, "?")
	// Skip unions, generics, and array shapes — too complex to resolve safely.
	if strings.ContainsAny(typeStr, "|&<>[") {
		return ""
	}
	if phpPrimitives[strings.ToLower(typeStr)] {
		return ""
	}
	return fc.Resolve(typeStr)
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

// parseDocComment extracts @property entries (with types) and @method names.
func parseDocComment(doc string) (properties []ideProperty, methods []string) {
	for _, m := range propertyTagRe.FindAllStringSubmatch(doc, -1) {
		// m[1] = type string, m[2] = property name
		properties = append(properties, ideProperty{typeStr: m[1], name: m[2]})
	}
	for _, m := range methodTagRe.FindAllStringSubmatch(doc, -1) {
		methods = append(methods, m[1])
	}
	return
}
