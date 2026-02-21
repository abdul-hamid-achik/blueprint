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
bp build <file.bp> [--out <dir>]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `generated/` | Output directory |

**Example:**

```bash
bp build my-service.bp
# Built my-service.bp -> generated/

bp build my-service.bp --out dist/
# Built my-service.bp -> dist/
```

Runs `check` first — exits on errors before generating any output.

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
bp migrate <file.bp> [generate|push|drop|studio] [--out <dir>]
```

Builds the service first, then delegates to `drizzle-kit`.

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `generate` | Generate SQL migration files from schema changes |
| `push` | Push schema directly to the database (no migration files) |
| `drop` | Drop all tables (destructive) |
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
bp fmt <file.bp> [--write]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--write` | false | Write formatted output back to the file |

**Example:**

```bash
# Preview formatted output
bp fmt my-service.bp

# Format in place
bp fmt my-service.bp --write
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

**What it checks:**

| Rule | Example |
|------|---------|
| Every endpoint should have an `@` intent | `POST /api/items { ... }` with no `@` |
| No TODO comments in production blocks | `|> # TODO: fix this` |
| Overly large endpoints | Endpoint with 20+ steps |
| Missing error handling | No `guard`, `try/recover`, or `on_error` |
| Unused secrets | `secret FOO required` never referenced |
| Unused fixtures | `fixture "x" from "..."` not referenced in tests |

**Example:**

```bash
bp lint my-service.bp
# my-service.bp:45: warning: endpoint POST /api/items has no intent (@)
# my-service.bp:67: warning: secret UNUSED_KEY is declared but never referenced
# 2 warnings
```

---

## `bp init`

Scaffold a new Blueprint project.

```bash
bp init [name]
```

Creates a new directory with a starter `.bp` file and `.env.example`.

**Example:**

```bash
bp init my-service
# Created my-service/
# Created my-service/my-service.bp
# Created my-service/.env.example

cd my-service
bp dev my-service.bp
```

If `name` is omitted, uses the current directory name.

---

## `bp version`

Print the installed Blueprint version.

```bash
bp version
# Blueprint v0.3.0
```

---

## `bp help`

Show usage information.

```bash
bp help
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
