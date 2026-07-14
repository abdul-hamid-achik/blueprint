# Blueprint Language Reference

The `.bp` DSL. Whitespace-significant blocks, `#`-prefix line comments, snake_case identifiers. Every file must open with a `blueprint "<name>" { ... }` block.

## File structure

```bp
blueprint "todo-api" {
  port: 3000
  database: postgres
  cache: redis      # optional
}

model todo {
  id      uuid       primary
  title   string     required
  done    bool       default(false)
  created timestamp  default(now)
  computed label string = title + "!"
}

GET /api/todos {
  -> 200 [{ id: string, title: string, done: bool, created: timestamp }]
  |> all = query todo
  |> return all
}
```

## Top-level declarations

| Decl | Purpose |
|---|---|
| `blueprint "name" { ... }` | Required first block. Project metadata. |
| `secret NAME required` | Declares an env-var dependency. Project fails to start if missing. |
| `model X { fields }` | Database table. Generates schema + migration. |
| `enum X { variant_a, variant_b }` | Named enum type. |
| `type X { fields }` | Named structured type. Use `alias X = ...` for an alias. |
| `external "name" { ... }` | Declares an external service for `call` steps; Node supports env-backed auth and bounded immediate retries. |
| `middleware X { before { ... } after { ... } }` | Request middleware. |
| `fn X(<-input ...) -> output { logic { ... } } [impl ...]` | Pure or impure function. `impl` can be `node`/`python`/`exec`/`http`. |
| `pipe X { logic { ... } }` | Reusable pipeline. |
| `METHOD /path { meta + inputs + steps + outputs }` | HTTP route (GET/POST/PUT/PATCH/DELETE). |
| `STREAM /path { ... }` | SSE endpoint on Node; currently rejected by Python codegen. |
| `WS /path { on_connect { ... } on_message { ... } on_disconnect { ... } }` | WebSocket endpoint on Node; currently rejected by Python codegen. |
| `test name { ... }` | Authored Node/Vitest test; Python rejects authored test blocks. |

## Type system

Primitives: `uuid`, `string`, `text`, `int`, `float`, `bool`, `timestamp`, `json`, `json<T>`. Named: `enum(a, b, c)` (inline) or an `enum` decl referenced by name. `ref(<model>)` is a foreign key.

## Field constraints

| Constraint | Meaning |
|---|---|
| `primary` | Primary key. `uuid primary` gets `DEFAULT gen_random_uuid()`. |
| `required` | NOT NULL. |
| `optional` | Nullable (default unless constrained). |
| `unique` | UNIQUE. |
| `default(value)` | Column default. `default(now)` → `defaultNow()` / `func.now()`. |
| `ref(model)` | FK to `model.id`. |
| `auto` | Auto-bump on `update` (timestamp convention). |
| `index` | Add an index. |

Computed model fields use
`computed <name> <string|text|int|float|money|bool> = <pure expression>`. They
may reference required supported persisted fields and earlier computed fields
through type-compatible literals/unary/binary operators. They are read-only
and non-persisted; calls, optional fields, forward refs, assignment, computed
keys in `save`/`seed`/`update`, and computed `where`/`order` columns reject.
Node materializes them after DB operations, while Python and Effect reject them.

## Pipeline steps (`|>`)

Every endpoint body, middleware `before`/`after`, and fn `logic` is a pipeline of `|>` steps.

| Step | Shape | Meaning |
|---|---|---|
| `fetch <M>(id)` | Single record by id. |
| `query <M> [where(...)] [with(...)] [order(...)] [paginate(p,pp)] [first]` | Collection / single / paginated. `where` supports `==`, `!=`, `<`, `>`, `<=`, `>=`, `in`, `or`, `and`, and text-search shorthand. Node `with(author)` loads one level through `author_id ref(target)`. |
| `save <M> { col: expr, ... }` | Insert. |
| `update <M> { col: expr, ... }` | Update bound row or all matching. |
| `delete <var-or-model>` | Delete row(s). |
| `guard <expr> -> <status> "<msg>"` | Short-circuit with HTTP error. |
| `when <cond> { ... }` / `when <cond>: <stmt>` | Conditional block / inline. |
| `try { ... } recover { ... }` | Exception handling; `recover` sees `error`. In the node target, a `try` body with multiple writes runs in a DB transaction (partial writes roll back); external side effects are not rolled back — compensate in `recover`. |
| `map <coll>: <op>` | For-each over a collection. |
| `<var> = <op>` | Bind step result. |
| `log "msg" [level(error\|info\|warning)]` | Structured log. |
| `inject <binding> as <name>` | (Middleware) Expose to handler context. |
| `return <value>` | Explicit return. |
| `emit "<event>" { data }` | Publish in-process event. |
| `enqueue "<queue>" { data }` | Enqueue a job to a BullMQ worker queue (multi-instance safe). |
| `broadcast room(id) { data }` | (WS) Send to all connections in a room (multi-instance via Redis pub/sub). |
| `join room(id)` / `leave room(id)` | (WS) Join/leave a room. |
| `call <service> GET /path` | External service call. |
| `sleep <duration>` | Delay execution. |

For Node external services, `auth` supports `bearer`, `jwt`, `basic`, or
`api_key` with exactly one declared `secret.NAME` or `env.NAME`; generated code
sets `Authorization` or `X-API-Key`. `retry N` means N additional immediate
attempts for network/timeout failures and HTTP 408/429/5xx, with a fresh timeout
for each attempt. Other 4xx responses are not retried, and malformed config
fails before files are returned.

On Python routes, supported GET/DELETE inputs become FastAPI `Query`,
POST/PUT/PATCH inputs become embedded `Body`, and `header.X-Name` becomes an
aliased `Header` parameter. `env.FIELD` must be backed by a declared secret or
generated infrastructure setting. The target remains fail-closed for its
documented unsupported declarations and expression/middleware shapes. Bare
`query model where(q)` is text search only for a string/text endpoint input;
dynamic filter accumulators and other bound values there are rejected. Field
access on JSON/map inputs or JSON-returning function results also fails closed
until Python can emit dictionary indexing. Direct header/env expressions work,
but raw interpolation of header/env or dictionary-backed values rejects.

Across Node and Python, declaring any model activates the full Postgres layer;
`database postgres` with no models still emits an importable empty schema/Base.
Blueprint settings are unique, and the checker validates the version/port and
runtime/database/cache/storage value shapes before codegen.

Node relationship loading is a nullable one-level LEFT JOIN on `query` only.
Aliases, self joins, repeated target models, nested eager loading, dynamic or
duplicate relationship names, field collisions, legacy positional/block query
arguments, and `fetch ... with(...)` reject. Python and Effect reject
`with(...)` before output.

## Endpoint inputs / outputs

```bp
POST /api/todos {
  <- title string required
  <- done bool default(false)
  -> 201 { id: string, title: string }
  -> 400 { error: string }
  |> created = save todo { title: title, done: done }
  |> return { id: created.id, title: created.title }
}
```

`<-` declares an input (parsed from path/query/body by HTTP method). `->` declares a possible response with a literal shape — these become the OpenAPI spec via `bp docs <file>.bp`.

## See also

- `bp context cli` — running the compiler
- `bp context errors` — what `Cxxx` codes mean
- `bp context examples` — see real specs
- `bp context codegen` — what the generated project looks like
