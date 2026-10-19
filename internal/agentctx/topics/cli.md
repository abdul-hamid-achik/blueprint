# Blueprint CLI

`bp <command>`. Most commands take a `.bp` file as the first argument. `check`, `lint`, and `fmt` also take several files or directories.

## Commands

| Command | Purpose |
|---|---|
| `bp check <file.bp\|dir>... [--json]` | Parse and check. Directories expand recursively to `*.bp`. |
| `bp fmt <file.bp\|dir>... [--write] [--check]` | Format. Several paths or a directory need `--write` or `--check`. Output is re-parsed and compared with the source; a formatter bug exits 3 and leaves the file untouched. |
| `bp lint <file.bp\|dir>... [--json]` | Lint. `--json` gives file, rule, severity, line, column, message, hint. |
| `bp build <file.bp> [--out dir] [--target node\|python\|effect]` | Generate a project. Idempotent. |
| `bp diff <file.bp> [--out dir] [--exit-code] [--apply [--force]]` | Show pending changes. `--exit-code` exits 1 on any change. `--apply --force` writes into a non-empty `--out` without a manifest. |
| `bp test <file.bp> [--target node\|python]` | Build with contract tests and run them: Vitest + PGlite (node), or pytest + Docker Postgres (python). |
| `bp run` / `bp dev <file.bp>` | Start the server / watch and rebuild. |
| `bp docs <file.bp> [--out file.json]` | OpenAPI 3.1 from declared inputs and outputs. |
| `bp migrate <file.bp> generate\|push\|check [--target node\|python]` | Drizzle or Alembic migrations. |
| `bp init [name] [--template default\|minimal\|crud\|auth\|worker]` | Scaffold a `.bp`, `.gitignore`, and `README.md`. Never overwrites. `--list-templates` lists them. |
| `bp explain <code>` | Docs for an error code, for example `bp explain C052`. |
| `bp context [topic] [--format md\|json]` | Agent-facing topics (this surface). |
| `bp llms [--out file]` | Every topic in one document. |
| `bp lsp` | Language server over stdio: diagnostics with quick fixes, hover, definition, references, rename, completion, formatting, document outline, folding, and workspace symbols. |
| `bp mcp` | Model Context Protocol server over stdio: tools `check`, `lint`, `format`, `explain`, `context`, `build`, `diff`; resources for every context topic. |
| `bp frontend <file.bp> [--out dir] [--react-query]` | Generate only the standalone frontend SDK package. |
| `bp import [path] --from ts [--out file.bp]` | Scaffold a `.bp` from TypeScript structure. Every handler becomes a TODO/501 stub. |
| `bp deploy`, `bp eject`, `bp generate`, `bp stats`, `bp doctor`, `bp completion`, `bp version` | Docker image, strip markers, resolve `@>` slots via LLM, code stats, toolchain check, shell completion, version. |

## Flags worth knowing

- `--out <dir>`: output directory (default `./generated`).
- `--target`: `node` (default), `python`, or `effect`. `test` and `migrate` accept only node and python; `deploy` uses its own `docker|fly`.
- `--gen-tests`: contract suite (node, python): REST contracts, plus refusal tests for STREAM and WS endpoints (auth, malformed declared path inputs, `use` header guards). `bp test` enables it.
- `--gen-property-tests`: Node only. Implies `--gen-tests`.
- `--no-color` or `NO_COLOR=1`: plain output.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | Validation failure: check, lint, `fmt --check`, a failed test, `diff --exit-code` with changes, undocumented `bp explain` code. |
| 2 | Usage or environment: no command or an unknown command, a missing required argument (usage is printed), an unknown subcommand or value, unknown flag, flag with no value, extra argument, missing file or directory with no `.bp`, `fmt` on several files without `--write`/`--check`, `diff --force` without `--apply`, missing `docker` or `uv`, unsafe `--out`. |
| 3 | `bp fmt` refused: the formatter produced output that changed the syntax tree. Report it. |
| 4 | Codegen error: the file checks, but the target cannot generate it (for example a Python build of a WS endpoint that uses `use` middleware). |

With several files, the exit code is the most severe per-file code, and a summary goes to stderr (`3 files checked, 1 failed`). `bp lint --json` and `bp check --json` wrap per-file results in one document.

## Common flows

```bash
bp check examples/todo-api.bp examples/hello-world.bp
bp check --json services/          # directory, recursive
bp fmt --check services/           # CI formatting gate
bp lint --json services/
bp build examples/todo-api.bp --out /tmp/todo
bp diff examples/todo-api.bp --out /tmp/todo --exit-code   # 0 after a fresh build
bp test examples/todo-api.bp       # Node, in-memory Postgres
bp init my-api --template crud
```

## See also

- `bp context workflow` — the agent loop built on these commands
- `bp context targets` — node, python, effect differences
- `bp context errors` — diagnostics and codes
