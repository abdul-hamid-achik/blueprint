# Error codes

Blueprint diagnostics carry an optional structured code so you can look up the
long-form explanation and a worked fix. Codes appear in brackets after the
label:

```
error[C004]: /path/to/file.bp:3:1

  model foo { id uuid primary }
  ^

  duplicate model name "foo" (previously defined at /path/to/file.bp:2:1)
  Hint: Rename one of the model declarations
```

You can also look a code up directly:

```bash
bp explain C004
```

Codes are namespaced by the pass that emits them:

| Prefix | Pass | Notes |
|--------|------|-------|
| `P###` | Parser | Syntax errors. Not yet codified — coming next. |
| `C###` | Checker | Semantic errors. First batch below. |
| `R###` | Resolver | Reserved. Resolver does not currently emit user-facing errors. |
| `L###` | Linter | Reserved. Style warnings will be codified next. |
| `G###` | Codegen | Reserved. |

Not every error has a code today; codes are added as we document them. The
formatter omits the brackets entirely when `Code` is empty.

---

## Checker codes (C###)

### C001 — missing blueprint block

Every `.bp` source file must start with a `blueprint` declaration that names
the service and configures its runtime.

```bp
@ "A todo API"
blueprint "todo-api" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
}
```

If the parser sees a top-level construct (model, endpoint, etc.) before the
blueprint block, you get `C001`.

### C002 — blueprint name empty

`blueprint "" { ... }` is rejected. The name flows into generated paths,
`package.json`, and (eventually) a Docker image tag, so it must be non-empty.
Use lowercase kebab-case.

### C003 — blueprint block missing required field

`version` and `runtime` are both required on the `blueprint` block. They drive
the generated `package.json` and the codegen target selection respectively.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}
```

### C004 — duplicate top-level name

Two declarations share a name within the same namespace. Common shapes:

```bp
model user { ... }
model user { ... }    # C004: rename one
```

The error message points at the second declaration and quotes the first one's
file:line so you can pick which to keep.

### C005 — duplicate endpoint

Two endpoints share the same `METHOD + PATH`. Even when they differ in body or
guards, the router can't dispatch between them.

```bp
GET /api/users/:id { ... }
GET /api/users/:id { ... }   # C005: distinguish by method or path
```

### C006 — unknown function

A call to a function name that isn't a Blueprint builtin, isn't declared via
`fn`, and isn't a declared `pipe`. The hint includes a "did you mean?"
suggestion for close matches.

```bp
|> hash = sha256(password)   # C006 if `sha256` isn't declared
```

Declare it with `fn sha256 { ... impl node { module: "./internal/hash" } }`,
or use one of the documented builtins (`query`, `save`, `fetch`, …).

### C007 — unknown middleware

`use <name>` on an endpoint references a middleware that isn't declared and
isn't a Blueprint builtin (`cors`, `logger`, …).

```bp
GET /api/secret {
  use require_auth   # C007 if no `middleware require_auth { ... }` exists
  -> 200 "ok"
}
```

### C008 — identifier must be snake_case

Identifiers in `.bp` source — model names, field names, fn names, variable
bindings — must be `snake_case`. The error names which thing is offending so
you don't have to guess.

```bp
model Todo { ... }    # C008: rename to `todo`
fn HashPassword { ... }  # C008: rename to `hash_password`
```

### C009 — path parameter must be snake_case

URL path parameters (the `:name` segments) must be `snake_case` so they
match field names and Zod schemas cleanly.

```bp
GET /api/users/:userId { ... }   # C009: rename to `:user_id`
```

### C010 — duplicate field in model

A model declares two fields with the same name. The error points at the
second declaration and quotes the first's location.

```bp
model user {
  email string required
  email string required   # C010
}
```

### C011 — arrow statement out of order

Within a block, statements must appear in the canonical order: `<-` inputs
first, then `|>` steps (and `guard`/`when`/`try`), then `->` outputs. The
parser accepts the wrong order so it can still report later errors, but the
checker rejects it.

```bp
POST /api/x {
  -> 200 "ok"
  <- name string required   # C011: <- must come before ->
}
```

### C012 — try/recover cannot be nested

A `try { ... } recover { ... }` block cannot contain another `try`/`recover`.
Flatten the error handling or push the inner failure into its own `pipe`.

```bp
|> try {
  |> try { ... } recover { ... }   # C012
} recover { ... }
```

---

## Adding a new code

1. Add a `Code...` constant in the relevant pass (e.g.
   `internal/checker/checker.go`).
2. Migrate the corresponding `addError(...)` call to `addErrorCode(..., code, ...)`.
3. Document the code here with a worked example.
4. Update the BACKLOG row for that pass with the new code if you're growing
   a batch.

See `internal/diag/diag.go` for the rendering contract; the only requirement
is that `Code` is short, prefix-namespaced, and stable across releases.
