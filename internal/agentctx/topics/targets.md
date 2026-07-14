# Codegen Targets

Blueprint compiles to one of three targets. Same `.bp` source, different
runtimes: `node` is the reference backend, `python` is an advanced beta, and
`effect` is an early, opt-in health/config scaffold.

## `--target node` (default)

- **Runtime**: Bun or Node.js
- **Framework**: Hono (HTTP), Drizzle ORM (database), Zod (validation)
- **Package manager**: bun (primary) or npm
- **Test harness**: Vitest + PGlite (in-memory Postgres for self-contained `bp test`)
- **Optional properties**: `--gen-property-tests` adds deterministic fast-check
  valid-request suites and implies contract tests; unsupported/non-hermetic
  routes fail closed, including impossible path/email/URL domains, ref-backed
  writes, and reachable recursive inline call graphs.
- **Migrations**: `bunx drizzle-kit generate|push|check`
- **Status**: all 5 examples plus the feature-complete fixture build and
  type-check under `tsc --noEmit` in CI. Generated test files are smoke-checked
  there, but the full Vitest runner remains a separate release/manual gate.
  `bp test` preflights unsupported authored-test surfaces before build/install:
  setup is only `seed`/`save` plus simple unbound `log`; request body/auth keys
  are lowercase and unique, repeats are positive, GET/HEAD bodies and
  calls/interpolation reject; assertions use strict executable forms/literals
  with at most one model assertion. Native checks cover every app-loaded
  route/realtime/background/subscription/middleware dependency.
  Any model activates the complete Postgres/Drizzle layer; database-only
  projects emit an importable empty schema.
  Pure, read-only computed fields and one-level `query ... with(relation)` LEFT
  JOINs ship on this target; aliases/self/repeated-target/nested joins reject.

## `--target python`

- **Runtime**: Python 3.11+
- **Framework**: FastAPI (HTTP), SQLAlchemy 2.0 sync (database), Pydantic v2 (validation, settings)
- **Package manager**: uv
- **Test harness**: pytest + `testcontainers[postgresql]>=4.0` (real Postgres in a Docker container)
- **Migrations**: `uv run alembic revision --autogenerate -m "..."` / `alembic upgrade head`
- **Status**: supported HTTP/data examples compile and generated contract tests run through `bp test --target python`. GET/DELETE inputs use FastAPI `Query`, POST/PUT/PATCH inputs use embedded `Body`, hyphenated headers get aliased `Header` parameters, and the shared Node/Python database-activation rule applies. The exhaustive support gate rejects authored tests/fixtures, inline fn logic, STREAM/WS handlers, computed model fields, `query ... with(...)`, named types/enums/aliases, env declarations, file/named endpoint inputs, undeclared env fields, attribute access on JSON/map inputs or JSON-returning function results, unknown value calls, unsafe fn impl configuration, mismatched defaults/constraints, Python-keyword names, dynamic/bound `where(q)` values, and middleware steps outside `fetch`/`log`/declared-fn/`inject`/`guard` before files are returned. Bare `where(q)` is supported only for string/text endpoint inputs. Property-test mode is Node-only and is rejected at CLI dispatch.

## `--target effect` (experimental health/config scaffold)

- **Runtime**: Node.js
- **Current stack**: pinned Effect core `Config` plus Node HTTP; `@effect/platform` HttpApi, Schema, Layers, and `@effect/sql` are future application slices
- **Status**: EARLY SCAFFOLD. `bp build --target effect` emits a runnable `GET /health` entrypoint, typed config for required/optional secrets and supported env defaults, `.env.example`, and pinned dependencies. It audits blueprint settings, uses, declarations, and env expressions and returns no files when semantics are unsupported. Model/endpoint emit remains in design. Opt-in/experimental — **not** the default. Supported by `bp build` and `bp diff` only; both reject `--gen-tests`, and `bp test`/`bp migrate` reject Effect before writing.
- **Why**: typed errors, DI sandboxes, and `Schema → JSON-Schema` make every endpoint trivially exposable as an agent/MCP tool. See the contract in `docs/multi-target-codegen.md`.

## Choosing a target

- Existing TS/Node infra, want zero-Docker tests, want a generated React Query client SDK → **node**.
- Existing Python infra, want `uv` + Pydantic + Alembic → **python**.
- Want typed errors / Effect-native code / agent-tool schemas → **effect** (experimental; `node` stays the default).
- Want more than one → run `bp build` per target with different `--out` and `--target`.

## Per-command target flag

`--target` is parsed by: `bp build`, `bp diff`, `bp test` (node/python only), `bp migrate` (node/python only — `effect` has no migration tooling and is rejected with a clear error), and `bp deploy` (a separate `docker|fly` deploy target, where `fly` exits cleanly as "not yet implemented"). `bp check`, `bp lint`, `bp fmt`, and `bp docs` (OpenAPI) are target-agnostic — they operate on `.bp` source only.

## See also

- `bp context codegen` — what each target writes
- `bp context cli` — flags + flows
