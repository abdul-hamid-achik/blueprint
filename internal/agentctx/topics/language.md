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
}

endpoint GET /api/todos {
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
| `type X = ...` | Type alias. |
| `external "name" { ... }` | Declares an external service for `call` steps. |
| `middleware X { before { ... } after { ... } }` | Request middleware. |
| `fn X(<-input ...) -> output { logic { ... } } [impl ...]` | Pure or impure function. `impl` can be `node`/`python`/`exec`/`http`. |
| `pipe X { logic { ... } }` | Reusable pipeline. |
| `endpoint METHOD /path { meta + inputs + steps + outputs }` | HTTP route. |
| `stream METHOD /path { ... }` | SSE endpoint (uses `EventSourceResponse`). |
| `ws /path { on_connect { ... } on_message { ... } on_disconnect { ... } }` | WebSocket endpoint. |
| `test "description" { ... }` | Authored Vitest/pytest test. |

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

## Pipeline steps (`|>`)

Every endpoint body, middleware `before`/`after`, and fn `logic` is a pipeline of `|>` steps.

| Step | Shape | Meaning |
|---|---|---|
| `fetch <M>(id)` | Single record by id. |
| `query <M> [where(...)] [order(...)] [paginate(p,pp)] [first]` | Collection / single / paginated. |
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

## Endpoint inputs / outputs

```bp
endpoint POST /api/todos {
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
