# AGENTS.md — Blueprint project guide

Read this before changing the Blueprint toolchain. It is written for coding
agents as well as human contributors.

## What Blueprint is

Blueprint is a Go compiler and developer toolchain for `.bp` service
definitions. The normal build path is:

```text
.bp source
  -> lexer tokens
  -> parser AST
  -> semantic checker
  -> resolved, target-neutral facts
  -> node | python | effect generator
  -> []codegen.OutputFile
  -> manifest-aware writer
  -> runnable project
```

`bp generate` is a separate, opt-in source transformation. It resolves quoted
`@>` slots into Blueprint arrow statements and can splice those statements back
into the original `.bp` file before the normal check/build pipeline runs.

`docs/language-reference.md` is the published language contract; lexer, parser,
checker, and formatter tests are its executable counterpart. If the frontend
deliberately grows new syntax, update the public reference and tests in the
same change. If implementation and documentation disagree accidentally, fix
one of them or document the staged gap; do not silently choose a third behavior
in codegen.

## Architecture

### Compiler front end

- `internal/lexer/` — hand-written tokenizer and token definitions.
- `internal/parser/` — recursive-descent parser with Pratt expression parsing
  and panic-mode recovery. The public entry point is
  `parser.ParseFile(filename, src)`.
- `internal/ast/` — syntax tree nodes, visitor/walker, and the `.bp` formatter.
- `internal/checker/` — scopes, name resolution, structural rules, and semantic
  validation. The public entry point is `checker.Check(file)`.
- `internal/resolve/` — target-neutral facts shared by generators, including
  binding/scope, query-relationship, foreign-key, and async-function
  information.
- `internal/diag/` — structured diagnostics and `bp explain` content.

### Code generation and output ownership

- `internal/codegen/codegen.go` defines `Generator` and `OutputFile`.
- `internal/codegen/common/` contains helpers that are genuinely shared across
  targets.
- `internal/codegen/js/` is the reference Node generator: Hono, Drizzle, Zod,
  BullMQ, Redis, frontend contracts, Vitest, and PGlite.
- `internal/codegen/python/` emits FastAPI, SQLAlchemy, Pydantic, Alembic, and
  pytest/testcontainers output.
- `internal/codegen/effect/` is an experimental, runnable Effect-TS
  health/config scaffold. It intentionally rejects unsupported service constructs.
- `internal/codegen/writer.go` writes generator output and maintains
  `.blueprint/manifest.json`.

Generators should first produce `[]codegen.OutputFile`. The shared writer owns
filesystem behavior: deterministic output, stale-file cleanup, overwrite
warnings, and manifest hashes. Files marked `UserOwned: true` are scaffolded
only when missing and must not be overwritten on later builds.
Every target's `Files` method must call
`codegen.RejectUnresolvedGenerateSteps(file)` before emission and must reject
unsupported AST kinds rather than silently dropping them.

Node and Python share one database-activation rule: declaring any model implies
the full Postgres dependency/env/schema/migration layer, even without an
explicit `database` entry. Conversely, `database postgres` with no models must
still emit an importable empty schema (`schema.ts` on Node, `Base` on Python).

### Developer tooling

- `cmd/bp/main.go` — CLI dispatch, flag parsing, command orchestration, and
  environment subprocesses.
- `internal/generate/` — Anthropic-backed `@>` discovery, prompt construction,
  and source-line replacement.
- `internal/importer/` — conservative TypeScript structure recovery for
  `bp import`; it emits review scaffolds, never handler translations.
- `internal/linter/` — style and best-practice rules.
- `internal/docs/` — OpenAPI 3.1 generation.
- `internal/lsp/` — diagnostics, hover, go-to-definition, context-aware
  completion, and local-workspace symbols over LSP.
- `editors/vscode-blueprint/` — packaged VS Code stdio client and TextMate
  grammar; it starts configurable `bp lsp` and exposes a restart command.
- `internal/agentctx/` — embedded `bp context` and `bp llms` guidance.
- `internal/registry/` — registry groundwork; package install/search/publish CLI
  commands do not ship yet.

### Target maturity

| Target | Current contract |
|--------|------------------|
| `node` (default) | Reference target. REST/model/codegen, migrations, frontend contracts, and generated tests are the mature path. Realtime and queue surfaces exist but deserve feature-specific verification. |
| `python` | Advanced beta. FastAPI/SQLAlchemy output and pytest/testcontainers generation ship; unsupported long-tail constructs should fail clearly instead of being silently mis-emitted. |
| `effect` | Experimental scaffold. A pinned health service and typed secret/env config ship; model, endpoint, and test generation remain unsupported. |

`bp build` and `bp diff` accept all three codegen targets. `bp test` and
`bp migrate` accept `node` or `python`; `effect` is rejected for those flows.
`bp deploy --target` is a different axis: `docker` works, while `fly` is
reserved but not implemented. Deploy currently builds the Node target.

## Language invariants

These rules define the language and should remain visible in the public
reference plus parser, checker, formatter, and generator tests:

1. **Flat by force** — ordinary blocks have at most one nested brace level;
   `try/recover` is the deliberate exception.
2. **Intent is syntax** — `@ "..."` is an AST node, not a comment.
3. **Arrows are directional** — inputs (`<-`) precede steps (`|>`) and outputs
   (`->`). A generation step is direct syntax: `@> "quoted prompt"`.
4. **No general `if`/`else`** — use `guard` for early returns and `when` for a
   conditional step.
5. **No general loops** — use `map`.

## Codegen patterns to preserve

### Resolve once, render per target

Facts that multiple generators need belong in `internal/resolve`, not in a
target-specific helper. A target may choose different syntax, but it must
preserve Blueprint semantics such as arrow ordering, guard behavior,
transaction intent, and manifest ownership.

### Node emission context

Node route/function emission passes an `emitCtx` through statement rendering.
It tracks request-context variables, resolved Blueprint bindings, declarations,
model bindings, async functions, and struct-enum lookups. Prefer extending that
context over adding another global or string-only special case.

`exprToJSWithCtx` is the context-aware expression path. It handles injected
middleware values, async calls, request headers, model-aware bindings, and
struct-enum configuration access.

### Struct enums

An enum with structured variants generates a string-value enum plus a config
lookup object. Keep database values and configuration lookup separate; route
expressions use the config object for structured access.

### Native implementation stubs

An `impl node` function can produce both a generated wrapper and a user-owned
implementation stub. `genFunction` therefore returns multiple output files.
The implementation stub must be `UserOwned: true`.

## Verified limitations

Treat these as correctness constraints for docs, diagnostics, and new features:

- On the Node target, bare endpoint metadata such as `auth api_key`,
  `auth bearer`, or `auth session` does not generate generic credential
  verification. Webhook HMAC via `auth webhook_sig using(secret.KEY)` is the
  implemented generated-auth path. Use explicit middleware for other auth.
- This endpoint-auth limitation does not apply to Node `external` service
  configuration. External `bearer`/`jwt`/`basic`/`api_key` auth requires exactly
  one declared `secret.NAME` or `env.NAME` and generates the corresponding
  env-backed request header. `retry N` means N additional immediate attempts
  for network/timeout failures and HTTP 408/429/5xx, with a fresh timeout per
  attempt; other 4xx responses are not retried. Malformed configuration must
  fail before any files are returned.
- Node endpoint `cache <duration>` metadata is currently emitted as a comment,
  not as response caching. Endpoint `limit` is an in-process, per-instance rate
  limiter rather than a distributed guarantee.
- The Node target rejects file/MIME endpoint inputs, including file-containing
  named types, until route parsing and the generated SDK both support
  multipart/form-data. File/MIME types remain valid in functions, pipes, and
  native implementation signatures.
- General generated expressions resolve declared environment values as
  `env.NAME`. Do not document arbitrary `secret.NAME` expression access;
  `secret.KEY` is specially interpreted by webhook-auth syntax.
- `bp import [path] --from ts` is a lossy structure scaffolder. It recognizes
  static Drizzle tables/enums, Hono route shapes, and only named Zod object
  inputs used by static, transport-compatible `zValidator` calls. Nullable or
  type-changing Zod fields skip with warnings. Static SQL column names are
  preserved when representable; renamed/dynamic SQL identity and dropped
  Drizzle builder/table/reference options must be warned. It never lifts
  handler bodies: every recovered route must remain a reported `TODO(import)`
  returning 501. Dynamic/unsupported structures must warn or skip rather than
  being guessed, and `bp check` does not prove import fidelity.
- `--gen-property-tests` is opt-in and Node-only on `build`, `diff`, and
  `test`; it implies contract tests. Generation uses deterministic fast-check
  seeds and 32 valid-request runs, resets PGlite per run when a database is
  active, and must fail with no files if any route uses unsupported input,
  auth/header/rate-limit state, impossible path/email/URL constraints,
  a ref-backed field in a `save`/`seed`/`update` block, a reachable recursive
  inline `fn`/`pipe` call graph, native/user implementations, external services,
  queues/storage/analytics/events/realtime, sleep, or wall-clock behavior.
- The Effect target is a health/config foundation, not an application emitter.
  It accepts only `runtime node`, the declared version/port, secrets, and env
  defaults that map faithfully to Effect `Config`; blueprint `use`, database,
  models, endpoints, authored tests, and other declarations return no files.
  `--gen-tests` and `--gen-property-tests` reject for Effect on both `build`
  and `diff`.
- Node relationship loading is one-level `query ... with(relation)` only. The
  relationship comes from `<relation>_id ref(target)` and emits a nullable LEFT
  JOIN against `target.id`. Aliases, self joins, two requested relations to the
  same target, field collisions, dynamic/duplicate names, legacy query args,
  and `fetch ... with(...)` reject. Python and Effect reject the feature.
- Model computed fields are pure, total, read-only, and non-persisted. The
  supported type/expression subset is enforced by the checker; optional or
  unsupported inputs, forward references, calls/field access, and assignment
  reject. Computed keys in `save`/`seed`/`update` bodies also reject, and they
  cannot be database `where`/`order` columns. Node materializes the values and
  includes them in contracts; Python/Effect reject computed models.
- LSP completion is based on the open document's recovery AST plus source
  context and has no resolve request. Workspace symbols scan only local `.bp`
  files, ignore common generated/vendor/VCS directories, and prefer unsaved
  documents. Full-document sync ships; rename/references/code actions do not.
- Node-authored `test {}` generation supports a narrower request surface than
  the grammar suggests. `bp test` preflights and rejects cleanup/shared setup,
  dynamic targets, custom or multipart/file requests, unsupported auth/timing
  forms, non-lowercase/duplicate request keys, nonpositive repeats, GET/HEAD
  bodies, calls/interpolation, and whole setup rows used as auth values. Setup
  supports only `seed`/`save` plus simple unbound `log`. Assertions must map to
  executable emitter forms: equality RHS values are safe literals, hyphenated
  header assertions and `not_exists` reject (`not exists` is supported), and at
  most one model assertion is allowed. Native preflight covers dependencies
  referenced by every app-loaded route, realtime/background/subscription, and
  middleware module. Basic auth still expects a pre-encoded value. Do not add
  examples that promise broader behavior without adding generator coverage.
- `emit` dispatches the in-process event/subscription bus. Source-less
  `subscribe "event" { ... }` consumes that bus. `emit ... to(service)` and
  `subscribe ... from(service)` are rejected until an external transport
  adapter exists. Use `enqueue "queue" { ... }` to produce a BullMQ job for a
  worker.
- Workers, schedules, `enqueue`, and `subscribe` are implemented on the Node
  target; do not reintroduce old documentation that labels them absent.
- Node `enqueue` is currently supported only in HTTP endpoint bodies and must
  resolve to exactly one worker queue. `retry N` means N additional attempts;
  generated producers pass `attempts: N + 1` plus fixed/exponential backoff,
  with `max` enforced through a generated custom BullMQ strategy. Unsupported
  producer contexts and malformed or ambiguous queue policies fail before any
  files are returned. Worker timeouts reject the wait but do not cancel
  in-flight handler work, and `on_fail` compensation does not yet cover
  terminal stall exhaustion or an early BullMQ `UnrecoverableError`.
- `bp generate --write` edits the source `.bp` file. It does not create a
  TypeScript implementation file.
- Docker deploy smoke testing uses the port declared in the generated
  Dockerfile. Fly deployment is not yet implemented.
- The Python target uses an exhaustive support gate. It rejects authored
  `test`/`fixture`/`test_group` blocks, inline `fn logic`, STREAM/WS handlers,
  named enums/aliases/types/env declarations, model computed fields,
  `query ... with(...)`, file or named endpoint inputs,
  unsupported endpoint metadata/`on_error`, and middleware configuration or
  `after` bodies rather than silently dropping or commenting them out. Its
  supported middleware step subset is `fetch`, `log`, declared function calls,
  `inject`, and `guard`; richer steps fail closed. It also rejects
  attribute access on JSON/map endpoint inputs or JSON-returning function
  results, unknown value calls, unsafe/malformed function implementation
  config, mismatched defaults/constraints, and names that would become Python
  keywords. Bare `query model where(q)` is supported
  only when `q` is a string/text endpoint input; dynamic filter accumulators and
  other bound values in that position fail closed. Endpoint `header.X`
  references generate a valid `Header(..., alias=...)` parameter (including
  hyphenated names), while `env.FIELD` must be backed by a declared secret or
  generated infrastructure setting. Raw string interpolation of header/env or
  dictionary-backed values is rejected; direct header/env expressions remain
  supported.
  Keep Python-only tests and unsupported transports outside generated output
  until those constructs are translated.
- The package-registry document is a design proposal. `bp add`, `bp search`,
  and package publishing commands do not exist. Do not confuse those proposed
  package commands with the shipped, local-only `bp import` scaffolder.

When a limitation is fixed, update this section, public docs, agent context,
and regression tests together.

## Making changes

### Add or change syntax

1. Update `docs/language-reference.md` and any affected CLI/error reference.
2. Add or change lexer tokens if needed.
3. Parse into an AST node; keep source locations accurate.
4. Add semantic validation and structured diagnostics.
5. Add target-neutral resolution facts if more than one backend needs them.
6. Implement each supported target, or reject the feature explicitly with a
   useful target-specific error.
7. Update the formatter, LSP/docs surfaces, fixtures, and tests.

### Add a CLI command or flag

1. Add dispatch/help parsing in `cmd/bp/main.go`.
2. Update the shared `cliCommands` metadata so completion, suggestions, and
   agent context do not drift.
3. Add CLI tests for success, malformed flags, and exit codes.
4. Update README/public CLI docs when the user-facing surface changes.

### Add a codegen target or feature

1. Reuse `internal/resolve` and `internal/codegen/common` where semantics are
   shared.
2. Return deterministic `OutputFile` values; let the shared writer touch disk.
3. Mark developer-owned scaffolds `UserOwned`.
4. Test generated contents, manifest behavior, rebuild idempotency, and the
   target's native compiler/test runner.

## Verification

Run the smallest relevant package tests while iterating, then the full Go suite:

```bash
go test ./...
go build -o bin/bp ./cmd/bp
./bin/bp check testdata/valid/all_features.bp
```

For Node codegen changes, build the reference fixture and type-check the result:

```bash
./bin/bp build testdata/valid/all_features.bp --target node --out /tmp/blueprint-node
cd /tmp/blueprint-node
bun install
bun run build
```

For Python or Effect work, run that generator's Go tests and build representative
fixtures with the matching `--target`. If generated dependencies are available,
also run the generated target's compiler/tests.

## Repository conventions

- Go implementation code uses the standard library only.
- Generated source is built with explicit string builders rather than Go
  templates.
- Valid and invalid language fixtures live under `testdata/`.
- Generated identifiers convert Blueprint `snake_case` to target conventions;
  models/types remain consistently pluralized or Pascal-cased as appropriate.
- Preserve unrelated worktree changes. Generated/user-owned files may already
  contain developer edits.
- `docs/` is the public VitePress site at **blueprint-lang.dev**. Put only
  user-facing material there. Internal audits, reviews, and design spikes
  belong outside the published docs tree.
