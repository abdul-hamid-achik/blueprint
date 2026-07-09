# Testing Guide

Blueprint has first-class support for integration tests. Write `test` blocks in your `.bp` file, and `bp test` compiles them to Vitest test files and runs them.

## Quick Start

```bp
@ "Creating a todo returns 201"
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

```bash
bp test todo-api.bp
# Built todo-api.bp -> generated/
# ✓ test/create-todo.test.ts (1)
# Test Files 1 passed (1)
```

---

## Test Block Structure

```bp
@ "Description of what is being tested"
test test_name {
  target METHOD /path

  setup {
    # seed test data
  }

  request {
    # define the HTTP request
  }

  expect {
    # assert on the response
  }

  cleanup {
    # tear down test data
  }
}
```

### Sections

| Section | Required | Purpose |
|---------|----------|---------|
| `target` | Yes | HTTP method and path to test |
| `setup` | No | Seed data before the request |
| `request` | Yes | Define the HTTP request (body, headers, auth) |
| `expect` | Yes | Assert on response status, body, headers, timing, and database state |
| `cleanup` | No | Remove seeded data after the test |

---

## Seeding Test Data

Use `seed` in the `setup` block to insert records before the test runs:

```bp
test update_user {
  target PATCH /api/users/:id

  setup {
    |> user = seed user { name: "Alice", email: "alice@test.com" }
  }

  request {
    body {
      name: "Alice Updated",
    }
  }

  expect {
    status 200
    body.name == "Alice Updated"
  }

  cleanup {
    |> delete_all user
  }
}
```

Seeded variables (like `user`) are available in `request` and `expect` blocks. For example, the path `/api/users/:id` is resolved using the seeded `user.id`.

---

## Request Configuration

### Body

```bp
request {
  body {
    title: "My Item",
    price: 999,
    tags:  ["sale", "new"],
  }
}
```

### Authentication

```bp
# API key auth
request {
  auth api_key(key.key_hash)
  body { ... }
}

# Bearer token
request {
  auth bearer(token.value)
  body { ... }
}

# Basic auth
request {
  auth basic(user.name, user.password)
  body { ... }
}
```

### Custom Headers

```bp
request {
  headers {
    X-Custom-Header: "my-value",
    Accept-Language: "en-US",
  }
  body { ... }
}
```

### File Uploads

Use `fixture()` to include files in the request:

```bp
fixture "sample.png" from "testdata/sample.png"

test upload_image {
  target POST /api/uploads

  request {
    body {
      file:     fixture("sample.png"),
      filename: "test-image.png",
    }
  }

  expect {
    status 201
    body.url is string
  }
}
```

---

## Assertions

### Status Code

```bp
expect {
  status 200          # exact match
  status 201
  status 4xx          # any 400-499
}
```

### Body Fields

```bp
expect {
  body.id is uuid           # type check
  body.name is string
  body.count is int
  body.active is bool
  body.url exists            # not null/undefined

  body.name == "Widget"      # equality
  body.name != "wrong"       # inequality
  body.count == 5
}
```

### Headers

```bp
expect {
  header.Content-Type == "application/json"
}
```

### Response Timing

```bp
expect {
  duration < 2s       # parsed today; codegen emits a TODO marker
}
```

### Database Side Effects

Verify that a request created or modified database records:

```bp
expect {
  # Check that a record matching the conditions exists
  model job where(status == "done") exists
  model job where(id == body.job_id, status == "done") exists
}
```

This generates a database query that asserts at least one matching row exists.

---

## Fixtures

Declare test data files for use in requests.

### File Fixtures

```bp
# Reference an actual file on disk
fixture "sample.png" from "testdata/sample.png"
```

### Generated Fixtures

```bp
# Generate a binary blob of a specific type and size
fixture "large.png" generated { type: "image/png", size: 15mb }
```

### Usage

```bp
request {
  body {
    file: fixture("sample.png"),
  }
}
```

---

## Load Testing

Use `repeat` to run a test multiple times:

```bp
test load_test_create {
  target POST /api/items

  request repeat(100) {
    body { title: "Load test item" }
  }

  expect {
    status 201
  }
}
```

This generates a `for` loop that sends the request 100 times.

---

## Test Strategies

### Testing CRUD Endpoints

Write a test for each operation:

```bp
@ "Create returns 201 with generated ID"
test create_item {
  target POST /api/items
  request { body { name: "Widget", price: 999 } }
  expect {
    status 201
    body.id is uuid
    body.name == "Widget"
  }
}

@ "Get returns 404 for non-existent item"
test get_missing_item {
  target GET /api/items/00000000-0000-0000-0000-000000000000
  request {}
  expect { status 404 }
}

@ "Delete removes the item"
test delete_item {
  target DELETE /api/items/:id
  setup { |> item = seed item { name: "Temp", price: 100 } }
  request {}
  expect { status 204 }
}
```

### Testing Authentication

```bp
@ "Unauthenticated request returns 401"
test missing_auth {
  target POST /api/process
  request { body { input: "test" } }
  expect { status 401 }
}

@ "Valid API key succeeds"
test valid_auth {
  target POST /api/process
  setup { |> key = seed api_key { plan: "pro" } }
  request {
    auth api_key(key.key_hash)
    body { input: "test" }
  }
  expect { status 200 }
  cleanup { |> delete_all api_key }
}
```

### Testing Validation

```bp
@ "Missing required field returns 400"
test missing_title {
  target POST /api/todos
  request { body {} }
  expect { status 4xx }
}

@ "Invalid email format rejected"
test bad_email {
  target POST /api/users
  request { body { name: "Test", email: "not-an-email" } }
  expect { status 4xx }
}
```

---

## Auto-Generated Contract Tests

You don't have to write a single `test` block to get coverage. Blueprint can
emit a self-contained contract test suite directly from your endpoint
definitions — one Vitest case per endpoint, grouped by resource.

`bp test` always generates and runs this suite, and `bp build --gen-tests`
writes it to disk so you can read or commit it.

```bash
# Generate the suite (and the rest of the project) without running it
bp build my-service.bp --gen-tests

# Generate + run everything (this is what `bp test` does under the hood)
bp test my-service.bp
```

The generated files are:

```
generated/
  vitest.config.ts                  # registers the harness setup file
  test/
    _harness/
      ddl.ts                        # CREATE TABLE DDL mirroring your models
      db.ts                         # PGlite-backed drizzle db + resetDb()
      setup.ts                      # dummy env so the app imports cleanly
    generated/
      <resource>.test.ts            # one describe block per route group
```

Each generated case:

1. Mocks `src/lib/db` with an **in-memory Postgres (PGlite)** — no external
   database, no Docker, nothing to start. `resetDb()` truncates every table
   before each test for isolation.
2. Seeds any rows a path param needs (e.g. it inserts a `todo` before hitting
   `GET /api/todos/:id`), seeding `ref()` parents first.
3. Sends the request with `app.request()` and asserts the response status is
   **one of the statuses the endpoint and its middleware can declare** — its
   outputs, guards, `400`/`500` fallbacks, and `401` when the route requires
   auth.
4. For `2xx` object responses, checks the body has every key your output block
   declares.

Assertions are deliberately lenient — a contract suite that never flakes is
worth more than one that asserts business logic it can't predict. Use it as a
safety net (every route boots, routes, validates, and returns a declared shape),
and add hand-written `test` blocks for the behavior that matters.

> The `--gen-tests` flag is available on `bp build` and `bp diff`. `bp test`
> turns it on automatically, so you never pass it there.

---

## Testing the Python Target

`bp build --target python --gen-tests` emits a separate, runnable pytest suite
backed by a real Postgres container (`testcontainers[postgresql]`) instead of
the node target's in-memory PGlite harness — SQLite/PGlite's dialect drifts too
far from Postgres FK/JSON/enum semantics to trust for contract-test signal, so
the Python suite uses the real thing.

```bash
bp build my-service.bp --target python --gen-tests
```

### Suite layout

```
generated/
  tests/
    __init__.py
    _harness/
      __init__.py
    conftest.py                # pg_container / engine / db / client fixtures
    test_<resource>.py         # one contract test per endpoint, grouped by resource
```

`tests/conftest.py` provides four fixtures, each depending on the last:

| Fixture | Scope | Purpose |
|---------|-------|---------|
| `pg_container` | session | Starts one `PostgresContainer("postgres:16-alpine")` for the whole run |
| `engine` | session | A SQLAlchemy engine pointed at the container, with `Base.metadata.create_all` run once (bypasses Alembic so the suite is hermetic) |
| `db` | function | A `Session` that `TRUNCATE`s every table before each test, for isolation |
| `client` | function | A FastAPI `TestClient` with `get_db` overridden via `app.dependency_overrides` so every route uses the test session |

Each generated `test_<resource>.py` case follows the same contract as the node
suite: it seeds any FK parents a path param needs, sends the request, and
asserts the response status is one of the statuses the endpoint (and its
guards) can declare — deliberately lenient, the same "route stopped responding
entirely" safety net described above.

### Requirements and running it

- **Docker is required** — `pg_container` needs a running Docker daemon to
  start the Postgres container.
- Run with:

  ```bash
  cd generated
  uv sync && uv run pytest
  ```

### `bp test` is node-only

Unlike `bp build`, which accepts `--target` for both `--gen-tests` paths,
**`bp test` has no `--target` flag at all** — it always builds and runs the
node/Vitest path. To run the Python suite, use `bp build --target python
--gen-tests` followed by `uv run pytest` directly as shown above; there is no
`bp test --target python` shortcut today.

---

## Running Tests

### Run All Tests

```bash
bp test my-service.bp
```

`bp test` builds the project (with the auto-generated contract suite enabled),
runs `bun install` if needed, then runs `bun run test`. That executes **both**
your authored `test` blocks and the generated contract suite. Because the whole
harness runs on in-memory PGlite, `bp test` needs **no `DATABASE_URL` and no
running Postgres**.

### Run from Generated Directory

If you've already built **with the test harness** (`bp build --gen-tests`):

```bash
cd generated
bun run test
```

### Watch Mode

From the generated directory:

```bash
cd generated
bunx vitest --watch
```

---

## Test Groups

Group related tests together using `test_group`:

```bp
test_group auth_tests {
  tests [login_success, login_wrong_password, missing_auth_header]
}

test_group crud_tests {
  tests [create_item, get_item, update_item, delete_item]
}
```

All referenced tests must be defined in the same file. The checker validates that every test name in the list exists.

---

## Assertion Reference

| Blueprint Assertion | Generated Vitest | Description |
|---|---|---|
| `status 200` | `expect(res.status).toBe(200)` | Exact status code |
| `status 4xx` | `expect(res.status).toBeGreaterThanOrEqual(400)` | Status code range |
| `body.name == "val"` | `expect(body.name).toBe('val')` | String equality |
| `body.count == 5` | `expect(body.count).toBe(5)` | Numeric equality |
| `body.name != "val"` | `expect(body.name).not.toBe('val')` | Inequality |
| `body.id is uuid` | `expect(body.id).toMatch(/^[0-9a-f]{8}-...$/i)` | UUID format check |
| `body.url is string` | `expect(typeof body.url).toBe('string')` | String type check |
| `body.count is int` | `expect(typeof body.count).toBe('number')` | Number type check |
| `body.active is bool` | `expect(typeof body.active).toBe('boolean')` | Boolean type check |
| `body.url exists` | `expect(body.url).toBeDefined()` | Existence check |
| `header.X == "val"` | `expect(res.headers.get('X')).toBe('val')` | Header check |
| `duration < 2s` | TODO marker | Timing checks are not generated yet |
| `model X where(...) exists` | Drizzle select + `toBeGreaterThan(0)` | Database record exists |
| `last_status 429` | `expect(lastRes.status).toBe(429)` | Last status (with `repeat`) |

---

## Troubleshooting

### Tests fail with "Cannot find module"

Make sure you've installed dependencies in the generated directory:

```bash
cd generated
bun install
```

### Do my tests need a real Postgres?

No. Under `bp test`, every test — both your authored `test` blocks and the
auto-generated contract suite — runs against an **in-memory PGlite database**.
Their `setup`, `seed`, `cleanup`, and `model ... exists` queries are routed to
PGlite by mocking `src/lib/db`, so you don't need `DATABASE_URL` set or a
Postgres server running.

A real `DATABASE_URL` only matters when you run the *application* (`bun run
dev`, `bun run start`) or run migrations against an actual database — not when
you run tests.

### Database tests fail with connection errors

If you're running Vitest directly (`bunx vitest`) rather than through `bp test`,
make sure you built **with the test harness**, otherwise the generated tests are
absent and any DB import points at the real `src/lib/db`:

```bash
bp build my-service.bp --gen-tests
cd generated
bun run test
```

### Seeded data persists between tests

The PGlite harness truncates every table before each test, so the generated
suite is always isolated. For your authored `test` blocks, add `cleanup` blocks
to remove anything you seeded that another test might observe.

### Fixture files not found

Fixture paths are relative to the `.bp` file. Ensure the testdata directory exists:

```
my-service.bp
testdata/
  sample.png
  config.json
```

---

## How Tests Are Generated

Each `test` block in your `.bp` file becomes a separate Vitest file in `test/<name>.test.ts`. The generated test:

1. Imports the Hono app from `src/index.ts`
2. Uses `app.request()` to send HTTP requests (no real server needed)
3. Asserts on the response using Vitest's `expect()`
4. Runs setup/cleanup and `model ... exists` queries against the database — under `bp test` that database is the in-memory PGlite harness, because the suite mocks `src/lib/db` (see [Auto-Generated Contract Tests](#auto-generated-contract-tests))

Example generated output:

```typescript
import { describe, it, expect } from 'vitest';
import app from '../src/index.js';

describe('createTodo', () => {
  it('create_todo', async () => {
    const res = await app.request('/api/todos', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: 'Buy milk' }),
    });
    const body = await res.json() as any;
    expect(res.status).toBe(201);
    expect(typeof body.id).toBe('string');
    expect(body.title).toBe('Buy milk');
  });
});
```
