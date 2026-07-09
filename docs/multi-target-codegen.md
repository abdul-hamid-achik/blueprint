# Multi-Target Code Generation — the generator contract

This is the contract every Blueprint code-generation target must satisfy. It is
the authoritative reference for adding a new target. A TypeScript-on-**Effect**
backend already exists as an early scaffold; the next fresh targets being
explored are **Go** and **Ruby**. It describes what the
toolchain already does for you, what your generator must provide, and the shared
infrastructure (the resolve IR, the file writer, the common helpers) you should
lean on instead of re-deriving.

> Status note: the JS/Node target is complete; the Python target is far along
> (see the "Python target" section of [BACKLOG.md](https://github.com/abdul-hamid-achik/blueprint/blob/main/BACKLOG.md)). Keep this
> doc in sync with `internal/codegen/codegen.go` — the code is the source of
> truth; this prose explains it.

## Targets

| Target | Selector | Status | Stack |
|--------|----------|--------|-------|
| JavaScript / Node | `--target node` (default) | ✅ Complete | Hono + Drizzle + Zod |
| Python | `--target python` | 🚧 Advanced (most constructs; some long-tail features rejected with a specific message) | FastAPI + SQLAlchemy 2.0 + Pydantic v2 + Alembic |
| Go | `--target go` | 🗺️ Planned | Chi + sqlc + validator |
| Ruby | `--target ruby` | 🗺️ Planned | TBD (Rails API / Roda) |
| TypeScript on Effect | `--target effect` | 🧱 Scaffold (emits the project shell + a `Config` secrets module; endpoint/model emit is the long tail) | `@effect/platform` HttpApi + `@effect/schema` + `@effect/sql` |

Target selection is the **`--target` flag**, but not every command accepts the
same set of values — see [Per-command target dispatch](#per-command-target-dispatch)
below. In short: `bp build` and `bp diff` accept `node` (default), `python`, or
`effect`; `bp migrate` accepts `node` (default) or `python` only; `bp test` has
no `--target` at all — it always runs the node Vitest suite; and `bp deploy`'s
`--target` flag is a *different* concept entirely — a deploy target (`docker`,
with `fly` not yet implemented) — `bp deploy` always builds the node codegen
target internally. `--target` is also distinct from the `runtime` entry inside
the `blueprint` block, which is a *node-target* concern (e.g. `node` vs other
JS runtimes).

## Per-command target dispatch

Verified against `resolveTarget` and each command's dispatch switch in
`cmd/bp/main.go`:

| Command | `node` | `python` | `effect` | Notes |
|---------|--------|----------|----------|-------|
| `bp build` | ✅ default | ✅ | ✅ (scaffold) | `--react-query`/`--frontend-only` are node-only |
| `bp diff` | ✅ default | ✅ | ✅ (scaffold) | must match the target the `--out` dir was built with |
| `bp migrate` | ✅ default (`drizzle-kit`) | ✅ (`alembic` via `uv`) | ⚠️ accepted by flag parsing but **not dispatched** — silently falls through to the drizzle-kit path and fails with a confusing `drizzle.config.json` error. Don't pass `--target effect` here; this is a known gap, not a supported combination | |
| `bp deploy` | always builds node internally | n/a | n/a | `--target` here means something else entirely — see below |
| `bp test` | always (hardcoded) | n/a | n/a | no `--target` flag exists on this command |
| `bp check` / `bp lint` / `bp fmt` / `bp docs` | target-agnostic | target-agnostic | target-agnostic | these operate on the parsed/checked AST only; no codegen involved |

**`bp deploy --target` is not a codegen target.** It selects a *deploy* target
(`docker` today; `fly` is parsed but exits with "not yet implemented").
`bp deploy` always builds the node codegen target internally before handing
off to Docker — there is currently no way to deploy a python- or
effect-generated project through `bp deploy`.

## The interface

```go
// internal/codegen/codegen.go
type Generator interface {
    // Files returns the generated project as in-memory OutputFiles.
    // It MUST NOT write to disk. A non-nil error means the target cannot
    // generate for this AST (e.g. an unsupported feature).
    Files(file *ast.File) ([]OutputFile, error)

    // Generate builds Files and persists them via WriteOutputFiles.
    Generate(file *ast.File, outDir string) error
}
```

The load-bearing method is **`Files`**. Emit logic stays pure and
unit-testable: assert on the returned `[]OutputFile` directly, no temp dir
required (see `TestFilesNoDisk` in `internal/codegen/js/generator_test.go`).
`Generate` is a thin wrapper every target writes identically:

```go
func (g *Generator) Generate(file *ast.File, outDir string) error {
    files, err := g.Files(file)
    if err != nil {
        return err
    }
    return codegen.WriteOutputFiles(outDir, files)
}
```

Assert the implementation at the bottom of your generator:

```go
var _ codegen.Generator = (*Generator)(nil)
```

### The `--gen-tests` builder convention

Options that change what a target emits (not what AST it reads) are chainable
`With*` methods on the `New()`-constructed `*Generator`, mutating a private
field and returning `g` so callers can compose them fluently:

```go
gen := js.New().WithReactQuery(reactQuery).WithFrontendOnly(frontendOnly).WithGenTests(genTests)
gen := pythongen.New().WithGenTests(genTests)
```

`WithGenTests(true)` is the one every target is expected to support — it's how
`bp build --gen-tests` (and `bp test`, which always enables it) turns on the
auto-generated contract-test suite for that target: an in-memory PGlite-backed
Vitest suite for node, a `testcontainers[postgresql]`-backed pytest suite for
python. `WithReactQuery`/`WithFrontendOnly` are node-only today — a new target
only needs to add the options it actually has a behavior for; there's no
required interface beyond `Generator` itself, so an option with no effect for
your target simply shouldn't exist rather than being a silent no-op.

## OutputFile and what the writer does for you

```go
type OutputFile struct {
    Path      string // relative path within the output dir (forward slashes)
    Content   []byte
    UserOwned bool   // scaffold-if-missing, never overwrite
}
```

`codegen.WriteOutputFiles(outDir, files)` is shared by every target, so on-disk
behavior is identical across `--target node|python|...`. It:

- **Tracks a manifest** at `.blueprint/manifest.json` (sha256 per generated
  file). Files emitted by a previous build but no longer produced are **removed**
  — renamed/dropped output never leaves stale artifacts.
- **Respects `UserOwned`**: those files are written only when missing and never
  overwritten, so a developer's hand-filled implementations survive rebuilds.
- **Sandboxes paths**: `..` and absolute escapes are rejected; everything stays
  within `outDir`.

You never write files yourself. You return `OutputFile`s and mark the ones the
user is meant to edit as `UserOwned`.

### Generated vs user-owned

A generated *wrapper* + a user-owned *stub* is the standard pattern for native
implementations. For JS, `fn foo { impl node { module: "./internal/foo" } }`
emits:

- `src/functions/foo.ts` — generated wrapper that imports and re-exports.
- `src/impl/functions/internal/foo.ts` — `UserOwned: true` stub for the
  developer to fill in.

Python mirrors this (generated dispatch + `raise NotImplementedError` scaffold).
Any new target must keep the same split so user code is never clobbered.

## The resolve IR — consume it, don't re-derive

`internal/resolve` is a **target-agnostic** pass (it must not import
`internal/codegen`) that computes semantic facts once so every generator reads
ground truth instead of re-walking the AST with heuristics. This is the lever
that makes the Nth target cheap. Use it.

What it exposes today:

- **`resolve.ResolveBlock(stmts) *BlockFacts`** — per-block variable facts. For
  each `|>` binding from a data op, a `StepFact{Name, Model, Cardinality}` where
  `Cardinality` ∈ `SingleCard` (`fetch`), `CollectionCard` (`query` without
  paginate, `map`), `PaginatedCard` (`query ... paginate(...)`). `MapResults` is kept separate
  from `DataOps` for the `map items: <data-op>` outer binding.
- **`resolve.AsyncFunctions(file)`** — the set of declared fn/pipe names that are
  async (so the target can `await` / `async def` correctly). JS composes this
  with its own built-ins (`upload`, `deleteS3Object`); Python uses it directly.
- **`resolve.FKAccessesInExpr` / `FKAccessesInStmt`** — foreign-key access
  patterns (`item.product.price`) so targets can emit the right sub-query /
  join, deduped via an alias map at the call site.
- **let/const facts** — `InputsReassignedInSteps`,
  `FetchVarsReassignedByUnboundUpdate`, `VarsWithPropertyMutation` — so a target
  knows which bindings must be mutable.

Targets apply their own naming on top (e.g. JS wraps `resolve.FKAccess` in a
camelCase `fkAccessInfo` via `toFKAccessInfo`). Keep the IR naming-neutral.

> Not yet in the IR: the JS generator still carries some bookkeeping in an
> ad-hoc `emitCtx` (the deferred "Slice 5" in BACKLOG). A new target does **not**
> need to reproduce `emitCtx` — call the resolve facts directly (Python already
> does). If you find yourself re-walking the AST to recompute something, that's a
> signal it should move into `internal/resolve` so all targets share it.

## Shared string/path helpers

`internal/codegen/common` holds pure, target-neutral helpers — `toCamelCase`,
`toSnakeCase`, `toPascalCase`, `toKebabCase`, `pluralize`, `extractResource`,
`extractPathParams`, `isPathParam`. Reuse these; do not fork per-target copies.
Anything genuinely target-specific (JS `await` sets, Python sync-vs-async route
selection) stays in the target package.

## Graceful degradation: `unsupportedFeatures`

A target need not support every construct on day one. The Python generator's
`unsupportedFeatures()` walks the AST and returns a sorted list of constructs it
can't yet emit; `Files` returns a clear error naming them and pointing at
BACKLOG, instead of emitting broken code:

```
python target does not yet support: `worker` declarations, `subscribe` declarations.
  Track progress in BACKLOG.md ("Python target") ...
  Use --target node for the full feature set today.
```

New targets should adopt the same pattern: ship a vertical slice, reject the
rest with a specific, actionable message. Never emit code you can't stand behind.

## Type mapping

| Blueprint | JavaScript | Python | Go (planned) |
|-----------|------------|--------|------|
| `string` | `string` | `str` | `string` |
| `int` | `number` | `int` | `int64` |
| `float` | `number` | `float` | `float64` |
| `bool` | `boolean` | `bool` | `bool` |
| `uuid` | `string` | `UUID` | `uuid.UUID` |
| `timestamp` | `Date` | `datetime` | `time.Time` |
| `json` | `unknown` | `dict` / typed model | `json.RawMessage` |
| `money` | `Decimal` | `Decimal` | `decimal.Decimal` |
| `file` | upload handle | `UploadFile` | `multipart.File` |

## Naming conventions

| Construct | JavaScript | Python | Go (planned) | Ruby (planned) |
|-----------|------------|--------|------|------|
| Variables | `camelCase` | `snake_case` | `camelCase` | `snake_case` |
| Functions | `camelCase` | `snake_case` | `CamelCase` | `snake_case` |
| Types | `PascalCase` | `PascalCase` | `PascalCase` | `PascalCase` |
| Files | `kebab-case.ts` | `snake_case.py` | `snake_case.go` | `snake_case.rb` |
| Constants | `SCREAMING_SNAKE` | `SCREAMING_SNAKE` | `ScreamingSnake` | `SCREAMING_SNAKE` |

Models pluralize for table names. Snake_case `.bp` identifiers convert to the
target's idiom via `internal/codegen/common`.

## Semantics every target must preserve

These are language guarantees, not stylistic choices — a target that breaks them
is wrong even if it compiles:

- **Arrow ordering**: inputs (`<-`) → steps (`|>`) → outputs (`->`).
- **`guard cond -> status "msg"`** is an early return mapping to an HTTP error.
- **`when cond`** is a single conditional step (no nesting beyond one level).
- **`try/recover`** is the only construct allowed two brace levels; it cannot
  nest. A `try` body that performs multiple writes should be **atomic** — the JS
  target wraps such bodies in a `db.transaction(...)` so partial writes roll back
  on failure (the Python savepoint equivalent is in progress; see
  [BACKLOG.md](https://github.com/abdul-hamid-achik/blueprint/blob/main/BACKLOG.md)). A new target should map `try/recover` onto its
  database's transaction primitive deliberately — `@effect/sql withTransaction`
  is a natural fit. External side effects inside the body are not rolled back;
  `recover` remains the place to compensate for those.
- **`map`** is the only iteration; there are no general loops.
- **Intent (`@ "..."`)** is an AST node, not a comment — surface it as a doc
  comment in generated output where natural.

## Exit-code contract

`cmd/bp/main.go` uses the same four exit codes across every command, and a new
target's wiring must keep respecting them (`resolveTarget` + each command's
dispatch `switch` follow this already — match it for any new case you add):

| Code | Meaning | Where it comes from for codegen |
|------|---------|----------------------------------|
| `0` | Success | `Generate`/`Files` returned no error and `WriteOutputFiles` succeeded |
| `1` | Validation error | Parse or `checker.Check` errors — never reaches your generator |
| `2` | Environment/file error | Unknown/malformed `--target`, unreadable input file, output-directory safety check failed |
| `4` | Codegen error | Your target's `Files`/`Generate` returned a non-nil error (e.g. `unsupportedFeatures()` rejected a construct) |

A target should never need a different code: an AST that fails to check never
reaches `Files`, and everything your generator itself rejects is a `4`.

## Adding a new target — checklist

1. Create `internal/codegen/<target>/` with a `Generator` struct and `New()`.
2. Implement **`Files(*ast.File) ([]codegen.OutputFile, error)`** + the standard
   `Generate` wrapper; add `var _ codegen.Generator = (*Generator)(nil)`.
3. Consume `internal/resolve` for variable/cardinality/FK/async facts; use
   `internal/codegen/common` for naming. Do not re-derive.
4. Mark developer-editable scaffolds `UserOwned: true`.
5. Add an `unsupportedFeatures()` gate for the long tail you don't cover yet.
6. Wire the target into the `--target` dispatch in `cmd/bp/main.go` for `bp
   build` and `bp diff` (and `bp migrate` if your target has a migration
   story). `bp deploy`'s `--target` is a separate deploy-target concept, not
   a codegen target, and `bp test` has no `--target` at all — see
   [Per-command target dispatch](#per-command-target-dispatch).
7. Add a generator test that asserts on `Files()` output (no temp dir) plus an
   end-to-end build of an example; gate it in CI like the existing targets.
8. Update this table and the BACKLOG.

## Compiler pipeline

```
.bp source
   ↓  Lexer → Tokens
   ↓  Parser → AST
   ↓  Checker → validated AST
   ↓  resolve → BlockFacts (target-agnostic semantic IR)
   ↓  Generator.Files → []OutputFile
   ↓  WriteOutputFiles → disk (manifest + UserOwned + stale cleanup)
```
