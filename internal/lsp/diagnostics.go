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

// diagOptions is the runtime form of the diagnostics initializationOptions.
type diagOptions struct {
	enabled  bool
	severity protocol.DiagnosticSeverity
	ignore   map[string]bool // extra property names never flagged
}

// defaultDiagOptions returns the options used when the client sends none.
func defaultDiagOptions() diagOptions {
	return diagOptions{enabled: true, severity: protocol.DiagnosticSeverityWarning}
}

// builtinModelAttrs are names that exist on every Eloquent model even though
// no project file declares them: the conventional columns Laravel manages
// (primary key, timestamps, soft-delete column), the runtime `pivot`
// attribute, and Illuminate\Database\Eloquent\Model's own PHP properties
// (reachable both as $model->exists and as $this->table inside a model).
// Accesses to these must never be flagged as unknown.
var builtinModelAttrs = map[string]bool{
	// Conventional columns and runtime attributes.
	"id": true, "created_at": true, "updated_at": true, "deleted_at": true,
	"pivot": true,
	// Public base-class properties.
	"exists": true, "wasRecentlyCreated": true, "incrementing": true,
	"timestamps": true, "preventsLazyLoading": true, "usesUniqueIds": true,
	// Protected base-class properties, commonly read via $this-> in models.
	"connection": true, "table": true, "primaryKey": true, "keyType": true,
	"with": true, "withCount": true, "perPage": true, "attributes": true,
	"original": true, "changes": true, "casts": true, "dateFormat": true,
	"appends": true, "dispatchesEvents": true, "observables": true,
	"relations": true, "touches": true, "hidden": true, "visible": true,
	"fillable": true, "guarded": true,
}

// publishDiagnostics parses src and pushes a textDocument/publishDiagnostics
// notification for any unrecognised Eloquent property accesses it finds.
// Calling it with a nil models index clears existing diagnostics for the file.
func publishDiagnostics(ctx *glsp.Context, uri protocol.DocumentUri, src []byte, path string, models *eloquent.ModelIndex, opts diagOptions) {
	diags := []protocol.Diagnostic{}
	if models != nil && len(src) > 0 {
		if collected := collectDiagnostics(src, path, models, opts); collected != nil {
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
func collectDiagnostics(src []byte, path string, models *eloquent.ModelIndex, opts diagOptions) []protocol.Diagnostic {
	if !opts.enabled {
		return nil
	}
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
		opts:   opts,
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
	opts         diagOptions
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
	if builtinModelAttrs[n.PropName] || v.opts.ignore[n.PropName] {
		return
	}
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

	sev := v.opts.severity
	src := diagSource
	v.diags = append(v.diags, protocol.Diagnostic{
		Range:    toLSPRange(n.PropLocation, v.src),
		Severity: &sev,
		Source:   &src,
		Message:  fmt.Sprintf("unknown property '%s' on %s", n.PropName, modelFQN),
	})
}
