package eloquent

import (
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// extractFileModels parses a single PHP file's tree and returns a
// ModelCatalog for each Model subclass found in it, plus an attribute
// catalog for every trait declared in the file (traits are extracted
// unconditionally — whether a model uses them is only known at lookup time).
func extractFileModels(path string, src []byte, tree *ts.Tree, syms *symbolTable) (models, traits []*ModelCatalog) {
	fc := &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)}
	ev := &extractVisitor{path: path, fc: fc, syms: syms}
	phpwalk.Walk(path, src, tree, ev)
	return ev.catalogs, ev.traitCatalogs
}

type extractVisitor struct {
	phpwalk.NullVisitor
	path          string
	fc            *phputil.FileContext
	syms          *symbolTable
	catalogs      []*ModelCatalog
	traitCatalogs []*ModelCatalog
}

func (v *extractVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *extractVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *extractVisitor) VisitClass(n phpwalk.ClassInfo) {
	fqn := v.fc.Resolve(n.NameText)
	if fqn == "" || !v.syms.isModel(fqn) {
		return
	}
	var extends phputil.FQN
	if n.ExtendsText != "" {
		extends = v.fc.Resolve(n.ExtendsText)
	}

	catalog := &ModelCatalog{
		Class:      fqn,
		Path:       v.path,
		Extends:    extends,
		UsesTraits: resolveNames(v.fc, n.UsesTraits),
		ByExposed:  make(map[string][]ModelAttribute),
	}

	attrs := extractMethods(v.path, n.Raw, n.Src, v.fc)
	attrs = append(attrs, extractArrayProperties(v.path, n.Raw, n.Src)...)
	for _, a := range attrs {
		catalog.ByExposed[a.ExposedName] = append(catalog.ByExposed[a.ExposedName], a)
	}

	v.catalogs = append(v.catalogs, catalog)
}

// VisitTrait extracts an attribute catalog from a trait declaration using the
// same method/array extractors as classes.
func (v *extractVisitor) VisitTrait(n phpwalk.TraitInfo) {
	fqn := v.fc.Resolve(n.NameText)
	if fqn == "" {
		return
	}
	catalog := &ModelCatalog{
		Class:      fqn,
		Path:       v.path,
		UsesTraits: resolveNames(v.fc, n.UsesTraits),
		ByExposed:  make(map[string][]ModelAttribute),
	}
	attrs := extractMethods(v.path, n.Raw, n.Src, v.fc)
	attrs = append(attrs, extractArrayProperties(v.path, n.Raw, n.Src)...)
	for _, a := range attrs {
		catalog.ByExposed[a.ExposedName] = append(catalog.ByExposed[a.ExposedName], a)
	}
	v.traitCatalogs = append(v.traitCatalogs, catalog)
}

// resolveNames resolves each raw source name via fc, dropping empty results.
func resolveNames(fc *phputil.FileContext, names []string) []phputil.FQN {
	var out []phputil.FQN
	for _, n := range names {
		if fqn := fc.Resolve(n); fqn != "" {
			out = append(out, fqn)
		}
	}
	return out
}
