# Backlog

A tickable tracker for in-flight and planned work. Narrative rationale lives in
[docs/roadmap.md](./docs/roadmap.md); shipped changes go in [CHANGELOG.md](./CHANGELOG.md).

Status legend: `[ ]` todo · `[~]` in progress · `[x]` done (move to CHANGELOG on release)

---

## Now

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
  `TestPrint_DataOpWithMarkers`) cover the shapes + idempotency. `examples/hello-world.bp`
  remains the only `bp fmt --check`-clean file; the four others (auth-service,
  ecommerce-api, realtime-chat, todo-api) now diverge only on **column alignment** of
  model fields / input statements and on **blank lines between sections** / inline-vs-
  multiline `BlockExpr` rendering. _(shipped — see CHANGELOG)_
- [ ] **`bp fmt` polish — column alignment (continuation).** Outstanding work for the
  remaining 4 examples: align `name type constraints` columns within `model { ... }`,
  align `<- name type ...` columns within an endpoint inputs group, preserve blank-line
  separators between meta / inputs / steps / outputs, and re-render small `BlockExpr`s
  multi-line + column-aligned when the source was that way. Tricky because the printer
  currently emits a stream of tokens without a two-pass width computation; needs either
  a structural width-pre-pass or a column-aware writer. Once done, extend the CI step
  in `.github/workflows/test.yml` "Format check examples" to gate all 5 examples.
  Touch: `internal/ast/printer.go`.

- [x] `bp explain <code>` command — embeds `internal/diag/error-codes.md`, prints the
  matching section, drift-tested against `docs/error-codes.md`. Coverage grown to 12
  sites (C001–C012). _(shipped — see CHANGELOG)_
- [ ] `internal/codegen/common` — extract pure-string helpers (`toCamelCase`, `toSnakeCase`,
  `toPascalCase`, `pluralize`, `extractResource`, `extractPathParams`, `isPathParam`) so the
  upcoming Python target can reuse them. JS codegen continues to use them via the new package.
- [ ] `--target` flag scaffolding on `bp build`/`test`/`diff` (default `node`; no new
  generator yet, just plumbing) so the Python work next session can slot in cleanly.

- [~] Reconcile roadmap P0 vs CHANGELOG drift (rate-limit store, etc.) — verify each P0 against code.
  _(2026-06-16 audit: several P0s below were already shipped; corrected here.)_
- [x] Inline enum codegen emits `pgEnum` instead of falling back to `text` (roadmap P0).
  _Verified 2026-06-16: `internal/codegen/js/schema.go` emits `pgEnum` for inline
  `enum(...)` model fields and the column uses it (`orderStatusEnum('status')`),
  no `text()` fallback._
- [x] `version` should be `var` not `const` so GoReleaser ldflags inject it (roadmap P0).
  _Verified 2026-06-16: `cmd/bp/main.go:33` is already `var version = ...` and
  `.goreleaser.yaml` injects `-X main.version`._
- [ ] Wire generated workers into `src/index.ts` startup (roadmap P0).
- [ ] Harden STREAM/WS transport codegen out of preview (roadmap P0).

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
  (emits project shell + `Config`-based secrets; gates models/endpoints with a
  clear message), wired into `build`/`diff` dispatch. Proves the `Files()`
  contract accepts a 3rd target. Hand-written reference + go/no-go captured in
  the maintainer's review notes (Obsidian). **Verdict: opt-in/experimental,
  ~6-8wk MVP, not default.**
  _(shipped 2026-06-16)_
- [ ] **`bp import` — scaffolder only, with loud TODO stubs (decision).** The
  2026-06-16 empirical experiment (captured in the Obsidian review notes) ported
  real handler archetypes: logic survival 25-55%, and **every** port passed
  `bp check` while silently diverging (data leaks, accepting revoked tokens,
  wrong totals, mishandled card declines). Conclusion: a faithful importer is
  not viable; if built, it must be a scaffolder that emits `@ "TODO"` stubs
  loudly and never claims the dropped logic was preserved. `bp check` is NOT a
  sufficient import-validity gate. _(experiment done 2026-06-16)_

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
- [ ] `docs/codegen-targets.md` — contract every generator must satisfy.
- [~] `internal/codegen/python/` — `Generator` impl:
  - [x] Phase 1 (shipped): FastAPI routes for static endpoints, Pydantic Settings env,
    `pyproject.toml` for `uv`, README with run instructions. End-to-end verified
    on `examples/hello-world.bp`; CI smoke gate added.
  - [x] Phase 2 (shipped): SQLAlchemy 2.0 schema + Pydantic v2 read models + sync
    `db.py` session + full Alembic skeleton from `model` declarations. `database`
    and `model` dropped from the unsupported list. Verified imports + table
    registration under `uv`. _(see CHANGELOG)_
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
    - [ ] `where(...)` with non-`==` predicates (`>=`, `<`, `!=`, text-search
      `where(q)`, `or`, `in`)
    - [x] Step calls to user-defined `fn` names — shipped in Phase 3d/5 via
      generated wrapper + user-owned scaffold (`raise NotImplementedError`).
    - [x] `|> total = sum(...)` aggregate — shipped (rewrites to
      `sum(r.x for r in <coll>)`).
    - [ ] Bare-expression step bindings of pure block literals
      (`|> filters = { ... }`)
    - [ ] `pipe` declarations (vs `fn`) under Python `impl python` mode
    - [ ] Partial-commit handling in `try`/`recover`: today `db.commit()` calls
      inside the try body survive the recover branch; a savepoint wrapper
      would make rollback automatic. Mirror Drizzle's transaction semantics.
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
    - [x] `middleware` declarations → FastAPI dependencies with
      alias-and-model injection. _(shipped — Phase 3d/5)_
    - [x] `fn` declarations with `impl <strategy> { module, func }` →
      generated wrapper + user-owned scaffold; step calls dispatch. _(shipped — Phase 3d/5)_
    - [ ] `bp test --target python` wraps `uv run pytest` (today users
      invoke `pytest` themselves after `bp build --gen-tests`).
  - [x] Phase 5 (shipped): STREAM endpoints → FastAPI
    `EventSourceResponse`; WS endpoints → `@router.websocket(...)`;
    `cache redis` → `src/lib/cache.py` with Redis client. realtime-chat
    compiles; coverage hits 5/5.
  - [ ] Phase 5b: full STREAM/WS body translation (Redis pub/sub
    backbone, `broadcast`/`join`/`leave`/`emit` builtins, `where(...)` filter
    wiring), `pipe` declarations under `impl python`, `worker` + `schedule`
    via APScheduler, `storage s3` → boto3, `payload.X` → `payload["X"]`
    for fn returns typed `json`, structured `log`, partial-commit rollback
    in `try`/`recover`, non-`==` `where` predicates.

## Later

- [ ] Multi-target codegen (Python/FastAPI) on top of the typed IR.
- [ ] LSP feature depth: diagnostics, go-to-def, autocomplete, hover from `@` intents.
- [ ] Behavioral test generation (property-based / fuzz) beyond contract tests.
- [ ] Relationships/joins syntax (`with(author)`), computed fields.

---

_Started 2026-05-27._
