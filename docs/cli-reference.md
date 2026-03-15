# CLI Reference

All commands follow the pattern: `bp <command> [arguments] [flags]`

## `bp check`

Validate a `.bp` file for syntax and semantic errors.

```bash
bp check <file.bp>
```

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
```

**Exit codes:**
- `0` — no errors
- `1` — one or more errors

---

## `bp build`

Compile a `.bp` file to TypeScript.

```bash
bp build <file.bp> [--out <dir>] [--react-query] [--frontend-only]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Output directory |
| `--react-query` | off | Generate `src/types/react-query.ts` and add TanStack React Query deps |
| `--frontend-only` | off | Emit only the standalone frontend contract package |

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
2. `npm install`
3. `npm run build`
4. `npm pack --dry-run`

Use it when you want a quick publish-readiness check without actually pushing anything to npm.
Use `--skip-install` when dependencies are already installed and you only want to rerun the build and dry-run package check.

**Example:**

```bash
bp frontend publish my-service.bp --out web-contract --react-query

bp frontend publish my-service.bp --out web-contract --react-query --skip-install
# Skip npm install and just rerun build + npm pack --dry-run
```

---

## `bp run`

Build and start the server.

```bash
bp run <file.bp> [--out <dir>]
```

Equivalent to `bp build` followed by `npm install && npm start` in the output directory.

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

Compiles the service (including test files), then runs `npx vitest run` in the output directory.

**Example:**

```bash
bp test my-service.bp
# Built my-service.bp -> generated/
# ✓ test/watermark-success.test.ts (1)
# ✓ test/watermark-oversized.test.ts (1)
# Test Files 2 passed (2)
```

---

## `bp migrate`

Run Drizzle Kit database migrations.

```bash
bp migrate <file.bp> [generate|push|studio] [--out <dir>]
```

Builds the service first, then delegates to `drizzle-kit`.

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `generate` | Generate SQL migration files from schema changes |
| `push` | Push schema directly to the database (no migration files) |
| `studio` | Open Drizzle Studio (visual database UI) |

**Examples:**

```bash
# Push schema to database (development)
bp migrate my-service.bp push

# Generate a migration file (production)
bp migrate my-service.bp generate

# Open Drizzle Studio
bp migrate my-service.bp studio
```

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
bp docs my-service.bp | npx @redocly/cli lint /dev/stdin
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
# bp version 0.1.0
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

Preview what changes `bp build` will make before overwriting output.

```bash
bp diff <file.bp> [--out <dir>] [--react-query] [--frontend-only]
```

Compares current generated output against what a fresh build would produce. Shows a unified diff of changes.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Output directory |
| `--react-query` | off | Compare output as if `bp build --react-query` were used |
| `--frontend-only` | off | Compare output as if `bp build --frontend-only` were used |

**Example:**

```bash
bp diff my-service.bp
# Shows diff between current generated/ and new build output

bp diff my-service.bp --react-query
# Also includes changes from generated React Query hooks and frontend package files

bp diff my-service.bp --frontend-only
# Compares only the standalone frontend package output
```

---

## `bp deploy`

Deploy the generated application to Docker or Fly.io.

```bash
bp deploy <file.bp> [--out <dir>] [--tag <image>]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Build output directory |
| `--tag <image>` | blueprint-app | Docker image tag |

**Example:**

```bash
# Build and deploy locally with Docker
bp deploy my-service.bp --tag my-app:latest

# Deploy to Fly.io (requires flyctl)
bp deploy my-service.bp --tag my-app:latest
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
- Node.js and npm
- Docker (optional)
- PostgreSQL client (optional)
- Redis client (optional)

**Example:**

```bash
bp doctor
# ✓ Go 1.22.0
# ✓ Node.js 20.5.0
# ✓ npm 10.2.3
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
- Syntax error diagnostics
- Hover documentation
- Go-to-definition (planned)
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

## Environment Variables

| Variable | Used by | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | `bp generate` | Anthropic API key for LLM slots |

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Error (parse error, check error, build error) |
