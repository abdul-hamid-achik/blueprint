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
| `L###` | Lexer | Token-level errors. `L001` documented below. |
| `P###` | Parser | Syntax errors. `P001`-`P002` documented below; broader coverage in progress. |
| `C###` | Checker | Semantic errors. `C001`–`C015` documented below. |
| `R###` | Resolver | Reserved. Resolver does not currently emit user-facing errors. |
| `G###` | Codegen | Reserved. |

(Note: the linter's style warnings will move to their own namespace in a
follow-up; for now `L###` belongs to the lexer.)

Not every error has a code today; codes are added as we document them. The
formatter omits the brackets entirely when `Code` is empty.

---

## Lexer codes (L###)

### L001 — lone `|` not part of `|>`

The lexer rejects a `|` that isn't followed by `>`. The only place a pipe
character appears in `.bp` source is the pipeline arrow `|>`, so a bare `|` is
almost always a typo.

```bp
model user |    # L001: did you mean '{' here?
  id uuid primary
}
```

The accompanying hint suggests `|>`. If you meant to start a block, use `{`.

---

## Parser codes (P###)

### P001 — missing blueprint block

The parser saw a top-level construct (model, endpoint, fn, etc.) before any
`blueprint` declaration. Every `.bp` source file must open with a `blueprint`
block that names the service and configures its runtime; the parser uses that
block as the file's anchor.

```bp
secret API_KEY required   # P001 if no `blueprint` block precedes this
```

Move (or add) the `blueprint` declaration to the top of the file:

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

secret API_KEY required
```

The checker emits `C001` for the related case where the parser saw no
top-level constructs at all (empty file).

### P002 — control-flow keyword in step position

Blueprint has no `if`/`else`, `for`, `while`, or `switch` — its flat control-flow
rules replace branching with `guard`/`when` and iteration with `map`. Using one of these keywords as
the first token of a step (right after `|>`) is the most common mistake for
anyone coming from JS/Python, so it gets a dedicated diagnostic instead of a
generic syntax error:

```bp
POST /api/todos {
  |> if title == "" {        # P002: no if/else
    -> 400 "title required"
  }
  -> 200 todo
}
```

Replace the branch with `guard` (early return) or `when` (conditional step):

```bp
POST /api/todos {
  |> guard title == "" -> 400 "title required"
  -> 200 todo
}
```

For iteration, use `map`:

```bp
|> items = map orders: order.total
```

The parser also bounds recovery to the malformed construct itself (the
condition plus its balanced `{ ... }` body, including any chained
`else`/`else if`), so a single stray `if` doesn't cascade into unrelated
errors on the steps that follow it.

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

### C013 — unknown type

A field or signature references a type that isn't a Blueprint primitive and
isn't declared as a `type`, `alias`, or `enum`.

```bp
model product {
  id    uuid       primary
  price NonExistent required   # C013
}
```

Either declare the type:

```bp
type money { amount int; currency string }
```

or replace the reference with a primitive (`int`, `string`, `decimal`, …) or
an existing alias. If the message includes a `did you mean "..."?` suggestion,
that's the closest already-declared name.

### C014 — ref references unknown model

`ref <name>` must point at a `model` declared in the same compilation. Common
shapes:

```bp
model post {
  id      uuid primary
  author  ref autor   # C014: did you mean "author"?
}
```

Declare the model first or fix the spelling. The hint includes a "did you
mean?" suggestion for close matches over the known model names.

### C015 — call references unknown external

`call <service> METHOD /path` requires `<service>` to be declared via an
`external "..." { ... }` block. The checker emits C015 when no such block
exists.

```bp
|> resp = call payments POST /charge   # C015 unless `external "payments"` exists
```

Declare the service:

```bp
external "payments" {
  base_url "https://api.example.com"
}
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
