# Generated Output Shape

What `bp build` writes to `--out`. Same `.bp` source produces (mostly) the same logical project across targets.

## Node target (default)

```
out/
  package.json              Hono + Drizzle + Zod + Pino, bun-friendly
  tsconfig.json
  drizzle.config.ts         (when database is declared)
  vitest.config.ts          (when --gen-tests)
  .env.example
  Dockerfile                multi-stage, non-root
  .blueprint/manifest.json  sha256 per generated file
  src/
    index.ts                Hono app, graceful shutdown
    lib/
      db.ts                 Drizzle client over node-postgres
      env.ts                Zod-validated env
      errors.ts             BpError class
      cache.ts              (when cache: redis)
      events.ts             (when stream/ws)
    models/
      schema.ts             Drizzle pg-core tables
    routes/
      <resource>.ts         one file per /api/<resource>; Hono<{ Variables: {...} }>
    validation/
      schemas.ts            Zod schemas per endpoint input
    middleware/
      <name>.ts             FastAPI-style Hono middleware
    functions/
      <name>.ts             generated wrapper
    impl/
      functions/<...>.ts    UserOwned scaffold; you implement
    types/
      api.ts, client.ts, schemas.ts
  test/                     (when --gen-tests)
    _harness/{ddl,db,setup}.ts
    generated/<resource>.test.ts
```

## Python target

```
out/
  pyproject.toml            uv-managed, FastAPI + SQLAlchemy 2.0 + Pydantic v2
  alembic.ini               (when database)
  alembic/
    env.py, script.py.mako, versions/
  src/
    app.py                  FastAPI app
    lib/
      db.py                 SQLAlchemy engine + get_db dependency
      env.py                Pydantic Settings v2
      cache.py              (when cache: redis)
    models/
      schema.py             SQLAlchemy Mapped[...] = mapped_column(...) classes
      pydantic.py           Pydantic models (from_attributes=True)
    routes/
      <resource>.py         APIRouter; sync handlers when touching DB, async otherwise
      <resource>_stream.py  (stream endpoints; sse-starlette)
      <resource>_ws.py      (ws endpoints; FastAPI WebSocket)
    middleware/
      <name>.py             FastAPI dependency function
    functions/
      <name>.py             generated wrapper
    impl/
      functions/<...>.py    UserOwned scaffold; you implement
  tests/                    (when --gen-tests)
    conftest.py             session-scoped testcontainers Postgres
    test_<resource>.py      one per route group
```

## Manifest and re-runs

`.blueprint/manifest.json` is the source of truth for "what files this build owns." On the next build:

- Files in the new build set → write/overwrite.
- Files in the previous manifest but NOT in the new set → delete from disk (clean cut).
- Files NOT in either manifest (e.g. `node_modules/`, `.env`, user-added) → leave alone.
- Files marked `UserOwned: true` (e.g. `src/impl/functions/<...>.ts`) → scaffold once, never overwrite.

This is what makes `bp diff --exit-code` honest: only manifest-tracked paths are considered.

## See also

- `bp context targets` — choosing node or python
- `bp context cli` — `bp build` flags
- `bp context examples` — what each example's output looks like
