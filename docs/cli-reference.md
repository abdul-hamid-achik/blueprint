# CLI Reference

All commands follow the pattern: `bp <command> [arguments] [flags]`

**Flag syntax:** flags that take a value (e.g. `--out`, `--target`) accept both
`--flag value` and `--flag=value` forms; boolean flags (e.g. `--json`,
`--force`) take no value in either form. Passing an unrecognized flag is a hard
error (with a "did you mean" hint when a close match exists) instead of being
silently ignored, and a value-flag with nothing usable after it (end of args,
or immediately followed by another `--flag`) is also an error rather than
silently keeping the default. `--help`/`-h` after any command prints that
command's usage and exits `0`.

## `bp check`

Validate a `.bp` file for syntax and semantic errors.

```bash
bp check <file.bp|dir> [more ...] [--json]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | off | Output a machine-readable JSON result (for CI) instead of formatted text |

Several paths, or a directory, switch to multi-file mode. See
[Multiple files and directories](#multiple-files-and-directories) below.

**What it checks:**
- Lexer errors (invalid tokens, malformed literals)
- Parser errors (invalid syntax)
- Semantic errors (undefined names, naming conventions, type mismatches)

**Example:**

```bash
bp check my-service.bp
# OK — no errors

bp check my-service.bp
# my-service.bp:12:5: error: undefined name 'user_service' (did you mean 'auth_service'?)
# my-service.bp:34:3: error: field 'email_address' should be snake_case

bp check my-service.bp --json
# {
#   "filename": "my-service.bp",
#   "success": true
# }
```

**Exit codes:**
- `0` — no errors
- `1` — one or more parse/semantic errors
- `2` — the file could not be read

---

## Multiple files and directories

`bp check`, `bp lint` and `bp fmt` accept more than one path, and any path may
be a directory:

- A directory expands recursively to its `*.bp` files, in sorted order. The
  walk skips `node_modules`, `generated`, `dist`, `.blueprint`, and any
  directory whose name starts with `.`. The directory argument itself is never
  skipped, so `bp check .` works.
- Include fragments are not standalone. A directory-discovered file that another
  file in the same invocation `include`s is skipped. A file you name explicitly
  is always processed, even when it is also included elsewhere.
- Include fragments skipped by the directory walk are not formatted or checked
  by `bp fmt --check`. Name a fragment explicitly to include it.
- Duplicate paths are dropped, keeping the first occurrence.
- Every file is processed. One failure does not stop the others. The exit code
  is the most severe per-file code (the numerically largest from the table in
  [Exit Codes](#exit-codes)).
- A one-line summary goes to stderr, for example `3 files checked, 1 failed`.
  A single regular file prints no summary and keeps its original output.
- A path that does not exist, or a directory with no `.bp` files, is a usage
  error. It exits `2` before any file is processed. A single missing file keeps
  its original file-read error, which also exits `2`.

**JSON for multiple files** (`--json`) is one document that wraps the
single-file objects:

```json
{
  "success": false,
  "file_count": 3,
  "failed_count": 1,
  "files": [
    { "filename": "api/users.bp", "success": true },
    { "filename": "api/orders.bp", "success": false, "error_count": 1, "errors": [ ... ] }
  ]
}
```

Each entry in `files` has the same shape as the single-file `--json` output
for that file, plus a `filename` key when the single-file object lacks one.

---

## `bp build`

Compile a `.bp` file to a runnable project.

```bash
bp build <file.bp> [--out <dir>] [--target <node|python|effect>] [--react-query] [--frontend-only] [--gen-tests] [--gen-property-tests] [--force]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Output directory |
| `--target <name>` | `node` | Codegen target. `node` emits the Hono + Drizzle + Zod reference project. `python` emits FastAPI + SQLAlchemy 2.0 + Pydantic v2 + Alembic (advanced beta — see "Python target status" below). `effect` emits a runnable, health-only TypeScript/Effect scaffold with typed secret/env config and pinned dependencies; authored endpoints/models still reject |
| `--react-query` | off | Generate `src/types/react-query.ts` and add TanStack React Query deps (node target only) |
| `--frontend-only` | off | Emit only the standalone frontend contract package (node target only) |
| `--gen-tests` | off | Generate contract + happy-path tests. **Node target:** Vitest suite under `test/generated/` with an in-memory (PGlite) database harness — runs with no external Postgres. **Python target:** pytest suite under `tests/` backed by `testcontainers[postgresql]` — Docker required, real Postgres per session, function-scoped TRUNCATE between tests. **Effect target:** rejected before writing because no test emitter ships yet |
| `--gen-property-tests` | off | Node only. Generate deterministic fast-check valid-request properties and the contract suite (`--gen-tests` is implied). Unsupported or non-hermetic routes reject the whole build; nothing is silently skipped. Cannot be combined with `--frontend-only` |
| `--force` | off | Overwrite a non-empty `--out` directory even if it has no `.blueprint/manifest.json`. Without it, `bp build` refuses to write into a foreign, non-empty directory it didn't create — see [Output directory safety](#output-directory-safety) below |

### Output directory safety

`bp build` refuses to write into an existing, non-empty `--out` directory that
Blueprint didn't create:

```bash
bp build my-service.bp --out ./some-existing-dir
# Error: output directory "./some-existing-dir" already exists, is not empty
# (e.g. package.json, src), and has no .blueprint/manifest.json — refusing to
# overwrite files bp didn't create. Use --force to proceed anyway, or point
# --out at a fresh/empty directory
```

A directory Blueprint already built into (it carries `.blueprint/manifest.json`)
is always safe to rebuild — that's the common case for `bp run`/`dev`/`test`/
`migrate`/`deploy`, all of which keep working against their own prior output.
Pass `--force` (available on `build`, `run`, `dev`, `test`, and `migrate`) to
overwrite a foreign directory anyway.

### Python target status

`--target python` is an advanced (not yet 1.0-gated) target tracked under the
"Multi-target codegen progress" table in
[docs/production-readiness.md](./production-readiness.md). Unsupported
constructs fail codegen rather than being silently omitted; this includes the
authored test blocks in `examples/auth-service.bp`.

**Supported:**
- Models, `database postgres` → SQLAlchemy 2.0 + Pydantic v2 + a full Alembic
  skeleton. Like Node, Python follows the shared database-activation rule: a
  model implies this layer even when `database` is omitted, and an explicit
  database with no models still emits an importable empty `Base`.
- Endpoint bodies: `save` / `fetch` / `update` / `delete` / `query` (with
  `paginate(page, per_page)`, `first`, `order(col, dir)`, and `where(...)`
  comparison/`in`/`or`/`and`/text-search/duration predicates), `guard`,
  `when` block + inline form, `try`/`recover`,
  `map items: save M { ... }` (bound + unbound), `map items: update M { ... }`,
  FK access (`item.product.x` via cached `db.get` lookups), `log "msg"`, `sum(...)`.
- `log "msg" level(debug|info|warn|error)` → `logger.<method>(f"...")` from the
  generated `src/lib/log.py` (stdlib `logging`, message-only text lines:
  debug/info to stdout, warn/error to stderr; no level means `info`). Any other
  level fails the build. Like Node's `console.*` output, the lines are plain text,
  not JSON, and there is no `LOG_LEVEL` switch.
- REST inputs preserve transport and validation: GET/DELETE values use FastAPI
  `Query(...)`; POST/PUT/PATCH values use embedded JSON `Body(...)`; scalar
  defaults and supported `min`/`max` constraints are emitted. Direct
  `header.X-Name` references become valid `Header(..., alias="X-Name")`
  parameters.
- `secret` declarations generate Pydantic settings, including optional
  defaults. `env.FIELD` expressions import those settings and are accepted only
  when backed by a declared secret or a generated infrastructure setting such
  as `DATABASE_URL`/`REDIS_URL`.
- `fn` declarations (the same `impl node { module: ..., func: ... }` block used
  by the node target — there is no separate `impl python` syntax; the module
  path is reinterpreted under `src/impl/functions/` and the user fills in the
  Python side) and step calls to them.
- `middleware` declarations → FastAPI `Depends(...)` dependencies for the
  `fetch`, `log`, declared-function, `inject`, and `guard` step subset.
- `cache redis` emits a working `src/lib/cache.py`.
- `--gen-tests` — a pytest suite under `tests/` backed by
  `testcontainers[postgresql]` (Docker required). See
  [Testing Guide](./testing-guide.md#python-harness).

**Still rejected** with a specific, actionable message (tracked under
"Python target" in BACKLOG.md):
- Top-level `env`, named `type`/`alias`/`enum`, `storage`, `content`, `pipe`,
  `worker`, `schedule`, `subscribe`, `external`, `state`, `analytics`, `save`
  (versioned save schemas), `translation`, and `locale` declarations.
- Authored `test`, `test_group`, and `fixture` declarations, plus inline
  `fn logic` bodies. These are rejected until Python can preserve their
  behavior; generated contract tests remain available through `--gen-tests`.
- `STREAM` and `WS` endpoints, file/MIME and named endpoint inputs, middleware
  configuration/`after` bodies, blueprint-level middleware, endpoint metadata
  other than `use`, endpoint `on_error`, non-native fn implementation
  strategies, richer middleware steps, explicit `like` predicates,
  attribute access on JSON/map endpoint inputs or JSON-returning function
  results, unknown value calls, unsafe or malformed native implementation
  configuration, mismatched defaults or
  constraints, Python-keyword generated names, bare `where(q)` where `q` is not
  a string/text endpoint input (including dynamic filter accumulators), and any
  raw string interpolation of header/env or dictionary-backed values. Direct
  `header.X`/declared `env.X` expressions remain supported. Any future
  expression shape without an explicit Python translation also rejects.
- Model `computed` fields and `query ... with(...)` relationship loading. These
  currently have a Node emitter only.

Generated layout: `pyproject.toml` (uv), `src/app.py`, `src/lib/{env,db}.py`
(plus `src/lib/cache.py` when `cache redis` is declared),
`src/models/{schema,pydantic}.py`, `src/routes/<resource>.py`, full Alembic
skeleton (`alembic.ini`, `alembic/env.py`, `alembic/script.py.mako`,
`alembic/versions/`). Run with `uv sync && uv run uvicorn src.app:app`. With
`--gen-tests`: `uv sync && uv run pytest` (Docker required).

**Example:**

```bash
bp build my-service.bp
# Built my-service.bp -> generated/

bp build my-service.bp --out dist/
# Built my-service.bp -> dist/

bp build my-service.bp --react-query
# Built my-service.bp -> generated/
# Includes src/types/react-query.ts

bp build my-service.bp --frontend-only --react-query --out web-contract
# Emits only the standalone frontend package in web-contract/

bp build my-service.bp --gen-property-tests
# Emits contract tests plus deterministic *.property.test.ts suites (Node only)
```

Runs `check` first — exits on errors before generating any output.

`bp build` always generates frontend-safe contract files in `src/types/api.ts`, `src/types/schemas.ts`, and `src/types/client.ts`. The `--react-query` flag adds hook wrappers on top of that client.
Use `--frontend-only` when you want just the export-ready frontend package instead of the full backend project.

---

## `bp frontend`

Generate only the standalone frontend SDK package.

```bash
bp frontend <file.bp> [--out <dir>] [--react-query]
```

This is a convenience alias for `bp build --frontend-only`.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Output directory |
| `--react-query` | off | Include TanStack React Query hooks |

**Example:**

```bash
bp frontend my-service.bp --out web-contract --react-query
# Emits a publishable frontend SDK package in web-contract/
```

### `bp frontend publish`

Generate the frontend SDK package, install its dependencies, build it, and run a dry-run package check.

```bash
bp frontend publish <file.bp> [--out <dir>] [--react-query] [--skip-install]
```

This command runs the equivalent of:

1. `bp frontend ...`
2. `bun install`
3. `bun run build`
4. `bun pm pack --dry-run`

Use it when you want a quick publish-readiness check without actually pushing anything to npm.
Use `--skip-install` when dependencies are already installed and you only want to rerun the build and dry-run package check.

**Example:**

```bash
bp frontend publish my-service.bp --out web-contract --react-query

bp frontend publish my-service.bp --out web-contract --react-query --skip-install
# Skip bun install and just rerun build + bun pm pack --dry-run
```

---

## `bp run`

Build and start the server.

```bash
bp run <file.bp> [--out <dir>] [--force]
```

Equivalent to `bp build` followed by `bun install && bun run start` in the output directory.

**Example:**

```bash
bp run my-service.bp
# Built my-service.bp -> generated/
# my-service listening on port 3000
```

---

## `bp dev`

Watch mode — rebuild and restart the server on file changes.

```bash
bp dev <file.bp> [--out <dir>] [--force]
```

Watches the `.bp` file (and all included files) for changes. On save:
1. Re-runs `check`
2. If no errors, re-runs `build`
3. Restarts the Node.js process

**Example:**

```bash
bp dev my-service.bp --out generated
# Watching my-service.bp...
# Built my-service.bp -> generated/
# my-service listening on port 3000
# [change detected] rebuilding...
```

---

## `bp test`

Build and run the generated contract test suite for Node or Python.

```bash
bp test <file.bp> [--out <dir>] [--target <name>] [--gen-property-tests] [--force]
```

`node` is the default: Blueprint compiles the service, enables the generated
PGlite harness, installs dependencies when needed, and runs `bun run test`.
With `--target python`, Blueprint emits the pytest/testcontainers harness and
runs `uv run pytest` in the output directory.

`bp test` enables contract-test generation automatically. On Node, every
endpoint gets a happy-path test backed by in-process PGlite, so no external
Postgres is required; supported authored `test { }` blocks run alongside it.
Before building or installing anything, Node preflights authored tests and
rejects unimplemented cleanup, multipart/file fixtures, custom request entries,
dynamic target paths, duration assertions, ignored shared setup, and missing
reachable native implementations. Python uses a real Postgres testcontainer
for dialect fidelity, so Docker must be running.
Python currently runs the generated contract suite only. Because authored
Blueprint `test`, `test_group`, and `fixture` declarations are not translated,
the Python generator rejects a source file containing them instead of silently
dropping the tests.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Generated project directory |
| `--target <name>` | `node` | `node` (Vitest + PGlite) or `python` (pytest + testcontainers) |
| `--gen-property-tests` | false | Node only. Also emit and run deterministic fast-check properties; implies the generated contract suite and fails closed on unsupported routes |
| `--force` | false | Allow a non-empty output directory without a Blueprint manifest |

**Example:**

```bash
bp test my-service.bp
# Built my-service.bp -> generated/
# ✓ test/generated/todos.test.ts (5)
# ✓ test/watermark-success.test.ts (1)
# Test Files 2 passed (2)

# Node contract tests plus deterministic valid-request properties
bp test my-service.bp --gen-property-tests

# Python (requires uv and Docker)
bp test my-service.bp --target python
# Running pytest in generated (Docker required for Postgres testcontainers)...
```

---

## `bp migrate`

Run database migrations. Default delegates to `drizzle-kit`; `--target python`
delegates to `alembic` via `uv run`.

```bash
bp migrate <file.bp> [generate|push|studio] [--out <dir>] [--target <name>] [--force]
```

Builds the service first, then runs the matching migration tool.

**Subcommands:**

| Subcommand | node (drizzle-kit) | python (alembic) |
|------------|--------------------|------------------|
| `generate` | `drizzle-kit generate` | `alembic revision --autogenerate -m "auto"` |
| `push` | `drizzle-kit push` | `alembic upgrade head` |
| `check` | `drizzle-kit check` | `alembic check` |
| `studio` | Open Drizzle Studio | unsupported (alembic has no GUI) |

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Build output directory |
| `--target <name>` | `node` | Codegen target: `node` (drizzle-kit) or `python` (alembic) |

**Examples:**

```bash
# Push schema to database (development)
bp migrate my-service.bp push

# Generate a migration file (production)
bp migrate my-service.bp generate

# Open Drizzle Studio
bp migrate my-service.bp studio

# Python: generate an alembic revision
bp migrate my-service.bp generate --target python

# Python: apply migrations
bp migrate my-service.bp push --target python
```

`--target python` requires `uv` on PATH. The generated project's `pyproject.toml`
already pins `alembic`, so the first `uv run` will install it on demand.

---

## `bp generate`

Resolve `@>` (LLM generation) slots using the Anthropic API.

```bash
bp generate <file.bp> [--write]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--write` | false | Write resolved code back to the generated output |

**Requirements:**
- `ANTHROPIC_API_KEY` environment variable must be set

**Example:**

```bp
fn calculate_price {
  <- plan       string
  <- operations int
  -> money

  @> implement pricing logic:
     free: $0, pro: $0.01/op, enterprise: $0.005/op
}
```

```bash
ANTHROPIC_API_KEY=sk-ant-... bp generate my-service.bp --write
# Resolved 1 generation slot in my-service.bp
```

---

## `bp docs`

Generate an OpenAPI 3.1 JSON specification.

```bash
bp docs <file.bp> [--out <file.json>]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <file>` | stdout | Write to file instead of stdout |

**Example:**

```bash
bp docs my-service.bp --out openapi.json
# Generated openapi.json

# Pipe to a file
bp docs my-service.bp > openapi.json

# Validate with a third-party tool
bp docs my-service.bp | bunx @redocly/cli lint /dev/stdin
```

**Coverage:**
- HTTP endpoints (GET, POST, PUT, PATCH, DELETE) → standard OpenAPI paths
- STREAM endpoints → GET paths with `text/event-stream` response
- WS endpoints → GET paths with `101` response and `x-websocket: true`
- Models and types → `components/schemas`
- Path parameters (REST, STREAM and WS), query parameters, request bodies, responses
- `servers` → `http://localhost:<port>` from the blueprint `port` entry (3000 when absent)
- `auth` → `components/securitySchemes` and per-operation `security`. An endpoint without `auth` gets `security: []`; an endpoint that only uses middleware gets an `x-blueprint-middleware` list and a description, because middleware checks are not modeled
- `guard ... -> <status> "msg"` → error response with a `{ "error": string }` body. The generated server also documents 400 (zValidator failures), 401/400 for `auth webhook_sig`, 429 for `limit`, and 500 for REST routes
- Inputs are required unless they declare `optional` or `default(...)`, matching the generated validation
- `operationId`s are unique (a numeric suffix resolves collisions). Two declarations for the same method and path are an error
- The output is byte-for-byte deterministic for the same input

---

## `bp fmt`

Format a `.bp` file or an include fragment that intentionally omits the root
`blueprint` block. `#` comments are preserved, including leading and inline
comments.

```bash
bp fmt <file.bp|dir> [more ...] [--write] [--check]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--write` | false | Write formatted output back to the file |
| `--check` | false | Check if file is already formatted; exit 1 if not (for CI) |

Formatting several files, or a directory, requires `--write` or `--check`.
Without either, the command is a usage error (exit `2`). With `--check`, each
unformatted path is printed to stdout. See
[Multiple files and directories](#multiple-files-and-directories).

**Example:**

```bash
# Preview formatted output
bp fmt my-service.bp

# Format in place
bp fmt my-service.bp --write

# CI check (exits 1 if not formatted)
bp fmt my-service.bp --check

# Format every .bp file under api/ in place
bp fmt api --write

# CI check across a project (lists each unformatted file)
bp fmt api services --check
```

**Formatting rules:**
- Consistent indentation (2 spaces)
- Aligned field declarations
- Normalized whitespace around arrows
- Consistent quote style (double quotes)
- Preserved `#` comments
- Blank lines kept where the source had them (at most one in a row); none added
- Output is re-parsed and compared with the source before it is used; if it
  differs, `bp fmt` leaves the file unchanged and exits `3`

---

## `bp lint`

Lint a `.bp` file for best practice violations.

```bash
bp lint <file.bp|dir> [more ...] [--json]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | off | Output issues as JSON (for CI) instead of formatted text |

Several paths, or a directory, switch to multi-file mode. See
[Multiple files and directories](#multiple-files-and-directories).

**JSON output** (single file):

```json
{
  "success": true,
  "filename": "my-service.bp",
  "issue_count": 1,
  "error_count": 0,
  "warning_count": 1,
  "issues": [
    {
      "file": "my-service.bp",
      "rule": "intent-on-endpoints",
      "severity": "warning",
      "line": 45,
      "column": 1,
      "message": "Endpoint POST /api/items is missing an @ intent description",
      "hint": "Add `@ \"describe what this endpoint does\"` before `POST /api/items`"
    }
  ]
}
```

`severity` is `error`, `warning` or `info`. Parse, include and semantic
failures appear as issues with `rule` set to `parse`, `include` or `semantic`
(`severity` `error`). `line` and `column` are `0` when unknown. `success` is
`false` when there is any error-severity issue. Multiple files use the wrapper
described in [Multiple files and directories](#multiple-files-and-directories).

**Rules:**

| Rule | Level | Description |
|------|-------|-------------|
| `block-ordering` | warning | Top-level blocks should follow canonical order (blueprint, secret, model, endpoint, ...) |
| `intent-on-endpoints` | warning | Every endpoint (REST, STREAM, WS) should have an `@` intent annotation |
| `empty-endpoint` | warning | Endpoints with no inputs or statements are flagged |
| `unused-secret` | warning | A `secret` that no `env.NAME`, `using(secret.NAME)`, or external auth reads. `DATABASE_URL` and `REDIS_URL` are never reported. Silent for files with a non-`exec`/`http` `impl` or a `@>` slot, which can read env outside the source |
| `unused-env` | warning | An `env` that no `env.NAME` reads (same suppression as `unused-secret`) |
| `unused-function` | warning | A `fn`, `pipe`, or `middleware` that nothing calls or applies. A self-call does not count (same suppression) |
| `fetch-without-guard` | warning | A REST endpoint reads a `fetch` result before `guard <name>`. A missing row fails the request (500) instead of returning 404 |
| `unpaginated-list` | info | A `GET` endpoint returns every row of a `query` with no `paginate(...)` or `first` |

**Example:**

```bash
bp lint my-service.bp
# my-service.bp:45:1 [warning] intent-on-endpoints: Endpoint POST /api/items is missing an @ intent description
#   hint: Add `@ "describe what this endpoint does"` before `POST /api/items`
# 1 issue(s): 0 error(s), 1 warning(s)
```

---

## `bp init`

Scaffold a new Blueprint project.

```bash
bp init [name] [--template <name>]
bp init --list-templates
```

Creates a new directory containing `<name>.bp`, plus `.gitignore` and
`README.md` when those files do not already exist. An existing `<name>.bp` is
never overwritten, and existing `.gitignore` and `README.md` files are left
alone.

If `name` is omitted, uses the current directory name. The name is trimmed and
lowercased, and spaces become dashes. It must then start with a letter or digit
and contain only letters, digits, `-`, `_`, or `.`. Names with path separators,
`..`, or an empty result are rejected with exit code 2 before anything is
written.

| Template | What it scaffolds |
|----------|-------------------|
| `default` | One `item` model with paginated list and create endpoints (Postgres). This is the default. |
| `minimal` | A single `GET /health` endpoint, no database |
| `crud` | One `item` model with paginated list, get, create, update, and delete (Postgres) |
| `auth` | Users with register and login, and a `use require_auth` profile endpoint (Postgres, `JWT_SECRET`). The auth functions are stubs until you implement them in `generated/src/impl/functions/internal/auth.ts`. |
| `worker` | `POST /api/jobs` enqueues a job for a Redis-backed `worker` (Postgres, `REDIS_URL`). `bp test` needs Redis running at `REDIS_URL`. |

An unknown template exits with code 2 and lists the valid names.

**Example:**

```bash
bp init my-service
# Created my-service/my-service.bp
# Created my-service/.gitignore
# Created my-service/README.md

cd my-service
bp check my-service.bp
bp build my-service.bp
```

```bash
bp init jobs --template worker
```

After scaffolding, `bp dev <file>` rebuilds and restarts on changes, and
`bp test <file>` builds and runs the generated tests.

---

## `bp import`

Create a review scaffold from static TypeScript structure.

```bash
bp import [path] --from ts [--out <file.bp>] [--name <name>] [--force]
```

`path` defaults to the current directory and may be one TypeScript file or a
directory. The only supported source kind is `ts`/`typescript`. The scaffolder
recognizes conservative, static forms of:

- Drizzle `pgTable` and inline `pgEnum` declarations;
- Hono `GET`/`POST`/`PUT`/`PATCH`/`DELETE` route calls and static `basePath`;
- named inline Zod object schemas referenced by static, transport-compatible
  `zValidator` calls on those routes.

It is not a transpiler. No handler body is inspected or lifted into Blueprint
steps. Every imported route contains an explicit `TODO(import)` and returns
501; stderr prints a per-handler fidelity report plus warnings for skipped,
renamed, dynamic, duplicate, or otherwise unsupported structure. Hono router
mounts are not flattened, and dynamic/wildcard routes are skipped. Nullable or
type-changing Zod fields are skipped because Blueprint cannot preserve their
semantics. Static SQL column names are retained when representable; SQL/property
renames, dynamic SQL names, and dropped Drizzle builder/table/reference options
are reported as fidelity warnings.

Without `--out`, the `.bp` scaffold is printed to stdout. `--out` writes a file
atomically and refuses to replace it unless `--force` is present. Directory
scans ignore dependency/build folders and `.d.ts` files.

```bash
bp import ./src --from ts --name "Billing API" --out billing.bp
bp check billing.bp
```

Passing `bp check` confirms only that the scaffold is valid Blueprint. It does
not prove that the original service behavior was preserved; restore and test
every TODO manually.

---

## `bp version`

Print the installed Blueprint version.

```bash
bp version
# bp version <installed-version>
```

---

## `bp eject`

Remove Blueprint markers from generated code, making it fully yours.

```bash
bp eject <dir>
```

**What it does:**
- Removes `// Generated by Blueprint...` header comments from all `.ts` and `.json` files
- Removes `// Do not edit directly...` comments
- Prints a summary of ejected files

**Example:**

```bash
bp eject ./generated
#   ejected: src/index.ts
#   ejected: src/routes/users.ts
#   ejected: src/models/schema.ts
#   ...
# Ejected 12 file(s). This code is now fully yours.
```

After ejecting, you can delete your `.bp` source and maintain the TypeScript directly.

---

## `bp help`

Show usage information.

```bash
bp help
```

---

## `bp diff`

Preview what changes `bp build` will make before overwriting output. Shows a
line-level unified diff per modified file (`---`/`+++`/`@@` hunks), plus
new and deleted file summaries.

```bash
bp diff <file.bp> [--out <dir>] [--target <node|python|effect>] [--react-query] [--frontend-only] [--gen-tests] [--gen-property-tests] [--apply [--force]] [--exit-code] [--no-color]
```

The codegen manifest (`.blueprint/manifest.json`) is suppressed from output — its
content is just hashes derived from the other files, not something you can review.

Color is on when stdout is a TTY (use `--no-color` to disable, e.g. for pipes).
Shells out to `diff -u` for the unified patch.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Output directory to compare against |
| `--target <name>` | `node` | Target to diff against (`node`, `python`, or runnable health-only `effect` scaffold) — must match the target the directory was built with |
| `--react-query` | off | Compare as if `bp build --react-query` were used |
| `--frontend-only` | off | Compare as if `bp build --frontend-only` were used |
| `--gen-tests` | off | Compare as if `bp build --gen-tests` were used (Node/Python only; Effect rejects the flag) |
| `--gen-property-tests` | off | Node only. Compare output including deterministic property and implied contract suites; rejects unsupported routes just like `bp build` |
| `--apply` | off | After showing the diff, actually write the changes (equivalent to running `bp build` afterwards) |
| `--force` | off | Only with `--apply`. Same meaning as `bp build --force`: write into a non-empty `--out` directory that has no Blueprint manifest. Without `--apply` it is a usage error (exit `2`) |
| `--exit-code` | off | Exit `1` if there are any changes — useful in CI / pre-commit hooks |
| `--no-color` | off | Disable ANSI color in diff output |

**Examples:**

```bash
# Preview what would change.
bp diff my-service.bp

# CI / pre-commit: fail if generated/ is stale.
bp diff my-service.bp --exit-code

# Review then apply in one step.
bp diff my-service.bp --apply

# Apply into a directory bp did not create (overwrites that directory's files).
bp diff my-service.bp --out ./existing-dir --apply --force
```

---

## `bp deploy`

Build a Docker image from the generated project, then smoke-test it by running
the image and probing `/health`.

```bash
bp deploy <file.bp> [--out <dir>] [--tag <image>] [--target <name>] [--no-run]
```

`bp deploy` always builds the **node** codegen target internally — its
`--target` flag is a *deploy* target (where/how to ship the built image), not a
codegen target. It is unrelated to the `--target node|python|effect` flag on
`bp build`/`bp diff`/`bp migrate`.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Build output directory |
| `--tag <image>` | `blueprint-app:latest` | Docker image tag |
| `--target <name>` | `docker` | Deploy target. `fly` is not yet implemented. |
| `--no-run` | false | Skip the smoke-test `docker run` after build (e.g. CI image builds) |

**Example:**

```bash
# Build and smoke-test locally
bp deploy my-service.bp --tag my-app:latest

# Build the image but skip the smoke run (CI image builds)
bp deploy my-service.bp --tag my-app:latest --no-run

# Fly.io is not implemented yet (see docs/production-readiness.md Pillar 5)
bp deploy my-service.bp --target fly   # exits 2 with a clear error
```

---

## `bp stats`

Show code statistics for a Blueprint file.

```bash
bp stats <file.bp> [--json]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | Output in JSON format |

**Example:**

```bash
bp stats my-service.bp
# Models: 12
# Endpoints: 45
# Functions: 8
# Pipes: 3
# Lines of code: 1,247
```

---

## `bp doctor`

Check your environment for Blueprint dependencies.

```bash
bp doctor
```

Verifies that required tools are installed and accessible:
- Go and Git
- Node.js, npm, Bun, TypeScript, and Drizzle Kit
- Python, uv, Alembic, and pytest
- Docker, PostgreSQL client, and Redis client

Most probes are informational because the exact requirements depend on the
selected target. The command exits successfully when it finds either Bun or
Node.js plus npm for the default Node workflow.

**Example:**

```bash
bp doctor
# ✓ Go 1.25.5
# ✓ Node.js 20.5.0
# ✓ Bun 1.2.0
# ✓ Docker 24.0.7
# ⚠ PostgreSQL client not found (optional)
```

---

## `bp completion`

Generate shell completion script.

```bash
bp completion <bash|zsh|fish>
```

**Example:**

```bash
# Bash
source <(bp completion bash)

# Zsh
source <(bp completion zsh)

# Fish
bp completion fish | source
```

---

## `bp lsp`

Start the Language Server Protocol server.

```bash
bp lsp
```

Provides IDE support for Blueprint files:

- Syntax + semantic error diagnostics (`publishDiagnostics`, with structured codes)
- Context-aware hover (models, `fn`/`pipe`/`middleware`, fields, `@` intents, data-op steps)
- Go-to-definition (`textDocument/definition` — model, `fn`/`pipe`/`middleware`, and `<model>.<field>` references)
- Find references (`textDocument/references`) and rename
  (`textDocument/prepareRename`, `textDocument/rename`) for models, enums,
  types, `fn`, `pipe`, middleware, external services, and endpoint-local
  bindings. Strings, intents, comments, and field names are never touched.
  Renaming a `<-` input that names a `:param` path segment renames the segment
  too. Rename is refused for a document with parse errors, for keywords and
  reserved names, for names that collide with or would be captured by another
  symbol, and for any edit after which the checker reports a new error
- Quick-fix code actions (`textDocument/codeAction`) that apply the
  did-you-mean suggestion attached to a diagnostic
- Context-aware completion for declarations, types, constraints, settings,
  middleware, data operations, local bindings, fields, env/secret names,
  computed expressions, and ref-backed `with(...)` relationships
- Workspace symbol search across local `.bp` files in initialized workspace
  folders
- Document formatting (`textDocument/formatting`) with the same printer as
  `bp fmt`; a document with parse errors, or whose formatted output would not
  re-parse, keep the same comments, or be stable on a second pass, is left
  unchanged
- Document outline (`textDocument/documentSymbol`) with nested fields, computed
  fields, and enum variants
- Folding ranges (`textDocument/foldingRange`) for multi-line brace blocks

Configure an LSP-capable editor client to launch `bp lsp` for `.bp` files over
standard input/output. The packaged client in `editors/vscode-blueprint`
launches it automatically and exposes `blueprint.server.path`,
`blueprint.server.args`, and **Blueprint: Restart Language Server**. Neovim and
other editors that support custom stdio language servers can point their client
command directly at `bp lsp`.

Current limits are deliberate: synchronization is full-document, completion
uses the open document's parser/source context and has no resolve request,
workspace symbols scan local `file:` roots only, and references and rename
cover the open document only (no cross-file rename). Workspace scanning ignores common dependency,
generated, build, virtual-environment, and VCS directories; an unsaved open
document takes precedence over its on-disk copy.

---

## `bp explain`

Print the documentation for a structured Blueprint error code.

```bash
bp explain <code>
```

The code is looked up case-insensitively (`bp explain c001` and `bp explain
C001` are equivalent) against the same embedded docs `bp check`/`bp lint`
errors point at — parser codes (`P###`), lexer codes (`L###`), and checker
codes (`C###`). The canonical source is `internal/diag/error-codes.md`,
mirrored at [error-codes.md](./error-codes.md) (a Go test fails CI if the two drift).

**Example:**

```bash
bp explain C001
# ### C001 — missing blueprint block
#
# Every `.bp` source file must start with a `blueprint` declaration...
```

**Exit codes:**
- `0` — the code is documented; docs printed to stdout
- `1` — the code isn't documented; prints an error + a hint pointing at [error-codes.md](./error-codes.md)

---

## `bp context`

Print the agent-facing language + CLI surface — concise, structured, and embedded into the binary (no network access required). Designed for AI agents that need to bootstrap on Blueprint without pulling the full VitePress site.

```bash
bp context [topic] [--format md|json]
```

With **no topic**, prints the full surface: Blueprint version, command index, codegen target index, and the topic catalogue. Mirrors the structure of `cairntrace`'s `cairn explain` so agents that handle one handle both.

With a **topic**, prints the focused doc for that topic. Available topics:

| Topic | Covers |
|---|---|
| `overview` | What Blueprint is, the compile loop, the codebase map |
| `language` | `.bp` DSL: top-level decls, types, constraints, pipeline steps |
| `cli` | The `bp` command surface, common flags, typical flows |
| `codegen` | What `bp build` writes (Node + Python tree, manifest) |
| `targets` | Choosing between `--target node`, `--target python`, and `--target effect` |
| `errors` | Diagnostic shape, code namespaces (`L###`/`P###`/`C###`), `bp explain` |
| `workflow` | Recommended agent loop: read → edit → check → build → diff |
| `examples` | What each `examples/*.bp` demonstrates |

**Examples:**

```bash
# Full surface (Markdown, default)
bp context

# Single topic, Markdown
bp context language

# Same topic as structured JSON (for tooling)
bp context language --format json

# Bootstrap an agent session
bp context             # learn the surface
bp context workflow    # learn the loop
bp context language    # learn the DSL
```

**Output formats**

- `md` (default) — verbatim Markdown, suitable for pasting into a system prompt.
- `json` — structured shape: `{ "$schema": "urn:blueprint.dev:context:v1", "topic": { name, title, summary, sections[], relatedTopics[] } }` for topic output, or the synthesized `Surface` shape for the no-topic full surface.

Topics ship as embedded Markdown files (`internal/agentctx/topics/*.md`) so the binary is self-contained — `bp context` works offline.

---

## `bp llms`

Print the complete agent/LLM onboarding guide — every `bp context` topic plus the live CLI command surface and codegen target list, concatenated into one self-contained document (an `llms.txt` for `bp`).

```bash
bp llms [--out <file>]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <file>` | stdout | Write the guide to a file (e.g. `llms.txt`) instead of printing it |

Where `bp context <topic>` gives you one focused slice and `bp explain <code>`
gives you one error code's docs, `bp llms` is the "read this once and you know
the whole surface" document — a framing preamble, the live CLI reference, the
target list, and every topic in reading order, all assembled from
`internal/agentctx` so it always reflects the real binary.

**Example:**

```bash
bp llms --out llms.txt
# Wrote agent/LLM guide to llms.txt (NNNN bytes)

bp llms | less
```

---

## Environment Variables

| Variable | Used by | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | `bp generate` | Anthropic API key for LLM slots |

## Exit Codes

Every command follows the same convention, so scripts and CI can branch on the
code without parsing command-specific output:

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Validation error — parse/semantic errors from `bp check`/`bp build`/etc., lint errors, `bp fmt --check` on an unformatted file, a failed test run, an undocumented `bp explain <code>` |
| `2` | Environment or file error — the input file couldn't be read, an unknown/malformed `--target` or flag, a value flag with no value, an unexpected extra argument, a path argument of `bp check`/`bp lint`/`bp fmt` that doesn't exist or is a directory with no `.bp` files, `bp fmt` on several files without `--write`/`--check`, `bp diff --force` without `--apply`, a required external tool is missing (`docker`, `uv`), an output-directory safety check failed (see `--force` on `bp build`) |
| `3` | Formatter bug — `bp fmt` produced output that does not re-parse or changes the syntax tree, so it refused to format the file. Report it; the file is unchanged |
| `4` | Codegen error — the AST parsed and checked cleanly, but the target generator itself failed (e.g. an unsupported construct for `--target python`/`--target effect`) |

`--help`/`-h` on any command always exits `0` after printing usage.
