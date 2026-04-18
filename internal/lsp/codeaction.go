package lsp

import (
	"fmt"
	"regexp"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-ls/internal/phpparse"
	"github.com/akyrey/laravel-ls/internal/phputil"
)

// unknownPropRe matches the diagnostic message produced by diagVisitor.
var unknownPropRe = regexp.MustCompile(`^unknown property '([^']+)' on (.+)$`)

// arrayQuickFix describes one "add to $array" quick-fix offered per diagnostic.
type arrayQuickFix struct {
	phpProp string // PHP property name without $, e.g. "fillable"
	title   func(prop, modelFQN string) string
	newText func(prop string, hasItems bool) string
}

// arrayQuickFixes lists all the quick-fixes offered for an unknown property.
// $fillable and $appends / $hidden take plain values; $casts takes key => type.
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

// CodeAction handles textDocument/codeAction requests. It offers quick-fixes
// for each "unknown property" diagnostic: add to $fillable, $casts, $appends,
// or $hidden.
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

		astRoot, parseErr := phpparse.Bytes(src, cat.Path)
		if parseErr != nil || astRoot == nil {
			continue
		}

		// Find all array insertion points in one pass.
		av := newArrayPropVisitor(cat.Path)
		traverser.NewTraverser(av).Traverse(astRoot)

		kind := protocol.CodeActionKindQuickFix
		for _, qf := range arrayQuickFixes {
			ins, ok := av.insertions[qf.phpProp]
			if !ok {
				continue
			}
			line, col := byteOffsetToLineCol(src, ins.insertByte)
			pos := protocol.Position{Line: uint32(line), Character: uint32(col)}
			uri := PathToURI(cat.Path)
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
	}

	if len(actions) == 0 {
		return nil, nil
	}
	return actions, nil
}

// buildAddToFillableEdit is kept for tests and external callers.
func buildAddToFillableEdit(modelPath string, src []byte, propName string) *protocol.WorkspaceEdit {
	astRoot, err := phpparse.Bytes(src, modelPath)
	if err != nil || astRoot == nil {
		return nil
	}
	av := newArrayPropVisitor(modelPath)
	traverser.NewTraverser(av).Traverse(astRoot)
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

// byteOffsetToLineCol converts a byte offset to a 0-based (line, utf16-col) pair.
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
	visitor.Null
	path       string
	insertions map[string]insertionPoint // key: "fillable", "casts", "appends", "hidden"
}

func newArrayPropVisitor(path string) *arrayPropVisitor {
	return &arrayPropVisitor{
		path:       path,
		insertions: make(map[string]insertionPoint),
	}
}

var arrayPropNames = map[string]bool{
	"fillable": true,
	"casts":    true,
	"appends":  true,
	"hidden":   true,
}

func (av *arrayPropVisitor) StmtPropertyList(n *ast.StmtPropertyList) {
	for _, prop := range n.Props {
		p, ok := prop.(*ast.StmtProperty)
		if !ok {
			continue
		}
		ev, ok := p.Var.(*ast.ExprVariable)
		if !ok {
			continue
		}
		id, ok := ev.Name.(*ast.Identifier)
		if !ok {
			continue
		}
		name := string(id.Value)
		// Strip leading $ if present.
		bare := name
		if len(bare) > 0 && bare[0] == '$' {
			bare = bare[1:]
		}
		if !arrayPropNames[bare] {
			continue
		}
		arr, ok := p.Expr.(*ast.ExprArray)
		if !ok {
			continue
		}
		// Find last item with a valid position (trailing comma adds nil/positionless entries).
		lastEndPos := -1
		for i := len(arr.Items) - 1; i >= 0; i-- {
			item := arr.Items[i]
			if item == nil {
				continue
			}
			pos := item.GetPosition()
			if pos == nil {
				continue
			}
			lastEndPos = pos.EndPos
			break
		}
		var ins insertionPoint
		if lastEndPos < 0 {
			// EndPos is exclusive (one past ']'), so ']' is at EndPos-1.
			ins.insertByte = arr.GetPosition().EndPos - 1
			ins.hasItems = false
		} else {
			ins.insertByte = lastEndPos
			ins.hasItems = true
		}
		av.insertions[bare] = ins
	}
}

// fillableVisitor is kept for tests that reference it directly.
type fillableVisitor = arrayPropVisitor

func newFillableVisitor(path string) *fillableVisitor {
	return newArrayPropVisitor(path)
}

// insertText returns the text to insert at the fillable array for propName.
func (av *arrayPropVisitor) insertText(propName string) string {
	ins, ok := av.insertions["fillable"]
	quoted := "'" + propName + "'"
	if ok && ins.hasItems {
		return ", " + quoted
	}
	return quoted
}

// insertByte returns the fillable insertion point for legacy callers.
func (av *arrayPropVisitor) insertByteVal() int {
	if ins, ok := av.insertions["fillable"]; ok {
		return ins.insertByte
	}
	return -1
}
