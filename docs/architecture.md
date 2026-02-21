# Architecture

Toolchain internals for contributors.

## Overview

Blueprint is a Go program. Given a `.bp` source file, it runs five passes in sequence:

```
source text
    ↓ Lexer        tokens
    ↓ Parser       AST
    ↓ Checker      validated AST
    ↓ Codegen      []OutputFile
    ↓ Writer       files on disk
```

Each pass is a separate package under `internal/`. They communicate through shared types in `internal/ast/`.

---

## Package Map

```
cmd/bp/             CLI entry point
internal/
  lexer/            Tokenizer
  parser/           Recursive-descent parser
  ast/              AST node types (no logic)
  checker/          Semantic validation
  codegen/
    js/             JavaScript/TypeScript code generator
  docs/             OpenAPI generator
```

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

1. Add the constant to the `TokenKind` iota block in `tokens.go`
2. If it's a keyword, add it to the `keywords` map in `lexer.go`
3. Add display name to `tokenKindString()` for error messages

---

## `internal/ast`

Pure data. No methods, no logic, just node types. This package is imported by parser, checker, and codegen — it must not import any of them.

### Key interfaces

```go
type Node interface {
    node()
    GetLoc() Loc
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
| `*ast.ObjectLit` | `{ key: value, ... }` |
| `*ast.StringLit`, `*ast.IntLit`, `*ast.FloatLit`, `*ast.BoolLit` | Literals |
| `*ast.EndpointMeta` | `use`, `auth`, `limit`, `cache`, `tags`, `timeout` |

### `ast.File`

```go
type File struct {
    Loc    Loc
    Blocks []TopLevel  // all top-level declarations in order
}
```

`Blocks` is a heterogeneous slice. Codegen iterates it with type switches.

---

## `internal/parser`

A recursive-descent parser with Pratt expression parsing. Converts `[]Token` into `*ast.File`.

### Entry point

```go
p := parser.New(tokens)
file, errs := p.ParseFile()
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

- Undefined names (references to models, middleware, functions that don't exist)
- Naming conventions (identifiers must be `snake_case`, blueprint names `kebab-case`)
- Structural rules (blueprint block must appear before endpoints, required fields present)
- Type mismatches where statically detectable

### Entry point

```go
c := checker.New()
errs := c.Check(file)
```

### How it works

The checker maintains a scope map populated in a first pass over top-level declarations. A second pass checks references. This two-pass approach means forward references work — a middleware defined after the endpoint that uses it is still valid.

### Adding a new check

1. Add a method `checkXxx` on `*Checker`
2. Call it from the appropriate parent visitor
3. Add test cases in `checker_test.go`

---

## `internal/codegen/js`

The JavaScript/TypeScript code generator. Converts a checked `*ast.File` into `[]codegen.OutputFile`.

### Entry point

```go
g := js.NewGenerator()
files, err := g.Generate(file)
```

`OutputFile` is:

```go
type OutputFile struct {
    Path    string
    Content string
}
```

The caller (CLI) writes these to disk.

### Key functions

| Function | Generates |
|----------|-----------|
| `genPackageJSON` | `package.json` |
| `genTSConfig` | `tsconfig.json` |
| `genIndex` | `src/index.ts` |
| `genSchema` | `src/models/schema.ts` |
| `genValidation` | `src/validation/schemas.ts` |
| `genTypes` | `src/types.ts` |
| `genLib` | All `src/lib/*.ts` files |
| `genRoute` | One `src/routes/<resource>.ts` |
| `genMiddleware` | One `src/middleware/<name>.ts` |
| `genWorker` | One `src/workers/<name>.ts` |
| `genSchedule` | One `src/schedules/<name>.ts` |
| `genPipe` | One `src/pipes/<name>.ts` |
| `genFn` | One `src/functions/<name>.ts` |
| `genTest` | One `test/<name>.test.ts` |
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

## `cmd/bp`

The CLI entry point. Parses `os.Args` and dispatches to the appropriate pipeline.

### Command pipeline

| Command | Pipeline |
|---------|----------|
| `bp check` | lex → parse → check |
| `bp build` | lex → parse → check → codegen → write files |
| `bp run` | build + `npm install && npm start` |
| `bp dev` | build + watch loop |
| `bp test` | build + `npx vitest run` |
| `bp migrate` | build + `drizzle-kit <subcommand>` |
| `bp generate` | parse + find `@>` slots + call Anthropic API |
| `bp docs` | lex → parse → check → OpenAPI generation |
| `bp fmt` | lex → parse → pretty-print AST |
| `bp lint` | lex → parse → check → lint rules |
| `bp init` | scaffold new project |
| `bp version` | print version string |

---

## Testing

Tests live next to the code they test.

| Package | Test file | Count |
|---------|-----------|-------|
| `internal/lexer` | `lexer_test.go` | 50+ |
| `internal/parser` | `parser_test.go` + `testdata/` | 61+ |
| `internal/checker` | `checker_test.go` + `testdata/` | 57+ |
| `internal/codegen/js` | `generator_test.go` | 30+ |
| `internal/docs` | `docs_test.go` | 10+ |

### Running tests

```bash
go test ./...
```

Or with the Taskfile:

```bash
task test
```

### Parser fixture tests

`internal/parser/testdata/` contains `.bp` fixture files. Tests in `parser_test.go` call `parseFixture("name.bp")` and assert on the resulting AST. This is the preferred way to test complex parse scenarios.

### Checker fixture tests

`internal/checker/testdata/invalid/` contains `.bp` files expected to produce specific errors. Tests assert that the error message contains the expected substring.

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
    g := js.NewGenerator()
    files, err := g.Generate(file)
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

The current `internal/codegen/js` package is one concrete implementation of:

```go
type Generator interface {
    Generate(file *ast.File) ([]OutputFile, error)
}
```

To add a new target (e.g., Python/FastAPI):

1. Create `internal/codegen/python/` with a `Generator` struct
2. Implement the `Generate` method
3. Add a `--target` flag to `bp build` in `cmd/bp/main.go`
4. Dispatch to the correct generator based on the flag

---

## Design Decisions

### No external Go dependencies

The toolchain uses only the Go standard library. This keeps the binary small and the build trivial (`go build ./...`).

### `strings.Builder` instead of templates

The codegen uses `strings.Builder` and direct string writes instead of `text/template`. This gives precise control over whitespace and eliminates template parse errors at startup.

### Data operations as function calls in AST

`query`, `save`, `update`, `delete` are not typed as special AST nodes — they're represented as `*ast.FnCall` with a known name. The codegen recognizes them by name and emits the appropriate Drizzle ORM code. This keeps the AST simpler at the cost of some codegen complexity.

### No runtime package

Generated code has zero Blueprint dependencies. Everything uses `hono`, `drizzle-orm`, `zod`, `bullmq`, and `node-cron` — standard libraries the user already knows and can maintain independently.
