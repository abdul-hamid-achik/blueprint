# Testing Blueprint Services

Blueprint provides three complementary test paths:

1. **Generated contract tests** come from endpoint declarations. They check
   that routes boot, accept a representative request, and return a declared
   status and response shape.
2. **Authored `test { }` blocks** describe specific examples in `.bp` source.
   The Node target emits them as Vitest files. The Python target rejects them
   until it can translate them faithfully.
3. **Generated property tests** are an opt-in Node layer. They use fast-check to
   send deterministic, generated valid requests through supported REST routes
   and accept only statuses the route can declare.

| Target | Command | Runner | Database | Authored tests | Properties |
|---|---|---|---|---|---|
| Node (default) | `bp test service.bp` | Vitest | In-process PGlite | Supported subset described below | Opt in with `--gen-property-tests` |
| Python | `bp test service.bp --target python` | pytest | Postgres testcontainer | Rejected; not translated yet | Rejected |

## Quick start

You do not need to write a test block to get a first signal. `bp test` enables
contract-test generation automatically:

```bash
# Node: no external database or Docker required
bp test examples/todo-api.bp

# Python: requires uv and a running Docker daemon
bp test examples/todo-api.bp --target python
```

To generate tests without running them, use `bp build --gen-tests`:

```bash
bp build service.bp --gen-tests
bp build service.bp --target python --gen-tests
bp build service.bp --gen-property-tests # Node; also emits contract tests
```

`bp diff` also accepts `--gen-tests` and `--gen-property-tests`, which is useful
when reviewing how a source change affects generated suites.

## Generated contract tests

Blueprint creates one happy-path contract case per endpoint. Each case:

- builds a representative request from the endpoint inputs;
- seeds foreign-key parents and path-parameter records when needed;
- calls the application without opening a network port;
- accepts only statuses declared by the endpoint, its guards, and standard
  validation/error fallbacks; and
- checks the keys of successful object responses.

These tests are deliberately structural. They catch broken routing,
validation, imports, database wiring, and response contracts; they cannot infer
all business rules. Add authored tests or hand-written tests for behavior that
matters to your application.

### Node harness

```text
generated/
├── vitest.config.ts
└── test/
    ├── _harness/
    │   ├── db.ts
    │   ├── ddl.ts
    │   └── setup.ts
    └── generated/
        └── <resource>.test.ts
```

The harness mocks `src/lib/db` with PGlite and resets every table between
tests. `bp test service.bp` therefore needs neither `DATABASE_URL` nor a live
Postgres server.

### Python harness

```text
generated/
└── tests/
    ├── conftest.py
    └── test_<resource>.py
```

The pytest harness starts `postgres:16-alpine` through
`testcontainers[postgresql]`, creates the SQLAlchemy metadata once, and
truncates tables between cases. Docker is required. `uv run` resolves the
generated development dependencies before invoking pytest.

Python contract tests cover generated endpoint contracts only. Blueprint
`test`, `fixture`, and `test_group` declarations currently make Python codegen
fail with an unsupported-feature error, which prevents a successful build from
silently omitting them. Keep Python-specific tests in an external application
test suite; generated output remains manifest-owned and may be replaced.

## Generated property tests (Node)

Use the same flag with `build`, `diff`, or `test`:

```bash
bp build service.bp --gen-property-tests
bp diff service.bp --gen-property-tests
bp test service.bp --gen-property-tests
```

The flag is Node-only, cannot be combined with `--frontend-only`, and implies
`--gen-tests`. It adds `fast-check` and emits one
`test/generated/<resource>.property.test.ts` file per REST resource. Each route
uses a stable method/path-derived seed, runs 32 generated valid requests, and
reports fast-check's seed/path for replay on failure. Database-backed projects
reset PGlite before every generated request; database-free projects omit the DB
harness and PGlite dependency.

Arbitraries honor supported types and declared bounds: string/text/int/uuid
path values; scalar or enum query values (including aliases resolving to
those); and JSON-body primitives,
named enums/aliases/structural types, lists, maps with string keys, and optional
values. The assertion admits only
statuses reachable from the route, guards, middleware, and `on_error`; it does
not add a blanket 400 or 500 success case.

Property mode fails closed for the whole source rather than silently omitting a
route. Current unsupported surfaces include optional or unsupported path types,
constraints with no generated valid value (including impossible path,
email, or URL lengths), structural query values,
file/MIME transport, auth/rate-limit/header-aware routes, ref-backed fields in
`save`/`seed`/`update` blocks, reachable recursive inline `fn`/`pipe` call
graphs, native or user implementation calls, and non-hermetic external HTTP,
queue, storage, analytics, event, realtime, sleep, or wall-clock behavior.
Ref-backed writes reject because each run starts from an empty database and the
generator does not synthesize referenced parent rows. A source also needs at
least one REST endpoint. This makes the generated suite an honest valid-request
boundary check, not proof of business correctness or an adversarial
invalid-input fuzzer.

## Authored tests on the Node target

An authored test has a target, an optional setup, one request, and expectations.
The grammar also reserves a cleanup block, but the generated Node runner rejects
nonempty cleanup until it can emit teardown faithfully:

```bp
@ "Creating a todo returns its title"
test create_todo {
  target POST /api/todos

  request {
    body {
      title: "Buy milk",
    }
  }

  expect {
    status 201
    body.id is uuid
    body.title == "Buy milk"
  }
}
```

Run authored tests together with the generated contract suite:

```bash
bp test service.bp
```

Before it builds, installs packages, or starts Vitest, `bp test` preflights
every authored Node test. If a test uses a surface below that the emitter cannot
preserve, the command exits with an actionable diagnostic and leaves the output
directory untouched. `bp build --gen-tests` remains available when you intend
to replace or supplement the emitted case with hand-written Vitest.

### Current support boundary

The parser accepts more test syntax than the Node generator implements. Use
this table as the runtime contract:

| Surface | Status in generated Vitest |
|---|---|
| `target METHOD /literal/path` | Supported |
| `setup { ... }` | Only `seed`/`save` data writes and a simple unbound `log` are supported; all other statements/calls reject |
| JSON `request { body { ... } }` | Supported for non-GET/HEAD targets; the lowercase `body` key may appear once |
| `auth api_key(expr)` | Emits `X-API-Key` |
| `auth bearer(expr)` / `auth jwt(expr)` | Emits `Authorization: Bearer …` |
| `auth basic(expr)` | Emits the expression after `Basic`; Blueprint does not encode username/password pairs |
| Request keys | Only lowercase `body` and `auth`, at most once each; calls and interpolation reject |
| `request repeat(N)` | Supported only for positive `N`; `repeat(0)` rejects |
| Custom request headers | `bp test` rejects before build; use hand-written Vitest |
| Multipart/form-data, file inputs, or `fixture(...)` request values | `bp test` rejects before build; emitted authored bodies are JSON-only |
| A target containing `:path` placeholders | `bp test` rejects; use a literal target path |
| `cleanup { ... }` | `bp test` rejects because cleanup is not emitted yet |
| `test_group` | Name grouping is accepted; nonempty `shared_setup` is rejected because it is not emitted |
| `duration < …` | `bp test` rejects because no timing assertion is emitted |
| Assertion forms | Must map to an executable emitter form with safe literal RHS values; see the table below |
| Native `impl node`/`impl exec` dependency | Every dependency referenced by any app-loaded route, realtime/background/subscription, or middleware module must exist and be implemented; the check is not limited to the target route |

Unsupported rows are fail-closed boundaries, not no-ops to rely on. Prefer a
hand-written Vitest file when a case needs custom headers, multipart bodies, dynamic path
parameters, per-test teardown, or precise timing.

## Setup and database assertions

Setup uses a deliberately narrow arrow subset: `seed`/`save` data writes may
bind rows, and a simple unbound `log` is allowed. Those row bindings are hoisted
so the request body and expectations can reference scalar fields. Every other
setup statement or call is rejected before generation:

```bp
test create_second_item {
  target POST /api/items

  setup {
    |> existing = save item { name: "First" }
  }

  request {
    body { name: "Second" }
  }

  expect {
    status 201
    model item where(name == "Second") exists
  }
}
```

Do not use `delete_all`; it is not a Blueprint operation. The PGlite contract
harness already truncates its tables between tests. `cleanup` syntax is
reserved for future emitted teardown and should not be used as the only source
of test isolation today.

## Request bodies and authentication

Authored requests currently emit JSON bodies:

```bp
request {
  auth bearer(token)
  body {
    name: "Widget",
    price: 999,
  }
}
```

Request keys are case-sensitive: use lowercase `body` and `auth`, no more than
once each. GET and HEAD targets cannot carry a body. Request values may not use
function calls or string interpolation. Auth accepts a scalar literal or a
scalar field such as `key.key_hash`; passing the entire setup row (`auth
bearer(key)`) is rejected.

For API-key middleware, a setup binding can feed the generated header:

```bp
setup {
  |> key = save api_key { key_hash: "test-key" }
}

request {
  auth api_key(key.key_hash)
  body { title: "Private item" }
}
```

This only configures the test request. Whether the application enforces that
authentication depends on the middleware generated for the endpoint; see the
[Language Reference](/language-reference) support notes.

## Assertion reference

| Blueprint assertion | Generated Vitest behavior |
|---|---|
| `status 200` | Exact status equality |
| `status 4xx` | Status is between 400 and 499 |
| `body.name == "value"` | Strict equality |
| `body.name != "value"` | Strict inequality |
| `body.id is uuid` | UUID-shaped string |
| `body.name is string` | JavaScript type check |
| `body.count is int` | JavaScript number check |
| `body.active is bool` | JavaScript boolean check |
| `body.value exists` | Value is defined |
| `body.value not exists` | Value is undefined (`not_exists` is rejected) |
| `header.Location == "/items/1"` | Response-header equality; hyphenated names such as `Content-Type` reject |
| `model item where(name == "x") exists` | Drizzle query returns at least one row |
| `model item where(name == "x") not exists` | Drizzle query returns no rows |
| `last_status 429` | Last response from a repeated request has status 429 |
| `duration < 2s` | `bp test` rejects; ordinary generated output contains only a TODO marker |

Equality and inequality assertions require a literal RHS. String literals that
contain an apostrophe, backslash, or newline are rejected because the current
emitter cannot escape them safely. A test may contain at most one model
assertion; additional model assertions would redeclare the generated `_row`
binding and therefore fail preflight.

## Repeated requests

Use `repeat` for a small deterministic sequence, such as a rate-limit test:

```bp
test rate_limit {
  target POST /api/items

  request repeat(15) {
    body { name: "Repeated" }
  }

  expect {
    last_status 429
  }
}
```

The count must be positive; `repeat(0)` is rejected.

The generator sends requests sequentially and retains the final response for
`last_status`.

## Fixtures

Blueprint parses file and generated fixtures:

```bp
fixture "sample.png" from "testdata/sample.png"
fixture "large.png" generated { type: "image/png", size: 15mb }
```

The parser and ordinary `bp build --gen-tests` can represent file fixtures, but
`bp test` rejects a `fixture(...)` request value because Blueprint neither
copies source assets into the output tree nor emits multipart `FormData`.
For file-upload tests today, write a Vitest test that constructs `FormData` and
manages the fixture path explicitly.

## Running the generated suites directly

```bash
# Node contract suite (use --gen-property-tests for contract + properties)
bp build service.bp --gen-tests
cd generated
bun install
bun run test

# Python
bp build service.bp --target python --gen-tests
cd generated
uv run pytest
```

Use `bunx vitest --watch` from a Node output directory for watch mode.

## Troubleshooting

### Node cannot find PGlite or Vitest

Rebuild with the harness and reinstall after `package.json` changes:

```bash
bp build service.bp --gen-tests
cd generated
bun install
bun run test
```

The `bp test` command performs this install check automatically.

### Python tests cannot connect to Docker

Confirm Docker is running and that `docker ps` succeeds, then rerun:

```bash
bp test service.bp --target python
```

### A generated contract case accepts several statuses

That is intentional. Contract tests verify declared behavior and response
shape, not one inferred business outcome. Add an authored Node test or a
hand-written target-specific test when one exact status is required.

### Property generation rejects a route

The rejection is intentional: property mode will not claim coverage while
skipping a route or invoking unproven external state. Remove
`--gen-property-tests`, isolate the unsupported behavior behind a separately
tested boundary, or add a hand-written property suite that supplies the
required credentials and mocks.

## Next steps

- [CLI Reference](/cli-reference#bp-test) for command flags
- [Generated Output](/generated-output) for the emitted project layout
- [Production Readiness](/production-readiness) for current release gates
