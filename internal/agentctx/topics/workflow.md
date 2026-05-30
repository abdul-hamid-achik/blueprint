# Agent Workflow

If you're an AI agent working on a Blueprint project, this is the loop that wastes the least context.

## The base loop

```
1. Read the .bp source (this is the source of truth).
2. Edit the .bp.
3. bp check <file.bp>          # 50-100ms; catches everything syntax+semantic.
4. bp build <file.bp> --out X  # idempotent; cheap to re-run.
5. (optional) bp diff <file.bp> --out X --exit-code   # confirms idempotency.
6. Inspect / run the generated project at X.
```

## What "edit" means

Most changes are direct edits to the `.bp` file. The `bp` tools rewrite the *generated project* whenever the source changes; you almost never edit the generated TypeScript/Python directly — your edits get clobbered on the next `bp build`.

Exception: files marked `UserOwned` (specifically `src/impl/functions/<...>.ts` / `.py` scaffolds, plus the `.env` and `alembic/script.py.mako`). The build creates these once and then never touches them.

## Reading errors

Errors render with `file:line:col` and a caret. Always grep the source file at that line before "fixing" — the compiler is precise; if it says line 17 col 4, the actual problem is at line 17 col 4 (rarely elsewhere).

`bp explain <code>` for any structured code (`bp explain C001`, `bp explain L001`). Reads embedded docs; no network access needed.

## Idempotency is a contract

A fresh `bp build` followed immediately by `bp diff --exit-code` must report no changes. The CI gate enforces this on every push. If you see drift, it's a codegen bug (sort order, map iteration, time-based output) — not user error. Report it.

## Examples are the spec

`examples/*.bp` exercise every supported feature. When the docs aren't precise enough about a syntax detail, grep an example:

```bash
grep -l 'try {'              examples/*.bp     # try/recover pattern
grep -l 'paginate'           examples/*.bp     # pagination
grep -l 'inject .* as'       examples/*.bp     # middleware injection
grep -l '^stream'            examples/*.bp     # SSE
grep -l '^ws '               examples/*.bp     # WebSocket
```

## Tests are cheap to generate

`bp test <file.bp>` will:
1. Build with `--gen-tests`.
2. Emit a Vitest suite (Node) or pytest suite (Python) from the endpoint declarations.
3. Run it against an in-memory Postgres (Node) or a testcontainers Postgres (Python).

You don't have to author the tests yourself for a sanity check — let the generator do it.

## Reading the codebase

If you need to understand WHY a piece of generated code looks the way it does, the order is: `internal/codegen/{js,python}/<area>.go` → `internal/resolve/` (typed IR) → `internal/checker/checker.go` → `internal/parser/parser.go` → `internal/lexer/lexer.go`. The flow is upstream-to-downstream.

## See also

- `bp context cli` — every command, every flag
- `bp context language` — what valid `.bp` looks like
- `bp context errors` — diagnostic shape + codes
- `bp context examples` — spec-by-example
