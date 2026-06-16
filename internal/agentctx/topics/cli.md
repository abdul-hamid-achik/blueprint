# Blueprint CLI

`bp <command>`. Most commands take a `.bp` file as the first positional arg. The two most-used: `bp check` (validate) and `bp build` (compile).

## Common commands

| Command | Purpose |
|---|---|
| `bp check <file.bp>` | Parse + semantic check. Exit 0 = valid. |
| `bp build <file.bp> [--out <dir>] [--target node\|python]` | Compile to a runnable project. Idempotent. |
| `bp diff <file.bp> [--out <dir>] [--exit-code]` | Show pending changes. With `--exit-code`, exit 1 if anything differs — CI/idempotency gate. |
| `bp fmt <file.bp> [--write] [--check]` | Format. Round-trip safe. |
| `bp lint <file.bp>` | Stylistic lint. |
| `bp docs <file.bp> [--out file.json]` | Emit OpenAPI 3.1 spec from declared inputs/outputs. |
| `bp run <file.bp>` | Build + start the server (Node default). |
| `bp dev <file.bp>` | Watch + rebuild on change. |
| `bp test <file.bp>` | Build with `--gen-tests` + run the generated suite (self-contained, in-memory Postgres via PGlite). |
| `bp migrate <file.bp> generate\|push\|check [--target node\|python]` | Drizzle (node) or Alembic (python) migrations. |
| `bp deploy <file.bp> [--target docker\|fly] [--tag <image>] [--no-run]` | Build + run Docker image; fly currently exits with "not implemented". |
| `bp init [name]` | Scaffold a new project. |
| `bp eject <dir>` | Strip Blueprint markers from a generated project (you take over). |
| `bp explain <code>` | Print docs for an error code (e.g. `bp explain C001`). |
| `bp context [topic] [--format md\|json]` | This command. Agent-facing language + CLI surface, by topic. |
| `bp llms [--out <file>]` | The complete agent/LLM guide — every topic in one document (the `llms.txt` for bp). |
| `bp doctor` | Check toolchain deps. |
| `bp lsp` | Start the language server (stdin/stdout, JSON-RPC). |
| `bp stats <file.bp>` | Code stats. |
| `bp version` | Print version. |

## Common flags

- `--out <dir>` — where to write generated project (default `./generated`).
- `--target {node,python,effect}` — codegen target. Default `node`. `build` + `diff` accept all three; `migrate`/`deploy` are node/python only. `effect` is an experimental scaffold.
- `--gen-tests` — emit auto-generated contract test suite (`build` and `test` only).
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
```

## See also

- `bp context targets` — node vs python
- `bp context workflow` — agent-friendly compile loop
- `bp context errors` — how diagnostics render
