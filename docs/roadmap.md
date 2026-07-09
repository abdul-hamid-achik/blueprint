# Roadmap

Blueprint has a usable core: lexer, parser, semantic checker, multi-target codegen (node default, plus python and an effect scaffold), DX tools, testing/migrations, an LSP server, Docker deploy, LLM integration, and release automation. This page separates stable surfaces from preview work and outlines what comes next.

---

## Completed (M1-M7)

| Milestone | What | Status |
|-----------|------|--------|
| M1 | Lexer + Parser + AST | Done |
| M2 | Semantic Checker | Stable core |
| M3 | JS/TS Codegen (Hono + Drizzle + Zod) | Stable REST core, preview realtime/worker surfaces |
| M4 | Developer Experience (`init`, `fmt`, `lint`, `docs`, `dev`, `run`, `diff`, `eject`) | Stable core |
| M5 | Testing + Migrations (`bp test`, `bp migrate`) | Stable core |
| M6 | LLM Integration (`bp generate` via Anthropic API) | Preview |
| M7 | Polish + Launch (GoReleaser, GitHub Actions, runtime packages) | In progress |

Also shipped beyond the original milestones:

- **`bp deploy`** — builds and smoke-runs a Docker image (`--target docker` default; `fly` not yet implemented).
- **LSP server** (`bp lsp`, `internal/lsp/`) — diagnostics, go-to-definition, and context-aware hover. Autocomplete, intent-hover, and a bundled VS Code extension are still open (see Quick Wins).
- **Multi-target codegen** — three `--target`s on `bp build`/`diff` (`bp migrate` supports `node`/`python` only): **node** (default), **python** (FastAPI + SQLAlchemy + Alembic), and **effect** (early scaffold). `bp deploy` has its own, unrelated `--target` (a deploy target: `docker`, with `fly` not yet implemented) and always builds the node codegen target internally.
- **`bp diff`** and **`bp eject`** — preview generated changes (with `--apply`/`--exit-code`) and strip Blueprint markers for a clean standalone project.

REST services, models, middleware, pipes, functions, schedules, external calls, and generated Vitest tests are the most mature path. The Docker deploy path and the LSP server are usable today; STREAM, WebSocket, workers, and subscriptions are available but should be treated as preview until the gaps below are closed.

---

## Priority 0: Known Bugs

Issues identified during the codebase audit that should be fixed immediately.

> **Fixed (2026-06-16 audit):** three previously-listed P0s have shipped and were removed — the rate-limit store is now hoisted to module scope with TTL-based cleanup (was reset per request), inline `enum(...)` fields now emit a proper `pgEnum` (was falling back to `text`), and `version` is now a `var` so GoReleaser ldflags inject the build-time value (was a `const`).

### Workers Not Wired in index.ts

Worker files are generated in `src/workers/` but never imported or started in `src/index.ts`. The BullMQ worker registration is present in individual files but the main entry point doesn't start them.

### STREAM/WS Codegen Gaps

STREAM and WS endpoints are fully parsed into the AST and collected during codegen, but the actual SSE/WebSocket handler code is incomplete. These endpoints are generated as route files but the real-time transport logic (EventSource streaming, WebSocket upgrade) needs hardening.

---

## Priority 1: Quick Wins

High-impact improvements that can be done in days, not weeks.

> The LSP server, `bp deploy`, `bp diff`, and `bp eject` shipped — see Completed below. The remaining items here are open.

### Language Server Protocol (LSP) — feature depth

**Status:** Shipped. `internal/lsp/` provides diagnostics, go-to-definition, and context-aware hover today.

**What's still open:**
- Autocomplete for keywords, model names, field names
- Hover documentation from `@` intent annotations
- Workspace symbol search depth
- Publish as a VS Code extension with the server bundled

### `bp deploy` — more targets

**Status:** Shipped for Docker. `bp deploy <file.bp>` builds and smoke-runs a Docker image (`--target docker` is the default; `fly` is not yet implemented).

**What's still open:**
- Wire up the reserved `--target fly` (build + push image + deploy)
- Add Railway/Render later

### Error Message Improvements

**Why:** Current error messages are functional but could be more helpful.

**What to build:**
- Colored output with source line context (show the offending line with a caret `^`)
- "Did you mean?" suggestions for misspelled names
- Structured error format: `file:line:col: error[E001]: message`
- Error codes that link to documentation

---

## Priority 2: Ecosystem

Medium-effort features that expand Blueprint's reach.

### Multi-Target Codegen — more targets

**Status:** Three targets ship today via the `Generator` interface in `internal/codegen/` (`--target` on `bp build`/`diff`; `bp migrate` accepts `node`/`python` only; `bp deploy`'s `--target` is a separate deploy-target concept and always builds node internally): **node** (default — Hono + Drizzle + Zod), **python** (`--target python` — FastAPI + SQLAlchemy + Alembic, via `internal/codegen/python/`), and **effect** (`--target effect` — an early TypeScript-on-Effect scaffold). See [Multi-Target Codegen](/multi-target-codegen) for the contract every generator satisfies.

**Targets still to consider:**
- **Go/Chi or Gin** — for teams that want Go output
- **Deno** — native TypeScript without Node.js

**Approach:** Add a package under `internal/codegen/<target>/` implementing the same `Generator` interface and register it in the `--target` dispatch.

### Plugin System

**Why:** Let users extend Blueprint with custom codegen, custom lint rules, or custom operations.

**What to build:**
- Plugin manifest format (`.bp-plugin.json`)
- Hook points: `after-parse`, `after-check`, `after-codegen`
- Custom lint rule plugins
- Custom operation plugins (new built-in operations beyond `query`, `save`, etc.)

### CI/CD Integration

**Why:** Blueprint should fit naturally into CI pipelines.

**What to build:**
- `bp check --format json` for machine-readable output
- `bp fmt --check` exit code 1 if unformatted (already exists)
- `bp lint --format json`
- GitHub Action: `uses: blueprint-lang/setup-bp@v1`
- Pre-commit hook integration

### Vite Frontend App Target

**Why:** The current frontend output is a TypeScript SDK package. That is good for monorepos and shared contracts, but teams often want a runnable Vite app scaffold that works with npm, pnpm, yarn, or Bun.

**Decision:** Keep the SDK/package target separate from a future app target. A Vite app should consume the generated client instead of being mixed into the backend service output.

**What to build:**
- `bp frontend app <file.bp> --target vite-react --out web`
- Generate `index.html`, `vite.config.ts`, `src/main.tsx`, `src/App.tsx`, and a typed API client wired to the generated contract.
- Keep `package.json` scripts package-manager neutral (`dev`, `build`, `preview`) so users can run them with their package manager of choice.
- Optionally support `--package-manager bun|npm|pnpm|yarn` for lockfile/install commands, without changing the generated source.

### Package Registry

**Why:** Sharing reusable middleware, pipes, and types across projects.

**What to build:**
- `bp add auth-middleware` — install a shared Blueprint package
- Registry of community-contributed packages
- `include "pkg:auth-middleware/require_jwt"` syntax for package imports

---

## Priority 3: Language Evolution

Features that extend what Blueprint can express.

### Relationships and Joins

**Why:** Currently, `ref(model)` creates foreign keys but there's no join syntax.

**What to build:**
```bp
model post {
  id        uuid   primary
  author_id uuid   ref(user)
  title     string required
}

# New: join syntax
GET /api/posts/:id {
  <- id uuid required
  |> post = fetch post(id) with(author)
  -> 200 { title: post.title, author_name: post.author.name }
}
```

### Computed Fields

**Why:** Derived values that are calculated, not stored.

```bp
model order {
  id       uuid  primary
  subtotal money required
  tax_rate float default(0.08)

  # Computed at query time
  computed total = subtotal * (1 + tax_rate)
}
```

### Conditional Middleware

**Why:** Apply middleware based on conditions.

```bp
POST /api/upload {
  use require_auth when(not env.DISABLE_AUTH)
  use rate_limit(10/min) when(auth.plan == "free")
  ...
}
```

### Batch Operations

**Why:** Bulk create/update/delete in a single request.

```bp
POST /api/items/batch {
  <- items []{ name: string, price: int }
  |> created = save_many item items
  -> 201 { count: created.count }
}
```

### GraphQL Target

**Why:** Some teams prefer GraphQL over REST.

**What to build:**
- Models automatically become GraphQL types
- Endpoints become queries/mutations
- Subscriptions from STREAM/WS endpoints

---

## Priority 4: Architecture Improvements

Internal improvements for maintainability and performance.

### Codegen Refactoring

**Current state:** `internal/codegen/js/generator.go` is 3,169 lines. `helpers.go` is 1,163 lines. All codegen logic lives in these two files.

**What to improve:**
- Split generator.go into focused files: `static.go` (package.json, tsconfig, Dockerfile), `schema.go` (models, types, validation), `routes.go` (REST/STREAM/WS + index.ts), `functions.go` (fn, pipe, middleware), `workers.go` (workers, schedules, subscriptions), `tests.go` (test codegen), `emit.go` (emitArrowStmts, emitCtx, imports)
- Extract `emitCtx` into a proper typed struct with methods (currently passed by value but contains maps with shared reference semantics)
- Eliminate 4 pairs of `X()` / `XWithCtx()` wrapper functions
- Deduplicate `genRoute`, `genStreamRoute`, `genWsRoute` import/builder patterns (~100 lines each)
- Create a shared `codegen` interface package for multi-target support
- Add golden file (snapshot) tests for generated output

### CLI Refactoring

**Current state:** `cmd/bp/main.go` is 770 lines with repeated parse-check boilerplate across commands.

**What to improve:**
- Extract `parseAndCheck(filename) (*ast.File, []byte, error)` helper
- Split into per-command files or use a subcommand pattern
- Add `--verbose`, `--quiet`, and `--json` output flags
- Add colored output for error messages (source line + caret `^`)

### Test Coverage Improvements

**Current state:** the suite covers the main parser, checker, and codegen paths, but critical gaps remain.

| Module | Coverage | Gap |
|--------|----------|-----|
| `internal/generate` | **0%** | No tests at all (266 lines) |
| `cmd/bp` | **0%** | No CLI integration tests (770 lines) |
| `internal/linter` | **41%** | Only 3 rules tested |
| `internal/ast` | **16%** | Printer tested, node methods not |
| `internal/codegen/js` | **83%** | Low coverage on helpers: `typeToTS` 33%, `typeToZod` 52%, `jsEscapeString` 55% |

**What to add:**
- Fuzz tests for lexer and parser (prevent panics on arbitrary input)
- CLI integration tests (`bp check`, `bp build`, `bp fmt` as subprocesses)
- Unit tests for `internal/generate` (slot collection, prompt building)
- Golden file tests for codegen output (snapshot regression tests)
- Add `-race` flag and `-coverprofile` to CI pipeline
- Enable `t.Parallel()` across independent tests

### Benchmark Suite

**What to measure:**
- Lexer throughput (tokens/second)
- Parser throughput (files/second)
- Codegen throughput (generated lines/second)
- End-to-end: `.bp` file to generated output
- Compare across releases to catch performance regressions

### End-to-End Tests

**Current state:** Tests verify individual stages (lex, parse, check, codegen) but don't test the full pipeline.

**What to add:**
- Compile a `.bp` file -> verify generated output compiles (`tsc --noEmit`)
- Compile + run -> verify HTTP endpoints respond correctly
- Test the CLI commands end-to-end
- Roundtrip formatter test: `bp fmt` twice produces identical output

---

## Priority 5: Community and Ecosystem

### Playground / REPL

**Why:** Try Blueprint without installing anything.

**What to build:**
- Web-based editor with live preview of generated code
- Share snippets via URL
- Built with WebAssembly (compile the Go toolchain to WASM)

### Documentation Site

**Status:** VitePress docs site exists with ~17 pages, including deployment, testing, LLM generation, multi-target codegen, production-readiness, error-codes, and FAQ guides.

**What to add:**
- Comparison with alternatives (Prisma, Supabase, tRPC)
- Interactive examples (link to playground)
- API reference auto-generated from Go source

### Editor Plugins

**Current state:** TextMate grammar for VS Code syntax highlighting.

**What to add:**
- VS Code extension with LSP integration
- Neovim plugin (Tree-sitter grammar + LSP)
- JetBrains plugin (IntelliJ, WebStorm)
- Zed extension

### Example Projects

**Current state:** 5 example `.bp` files in `examples/`.

**What to add:**
- SaaS starter (auth + billing + teams)
- Blog API with comments and search
- E-commerce with Stripe integration
- Real-time dashboard with SSE
- Multi-tenant API with row-level security

---

## Contributing

Blueprint is open source. To contribute:

1. Pick an item from this roadmap
2. Open an issue to discuss the approach
3. Read the [Architecture](/architecture) page for codebase orientation
4. Submit a PR

See the [GitHub repository](https://github.com/abdul-hamid-achik/blueprint) for the issue tracker and contributing guidelines.
