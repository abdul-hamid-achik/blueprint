# Codegen Targets

Blueprint compiles to one of three targets. Same `.bp` source, different
runtimes: `node` (default) and `python` are full backends; `effect` is an early,
opt-in scaffold.

## `--target node` (default)

- **Runtime**: Bun or Node.js
- **Framework**: Hono (HTTP), Drizzle ORM (database), Zod (validation), Pino (logging)
- **Package manager**: bun (primary) or npm
- **Test harness**: Vitest + PGlite (in-memory Postgres for self-contained `bp test`)
- **Migrations**: `bunx drizzle-kit generate|push|check`
- **Status**: 5/5 examples compile + run; 5/5 type-check under `tsc --noEmit`

## `--target python`

- **Runtime**: Python 3.11+
- **Framework**: FastAPI (HTTP), SQLAlchemy 2.0 sync (database), Pydantic v2 (validation, settings), sse-starlette (SSE)
- **Package manager**: uv
- **Test harness**: pytest + `testcontainers[postgresql]>=4.0` (real Postgres in a Docker container)
- **Migrations**: `uv run alembic revision --autogenerate -m "..."` / `alembic upgrade head`
- **Status**: 5/5 examples compile; runtime correctness verified for hello-world + todo-api; auth-service, ecommerce-api, realtime-chat run with the three v0.10 runtime fixes (`.count`, `delete <collection>`, `map ... save M`). Streams/WS bodies emit as commented pseudo-code for now (handshake + dispatch are wired).

## `--target effect` (experimental scaffold)

- **Runtime**: Node.js
- **Framework**: Effect (effect-ts) — `@effect/platform` HttpApi (routing), Effect `Schema` (validation + serialization), `@effect/sql` (database, `withTransaction`), `Config` (secrets), Layers (dependency injection)
- **Status**: EARLY SCAFFOLD. `bp build --target effect` emits the project shell + a `Config` module for the spec's secrets, and reports the constructs it can't emit yet with a clear message (model/endpoint emit is in design). Opt-in/experimental — **not** the default. Supported by `bp build` and `bp diff` only.
- **Why**: typed errors, DI sandboxes, and `Schema → JSON-Schema` make every endpoint trivially exposable as an agent/MCP tool. See the contract in `docs/multi-target-codegen.md`.

## Choosing a target

- Existing TS/Node infra, want zero-Docker tests, want a generated React Query client SDK → **node**.
- Existing Python infra, want `uv` + Pydantic + Alembic → **python**.
- Want typed errors / Effect-native code / agent-tool schemas → **effect** (experimental; `node` stays the default).
- Want more than one → run `bp build` per target with different `--out` and `--target`.

## Per-command target flag

`--target` is parsed by: `bp build`, `bp diff`, `bp migrate`, `bp deploy` (currently `docker|fly`, where `fly` exits cleanly as "not implemented; v0.11"). `bp check`, `bp lint`, `bp fmt`, `bp docs` (OpenAPI) are target-agnostic — they operate on `.bp` source only.

## See also

- `bp context codegen` — what each target writes
- `bp context cli` — flags + flows
