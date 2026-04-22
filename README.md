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

- Go 1.23+
- PHP 8.1+ project (Laravel 10+)

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

## Editor setup

### Neovim

```lua
vim.lsp.start({
  name = 'laravel-lsp',
  cmd = { vim.fn.exepath('laravel-lsp') },
  root_dir = vim.fs.root(0, { 'artisan', 'composer.json' }),
  filetypes = { 'php' },
})
```

### VS Code

Use any extension that accepts a generic LSP server and point it at the binary.

## Project structure

```
cmd/laravel-lsp/
  main.go                     # entry point — LSP server over stdio
internal/
  indexer/
    container/                # service-container binding indexer
      index.go                # BindingIndex type + lookup/reindex methods
      symbols.go              # cross-file class declaration map
      scan.go                 # phase 1: build symbol table
      extract.go              # phase 2: extract bindings from ServiceProviders
      walk.go                 # Walk() + ReindexFile()
    eloquent/                 # Eloquent model attribute indexer
      catalog.go              # ModelCatalog + ModelIndex types
      symbols.go              # cross-file class declaration map
      scan.go                 # phase 1: build symbol table
      attributes.go           # modern/legacy accessors, mutators, relationships
      arrays.go               # $fillable / $casts / $appends / $hidden
      extract.go              # per-file catalog extraction
      walk.go                 # Walk() + ReindexFile()
    idehelper/
      merge.go                # _ide_helper_models.php stub parser
  lsp/
    server.go                 # Server struct — owns all state and handler methods
    definition.go             # textDocument/definition
    references.go             # textDocument/references
    hover.go                  # textDocument/hover
    completion.go             # textDocument/completion
    rename.go                 # textDocument/rename + prepareRename
    diagnostics.go            # textDocument/publishDiagnostics
    codeaction.go             # textDocument/codeAction
    symbols.go                # textDocument/documentSymbol
    scope.go                  # $var → FQN inference (assignments + typed params)
    documents.go              # DocumentStore — in-memory cache with disk fallback
    uri.go                    # URI/path conversion, UTF-16 position helpers
  phpparse/
    parse.go                  # shared PHP 8.1 parse helpers
  phputil/
    fqn.go                    # FQN type, UseMap, FileContext.Resolve()
    case.go                   # Snake/Studly/Camel — mirrors Illuminate\Support\Str
    ast.go                    # NameToString, ClassName helpers
    location.go               # Location type + FromPosition()
testdata/
  bindings/                   # PHP fixtures for container indexer tests
  models/                     # PHP fixtures for Eloquent indexer tests
  idehelper/                  # _ide_helper_models.php fixture
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

Uses [`github.com/VKCOM/php-parser`](https://github.com/VKCOM/php-parser) v0.8.2,
pinned to PHP 8.1 config. Files using PHP 8.2+ syntax that the parser cannot
handle are logged to stderr and skipped; the partial AST is still processed.

## License

MIT
