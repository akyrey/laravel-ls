# laravel-lsp

A Go LSP server that brings Laravel-specific navigation to editors without
Laravel Idea. Provides jump-to-definition, find-references, hover, completion,
rename, diagnostics, and code actions across Laravel's runtime conventions that
generic PHP language servers miss.

## Why

Intelephense and Phpactor understand PHP types but not Laravel semantics.
`$user->email_address` is a dead end even though it resolves at runtime to an
`emailAddress()` `Attribute` method. `app(PaymentGateway::class)` resolves
through the service container, not a PHP call. This server indexes both.

## Features

### Service container

Jump from an abstract/interface reference to its concrete binding.

- `$this->app->bind/singleton/scoped/instance(...)` in `ServiceProvider` subclasses
- `App::bind(...)` facade static calls
- PHP 8 attributes: `#[Bind]`, `#[Singleton]`, `#[ScopedBind]`
- Closure bindings where the body is a single `return new X(...)`
- Cursor on a bare class/interface name also jumps to the concrete

### Eloquent model attributes

Jump from `$user->email_address` to the accessor declaration.

- Modern accessors: methods returning `Illuminate\Database\Eloquent\Casts\Attribute`
- Legacy accessors/mutators: `getXxxAttribute()` / `setXxxAttribute()` methods
- Array properties: `$fillable`, `$casts`, `$appends`, `$hidden`
- Relationships: typed (`: HasMany`) and untyped (`return $this->hasMany(...)`) methods
- Chained access: `$user->posts->slug_url` resolves through the relationship to `Post`
- Assignment inference: `$user = User::find(...)` / `new User()` infers the type without a type hint
- `_ide_helper_models.php` entries merged in (AST entries win on conflict)

### LSP handlers

| Handler | Behaviour |
|---------|-----------|
| `textDocument/definition` | Container and Eloquent jump targets |
| `textDocument/references` | All `$model->prop` / `AbstractClass::class` usages |
| `textDocument/hover` | Attribute kind + model FQN; bound concrete for containers |
| `textDocument/completion` | Property name completions after `->` |
| `textDocument/rename` | Eloquent property rename across accessor declarations and reference sites |
| `textDocument/prepareRename` | Validates cursor is on a renameable Eloquent property |
| `textDocument/publishDiagnostics` | `Warning` for `$model->unknownProp` on indexed models |
| `textDocument/codeAction` | Quick-fixes for unknown-property diagnostics: add to `$fillable`, `$casts`, `$appends`, or `$hidden` |
| `textDocument/documentSymbol` | All exposed attributes for model files as document symbols |

### Indexing

- **Two-phase**: symbol scan builds the class hierarchy; extraction pass emits index entries
- **Incremental reindex**: file-save events trigger per-file reindex instead of a full walk
- **File watcher**: `app/` and `routes/` watched via `fsnotify`; 500 ms debounce

## Requirements

- Go 1.23+ with CGo enabled (required by the tree-sitter PHP grammar)
- PHP 8.1+ project (Laravel 10+); PHP 8.4 and 8.5 are fully supported

## Installation

```bash
go install github.com/akyrey/laravel-lsp/cmd/laravel-lsp@latest
```

Or build from source:

```bash
git clone https://github.com/akyrey/laravel-lsp.git
cd laravel-lsp
make build
```

## Configuration

All settings are passed as `initializationOptions` in the LSP `initialize`
request. All fields are optional.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `scanDirs` | `string[]` | `["app"]` | Directories to index (models, service providers). Supports single-level glob patterns. |
| `referenceDirs` | `string[]` | *(auto)* | Directories searched when finding references and rename sites. Defaults to `scanDirs + ["routes"]`. Supports glob patterns. |

`referenceDirs` is auto-derived from `scanDirs + ["routes"]` when not explicitly
set — so adding module directories to `scanDirs` is usually all you need.

Glob patterns use `filepath.Glob` semantics: `*` matches any sequence of
non-separator characters within one directory level. `**` is not supported.

**Modular Laravel (e.g. nwidart/laravel-modules) — minimal config:**
```json
{
  "scanDirs": ["app", "Modules/*/app"]
}
```
This automatically sets `referenceDirs` to `["app", "Modules/*/app", "routes"]`.

**Override `referenceDirs` explicitly if you need finer control:**
```json
{
  "scanDirs":      ["app", "Modules/*/app"],
  "referenceDirs": ["app", "routes", "Modules/*/app", "Modules/*/routes"]
}
```

## Editor setup

### Neovim (minimal)

```lua
vim.lsp.start({
  name = 'laravel-lsp',
  cmd = { vim.fn.exepath('laravel-lsp') },
  root_dir = vim.fs.root(0, { 'artisan', 'composer.json' }),
  filetypes = { 'php' },
})
```

### Neovim (modular project)

```lua
vim.lsp.start({
  name = 'laravel-lsp',
  cmd = { vim.fn.exepath('laravel-lsp') },
  root_dir = vim.fs.root(0, { 'artisan', 'composer.json' }),
  filetypes = { 'php' },
  init_options = {
    -- referenceDirs auto-derives to { 'app', 'Modules/*/app', 'routes' }
    scanDirs = { 'app', 'Modules/*/app' },
  },
})
```

### VS Code

Use any extension that accepts a generic LSP server, point it at the binary,
and pass `initializationOptions` with the fields above.

## Debug command

Inspect what the server would index without starting an editor session:

```bash
# Text output (models + bindings)
laravel-lsp debug /path/to/laravel-project

# Flags
laravel-lsp debug -models   /path/to/laravel-project   # models only
laravel-lsp debug -bindings /path/to/laravel-project   # bindings only
laravel-lsp debug -json     /path/to/laravel-project   # JSON output
laravel-lsp debug -dirs app,src /path/to/project       # custom scan dirs
laravel-lsp debug --help                               # full flag list
```

Example text output:

```
=== Models (2) ===

App\Models\User
  app/Models/User.php
  email_address                  accessor  via emailAddress()
  email_address                  $fillable
  posts                          relation  → App\Models\Post

=== Container bindings (3) ===

App\Contracts\PaymentGateway
  → App\Services\StripeGateway  [transient, call]
  source: app/Providers/AppServiceProvider.php:16
  target: app/Services/StripeGateway.php:7
```

## Project structure

```
cmd/laravel-lsp/
  main.go                   # entry point — LSP server or debug subcommand
  debug.go                  # laravel-lsp debug — index inspection tool
internal/
  indexer/
    container/              # service-container binding indexer
      index.go              # BindingIndex type
      symbols.go            # cross-file class declaration map
      scan.go               # phase 1: build symbol table
      extract.go            # phase 2: extract bindings from ServiceProviders
      walk.go               # Walk() + ReindexFile()
    eloquent/               # Eloquent model attribute indexer
      catalog.go            # ModelCatalog + ModelIndex types
      symbols.go            # cross-file class declaration map
      scan.go               # phase 1: build symbol table
      attributes.go         # modern/legacy accessors, mutators, relationships
      arrays.go             # $fillable / $casts / $appends / $hidden
      extract.go            # per-file catalog extraction
      walk.go               # Walk() + ReindexFile()
    idehelper/
      merge.go              # _ide_helper_models.php stub parser
  lsp/
    server.go               # Server struct — owns all state and handler methods
    definition.go           # textDocument/definition
    references.go           # textDocument/references
    hover.go                # textDocument/hover
    completion.go           # textDocument/completion
    rename.go               # textDocument/rename + prepareRename
    diagnostics.go          # textDocument/publishDiagnostics
    codeaction.go           # textDocument/codeAction
    symbols.go              # textDocument/documentSymbol
    scope.go                # $var → FQN inference (assignments + typed params)
    documents.go            # DocumentStore — in-memory cache with disk fallback
    uri.go                  # URI/path conversion, UTF-16 position helpers
  phpnode/
    parse.go                # ParseBytes() / ParseFile() + FromNode() using tree-sitter
  phpwalk/
    visitor.go              # Visitor interface, NullVisitor, all Info types
    walk.go                 # Walk(path, src, tree, v) — depth-first CST traversal
    names.go                # ClassConstFQN, UnwrapTypeName, ArgExprs helpers
  phputil/
    fqn.go                  # FQN type, UseMap, FileContext + Resolve()
    case.go                 # Snake/Studly/Camel — mirrors Illuminate\Support\Str
    ast.go                  # ClassFQN, LastSegment helpers
    location.go             # Location type (parser-agnostic byte-offset struct)
testdata/
  bindings/                 # PHP fixtures for container indexer tests
  models/                   # PHP fixtures for Eloquent indexer tests
  idehelper/                # _ide_helper_models.php fixture
```

## Development

```bash
make build        # build ./laravel-lsp
make test         # go test ./...
make test-race    # go test -race ./...
make vet          # go vet ./...
make fmt          # gofmt -s -w .
make tidy         # go mod tidy && go mod verify
make lint         # golangci-lint run ./...
make install      # go install ./cmd/laravel-lsp
```

## Architecture

Indexer packages are **pure** — they take a filesystem root and return an index
value. No LSP types leak into them. The `lsp` package is the only layer that
touches `tliron/glsp` protocol types.

**Two-phase indexing:**
1. Symbol scan — walk all `.php` files, build a class FQN → extends/location map
2. Extraction — re-walk files in the target class hierarchy and emit index entries

**Visitor pattern** (`phpwalk`): implement `phpwalk.Visitor`, embed
`phpwalk.NullVisitor` for no-op defaults, and call `phpwalk.Walk(path, src,
tree, v)` for a depth-first pre-order traversal. Each node kind gets a typed
callback (e.g. `VisitClass(ClassInfo)`, `VisitPropertyFetch(PropertyFetchInfo)`).

**Case translation** mirrors `Illuminate\Support\Str` exactly. `snake()` uses a
per-character lookahead so `"HTMLParser"` → `"h_t_m_l_parser"`.

**Position handling** — all cursor positions use UTF-16 code units (the LSP
wire format). `positionToByteOffset` and `countUTF16Units` handle surrogate
pairs for files with emoji or other non-BMP characters.

## Known limitations

- References scan covers `app/` and `routes/` only
- Chained access resolves through one relationship hop
- Relationship detection requires `return $this->relationMethod(Class::class)`;
  multi-statement bodies are not detected
- `textDocument/rename` covers Eloquent properties only (not container abstracts)

## PHP parser

Uses [`tree-sitter-php`](https://github.com/tree-sitter/tree-sitter-php) v0.24.2
via [`go-tree-sitter`](https://github.com/tree-sitter/go-tree-sitter) v0.25.0
(CGo). The grammar tracks the PHP language and supports PHP 5 through 8.x,
including PHP 8.4 property hooks and asymmetric visibility. tree-sitter always
produces a tree with full error recovery — no files are skipped due to parser
version limits.

## License

MIT
