# Frequently Asked Questions

## General Questions

### What is Blueprint?

Blueprint is a declarative programming language for web services. It lets you write API specifications using an intent-first syntax, then generates a complete, working backend. By default it compiles to TypeScript/Node.js (Hono, Drizzle ORM, and Zod), and it can also target Python or Effect — see [Can Blueprint generate something other than Node.js?](#can-blueprint-generate-something-other-than-node-js).

### Who is Blueprint for?

- **Backend developers** who want to move quickly without boilerplate
- **Full-stack developers** building MVPs or prototypes
- **Teams** that want readable, maintainable API specifications
- **AI-assisted workflows** — Blueprint's structured syntax works well with LLMs

### Why not just write TypeScript directly?

Blueprint eliminates boilerplate and enforces best practices:
- Automatic validation schema generation
- Type-safe database operations
- Endpoint rate limiting plus explicit middleware hooks for application auth
- Clean separation of concerns with the arrow syntax
- Generated code is clean — you can `bp eject` at any time

Blueprint does not currently generate generic API-key/JWT/session verification
or response caching from metadata alone. Security-sensitive behavior belongs in
an explicit middleware or user-owned function; see the support notes in the
[Language Reference](/language-reference#endpoint-metadata).

---

## Installation & Setup

### How do I install Blueprint?

```bash
# macOS / Linux (Homebrew)
brew install abdul-hamid-achik/tap/bp

# Or download a release binary
# https://github.com/abdul-hamid-achik/blueprint/releases

# Or build from source
git clone https://github.com/abdul-hamid-achik/blueprint
cd blueprint
go build -o bin/bp ./cmd/bp
```

### What are the system requirements?

- **Go 1.25.5+** (only to build Blueprint from source)
- **Node.js 20+ or Bun 1.x** (to run the default Node target)
- **PostgreSQL** (if using database features)
- **Redis** (optional, for workers and multi-instance realtime delivery)
- **Docker** (for `bp test --target python`)

### Do I need to know Go?

No. Blueprint is a standalone language. You only need Go if you're building the `bp` CLI from source. Pre-built binaries are available for macOS, Linux, and Windows.

---

## Language Questions

### What's with the arrow syntax?

The arrows make data flow visually scannable:

```bp
<- file     image/*   # Input (receives)
|> job = save job {}  # Step (then)
-> 200 { id: job.id } # Output (returns)
```

- `<-` : Input declarations
- `|>` : Pipeline steps
- `->` : Response outputs
- `@`  : Intent annotations

### Can I use conditionals?

Yes, but differently than traditional languages:

```bp
# Guard — early return if condition fails
|> guard user.active -> 403 "Account suspended"

# When — conditional step execution
|> when plan == "pro": limit = 1000

# When block — multiple conditional steps
|> when event.type == "payment" {
  |> process_payment(event)
  |> send_receipt(event)
}
```

There's no `if/else` — use `guard` for early returns and `when` for conditional logic.

### How do I write loops?

Use `map` for iteration:

```bp
|> results = query items where(active == true)
|> map results: process_item(item)
```

### Can I import code from other files?

Yes, use `include`:

```bp
# main.bp
blueprint "my-api" { ... }

include "models.bp"
include "routes/users.bp"
include "middleware/auth.bp"
```

Rules:
- Only the root file can have a `blueprint` declaration
- Paths are relative to the file containing `include`
- No circular includes allowed

### Can I migrate an existing TypeScript service?

`bp import` can give you a structural starting point:

```bash
bp import ./src --from ts --out service.bp
```

It recognizes conservative static forms of Drizzle tables/enums, Hono routes,
and referenced Zod object inputs. It never translates imperative handler
bodies. Every recovered handler is reported on stderr and becomes an explicit
TODO returning 501; dynamic and unsupported structure is warned about or
skipped. Treat the result as a review checklist, not a behavior-preserving
conversion, even when `bp check service.bp` succeeds.

### Can Blueprint generate something other than Node.js?

Yes. The same `.bp` source can compile to three targets, selected with `--target` on `bp build` and `bp diff` (`bp test` and `bp migrate` accept `--target node|python`):

- **`node`** (default) — TypeScript on Hono + Drizzle ORM + Zod.
- **`python`** (`--target python`) — FastAPI + SQLAlchemy + Alembic.
- **`effect`** (`--target effect`) — TypeScript on Effect; a runnable,
  health-only config scaffold. Models/endpoints/tests still fail closed.

```bash
bp build my-api.bp --target python
```

`bp deploy` also has a `--target` flag, but it means something different there
— it picks a *deploy* target (`docker`, with `fly` not yet implemented), not a
codegen target. `bp deploy` always builds the node target internally.

See [Multi-Target Codegen](./multi-target-codegen.md) for what each target covers.

### Can a model expose computed values or load relationships?

On Node, yes, within a deliberately narrow slice:

```bp
model author {
  id uuid primary
  name string required
  computed label string = name + "!"
}

model post {
  id uuid primary
  author_id uuid ref(author)
}

GET /posts {
  |> posts = query post with(author)
  -> 200 { posts: posts }
}
```

Computed fields are pure, read-only materialized values, not database columns.
`with(author)` is a one-level `LEFT JOIN` through `author_id ref(author)`.
Aliases, self joins, repeated joins to the same target, nested loading, and
`fetch ... with(...)` are not supported. Python and Effect reject these
features before emitting files.

---

## Error Handling

### What happens when something goes wrong?

By default, runtime errors return a 500 with a generic message. For explicit handling, use `try/recover`:

```bp
|> try {
  |> result = risky_operation()
  |> save result
} recover {
  |> log "Failed: {error.message}" level(error)
  -> 500 { error: "Operation failed" }
}
```

In the node target, a `try` body with multiple writes is wrapped in a `db.transaction(...)`, so if a later step throws the partial writes roll back and nothing is committed. The `recover` block then runs to compensate or respond.

### How do I validate inputs?

Use constraints on inputs:

```bp
POST /api/items {
  <- name  string  required  min(1) max(100)
  <- price money   required  min(0)
  <- qty   int     default(1) min(1)
  ...
}
```

Or use guards for complex validation:

```bp
|> guard file.size < env.MAX_SIZE -> 413 "File too large"
```

### Why am I getting "unexpected token" errors?

Common causes:
1. **Missing commas** in block expressions: `{ a: 1, b: 2 }`
2. **Wrong arrow order** — inputs must come before steps before outputs
3. **Unclosed blocks** — every `{` needs a `}`
4. **Reserved words** — don't use keywords like `type`, `model`, `fn` as identifiers

---

## Generated Code

### What does the generated code look like?

Clean, idiomatic TypeScript:

```typescript
// Generated from a Blueprint endpoint
app.post('/api/items', zValidator('json', createItemSchema), async (c) => {
  const { name, price } = c.req.valid('json');
  const [item] = await db.insert(schema.items).values({ name, price }).returning();
  return c.json({ id: item.id }, 201);
});
```

The generated code has:
- No Blueprint runtime dependencies
- Standard Hono + Drizzle patterns
- Full TypeScript types
- Human-readable formatting

### Can I modify the generated code?

Yes, but your changes will be overwritten on the next `bp build`. Two options:

1. **Use `bp eject`** to remove Blueprint markers and take full ownership
2. **Extend via native modules** — implement functions in `./internal/` folders

```bp
fn watermark {
  impl node {
    module: "./internal/watermark"
    func: "apply"
  }
}
```

### How do I deploy the generated code?

The generated project includes a `Dockerfile`:

```bash
cd generated
docker build -t my-app .
docker run -p 3000:3000 -e DATABASE_URL=... my-app
```

Or use `bp deploy`:

```bash
bp deploy my-api.bp --tag my-app:latest
```

`bp deploy` builds the Docker image and then smoke-runs it: it starts the container, probes `/health`, and tears it down, failing if the health check doesn't pass. Pass `--no-run` to skip the smoke test (useful in CI that only validates the build). The default `--target docker` is the only target that runs today; `--target fly` is not yet implemented.

---

## Testing

### How do I write tests?

Blueprint has built-in test blocks:

```bp
test create_user {
  target POST /api/users

  setup {
    |> org = seed organization { name: "Test Org" }
  }

  request {
    body { name: "John", email: "john@example.com" }
  }

  expect {
    status 201
    body.id is uuid
    body.name == "John"
  }

}
```

Run with: `bp test my-api.bp`

Generated Node tests currently accept a strict executable subset. Cleanup,
dynamic target paths, custom/multipart/file requests, timing forms, and richer
setup are rejected before build. See the [Testing Guide](/testing-guide) for
the complete boundary.

For deterministic valid-request generation on supported Node REST routes, add:

```bash
bp test my-api.bp --gen-property-tests
```

This adds fast-check properties and implies contract tests. Auth/header/rate
limit routes, native or external side effects, queues/storage/events/realtime,
sleep/wall-clock behavior, and unsupported input transports fail property
generation rather than being silently skipped.

### Can I use fixtures?

Fixture declarations are valid source syntax:

```bp
# Reference a file
fixture "sample.png" from "testdata/sample.png"

# Generate test data
fixture "large.png" generated { type: "image/png", size: 15mb }

# Use in tests
request {
  body { file: fixture("sample.png") }
}
```

However, the generated Node authored-test runner does not yet copy assets or
emit multipart `FormData`, so `bp test` rejects a `fixture(...)` request value.
Use a hand-written Vitest multipart test today. Python rejects authored
`fixture`/`test` blocks entirely until it can translate them.

---

## Advanced Topics

### How do I add custom middleware?

```bp
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Missing auth"
    |> user = verify_token(header.Authorization)
    |> guard user -> 401 "Invalid token"
    |> inject user as current_user
  }
}

# Apply globally
blueprint "my-api" {
  use require_auth
}

# Or per-endpoint
GET /api/items {
  use require_auth
  ...
}
```

### Can I call external APIs?

Yes:

```bp
secret STRIPE_TOKEN required

external "stripe" {
  url:     "https://api.stripe.com/v1"
  timeout: 30s
  auth:    bearer(secret.STRIPE_TOKEN)
  retry:   2
}

POST /api/charges {
  |> result = call stripe POST /charges {
    amount: amount,
    currency: "usd"
  }
  -> 200 { charge_id: result.id }
}
```

The generated Node helper applies URL, timeout, authentication, and retry
configuration. `bearer`, `jwt`, `basic`, and `api_key` each require one declared
`secret.NAME` or `env.NAME`; the generated request reads the credential through
the env module and sets `Authorization` or `X-API-Key` as appropriate.

`retry 2` means the initial call plus at most two additional immediate attempts.
Each attempt has a fresh timeout. Only network/timeout failures and HTTP
408/429/5xx are retried; other 4xx responses fail immediately. Malformed auth,
undeclared credentials, and invalid retry values fail codegen before files are
returned. For provider-specific signing, refresh flows, or backoff, use a
user-owned function.

### How do background jobs work?

```bp
worker process_image {
  trigger queue("images")
  retry 3 backoff(exponential, base: 1s, max: 30s)

  <- job_id uuid

  |> job = fetch job(job_id)
  |> result = process(job.image_url)
  |> update job { status: "done", result: result }

  on_fail {
    |> update job { status: "failed" }
  }
}
```

Trigger jobs from endpoints:

```bp
|> job = save job { status: "pending", image_url: url }
|> enqueue "images" { job_id: job.id }
```

`enqueue` sends a BullMQ job to the queue named by `trigger queue(...)`.
`emit` is the local event bus used by STREAM/subscription handlers; it does not
enqueue worker jobs.

---

## Troubleshooting

### "Error: unexpected token" on a valid-looking file

Check for:
- Tabs vs spaces (use spaces)
- Hidden characters (run `cat -A file.bp` to see)
- Unclosed quotes or parentheses

### Generated TypeScript has type errors

This is a bug — the generated code should always type-check. Please:
1. Run `bp check your-file.bp` to verify your Blueprint is valid
2. Check the TypeScript version (should be 5.x)
3. File an issue with your `.bp` file

### Database connection fails

Make sure:
- PostgreSQL is running
- `DATABASE_URL` is set correctly
- Database exists: `createdb mydb`
- Run migrations: `bp migrate my-api.bp push`

### Rate limiting isn't working

Endpoint `limit` metadata generates a per-process, per-instance IP/path counter:

```bp
GET /api/items {
  limit 100/min
  -> 200 { ok: true }
}
```

This counter is not shared across replicas. For a global or distributed limit,
declare a custom middleware backed by Redis or an API gateway. The recognized
`use rate_limit(...)` name is not generated as a working global middleware.

### Tests timeout or hang

Common causes:
- A Node test was built without `--gen-tests`/the PGlite harness
- Docker is not running for `bp test --target python`
- Redis-backed workers started in a hand-written test environment
- A user-owned function never resolves

---

## Contributing

### How can I contribute?

See [CONTRIBUTING.md](https://github.com/abdul-hamid-achik/blueprint/blob/main/CONTRIBUTING.md) for:
- Setting up the development environment
- Running tests
- Adding new language features
- Code style guidelines

### Where can I get help?

- **GitHub Issues**: https://github.com/abdul-hamid-achik/blueprint/issues
- **Discussions**: https://github.com/abdul-hamid-achik/blueprint/discussions

---

## Roadmap

LSP support (including completion, workspace symbols, and a VS Code client) and
multi-target codegen (Python and Effect) have already shipped — run `bp lsp`,
or build with `--target python` / `--target effect`. See
[Roadmap](./roadmap.md) for what's still planned, including:
- Package registry
- Managed hosting
- Rename/references/code actions and deeper cross-file editor semantics
- Join aliases/nested loading and Python relationship/computed-field parity
