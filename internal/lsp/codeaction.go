package lsp

import (
	"fmt"
	"regexp"

	ts "github.com/tree-sitter/go-tree-sitter"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// unknownPropRe matches the diagnostic message produced by diagVisitor.
var unknownPropRe = regexp.MustCompile(`^unknown property '([^']+)' on (.+)$`)

// eloquentAttributeFQN mirrors eloquent's unexported eloquentAttributeTypeFQN
// — the return type Laravel 9+ modern accessors declare.
const eloquentAttributeFQN phputil.FQN = "Illuminate\\Database\\Eloquent\\Casts\\Attribute"

// arrayQuickFix describes one "add to $array" quick-fix offered per diagnostic.
type arrayQuickFix struct {
	phpProp string
	title   func(prop, modelFQN string) string
	newText func(prop string, hasItems bool) string
}

var arrayQuickFixes = []arrayQuickFix{
	{
		phpProp: "fillable",
		title:   func(prop, fqn string) string { return fmt.Sprintf("Add '%s' to $fillable", prop) },
		newText: func(prop string, hasItems bool) string {
			if hasItems {
				return ", '" + prop + "'"
			}
			return "'" + prop + "'"
		},
	},
	{
		phpProp: "casts",
		title:   func(prop, fqn string) string { return fmt.Sprintf("Add '%s' to $casts", prop) },
		newText: func(prop string, hasItems bool) string {
			entry := "'" + prop + "' => 'string'"
			if hasItems {
				return ", " + entry
			}
			return entry
		},
	},
	{
		phpProp: "appends",
		title:   func(prop, fqn string) string { return fmt.Sprintf("Add '%s' to $appends", prop) },
		newText: func(prop string, hasItems bool) string {
			if hasItems {
				return ", '" + prop + "'"
			}
			return "'" + prop + "'"
		},
	},
	{
		phpProp: "hidden",
		title:   func(prop, fqn string) string { return fmt.Sprintf("Add '%s' to $hidden", prop) },
		newText: func(prop string, hasItems bool) string {
			if hasItems {
				return ", '" + prop + "'"
			}
			return "'" + prop + "'"
		},
	},
}

// CodeAction handles textDocument/codeAction requests.
func (s *Server) CodeAction(_ *glsp.Context, p *protocol.CodeActionParams) (any, error) {
	s.mu.RLock()
	models := s.models
	s.mu.RUnlock()
	if models == nil {
		return nil, nil
	}

	var actions []protocol.CodeAction
	for i := range p.Context.Diagnostics {
		diag := p.Context.Diagnostics[i]
		if diag.Source == nil || *diag.Source != diagSource {
			continue
		}
		m := unknownPropRe.FindStringSubmatch(diag.Message)
		if m == nil {
			continue
		}
		propName, modelFQN := m[1], phputil.FQN(m[2])

		cat := models.Lookup(modelFQN)
		if cat == nil || cat.Path == "" {
			continue
		}

		src, err := s.docs.Read(PathToURI(cat.Path))
		if err != nil {
			continue
		}

		actions = append(actions, codeActionsForDiagnostic(diag, cat.Path, src, propName, modelFQN)...)
	}

	if len(actions) == 0 {
		return nil, nil
	}
	return actions, nil
}

// codeActionsForDiagnostic builds every quick-fix action for a single
// "unknown property" diagnostic. It parses src exactly once and shares that
// tree between the array-property scan and the accessor-insertion scan,
// rather than each parsing (and closing) its own tree.
func codeActionsForDiagnostic(diag protocol.Diagnostic, modelPath string, src []byte, propName string, modelFQN phputil.FQN) []protocol.CodeAction {
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return nil
	}
	defer tree.Close()

	kind := protocol.CodeActionKindQuickFix
	var actions []protocol.CodeAction
	uri := PathToURI(modelPath)

	av := newArrayPropVisitor(modelPath)
	phpwalk.Walk(modelPath, src, tree, av)
	for _, qf := range arrayQuickFixes {
		ins, ok := av.insertions[qf.phpProp]
		if !ok {
			continue
		}
		line, col := byteOffsetToLineCol(src, ins.insertByte)
		pos := protocol.Position{Line: uint32(line), Character: uint32(col)}
		edit := &protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentUri][]protocol.TextEdit{
				uri: {{
					Range:   protocol.Range{Start: pos, End: pos},
					NewText: qf.newText(propName, ins.hasItems),
				}},
			},
		}
		actions = append(actions, protocol.CodeAction{
			Title:       qf.title(propName, string(modelFQN)),
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{diag},
			Edit:        edit,
		})
	}

	accessorV := &accessorInsertVisitor{attributeRef: "\\" + string(eloquentAttributeFQN)}
	phpwalk.Walk(modelPath, src, tree, accessorV)
	if accessorV.found {
		edit := buildCreateAccessorEditFromInsertPoint(modelPath, src, accessorV.insertByte, accessorV.attributeRef, propName)
		actions = append(actions, protocol.CodeAction{
			Title:       fmt.Sprintf("Generate accessor for '%s'", propName),
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{diag},
			Edit:        edit,
		})
	}

	return actions
}

// buildAddToFillableEdit is kept for tests and external callers.
func buildAddToFillableEdit(modelPath string, src []byte, propName string) *protocol.WorkspaceEdit {
	av := newArrayPropVisitor(modelPath)
	if err := av.scan(src); err != nil {
		return nil
	}
	ins, ok := av.insertions["fillable"]
	if !ok {
		return nil
	}
	line, col := byteOffsetToLineCol(src, ins.insertByte)
	pos := protocol.Position{Line: uint32(line), Character: uint32(col)}
	newText := "'" + propName + "'"
	if ins.hasItems {
		newText = ", " + newText
	}
	uri := PathToURI(modelPath)
	return &protocol.WorkspaceEdit{
		Changes: map[protocol.DocumentUri][]protocol.TextEdit{
			uri: {{Range: protocol.Range{Start: pos, End: pos}, NewText: newText}},
		},
	}
}

// buildCreateAccessorEdit builds a WorkspaceEdit that appends a Laravel 9+
// modern-accessor method stub for propName to the end of the class body in
// modelPath. Returns nil when no class declaration is found in src.
// Kept for tests and external callers; it parses src itself to find the
// insertion point. When a tree is already available (e.g. CodeAction, which
// shares one tree across several checks), call
// buildCreateAccessorEditFromInsertPoint directly instead of re-parsing.
func buildCreateAccessorEdit(modelPath string, src []byte, propName string) *protocol.WorkspaceEdit {
	insertByte, attributeRef, ok := findAccessorInsertPoint(modelPath, src)
	if !ok {
		return nil
	}
	return buildCreateAccessorEditFromInsertPoint(modelPath, src, insertByte, attributeRef, propName)
}

// buildCreateAccessorEditFromInsertPoint builds the WorkspaceEdit given an
// already-resolved insertion point and attribute reference, without parsing
// src again.
func buildCreateAccessorEditFromInsertPoint(modelPath string, src []byte, insertByte int, attributeRef, propName string) *protocol.WorkspaceEdit {
	line, col := byteOffsetToLineCol(src, insertByte)
	pos := protocol.Position{Line: uint32(line), Character: uint32(col)}
	newText := fmt.Sprintf(
		"\n    public function %s(): %s\n    {\n        return %s::make(\n            get: fn ($value) => $value,\n        );\n    }\n",
		phputil.Camel(propName), attributeRef, attributeRef,
	)
	uri := PathToURI(modelPath)
	return &protocol.WorkspaceEdit{
		Changes: map[protocol.DocumentUri][]protocol.TextEdit{
			uri: {{Range: protocol.Range{Start: pos, End: pos}, NewText: newText}},
		},
	}
}

// findAccessorInsertPoint parses src and returns the byte offset just before
// the closing brace of the first class body, plus the reference to use for
// Illuminate\Database\Eloquent\Casts\Attribute in generated code: the
// existing use-alias if the class already imports it, or a fully-qualified
// name otherwise (so the generated stub never needs a new use statement).
func findAccessorInsertPoint(path string, src []byte) (insertByte int, attributeRef string, ok bool) {
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return 0, "", false
	}
	defer tree.Close()

	v := &accessorInsertVisitor{attributeRef: "\\" + string(eloquentAttributeFQN)}
	phpwalk.Walk(path, src, tree, v)
	if !v.found {
		return 0, "", false
	}
	return v.insertByte, v.attributeRef, true
}

// accessorInsertVisitor locates the first class body's end and, if the file
// already imports the Attribute cast class, the alias it was imported under.
type accessorInsertVisitor struct {
	phpwalk.NullVisitor
	attributeRef string
	insertByte   int
	found        bool
}

func (v *accessorInsertVisitor) VisitUseItem(alias, fqn string) {
	if phputil.FQN(fqn) == eloquentAttributeFQN {
		v.attributeRef = alias
	}
}

func (v *accessorInsertVisitor) VisitClass(n phpwalk.ClassInfo) {
	if v.found {
		return
	}
	bodyNode := n.Raw.ChildByFieldName("body")
	if bodyNode == nil {
		return
	}
	v.insertByte = int(bodyNode.EndByte()) - 1
	v.found = true
}

// byteOffsetToLineCol converts a byte offset to 0-based (line, utf16-col).
func byteOffsetToLineCol(src []byte, offset int) (line, col int) {
	if offset > len(src) {
		offset = len(src)
	}
	lineStart := 0
	for i := 0; i < offset; i++ {
		if src[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	col = int(countUTF16Units(src[lineStart:offset]))
	return line, col
}

// insertionPoint records where to insert a new item into an array property.
type insertionPoint struct {
	insertByte int
	hasItems   bool
}

// arrayPropVisitor finds all four model array properties in one AST pass.
type arrayPropVisitor struct {
	phpwalk.NullVisitor
	path       string
	insertions map[string]insertionPoint
}

func newArrayPropVisitor(path string) *arrayPropVisitor {
	return &arrayPropVisitor{
		path:       path,
		insertions: make(map[string]insertionPoint),
	}
}

// scan parses src and populates insertions.
func (av *arrayPropVisitor) scan(src []byte) error {
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return err
	}
	defer tree.Close()
	phpwalk.Walk(av.path, src, tree, av)
	return nil
}

var arrayPropNames = map[string]bool{
	"fillable": true, "casts": true, "appends": true, "hidden": true,
}

func (av *arrayPropVisitor) VisitProperty(n phpwalk.PropertyInfo) {
	if !arrayPropNames[n.PropName] || n.ValueRaw == nil {
		return
	}
	if n.ValueRaw.Kind() != "array_creation_expression" {
		return
	}
	av.insertions[n.PropName] = arrayInsertPoint(n.ValueRaw)
}

// arrayInsertPoint determines the byte offset at which to insert a new element
// into an array_creation_expression node, and whether the array already has items.
func arrayInsertPoint(arr *ts.Node) insertionPoint {
	// Find the last array_element_initializer with a valid position.
	var lastItemEnd int = -1
	for i := uint(0); i < arr.ChildCount(); i++ {
		child := arr.Child(i)
		if child.Kind() == "array_element_initializer" {
			end := int(child.EndByte())
			if end > lastItemEnd {
				lastItemEnd = end
			}
		}
	}
	if lastItemEnd < 0 {
		// Empty array: insert just before the closing ']'.
		return insertionPoint{insertByte: int(arr.EndByte()) - 1, hasItems: false}
	}
	return insertionPoint{insertByte: lastItemEnd, hasItems: true}
}

// fillableVisitor and newFillableVisitor are kept for tests.
type fillableVisitor = arrayPropVisitor

func newFillableVisitor(path string) *fillableVisitor {
	return newArrayPropVisitor(path)
}

func (av *arrayPropVisitor) insertText(propName string) string {
	ins, ok := av.insertions["fillable"]
	quoted := "'" + propName + "'"
	if ok && ins.hasItems {
		return ", " + quoted
	}
	return quoted
}

func (av *arrayPropVisitor) insertByteVal() int {
	if ins, ok := av.insertions["fillable"]; ok {
		return ins.insertByte
	}
	return -1
}
