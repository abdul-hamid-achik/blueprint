# Example Specs

Five `.bp` files in `examples/`. CI checks and builds all five for Node. Only `hello-world` and `todo-api` build for `--target python`; CI asserts the other three are rejected. Pick the one closest to your task and grep for the construct you need.

## `hello-world.bp` (smallest)

No database. Two GET endpoints, one with a path parameter and a constant response. Use it to confirm a toolchain or CLI change without Postgres.

## `todo-api.bp` (canonical CRUD)

One model and five endpoints: paginated list, create, get, toggle, and delete. Start here when learning the language. It also builds on Python.

Demonstrates: `model` with constraints, `query ... paginate(...)`, `save`, `fetch`, `update`, `delete`, `guard`, `->` responses, path parameters.

## `auth-service.bp` (middleware and fn impl)

Register, login, profile, password change, logout, and email verification. Two models (`user`, `session`), two secrets, four `fn` declarations that use `impl node { module, func }` for user-owned implementations, and a `require_auth` middleware.

Demonstrates: `middleware { before { ... } }`, `inject ... as ...`, header-based auth (`header.Authorization`), `fn` impl scaffolds.

## `ecommerce-api.bp` (multi-model and transactions)

Products, cart items, orders, and order items, with `ref(...)` foreign keys and an `OrderStatus` enum. The checkout path uses `try { ... } recover { ... }` around several writes, plus a `charge_stripe` fn that needs `STRIPE_KEY`.

Demonstrates: `ref(model)`, `try`/`recover`, `enum`, admin endpoints, `guard`-based 404s.

## `realtime-chat.bp` (streaming and WebSocket)

Rooms and messages, a WebSocket endpoint per room, and two STREAM endpoints for live events and presence. Uses `cache redis`.

Demonstrates: `WS` with `on_connect`/`on_message`/`on_disconnect`, `join`/`leave`/`broadcast room(...)`, `|> close`, `STREAM` with `stream { |> on event(...) }` and `on timeout(...)`, `emit`.

Node only: `bp build --target python` rejects it.

## See also

- `bp context language` — what each construct means, with a complete example
- `bp context targets` — which examples build on which target
- `bp context cli` — flags for `bp build` and `bp test`
