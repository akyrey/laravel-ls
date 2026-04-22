package lsp

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpparse"
	"github.com/akyrey/laravel-lsp/internal/phputil"
)

// PrepareRename validates that the cursor is on a renameable symbol and returns
// the range of the token to rename. Returns nil when renaming is not supported
// at the position (non-Eloquent symbol, unknown position, etc.).
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

// textReplacement pairs a source location with the replacement text.
// loc must point to the exact token (StartByte inclusive, EndByte inclusive).
type textReplacement struct {
	loc     phputil.Location
	newText string
}

// Rename handles textDocument/rename requests.
// Only Eloquent property renames are supported; container abstract rename is
// out of scope (it requires a full PHP class rename).
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

	newName := p.NewName

	// Reference sites across app/ and routes/.
	reps := scanRenameRefs(root, referenceScanDirs, sym, s.docs, models, newName)

	// Declaration sites from the model file(s).
	reps = append(reps, collectDeclReplacements(sym, models, newName, s.docs)...)

	if len(reps) == 0 {
		return nil, nil
	}
	return buildWorkspaceEdit(reps), nil
}

// — Reference-site scanning —

// scanRenameRefs walks dirs under root and records (loc, newName) for every
// $model->propName access that matches sym.
func scanRenameRefs(
	root string,
	dirs []string,
	sym *refSymbol,
	docs *DocumentStore,
	models *eloquent.ModelIndex,
	newName string,
) []textReplacement {
	var reps []textReplacement
	for _, dir := range dirs {
		scanDir := filepath.Join(root, dir)
		if _, err := os.Stat(scanDir); err != nil {
			continue
		}
		_ = filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".php") {
				return nil
			}
			src, err := docs.Read(PathToURI(path))
			if err != nil {
				return nil
			}
			astRoot, err := phpparse.Bytes(src, path)
			if err != nil || astRoot == nil {
				return nil
			}
			rv := &renameRefsVisitor{
				fc:      &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
				sym:     sym,
				path:    path,
				models:  models,
				newName: newName,
			}
			traverser.NewTraverser(rv).Traverse(astRoot)
			reps = append(reps, rv.replacements...)
			return nil
		})
	}
	return reps
}

type renameRefsVisitor struct {
	visitor.Null
	fc           *phputil.FileContext
	sym          *refSymbol
	path         string
	models       *eloquent.ModelIndex
	newName      string
	encClass     phputil.FQN
	encMethod    *ast.StmtClassMethod
	assignedVars map[string]phputil.FQN
	replacements []textReplacement
}

func (v *renameRefsVisitor) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *renameRefsVisitor) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *renameRefsVisitor) StmtGroupUse(n *ast.StmtGroupUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, phputil.NameToString(n.Prefix))
}

func (v *renameRefsVisitor) StmtClass(n *ast.StmtClass) {
	v.encClass = phputil.ClassNodeFQN(n.Name, v.fc)
	v.encMethod = nil
}

func (v *renameRefsVisitor) StmtClassMethod(n *ast.StmtClassMethod) {
	v.encMethod = n
	v.assignedVars = collectAssignments(n, v.fc)
}

func (v *renameRefsVisitor) ExprPropertyFetch(n *ast.ExprPropertyFetch) {
	prop, ok := n.Prop.(*ast.Identifier)
	if !ok || string(prop.Value) != v.sym.propName {
		return
	}
	modelFQN := resolveExprType(n.Var, v.encClass, v.encMethod, v.assignedVars, v.fc, v.models)
	if modelFQN != v.sym.modelFQN {
		return
	}
	v.replacements = append(v.replacements, textReplacement{
		loc:     phputil.FromPosition(v.path, prop.GetPosition()),
		newText: v.newName,
	})
}

// — Declaration-site renames —

// collectDeclReplacements walks the model file(s) and returns replacements for
// method name tokens that declare sym.propName. Array entries ($fillable, etc.)
// are NOT renamed because their stored location points to the whole property
// list, not the individual string literal.
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

	// Collect unique (file, methodName, kind) triples for method-based declarations.
	seen := make(map[string]bool)
	var entries []declMethodEntry
	for _, a := range attrs {
		if a.Source != eloquent.SourceAST || a.MethodName == "" || a.Location.Zero() {
			continue
		}
		switch a.Kind {
		case eloquent.ModernAccessor, eloquent.LegacyAccessor, eloquent.LegacyMutator, eloquent.Relationship:
		default:
			continue
		}
		key := a.Location.Path + "|" + a.MethodName
		if seen[key] {
			continue
		}
		seen[key] = true
		entries = append(entries, declMethodEntry{path: a.Location.Path, methodName: a.MethodName, kind: a.Kind})
	}
	if len(entries) == 0 {
		return nil
	}

	// Group by file so we parse each file once.
	byFile := make(map[string][]declMethodEntry)
	for _, e := range entries {
		byFile[e.path] = append(byFile[e.path], e)
	}

	var reps []textReplacement
	for filePath, fileEntries := range byFile {
		src, err := docs.Read(PathToURI(filePath))
		if err != nil {
			continue
		}
		astRoot, err := phpparse.Bytes(src, filePath)
		if err != nil || astRoot == nil {
			continue
		}
		mv := &declRenameVisitor{
			path:    filePath,
			entries: fileEntries,
			newName: newName,
		}
		traverser.NewTraverser(mv).Traverse(astRoot)
		reps = append(reps, mv.replacements...)
	}
	return reps
}

// declMethodEntry records a method-based declaration to be renamed.
type declMethodEntry struct {
	path, methodName string
	kind             eloquent.AttributeKind
}

// declRenameVisitor finds method name identifier tokens for known declarations
// and records (nameToken location, new method name) replacements.
type declRenameVisitor struct {
	visitor.Null
	path         string
	entries      []declMethodEntry
	newName      string
	replacements []textReplacement
}

func (v *declRenameVisitor) StmtClassMethod(n *ast.StmtClassMethod) {
	nameID, ok := n.Name.(*ast.Identifier)
	if !ok {
		return
	}
	methodName := string(nameID.Value)
	for _, e := range v.entries {
		if e.methodName != methodName {
			continue
		}
		v.replacements = append(v.replacements, textReplacement{
			loc:     phputil.FromPosition(v.path, nameID.GetPosition()),
			newText: methodNameFor(e.kind, v.newName),
		})
		return
	}
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
		// Relationships use the method name directly as ExposedName; the new
		// name is used as-is (camelCase by convention, but the user decides).
		return newName
	}
	return newName
}

// — WorkspaceEdit builder —

// buildWorkspaceEdit groups replacements by file URI and returns a WorkspaceEdit.
func buildWorkspaceEdit(reps []textReplacement) *protocol.WorkspaceEdit {
	changes := make(map[protocol.DocumentUri][]protocol.TextEdit)
	srcCache := make(map[string][]byte)

	for _, r := range reps {
		if _, ok := srcCache[r.loc.Path]; !ok {
			src, err := os.ReadFile(r.loc.Path)
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
