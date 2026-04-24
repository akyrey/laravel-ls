package lsp

import (
	"fmt"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/akyrey/laravel-lsp/internal/indexer/eloquent"
	"github.com/akyrey/laravel-lsp/internal/phpnode"
	"github.com/akyrey/laravel-lsp/internal/phputil"
	"github.com/akyrey/laravel-lsp/internal/phpwalk"
)

// diagSource is the source label shown in the editor for diagnostics.
const diagSource = "laravel-lsp"

// publishDiagnostics parses src and pushes a textDocument/publishDiagnostics
// notification for any unrecognised Eloquent property accesses it finds.
// Calling it with a nil models index clears existing diagnostics for the file.
func publishDiagnostics(ctx *glsp.Context, uri protocol.DocumentUri, src []byte, path string, models *eloquent.ModelIndex) {
	diags := []protocol.Diagnostic{}
	if models != nil && len(src) > 0 {
		if collected := collectDiagnostics(src, path, models); collected != nil {
			diags = collected
		}
	}
	ctx.Notify(string(protocol.ServerTextDocumentPublishDiagnostics), protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diags,
	})
}

// collectDiagnostics returns Warning diagnostics for every $model->propName
// access where propName is not found in the model's attribute catalog.
func collectDiagnostics(src []byte, path string, models *eloquent.ModelIndex) []protocol.Diagnostic {
	tree, err := phpnode.ParseBytes(src)
	if err != nil {
		return nil
	}
	defer tree.Close()

	dv := &diagVisitor{
		fc:     &phputil.FileContext{Path: path, Uses: make(phputil.UseMap)},
		path:   path,
		src:    src,
		models: models,
	}
	phpwalk.Walk(path, src, tree, dv)
	return dv.diags
}

type diagVisitor struct {
	phpwalk.NullVisitor
	fc           *phputil.FileContext
	path         string
	src          []byte
	models       *eloquent.ModelIndex
	encClass     phputil.FQN
	encMethod    *phpwalk.MethodInfo
	assignedVars map[string]phputil.FQN
	diags        []protocol.Diagnostic
}

func (v *diagVisitor) VisitNamespace(ns string) { v.fc.Namespace = phputil.FQN(ns) }
func (v *diagVisitor) VisitUseItem(alias, fqn string) {
	v.fc.Uses[alias] = phputil.FQN(fqn)
}

func (v *diagVisitor) VisitClass(n phpwalk.ClassInfo) {
	v.encClass = v.fc.Resolve(n.NameText)
	v.encMethod = nil
}

func (v *diagVisitor) VisitClassMethod(n phpwalk.MethodInfo) {
	v.encMethod = &n
	v.assignedVars = collectAssignments(n, v.fc)
}

func (v *diagVisitor) VisitPropertyFetch(n phpwalk.PropertyFetchInfo) {
	var params []phpwalk.ParamInfo
	if v.encMethod != nil {
		params = v.encMethod.Params
	}
	modelFQN := resolveExprType(n.VarRaw, n.Src, v.encClass, params, v.assignedVars, v.fc, v.models)
	if modelFQN == "" {
		return
	}
	cat := v.models.Lookup(modelFQN)
	if cat == nil {
		return
	}
	if _, known := cat.ByExposed[n.PropName]; known {
		return
	}

	sev := protocol.DiagnosticSeverityWarning
	src := diagSource
	v.diags = append(v.diags, protocol.Diagnostic{
		Range:    toLSPRange(n.PropLocation, v.src),
		Severity: &sev,
		Source:   &src,
		Message:  fmt.Sprintf("unknown property '%s' on %s", n.PropName, modelFQN),
	})
}
