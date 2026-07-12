package lsp

import (
	"fmt"
	"regexp"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// PrepareRename validates that the cursor is on a renameable symbol.
func (s *Server) PrepareRename(_ *glsp.Context, p *protocol.PrepareRenameParams) (any, error) {
	s.mu.RLock()
	bindings, models := s.bindings, s.models
	s.mu.RUnlock()
	if bindings == nil || models == nil {
		return nil, nil
	}

	src, err := s.docs.Read(p.TextDocument.URI)
	if err != nil {
		return nil, nil
	}

	path := URIToPath(p.TextDocument.URI)
	offset := positionToByteOffset(src, p.Position)

	sym, tokenLoc := identifySymbolWithLoc(src, path, offset, bindings, models)
	if sym == nil || !sym.isEloquent() || tokenLoc.Zero() {
		return nil, nil
	}

	rng := toLSPRange(tokenLoc, src)
	return rng, nil
}

// validPropertyNameRe matches a snake_case PHP identifier — the only form an
// Eloquent exposed attribute name can take.
var validPropertyNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// textReplacement pairs a source location with the replacement text.
type textReplacement struct {
	loc     phputil.Location
	newText string
}

// Rename handles textDocument/rename requests.
func (s *Server) Rename(_ *glsp.Context, p *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	s.mu.RLock()
	bindings, models, root := s.bindings, s.models, s.root
	s.mu.RUnlock()
	if bindings == nil || models == nil || root == "" {
		return nil, nil
	}

	src, err := s.docs.Read(p.TextDocument.URI)
	if err != nil {
		return nil, nil
	}

	path := URIToPath(p.TextDocument.URI)
	offset := positionToByteOffset(src, p.Position)

	sym := identifySymbol(src, path, offset, bindings, models)
	if sym == nil || !sym.isEloquent() {
		return nil, nil
	}

	// Exposed attribute names are snake_case; reference sites use the new
	// name verbatim while declaration sites derive method names from it
	// (Camel for modern accessors, Studly for legacy ones), so anything but
	// a snake_case identifier would produce edits that no longer match.
	if !validPropertyNameRe.MatchString(p.NewName) {
		return nil, fmt.Errorf("invalid property name %q: use a snake_case identifier (e.g. contact_email)", p.NewName)
	}

	reps := scanRenameRefs(root, s.effectiveReferenceDirs(root), sym, s.docs, models, p.NewName)
	reps = append(reps, collectDeclReplacements(sym, models, p.NewName, s.docs)...)

	if len(reps) == 0 {
		return nil, nil
	}
	return buildWorkspaceEdit(reps, s.docs), nil
}

// — Reference-site scanning —

func scanRenameRefs(
	root string,
	dirs []string,
	sym *refSymbol,
	docs *DocumentStore,
	models *eloquent.ModelIndex,
	newName string,
) []textReplacement {
	files := listPHPFiles(root, dirs)
	return scanFilesParallel(files, func(path string) []textReplacement {
		src, err := docs.Read(PathToURI(path))
		if err != nil {
			return nil
		}
		tree, parseErr := phpnode.ParseBytes(src)
		if parseErr != nil {
			return nil
		}
		defer tree.Close()

		rv := &renameRefsVisitor{
			fc:      &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
			sym:     sym,
			path:    path,
			src:     src,
			models:  models,
			newName: newName,
		}
		phpwalk.Walk(path, src, tree, rv)
		return rv.replacements
	})
}

type renameRefsVisitor struct {
	phpwalk.NullVisitor
	fc           *phputil.FileContext
	sym          *refSymbol
	path         string
	src          []byte
	models       *eloquent.ModelIndex
	newName      string
	encClass     phputil.FQN
	encMethod    *phpwalk.MethodInfo
	assignedVars map[string]phputil.FQN
	replacements []textReplacement
}

func (v *renameRefsVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *renameRefsVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *renameRefsVisitor) VisitClass(n phpwalk.ClassInfo) {
	v.encClass = v.fc.Resolve(n.NameText)
	v.encMethod = nil
}

func (v *renameRefsVisitor) VisitClassMethod(n phpwalk.MethodInfo) {
	v.encMethod = &n
	v.assignedVars = collectAssignments(n, v.fc)
}

func (v *renameRefsVisitor) VisitPropertyFetch(n phpwalk.PropertyFetchInfo) {
	if n.PropName != v.sym.propName {
		return
	}
	var params []phpwalk.ParamInfo
	if v.encMethod != nil {
		params = v.encMethod.Params
	}
	modelFQN := resolveExprType(n.VarRaw, n.Src, v.encClass, params, v.assignedVars, v.fc, v.models)
	if modelFQN != v.sym.modelFQN {
		return
	}
	v.replacements = append(v.replacements, textReplacement{
		loc:     n.PropLocation,
		newText: v.newName,
	})
}

// — Declaration-site renames —

func collectDeclReplacements(
	sym *refSymbol,
	models *eloquent.ModelIndex,
	newName string,
	docs *DocumentStore,
) []textReplacement {
	cat := models.Lookup(sym.modelFQN)
	if cat == nil {
		return nil
	}
	attrs := cat.ByExposed[sym.propName]
	if len(attrs) == 0 {
		return nil
	}

	// Group the work per declaring file: method-based declarations rename
	// the method identifier; array-based ones ($fillable, $casts, ...,
	// including the casts() method) rename the quoted string entry.
	type fileWork struct {
		methods []declMethodEntry
		arrays  bool
	}
	seen := make(map[string]bool)
	work := make(map[string]*fileWork)
	at := func(path string) *fileWork {
		if work[path] == nil {
			work[path] = &fileWork{}
		}
		return work[path]
	}
	for _, a := range attrs {
		if a.Source != eloquent.SourceAST || a.Location.Zero() {
			continue
		}
		switch a.Kind {
		case eloquent.ModernAccessor, eloquent.LegacyAccessor, eloquent.LegacyMutator, eloquent.Relationship:
			if a.MethodName == "" {
				continue
			}
			key := a.Location.Path + "|" + a.MethodName
			if seen[key] {
				continue
			}
			seen[key] = true
			fw := at(a.Location.Path)
			fw.methods = append(fw.methods, declMethodEntry{path: a.Location.Path, methodName: a.MethodName, kind: a.Kind})
		case eloquent.FillableArray, eloquent.CastArray, eloquent.AppendsArray, eloquent.HiddenArray:
			at(a.Location.Path).arrays = true
		}
	}
	if len(work) == 0 {
		return nil
	}

	var reps []textReplacement
	for filePath, fw := range work {
		src, err := docs.Read(PathToURI(filePath))
		if err != nil {
			continue
		}
		tree, err := phpnode.ParseBytes(src)
		if err != nil {
			continue
		}
		mv := &declRenameVisitor{
			path:         filePath,
			src:          src,
			entries:      fw.methods,
			renameArrays: fw.arrays,
			oldName:      sym.propName,
			newName:      newName,
		}
		phpwalk.Walk(filePath, src, tree, mv)
		tree.Close() // not deferred: this runs inside a loop
		reps = append(reps, mv.replacements...)
	}
	return reps
}

type declMethodEntry struct {
	path, methodName string
	kind             eloquent.AttributeKind
}

type declRenameVisitor struct {
	phpwalk.NullVisitor
	path         string
	src          []byte
	entries      []declMethodEntry
	renameArrays bool
	oldName      string
	newName      string
	replacements []textReplacement
}

func (v *declRenameVisitor) VisitClassMethod(n phpwalk.MethodInfo) {
	// Laravel 11 casts() method: rename the array key inside its body.
	if v.renameArrays && n.Name == "casts" {
		if strNode := eloquent.CastsMethodItemNamed(n.Raw, v.src, v.oldName); strNode != nil {
			v.appendStringReplacement(strNode)
		}
		return
	}
	for _, e := range v.entries {
		if e.methodName != n.Name {
			continue
		}
		nameNode := n.Raw.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		v.replacements = append(v.replacements, textReplacement{
			loc:     phpnode.FromNode(v.path, nameNode),
			newText: methodNameFor(e.kind, v.newName),
		})
		return
	}
}

// VisitProperty renames the matching entry of $fillable / $casts / $appends /
// $hidden array declarations.
func (v *declRenameVisitor) VisitProperty(n phpwalk.PropertyInfo) {
	if !v.renameArrays {
		return
	}
	kind, ok := eloquent.ArrayPropKinds[n.PropName]
	if !ok || n.ValueRaw == nil {
		return
	}
	if strNode := eloquent.ArrayItemNamed(kind, n.ValueRaw, v.src, v.oldName); strNode != nil {
		v.appendStringReplacement(strNode)
	}
}

// appendStringReplacement replaces a whole string literal node with the new
// name, preserving the original quote character.
func (v *declRenameVisitor) appendStringReplacement(strNode *ts.Node) {
	quote := "'"
	if b := v.src[strNode.StartByte()]; b == '"' {
		quote = "\""
	}
	v.replacements = append(v.replacements, textReplacement{
		loc:     phpnode.FromNode(v.path, strNode),
		newText: quote + v.newName + quote,
	})
}

// methodNameFor computes the PHP method name for a given kind and new
// snake_case exposed name.
func methodNameFor(kind eloquent.AttributeKind, newName string) string {
	switch kind {
	case eloquent.ModernAccessor:
		return phputil.Camel(newName)
	case eloquent.LegacyAccessor:
		return "get" + phputil.Studly(newName) + "Attribute"
	case eloquent.LegacyMutator:
		return "set" + phputil.Studly(newName) + "Attribute"
	case eloquent.Relationship:
		return newName
	}
	return newName
}

// — WorkspaceEdit builder —

func buildWorkspaceEdit(reps []textReplacement, docs *DocumentStore) *protocol.WorkspaceEdit {
	changes := make(map[protocol.DocumentUri][]protocol.TextEdit)
	srcCache := make(map[string][]byte)

	for _, r := range reps {
		if _, ok := srcCache[r.loc.Path]; !ok {
			src, err := docs.Read(PathToURI(r.loc.Path))
			if err != nil {
				continue
			}
			srcCache[r.loc.Path] = src
		}
		src := srcCache[r.loc.Path]
		rng := toLSPRange(r.loc, src)
		uri := PathToURI(r.loc.Path)
		changes[uri] = append(changes[uri], protocol.TextEdit{
			Range:   rng,
			NewText: r.newText,
		})
	}

	if len(changes) == 0 {
		return nil
	}
	return &protocol.WorkspaceEdit{Changes: changes}
}
