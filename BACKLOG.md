# Backlog

A tickable tracker for in-flight and planned work. Narrative rationale lives in
[docs/roadmap.md](./docs/roadmap.md); shipped changes go in [CHANGELOG.md](./CHANGELOG.md).

Status legend: `[ ]` todo · `[~]` in progress · `[x]` done (move to CHANGELOG on release)

---

## Now

- [ ] **Eliminate the remaining Drizzle Kit development-tool advisories.** The
  generated production dependency graph is clean and CI rejects high/critical
  findings, but current Drizzle Kit `0.31.10` still brings four moderate
  `@esbuild-kit`/esbuild advisories. npm's suggested downgrade to `0.18.1` is
  not compatible with the current Drizzle schema/tooling contract; move to a
  patched upstream line (or isolate the migration CLI) when one is available.
- [~] **Complete worker cancellation and terminal-failure semantics.** REST
  endpoint producers now resolve exactly one worker, translate `retry N` to
  BullMQ `attempts: N + 1`, propagate fixed/exponential backoff, enforce `max`
  through a generated custom strategy, and reject malformed, missing,
  ambiguous, or unsupported producer contexts before returning files. Normal
  processor failures compensate through `on_fail` only on the final declared
  attempt. Remaining: define cooperative cancellation instead of the current
  `Promise.race` timeout, and cover terminal stall exhaustion or early
  `UnrecoverableError` paths that bypass the processor catch. _(retry/backoff
  slice shipped 2026-07-14)_
- [~] **Checker soundness.** Unbound/use-before-bind names, unknown data models,
  invalid lexical-scope leaks, accidental runtime globals, interpolation roots,
  duplicate declarations/local bindings/inputs, callable arity, reserved
  builtin names, direct model-field typos, and duplicate enum variants now fail
  with source diagnostics. Duplicate blueprint settings, non-string versions,
  invalid ports, and malformed runtime/database/cache/storage values also fail
  before generators can fall back to defaults. Valid query fields, explicit
  REST/realtime path values, local middleware injections, input reassignment,
  and implicit map results remain accepted. Conservative primitive/enum/model/
  list/map/optional/null assignability now covers input reassignments,
  declared function and pipe arguments, known model writes, optional
  `fetch`/`query ... first` results, and truthy-guard narrowing. Remaining:
  deeper nested JSON/FK leaf typing, composite function-output recovery, and
  broader expression/operator assignability. _(partially shipped 2026-07-14)_
- [x] **Fail closed when a target cannot preserve semantics.** Effect rejects
  every unimplemented top-level AST kind. Python now uses an exhaustive
  declaration allowlist, rejects authored tests/fixtures, inline `fn logic`,
  STREAM/WS handler bodies, unsupported blueprint/endpoint metadata and input
  types, and middleware configuration/`after` bodies, and translates every
  currently parsed expression literal instead of emitting TODO fallbacks.
  It also rejects undeclared env access and attribute access on JSON/map
  endpoint inputs or JSON-returning function results, plus unknown value calls,
  unsafe native implementation configuration, mismatched defaults/constraints,
  Python-keyword generated names, and bare `where(q)` values that are not
  string/text endpoint inputs (dynamic filter accumulators are not translated
  yet). Raw interpolation of `header.X`, `env.X`, or dictionary-backed values
  is rejected even though direct header/env expressions are supported. Invalid
  `sum(...)` shapes fail before files are returned. _(shipped
  2026-07-14)_
- [x] **Keep database activation consistent across targets.** On both Node and
  Python, declaring any model now activates the complete Postgres dependency,
  env, schema, and migration layer even when `database` is omitted. An explicit
  `database postgres` with zero models emits an importable empty schema/Base
  instead of broken migration imports. _(shipped 2026-07-14)_
- [x] **External auth/retry correctness.** Node external services now accept
  `bearer`/`jwt`/`basic`/`api_key` auth with exactly one declared
  `secret.NAME` or `env.NAME`, emit the appropriate env-backed request header,
  and reject malformed or undeclared credentials before returning files.
  `retry N` means N additional immediate attempts, each with a fresh timeout,
  for network/timeout failures and HTTP 408/429/5xx; other 4xx responses are not
  retried. Compile-shape and mocked-fetch runtime tests cover the boundary.
  _(shipped 2026-07-14)_
- [x] **Reject unresolved `@>` slots during ordinary builds.** Every generator's
  `Files` entrypoint now returns an actionable error before emitting files and
  points to `bp generate --write`; `bp build` exits with codegen status 4.
  Shared AST walking now includes test-group setup and fixture bodies.
  _(shipped 2026-07-14)_
- [x] **Reject `subscribe ... from(service)` until a transport exists.** Node
  codegen now returns an actionable error and no files when `from(service)` is
  present. Source-less `subscribe "event" { ... }` remains the supported
  in-process event-bus form. _(shipped 2026-07-14)_
- [x] **Preflight unsupported authored Node tests before running.** `bp test`
  now accepts only executable emitter shapes before build/install/Vitest: setup
  is `seed`/`save` plus simple unbound `log`; request entries are unique lowercase
  `body`/`auth`, repeats are positive, GET/HEAD bodies and calls/interpolation
  reject; assertion syntax/literals are strict and only one model assertion is
  allowed. Dynamic targets, cleanup/shared setup, custom or multipart/file
  requests, and unsupported auth/timing forms also reject. Native preflight
  covers dependencies from every app-loaded route, realtime/background/
  subscription, and middleware module, not only the test target. Ordinary
  `bp build` remains available for hand-written Vitest workflows. _(shipped
  2026-07-14)_
- [x] **Save migration hooks are user-owned.** `src/saves/*.ts` implementation
  scaffolds now set `UserOwned: true`, state their ownership in the header, and
  have a two-build regression proving developer edits survive and stay out of
  the manifest. _(shipped 2026-07-14)_
- [x] **`bp test --target python`.** Builds the generated pytest + Postgres
  testcontainers suite and runs it through `uv run pytest`; Effect is rejected
  before writing output. _(shipped 2026-07-14)_
- [~] **Typed IR / resolve pass.** New `internal/resolve` package; slice 1 (per-block
  variable facts: model + cardinality `Single`/`Collection`/`Paginated`) shipped. Codegen
  reads pre-resolved facts instead of incrementally bookkeeping during emit. Remaining slices:
  - [x] Slice 2: FK access resolution into the resolver. _(shipped)_
  - [x] Slice 3: async-ness migrated to `resolve.AsyncFunctions`. _(shipped)_
  - [x] Slice 4: let/const facts migrated to `resolve.InputsReassignedInSteps`,
    `FetchVarsReassignedByUnboundUpdate`, `VarsWithPropertyMutation`. _(shipped)_
  - [ ] Slice 5: lift the `emitCtx` ad-hoc maps to a single `*resolve.BlockFacts` lookup.
    _Deferred — slices 1–4 already deliver the multi-target value (resolver is a
    real package, JS codegen consumes it, Python codegen calls it directly without
    going through `emitCtx` at all). Slice 5 is internal JS cleanup; revisit if the
    JS ctx ever becomes a porting blocker for a third target._
  _Why: removes the recurring "fix codegen bug" class; makes multi-target codegen cheap._
- [x] **Auto-generated contract tests.** `bp build --gen-tests` emits a Vitest case per endpoint
  that builds a request from the input schema, seeds FK parents, and asserts status + response
  shape. Distinct from authored `test {}` blocks. _(shipped — see CHANGELOG [Unreleased])_
- [x] **Self-contained test setup.** In-memory PGlite harness mocks `src/lib/db`; `bp test` runs
  with no external Postgres. _(shipped)_
- [x] **Unified `bp diff`.** Line-level unified diff per modified file with `--apply`,
  `--exit-code`, color, and manifest noise suppressed. _(shipped)_
- [x] **Add `[Unreleased]` section convention to CHANGELOG.md.** _(shipped)_
- [x] **Production-readiness doc + status table.** `docs/production-readiness.md` with
  5 pillars, measurable gates, and the engineering workflow. _(shipped)_
- [x] **CI: build idempotency check + `--gen-tests` smoke.** Added to
  `.github/workflows/test.yml`. _(shipped)_

## Next

- [x] **`bp fmt` polish — data-op shorthand preserved.** The `FnCall` branch in
  `internal/ast/printer.go` now detects data-op call shapes (`query` / `save` / `fetch` /
  `update` / `delete` / `count` / `seed` with a model `Ident` as the first arg) and emits
  the README idiom (`|> todos = query todo paginate(page, per_page)`,
  `|> todo = fetch todo(id)`, `|> todo = save todo { title: title }`) instead of the
  function-call form. Two new printer tests (`TestPrint_DataOpShorthand`,
  `TestPrint_DataOpWithMarkers`) cover the shapes + idempotency. All five
  canonical examples now pass `bp fmt --check`. _(shipped — see CHANGELOG)_
- [x] **`bp fmt` polish — column alignment.** Model fields, endpoint inputs,
  and blueprint keys use a two-pass width computation; CI gates every
  `examples/*.bp` file. _(shipped v0.14.0)_

- [x] `bp explain <code>` command — embeds `internal/diag/error-codes.md`, prints the
  matching section, drift-tested against `docs/error-codes.md`. Coverage grown to 12
  sites (C001–C012). _(shipped — see CHANGELOG)_
- [x] `internal/codegen/common` — extract pure-string helpers (`toCamelCase`, `toSnakeCase`,
  `toPascalCase`, `pluralize`, `extractResource`, `extractPathParams`, `isPathParam`) for
  reuse across the Node, Python, and future targets.
- [x] `--target` dispatch on `bp build`/`diff` (`node`/`python`/`effect`) and
  `bp test`/`migrate` (`node`/`python`).

- [~] Reconcile roadmap P0 vs CHANGELOG drift (rate-limit store, etc.) — verify each P0 against code.
  _(2026-06-16 audit: several P0s below were already shipped; corrected here.)_
- [x] Inline enum codegen emits `pgEnum` instead of falling back to `text` (roadmap P0).
  _Verified 2026-06-16: `internal/codegen/js/schema.go` emits `pgEnum` for inline
  `enum(...)` model fields and the column uses it (`orderStatusEnum('status')`),
  no `text()` fallback._
- [x] `version` should be `var` not `const` so GoReleaser ldflags inject it (roadmap P0).
  _Verified 2026-06-16: `cmd/bp/main.go:33` is already `var version = ...` and
  `.goreleaser.yaml` injects `-X main.version`._
- [x] Wire generated workers into `src/index.ts` startup (roadmap P0). _(shipped 2026-07-09:
  worker bodies compile — inputs bound, async fns awaited, guard semantics, on_fail scope;
  scheduler consumer Worker added so cron handlers actually run; REDIS_URL/DATABASE_URL
  auto-added to env schema; startup moved inside the VITEST guard; backoff quoting fixed;
  compile gate via all_features.bp worker + worker_basic.bp tests. See CHANGELOG [Unreleased].)_
- [x] **Worker producer path.** Shipped v0.13.0: `enqueue "queue_name" { data }`
  builtin enqueues a job to a BullMQ queue. Lexer/parser/checker/codegen all wired;
  `tsc`-compiled end-to-end. _(shipped 2026-07-10)_
- [x] Harden STREAM/WS transport codegen out of preview (roadmap P0). _(shipped 2026-07-09:
  WS handler crash isolation — a failing guard/bad client JSON no longer kills the process;
  SSE subscriptions unsubscribed on abort; STREAM guards run before the 200 is sent; dead-socket
  eviction + broadcast readyState guard; STREAM auth/limit/use meta enforced, WS bare-auth gets
  linter warning `unenforceable-ws-auth`. See CHANGELOG [Unreleased].)_
- [~] **STREAM/WS remaining tail:** WS rooms now use Redis pub/sub for multi-instance
  broadcast (shipped v0.14.0); STREAM endpoints still use process-local event subscriptions.
  Remaining: backpressure on emit fan-out; STREAM/WS endpoints in all_features.bp; WS
  path-param validation. _(partially shipped 2026-07-10)_

### Found in the 2026-06-16 deep review

_(detailed working notes live in the maintainer's Obsidian vault under
`projects/blueprint/`, not in this repo — `docs/` is the published website.)_

- [x] **Codegen `Generator` interface exposes `[]OutputFile`.** Added
  `Files(*ast.File) ([]OutputFile, error)` to the interface and both targets;
  `Generate` is now a thin `Files` + `WriteOutputFiles` wrapper. Emit logic is
  unit-testable without a temp dir (`TestFilesNoDisk`). De-risks Effect/Go/Ruby.
  Contract written up in `docs/multi-target-codegen.md`. _(shipped 2026-06-16)_
- [x] **`try/recover` DB transaction (JS, Option A).** JS now wraps a `try`
  body in `db.transaction(...)` when it has ≥2 mutations and no in-body
  `guard`/output, so partial writes roll back. `examples/ecommerce-api.bp`
  recover updated; generated TS `tsc`-passes; 2 new tests. **Python still
  open** (savepoint wrapper — see Python "Phase 3d"). _(shipped 2026-06-16)_
- [x] **Duplicate `pgEnum` name dedupe.** `internal/codegen/js/schema.go` now
  registers pgEnums by pg type name; an inline `enum(...)` matching a declared
  enum's variants reuses it (no colliding `CREATE TYPE`), and differing variants
  disambiguate to `<model>_<field>`. Inline-enum emit is now deterministic. 2
  new tests. _(shipped 2026-06-16)_
- [x] **`--target effect` scaffold.** New `internal/codegen/effect/` Generator
  (emits a pinned, runnable health service + typed secret/env `Config`; audits
  blueprint settings and gates models/endpoints/test flags with a clear
  message), wired into `build`/`diff` dispatch. Proves the `Files()`
  contract accepts a 3rd target. Hand-written reference + go/no-go captured in
  the maintainer's review notes (Obsidian). **Verdict: opt-in/experimental,
  ~6-8wk MVP, not default.**
  _(shipped 2026-06-16)_
- [x] **`bp import` — scaffolder only, with loud TODO stubs (decision).** The
  2026-06-16 empirical experiment (captured in the Obsidian review notes) ported
  real handler archetypes: logic survival 25-55%, and **every** port passed
  `bp check` while silently diverging (data leaks, accepting revoked tokens,
  wrong totals, mishandled card declines). Conclusion: a faithful importer is
  not viable; if built, it must be a scaffolder that emits `@ "TODO"` stubs
  loudly and never claims the dropped logic was preserved. `bp check` is NOT a
  sufficient import-validity gate. `bp import [path] --from ts` now extracts
  only static Drizzle `pgTable`/`pgEnum` declarations, Hono route shapes, and
  named Zod objects from static, transport-compatible `zValidator` calls.
  Nullable/type-changing Zod fields skip, while SQL identity loss and dropped
  Drizzle options warn. It never lifts handler bodies: every emitted route
  carries a TODO, returns 501, and appears in the stderr fidelity report;
  unsupported/dynamic structure is skipped with a warning. _(shipped
  2026-07-14)_

### Iteration-3 queue — reviewer-confirmed defects (shipped 2026-07-09)

All 6 must-fix defects + 5 smaller reviewer items + P1 (python where-predicates) + P3 (JS
string interpolation) shipped. See CHANGELOG [Unreleased] "iteration-3 batch".

- [x] **(python, must-fix)** try/recover nested via `when` inside a transaction-wrapped try
  — inner try now gets its own `begin_nested()` savepoint when `ctx.inTxn`, flushes instead
  of committing, suppresses `db.rollback()` (the savepoint handles it).
- [x] **(python, must-fix)** `lastBindingForModel` is position-unaware — bindings are now
  registered incrementally after each step is emitted; the lookup only sees preceding
  declarations.
- [x] **(python, must-fix)** `orderedBindings` omits `facts.MapResults` — map-result bindings
  are now included in the lookup.
- [x] **(js, must-fix)** worker input named `data`/`error` collides with generated params —
  renamed to `_bpData`/`_bpError`.
- [x] **(js, must-fix)** auto-added REDIS_URL/DATABASE_URL not deduped vs user `env` — dedupe
  set seeded with env names before infra vars in both `genEnvTS` and `genEnvExample`.
- [x] **(js, must-fix)** `off()` during `emit()` splices the array — `emit()` now iterates a
  snapshot.
- [x] (js) scheduler consumer queue name collision — namespaced to `__bp_scheduler`.
- [x] (js) WS onOpen close reason >123 bytes — truncated via `err.message.slice(0, 123)`.
- [x] (js) duplicate `const _interval` — indexed as `_interval0`, `_interval1`, ….
- [x] (cli) `--force` not accepted by run/dev/test/migrate — threaded through all
  build-adjacent commands.
- [x] (cli) stale-file removal deletes hand-edits silently — `warnIfStaleRemoved` checks the
  manifest hash before `os.Remove`.
- [x] **(python, P1)** where-predicate comparison ops (`!=`, `<`, `>`, `<=`, `>=`) and `in`
  (SQLAlchemy `.in_`) — `whereConditions` now translates all of them; new
  `testdata/valid/where_predicates.bp` fixture wired into both targets' tests. Duration RHS
  (`N.days.ago` → timedelta) is still deferred.
- [x] **(js, P3)** string-interpolation `{expr}` snake_case identifiers —
  `transformInterpolation` now regex-matches and applies `toCamelCase`.

### Found in the 2026-07-09 audit (not yet fixed)

- [x] **`bp migrate --target effect` silently falls through to the node/drizzle path** —
  rejected explicitly (iteration-2). _(shipped)_
- [x] **JS string-interpolation identifiers not camelCased** — `transformInterpolation` now
  regex-matches snake_case identifiers and applies `toCamelCase`. _(shipped 2026-07-09
  iteration-3)_
- [x] **Stale "fly reserved for v0.11" strings inside the binary** — already fixed: all
  strings now say "reserved for a future release" (iteration-2). _(verified 2026-07-10)_
- [x] **Language-contract truth sync** — the target table reflects reality (node mature /
  python advanced / effect experimental / go planned); `in` predicate in the grammar;
  Appendix D CLI exit codes. _(shipped iteration-2)_
- [x] **Parser: dedicated if/else diagnostic (P002)** — `if`/`else`/`for`/`while`/`switch`
  in step position now produces a clear diagnostic with guard/when/map hints; panic-mode
  cascade suppressed. _(shipped iteration-2)_
- [x] **`bp eject` deletes manifest so rebuilds hit --force guard** — first eject tests
  added (iteration-2). `internal/generate` coverage started (iteration-2). _(partially
  shipped; more coverage welcome)_
- [x] **docs/roadmap.md stale line counts** — verified 2026-07-10: no stale line-count
  references remain in docs/roadmap.md.
- [x] **LSP didChange applies ContentChanges[0] only** — fixed: all entries now applied in
  order (full-document sync, last entry wins); first handler tests added. _(shipped iteration-2)_
- [x] **`bp fmt` printer 0% coverage for worker/schedule/stream/ws/try-recover/test/fixture
  blocks** — `TestPrint_RoundtripAllBlockTypes` exercises all block types via a
  parse → print → parse → print idempotency check. _(shipped 2026-07-10)_

## Python target (decisions locked, work pending)

Stack: **FastAPI + Pydantic v2 + SQLAlchemy + Alembic**. Package manager: **uv**
(generated `pyproject.toml`; `bp run/test/migrate` shell to `uv run` for Python projects, the
way they shell to `bun` for Node today). Generated tests use **testcontainers** for real
Postgres — no in-memory shim (SQLite's dialect drifts too far from Postgres FK/enum/JSON
semantics for the contract tests to be meaningful).

Prep work (must precede the Python generator):

- [x] IR Slice 1 (variable facts) _(shipped)_
- [x] IR Slice 2 (FK access) _(shipped)_
- [x] IR Slice 3 (async-ness) _(shipped)_
- [x] IR Slice 4 (let/const decisions) _(shipped)_
- [x] `internal/codegen/common` — pure-string helpers extracted _(shipped)_
- [x] `--target` flag scaffolding on `bp build`/`bp diff`, default `node`, with python returning "in progress" _(shipped)_
- [x] `docs/codegen-targets.md` — contract every generator must satisfy. _(resolved 2026-07-09:
  the contract shipped as `docs/multi-target-codegen.md` in v0.11.0; that doc now also covers
  the `--gen-tests` builder convention, the exit-code contract, and the per-command target
  dispatch table. No separate file needed.)_
- [~] `internal/codegen/python/` — `Generator` impl:
  - [x] Phase 1 (shipped): FastAPI routes for static endpoints, Pydantic Settings env,
    `pyproject.toml` for `uv`, README with run instructions. End-to-end verified
    on `examples/hello-world.bp`; CI smoke gate added.
  - [x] Phase 2 (shipped): SQLAlchemy 2.0 schema + Pydantic v2 read models + sync
    `db.py` session + full Alembic skeleton from `model` declarations. `database`
    and `model` dropped from the unsupported list. Python participates in the
    shared Node/Python database-activation rule: a model implies the Postgres
    layer, while a database with no models emits an importable empty `Base`.
    Verified imports + table registration under `uv`. _(see CHANGELOG)_
  - [x] Phase 3 (shipped): endpoint bodies with `|>` steps (`save`/`fetch`/`update`/
    `delete`/`query` + optional `paginate`) and `guard` → SQLAlchemy + HTTPException;
    `db: Session = Depends(get_db)` plumbed; sync `def` for DB handlers, `async def`
    for static; outputs via `jsonable_encoder`. `examples/todo-api.bp` compiles
    end-to-end. _(see CHANGELOG)_
  - [x] Phase 3b (shipped): `where(col == val, ...)` predicates with `==` only,
    `first` modifier, and `when` block form.
  - [x] Phase 3c (shipped): every remaining endpoint-body construct.
    - [x] `order(col, asc|desc)` clauses on `query` — emits `.order_by(schema.M.col.asc()/desc())`
    - [x] FK access patterns (`item.product.x`) — uses `resolve.FKAccessesInStmt`
      + a new `fkAliases` map on the body context to dedupe sub-queries
    - [x] `|> when cond: expr` inline form
    - [x] `try`/`recover` -> Python `try:` / `except Exception as error:`
    - [x] `map items: save M { ... }` (bound and unbound) -> explicit for-loop
    - [x] `map items: update M { ... }` (unbound) -> for-loop with per-iteration FK alias
    - [x] `log "msg"` -> `print(f"...")`
  - [ ] Phase 3d: the long tail still rejected with a specific message.
    - [x] `where(...)` comparison ops (`!=`, `<`, `>`, `<=`, `>=`), `in`,
      recursive `or`/`and`, text-search `where(q)`, and duration RHS values —
      shipped across the 2026-07-09 audit and v0.13.0 batch. Explicit `like`
      syntax remains unsupported.
    - [x] Step calls to user-defined `fn` names — shipped in Phase 3d/5 via
      generated wrapper + user-owned scaffold (`raise NotImplementedError`).
    - [x] `|> total = sum(...)` aggregate — shipped (rewrites to
      `sum(r.x for r in <coll>)`).
    - [x] Bare-expression step bindings of pure block literals
      (`|> filters = { ... }`) — shipped v0.14.0 (iteration-3 polish).
    - [ ] `pipe` declarations (vs `fn`) under Python `impl python` mode
    - [x] Partial-commit handling in `try`/`recover` — shipped 2026-07-09: multi-write
      try bodies wrap in `with db.begin_nested():` + per-mutation `db.flush()` + single
      `db.commit()` (same wrap predicate as JS Option A); `db.rollback()` leads every
      DB-touching recover branch; guards re-raise `HTTPException` past the generic except;
      `error.message` → `str(error)`. Also fixed the critical wrong-target `update`/`delete`
      binding resolution (nondeterministic wrong-row writes). See CHANGELOG [Unreleased].
    - [ ] Structured `log` (`level(error)`, JSON output) — Phase 3c keeps deps
      minimal with `print(f"...")`.
  - [~] Phase 4: middleware, fn/pipe (Python `impl python` mode), `bp test`
    against testcontainers for real Postgres.
    - [x] `bp build --target python --gen-tests` emits a runnable pytest
      contract suite backed by testcontainers (`tests/conftest.py` with
      `pg_container`/`engine`/`db`/`client` fixtures + one
      `tests/test_<resource>.py` per route group, lenient status + shape
      assertions, FK-aware seeding). The CLI's previous "not supported"
      guard against `--target python --gen-tests` is gone; `--gen-tests`
      works on both targets. See CHANGELOG [Unreleased].
    - [x] `middleware` declarations → FastAPI dependencies with header aliases
      and a fail-closed `fetch`/`log`/declared-fn/`inject`/`guard` step subset.
      Richer middleware pipelines are rejected until translated. _(shipped —
      Phase 3d/5)_
    - [x] `fn` declarations with `impl <strategy> { module, func }` →
      generated wrapper + user-owned scaffold; step calls dispatch. _(shipped — Phase 3d/5)_
    - [x] `bp test --target python` wraps `uv run pytest`; Docker remains a
      runtime requirement for the generated Postgres testcontainer.
  - [~] Phase 5: `cache redis` → `src/lib/cache.py` with a Redis client is
    shipped. Dormant STREAM/WS routing emitters exist, but public Python
    codegen now rejects those declarations because their event/lifecycle
    bodies were only TODO placeholders; successful builds never imply that
    incomplete handlers execute.
  - [ ] Phase 5b: full STREAM/WS body translation (Redis pub/sub
    backbone, `broadcast`/`join`/`leave`/`emit` builtins, `where(...)` filter
    wiring), `pipe` declarations under `impl python`, `worker` + `schedule`
    via APScheduler, `storage s3` → boto3, `payload.X` → `payload["X"]`
    for fn returns typed `json`, and structured `log`.

## Later

- [~] Multi-target codegen parity: Python/FastAPI is advanced; Effect has a
  fail-closed runnable health/config foundation, with application emit still open.
- [x] LSP feature depth: context-aware completion, local-workspace symbol
  search, and a packaged VS Code language client that starts `bp lsp` are
  shipped. Completion is intentionally syntax/source-context based (no resolve
  round-trip), workspace search scans local `.bp` files while ignoring common
  generated/vendor directories, and rename remains future work. _(shipped
  2026-07-14)_
- [x] Behavioral test generation beyond contract tests: Node-only
  `--gen-property-tests` emits deterministic fast-check suites, implies
  `--gen-tests`, resets PGlite per run when needed, and fails the whole build
  for non-hermetic or unsupported route surfaces instead of skipping them.
  _(shipped 2026-07-14)_
- [x] Relationships/joins syntax and computed fields: Node supports one-level
  `query ... with(author)` LEFT JOINs through `author_id ref(target)` and pure,
  read-only `computed` model fields. Join aliases, self joins, repeated targets,
  and `fetch ... with(...)` reject; Python and Effect reject both slices until
  they have faithful emitters. _(shipped 2026-07-14)_

---

_Started 2026-05-27._
