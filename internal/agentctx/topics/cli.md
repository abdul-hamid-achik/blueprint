# Blueprint CLI

`bp <command>`. Most commands take a `.bp` file as the first positional arg. The two most-used: `bp check` (validate) and `bp build` (compile).

## Common commands

| Command | Purpose |
|---|---|
| `bp check <file.bp>` | Parse + semantic check. Exit 0 = valid. |
| `bp build <file.bp> [--out <dir>] [--target node\|python\|effect]` | Compile to a target project. Idempotent; Effect is experimental. |
| `bp diff <file.bp> [--out <dir>] [--exit-code]` | Show pending changes. With `--exit-code`, exit 1 if anything differs — CI/idempotency gate. |
| `bp fmt <file.bp> [--write] [--check]` | Format. Round-trip safe. |
| `bp lint <file.bp>` | Stylistic lint. |
| `bp docs <file.bp> [--out file.json]` | Emit OpenAPI 3.1 spec from declared inputs/outputs. |
| `bp run <file.bp>` | Build + start the server (Node default). |
| `bp dev <file.bp>` | Watch + rebuild on change. |
| `bp test <file.bp> [--target node\|python]` | Build with contract tests, then run Vitest + PGlite (node, default) or pytest + a Postgres testcontainer (python). |
| `bp migrate <file.bp> generate\|push\|check [--target node\|python]` | Drizzle (node) or Alembic (python) migrations. |
| `bp deploy <file.bp> [--target docker\|fly] [--tag <image>] [--no-run]` | Build + run Docker image; fly currently exits with "not implemented". |
| `bp init [name]` | Scaffold a new project. |
| `bp import [path] --from ts [--out <file.bp>]` | Recover static Drizzle/Hono and transport-compatible `zValidator` structure as a review scaffold; every handler becomes TODO/501. |
| `bp eject <dir>` | Strip Blueprint markers from a generated project (you take over). |
| `bp explain <code>` | Print docs for an error code (e.g. `bp explain C001`). |
| `bp context [topic] [--format md\|json]` | This command. Agent-facing language + CLI surface, by topic. |
| `bp llms [--out <file>]` | The complete agent/LLM guide — every topic in one document (the `llms.txt` for bp). |
| `bp doctor` | Check toolchain deps. |
| `bp lsp` | Start the stdio language server: diagnostics, hover, definition, completion, and local-workspace symbols. |
| `bp stats <file.bp>` | Code stats. |
| `bp version` | Print version. |

## Common flags

- `--out <dir>` — where to write generated project (default `./generated`).
- `--target {node,python,effect}` — codegen target. Default `node`. `build` + `diff` accept all three; `test` + `migrate` accept `node`/`python`. `deploy` uses a separate `docker|fly` target. `effect` is an experimental runnable health/config scaffold.
- `--gen-tests` — emit an auto-generated contract suite with `build`/`diff` for Node/Python; Effect rejects the flag. `bp test` enables it automatically.
- `--gen-property-tests` — Node-only on `build`/`diff`/`test`; implies contract tests and emits deterministic fast-check valid-request properties. Unsupported/non-hermetic routes reject the whole build.
- `--exit-code` — make `diff` return 1 if anything would change (CI gate).
- `--no-color` — disable ANSI color (`diff`, error rendering). Also honors `NO_COLOR=1`.
- `--write` — `fmt --write` writes back; `generate --write` resolves @> slots.

## Typical flows

```bash
# Validate then build
bp check examples/todo-api.bp
bp build examples/todo-api.bp --out /tmp/todo

# Idempotency check (CI gate)
bp build examples/todo-api.bp --out /tmp/todo
bp diff  examples/todo-api.bp --out /tmp/todo --exit-code   # exit 0

# Python target
bp build examples/todo-api.bp --out /tmp/py-todo --target python
cd /tmp/py-todo && uv sync && uv run uvicorn src.app:app

# Auto-generated tests
bp test examples/todo-api.bp                                # self-contained, no Postgres needed
bp test examples/todo-api.bp --target python                # pytest + Docker-backed Postgres

# Deterministic Node contract + property suites
bp test examples/todo-api.bp --gen-property-tests

# TypeScript migration starting point (never imports handler behavior)
bp import ./src --from ts --out imported.bp
```

## See also

- `bp context targets` — node vs python
- `bp context workflow` — agent-friendly compile loop
- `bp context errors` — how diagnostics render
