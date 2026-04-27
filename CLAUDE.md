# CLAUDE.md

Project context for Claude Code sessions.

## What is this project?

`laravel-lsp` is a Go LSP server that provides Laravel-specific
jump-to-definition, find-references, hover, completion, rename, diagnostics,
and code actions for editors that lack a Laravel Idea equivalent (Neovim,
VS Code). It indexes Laravel's runtime conventions that generic PHP language
servers miss: service container bindings and Eloquent model attribute accessors.

## Tech stack

- **Language**: Go 1.23+
- **PHP parser**: `github.com/tree-sitter/tree-sitter-php` v0.24.2 via
  `github.com/tree-sitter/go-tree-sitter` v0.25.0 (CGo). Supports PHP 5–8.x
  including PHP 8.4 property hooks and asymmetric visibility.
- **LSP framework**: `github.com/tliron/glsp` v0.2.2, protocol 3.16, stdio transport
- **Build**: `make build` (outputs `./laravel-lsp`) or `go build -o laravel-lsp ./cmd/laravel-lsp`
- **Tests**: `make test` / `go test ./...`

## Commands

```bash
make build        # build ./laravel-lsp
make test         # go test ./... -count=1
make test-race    # go test -race ./... -count=1
make vet          # go vet ./...
make fmt          # gofmt -s -w .
make tidy         # go mod tidy && go mod verify
make lint         # golangci-lint run ./... (requires golangci-lint)
make install      # go install ./cmd/laravel-lsp
```

Always run `make test-race` before committing.

### Debug command

```bash
# Inspect what the LSP server would index, without starting an editor session.
laravel-lsp debug /path/to/laravel-project          # text output (defaults: app/)
laravel-lsp debug -json /path/to/laravel-project     # JSON output
laravel-lsp debug -models /path/to/laravel-project   # models only
laravel-lsp debug -bindings /path/to/laravel-project # bindings only
laravel-lsp debug -dirs app,Modules/*/app /path/to/project  # glob scan dirs
laravel-lsp debug --help                             # full flag list
```

## Configuration (initializationOptions)

Passed by the editor in the LSP `initialize` request. All fields are optional.

```json
{
  "scanDirs":      ["app", "Modules/*/app"],
  "referenceDirs": ["app", "routes", "Modules/*/app", "Modules/*/routes"]
}
```

| Field | Default | Purpose |
|-------|---------|---------|
| `scanDirs` | `["app"]` | Directories walked to build the container + Eloquent indexes |
| `referenceDirs` | *(auto)* | Directories walked when finding references and rename sites. Defaults to `scanDirs + ["routes"]` when not set. |

Both fields support single-level glob patterns (`Modules/*/app`). `**` is not
supported. Patterns are expanded via `filepath.Glob` at indexing time.

The file watcher watches the union of `scanDirs` and `referenceDirs` so changes
in any configured directory trigger an incremental reindex.

**Common module setup** — only `scanDirs` needs to be set:
```json
{ "scanDirs": ["app", "Modules/*/app"] }
```
`referenceDirs` auto-derives to `["app", "Modules/*/app", "routes"]`.

## Project layout

```
cmd/laravel-lsp/
  main.go                       # entry point: dispatches LSP server or debug subcommand
  debug.go                      # `laravel-lsp debug` — index inspection tool
internal/
  indexer/
    container/                  # service-container binding indexer
      index.go                  # BindingIndex + Binding types
      symbols.go                # classDecl + symbolTable (phase 1 output)
      scan.go                   # phase 1 traversal: build symbolTable
      extract.go                # phase 2 traversal: extract Bindings
      walk.go                   # Walk() + ReindexFile()
    eloquent/                   # Eloquent model attribute indexer
      catalog.go                # ModelCatalog + ModelIndex + attribute types
      symbols.go                # classDecl + symbolTable (phase 1 output)
      scan.go                   # phase 1 traversal: build symbolTable
      attributes.go             # modern/legacy accessors, mutators, relationships
      arrays.go                 # $fillable / $casts / $appends / $hidden
      extract.go                # per-file catalog extraction
      walk.go                   # Walk() + ReindexFile()
    idehelper/
      merge.go                  # _ide_helper_models.php stub parser
  lsp/
    server.go                   # Server struct — owns all LSP state + handler methods
    definition.go               # textDocument/definition — container + Eloquent dispatch
    references.go               # textDocument/references
    hover.go                    # textDocument/hover
    completion.go               # textDocument/completion + varTypeVisitor
    rename.go                   # textDocument/rename + prepareRename
    diagnostics.go              # textDocument/publishDiagnostics
    codeaction.go               # textDocument/codeAction
    symbols.go                  # textDocument/documentSymbol
    scope.go                    # $var → FQN inference (assignments + typed params)
    documents.go                # DocumentStore — in-memory cache with disk fallback
    uri.go                      # URI/path conversion, UTF-16 position helpers
  phpnode/
    parse.go                    # ParseBytes() / ParseFile() + FromNode() using tree-sitter
  phpwalk/
    visitor.go                  # Visitor interface + NullVisitor + all Info types
    walk.go                     # Walk(path, src, tree, v) — depth-first CST traversal
    names.go                    # ClassConstFQN, UnwrapTypeName, ArgExprs helpers
  phputil/
    fqn.go                      # FQN type, UseMap, FileContext + Resolve()
    case.go                     # Snake/Studly/Camel — mirrors Illuminate\Support\Str
    ast.go                      # ClassFQN, LastSegment helpers
    location.go                 # Location type (parser-agnostic byte-offset struct)
testdata/
  bindings/                     # PHP fixtures for container indexer tests
  models/                       # PHP fixtures for Eloquent indexer tests
  idehelper/                    # _ide_helper_models.php fixture
```

## Architecture

**Two-phase compiler model:**
- Phase 1 (symbol scan): walk all `.php` files under the project root, build a
  class FQN → extends/location map. This lets us detect ServiceProvider / Model
  subclasses transitively without requiring file-order guarantees.
- Phase 2 (extraction): re-walk the same files; for each class in the target
  hierarchy run the extractor visitor to emit index entries.

Indexer packages are **pure** — they take a root path and return an index value.
No LSP types leak into them. The lsp package is the only layer that touches
`tliron/glsp` protocol types.

**Visitor pattern** (`phpwalk`): implement `phpwalk.Visitor`, embed
`phpwalk.NullVisitor` for no-op defaults, override only the methods needed.
`phpwalk.Walk(path, src, tree, visitor)` does a depth-first pre-order traversal
of the tree-sitter CST, calling typed callbacks for each relevant PHP node kind.
The visitor cannot prune recursion — all nodes are always visited.

**FileContext**: built incrementally during a traversal. PHP files have
`namespace` before class declarations, so `VisitNamespace` / `VisitUseItem`
always fire before `VisitClass`. Visitors maintain their own `fc` and call
`fc.Resolve(name)` to turn unqualified/aliased names into FQNs.

**tree-sitter grammar notes:**
- `instanceof` is parsed as `binary_expression` with `operator: instanceof`,
  not as an `instanceof_expression` node.
- `class_constant_access_expression` (`Foo::class`) has no named fields —
  children are positional: first `name`/`qualified_name` = class, second = constant.
- `optional_type` is the nullable type wrapper (`?Foo`), not `nullable_type`.
- `string` nodes wrap their content in a `string_content` child; use that child
  to extract the value without quotes.
- `array_element_initializer` has no named fields for its children either; the
  key/value are positional string nodes.

**`$this` in variable_name nodes**: `Utf8Text(src)` on a `variable_name` node
returns the full text including `$`, e.g. `"$this"`. Variable name comparisons
always use the full `$`-prefixed string.

## Key design decisions

- **PHP version support**: tree-sitter-php v0.24.2 parses PHP 5–8.x with full
  error recovery. PHP 8.4 property hooks and asymmetric visibility parse cleanly
  (`HasError() == false`). Files always produce a usable tree; no files are
  skipped due to parser version limits.
- **Case translation**: mirrors `Illuminate\Support\Str` exactly. `Snake()`
  uses a per-character lookahead — `"HTMLParser"` → `"h_t_m_l_parser"` (each
  capital gets its own underscore, no run collapsing). Tests cover all 15 rows
  from the plan including the starred edge cases.
- **IDE-helper policy**: when `SourceIdeHelper` is the only source for a name,
  return nothing. `ModelAttribute.Source` is the filter; AST entries always win.
- **Closure bindings**: only extracted when the closure body is a single
  `return_statement` with an `object_creation_expression` (or an `arrow_function`
  whose body is `object_creation_expression`). Multi-statement closures emit a
  `Binding` with empty `Concrete`.

## Testing conventions

- External test packages (`package container_test`, `package eloquent_test`) for
  black-box tests; internal package only for debug/traversal tests.
- Fixtures live in `testdata/` at repo root. Referenced by relative path:
  `../../../testdata/bindings` from `internal/indexer/container/`.
- Table-driven tests where multiple input cases exist.
- Race detector must pass: `make test-race`.

## LSP handler wiring

`textDocument/definition` is live for two flows:

1. **Container lookup** — cursor on `X::class` inside `app()`/`resolve()`/`make()` or
   `App::make()`. Resolves `X` to its FQN, looks up `BindingIndex`, returns the
   concrete class declaration location.

2. **Eloquent property access** — cursor on the property name in `$this->prop` or
   `$param->prop` where `$param` is a typed method parameter. Resolves the LHS
   type, looks up `ModelIndex`, returns accessor/fillable locations ranked by kind.

3. **Chained Eloquent access** — cursor on `$a->rel->prop` resolves through the
   relationship (`rel`) to the related model and jumps to `prop` in that model.

4. **Bare class-name cursor** — `new ClassName(...)`, `ClassName::method(...)`,
   `$x instanceof ClassName` all jump to the container concrete when the class
   is bound.

5. **Assignment scope** — `$user = User::find(...)` / `new User(...)` infers
   the variable type so `$user->prop` resolves without a type hint.

6. **`textDocument/references`** — finds all `$model->propName` accesses (Eloquent)
   or `AbstractClass::class` usages (container) across `app/` and `routes/`.

7. **`textDocument/completion`** — property name completions after `->` using
   the Eloquent model index.

8. **`textDocument/hover`** — shows attribute kind and model FQN for Eloquent
   properties; shows bound concrete for container abstracts.

9. **`_ide_helper_models.php` merge** — `@property` / `@method` doc-comment
   entries are merged into the model index (AST entries win on conflict).

10. **File watcher** — `app/` and `routes/` are watched via `fsnotify`; a
    500 ms debounce triggers a full reindex on any PHP file change.

11. **UTF-16 column handling** — LSP column offsets are correct for files
    containing multi-byte Unicode characters (e.g. emoji in strings/comments).

**Known limitations:**
- References scan covers `app/` and `routes/` only (configurable via `referenceScanDirs`).
- Chained access resolves through one Relationship hop only (not `$a->b->c->d`).
- Relationship detection requires `return $this->relationMethod(Class::class)`;
  multi-statement bodies are not detected.

12. **`textDocument/rename`** — Eloquent property rename across files. Reference
    sites (`$model->propName`) and method-based declaration sites (modern/legacy
    accessors) are renamed. Array-based declarations (`$fillable`, `$casts`, etc.)
    are not renamed automatically.
    Container abstract rename is out of scope.

13. **`textDocument/prepareRename`** — validates the cursor is on a renameable
    Eloquent property and returns the exact token range. Returns nil for
    non-Eloquent symbols so editors correctly disable the rename action.

14. **`textDocument/publishDiagnostics`** — pushed on `DidOpen`/`DidChange`.
    Emits `Warning` for any `$model->undefinedProp` access on a model whose
    class is indexed. Cleared on `DidClose`. Does not fire for variables whose
    type cannot be resolved (avoids false positives).

**tree-sitter EndByte convention**: `EndByte()` is exclusive (one past the last
byte), matching LSP's exclusive range end. `toLSPRange` uses it directly.

15. **Incremental reindex** — file-save events trigger per-file reindex instead
    of a full walk. Both `BindingIndex` and `ModelIndex` retain their symbol
    tables (`Syms()`). `container.ReindexFile` / `eloquent.ReindexFile` clone
    the symbol table, remove the changed file's declarations, re-scan the file,
    re-resolve the transitive sets, then return a new index with only that file's
    entries replaced. The server swaps the new indexes atomically under `s.mu`.
    Falls back to full reindex when the symbol table is absent (first run).

16. **`textDocument/codeAction`** — four quick-fixes offered per `unknown property`
    diagnostic: "Add to `$fillable`", "Add to `$casts`" (`'prop' => 'string'`),
    "Add to `$appends`", "Add to `$hidden`". A single `arrayPropVisitor` AST
    pass finds all four arrays; each action inserts at the appropriate point.
    Requires `cat.Path` set on `ModelCatalog` (populated by Walk).

17. **`textDocument/documentSymbol`** — returns all exposed Eloquent attributes
    for model files as `DocumentSymbol` entries. Method-based attributes appear
    as `SymbolKindMethod`; array-entry attributes appear as `SymbolKindProperty`.
    Returns nil for non-model files so the editor falls through to other
    providers (Intelephense, Psalm, etc.).

## What is not yet implemented

- `textDocument/rename` for container abstracts (requires full PHP class rename)
- Diagnostics for renamed properties (stale after rename until next reindex)

## Dependencies

| Package | Use |
|---------|-----|
| `github.com/tree-sitter/go-tree-sitter` | CGo bindings for the tree-sitter runtime |
| `github.com/tree-sitter/tree-sitter-php` | PHP grammar (supports PHP 5–8.x) |
| `github.com/tliron/glsp` | LSP protocol 3.16 + stdio transport |
| `github.com/tliron/commonlog` | Structured logging to stderr |
| `github.com/fsnotify/fsnotify` | File watcher for incremental reindex |
