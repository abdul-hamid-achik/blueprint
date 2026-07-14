# Architecture

Toolchain internals for contributors.

## Overview

Blueprint is a Go program. Given a `.bp` source file, it runs these passes in sequence:

```
source text
    ↓ Lexer        tokens
    ↓ Parser       AST
    ↓ Checker      validated AST
    ↓ resolve      semantic facts (typed-IR)
    ↓ Codegen      []OutputFile
    ↓ Writer       files on disk
```

Each pass is a separate package under `internal/`. They communicate through shared types in `internal/ast/`. The `resolve` pass produces semantic facts (the target model and cardinality of each bound variable, and similar ground truth) that code generators consume instead of re-deriving heuristically at emit time — see [`internal/resolve`](#internal-resolve).

---

## Package Map

```
cmd/bp/             CLI entry point (check, build, frontend, diff, run, dev, test,
                    migrate, generate, import, docs, fmt, lint, init, eject, deploy,
                    completion, stats, explain, context, llms, doctor, lsp, version)
internal/
  lexer/            Tokenizer (~95 token kinds)
  parser/           Recursive-descent parser with Pratt expressions
  ast/              AST node types + printer (bp fmt)
  checker/          Semantic validation (scopes, naming, refs)
  resolve/          Semantic facts (typed-IR): per-binding model + cardinality, FK access, async-ness
  diag/             Shared diagnostic surface (source line + caret, error codes L/P/C/R/G###)
  codegen/
    common/         Path + string helpers shared across targets
    js/             JavaScript/TypeScript target (Hono + Drizzle + Zod) — the default
    python/         Python target (FastAPI + SQLAlchemy + Alembic), via --target python
    effect/         Effect (TypeScript) target — runnable health/config scaffold, via --target effect
  linter/           Style linter (intent annotations, block ordering, empty endpoints)
  docs/             OpenAPI 3.1 JSON generator
  generate/         LLM code generation (@> slots via Anthropic API)
  importer/         Conservative static TypeScript-to-.bp scaffolding
  lsp/              LSP diagnostics, hover, definition, completion, workspace symbols
  agentctx/         Agent-facing context surface (bp context / bp llms)
  registry/         Package registry groundwork (no add/search/publish CLI yet)
packages/
  runtime/          npm package for Blueprint runtime helpers
editors/
  vscode-blueprint/ Packaged VS Code stdio client + TextMate grammar
```

`internal/codegen/codegen.go` defines the `Generator` interface and `OutputFile` type shared by all targets; `writer.go` defines `WriteOutputFiles`. See [Multi-Target Code Generation](./multi-target-codegen.md) for the authoritative target contract.

---

## `internal/lexer`

A hand-written tokenizer. Converts a source string into a flat `[]Token` slice.

### Key types

```go
type Token struct {
    Kind    TokenKind
    Value   string    // literal text
    Line    int
    Col     int
}

type TokenKind int
const (
    TokenEOF TokenKind = iota
    TokenIdent
    TokenString
    TokenInt
    TokenFloat
    // ... ~95 kinds total
)
```

### How it works

`Lexer.Next()` advances one token. Keywords are matched after scanning an identifier — `lexer.go` has a `keywords` map from string to `TokenKind`. This is why `using`, `with`, `where`, `on`, etc. are reserved.

MIME types (`image/png`, `application/json`) are assembled in the lexer: when a `/` follows a known media type prefix, the lexer scans the subtype and emits a single `TokenMimeType`.

### Adding a token kind

1. Add the constant to the `TokenKind` iota block in `token.go`
2. If it's a keyword, add it to the `keywords` map in `lexer.go`
3. Add display name to `tokenKindString()` for error messages

---

## `internal/ast`

Pure data. No methods, no logic, just node types. This package is imported by parser, checker, and codegen — it must not import any of them.

### Key interfaces

```go
type Node interface {
    nodeType() string
    Location() lexer.Loc
}

type TopLevel interface {
    Node
    topLevel()
}

type ArrowStmt interface {
    Node
    arrowStmt()
}

type Expr interface {
    Node
    expr()
}

type TypeExpr interface {
    Node
    typeExpr()
}
```

### Key nodes

| Node | Represents |
|------|-----------|
| `*ast.File` | Entire source file — root of the tree |
| `*ast.Blueprint` | `blueprint "name" { ... }` block |
| `*ast.Endpoint` | HTTP endpoint (`GET /path { ... }`) |
| `*ast.StreamEndpoint` | `STREAM /path { ... }` |
| `*ast.WsEndpoint` | `WS /path { ... }` |
| `*ast.Model` | `model foo { ... }` |
| `*ast.Middleware` | `middleware name { ... }` |
| `*ast.Worker` | `worker name { ... }` |
| `*ast.Schedule` | `schedule name { ... }` |
| `*ast.Pipe` | `pipe name { ... }` |
| `*ast.Fn` | `fn name { ... }` |
| `*ast.TestBlock` | `test name { ... }` |
| `*ast.InputStmt` | `<- name type constraints...` |
| `*ast.StepStmt` | `\|> var = expr` |
| `*ast.OutputStmt` | `-> status { ... }` |
| `*ast.Ident` | Bare name: `foo` |
| `*ast.FieldAccess` | `foo.bar` |
| `*ast.FnCall` | `foo(args...)` |
| `*ast.BlockExpr` | `{ key: value, ... }` |
| `*ast.StringLit`, `*ast.IntLit`, `*ast.FloatLit`, `*ast.BoolLit` | Literals |
| `*ast.EndpointMeta` | `use`, `auth`, `limit`, `cache`, `tags`, `timeout` |

### `ast.File`

```go
type File struct {
    Loc       lexer.Loc
    Blueprint *Blueprint
    Blocks    []TopLevel  // non-blueprint declarations in source order
}
```

`Blocks` is a heterogeneous slice. Codegen iterates it with type switches.

---

## `internal/parser`

A recursive-descent parser with Pratt expression parsing. Converts `[]Token` into `*ast.File`.

### Entry point

```go
file, errs := parser.ParseFile(filename, src)
```

Errors are collected and returned; the parser attempts panic-mode recovery so multiple errors can be reported in one pass.

### Structure

| Function | Parses |
|----------|--------|
| `ParseFile` | Top-level loop, dispatches on first token |
| `parseBlueprint` | `blueprint "name" { ... }` |
| `parseEndpoint` | `GET/POST/... /path { ... }` |
| `parseModel` | `model name { ... }` |
| `parseMiddleware` | `middleware name { ... }` |
| `parseWorker` | `worker name { ... }` |
| `parseSchedule` | `schedule name { ... }` |
| `parsePipe` | `pipe name { ... }` |
| `parseFn` | `fn name { ... }` |
| `parseTest` | `test name { ... }` |
| `parseArrowStmt` | `<-`, `\|>`, `->` statements |
| `parseExpr` | Pratt expression parser |
| `parseEndpointMeta` | `use`, `auth`, `limit`, etc. |
| `parseTypeExpr` | Type annotations |

### Expression parsing

`parseExpr` uses Pratt parsing with a simple precedence table. Function calls are handled by checking for `(` after an identifier. Field access is handled by checking for `.`. The parser does NOT attempt type inference — that is left to codegen.

### Adding a new top-level construct

1. Add AST nodes to `internal/ast/`
2. Add a `parseXxx` function in `parser.go`
3. Add a dispatch case in `ParseFile`'s top-level switch
4. Add the construct's opening token to the first-token check

---

## `internal/checker`

The semantic pass. Walks the `*ast.File` and reports errors for:

- Undefined top-level references (models, middleware, functions, and external services)
- Naming conventions (identifiers must be `snake_case`, blueprint names `kebab-case`)
- Structural rules (blueprint block must appear before endpoints, required fields present,
  settings are unique, setting values have the required scalar shape, and ports are in range)
- Selected structural/type errors where statically detectable

### Entry point

```go
errs := checker.Check(file)
```

The checker now covers local binding/input duplication, unbound and
use-before-bind names, callable arity, builtin collisions, and direct fields on
known model values. It does not yet provide full nested JSON/FK leaf typing or
general assignability; closing that remaining gap is a current
[roadmap priority](/roadmap#strengthen-semantic-checking).

### How it works

The checker maintains a scope map populated in a first pass over top-level declarations. A second pass checks references. This two-pass approach means forward references work — a middleware defined after the endpoint that uses it is still valid.

### Adding a new check

1. Add a method `checkXxx` on `*Checker`
2. Call it from the appropriate parent visitor
3. Add test cases in `checker_test.go`

---

## `internal/resolve`

The first slice of the resolver / typed-IR work. It produces **semantic facts** about a checked `*ast.File` that code generators consume as ground truth instead of re-deriving heuristically at emit time.

Today it records, for each `ArrowStmt` that binds a variable from a data operation (or `map`), the target model and the result's **cardinality**:

- `SingleCard` — one record, produced by `fetch` or `query ... first`
- `CollectionCard` — an unordered list, produced by `query` without `paginate()` and by `map(...)`
- `PaginatedCard` — the `{ items, total, page, per_page }` shape produced by `query ... paginate(page, per_page)`

Each data-operation fact also carries ref-backed relationship names requested
by `query ... with(...)`. Generators consume that target-neutral list while
choosing target-specific join syntax; unsupported targets reject it.

Subsequent slices move FK access, async-ness, and `let`/`const` decisions out of the codegen heuristics into this package.

Design constraint: `resolve` must **not** import `internal/codegen` — it has to be importable by every code generator (JS, Python, Effect, and future targets).

---

## `internal/codegen` — the target contract

`internal/codegen/codegen.go` defines the interface every target implements:

```go
type Generator interface {
    // Files returns the generated output files for a parsed+checked AST.
    // It must not write to disk.
    Files(file *ast.File) ([]OutputFile, error)
    // Generate builds Files and persists them to outDir via WriteOutputFiles.
    Generate(file *ast.File, outDir string) error
}

type OutputFile struct {
    Path      string // relative path within the output directory
    Content   []byte
    UserOwned bool // scaffold if missing, then leave untouched on later builds
}
```

`Files` is the target-agnostic contract: turn an AST into in-memory `[]OutputFile` without touching disk (so emit logic stays pure and unit-testable). Persistence — manifest tracking, `UserOwned` scaffolding, and stale-file cleanup — is the shared responsibility of `codegen.WriteOutputFiles`, so every target gets identical on-disk behavior. `Generate` is the convenience that wires the two together.

See **[Multi-Target Code Generation](./multi-target-codegen.md)** for the authoritative, full description of the target contract.

---

## `internal/codegen/js`

The JavaScript/TypeScript target (Hono + Drizzle + Zod) — the default. Converts a checked `*ast.File` into `[]codegen.OutputFile`.

### Entry point

```go
g := js.New()
files, err := g.Files(file)     // in-memory
// or
err := g.Generate(file, outDir) // build + persist via WriteOutputFiles
```

### Key functions

| Function | Generates |
|----------|-----------|
| `genPackageJSON` | `package.json` |
| `genTSConfig` | `tsconfig.json` |
| `genIndex` | `src/index.ts` |
| `genSchema` | `src/models/schema.ts` |
| `genValidation` | `src/validation/schemas.ts` |
| `genTypes` | `src/types.ts` |
| `genDB`, `genErrors`, `genCache`, `genEnv`, ... | The `src/lib/*.ts` files (one generator per lib file: `db.ts`, `errors.ts`, `cache.ts`, `env.ts`, `storage.ts`, ...) |
| `genRoute` | One `src/routes/<resource>.ts` |
| `genMiddleware` | One `src/middleware/<name>.ts` |
| `genWorker` | One `src/workers/<name>.ts` |
| `genSchedule` | One `src/schedules/<name>.ts` |
| `genPipe` | One `src/pipes/<name>.ts` |
| `genFunction` | One `src/functions/<name>.ts` |
| `genTest` | One `test/<name>.test.ts` |
| `genPropertyTestFile` | One deterministic `test/generated/<resource>.property.test.ts` when property mode is enabled |
| `genEnvExample` | `.env.example` |
| `genDockerfile` | `Dockerfile` |

### String helpers

All string conversion helpers live in `helpers.go`:

| Helper | Example |
|--------|---------|
| `toCamelCase("my_field")` | `"myField"` |
| `toPascalCase("my_model")` | `"MyModel"` |
| `toKebabCase("my_service")` | `"my-service"` |
| `pluralize("todo")` | `"todos"` |

### Type mapping helpers

| Helper | Purpose |
|--------|---------|
| `typeToTS(t)` | BP type → TypeScript type |
| `typeToZod(t)` | BP type → Zod schema method |
| `typeToDrizzle(t)` | BP type → Drizzle column type |
| `constraintsToZod(c)` | Constraint list → Zod chain |

### Expression serialization

`exprToJS(expr ast.Expr) string` converts any AST expression to a JavaScript string. It handles:

- Identifiers: `foo` → `"foo"`
- Field access: `foo.bar` → `"foo.bar"` (with `snake_case` → `camelCase` conversion for object fields)
- Function calls: `hash(x)` → `"hash(x)"`
- Literals: strings, ints, floats, booleans
- Object literals: `{ a: b }` → `"{ a: b }"`
- String interpolation: `"Hello, {name}!"` → `` `Hello, ${name}!` ``

### Adding a new codegen feature

1. Identify which AST node carries the new information
2. Find the relevant `genXxx` function
3. Add a case to the appropriate type switch or conditional
4. Add a test in `generator_test.go` with a minimal `.bp` source that exercises the feature
5. Check that `exprToJS` handles any new expression forms

### Route grouping

Endpoints are grouped by the first path segment to determine the route file name:
- `GET /api/todos` → `src/routes/todos.ts`
- `POST /api/users/:id` → `src/routes/users.ts`
- `POST /webhooks/stripe` → `src/routes/webhooks.ts`

Multiple endpoints in the same group are emitted into the same file.

Node query emission can materialize one-level ref-backed relationships and
computed fields. `with(author)` resolves through `author_id ref(target)` and
renders a `LEFT JOIN`; the checker rejects aliases/self joins/repeated targets
that the emitter cannot represent. Computed fields are not Drizzle columns:
`genSchema` emits pure materializers, and response/frontend/OpenAPI paths expose
the resulting read-only values. Python and Effect reject both AST slices.

---

## `internal/docs`

Generates an OpenAPI 3.1 JSON document from a checked `*ast.File`.

### Entry point

```go
spec, err := docs.Generate(file)
// spec is map[string]any, marshal to JSON
```

### Key functions

| Function | Generates |
|----------|-----------|
| `buildInfo` | `info` object (title, version) |
| `buildPaths` | `paths` — iterates all endpoints, STREAM, WS |
| `buildComponents` | `components/schemas` from models |
| `buildOperation` | One HTTP endpoint's operation object |
| `buildStreamOperation` | STREAM endpoint (GET + text/event-stream) |
| `buildWsOperation` | WS endpoint (GET + 101 + `x-websocket:true`) |

### STREAM endpoints

STREAM endpoints compile to a GET path with a `text/event-stream` response:

```json
{
  "get": {
    "operationId": "stream_job_progress",
    "responses": {
      "200": {
        "content": { "text/event-stream": { "schema": { "type": "string" } } }
      }
    }
  }
}
```

### WS endpoints

WS endpoints compile to a GET path with `101 Switching Protocols` and an extension flag:

```json
{
  "get": {
    "operationId": "connect_chat",
    "x-websocket": true,
    "responses": {
      "101": { "description": "Switching Protocols" }
    }
  }
}
```

---

## `internal/linter`

A style linter that checks for best-practice violations beyond what the semantic checker enforces.

### Entry point

```go
l := linter.New()
issues := l.Lint(file)
```

### Rules

| Rule | Level | Description |
|------|-------|-------------|
| `block-ordering` | warning | Top-level blocks should follow canonical order: blueprint, secret/env, model, type/alias/enum, fn/pipe/middleware, endpoints, worker/schedule, external/subscribe, test/fixture |
| `intent-on-endpoints` | warning | Every endpoint (REST, STREAM, WS) should have an `@` intent annotation |
| `empty-endpoint` | warning | Endpoints with no inputs or statements |

### Adding a lint rule

1. Add a method `checkXxxRule` on `*Linter`
2. Define the rule name and default severity
3. Add test cases in `linter_test.go`

---

## `internal/generate`

Resolves `@>` generation slots by calling the Anthropic API (Claude).

### Entry points

```go
slots := generate.FindSlots(file)
replacements, err := generate.GenerateAll(slots, apiKey)
updatedSource := generate.Apply(src, replacements)
```

### How it works

1. Walks arrow-statement bodies for quoted `@>` (`GenerateStep`) nodes
2. Builds a prompt from the enclosing block context + the `@>` text and hints
3. Calls the Anthropic Messages API with Claude
4. Parses returned Blueprint arrow statements and replaces the source line in memory

### Configuration

- Requires `ANTHROPIC_API_KEY` environment variable
- Uses Claude as the default model
- Without `--write`, the CLI prints the updated `.bp` source; with `--write`,
  it rewrites that source file

---

## `internal/importer`

`ImportTypeScript` is a structural rewrite scaffolder, not a transpiler. It
sorts supplied sources, extracts conservative static forms of Drizzle
`pgTable`/`pgEnum`, Hono route calls/base paths, and named Zod object inputs from
static, transport-compatible `zValidator` calls, then prints and
re-parses/re-checks the resulting AST. Nullable/nullish or type-changing Zod
fields are skipped. Static SQL column names are preserved when representable;
SQL identity changes and dropped Drizzle builder/table/reference options are
recorded as warnings.

Every recovered route deliberately drops the imperative handler body, emits a
`TODO(import)` plus 501 output, and records mapped/dropped facts in a fidelity
report. Dynamic routes, unsupported methods/types, route mounts, incomplete
refs, duplicates, and renamed identifiers are skipped or weakened only with an
explicit warning. `cmd/bp/import.go` owns directory scanning, safety limits,
atomic output, and overwrite refusal.

---

## `internal/lsp`

`bp lsp` is a dependency-free stdio JSON-RPC server. It keeps full-text open
documents and parser/checker indexes for diagnostics, hover, definition, and
context-aware completion. Completion combines the recovery AST with lightweight
source context so it can suggest declarations, types, constraints, settings,
model/ref/relationship names, local bindings, fields, and arrow steps while a
line is incomplete. It does not implement completion resolve.

`workspace/symbol` merges unsaved open documents with `.bp` files scanned from
local workspace folders. Open text wins over disk; common VCS, dependency,
generated, build, and virtual-environment directories are ignored. Remote
workspace roots, rename, references, code actions, and incremental document
sync/indexing are not implemented.

`editors/vscode-blueprint` packages the TextMate grammar with
`vscode-languageclient`. It starts `bp lsp`, watches `.bp` files, exposes
`blueprint.server.path`/`blueprint.server.args`, and provides a restart command.

---

## `internal/ast/printer.go`

Pretty-printer for `bp fmt`. Converts an AST back to formatted `.bp` source text.

### Entry point

```go
formatted := ast.Print(file)
```

### Formatting rules

- 2-space indentation
- Aligned field declarations within blocks
- Normalized whitespace around arrows (`<-`, `|>`, `->`)
- Consistent double-quote style
- Source-aware preservation of leading and inline `#` comments
- Trailing newline

---

## `cmd/bp`

The CLI entry point. Parses `os.Args` and dispatches to the appropriate pipeline.

### Command pipeline

| Command | Pipeline |
|---------|----------|
| `bp check` | lex → parse → check |
| `bp build` | lex → parse → check → resolve → codegen → write files |
| `bp frontend` | build the standalone frontend SDK package |
| `bp diff` | build, but show changes without overwriting |
| `bp run` | build + `bun install && bun run start` |
| `bp dev` | build + watch loop |
| `bp test` | build + run Vitest (Node) or pytest (Python); optional Node property mode |
| `bp migrate` | build + `drizzle-kit <subcommand>` (node) or `alembic` (`--target python`) |
| `bp generate` | parse + find `@>` slots + call Anthropic API |
| `bp import` | scan TypeScript → recover static structure → validate + print TODO/501 `.bp` scaffold |
| `bp docs` | lex → parse → check → OpenAPI generation |
| `bp fmt` | lex → parse → pretty-print AST |
| `bp lint` | lex → parse → check → lint rules |
| `bp init` | scaffold new project |
| `bp eject` | strip Blueprint markers from generated code |
| `bp deploy` | build + smoke-run Docker image (`--target docker`; `fly` not yet implemented) |
| `bp stats` | parse + report code statistics |
| `bp explain` | print docs for a structured error code (`Cxxx`/`Lxxx`/`Pxxx`) |
| `bp context` | print the agent-facing language + CLI surface |
| `bp llms` | print the complete agent/LLM guide |
| `bp doctor` | check environment dependencies |
| `bp lsp` | start the Language Server Protocol server |
| `bp completion` | generate a shell completion script |
| `bp version` | print version string |

The codegen `--target` (`node` default, `python`, `effect`) is accepted by
`build` and `diff`; `test` and `migrate` accept `node`/`python` only. `bp deploy`
has a same-named but unrelated `--target` flag — a deploy target (`docker`;
`fly` not yet implemented) — and always builds the Node codegen target
internally. `--gen-property-tests` is accepted by `build`, `diff`, and `test`
for Node only and implies generated contract tests.

---

## Testing

Tests live next to the Go packages they cover. Shared valid/invalid language
fixtures live under the repository-level `testdata/`; generator packages also
carry focused source strings and golden output.

### Running tests

```bash
go test ./...
```

Or with the Taskfile:

```bash
task test
```

### Parser and checker fixtures

`testdata/valid/` contains sources expected to parse and check. `testdata/invalid/`
contains focused failures. Parser/checker tests consume these shared fixtures so
one source can pin behavior across the frontend.

### Codegen tests

Codegen tests follow this pattern:

```go
func TestGen_MyFeature(t *testing.T) {
    src := `
        blueprint "test" { version "1.0.0" port 3000 runtime node }
        POST /api/things {
            <- name string required
            |> thing = save thing { name: name }
            -> 201 { id: thing.id }
        }
    `
    file := mustParse(t, src)
    g := js.New()
    files, err := g.Files(file)
    require.NoError(t, err)
    out := findFile(files, "src/routes/things.ts")
    require.Contains(t, out, "expected substring")
}
```

---

## Extending Blueprint

### Adding a new top-level construct

1. **Lexer**: add any new keywords to `keywords` map
2. **AST**: add node type implementing `TopLevel` interface
3. **Parser**: add `parseXxx` method + dispatch case in `ParseFile`
4. **Checker**: add `checkXxx` method + call it
5. **Codegen**: add `genXxx` method + call from `Generate`
6. **Tests**: add fixture test (parser), checker test, codegen test

### Adding a new statement kind

1. **AST**: add node type implementing `ArrowStmt` interface
2. **Parser**: add case in `parseArrowStmt`
3. **Checker**: add case in `checkArrowStmt`
4. **Codegen**: add case in `genStmt`
5. **Tests**: add tests at each layer

### Adding a new type

1. **Lexer**: if the type is a new keyword, add it
2. **AST**: add a case to `*ast.SimpleType` or create a new type node
3. **Codegen helpers**: add mappings in `typeToTS`, `typeToZod`, `typeToDrizzle`
4. **Tests**: add to type mapping tests in `generator_test.go`

### Adding a new code generation target

`internal/codegen/js` is the default target. Two more already exist: `internal/codegen/python` (FastAPI + SQLAlchemy + Alembic, via `--target python`) and `internal/codegen/effect` (a runnable, fail-closed Effect health/config scaffold, via `--target effect`). Each is a concrete implementation of the `codegen.Generator` interface (see [`internal/codegen`](#internal-codegen-the-target-contract)):

```go
type Generator interface {
    Files(file *ast.File) ([]OutputFile, error)
    Generate(file *ast.File, outDir string) error
}
```

To add a target (e.g., Go or Ruby):

1. Create `internal/codegen/<target>/` with a `Generator` struct and a `New()` constructor
2. Implement `Files` (pure, in-memory) and `Generate` (which calls `WriteOutputFiles`) — reuse helpers from `internal/codegen/common`
3. Register the new value in the `--target` parsing in `cmd/bp/main.go` and dispatch to the constructor
4. Consume `internal/resolve` facts rather than re-deriving them per target

See **[Multi-Target Code Generation](./multi-target-codegen.md)** for the authoritative contract and the patterns the existing targets follow.

---

## Design Decisions

### No external Go dependencies

The toolchain uses only the Go standard library. This keeps the binary small and the build trivial (`go build ./...`).

### `strings.Builder` instead of templates

The codegen uses `strings.Builder` and direct string writes instead of `text/template`. This gives precise control over whitespace and eliminates template parse errors at startup.

### Data operations as function calls in AST

`query`, `save`, `update`, `delete` are not typed as special AST nodes — they're represented as `*ast.FnCall` with a known name. The codegen recognizes them by name and emits the appropriate Drizzle ORM code. This keeps the AST simpler at the cost of some codegen complexity.

### No required runtime package

Generated code has zero Blueprint dependencies. The Node target uses familiar
packages such as `hono`, `drizzle-orm`, `zod`, `bullmq`, and `redis`; the Python
target uses FastAPI, SQLAlchemy, Pydantic, and Alembic. The optional
`@blueprint/runtime` package exists for projects that want shared helpers, but
generated services do not depend on it.
