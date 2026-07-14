# Generated Output Shape

An unresolved `@>` slot is never part of generated output. Each target rejects
it from `Files` and points back to `bp generate <file> --write`; resolve and
review generation slots before building.

What `bp build` writes to `--out`. Same `.bp` source produces (mostly) the same logical project across targets.

Node and Python share database activation semantics: any model implies the full
Postgres dependencies/env/schema/migration layer, while an explicit database
with zero models emits an importable empty schema/Base.

## Node target (default)

```
out/
  package.json              Hono + Drizzle + Zod, bun-friendly
  tsconfig.json
  drizzle.config.ts         (when database or any model is declared)
  vitest.config.ts          (when --gen-tests or --gen-property-tests)
  .env.example
  Dockerfile                multi-stage, non-root
  .blueprint/manifest.json  sha256 per generated file
  src/
    index.ts                Hono app, graceful shutdown
    lib/
      db.ts                 Drizzle client over node-postgres
      env.ts                Zod-validated env
      errors.ts             BpError class
      external.ts           External fetch/auth/retry helper (when declared)
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
    saves/<...>.ts          UserOwned save-migration scaffold, when declared
    types/
      api.ts, client.ts, schemas.ts
  test/                     (when --gen-tests)
    _harness/{ddl,db,setup}.ts
    generated/<resource>.test.ts
    generated/<resource>.property.test.ts (when --gen-property-tests)
```

`--gen-property-tests` is Node-only and implies `--gen-tests`. It adds
fast-check properties with stable method/path seeds and 32 valid-request runs,
resetting PGlite for each run when a database is active. It returns no files if
any route needs unsupported credentials/headers/input transport, has impossible
path/email/URL constraints, writes a ref-backed field through
`save`/`seed`/`update`, reaches a recursive inline `fn`/`pipe` graph, or uses
native code, external state, queues/storage/events/realtime, sleep, or
wall-clock behavior.

Node also materializes pure, read-only `computed` model fields and one-level
`query ... with(relation)` LEFT JOINs derived from
`<relation>_id ref(target)`. Aliases, self/repeated-target joins, nested loading,
and `fetch ... with(...)` reject. Python and Effect reject both slices.

## Python target

```
out/
  pyproject.toml            uv-managed, FastAPI + SQLAlchemy 2.0 + Pydantic v2
  alembic.ini               (when database or any model is declared)
  alembic/
    env.py, script.py.mako, versions/
  src/
    app.py                  FastAPI app
    lib/
      db.py                 SQLAlchemy engine + get_db dependency (database/model)
      env.py                Pydantic Settings v2
      cache.py              (when cache: redis)
    models/
      schema.py             SQLAlchemy Mapped[...] = mapped_column(...) classes
      pydantic.py           Pydantic models (from_attributes=True)
    routes/
      <resource>.py         APIRouter; sync handlers when touching DB, async otherwise
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

Python codegen fails before returning files for STREAM/WS declarations,
computed fields, relationship `with(...)`, and other unsupported semantics;
the tree above contains only fully translated
surfaces. GET/DELETE inputs use FastAPI `Query`, POST/PUT/PATCH inputs use
embedded `Body`, and direct `header.X-Name` access generates an aliased `Header`
parameter. `env.FIELD` must resolve to a declared secret or generated infra
setting. Middleware currently supports only
`fetch`/`log`/declared-fn/`inject`/`guard` steps; richer bodies fail closed.
Bare `where(q)` is text search only for a string/text endpoint input, not a
dynamic filter accumulator. Field access on JSON/map endpoint inputs or
JSON-returning function results is rejected until `payload.X` can translate to
`payload["X"]`. Direct header/env expressions are supported; raw interpolation
of header/env or dictionary-backed values fails closed.

## Manifest and re-runs

`.blueprint/manifest.json` is the source of truth for "what files this build owns." On the next build:

- Files in the new build set → write/overwrite.
- Files in the previous manifest but NOT in the new set → delete from disk (clean cut).
- Files NOT in either manifest (e.g. `node_modules/`, `.env`, user-added) → leave alone.
- Files marked `UserOwned: true` (for example function implementations and
  save-migration hooks) → scaffold once, never overwrite.

This is what makes `bp diff --exit-code` honest: only manifest-tracked paths are considered.

## See also

- `bp context targets` — choosing node or python
- `bp context cli` — `bp build` flags
- `bp context examples` — what each example's output looks like
