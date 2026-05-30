# Codegen Targets

Blueprint compiles to one of two targets today. Same `.bp` source, different runtimes.

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

## Choosing a target

- Existing TS/Node infra, want zero-Docker tests, want a generated React Query client SDK → **node**.
- Existing Python infra, want `uv` + Pydantic + Alembic → **python**.
- Want both → run `bp build` twice with different `--out` and `--target`.

## Per-command target flag

`--target` is parsed by: `bp build`, `bp diff`, `bp migrate`, `bp deploy` (currently `docker|fly`, where `fly` exits cleanly as "not implemented; v0.11"). `bp check`, `bp lint`, `bp fmt`, `bp docs` (OpenAPI) are target-agnostic — they operate on `.bp` source only.

## See also

- `bp context codegen` — what each target writes
- `bp context cli` — flags + flows
