# Blueprint Language Reference

The `.bp` DSL: whitespace-significant blocks, `#` line comments, snake_case fields and inputs, PascalCase enum names (C048). Every file opens with one `blueprint "<name>" { ... }` block. Full reference: `docs/language-reference.md`. Real specs: `examples/*.bp`.

## Complete example

```bp
blueprint "todo-api" {
  version "1.0.0"
  port 3000
  runtime node
}

secret DATABASE_URL required

enum Priority {
  low
  high
}

model todo {
  id       uuid       primary
  title    string     required min(1) max(200)
  done     bool       default(false)
  priority Priority   default(low)
  score    int        min(0) max(100) default(0)
  created  timestamp  default(now)
}

@ "List todos, one page at a time"
GET /api/todos {
  <- page     int default(1) min(1)
  <- per_page int default(20) min(1) max(100)
  |> todos = query todo paginate(page, per_page)
  -> 200 { items: todos.items, total: todos.total }
}

@ "Create a todo"
POST /api/todos {
  <- title string required
  |> created = save todo { title: title }
  -> 201 { id: created.id, title: created.title }
  -> 400 "invalid"
}

@ "Get one todo"
GET /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "Todo not found"
  -> 200 { id: todo.id, title: todo.title, done: todo.done }
}
```

## Top-level declarations

| Declaration | Notes |
|---|---|
| `blueprint "name" { version, port, runtime, database, cache }` | Required, first block, once per file. |
| `secret NAME required` | Env var the service needs. |
| `model name { field type constraints }` | Database table. |
| `enum Name { one variant per line }` | Named enum. Inline form in a field: `enum(a, b)`. |
| `type Name { field type }` / `alias Name = ...` | Structured type / alias. Python rejects both. |
| `external "name" { ... }` | Service for `call` steps. Node `auth` is `bearer`, `jwt`, `basic`, or `api_key` with exactly one `secret.NAME` or `env.NAME` (sets `Authorization` or `X-API-Key`). `retry N` adds N immediate attempts for network/timeout failures and HTTP 408/429/5xx only. |
| `middleware name { before { ... } after { ... } }` | Request middleware. |
| `fn name { <- in type  -> type  impl node { module: "...", func: "..." } }` | Function; `impl` gives a user-owned scaffold. |
| `pipe name { ... }` | Reusable pipeline. |
| `GET\|POST\|PUT\|PATCH\|DELETE /path { ... }` | REST endpoint. |
| `auth <strategy> using(secret.NAME)` (endpoint meta, REST/STREAM/WS) | Node enforces `bearer`, `jwt` (HS256 only), `api_key`, and `basic` against the secret before the handler: 401 `{"error":"Unauthorized"}`. STREAM/WS also accept `?token=` (bearer, jwt) and `?api_key=`. Claims are not exposed; use a `use` middleware. A bare strategy with no `using` is metadata only. |
| `cache <duration>` (endpoint meta) | Node, GET only: `Cache-Control` on 2xx responses, `private` when the response can depend on the caller (`use`/`auth`/`limit`, global `use`, or header/cookie/session/auth reads), otherwise `public`. Other methods get no header (lint `cache-on-non-get`). Python rejects it. |
| `STREAM /path { ... }` | SSE endpoint. Node only. |
| `WS /path { on_connect { } on_message { } on_disconnect { } }` | WebSocket endpoint. Node only. |
| `test name { ... }` | Authored Node test. Python rejects. |
| `computed name type = expr` (in a model) | Read-only, non-persisted field: `string`/`text`/`int`/`float`/`money`/`bool`, a pure expression over required fields and earlier computed fields. Calls, optional fields, forward refs, and use in `save`/`seed`/`update` keys or `where`/`order` are rejected. Node only. |

**Intents:** `@ "text"` may precede any top-level declaration (checked: secret, model, enum, type, fn, middleware, endpoints) and any `|>` step (`|> @ "fetch it"`). Intents are documentation only. `bp lint` warns `intent-on-endpoints` for an endpoint without one.

## Types

`string`, `text`, `int`, `float`, `bool`, `uuid`, `timestamp`, `json`, `json<T>`, `money`, `file`; `enum(a, b)` inline or a named enum; `ref(model)` for a foreign key.

## Field constraints

| Constraint | Meaning |
|---|---|
| `primary`, `required`, `optional`, `unique`, `index`, `auto` | Key, NOT NULL, nullable, UNIQUE, index, auto-bump on update. |
| `default(v)` | Column default. `default(now)` for timestamps. |
| `ref(model)` | Foreign key to `model.id`. |
| `min(n)`, `max(n)` | Bounds. Rules below. |
| `format(kind)` | String validation: `email`, `url`, `uuid`, `ip`, `date`. |

### Constraint values (C052)

`bp check` rejects a value that cannot apply to the field's type. Every violation is C052.

- `default(v)` must fit the type: int = whole number; float/money = number; bool = `true`/`false`; string = quoted string; uuid = quoted UUID; timestamp = `now` or a quoted ISO-8601 value; enum = one of its variants.
- `min`/`max` on int, float, money: a number (or a size such as `10mb` / duration such as `5s` where allowed).
- `min`/`max` on string, uuid, list: a whole, non-negative length, e.g. `max(255)`.
- `min`/`max` on bool, enum, timestamp: not allowed.
- `min(n) max(m)` with plain literals requires `n <= m`.
- `format` only on string fields, with one of the five kinds above.

## Pipeline steps (`|>`)

Endpoint bodies, middleware `before`/`after`, and fn logic are `|>` steps. Output lines (`-> status ...`) must come after all steps (C011).

| Step | Meaning |
|---|---|
| `x = fetch M(id)` | One record by id. |
| `x = query M [where(...)] [with(rel)] [order(...)] [paginate(p, pp)] [first]` | Collection, page, or single row. `where` takes `==`, `!=`, `<`, `>`, `<=`, `>=`, `in`, `or`, `and`, and text search. Node `with(author)` is a one-level LEFT JOIN through `author_id ref(target)`; aliases, self joins, nested joins, and `fetch ... with(...)` are rejected. |
| `x = save M { col: expr }` / `update M { ... }` / `delete x` | Write. |
| `guard expr -> 404 "msg"` | Stop with an HTTP error when falsy. |
| `when cond { ... }` | Conditional block. |
| `try { ... } recover { ... }` | `recover` sees `error`. A `try` with two or more writes rolls back as a unit (Node transaction, Python SAVEPOINT). External side effects are not rolled back; compensate in `recover`. |
| `map coll: op` | For-each. |
| `log "msg" level(info)` | Levels: debug, info, warn, error. Other levels are C051. |
| `inject x as y` | Middleware: expose a value to the handler. |
| `emit "event" { data }` / `enqueue "queue" { data }` | Event / BullMQ job. With `REDIS_URL`, STREAM listeners on other instances receive emitted events; `subscribe` handlers stay local. |
| `join room(id)` / `leave room(id)` / `broadcast room(id) { data }` | WS only. Need no `model room`. |
| `close` | WS only: closes the connection and ends the handler. Anywhere else: C035. |
| `call svc GET /path` / `sleep 5s` | External call / delay. |

`whisper` is not implemented; the checker rejects it. There is no `return` step: end with `->` outputs.

## REST endpoints

- Inputs come from the query string (GET, DELETE), the JSON body (POST, PUT, PATCH), `:param` path segments, or `header.X-Name`.
- Path inputs are validated against their type and constraints. A bad segment answers `400` on Node (`{ success: false, error }`, before the handler) and `422` on Python.
- Output values reference bindings or literals. A type name in a response (`-> 200 { id: string }`) is C035, an unbound identifier.
- The same METHOD and path twice is C005. GET, STREAM, and WS on one path collide too, as do paths that differ only in `:param` names.

## Realtime (Node only)

```bp
blueprint "chat" {
  version "1.0.0"
  port 3000
  runtime node
}

@ "A chat room; room(...) needs no model"
WS /ws/rooms/:id {
  <- id uuid required
  on_connect {
    |> join room(id)
    |> log "client joined" level(info)
  }
  on_message {
    |> broadcast room(id) { type: "message" }
    |> close
  }
  on_disconnect {
    |> leave room(id)
  }
}

@ "Live events for one room"
STREAM /rooms/:id/events {
  <- id uuid required
  stream {
    |> on timeout(30s) {
      -> { online: 0 }
    }
  }
}
```

`:param` inputs on STREAM and WS are validated before the stream or upgrade starts (400 refuses the handshake). Python rejects both kinds of endpoint.

Slow clients are bounded: a STREAM client that stays more than `BP_SSE_MAX_PENDING` (default 256) events behind for 5 s (or reaches 16× that) is dropped, and a WS socket with more than `BP_WS_MAX_BUFFERED_BYTES` (default 1 MiB) unsent is closed with code 1013.

## See also

- `bp context cli` — commands and flags
- `bp context errors` — C-codes and `bp explain`
- `bp context targets` — what each target accepts
- `bp context examples` — real specs
