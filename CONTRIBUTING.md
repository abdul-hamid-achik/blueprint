# Contributing to Blueprint

Thank you for your interest in contributing to Blueprint! This guide will help you get started.

## Development Setup

### Prerequisites

- **Go 1.25.5+** (per `go.mod`; no external Go dependencies are used)
- **Bun 1.x** for the JavaScript tooling: the docs site, the VS Code extension
  checks, and the generated projects that `bp run`, `bp dev`, and `bp test`
  start
- **Node.js 22** (CI runs the npm-based `packages/runtime` tests on it)
- **Git**

### Clone and Build

```bash
git clone https://github.com/abdul-hamid-achik/blueprint.git
cd blueprint
go build -o bin/bp ./cmd/bp
```

### Run Tests

```bash
# Run all tests (CI runs this with -race and coverage)
go test ./... -count=1

# Run tests for a specific package
go test ./internal/checker/ -count=1

# Run with the race detector, as CI does
go test -race ./... -count=1

# Regenerate the Node golden files after an intentional codegen change,
# then review the diff under internal/codegen/js/
UPDATE_GOLDEN=1 go test ./internal/codegen/js/ -run TestGolden

# Run fuzz targets (the formatter round-trip guard is FuzzFormatRoundTrip)
go test ./internal/parser/ -fuzz=FuzzParseFile -fuzztime=30s
go test ./internal/ast/ -fuzz=FuzzFormatRoundTrip -fuzztime=30s
```

`go test ./...` includes `TestErrorCodesDocInSync` in `internal/diag/`, which
fails when `internal/diag/error-codes.md` and `docs/error-codes.md` differ.
Keep the two files identical when you document a diagnostic code.

### Verify a Change

Before opening a pull request, run the project gate from the repository root:

```bash
go vet ./...
go test ./... -count=1
go build -o bin/bp ./cmd/bp
./bin/bp check testdata/valid/all_features.bp
```

For Node generator changes, also build the reference fixture into a scratch
directory and type-check it with Bun:

```bash
./bin/bp build testdata/valid/all_features.bp --target node --out /tmp/blueprint-node
cd /tmp/blueprint-node && bun install && bun run build
```

For the documentation site, run `cd docs && bun install && bun run docs:build`.
For the VS Code extension, run `cd editors/vscode-blueprint && npm ci && npm run check` (it uses the committed `package-lock.json`, as CI does).

## Project Structure

```
cmd/bp/                   CLI entry point (main.go), path expansion (paths.go), init templates (templates/)
internal/
  lexer/                  Hand-written tokenizer (about 100 token kinds)
  parser/                 Recursive descent parser, Pratt expressions, panic-mode recovery
  ast/                    AST node types, visitor/walker, and the bp fmt printer (printer.go)
  checker/                Semantic analysis: scopes, name resolution, validation, structured C-codes
  resolve/                Target-neutral facts shared by the generators
  codegen/                Generator interface, OutputFile, and the manifest-aware writer (writer.go)
    js/                   Node target: Hono, Drizzle, Zod (strings.Builder, no templates)
    python/               Python target: FastAPI, SQLAlchemy, Pydantic, Alembic
    effect/               Experimental Effect health/config scaffold
  diag/                   Structured diagnostics and the bp explain catalog (error-codes.md)
  lsp/                    Language server behind bp lsp
  linter/                 Best-practice linter (bp lint)
  docs/                   OpenAPI 3.1 generator (bp docs)
  generate/               LLM slot resolution via Anthropic API (bp generate)
  importer/               Structure recovery for bp import --from ts
  agentctx/               Embedded guidance for bp context and bp llms
  registry/               Registry groundwork (no CLI commands yet)
docs/                     Public documentation site (VitePress)
editors/                  VS Code extension and Neovim syntax plugin
packages/runtime/         npm runtime package
testdata/
  valid/                  .bp fixtures that must parse and check without errors
  invalid/                .bp fixtures that must produce parser or checker errors
```

[AGENTS.md](./AGENTS.md) has the architecture, output ownership rules, and
verified limitations in more detail.

## How to Add a New Language Construct

Adding a new keyword or block type to Blueprint touches four layers. Here is the typical workflow:

### 1. Lexer (`internal/lexer/`)

- Add a new `Token` constant (e.g., `TokenMyKeyword`)
- Add the keyword string to the `keywords` map so the lexer recognizes it
- Add any new operators or punctuation if needed

### 2. Parser (`internal/parser/`)

- Add a parse function (e.g., `parseMyBlock()`) that builds an AST node
- Wire it into the top-level dispatch in `parseTopLevelBlock()` or the appropriate statement parser
- Handle error recovery so one bad block does not cascade

### 3. AST (`internal/ast/`)

- Define the new node struct implementing the appropriate interface (`TopLevel`, `ArrowStmt`, `Expr`, etc.)
- Add it to `printer.go` so `bp fmt` can round-trip the node
- Ensure the node has a `Loc` field and implements `Location()`

### 4. Checker (`internal/checker/`)

- Add validation for the new node in `validateBlocks()`
- Check naming conventions, reference validity, structural rules
- Add the new symbol kind to `scope.go` if it introduces a named declaration
- Give each new error a structured code (`C0xx`) and document it in
  `internal/diag/error-codes.md`, then copy that file to `docs/error-codes.md`.
  `bp explain` reads the first file, and the catalog test checks that each
  documented example reproduces its code

### 5. Code Generator (`internal/codegen/js/`)

- Add the generation logic in `generator.go` and/or `helpers.go`
- Produce the appropriate TypeScript output files
- Ensure deterministic output (sort maps before iterating)
- Reject unsupported constructs with an error instead of dropping them, and
  update the golden files (`UPDATE_GOLDEN=1 go test ./internal/codegen/js/ -run TestGolden`)
  when the output changes on purpose. See [AGENTS.md](./AGENTS.md) for the
  Python and Effect targets' fail-closed rules

### 6. Tests

- Add valid `.bp` fixtures in `testdata/valid/`
- Add invalid `.bp` fixtures in `testdata/invalid/`
- Add unit tests for the new parser, checker, and codegen behavior

## Code Style Guidelines

- **Format with `gofmt`** -- all Go code must be formatted with `gofmt`
- **No external Go dependencies** -- the entire toolchain uses only the Go standard library
- **Deterministic output** -- sort maps before iterating to ensure reproducible codegen
- **snake_case in .bp** -- Blueprint source uses `snake_case` for identifiers, `PascalCase` for types/enums, `SCREAMING_SNAKE_CASE` for secrets/env
- **Flat by force** -- max 1 level of `{}` nesting in .bp (except `try/recover`)
- **Use `strings.Builder`** -- for codegen, not `fmt.Sprintf` concatenation or templates

## Pull Request Workflow

1. **Fork** the repository and create a feature branch from `main`
2. **Make your changes** following the style guidelines above
3. **Add tests** for any new functionality
4. **Format your code**: `gofmt -w .`
5. **Run the full test suite and vet**: `go vet ./...` and `go test ./... -count=1`
6. **Open a pull request** against `main` with a clear description

### PR Checklist

- [ ] No external Go dependencies added
- [ ] Code formatted with `gofmt`
- [ ] `go vet ./...` and all existing tests pass
- [ ] New tests added for new functionality
- [ ] Generated output is deterministic (sorted maps)
- [ ] Golden files and `internal/diag/error-codes.md` and `docs/error-codes.md` updated together when a code or Node output changes on purpose

## Issue Reporting Guidelines

When reporting a bug, please include:

1. **Blueprint version** (`bp version`)
2. **The `.bp` source** that triggers the issue (minimal reproduction preferred)
3. **Expected behavior** vs **actual behavior**
4. **Error messages** if any
5. **Operating system** and Go version (if building from source)

For feature requests, describe the use case and proposed `.bp` syntax if applicable.

## Questions?

Open a GitHub issue or discussion. We are happy to help!
