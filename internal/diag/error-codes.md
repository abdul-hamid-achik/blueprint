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
| `C###` | Checker | Semantic errors. `C001`–`C050` documented below. |
| `R###` | Resolver | Reserved. Resolver does not currently emit user-facing errors. |
| `G###` | Codegen | Reserved. |

(Note: the linter's style warnings will move to their own namespace in a
follow-up; for now `L###` belongs to the lexer.)

Lexer and parser errors do not all have codes yet; checker errors always do. The
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
## Checker codes (C###)

Every checker diagnostic carries one of these codes. Several codes group the
same mistake class, so the example shown is one way to trigger it, not the only
way. Each failing example below is complete and produces its code when checked.

### C001 — missing blueprint block

Every `.bp` source file must start with a `blueprint` declaration that names
the service and configures its runtime.

```bp
# A todo API, but the blueprint block is missing
model todo {
  id uuid primary
}
```

`bp check` reports this shape as `P001` because the parser also requires the
blueprint block first. `C001` is the checker's own report for the same case
when the tolerant parse used by the editor reaches the checker.

Start the file with the blueprint block:

```bp
@ "A todo API"
blueprint "todo-api" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
}
```

### C002 — blueprint name empty

`blueprint "" { ... }` is rejected. The name flows into generated paths,
`package.json`, and (eventually) a Docker image tag, so it must be non-empty.
Use lowercase kebab-case.

```bp
blueprint "" {   # C002: give the service a name
  version "1.0.0"
  runtime node
}
```

### C003 — blueprint block missing required field

`version` and `runtime` are both required on the `blueprint` block. They drive
the generated `package.json` and the codegen target selection respectively.

```bp
blueprint "todo-api" {   # C003: add version
  runtime node
}
```

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}
```

### C004 — duplicate top-level name

Two declarations share a name within the same namespace. Common shapes:

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model user {
  id uuid primary
}

model user {   # C004: rename one
  id uuid primary
}
```

The error message points at the second declaration and quotes the first one's
file:line so you can pick which to keep.

### C005 — duplicate endpoint

Two endpoints share the same `METHOD + PATH`. Even when they differ in body or
guards, the router can't dispatch between them.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/users/:id {
  -> 200 "one"
}

GET /api/users/:id {   # C005: distinguish by method or path
  -> 200 "two"
}
```

### C006 — unknown function

A call to a function name that isn't a Blueprint builtin, isn't declared via
`fn`, and isn't a declared `pipe`. The hint includes a "did you mean?"
suggestion for close matches.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

POST /api/login {
  <- password string required
  |> hash = sha256(password)   # C006 if `sha256` isn't declared
  -> 200 { hash: hash }
}
```

Declare it with `fn sha256 { ... impl node { module: "./internal/hash" } }`,
or use one of the documented builtins (`query`, `save`, `fetch`, …).

### C007 — unknown middleware

`use <name>` on an endpoint references a middleware that isn't declared and
isn't a Blueprint builtin (`cors`, `logger`, …).

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

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
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model Todo {   # C008: rename to `todo`
  id uuid primary
}
```

Other offenders take the same fix: `fn HashPassword` becomes
`fn hash_password`.

### C009 — path parameter must be snake_case

URL path parameters (the `:name` segments) must be `snake_case` so they
match field names and Zod schemas cleanly.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/users/:userId {   # C009: rename to `:user_id`
  -> 200 "ok"
}
```

### C010 — duplicate field in model

A model declares two fields with the same name. The error points at the
second declaration and quotes the first's location.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model user {
  id    uuid primary
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
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

POST /api/x {
  -> 200 "ok"
  <- name string required   # C011: <- must come before ->
}
```

### C012 — try/recover cannot be nested

A `try { ... } recover { ... }` block cannot contain another `try`/`recover`.
Flatten the error handling or push the inner failure into its own `pipe`.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/x {
  |> try {
    |> try {                 # C012
      |> log "inner"
    } recover {
      |> log "inner failed"
    }
  } recover {
    |> log "outer failed"
  }
  -> 200 "ok"
}
```

### C013 — unknown type

A field or signature references a type that isn't a Blueprint primitive and
isn't declared as a `type`, `alias`, or `enum`.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

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

`ref(<name>)` must point at a `model` declared in the same compilation. Common
shapes:

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model post {
  id        uuid primary
  author_id uuid required ref(autor)   # C014: did you mean "author"?
}
```

Declare the model first or fix the spelling. The hint includes a "did you
mean?" suggestion for close matches over the known model names.

### C015 — unknown external service

A `call <service> METHOD /path` or a `subscribe "event" from(<service>)` names
a service that has no `external "..." { ... }` block. The checker emits C015
when no such block exists.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/charge {
  |> resp = call payments POST /charge   # C015 unless `external "payments"` exists
  -> 200 resp
}
```

Declare the service:

```bp
external "payments" {
  base_url "https://api.example.com"
}
```

### C016 — duplicate blueprint setting

The same setting appears twice in the `blueprint` block. Each setting takes
one value, so the second one is ambiguous.

```bp
blueprint "todo-api" {
  version "1.0.0"
  version "2.0.0"   # C016: keep one version
  runtime node
}
```

Keep the value you want:

```bp
blueprint "todo-api" {
  version "2.0.0"
  runtime node
}
```

### C017 — invalid setting value

A setting's value is outside its allowed set: a `version` that isn't a string,
a `port` that isn't an integer or is outside `1..65535`, a `runtime`,
`database`, `cache`, or `storage` value that isn't a bare identifier, or an
analytics `sink` kind that isn't supported.

```bp
blueprint "todo-api" {
  version "1.0.0"
  port    70000   # C017: ports run from 1 through 65535
  runtime node
}
```

```bp
blueprint "todo-api" {
  version "1.0.0"
  port    3000
  runtime node
}
```

### C018 — computed field operand not allowed

Computed fields are pure and total. A computed expression can only read required
persisted fields or earlier computed fields, use literals, parentheses, and pure
operators that fit the operand types. A nullable field, a field of a non-scalar
type, a call, or an operator that doesn't apply to the operand type is rejected.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model product {
  id       uuid   primary
  nickname string               # optional, so it may be null
  computed label string = nickname + "!"   # C018
}
```

Make the source required, or compute the fallback in application code:

```bp
model product {
  id       uuid   primary
  nickname string required
  computed label string = nickname + "!"
}
```

### C019 — computed field type mismatch

A computed field's declared result type must be one of the supported result types
(`string`, `text`, `int`, `float`, `money`, `bool`) and must match what the
expression produces.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model product {
  id   uuid   primary
  name string required
  computed label int = name   # C019: name is a string
}
```

Declare the type the expression produces:

```bp
model product {
  id   uuid   primary
  name string required
  computed label string = name
}
```

### C020 — value type mismatch

A value's type doesn't match the place it goes: a `fn` or `pipe` argument, an
input that is assigned a value of another type, or a field in a `save`/`update`
body.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model product {
  id   uuid primary
  name string required
}

POST /api/products {
  <- name string required
  |> product = save product { name: 42 }   # C020: name expects a string
  -> 201 product
}
```

Pass a value of the declared type, or change the declaration to match what you
mean to store.

### C021 — reserved built-in name

A `fn` or `pipe` can't take the name of a built-in operation such as `query`,
`save`, or `fetch`. A call such as `query(...)` must keep meaning the builtin.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

pipe query {   # C021: rename the pipe
  <- value string
  -> value
}
```

Rename it, for example to `search_query`.

### C022 — invalid input declaration

A `pipe` takes at most one `<-` input, and an input name can be declared only
once in the same block.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

pipe combine {
  <- left string
  <- right string   # C022: a pipe transforms one value
  -> left
}
```

Keep one input and pass the rest as fields of that value, or split the work
into two pipes.

### C023 — duplicate enum variant

Two variants of one `enum` share a name, so a value can't be mapped to exactly
one variant.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

enum Status {
  pending
  pending   # C023
}
```

Remove the duplicate or rename it.

### C024 — invalid locale declaration

A `locale` declaration is malformed: its code is empty, its fallback points at
itself, or more than one locale is marked `default`.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

locale en default
locale fr default   # C024: keep exactly one default
```

Keep one `default` locale and give the others a fallback chain:

```bp
locale en default
locale "fr-FR" fallback(en)
```

### C025 — unknown locale reference

A locale's `fallback(...)` names a locale that isn't declared.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

locale en default
locale "fr-FR" fallback(de)   # C025: `de` is not declared
```

Declare the fallback locale first, or point the fallback at a declared one.

### C026 — duplicate translation entry

A `translation` block repeats a `key`, or a locale bundle repeats a key or
appears twice for the same locale.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

locale en default

translation greeting {
  key "hello"
  key "hello"   # C026
}
```

Declare each key once, and keep one bundle per locale.

### C027 — unknown translation reference

A translation bundle assigns a value to a key the namespace didn't declare, or a
`tkey(...)` names a translation namespace that doesn't exist.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

locale en default

translation greeting {
  key "hello"
  locale en {
    "bye": "Bye"   # C027: declare `key "bye"` first
  }
}
```

Declare the key in the translation block before assigning a localized value to
it.

### C028 — duplicate state transition or analytics event

A `state` block repeats a transition, or an `analytics` block repeats an event.
Each transition or event name must be declared once.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

state order_status {
  draft -> paid
  draft -> paid   # C028
}
```

Remove the repeated line.

### C029 — save missing required clause

A `save` block must name the model it saves and its `version_field`.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model slot {
  id uuid primary
  save_version int required
}

save progress {   # C029: add `model slot`
  version_field save_version
  latest 1
}
```

Add the missing clause:

```bp
save progress {
  model slot
  version_field save_version
  latest 1
}
```

### C030 — invalid save version or migration

A `save` declares `latest` below 1, or a `migrate` step doesn't increase the
version, goes beyond `latest`, or repeats an earlier step.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model slot {
  id uuid primary
  save_version int required
}

save progress {
  model slot
  version_field save_version
  latest 0   # C030: latest must be at least 1
}
```

### C031 — missing or unknown model reference

A `save` or data operation names a model (or a bound value) that isn't declared,
or a data operation names no model at all.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/x {
  |> g = save ghost { name: "x" }   # C031: `ghost` is not a declared model
  -> 200 g
}
```

Declare the model or fix its name. For `update` and `delete`, the name may also
be a value bound earlier in the same block.

### C032 — unknown model field

A `version_field`, a computed field, a field access, or a `with(...)`
relationship refers to a field or relationship that the model doesn't have.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model slot {
  id uuid primary
  save_version int required
}

save progress {
  model slot
  version_field nope   # C032
  latest 1
}
```

Point the reference at a field declared on the model, or add the field.

### C033 — unknown test in test group

A `test_group` lists a test that no `test` block declares.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

test_group all_tests {
  tests [nonexistent_test]   # C033
}
```

Declare the test, or remove it from the group.

### C034 — duplicate local binding

A `|> name = ...` step binds a name that is already bound in the same block.
Only `<-` inputs may be reassigned, so the second binding would hide the first.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/x {
  |> total = 1
  |> total = 2   # C034: choose a new name
  -> 200 total
}
```

Give the second value its own name.

### C035 — unbound identifier

An identifier is used before anything binds it in the block.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/x {
  -> 200 missing_value   # C035
}
```

Declare it with `<-`, bind it with `|> name = ...`, or fix the spelling.

### C036 — computed field is read-only

A `save`, `update`, or `seed` body assigns a computed field. Computed values are
derived from persisted fields and aren't stored.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model person {
  id         uuid primary
  first_name string required
  computed full_name string = first_name
}

POST /api/people {
  <- first_name string required
  |> p = save person { full_name: "x", first_name: first_name }   # C036
  -> 201 p
}
```

Write the persisted fields it is derived from, and let the computed value follow.

### C037 — duplicate query modifier

A data operation repeats a modifier such as `first(...)`, or a `with(...)`
list names the same relationship twice.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model post {
  id    uuid primary
  title string required
}

GET /api/posts {
  |> p = query post first first   # C037: keep one first modifier
  -> 200 p
}
```

Keep one modifier of each kind, and name each relationship once.

### C038 — invalid with(...) clause

A `with(...)` clause is used on an operation that doesn't support relationships,
is empty, or lists something other than relationship names.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model author {
  id uuid primary
}

model post {
  id        uuid primary
  author_id uuid required ref(author)
}

GET /api/posts {
  |> p = query post with()   # C038: name a relationship, e.g. with(author)
  -> 200 p
}
```

Name a ref-backed relationship, for example `with(author)`, on a query operation.

### C039 — conflicting query modifiers

A query combines modifiers that can't be used together: `paginate(...)` with
`first`, or the structured `with(...)` with the legacy positional or block
arguments.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model post {
  id    uuid primary
  title string required
}

GET /api/posts {
  |> p = query post paginate(1, 10) first   # C039
  -> 200 p
}
```

Choose a paginated collection, or a single first record.

### C040 — relationship target not joinable

A `with(...)` relationship joins through a `ref` to a target model whose `id`
isn't a unique persisted field, or whose type doesn't match the `ref` field.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model author {
  name string required   # no id field to join against
}

model post {
  id        uuid primary
  author_id uuid required ref(author)
}

GET /api/posts {
  |> p = query post with(author)   # C040
  -> 200 p
}
```

Give the target an `id` field marked `primary` or `unique`, with the same type as
the `ref` field.

### C041 — unsupported join shape

A `with(...)` relationship needs a join this release doesn't support: a self
relationship (which would need a join alias), or a second relationship to a
model that is already joined.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model node {
  id        uuid primary
  parent_id uuid ref(node)
}

GET /api/nodes {
  |> n = query node with(parent)   # C041: self-join aliases are not supported
  -> 200 n
}
```

Omit the relationship from `with(...)`, and load it in application code.

### C042 — storage operation without storage

A storage operation such as `upload(...)` is used, but the `blueprint` block
doesn't configure `storage`.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/x {
  <- file string required
  <- bucket string required
  |> u = upload(file, bucket)   # C042
  -> 200 u
}
```

Add `storage s3` to the blueprint block, or remove the storage operation.

### C043 — unknown state, save, or secret reference

A `transition(...)` names a state machine that isn't declared, `upgrade_save(...)`
names a save that isn't declared, or `auth webhook_sig` uses a secret that isn't
declared.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/x {
  |> t = transition(order_status, draft, paid)   # C043: `order_status` is not declared
  -> 200 t
}
```

Declare it with `state <name> { ... }`, `save <name> { ... }`, or
`secret <NAME> required`.

### C044 — wrong symbol kind

A name is used where a declaration of another kind is required: a call to an
`enum`, a `use` that names a `fn` instead of a middleware, or a translation
namespace that names some other declaration.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

enum Status {
  pending
  done
}

GET /api/x {
  |> x = Status(1)   # C044: Status is an enum, not a function or pipe
  -> 200 x
}
```

Use a declared fn or pipe name here.

### C045 — wrong argument count

A `fn` or `pipe` is called with a different number of arguments than it declares.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

pipe double {
  <- value int
  -> value
}

GET /api/x {
  |> x = pipe double(1, 2)   # C045: double takes 1 argument
  -> 200 x
}
```

Pass the declared number of arguments.

### C046 — invalid call target

A `call` node has no service name, or its service isn't an identifier.

The parser always reads an identifier after `call`, so source files can't reach
this code today: `call` without a service is a syntax error. The check guards
call nodes built without one, such as ones produced by tooling. There is no
failing `.bp` example for this code; the fix is to write `call service_name GET
/path`, with `service_name` declared as an `external` block.

### C047 — webhook signature secret missing

`auth webhook_sig` must say which secret verifies the signature.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/hook {
  auth webhook_sig   # C047
  -> 200 "ok"
}
```

Use `auth webhook_sig using(secret.WEBHOOK_SECRET)` and declare that secret with
`secret WEBHOOK_SECRET required`.

### C048 — name uses the wrong letter case

A name or path segment doesn't use the case its kind requires: `secret` and `env`
names are `SCREAMING_SNAKE_CASE`, enum and type names are `PascalCase`, and path
segments are `kebab-case`.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

secret apiKey required   # C048: secret names are SCREAMING_SNAKE_CASE
```

Rename it, for example `API_KEY`. A path segment such as `userProfiles` becomes
`user-profiles`.

### C049 — relationship name collides with a field

A `with(...)` relationship has the same name as a persisted field on the source
model, so the joined value would shadow the field.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

model author {
  id   uuid primary
  name string required
}

model post {
  id        uuid primary
  author    uuid required   # C049: same name as the relationship
  author_id uuid required ref(author)
}

GET /api/posts {
  |> p = query post with(author)
  -> 200 p
}
```

Rename the relationship or the colliding field.

### C050 — operator applied to incompatible operand types

An operator's two operands both have a known type, and the operator does not
accept that pair. The checker reports the cases it can prove:

- a comparison (`==`, `!=`, `<`, `>`, `<=`, `>=`, `in`) between a string and a
  number, a bool and a non-bool, a number and a timestamp, or two different
  named enums;
- arithmetic on a bool, or `+`, `-`, `*`, `/` between a string and a number
  (`+` is the one operator that joins two strings);
- unary `-` on a bool or a string;
- the same rules for `where(...)` predicates, where a column name takes that
  column's declared type.

Numbers (`int`, `float`, `money`) mix freely. An optional value is checked by
its underlying type, and comparisons with `null` are never reported. Enum values
compared with strings are not reported either. Anything whose type is unknown
(`json`, model values, call results without a declared output) is never
reported.

```bp
blueprint "todo-api" {
  version "1.0.0"
  runtime node
}

GET /api/check {
  <- name  string required
  <- limit int required
  |> guard name > limit -> 400 "name too long"   # C050: cannot compare string with int
  -> 200 "ok"
}
```

Compare values of the same kind. To build text from other values, use string
interpolation (`"{name} has {count} items"`) rather than `+`.

---

## Adding a new code

1. Add a `Code...` constant in `internal/checker/checker.go` (or the relevant
   pass), with a doc comment.
2. Report the error with `addErrorCode(..., CodeX, ...)`. The checker has no
   uncoded error path, and a test rejects one.
3. Document the code here with a failing example. The first ```` ```bp ```` block in
   the section must be a complete file that produces the code; a test checks it.
4. Mirror the change into `docs/error-codes.md`. The drift test requires the two
   files to be byte-identical.
