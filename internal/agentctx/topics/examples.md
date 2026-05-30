# Example Specs

Five `.bp` files in `examples/`. Each compiles on both targets and is covered end-to-end by CI. Pick one close to your task and grep for the construct you need.

## `hello-world.bp` (smallest)

Two static endpoints — no database. The minimum viable spec. Use it when you need to confirm a CLI flag works without spinning up Postgres.

```bp
endpoint GET /health { -> 200 { status: "ok" } |> return { status: "ok" } }
endpoint GET /hello/:name { <- name string required ... }
```

## `todo-api.bp` (canonical CRUD)

Single model + 5 endpoints (list with pagination, create, get, update, delete). The most-used example. If you're learning the language, read this first.

Demonstrates: `model`, `endpoint`, `query / paginate`, `fetch`, `save`, `update`, `delete`, `guard`, declared `-> <status>` responses.

## `auth-service.bp` (middleware + fn impl)

Real-world auth. `secret` declarations for `JWT_SECRET` / `DATABASE_URL`. A `require_auth` middleware that runs before every protected route. Four `fn` declarations (`hashPassword`, `verifyPassword`, `signJwt`, `verifyJwt`) with `impl node { module: "./internal/auth", func: "..." }` — these emit a `UserOwned` scaffold the user fills in.

Demonstrates: `secret`, `middleware { before { ... } }`, `inject X as Y`, `fn ... impl native`, the merged-stub scaffold pattern, header-based auth (`header.Authorization`).

## `ecommerce-api.bp` (multi-model + FKs + transactions)

Product / cart / order / order_item with foreign keys. Checkout endpoint uses `try { ... } recover { ... }` around a multi-step transaction (decrement stock, charge stripe, save order, map line items). External `stripe` service for the charge step. Pagination, search, and FK-aware queries.

Demonstrates: `ref(<model>)` foreign keys, FK-access dereferencing (`item.product.stock`), `try / recover`, `sum()`, `map <coll>: save <M>`, `external "<name>" { ... }` for `call stripe.charge(...)`, `cache redis`.

## `realtime-chat.bp` (streaming + WebSocket)

Room/message models plus three real-time surfaces: SSE for live updates (`stream GET /rooms/:id/events`), WebSocket for bidirectional chat (`ws /rooms/:id/ws`), and REST endpoints for room management. Uses `cache redis` for pub/sub.

Demonstrates: `stream` endpoints (`first(timeout)`, `on event(name)`), `ws { on_connect / on_message / on_disconnect }`, `inject` in WS handshake, `broadcast`/`emit` to clients.

## See also

- `bp context language` — what every construct above means
- `bp context codegen` — what the build of each example writes
- `bp context cli` — flags for `bp build` + `bp test`
