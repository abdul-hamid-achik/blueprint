# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [0.11.0] - 2026-06-16

Adds a third codegen target (an Effect-TS scaffold), a one-shot agent/LLM guide command (`bp llms`), two codegen correctness fixes (duplicate `pgEnum`, transactional `try/recover`), a generator-interface refactor that exposes `Files()`, and a full documentation accuracy + learnability pass (the VitePress site builds with every `.bp` snippet `bp check`-clean and zero dead links).

### Added

- **`bp llms [--out <file>]`** — prints the complete agent/LLM onboarding guide in one self-contained document: a framing preamble, the live CLI command surface, the codegen target list, and every `bp context` topic concatenated in reading order. `--out` writes an `llms.txt`. Assembled from `internal/agentctx`, so it always reflects the real binary. Complements `bp context <topic>` (one slice) and `bp explain <code>` (one error code).

- **`--target effect` (experimental scaffold)** — a new `internal/codegen/effect/` generator emitting an Effect-TS (`@effect/platform` HttpApi + `Schema` + `@effect/sql`) project shell and a `Config`-based secrets module, wired into `bp build` and `bp diff`. Model/endpoint emit is in design; unsupported constructs are rejected with a clear, actionable message (the Python-target staging pattern). Opt-in, not the default — `node` stays default. The generator contract every target satisfies is now documented in `docs/multi-target-codegen.md`.

- **`docs/multi-target-codegen.md` rewritten as the authoritative generator contract** — the `Files()` interface, `OutputFile`/`UserOwned`, the manifest writer, the `resolve` IR surface, `common` helpers, the `unsupportedFeatures` degradation pattern, and an add-a-target checklist. New `CLAUDE.md` + an `AGENTS.md` convention note record that `docs/` is the published website (blueprint-lang.dev) and internal notes belong outside it.

- **`bp context [topic] [--format md|json]`** — agent-facing language + CLI surface, embedded into the binary. With no topic, prints a synthesized full surface (version, command index, codegen target list, topic catalogue) modeled on `cairntrace`'s `cairn explain`. With a topic, prints focused docs for one of eight curated topics: `overview`, `language`, `cli`, `codegen`, `targets`, `errors`, `workflow`, `examples`. Each topic is a hand-written Markdown file under `internal/agentctx/topics/` (embedded via `go:embed`), kept short and scannable so agents can bootstrap on Blueprint in one or two reads. JSON output is structured under the URN `urn:blueprint.dev:context:v1` with parsed sections + fenced-code examples for tooling consumers. Self-contained — works offline, no network access required. Mirrors the pattern of `bp explain <code>` (which prints one error code's docs) but covers language + CLI rather than diagnostics. The `effect` target and the `try/recover` transaction behavior are now reflected in the embedded topics.

### Changed

- **Codegen `Generator` interface exposes `Files(*ast.File) ([]OutputFile, error)`** alongside `Generate(file, outDir) error`. Emit logic is now pure and unit-testable without a temp dir (`TestFilesNoDisk`); `Generate` is a thin `Files` + `WriteOutputFiles` wrapper in every target. This de-risks adding the Effect/Go/Ruby targets. JS and Python generators both updated.

- **`try/recover` (node target) wraps multi-write bodies in a DB transaction.** A `try` body with ≥2 data mutations and no in-body `guard`/output now emits `await db.transaction(async (tx) => { … })`, so partial writes roll back if a later step throws. Single-write bodies and bodies that return mid-block are unchanged (a `return` inside a transaction callback would commit partial state). `examples/ecommerce-api.bp` updated so `recover` compensates only for external effects (the DB rolls back on its own); generated TS still `tsc`-passes. External side effects inside a `try` body are not transactional.

- **`bp version` displays `0.11.0`** (was `0.10.0`).

- **Documentation accuracy + learnability pass** across the VitePress site, driven by a multi-agent audit that ran the real CLI. Getting Started no longer crashes a new user (it now does DB setup + lists Bun as a prerequisite + leads with the `bp run`/`bp migrate` happy path); example snippets that failed `bp check` are fixed (undeclared `fn`s, an invalid `-> skip` guard target) and a "Calling your own code (`fn`)" section draws the builtin-vs-user-function line; the package registry page is flagged design-only (its commands don't ship yet); `roadmap.md`/`changelog.md` are reconciled with shipped reality; and the three-target story is threaded through `index`/`faq`/`deployment`/`testing-guide`/`architecture`/`generated-output`. Every `.bp` snippet in the docs is now `bp check`-clean and the site builds with zero dead links.

### Fixed

- **Duplicate `pgEnum` name collision (node target).** A declared `enum` plus an inline `enum(...)` with identical variants previously emitted two `pgEnum` consts sharing one Postgres type name → a duplicate `CREATE TYPE` that collides in a drizzle-kit migration. pgEnums are now registered by type name: an inline enum matching a declared enum reuses it, differing variants disambiguate to `<model>_<field>`, and inline-enum emission is now deterministic (was map-iteration order).

## [0.10.0] - 2026-05-29

A focused release that closes the 1.0 audit gates for typed handler boundaries, LSP, structured codes, fmt round-trip safety, doctor, and migrate/deploy `--target` plumbing — plus fixes three Python-target runtime bugs that were shipping silently in v0.9.0.

### Added

- **LSP: `publishDiagnostics` + `textDocument/definition` + context-aware hover**. The Blueprint LSP server is now genuinely useful in editors. `publishDiagnostics` runs the parser + checker on every `didOpen`/`didChange` and emits LSP `Diagnostic` objects with severity, structured code (`Cxxx`/`Pxxx`/`Lxxx` when set), `source: "blueprint"`, and a source range — editors show squiggles for parse + check errors and the structured code is clickable through to `docs/error-codes.md`. `textDocument/definition` walks the cached AST and jumps to declarations for model names, `fn`/`pipe`/`middleware` references (in calls or `via` clauses), and `<model>.<field>` references. Hover is now context-aware: models render with `@intent` + field list; `fn`/`pipe`/`middleware` render with their signature + intent; fields render with their type + constraints; data-op steps (`query`/`fetch`/`save`/`update`/`delete`/…) get short docs.
- **Structured codes `L001` + `P001` + `C013`–`C015`**. Extended the structured-codes namespace: `L001` (lexer) for `'|'` not followed by `'>'`; `P001` (parser) for a file that opens with a non-blueprint top-level construct; `C013` (checker) for `unknown type`; `C014` for `ref(<model>)` pointing at a name that isn't a model; `C015` for `call <service> ...` where the service isn't declared via `external`. `bp explain L001` / `P001` / `C013` / `C014` / `C015` all return docs.
- **`bp deploy --target` parsing**. `bp deploy` now parses `--target {docker,fly}` (default `docker`). `--target docker` builds the image and additionally runs it as `bp-smoke`, probes `/health`, then tears the container down. `--no-run` opts out of the smoke step for CI image builds. `--target fly` exits non-zero with a clear "not implemented; tracked for v0.11" message instead of silently building Docker.
- **`bp migrate --target python`**. `bp migrate` now parses `--target {node,python}` (default `node`). The Node path is unchanged (shells to `bunx drizzle-kit <subCmd>`). The Python path builds the FastAPI tree first, then shells to `uv run alembic ...` (`generate` → `revision --autogenerate`, `push` → `upgrade head`, `check` → `alembic check`; `studio` is rejected because Alembic has no GUI). When `uv` is missing, a clean error fires instead of clobbering the Python tree with Node output.
- **Linter rules: `where-predicate-self-equal` and `unused-input`**. `where-predicate-self-equal` flags `where(X == X)` only when no input, step binding, or URL path param named `X` is in scope (so `where(email == email)` with a matching `<- email` input is correctly ignored). `unused-input` flags inputs declared via `<- name type` that are never referenced in the endpoint body; the search/filter convention (`q`/`search`/`query`/`keyword`/`term`/`filter`) is exempted.

### Changed

- **Hono `Variables` typed from the middleware-injected model instead of `any`**. Every route file that uses `@use middleware` now types `c.get('<var>')` against the injected model's column set (wrapped in a `NonNullable` mapped type) instead of `any`. The model is resolved via the existing `inject <binding> as <var>` mapping. Client SDK types (`types/api.ts`) widen from `unknown` to concrete column types.
- **Merged `fn` scaffolds typed from `fn.Inputs`/`fn.Outputs` instead of `...args: any[]`**. The user-owned `src/impl/functions/...` scaffolds that get merged when multiple `fn` declarations share an `impl module:` path now emit typed signatures (e.g. `export async function hashPassword(password: string): Promise<any>`). A new `g.fnSignatureTS(fn)` helper shares the typing logic with the per-fn path so both stay in sync.
- **`bp doctor` probes the full toolchain `bp` actually uses**. The deps list now includes `drizzle-kit`, `tsc`, `python3`, `uv`, `alembic`, `pytest` in addition to the existing bun/node/postgres/redis/git probes, with an "(install via uv add)" hint for the project-local Python tools. The version parser switched from a whitespace heuristic to a semver regex, so `redis-cli 8.8.0` reports `8.8.0` and `Docker version 29.5.2, build 79eb04c` reports `29.5.2`.
- **`bp version` displays `0.10.0`** (was `0.9.0`).
- **Generated-file headers no longer hard-code `v0.2.0`**. The `// Generated by Blueprint from <source>` (and `# Generated by Blueprint from <source>` for Python) header dropped the stale version. Provenance still lives in `.blueprint/manifest.json` (per-file sha256 hashes) and git history.

### Fixed

- **`bp fmt` printer round-trip safety**. Four bugs in the printer could corrupt or silently rewrite user code; all are fixed and locked behind regression tests: `not existing` no longer prints as `notexisting` (word-form ops now keep a space); multi-line `{ ... }` block values indent their closing brace correctly; stream/WS shorthands (`|> inject payload.username as sender`, `|> join room(id)`, `|> broadcast room(id) { ... }`, etc.) print in their source form instead of round-tripping into parse errors; small `impl { module: "...", func: "..." }` blocks reflow back to inline form. All 5 shipped examples now round-trip cleanly under `bp fmt | bp check`.
- **Python codegen: three runtime-correctness bugs in endpoint body emit**. v0.9.0 shipped Python that parsed cleanly but crashed at request time for `auth-service`, `ecommerce-api`, and `realtime-chat`: `.count` on a collection now rewrites to `len(x)` instead of emitting the bound method; `delete <collection>` now emits a `for _row in <coll>: db.delete(_row)` loop instead of `db.delete(<list>)` (which SQLAlchemy rejects); and an unbound `map items: save M { ... }` now names its result by the snake-plural of the model so downstream references resolve instead of raising `NameError`.
- **JS codegen: `NonNullable` wrap on Hono-injected row types**. Surfacing the real schema row type initially broke `tsc` on fixtures whose models use `default(...)` without `required` (Drizzle types those as `T | null`). Blueprint's contract is "if it got injected, every column is usable" — once a middleware `guard <row>` has passed, every column is present — so the injected row type is wrapped with `{ [K in keyof T]: NonNullable<T[K]> }`.

## [0.9.0] - 2026-05-29

### Added

- **Python target — phase 1**. `bp build --target python` emits a runnable FastAPI project: `pyproject.toml` (uv-shaped), `src/app.py` with `FastAPI(...)`, one route file per resource using `APIRouter` and FastAPI path-param syntax (`:name` → `{name}`), `src/lib/env.py` (Pydantic Settings v2). String interpolation in outputs becomes f-strings. Verified end-to-end: `examples/hello-world.bp` builds and FastAPI's `TestClient` returns the expected responses.
- **Python target — phase 2: data layer**. `model` declarations and `database postgres` now emit a working data layer: `src/models/schema.py` (SQLAlchemy 2.0 declarative classes), `src/models/pydantic.py` (Pydantic v2 models with `from_attributes=True`), `src/lib/db.py` (sync engine + `SessionLocal` + `get_db` dependency), and a working Alembic skeleton. `pyproject.toml` picks up `sqlalchemy`/`psycopg`/`alembic` when a DB is in use.
- **Python target — phase 3: endpoint bodies with data ops**. `|>` steps and `guard` statements translate to SQLAlchemy + FastAPI: `save` → `schema.X(...) + db.add + commit + refresh`; `fetch` → `db.get`; non-paginated `query` → `select(...).scalars().all()`; `paginate(p, pp)` → items + `select(func.count())`; `update` → field assignments + commit; `delete` → `db.delete + commit`. Guards become `if not (cond): raise HTTPException(...)`. **`examples/todo-api.bp` now compiles end-to-end on `--target python`.**
- **Python target — phase 3b/3c/3d: where, first, when, order, FK access, try/recover, map, log, `fn` + `middleware` + `sum()`**. `query M where(...)` translates to `select(...).where(...)`; `first` becomes `.scalars().first()`; `when` blocks become `if`; `order(col, asc|desc)` chains `.order_by(...)`; FK access (`item.product.stock`) emits a `db.get(...)` lookup reused across references; `try { } recover { }` becomes `try: / except Exception as error:`; `map` emits a Python for-loop; `log "msg"` becomes `print(f"msg")`; `fn`/`middleware` declarations emit generated wrappers + user-owned scaffolds and wire `Depends(...)` into endpoint signatures; `sum(<coll>.<field>)` becomes a generator expression. **`examples/auth-service.bp` and `examples/ecommerce-api.bp` both compile end-to-end on `--target python`.**
- **Python target — phase 5: STREAM + WS + cache**. `cache redis` emits `src/lib/cache.py` with a Redis client + `cached(key, ttl, fn)` helper; STREAM endpoints emit a per-resource SSE router via `sse-starlette`; WS endpoints emit a `@router.websocket(...)` handler with the FastAPI handshake and a `receive_text` loop. **`examples/realtime-chat.bp` now compiles end-to-end on `--target python`** — Python coverage hits 5/5 examples.
- **Python target — phase 4: `--gen-tests` with testcontainers**. `bp build --target python --gen-tests` emits a runnable pytest contract suite backed by a real Postgres container (`testcontainers[postgresql]`). Each contract test builds a request from the input schema, seeds FK parents in topological order, interpolates seeded ids into path params, and asserts the response status is in the set the endpoint + its guards can declare. Run it with `uv sync && uv run pytest` (Docker required).
- **`bp explain <code>`** — print the embedded documentation section for a Blueprint error code (e.g. `bp explain C001`). Case-insensitive; returns exit 1 with a helpful hint when the code isn't documented yet. The canonical doc lives at `internal/diag/error-codes.md` (embedded via `go:embed`) and is mirrored at `docs/error-codes.md`; a Go test diffs the two and fails CI on drift.
- **Structured error codes `C001`–`C012`**. `CheckError` and `ParseError` gained an optional `Code` field. Checker codes shipped: `C001` (missing blueprint block), `C002` (blueprint name empty), `C003` (blueprint missing required field), `C004` (duplicate name), `C005` (duplicate endpoint), `C006` (unknown function), `C007` (unknown middleware), `C008` (identifier not snake_case), `C009` (path parameter not snake_case), `C010` (duplicate field in model), `C011` (arrow statement out of order), `C012` (try/recover cannot be nested). All get worked examples in `docs/error-codes.md`.
- **`internal/diag` package — shared diagnostic formatter**. Parser and checker now render errors through one place: `error[<code>]: file:line:col` header, source-line + caret, message, and `Hint:`. TTY-aware color (auto-disables for pipes; honours `NO_COLOR`).
- **`--target` flag**. `bp build`/`bp diff` accept `--target {node,python}`; default `node`. `internal/codegen/common` and `internal/resolve` were extracted as target-agnostic packages so the Python generator can reuse string/path helpers and typed-IR resolution.
- **`bp diff` is now line-level**: prints a unified diff (`---`/`+++`/`@@` hunks) per modified file instead of just a filename list, with ANSI color on a TTY. Adds `--apply` (write changes after showing the diff), `--exit-code` (return 1 if anything changed — useful in CI / pre-commit), `--no-color`, and `--gen-tests`.
- **`bp build --gen-tests`** (Node target): auto-generate contract + happy-path tests from the spec, backed by an in-memory (PGlite) database harness. Generated files: `test/_harness/{ddl,db,setup}.ts`, `vitest.config.ts`, and `test/generated/<resource>.test.ts`.
- **Self-contained `bp test`**: now enables `--gen-tests` automatically and runs against in-memory PGlite, so the suite runs with no external Postgres. Hand-written `test { }` blocks are also routed through the in-memory harness.
- **`docs/production-readiness.md`** — measurable gates for 1.0 across 5 pillars (language stability, codegen correctness, DX, testing/migrations, deploy), the status table, and the engineering workflow.
- **CI gates**: idempotency check (`bp diff --exit-code` after each example build), `--gen-tests` smoke, and `--target python` smoke + idempotency (build `hello-world.bp` to python, assert key files exist, AST-parse every emitted `.py`).

### Fixed

- **`bp fmt` preserves data-op shorthand**. The printer used to rewrite `|> todos = query todo paginate(page, per_page)` into `|> todos = query(todo, paginate(page, per_page))` — semantically identical but contradicting the README idiom. The printer now detects data-op call shapes and emits the shorthand form. (Column alignment of model fields / input statements remains a stretch item, so the `bp fmt --check` CI gate stays scoped to `hello-world.bp`.)
- **Codegen determinism for `update <other-model>` inside `map items: ...`**. The fallback path iterated a Go map in random order; two consecutive builds could produce different output, breaking the `bp diff --exit-code` idempotency gate. Now sorts keys alphabetically.
- **Server start guarded under tests**: generated `src/index.ts` now wraps `serve(...)` and the shutdown handlers in `if (!process.env.VITEST)`, so importing the app for `app.request(...)` never binds a port. Fixes `EADDRINUSE` crashes when multiple Vitest files run in parallel.
- **Authored test JSON bodies set `Content-Type`**: generated tests with a request `body` now send `Content-Type: application/json`, so the JSON validator parses the body instead of rejecting it with 400.

## [0.3.4] - 2026-02-24

### Fixed

- **Collection variables mapped to snake_case in responses**: `-> 200 { notes: notes }` where `notes` is a `query note` result now emits `notes.map((r: any) => ({ id: r.id, title: r.title, created_at: r.createdAt, ... }))` to convert Drizzle camelCase keys back to `.bp` snake_case. Applies to collections, single records, and paginated `.items`. Previously only outer BlockExpr keys were preserved while inner row data retained camelCase.
- **Unbound `update` reassigns fetch variable**: `|> update note { ... }` (no binding) after `|> note = fetch note(id)` now generates `note = (await db.update(...).returning())[0]` instead of discarding the `.returning()` result. The fetch binding uses `let` so the updated row is available to subsequent response output.

## [0.3.3] - 2026-02-24

### Fixed

- **Response JSON keys preserve `.bp` snake_case**: Output blocks like `-> 200 { created_at: note.created_at }` now emit `{ created_at: note.createdAt }` instead of `{ createdAt: note.createdAt }`. The JSON key matches the `.bp` source declaration while the value accessor correctly uses Drizzle's camelCase property. This applies to all endpoints including list responses (Bugs 1 & 8).
- **PATCH returns updated data via `.returning()`**: `update` data operations now use `.returning()` so the response contains the actual updated row, not stale pre-update data (Bug 2).
- **PATCH only sets defined fields**: PATCH handlers wrap set values with `Object.fromEntries(Object.entries({...}).filter(([_, v]) => v !== undefined))` so that fields not included in the request body aren't overwritten to `NULL` (Bug 3).
- **Auto timestamp fields bumped on update**: Model fields with the `auto` modifier (e.g., `updated_at timestamp default(now) auto`) now inject `updatedAt: new Date()` into every `update` set call (Bug 4).
- **`inArray` guarded against empty arrays**: Junction table queries like `where(id in links.tag_id)` now emit `(links.length > 0 ? inArray(...) : sql\`1 = 0\`)` to prevent Drizzle from crashing on empty arrays (Bug 5).
- **Validation schema uses original field names**: Zod schemas now use `.bp`-declared names (`tag_id`, `per_page`) instead of camelCase (`tagId`, `perPage`). Input extraction also reads from the original name: `c.req.valid('json').tag_id` (Bug 6).
- **CORS middleware auto-generated**: `index.ts` now always imports and applies `cors()` from `hono/cors` unless already declared via `use cors` in the blueprint block. This ensures SPAs and mobile apps can connect out of the box (Bug 7).

## [0.3.2] - 2026-02-24

### Fixed

- **Search ILIKE targets columns, not table**: `where(q)` now generates `or(sql\`${schema.note.title} ILIKE ...\`, sql\`${schema.note.content} ILIKE ...\`)` across all string/text columns in the model, instead of the whole table reference.
- **`text` type support**: `text` is now a recognized primitive type throughout the toolchain (parser, checker, codegen). Maps to Drizzle `text()` column, TypeScript `string`, and Zod `z.string()`.

## [0.3.1] - 2026-02-24

### Fixed

- **`where()` with optional filter params**: `query note where(q, pinned)` now generates proper SQL predicates — text search params (q, search) produce `ILIKE` patterns, boolean/enum params produce `eq()` with null guards, and optional conditions are filtered with `.filter(Boolean)`.
- **`.items` on non-paginated queries**: `all_tags.items` on a plain `query tag` (no `paginate()`) now resolves to the array itself instead of accessing a nonexistent `.items` property. Only paginated query results retain `.items`/`.total` access.
- **`inArray` in where clauses**: `where(id in links.tag_id)` now generates `inArray(schema.tag.id, links.map((r: any) => r.tagId))` instead of the incorrect `links.tagId.includes(id)`.
- **Compound `where` in `fetch`**: `fetch note_tag where(note_id == id, tag_id == tag_id)` now generates `and(eq(schema.noteTag.noteId, id), eq(schema.noteTag.tagId, tagId))` instead of nesting `where()` inside `eq()`.
- **`.env` propagation in `bp migrate`**: `bp migrate push` now copies the project's `.env` file into `generated/` before running `drizzle-kit`, so `DATABASE_URL` is available.
- **`tags` usable as identifier**: `tags` keyword is now allowed in expression/binding contexts (e.g., `|> tags = query tag`), fixing a parser restriction that prevented using `tags` as a variable name.

## [0.3.0] - 2026-02-24

### Added

- **FK relation access**: `item.product.stock` through `ref()` foreign keys now resolves via sub-queries. The codegen pre-scans expressions for FK access patterns and emits Drizzle lookups before the referencing statement.
- **`sum()` builtin**: `sum(items.price * items.quantity)` compiles to `.reduce((acc, r) => acc + r.price * r.quantity, 0)`.
- **Unbound map result capture**: `map items: save order_item { ... }` auto-captures the result when subsequent statements reference `orderItems`.
- **WS factory pattern**: WebSocket route files export `createXxxRoutes(upgradeWebSocket)` factory functions. `index.ts` imports `@hono/node-ws` and passes the runtime `upgradeWebSocket` at startup.
- **WS variable hoisting**: Variables assigned in `on_connect` (e.g., `room`, `sender`) are hoisted to the outer closure scope so `on_message` and `on_disconnect` can access them.
- **WS builtins**: `join`, `leave`, `broadcast` emit TODO comments; `emit` compiles to `emit('event_name', data)` with events lib import.
- **STREAM event subscriptions**: Event handlers compile to `on('event_name', async (eventData) => { ... })` using the events lib. Conditions prefix undeclared identifiers with `eventData.`.
- **STREAM timeouts**: `on timeout(5min)` compiles to `setInterval` with `stream.onAbort` cleanup.
- **ContentfulStatusCode typing**: All status codes use `N as const` for Hono's `ContentfulStatusCode` type. `BpError.statusCode` typed as `ContentfulStatusCode`.
- **Hono context variable typing**: Routes with middleware inject get `new Hono<{ Variables: { key: any } }>()` for type-safe `c.get()`/`c.set()`.
- **Context variable model resolution**: `update current_user { ... }` resolves to the real model via `varModels` mapping.
- **Stub merging**: Multiple functions sharing the same `impl` module produce a single merged stub file.
- **Events lib for STREAM/WS**: Events lib generated when STREAM endpoints have event handlers or WS handlers use `emit`.
- **Import collector cleanup**: Builtins and model names excluded from TODO stub generation.
- **Blueprint required fields**: Checker validates `version` and `runtime` are present.
- **Duplicate model field detection**: Checker reports duplicate field names within a model.
- **Parser recursion depth limit**: `maxExprDepth = 256` prevents stack overflow on deeply nested expressions.
- **Parser error count limit**: `maxErrors = 50` prevents runaway error cascading.
- **Duration `1d` support**: Lexer accepts `d` as a duration unit.
- **`@>` generate error**: Top-level `@>` steps emit a proper error instead of silent discard.
- **Todo-api golden test**: Snapshot test with 13 golden files for the todo-api example.
- **WS factory test coverage**: 5 explicit checks for the WS factory pattern in `index.ts`.
- **`@hono/node-ws` dependency**: Added to `package.json` when WS endpoints are present.
- **`REDIS_URL` env var**: Auto-added to `env.ts` schema when cache is enabled.

### Changed

- **Map loop variable**: Always uses `item` instead of model-derived name for consistency with bp convention.
- **Update ID fallback**: When `update` targets a model inside `map`, falls back to `item.id` from bound vars.
- **Webhook auth**: `Buffer.from(_sig, 'hex')` wrapped in try-catch to handle invalid hex input.
- **WS handler signatures**: `onOpen`, `onMessage`, `onClose` have typed parameters (`Event`, `MessageEvent<WSMessageReceive>`, `CloseEvent`).
- **WS message parsing**: `onMessage` parses `event.data` as JSON automatically.
- **STREAM handler structure**: Event handlers wrapped in `on()` callbacks with proper `eventData` typing instead of bare conditional blocks.
- **STREAM path params**: Extracted at handler start and marked as declared in context.
- **Rate limit store**: Confirmed at module scope (not per-request) with periodic cleanup.
- **Graceful shutdown**: SIGTERM/SIGINT handlers close server and drain DB pool.
- **`boundVars` in expression resolution**: General `Ident` case checks `boundVars` for variable aliasing (e.g., `event` to `eventData`).

### Fixed

- **57 TypeScript errors reduced to 0**: All 5 examples (hello-world, todo-api, auth-service, ecommerce-api, realtime-chat) pass `tsc --noEmit` cleanly.
- **`as any` removed**: Status codes, `BpError.statusCode`, and `c.get()` no longer use `as any` casts.
- **WebSocket extra paren**: Fixed `}))))` to `})))` in WS route generation.
- **`upgradeWebSocket` import**: No longer imports non-existent runtime export from `hono/ws`; uses factory pattern with `@hono/node-ws` instead.
- **`sender` undefined in WS**: `inject X as Y` in WS context generates variable assignment instead of `c.set()`.
- **`room(id)` not callable**: WS builtins resolve `room(id)` pattern to just `id` instead of calling the variable as a function.
- **`newMessage` undefined**: `emit new_message` now passes event name as string `"new_message"`.
- **`roomId` undefined in STREAM**: Event conditions prefix undeclared identifiers with `eventData.`.
- **STREAM return type**: Removed bare `return` before `writeSSE` that caused dead code and type mismatch.
- **Missing events lib**: Events lib now generated for STREAM/WS usage, not just `subscribe` blocks.
- **`room` stub function**: Model names no longer generate TODO stubs in import collector.

## [0.2.0] - 2025-12-15

### Added

- Production readiness overhaul: rate limiting, graceful shutdown, health endpoint
- `bp eject` command to remove generated headers
- "Did you mean?" suggestions for unknown identifiers
- Deterministic output via sorted map iterations
- Fuzz tests, CLI integration tests, golden file tests
- CI: race detection, coverage, tsc validation for all 5 examples

## [0.1.0] - 2025-11-01

### Added

- Initial release: lexer, parser, checker, JS codegen
- Blueprint DSL with models, endpoints, middleware, functions, pipes
- Hono + Drizzle + Zod code generation
- 5 example applications
