# CLAUDE.md — orientation for Claude Code

Blueprint is a Go toolchain that compiles a declarative `.bp` DSL into web-service
projects (JS/Node via Hono+Drizzle+Zod; Python via FastAPI; an `effect` target is
an early scaffold). Pipeline: Lexer → Parser → AST → Checker → resolve IR → Codegen.

## Read these first
- **[AGENTS.md](./AGENTS.md)** — the canonical agent guide (architecture, codegen
  patterns, known gaps, how to add commands/targets/tests).
- **[SPEC.md](./SPEC.md)** — the language spec; the single source of truth.
- **[BACKLOG.md](./BACKLOG.md)** — in-flight and planned work.

## Where things go (important)
- **`docs/` is the published website.** `docs/.vitepress/` deploys to
  **blueprint-lang.dev**, and every `.md` under `docs/` becomes a public page.
  Put **user-facing reference docs** there — nothing else.
- **Internal notes, reviews, experiments, and design spikes do NOT go in `docs/`**
  (they would ship as public pages) and generally do **not** go in the repo at
  all. They live in the maintainer's **Obsidian vault**. Use the `obsidian-cli`
  skill to read/write them; organize under a `Blueprint/` folder there.

## Verify before you call it done
```bash
go test ./...                                   # all packages must pass
go build -o bin/bp ./cmd/bp
./bin/bp check testdata/valid/all_features.bp
./bin/bp build testdata/valid/all_features.bp --out generated
cd generated && bun install && bun run build    # generated TS must have 0 tsc errors
```
