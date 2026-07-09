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
bp check <file.bp> [--json]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | off | Output a machine-readable JSON result (for CI) instead of formatted text |

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

## `bp build`

Compile a `.bp` file to a runnable project.

```bash
bp build <file.bp> [--out <dir>] [--target <node|python|effect>] [--react-query] [--frontend-only] [--gen-tests] [--force]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Output directory |
| `--target <name>` | `node` | Codegen target. `node` emits Hono + Drizzle + Zod (production-ready, all examples). `python` emits FastAPI + SQLAlchemy 2.0 + Pydantic v2 + Alembic (advanced — see "Python target status" below). `effect` emits TypeScript on Effect (experimental scaffold — project shell + secrets module; endpoint/model emit is still in design) |
| `--react-query` | off | Generate `src/types/react-query.ts` and add TanStack React Query deps (node target only) |
| `--frontend-only` | off | Emit only the standalone frontend contract package (node target only) |
| `--gen-tests` | off | Generate contract + happy-path tests. **Node target:** Vitest suite under `test/generated/` with an in-memory (PGlite) database harness — runs with no external Postgres. **Python target:** pytest suite under `tests/` backed by `testcontainers[postgresql]` — Docker required, real Postgres per session, function-scoped TRUNCATE between tests |
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
`migrate`/`deploy`, none of which expose `--force` and all of which keep working
against their own prior output. Pass `--force` to overwrite a foreign directory
anyway.

### Python target status

`--target python` is an advanced (not yet 1.0-gated) target tracked under the
"Multi-target codegen progress" table in
[docs/production-readiness.md](./production-readiness.md). All 5 shipped
examples (`hello-world`, `todo-api`, `auth-service`, `ecommerce-api`,
`realtime-chat`) compile end-to-end on `--target python` today.

**Supported:**
- Models, `database postgres` → SQLAlchemy 2.0 + Pydantic v2 + a full Alembic
  skeleton.
- Endpoint bodies: `save` / `fetch` / `update` / `delete` / `query` (with
  `paginate(page, per_page)`, `first`, `order(col, dir)`, `where(col == val, ...)`
  `==`-only predicates), `guard`, `when` block + inline form, `try`/`recover`,
  `map items: save M { ... }` (bound + unbound), `map items: update M { ... }`,
  FK access (`item.product.x` via cached `db.get` lookups), `log "msg"` →
  `print(f"...")`, `sum(...)`.
- `fn` declarations (the same `impl node { module: ..., func: ... }` block used
  by the node target — there is no separate `impl python` syntax; the module
  path is reinterpreted under `src/impl/functions/` and the user fills in the
  Python side) and step calls to them.
- `middleware` declarations → FastAPI `Depends(...)` dependencies.
- `STREAM` endpoints (SSE via `sse-starlette`) and `WS` endpoints (FastAPI
  WebSocket handshake) generate real routing/handshake code with a guarded
  fetch prelude; the event body itself (message parsing, `on event(...)
  where(...)` predicates, broadcast) emits as a commented TODO placeholder to
  be filled in by hand. `cache redis` emits a working `src/lib/cache.py`.
- `--gen-tests` — a pytest suite under `tests/` backed by
  `testcontainers[postgresql]` (Docker required). See
  [Testing Guide](./testing-guide.md#testing-the-python-target).

**Still rejected** with a specific, actionable message (tracked under
"Python target" in BACKLOG.md):
- Top-level `storage`, `content`, `pipe`, `worker`, `schedule`, `subscribe`,
  `external`, `state`, `analytics`, `save` (versioned save schemas),
  `translation`, and `locale` declarations. None of the 5 shipped examples use
  these, which is why all 5 still compile.
- `query ... where(...)` predicates that aren't `==` (text search, `!=`, `<`,
  `>`, `in`, function calls).
- Step statements that aren't a data-op call — a bare expression or block
  literal on the left of `|>` isn't translated.

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
bp run <file.bp> [--out <dir>]
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
bp dev <file.bp> [--out <dir>]
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

Build and run the Vitest test suite.

```bash
bp test <file.bp> [--out <dir>]
```

Compiles the service (including test files), then runs `bun run test` in the output directory.

`bp test` enables `--gen-tests` automatically, so every endpoint gets a contract +
happy-path test backed by an in-memory PGlite database. Tests run with **no external
Postgres** — the generated harness mocks `src/lib/db` with an in-process instance and
seeds foreign-key parents as needed. Hand-written `test { }` blocks run alongside the
generated suite.

**Example:**

```bash
bp test my-service.bp
# Built my-service.bp -> generated/
# ✓ test/generated/todos.test.ts (5)
# ✓ test/watermark-success.test.ts (1)
# Test Files 2 passed (2)
```

---

## `bp migrate`

Run database migrations. Default delegates to `drizzle-kit`; `--target python`
delegates to `alembic` via `uv run`.

```bash
bp migrate <file.bp> [generate|push|studio] [--out <dir>] [--target <name>]
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
- Path parameters, query parameters, request bodies, responses

---

## `bp fmt`

Format a `.bp` file.

```bash
bp fmt <file.bp> [--write] [--check]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--write` | false | Write formatted output back to the file |
| `--check` | false | Check if file is already formatted; exit 1 if not (for CI) |

**Example:**

```bash
# Preview formatted output
bp fmt my-service.bp

# Format in place
bp fmt my-service.bp --write

# CI check (exits 1 if not formatted)
bp fmt my-service.bp --check
```

**Formatting rules:**
- Consistent indentation (2 spaces)
- Aligned field declarations
- Normalized whitespace around arrows
- Consistent quote style (double quotes)

---

## `bp lint`

Lint a `.bp` file for best practice violations.

```bash
bp lint <file.bp>
```

**Rules:**

| Rule | Level | Description |
|------|-------|-------------|
| `block-ordering` | warning | Top-level blocks should follow canonical order (blueprint, secret, model, endpoint, ...) |
| `intent-on-endpoints` | warning | Every endpoint (REST, STREAM, WS) should have an `@` intent annotation |
| `empty-endpoint` | warning | Endpoints with no inputs or statements are flagged |

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
bp init [name]
```

Creates a new directory with a starter `.bp` file.

**Example:**

```bash
bp init my-service
# Created my-service/my-service.bp

cd my-service
bp build my-service.bp
```

If `name` is omitted, uses the current directory name.

---

## `bp version`

Print the installed Blueprint version.

```bash
bp version
# bp version 0.11.0
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
bp diff <file.bp> [--out <dir>] [--target <node|python|effect>] [--react-query] [--frontend-only] [--gen-tests] [--apply] [--exit-code] [--no-color]
```

The codegen manifest (`.blueprint/manifest.json`) is suppressed from output — its
content is just hashes derived from the other files, not something you can review.

Color is on when stdout is a TTY (use `--no-color` to disable, e.g. for pipes).
Shells out to `diff -u` for the unified patch.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Output directory to compare against |
| `--target <name>` | `node` | Target to diff against (`node`, `python`, or `effect` — experimental scaffold) — must match the target the directory was built with |
| `--react-query` | off | Compare as if `bp build --react-query` were used |
| `--frontend-only` | off | Compare as if `bp build --frontend-only` were used |
| `--gen-tests` | off | Compare as if `bp build --gen-tests` were used |
| `--apply` | off | After showing the diff, actually write the changes (equivalent to running `bp build` afterwards) |
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
- Go (for building from source)
- Node.js and Bun
- Docker (optional)
- PostgreSQL client (optional)
- Redis client (optional)

**Example:**

```bash
bp doctor
# ✓ Go 1.22.0
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
- Autocomplete (planned)

Configure your editor to use `bp lsp` for `.bp` files.

**Example (VS Code settings.json):**

```json
{
  "blueprint.languageServer.path": "bp",
  "blueprint.languageServer.args": ["lsp"]
}
```

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
| `2` | Environment or file error — the input file couldn't be read, an unknown/malformed `--target` or flag, a required external tool is missing (`docker`, `uv`), an output-directory safety check failed (see `--force` on `bp build`) |
| `4` | Codegen error — the AST parsed and checked cleanly, but the target generator itself failed (e.g. an unsupported construct for `--target python`/`--target effect`) |

`--help`/`-h` on any command always exits `0` after printing usage.
