# CLAUDE.md — orientation for Claude Code

Blueprint is a Go compiler and developer toolchain for declarative `.bp`
service definitions.

```text
.bp -> lexer -> parser/AST -> checker -> resolve facts
    -> node | python | effect generator
    -> OutputFile set -> manifest-aware writer
```

The default Node target emits Hono + Drizzle + Zod. The Python target emits
FastAPI + SQLAlchemy + Pydantic/Alembic and is an advanced beta. The Effect
target is an experimental project/config scaffold and does not yet emit models
or endpoints.

`bp generate` is separate from codegen: it asks Anthropic for Blueprint arrow
statements and optionally replaces quoted `@> "..."` lines in the source `.bp`
file. It does not generate TypeScript implementation files.

## Read first

- [AGENTS.md](./AGENTS.md) — architecture, output ownership, verified
  limitations, and change workflows.
- [docs/language-reference.md](./docs/language-reference.md) — published
  language contract.
- [docs/cli-reference.md](./docs/cli-reference.md) — command and exit-code
  contract.
- [BACKLOG.md](./BACKLOG.md) — in-flight and planned work.
- `bp context [topic]` or `bp llms` — the command's embedded agent-facing
  language, CLI, target, and workflow guidance.

## Documentation boundary

`docs/` is the source for the public site at **blueprint-lang.dev**. Put only
user-facing tutorials, guides, reference, explanations, and public release
notes there. Internal reviews, audit logs, experiments, and design spikes do
not belong in that directory.

The maintainer may keep internal notes in an Obsidian vault. Use an Obsidian
integration only when it is available in the current environment and the
maintainer has placed that vault in scope; otherwise ask where the note should
go.

## Verification

Start with the affected package, then run the project gate:

```bash
go test ./...
go build -o bin/bp ./cmd/bp
./bin/bp check testdata/valid/all_features.bp
```

For Node generator changes, also build and type-check the reference fixture:

```bash
./bin/bp build testdata/valid/all_features.bp --target node --out /tmp/blueprint-node
cd /tmp/blueprint-node
bun install
bun run build
```

For Python or Effect changes, run the corresponding generator tests and build
representative fixtures with `--target python` or `--target effect`. Preserve
unrelated worktree changes and never overwrite user-owned generated stubs.
