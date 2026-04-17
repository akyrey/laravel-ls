# laravel-ls

A Go LSP server that brings Laravel-specific navigation to editors without
Laravel Idea. Provides jump-to-definition and find-references across Laravel's
runtime conventions that generic PHP language servers miss.

## Why

Intelephense and Phpactor understand PHP types but not Laravel semantics.
`$user->email_address` is a dead end even though it resolves at runtime to an
`emailAddress()` `Attribute` method. `app(PaymentGateway::class)` resolves
through the service container, not a PHP call. This server indexes both.

## Features

**Service container bindings** — jump from an interface reference to its concrete
class. Indexes:
- `$this->app->bind/singleton/scoped/instance(...)` calls in ServiceProvider subclasses
- `App::bind(...)` facade static calls
- PHP 8 attributes: `#[Bind]`, `#[Singleton]`, `#[ScopedBind]`
- Closure bindings where the body is a single `return new X(...)`

**Eloquent model attributes** — jump from `$user->email_address` to the
`emailAddress()` accessor method. Per-model catalog of:
- Modern accessors: methods returning `Illuminate\Database\Eloquent\Casts\Attribute`
- Legacy accessors/mutators: `getXxxAttribute()` / `setXxxAttribute()` methods
- Array properties: `$fillable`, `$casts`, `$appends`, `$hidden`

## Requirements

- Go 1.23+
- PHP 8.2+ project (Laravel 12 / 13)

## Installation

```bash
go install github.com/akyrey/laravel-ls/cmd/laravel-lsp@latest
```

Or build from source:

```bash
git clone https://github.com/akyrey/laravel-ls.git
cd laravel-ls
make build
```

## Editor setup

### Neovim

```lua
vim.lsp.start({
  name = 'laravel-ls',
  cmd = { vim.fn.exepath('laravel-ls') },
  root_dir = vim.fs.root(0, { 'artisan', 'composer.json' }),
  filetypes = { 'php' },
})
```

### VS Code

Install the [LSP client extension](https://marketplace.visualstudio.com/items?itemName=llvm-vs-code-extensions.vscode-clangd)
or any extension that accepts a generic LSP server, then point it at the binary.

## Project structure

```
cmd/laravel-lsp/
  main.go                     # entry point — LSP server over stdio
internal/
  indexer/
    container/                # service-container binding indexer
      index.go                # BindingIndex type + lookup methods
      symbols.go              # cross-file class declaration map
      scan.go                 # phase 1: build symbol table
      extract.go              # phase 2: extract bindings from ServiceProviders
      walk.go                 # Walk() — two-phase entry point
    eloquent/                 # Eloquent model attribute indexer
      catalog.go              # ModelCatalog + ModelIndex types
      symbols.go              # cross-file class declaration map
      scan.go                 # phase 1: build symbol table
      attributes.go           # extract modern + legacy accessor/mutator methods
      arrays.go               # extract $fillable / $casts / $appends / $hidden
      extract.go              # per-file catalog extraction
      walk.go                 # Walk() — two-phase entry point
    idehelper/
      merge.go                # _ide_helper_models.php stub parser (stub)
  lsp/
    server.go                 # glsp handler wiring; owns the indexes
    definition.go             # textDocument/definition dispatch (stub)
    references.go             # textDocument/references dispatch (stub)
  phputil/
    fqn.go                    # FQN type, UseMap, FileContext.Resolve()
    case.go                   # Snake/Studly/Camel — mirrors Illuminate\Support\Str
    ast.go                    # NameToString, ClassName helpers
    location.go               # Location type + FromPosition()
  resolver/
    scope.go                  # per-function $var → FQN tracking (stub)
    expr.go                   # expression type resolver (stub)
testdata/
  bindings/                   # PHP fixtures for container indexer tests
  models/                     # PHP fixtures for Eloquent indexer tests
  idehelper/                  # _ide_helper_models.php fixture
```

## Development

```bash
make build        # build ./laravel-ls
make test         # go test ./...
make test-race    # go test -race ./...
make vet          # go vet ./...
make fmt          # gofmt -s -w .
make tidy         # go mod tidy && go mod verify
make lint         # golangci-lint run ./...
```

## Architecture

Two-phase compiler analogy: indexers are the front-end (produce symbol tables),
resolver + lsp are the back-end (query them). Indexer packages are pure — they
take a filesystem root and return an index value. No LSP types leak into them.

**Two-phase indexing:**
1. Symbol scan — walk all `.php` files, build a class FQN → extends/location map
2. Extraction — re-walk files that belong to the target class hierarchy (ServiceProvider
   subclasses for container, Model subclasses for Eloquent) and extract entries

**Case translation** mirrors `Illuminate\Support\Str` exactly. `snake()` uses a
per-character lookahead so `"HTMLParser"` → `"h_t_m_l_parser"`, not `"html_parser"`.

## PHP parser

Uses [`github.com/VKCOM/php-parser`](https://github.com/VKCOM/php-parser) v0.8.2,
pinned to PHP 8.1 config. Files using PHP 8.2+ syntax that the parser cannot
handle are logged to stderr and skipped; the partial AST is still processed.

## License

MIT
