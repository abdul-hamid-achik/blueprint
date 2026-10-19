# Blueprint Overview

Blueprint is a Go-based compiler. Source files use the `.bp` DSL — a declarative description of a web service: data models, business pipelines, HTTP endpoints, middleware, and (optionally) streaming/WebSocket handlers. The compiler emits a runnable project in TypeScript (Hono + Drizzle + Zod, default), Python (FastAPI + SQLAlchemy + Pydantic v2 + Alembic, via `--target python`), or an early Effect-TS scaffold (`--target effect`, experimental).

## The compile loop

```
.bp source -> bp fmt -> bp check -> bp lint -> bp build --out <dir> -> bp test
```

`bp build` is idempotent: a second build right after the first produces no diff. `bp diff --exit-code` is the CI gate. `bp context workflow` has the full loop.

## What goes where

- `examples/*.bp` — five canonical specs (hello-world, todo-api, auth-service, ecommerce-api, realtime-chat). CI checks and builds all five for Node. Only hello-world and todo-api build for Python. Read them first when learning the language (`bp context examples`).
- `internal/parser`, `internal/checker`, `internal/resolve` — the compiler frontend (lexer → parser → checker → typed IR).
- `internal/codegen/{js,python,common}` — backends. `common` holds target-agnostic string/path helpers.
- `internal/importer` — static TypeScript structure recovery for `bp import`;
  all imperative handlers become reported TODO/501 stubs.
- `internal/lsp` + `editors/vscode-blueprint` — stdio diagnostics with quick
  fixes, hover, definition, references, open-document rename, completion,
  workspace symbols, formatting, document outline, folding, and the packaged
  VS Code client.
- `internal/diag` — diagnostic formatter; `internal/diag/error-codes.md` is the canonical doc for `Cxxx`/`Pxxx`/`Lxxx`.
- `docs/` — VitePress site source (`language-reference.md`, `cli-reference.md`, `error-codes.md`, `architecture.md`, etc).

## Generated output is real code

`bp build` writes a complete project the user (or you) can edit. The build tracks every emitted file in `.blueprint/manifest.json` (sha256 per file). On the next build, stale files get removed; user-edited files marked `UserOwned: true` (e.g. `src/impl/functions/<...>.ts` scaffolds) are preserved.

Node can opt into deterministic fast-check properties with
`--gen-property-tests`, and it supports pure computed model fields plus
one-level ref-backed `query ... with(...)` loading. Other targets reject those
Node-only semantics rather than dropping them.

## See also

- `bp context language` — `.bp` DSL syntax
- `bp context cli` — the `bp` command surface
- `bp context workflow` — recommended loop for agents
- `bp context examples` — what each example.bp demonstrates
