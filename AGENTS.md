# AGENTS.md — Blueprint Project Guide for AI Agents

This file is for Claude Code and other AI agents working on the Blueprint toolchain. Read it before making changes.

---

## What This Project Is

Blueprint is a Go toolchain that compiles `.bp` (Blueprint) source files into TypeScript/Node.js projects. The pipeline is:

```
.bp source → Lexer → Parser → AST → Checker → Codegen → TypeScript project
```

**SPEC.md is the single source of truth.** Every implementation decision must align with it. When in doubt, read the spec.

---

## Architecture

```
cmd/bp/main.go                  # CLI: bp check, bp build, bp version
internal/
  lexer/
    token.go                    # TokenKind enum (~95 kinds), Token struct
    lexer.go                    # Hand-written tokenizer
    lexer_test.go               # 50+ tests
  parser/
    error.go                    # ParseError type, FormatError()
    parser.go                   # Recursive descent, Pratt expressions, panic-mode recovery
    parser_test.go              # 61+ tests (including fixture-driven tests)
  ast/
    nodes.go                    # All AST node types (Blueprint, Model, Endpoint, ...)
    visitor.go                  # Visitor interface
    walk.go                     # Generic tree walker
    printer.go                  # BP file formatter
  checker/
    scope.go                    # Scope management, name resolution
    types.go                    # Type system implementation
    checker.go                  # Semantic validation (630 lines)
    checker_test.go             # 57+ tests
  codegen/
    codegen.go                  # Generator interface + OutputFile type
    js/                         # JavaScript/TypeScript code generator
      generator.go              # Main JS/TS generator
      helpers.go                # exprToJS, type mappings, query helpers
      routes.go                 # REST/STREAM/WS route generation
      schema.go                 # Drizzle schema generation
      functions.go              # Function and pipe generation
      static.go                 # package.json, tsconfig, Dockerfile
      imports.go                # Import collection and deduplication
      events.go                 # Event/stream handling
      templates/                # Code templates
      generator_test.go         # Codegen tests
      golden_test.go            # Golden file snapshot tests
  generate/
    generate.go                 # LLM integration for @> slots
  linter/
    linter.go                   # Lint rules (block-ordering, intent-on-endpoints, etc.)
    linter_test.go              # Linter tests
  docs/
    openapi.go                  # OpenAPI 3.1 spec generation
    docs_test.go                # Docs tests
  lsp/
    server.go                   # Language Server Protocol implementation
  registry/
    registry.go                 # Package registry (future: bp add/install)
testdata/
  valid/                        # 35+ valid .bp fixture files
  invalid/                      # 21+ invalid .bp fixture files (expected errors)
generated/                      # Output of: bp build testdata/valid/all_features.bp
examples/
  hello-world.bp
  todo-api.bp
```

---

## Milestone Status

| Milestone | Status |
|-----------|--------|
| M1: Lexer + Parser + AST | ✅ Complete |
| M2: Semantic Checker | ✅ Complete |
| M3: JavaScript Codegen | ✅ Complete (core) |
| M4: Developer Experience | ✅ Complete (init, fmt, lint, docs, dev, run) |
| M5: Testing + Migrations | ✅ Complete (test, migrate) |
| M6: LLM Integration | ✅ Complete (generate — resolves @> slots via Anthropic API) |
| M7: Polish + Launch | ✅ Complete (.goreleaser.yaml, release workflow, @blueprint/runtime) |

---

## Test Commands

```bash
go test ./...                   # run all tests (must always pass before committing)
go test ./internal/lexer/...    # lexer only
go test ./internal/parser/...   # parser only
go test ./internal/checker/...  # checker only
go test ./internal/codegen/...  # codegen only
go test ./internal/generate/... # generate/LLM tests
go test ./internal/linter/...   # linter tests
go test ./internal/docs/...     # docs/OpenAPI tests

# Build and validate the reference example
go build -o bin/bp ./cmd/bp
./bin/bp check testdata/valid/all_features.bp
./bin/bp build testdata/valid/all_features.bp --out generated

# Verify generated TypeScript has 0 errors
cd generated && bun install && bun run build
```

All 169 top-level tests (296 including subtests) must pass. The generated TypeScript must have 0 `tsc` errors.

---

## Key Design Rules (from SPEC.md §1.3)

1. **Flat by force** — max 1 level of `{}`. Only `try/recover` allows 2 levels.
2. **Intent is syntax** — `@` blocks are AST nodes, not comments.
3. **Arrows are directional** — `<-` inputs before `|>` steps before `->` outputs.
4. **No if/else** — use `guard` (early return) and `when` (conditional step).
5. **No loops** — use `map` for iteration.

---

## Codegen: Key Patterns

### `emitCtx` struct (generator.go)

Every call to `emitArrowStmts` passes an `emitCtx` carrying:
- `ctxVars`: Hono context variable names (`c.req.valid('json').foo`)
- `boundVars`: resolved JS expressions for Blueprint variables (e.g., `auth` → `(c as any).get('auth')`)
- `declared`: set of locally declared variable names
- `varModels`: model name for each bound variable
- `asyncFns`: set of function names that must be `await`ed
- `structEnums`: set of enum names that have struct bodies (→ use `PlanConfig[x as any]` not `Plan[x]`)

### `exprToJS` / `exprToJSWithCtx` (helpers.go)

Converts AST expressions to JavaScript strings. Context-aware version handles:
- `(c as any).get('auth')` for injected middleware vars
- `PlanConfig[x as any]` for struct enum bracket access
- `await fnCall()` for async functions
- `(c.req.header('X') as string)` for header access

### Struct enums (generator.go `genTypes`)

An enum with struct variants (e.g., `Plan { free { ... } }`) generates **two** exports:
1. `Plan` — `as const` string enum for DB column values
2. `PlanConfig: Record<string, any>` — config lookup object

### Filter accumulators (helpers.go `queryToJSWithCtx`)

When `query model where(filters)` receives an `*ast.Ident` (a variable reference instead of conditions), it expands to:
```typescript
and(...Object.entries(filters).map(([k, v]) => eq((schema.model as any)[k], v)))
```

### `genFunction` returns `[]codegen.OutputFile`

Functions with `impl node { module: "./internal/X" }` generate:
1. `src/functions/<name>.ts` — wrapper that imports and re-exports
2. `src/impl/functions/internal/X.ts` — user-owned stub file for the developer to implement

Implementation stubs must be marked `UserOwned: true`. `bp build` writes
generated files through `.blueprint/manifest.json`, but user-owned files are
scaffolded only when missing and must not be overwritten on later builds.

---

## Known Limitations / Gaps

These are acknowledged gaps between spec and current implementation:

### Lexer/Parser
- `header.X-API-Key` — hyphenated header names fail lexing. The lexer tokenizes `X-API-Key` as `X`, `-`, `API`, `-`, `Key`. Workaround: the parser handles `header.X` as a special two-token sequence; hyphen suffix is currently silently dropped.
- `using(secret.KEY)` syntax (SPEC §7.11 `auth webhook_sig using(...)`) — parsed but `secret.` prefix resolution not implemented in codegen.
- `@>` generate directives — parsed into AST but not resolved (M6 feature).

### Codegen (M3 gaps)
- **Workers** — `genWorker()` is implemented but has no test coverage in `generator_test.go` and is not exercised by `all_features.bp`.
- **`subscribe` blocks** — not implemented.
- **`call external`** in endpoints — the `external` block generates `src/lib/external.ts` but `call service GET /path` inside endpoints is not fully hardened.
- **Test codegen** — basic vitest files generated but fixture system (`seed api_key { ... }`) emits raw strings, not functional code.

### CLI (M4–M7)
- `bp check` — ✅ implemented (`--json` flag for CI output)
- `bp build` — ✅ implemented
- `bp diff` — ✅ implemented (preview changes before building)
- `bp run` — ✅ implemented (build + bun install + bun run start)
- `bp dev` — ✅ implemented (polling watcher + subprocess restart)
- `bp test` — ✅ implemented (build + bun install if needed + bun run test)
- `bp migrate` — ✅ implemented (build + bunx drizzle-kit generate|push|studio|check)
- `bp deploy` — ✅ implemented (Docker build + optional Fly.io deploy)
- `bp generate` — ✅ implemented (`internal/generate/generate.go`, needs `ANTHROPIC_API_KEY`)
- `bp init` — ✅ implemented (`cmd/bp/main.go`)
- `bp fmt` — ✅ implemented (`internal/ast/printer.go`)
- `bp lint` — ✅ implemented (`internal/linter/linter.go`)
- `bp docs` — ✅ implemented (`internal/docs/openapi.go`, generates OpenAPI 3.1 JSON)
- `bp stats` — ✅ implemented (code statistics: models, endpoints, functions, etc.)
- `bp doctor` — ✅ implemented (environment dependency checks)
- `bp completion` — ✅ implemented (bash/zsh/fish shell completion)
- `bp lsp` — ✅ implemented (`internal/lsp/server.go`, basic LSP server)
- `bp eject` — ✅ implemented (removes Blueprint markers from generated code)
- `@blueprint/runtime` npm package — ✅ `packages/runtime/` (BpError, paginate, requireEnv, type utilities)
- GoReleaser — ✅ `.goreleaser.yaml` (Linux/macOS/Windows, amd64/arm64, Homebrew tap)
- Release CI — ✅ `.github/workflows/release.yml` (triggers on v* tags)

---

## Adding a New CLI Command

1. Add a new `case` in `cmd/bp/main.go`'s `main()` switch
2. Implement `cmdFoo(args) int` in the same file or a new file in `cmd/bp/`
3. Return 0 for success, 1 for validation error, 2 for file error, 4 for codegen error (see SPEC §24.2)

## Adding a New Codegen Feature

1. Make sure the feature is parsed into the AST (`internal/ast/nodes.go`, `internal/parser/parser.go`)
2. Add semantic checks if needed (`internal/checker/checker.go`)
3. Add the generation in `internal/codegen/js/generator.go`
4. Add a test fixture in `testdata/valid/` and a codegen test in `generator_test.go`

## Adding a New Codegen Test

Tests in `generator_test.go` follow this pattern:
```go
func TestGen_MyFeature(t *testing.T) {
    src := `blueprint "x" { version "1.0" port 8080 runtime node }
    // ... blueprint source
    `
    files := buildAndGen(t, src)
    content := findFile(files, "src/routes/my-resource.ts")
    require.Contains(t, content, "expected snippet")
}
```

---

## Conventions

- **Go**: stdlib only, no external dependencies (`go.mod` has none)
- **Codegen**: uses `strings.Builder` directly — no Go templates
- **Test fixtures**: valid fixtures in `testdata/valid/`, invalid in `testdata/invalid/`
- **Error format**: `filename:line:col\n\n  source_line\n  ^^^^^pointer\n\n  message\n  hint`
- **Generated TS naming**: `snake_case` bp names → `camelCase` TS, models are pluralized for table names, `PascalCase` for types

## The Reference Example

`testdata/valid/all_features.bp` is the canonical test for the codegen. It exercises:
- Rich enums (`Plan`)
- Middleware with `inject`
- Functions with `impl node` (native) and `logic` (inline)
- Pipes
- Multiple endpoints (POST/GET with filters, pagination, struct enum access)
- Try/recover
- Schedules
- Tests + fixtures

Running `bp build testdata/valid/all_features.bp` and then `tsc --noEmit` in the output is the end-to-end correctness check.
