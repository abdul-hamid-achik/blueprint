# Codegen Targets

One `.bp` source, three possible outputs. `node` is the reference target and the default. `python` is a beta with a fail-closed subset. `effect` is an experimental scaffold that emits only a health endpoint and config.

## `--target node` (default)

- **Stack**: Hono (HTTP), Drizzle (database), Zod (validation). Bun or npm.
- **Tests**: `bp test` runs Vitest with PGlite (in-memory Postgres). No Docker needed. Generated projects pin `vitest ^5.0.5`.
- **Path inputs**: every declared `:param` is validated against its type and constraints (`min`/`max`, enum, `int`/`float`/`bool`/`timestamp`/`uuid`). A bad segment answers `400` before the handler runs.
- **Formats**: `format(email|url|uuid|ip|date)` is enforced with Zod.
- **Realtime**: STREAM and WS endpoints exist only here. Variables injected by `use` middleware are typed in STREAM and WS route files (`c.get('auth')` has the row type).
- **Optional**: `--gen-property-tests` adds deterministic fast-check suites. Unsupported routes fail the whole build rather than being skipped.
- **Also here**: computed model fields and one-level `query ... with(relation)` joins.

## `--target python` (beta)

- **Stack**: FastAPI, SQLAlchemy 2.0, Pydantic v2, Alembic. Python 3.11+, managed with `uv`.
- **Tests**: `bp test --target python` runs pytest against a Postgres testcontainer (Docker required when the spec declares a database).
- **Path inputs**: `min`/`max` become FastAPI `Path(...)` bounds. A violation answers `422`, not Node's `400`.
- **Formats**: `format(email|url|uuid|ip|date)` on string inputs and model fields is enforced through a generated stdlib-only `src/lib/formats.py`. A violation answers `422`.
- **Query and body**: GET/DELETE inputs are `Query`, POST/PUT/PATCH inputs are `Body`, `header.X-Name` is an aliased `Header`.
- **Rejected**: STREAM and WS endpoints; endpoint `timeout` metadata, `auth webhook_sig`, and `auth` without `using(secret.NAME)`; blueprint-level `use` middleware; struct `enum` declarations (variants with bodies); model fields typed by a `type` declaration; `env` declarations and `env.X` reads outside endpoint/middleware bodies; computed fields and `query ... with(...)`; `fn logic` bodies and `middleware after` bodies; `enqueue` steps; `worker`/`schedule`/`subscribe`/`external`/`save`/`state`/`analytics`/`content`/`translation`/`locale` declarations; the blueprint `storage` entry, a `cache` backend other than `redis`, or a `database` other than `postgres`; authored `test` blocks and fixtures; `--gen-property-tests`; and middleware steps other than `fetch`/`log`/`fn`/`inject`/`guard`.
- **Check before relying on it**: `bp build --target python` lists every reason it refuses (exit 4). Example specs: `todo-api` and `hello-world` build; `auth-service`, `ecommerce-api`, and `realtime-chat` do not. Cached (`cache`) and rate-limited (`limit`) routes work on Python; `auth bearer|jwt|api_key|basic using(secret.NAME)` routes are enforced on Python REST; do not promise Python output for `auth webhook_sig` routes.

## `--target effect` (experimental scaffold)

- **Emits**: a runnable `GET /health`, typed config for declared secrets and env defaults, `.env.example`, and pinned dependencies.
- **Does not emit** models, endpoints, or tests. Unsupported semantics return no files.
- **Commands**: `bp build` and `bp diff` only. `--gen-tests` is rejected. `bp test` and `bp migrate` reject it.

## Parity summary

| Feature | node | python | effect |
|---|---|---|---|
| Path validation | 400 | 422 | n/a |
| `format(...)` | Zod | `formats.py` (422) | n/a |
| STREAM / WS | yes | rejected | rejected |
| Typed `use` variables in STREAM/WS | yes | n/a | n/a |
| Computed fields, `with(...)` | yes | rejected | rejected |
| Authored `test` blocks | Vitest | rejected | rejected |
| Property tests | yes | rejected | rejected |

## Choosing a target

- TypeScript infra, zero-Docker tests, realtime, or property tests: **node**.
- Python infra with `uv`, Pydantic, and Alembic, for the HTTP/data subset: **python**.
- Typed-error or agent-tool experiments: **effect** (scaffold only).

## Target flags by command

- `bp build`, `bp diff`: node, python, effect.
- `bp test`, `bp migrate`: node, python. Effect is rejected with a clear error.
- `bp deploy`: its own `--target docker|fly`. `fly` is not implemented.
- `bp check`, `bp lint`, `bp fmt`, `bp docs`: target-agnostic.

## See also

- `bp context codegen` — what each target writes
- `bp context cli` — flags and flows
