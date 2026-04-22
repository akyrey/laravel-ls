package lsp

import (
	"fmt"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpparse"
	"github.com/akyrey/laravel-lsp/internal/phputil"
)

// diagSource is the source label shown in the editor for diagnostics.
const diagSource = "laravel-lsp"

// publishDiagnostics parses src and pushes a textDocument/publishDiagnostics
// notification for any unrecognised Eloquent property accesses it finds.
// Calling it with a nil models index clears existing diagnostics for the file.
func publishDiagnostics(ctx *glsp.Context, uri protocol.DocumentUri, src []byte, path string, models *eloquent.ModelIndex) {
	var diags []protocol.Diagnostic
	if models != nil && len(src) > 0 {
		diags = collectDiagnostics(src, path, models)
	}
	ctx.Notify(string(protocol.ServerTextDocumentPublishDiagnostics), protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diags,
	})
}

// collectDiagnostics returns Warning diagnostics for every $model->propName
// access where propName is not found in the model's attribute catalog.
func collectDiagnostics(src []byte, path string, models *eloquent.ModelIndex) []protocol.Diagnostic {
	astRoot, err := phpparse.Bytes(src, path)
	if err != nil || astRoot == nil {
		return nil
	}
	dv := &diagVisitor{
		fc:     &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
		path:   path,
		src:    src,
		models: models,
	}
	traverser.NewTraverser(dv).Traverse(astRoot)
	return dv.diags
}

type diagVisitor struct {
	visitor.Null
	fc           *phputil.FileContext
	path         string
	src          []byte
	models       *eloquent.ModelIndex
	encClass     phputil.FQN
	encMethod    *ast.StmtClassMethod
	assignedVars map[string]phputil.FQN
	diags        []protocol.Diagnostic
}

func (v *diagVisitor) StmtNamespace(n *ast.StmtNamespace) {
	if n.Name != nil {
		v.fc.Namespace = phputil.FQN(phputil.NameToString(n.Name))
	} else {
		v.fc.Namespace = ""
	}
}

func (v *diagVisitor) StmtUse(n *ast.StmtUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, "")
}

func (v *diagVisitor) StmtGroupUse(n *ast.StmtGroupUseList) {
	phputil.AddUsesToContext(v.fc, n.Uses, phputil.NameToString(n.Prefix))
}

func (v *diagVisitor) StmtClass(n *ast.StmtClass) {
	v.encClass = phputil.ClassNodeFQN(n.Name, v.fc)
	v.encMethod = nil
}

func (v *diagVisitor) StmtClassMethod(n *ast.StmtClassMethod) {
	v.encMethod = n
	v.assignedVars = collectAssignments(n, v.fc)
}

func (v *diagVisitor) ExprPropertyFetch(n *ast.ExprPropertyFetch) {
	prop, ok := n.Prop.(*ast.Identifier)
	if !ok {
		return
	}
	propName := string(prop.Value)

	modelFQN := resolveExprType(n.Var, v.encClass, v.encMethod, v.assignedVars, v.fc, v.models)
	if modelFQN == "" {
		return
	}
	cat := v.models.Lookup(modelFQN)
	if cat == nil {
		return
	}
	if _, known := cat.ByExposed[propName]; known {
		return
	}

	// Unknown property on a known model — emit a warning.
	propPos := prop.GetPosition()
	if propPos == nil {
		return
	}
	loc := phputil.FromPosition(v.path, propPos)
	sev := protocol.DiagnosticSeverityWarning
	src := diagSource
	v.diags = append(v.diags, protocol.Diagnostic{
		Range:    toLSPRange(loc, v.src),
		Severity: &sev,
		Source:   &src,
		Message:  fmt.Sprintf("unknown property '%s' on %s", propName, modelFQN),
	})
}
