# CLAUDE.md

Project context for Claude Code sessions.

## What is this project?

`laravel-ls` is a Go LSP server that provides Laravel-specific
jump-to-definition and find-references for editors that lack a Laravel Idea
equivalent (Neovim, VS Code). It indexes Laravel's runtime conventions that
generic PHP language servers miss: service container bindings and Eloquent
model attribute accessors.

## Tech stack

- **Language**: Go 1.23+
- **PHP parser**: `github.com/VKCOM/php-parser` v0.8.2 (VKCOM fork, PHP 8.1 config)
- **LSP framework**: `github.com/tliron/glsp` v0.2.2, protocol 3.16, stdio transport
- **Build**: `make build` (outputs `./laravel-ls`) or `go build -o laravel-ls ./cmd/laravel-lsp`
- **Tests**: `make test` / `go test ./...`

## Commands

```bash
make build        # build ./laravel-ls
make test         # go test ./... -count=1
make test-race    # go test -race ./... -count=1
make vet          # go vet ./...
make fmt          # gofmt -s -w .
make tidy         # go mod tidy && go mod verify
make lint         # golangci-lint run ./... (requires golangci-lint)
make install      # go install ./cmd/laravel-lsp
```

Always run `make test-race` before committing.

## Project layout

```
cmd/laravel-lsp/main.go             # entry point only — no logic here
internal/
  indexer/
    container/                      # service-container binding indexer
      index.go                      # BindingIndex + Binding types
      symbols.go                    # classDecl + symbolTable (phase 1 output)
      scan.go                       # phase 1 traversal: build symbolTable
      extract.go                    # phase 2 traversal: extract Bindings
      walk.go                       # Walk() — two-phase entry point
    eloquent/                       # Eloquent model attribute indexer
      catalog.go                    # ModelCatalog + ModelIndex + attribute types
      symbols.go                    # classDecl + symbolTable (phase 1 output)
      scan.go                       # phase 1 traversal: build symbolTable
      attributes.go                 # extract modern + legacy accessor/mutator methods
      arrays.go                     # extract $fillable/$casts/$appends/$hidden
      extract.go                    # per-file catalog extraction
      walk.go                       # Walk() — two-phase entry point
    idehelper/merge.go              # _ide_helper_models.php stub parser (not yet implemented)
  lsp/
    server.go                       # Server struct — owns all LSP state + handler methods
    definition.go                   # textDocument/definition — container + Eloquent dispatch
    references.go                   # textDocument/references (returns nil — v0.2)
    documents.go                    # DocumentStore — in-memory doc cache with disk fallback
    uri.go                          # URIToPath, PathToURI, toLSPLocation, positionToByteOffset
  phpparse/
    parse.go                        # Bytes() + File() — shared PHP 8.1 parse helpers
  phputil/
    fqn.go                          # FQN type, UseMap, FileContext + Resolve()
    case.go                         # Snake/Studly/Camel — mirrors Illuminate\Support\Str
    ast.go                          # NameToString, ClassName, AddUsesToContext, ClassNodeFQN
    location.go                     # Location type + FromPosition()
  resolver/
    scope.go                        # $var → FQN tracking per function (not yet implemented)
    expr.go                         # expression type resolver (not yet implemented)
testdata/
  bindings/                         # PHP fixtures for container indexer tests
  models/                           # PHP fixtures for Eloquent indexer tests
  idehelper/                        # _ide_helper_models.php fixture
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

**Visitor pattern**: embed `visitor.Null` (VKCOM's 170-method no-op visitor),
override only the methods needed. The traverser dispatches via `n.Accept(v)`,
then recurses into children automatically — no manual child traversal needed.

**FileContext**: built incrementally during a traversal. PHP files have
`namespace` before class declarations, so the namespace and `use` statements
are always populated by the time `StmtClass`/`StmtInterface` fires. When
calling `Resolve()` on a name node, always call `phputil.NameToString()` first
to get the raw string, then `fc.Resolve()`.

**`NameToString` for fully-qualified names**: `*ast.NameFullyQualified` nodes
(PHP's `\Foo\Bar` syntax) return `"\Foo\Bar"` (with leading backslash) so that
`FileContext.Resolve` recognises and strips it correctly. Do not call `Resolve`
on a raw `joinNameParts` result for FQ names — it will prepend the namespace.

**`ExprVariable.Name` for `$this`**: `Identifier.Value` is `"$this"` (dollar
sign included), not `"this"`. All variable name comparisons must include `$`.

## Key design decisions

- **PHP 8.1 parser config**: pinned to `{Major:8, Minor:1}` (official max
  supported by VKCOM v0.8.2). PHP 8.2/8.3 files that produce parse errors are
  logged to stderr and skipped; the partial AST is still processed.
- **Case translation**: mirrors `Illuminate\Support\Str` exactly. `Snake()`
  uses a per-character lookahead — `"HTMLParser"` → `"h_t_m_l_parser"` (each
  capital gets its own underscore, no run collapsing). Tests cover all 15 rows
  from the plan including the starred edge cases.
- **IDE-helper policy**: when `SourceIdeHelper` is the only source for a name,
  return nothing (option b from the plan). `ModelAttribute.Source` is the
  filter; AST entries always win.
- **Closure bindings**: only extracted when the closure body is a single
  `StmtReturn` with an `ExprNew` (or an `ExprArrowFunction` whose body is
  `ExprNew`). Multi-statement closures emit a `Binding` with empty `Concrete`.

## Testing conventions

- External test packages (`package container_test`, `package eloquent_test`) for
  black-box tests; internal package only for debug/traversal tests.
- Fixtures live in `testdata/` at repo root. Referenced by relative path:
  `../../../testdata/bindings` from `internal/indexer/container/`.
- Table-driven tests where multiple input cases exist.
- Race detector must pass: `make test-race`.

## LSP handler wiring (v0.1)

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

12. **`textDocument/rename`** — Eloquent property rename across files. Reference
    sites (`$model->propName`) and method-based declaration sites (modern/legacy
    accessors) are renamed. Array-based declarations (`$fillable`, `$casts`, etc.)
    are not renamed automatically (their stored location points to the whole
    property list, not the individual string literal; update them manually).
    Container abstract rename is out of scope.

13. **`textDocument/prepareRename`** — validates the cursor is on a renameable
    Eloquent property and returns the exact token range. Returns nil for
    non-Eloquent symbols so editors correctly disable the rename action.

14. **`textDocument/publishDiagnostics`** — pushed on `DidOpen`/`DidChange`.
    Emits `Warning` for any `$model->undefinedProp` access on a model whose
    class is indexed. Cleared on `DidClose`. Does not fire for variables whose
    type cannot be resolved (avoids false positives).

**VKCOM parser EndPos convention**: `EndPos` is exclusive (one past the last
byte), matching LSP's exclusive range end. `toLSPRange` uses it directly
without adding 1.

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
| `github.com/VKCOM/php-parser` | PHP 8 AST parser |
| `github.com/tliron/glsp` | LSP protocol 3.16 + stdio transport |
| `github.com/tliron/commonlog` | Structured logging to stderr |
