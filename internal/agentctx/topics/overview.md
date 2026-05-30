# Blueprint Overview

Blueprint is a Go-based compiler. Source files use the `.bp` DSL — a declarative description of a web service: data models, business pipelines, HTTP endpoints, middleware, and (optionally) streaming/WebSocket handlers. The compiler emits a runnable project in TypeScript (Hono + Drizzle + Zod + Pino, default) or Python (FastAPI + SQLAlchemy + Pydantic v2 + Alembic, via `--target python`).

## The compile loop

```
.bp source -> bp check (parse + semantics) -> bp build --out <dir> -> runnable project
```

`bp build` is idempotent: running it twice in a row produces zero diff. `bp diff` shows what would change without writing; `bp diff --exit-code` is the CI gate.

## What goes where

- `examples/*.bp` — five canonical specs that the test suite + CI cover end-to-end (hello-world, todo-api, auth-service, ecommerce-api, realtime-chat). Read these first when learning the language.
- `internal/parser`, `internal/checker`, `internal/resolve` — the compiler frontend (lexer → parser → checker → typed IR).
- `internal/codegen/{js,python,common}` — backends. `common` holds target-agnostic string/path helpers.
- `internal/diag` — diagnostic formatter; `internal/diag/error-codes.md` is the canonical doc for `Cxxx`/`Pxxx`/`Lxxx`.
- `docs/` — VitePress site source (`language-reference.md`, `cli-reference.md`, `error-codes.md`, `architecture.md`, etc).

## Generated output is real code

`bp build` writes a complete project the user (or you) can edit. The build tracks every emitted file in `.blueprint/manifest.json` (sha256 per file). On the next build, stale files get removed; user-edited files marked `UserOwned: true` (e.g. `src/impl/functions/<...>.ts` scaffolds) are preserved.

## See also

- `bp context language` — `.bp` DSL syntax
- `bp context cli` — the `bp` command surface
- `bp context workflow` — recommended loop for agents
- `bp context examples` — what each example.bp demonstrates
