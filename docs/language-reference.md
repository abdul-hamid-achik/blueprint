# Language Reference

Complete reference for the Blueprint (`.bp`) language.

## Table of Contents

1. [File Structure](#1-file-structure)
2. [Primitives and Literals](#2-primitives-and-literals)
3. [Naming Conventions](#3-naming-conventions)
4. [The Arrow System](#4-the-arrow-system)
5. [blueprint Declaration](#5-blueprint-declaration)
6. [secret, env, locale, and translation](#6-secret-env-locale-and-translation)
7. [model, content, and save](#7-model-content-and-save)
8. [type, alias, and extended type expressions](#8-type-alias-and-extended-type-expressions)
9. [enum](#9-enum)
10. [fn — Functions](#10-fn--functions)
11. [pipe — Pipelines](#11-pipe--pipelines)
12. [middleware](#12-middleware)
13. [HTTP Endpoints](#13-http-endpoints)
14. [STREAM Endpoints](#14-stream-endpoints)
15. [WS Endpoints](#15-ws-endpoints)
16. [worker](#16-worker)
17. [schedule](#17-schedule)
18. [external](#18-external)
19. [subscribe](#19-subscribe)
20. [test](#20-test)
21. [fixture](#21-fixture)
22. [include](#22-include)
23. [Expressions](#23-expressions)
24. [Built-in Operations](#24-built-in-operations)
25. [Error Handling](#25-error-handling)
26. [Guards and When](#26-guards-and-when)
27. [Game and Content Workflows](#27-game-and-content-workflows)

---

## 1. File Structure

A `.bp` file contains one or more top-level blocks. The `blueprint` declaration must come first.

```bp
blueprint "service-name" { ... }    # required, first

secret API_KEY required              # secrets
env MAX_SIZE 10mb                    # env vars
locale en default                    # locales
translation mission_text { ... }     # translation namespaces + bundles

model user { ... }                   # data models
content mission { ... }              # versioned content records
type ImageFile { ... }               # custom types
alias Email = string format(email)   # type aliases
enum Status { ... }                  # enumerations
state mission_status { ... }         # state machines
analytics gameplay { ... }           # analytics events + sinks
save player_progress { ... }         # save schema + migrations

fn watermark { ... }                 # function declarations
pipe validate_image { ... }          # reusable pipelines
middleware require_auth { ... }      # middleware

GET /api/items { ... }               # HTTP endpoints
POST /api/items { ... }
STREAM /api/events { ... }           # SSE endpoints
WS /ws/chat { ... }                  # WebSocket endpoints

worker process_job { ... }           # background workers
schedule cleanup { ... }             # cron jobs
external "auth-service" { ... }      # external services
subscribe "user.deleted" { ... }     # event subscriptions

test create_user { ... }             # test blocks
fixture "sample.png" from "..."      # test fixtures

include "other-file.bp"              # file splitting
```

---

## 2. Primitives and Literals

### Scalar Types

| Type | Description | Example |
|------|-------------|---------|
| `string` | Text | `"hello"`, `"world"` |
| `int` | Integer | `42`, `0`, `-5` |
| `float` | Floating point | `3.14`, `0.5` |
| `bool` | Boolean | `true`, `false` |
| `uuid` | UUID v4 | Generated automatically |
| `timestamp` | ISO 8601 datetime | `now`, `2024-01-01` |
| `json` | Arbitrary JSON | Any object or array |
| `money` | Decimal with 2 places | `9.99` |
| `file` | Binary file | Multipart upload |

### Special Literals

```bp
now                 # current timestamp
null                # null value
```

### Size Literals

```bp
512b                # bytes
10kb                # kilobytes
10mb                # megabytes
1gb                 # gigabytes
```

### Duration Literals

```bp
100ms               # milliseconds
5s                  # seconds
30min               # minutes
2h                  # hours
1d                  # day
30days              # days
90.days.ago         # relative time (for queries)
```

### Rate Literals

```bp
10/min              # 10 per minute
100/hour            # 100 per hour
1000/day            # 1000 per day
```

### MIME Type Literals

```bp
image/*             # any image
image/png           # specific type
application/pdf
video/*
*/*                 # any file
```

MIME types are valid in target-neutral signatures and native functions. The
Node target currently rejects MIME/file **endpoint inputs** because its routes
and generated browser SDK do not yet emit multipart/form-data handling. Use a
URL or another JSON-safe reference at the HTTP boundary until multipart
generation ships.

### Lists

```bp
["image/png", "image/jpeg", "image/webp"]
[1, 2, 3]
["a", "b", "c"]
```

### String Interpolation

```bp
"Hello, {user.name}!"
"Job {job.id} completed in {elapsed}ms"
"Cleaned {count} records"
```

The identifier at the start of each interpolation must already be in scope.
Bind computed values first—for example `|> finished_at = now`, then interpolate
`{finished_at}`—so generated targets never receive an undeclared runtime name.

---

## 3. Naming Conventions

The checker enforces these conventions:

| Context | Convention | Example |
|---------|------------|---------|
| Block names | `snake_case` | `require_auth`, `process_job` |
| Field names | `snake_case` | `api_key_id`, `created_at` |
| Variables | `snake_case` | `let key = ...` |
| Secrets | `SCREAMING_SNAKE_CASE` | `DATABASE_URL`, `STRIPE_KEY` |
| Env vars | `SCREAMING_SNAKE_CASE` | `MAX_FILE_SIZE`, `LOG_LEVEL` |
| Custom types | `PascalCase` | `ImageFile`, `PriceInfo` |
| Enums | `PascalCase` | `Status`, `Plan` |
| Paths | `kebab-case` | `/api/my-items/:item-id` |

---

## 4. The Arrow System

Arrows on the left margin show data flow at a glance:

| Arrow | Meaning | Reads as |
|-------|---------|----------|
| `<-` | Input declaration | "receives" |
| `\|>` | Pipeline step | "then" |
| `->` | Output | "returns" |
| `@` | Intent annotation | "this should..." |
| `@>` | LLM generation slot | "LLM fills this" |

**Ordering rules (enforced):**
1. `<-` inputs come first
2. `|>` steps come in the middle
3. `->` outputs come last

**Scanning a block visually:**

```bp
POST /api/watermark {
  <- file     image/*   required    # ← inputs
  <- text     string    required
  <- position string    default(center)

  |> file = pipe validate(file)     # ← steps
  |> job  = save job { ... }
  |> out  = watermark(file, text)

  -> 200 { url: out.url }           # ← output
}
```

---

## 5. `blueprint` Declaration

Required. Must be first. Exactly one per root file.

```bp
blueprint "service-name" {
  version   "1.0.0"
  port      3000
  runtime   node

  # Optional infrastructure
  database  postgres         # postgres | mysql | sqlite | mongo
  cache     redis            # redis | memcached | memory
  storage   s3               # s3 | gcs | local
  queue     redis            # redis | sqs | rabbitmq

  # Global middleware
  use request_logger
  use cors { origins: ["https://example.com"] }
}
```

Each blueprint setting may appear at most once. The checker requires `version`
to be a string, `port` to be an integer from 1 through 65535, and
`runtime`/`database`/`cache`/`storage` values to be unquoted identifiers. This
validation runs before generators can silently fall back to defaults.

---

## 6. `secret`, `env`, `locale`, and `translation`

### `secret`

Secrets are sensitive values loaded from environment variables. Missing required secrets cause a startup error.

```bp
secret DATABASE_URL required
secret STRIPE_KEY   required
secret WEBHOOK_KEY  optional default("")
```

Declared secrets are exposed by generated projects through the same validated
environment object as other configuration: use `env.DATABASE_URL` or
`env.STRIPE_KEY` in arrow expressions. `secret.KEY` is not a general expression
namespace; it is interpreted only by the webhook-auth form
`auth webhook_sig using(secret.KEY)`.

Generated `.env.example` lists all declared secrets.

### `env`

Environment variables with baked-in defaults. Overridable at runtime.

```bp
env MAX_FILE_SIZE   10mb
env ALLOWED_TYPES   ["image/png", "image/jpeg"]
env LOG_LEVEL       "info"
env FREE_MONTHLY    50
env ENABLE_METRICS  true
```

Access in code: `env.MAX_FILE_SIZE`, `env.ALLOWED_TYPES`

---

### `locale`

Locales declare the language codes your generated project understands, plus the default locale and fallback chain.

```bp
locale en default
locale "fr-FR" fallback(en)
```

Generated output exposes these declarations in `src/lib/i18n.ts` and `frontend/src/i18n.ts` as `locales`, `defaultLocale`, and `localeFallbacks`.

### `translation`

Translation namespaces declare valid translation keys and can also embed localized values.

```bp
translation mission_text {
  key "mission.start"
  key "mission.complete"

  locale en {
    "mission.start": "Start mission"
    "mission.complete": "Mission complete"
  }

  locale "fr-FR" {
    "mission.start": "Commencer la mission"
  }
}
```

Rules enforced by the checker:

- each translation key must be unique within the namespace
- each locale bundle can appear once per namespace
- locale bundles must reference declared locales
- localized values can only use keys declared with `key "..."`

Generated output exposes translation metadata as `translationNamespaces` and localized values as `translationValues`.

---

## 7. `model`, `content`, and `save`

Data models define database tables, TypeScript types, and Zod schemas.

```bp
@ "Tracks every processing job"
model job {
  id           uuid       primary
  api_key_id   uuid       ref(api_key)
  status       enum(pending, processing, done, failed)
  input_url    string
  output_url   string     optional
  error        string     optional
  duration_ms  int        optional
  created      timestamp  default(now)
}
```

On both Node and Python, declaring any model activates the complete Postgres
dependency, validated `DATABASE_URL`, schema, and migration layer even if the
blueprint omits `database`. Conversely, `database postgres` with no models still
emits an importable empty schema/Base so migration tooling remains valid.

### Field Constraints

| Constraint | Description | Generated Code |
|------------|-------------|----------------|
| `primary` | Primary key (auto UUID) | `.primaryKey().defaultRandom()` |
| `unique` | Unique constraint | `.unique()` |
| `index` | Database index | Drizzle `index(...).on(...)` |
| `required` | NOT NULL (default) | `.notNull()` |
| `optional` | Nullable | *(no `.notNull()`)* |
| `auto` | Auto-updated | Trigger or app-level |
| `default(value)` | Default value | `.default(value)` |
| `ref(model)` | Foreign key | `.references(() => model.id)` |
| `min(value)` | Min value or length | Zod `.min()` |
| `max(value)` | Max value or length | Zod `.max()` |
| `format(kind)` | Format validation | Zod `.email()`, `.url()`, etc. |

### Computed Fields

A model can declare pure, read-only values that are derived when a record is
materialized rather than stored as database columns:

```bp
model person {
  id uuid primary
  first_name string required
  last_name string required
  computed full_name string = first_name + " " + last_name
  computed label string = full_name + "!"
}
```

The form is `computed <name> <type> = <expression>`. The supported result types
are `string`/`text`, `int`, `float`, `money`, and `bool`. Expressions are a
deliberately pure and total subset: literals, required persisted fields,
earlier computed fields, parentheses, unary `not`/`-`, and type-compatible
arithmetic, comparison, and boolean operators. Function calls, field access,
optional source fields, unsupported source types, forward references, and
assignment to a computed field are rejected. Computed names are also rejected
as keys in `save`/`seed`/`update` bodies: write the persisted dependencies
instead. Because computed values do not exist in the database, they cannot be
used as `where(...)` or `order(...)` columns; filter/order on persisted fields
and use the computed value only after record materialization.

The Node target includes computed fields in materialized model results,
frontend contracts, and Zod response schemas, but does not add a Drizzle
column. Target-agnostic `bp docs` includes them in OpenAPI schemas. Python and
Effect currently reject models with computed fields before returning output
files.

### Inline Enums

Fields can declare inline enums:

```bp
model job {
  status enum(pending, processing, done, failed)
  plan   enum(free, pro, enterprise) default(free)
}
```

### Naming

- Model name `job` → table `jobs` (pluralized)
- Field `api_key_id` → `apiKeyId` in TypeScript
- Generated type: `Job` (PascalCase)

### `content`

`content` is a versioned record pattern for authored game/application data such as missions, dialogue trees, item definitions, and unit stats.

```bp
content mission {
  data json<MissionDefinition> required
}
```

Blueprint expands a `content` block into a generated model shape with these lifecycle fields unless you override them explicitly:

- `key`
- `version`
- `status`
- `published`
- `created`
- `updated`

`content` records work with `publish(...)`, `archive(...)`, `rollback(...)`, `import_bundle(...)`, and `export_bundle(...)`.

### `save`

`save` declares save-data upgrade metadata for long-lived player data.

```bp
save player_progress {
  model save_slot
  version_field save_version
  latest 3
  migrate 1 -> 2 using "./custom-player-progress"
}
```

Generated output includes:

- `src/lib/save-migrations.ts` with `upgrade<SaveName>Save(...)`
- `src/saves/<save-name>.ts` stubs for default migrations
- custom hook stubs for `migrate ... using "./module"`

---

## 8. `type`, `alias`, and extended type expressions

### `type` — Composite Types

Reusable structured types used as input/output shapes.

```bp
type ImageFile {
  url    string
  width  int
  height int
  format enum(png, jpeg, webp)
  size   int
}
```

Use as field types:

```bp
model post {
  id      uuid      primary
  image   ImageFile
  created timestamp default(now)
}
```

Or as endpoint inputs/outputs:

```bp
GET /api/image/:id {
  <- id uuid required
  |> img = fetch image(id)
  -> 200 ImageFile(img)
}
```

### `alias` — Type Aliases

Single-type aliases with optional constraints.

```bp
alias UserId   = uuid
alias Email    = string format(email)
alias Url      = string format(url)
alias FileSize = int min(0) max(100mb)
```

Supported formats: `email`, `url`, `uuid`, `ip`, `date`

### Extended Type Expressions

#### `json<T>`

Typed JSON keeps authored blobs strongly typed through generated TypeScript, Zod, OpenAPI, and Drizzle.

```bp
type MissionDefinition {
  title string required
}

model mission {
  data json<MissionDefinition> required
}
```

#### `tkey(namespace)`

Translation-key types restrict a field or input to keys declared in a `translation` block.

```bp
translation mission_text {
  key "mission.start"
  key "mission.complete"
}

type MissionDefinition {
  title_key tkey(mission_text) required
}
```

Generated output turns `tkey(mission_text)` into a string-literal union and a matching Zod/OpenAPI enum.

---

## 9. `enum`

### Simple Enums

```bp
enum Status {
  pending
  processing
  done
  failed
}
```

Generated as a TypeScript union type and Zod enum.

### Rich Enums

Variants carry associated data:

```bp
enum Plan {
  free       { rate_limit: 10/min,   max_file: 5mb,   monthly_ops: 50 }
  pro        { rate_limit: 60/min,   max_file: 50mb,  monthly_ops: 10000 }
  enterprise { rate_limit: 1000/min, max_file: 500mb, monthly_ops: 100000 }
}
```

Access the data with bracket notation:

```bp
limit Plan[auth.plan].rate_limit
|> guard file.size < Plan[auth.plan].max_file -> 413 "File too large"
|> guard auth.ops_used < Plan[auth.plan].monthly_ops -> 429 "Quota exceeded"
```

Generated as a TypeScript `const` object mapping variant names to their data.

---

## 10. `fn` — Functions

Named, callable logic units.

```bp
@ "Apply text watermark to an image"
fn watermark {
  <- file     image/*
  <- text     string
  <- position string
  <- opacity  float

  -> file image/*

  impl node {
    module: "./internal/watermark"
    func:   "apply"
  }
}
```

### Implementation Strategies

**Native module:**
```bp
impl node {
  module: "./internal/watermark"
  func:   "apply"
}
```

**Shell command:**
```bp
impl exec {
  cmd: "convert {file} -watermark {text} output.png"
}
```

**HTTP call:**
```bp
impl http {
  method:  POST
  url:     "https://api.example.com/process"
  body:    { image: file, text: text }
}
```

The Node target currently emits `method`, `url`, and an optional JSON `body`.
Authentication, retries, response extraction, and non-JSON protocols still
belong in a user-owned `impl node` module.

**Inline logic:**
```bp
fn calculate_price {
  <- plan       string
  <- operations int
  -> money

  logic {
    |> when plan == "free"       -> 0
    |> when plan == "pro"        -> operations * 0.01
    |> when plan == "enterprise" -> operations * 0.005
  }
}
```

**LLM-generated:**
```bp
fn summarize {
  <- text string
  -> string

  @> Summarize the given text in 2-3 sentences.
     Use clear, professional language.
}
```

### Calling Functions

```bp
|> result = watermark(file, text, position, opacity)
|> price  = calculate_price(auth.plan, auth.ops_used)
```

### Function Composition

```bp
fn process_image {
  <- file image/*
  <- text string
  -> file image/*

  logic {
    |> file = compress_image(file, 80)
    |> file = watermark(file, text, "center", 0.5)
    -> file
  }
}
```

---

## 11. `pipe` — Pipelines

Named, reusable sequences of steps. Unlike `fn`, pipes don't have explicit return types — they transform values in place.

```bp
@ "Validate image size and type"
pipe validate_image {
  <- file image/*

  |> guard file.size < env.MAX_FILE_SIZE     -> 413 "File too large"
  |> guard file.type in env.ALLOWED_TYPES    -> 415 "Unsupported type"

  -> file
}
```

**Usage:**

```bp
|> file = pipe validate_image(file)
```

Pipes return either an error response (from guards) or the transformed value. They become TypeScript functions in the generated code.

---

## 12. `middleware`

Reusable code that runs before or after endpoint handlers.

```bp
@ "Verify API key and enforce quota"
middleware require_auth {
  before {
    |> guard header.X-API-Key                -> 401 "Missing API key"
    |> key = query api_key where(key_hash == hash(header.X-API-Key)) first
    |> guard key                             -> 401 "Invalid API key"
    |> guard check_quota(key)                -> 429 "Quota exceeded"
    |> inject key as auth
  }
}

@ "Log request timing"
middleware request_logger {
  before {
    |> start = clock()
  }
  after {
    |> elapsed = clock() - start
    |> log "{method} {path} {status} {elapsed}ms" level(info)
  }
}

@ "CORS"
middleware cors {
  origins ["https://example.com"]
  methods [GET, POST, PUT, DELETE]
  headers ["Content-Type", "Authorization"]
  max_age 86400
}
```

### `inject`

Makes a value available in the endpoint context:

```bp
|> inject key as auth
```

In the endpoint, access it as the injected name:

```bp
POST /api/watermark {
  use require_auth
  ...
  |> job = save job { api_key_id: auth.id, ... }
}
```

### Applying Middleware

Global (applies to all endpoints):

```bp
blueprint "my-service" {
  use request_logger
  use cors { origins: ["*"] }
}
```

Per-endpoint:

```bp
POST /api/watermark {
  use require_auth
  ...
}
```

Built-in global middleware generated by the Node target:

| Name | Example |
|------|---------|
| `cors { ... }` | `use cors { origins: ["*"] }` |
| `compress` | `use compress` |

`rate_limit`, `timeout`, and `cache` are recognized names in the language, but
they are not generated as global `use` middleware today. Use endpoint `limit`
for the current in-process rate limiter and write an explicit middleware for
authentication, distributed rate limiting, timeouts, or response caching.

---

## 13. HTTP Endpoints

```bp
@ "Create a watermark job for an existing image URL"
POST /api/watermark {
  # Metadata
  use    require_auth
  auth   api_key
  limit  Plan[auth.plan].rate_limit
  cache  5min
  tags   ["processing", "core"]
  timeout 30s

  # Inputs
  <- input_url string                              required format(url)
  <- text     string                               required
  <- position enum(center, corner, tile)           default(center)
  <- opacity  float    min(0.0) max(1.0)           default(0.5)

  # Steps
  |> job = save job { status: "pending", input_url: input_url, ... }
  |> enqueue "watermarks" { job_id: job.id }

  -> 202 { job_id: job.id, status: "pending" }
}
```

### Endpoint Metadata

| Key | Syntax | Node runtime behavior |
|-----|--------|-----------------------|
| `use` | `use middleware_name` | Applies a declared custom middleware |
| `auth` | `auth api_key` | Metadata only for generic strategies; webhook HMAC is the exception below |
| `limit` | `limit 60/min` | Enforced by an in-process, per-instance IP/path counter |
| `cache` | `cache 5min` | Parsed and emitted as a comment; responses are not cached yet |
| `tags` | `tags ["a", "b"]` | Included in generated OpenAPI metadata |
| `timeout` | `timeout 30s` | Parsed, but REST handler cancellation is not generated yet |

### Auth Strategies

| Strategy | Node runtime status |
|----------|---------------------|
| `auth api_key` | Parsed metadata; does not verify the header by itself |
| `auth bearer` | Parsed metadata; does not verify a JWT by itself |
| `auth session` | Parsed metadata; does not verify a session by itself |
| `auth webhook_sig using(secret.KEY)` | Enforced with HMAC-SHA256 against `X-Webhook-Signature` |

For API keys, bearer tokens, and sessions, declare and apply a middleware whose
`before` block validates the credential and `inject`s the authenticated value.
Do not rely on a bare `auth` line as a security boundary.

### Input Sources

| Method | Default source |
|--------|----------------|
| GET, DELETE | Query string |
| POST, PUT, PATCH | JSON body |
| Any | `header.X-Name` for headers |
| Any | `:param` in path for path params |

On the Python target, GET/DELETE inputs become FastAPI `Query(...)` parameters,
POST/PUT/PATCH inputs become embedded `Body(...)` parameters, and supported
`min`/`max` constraints are preserved. Direct header references, including
hyphenated names such as `header.X-API-Key`, become valid
`Header(..., alias="X-API-Key")` parameters. An `env.FIELD` reference must be
backed by a declared `secret FIELD` or by an infrastructure setting Blueprint
generates (for example `DATABASE_URL`); otherwise Python codegen fails.

### Output Syntax

```bp
-> 200 { key: value }          # JSON response
-> 200 "Success"                # String response
-> 200 file(result)             # File download
-> 204 "deleted"                # No content
-> stream chunks                # Streaming response
```

---

## 14. STREAM Endpoints

Server-Sent Events endpoints.

```bp
@ "Stream job progress"
STREAM /api/jobs/:id/progress {
  auth api_key
  tags ["jobs", "streaming"]

  <- id uuid required

  |> job = fetch job(id)
  |> guard job -> 404 "Job not found"

  stream {
    |> on event(job_progress) where(job_id == id) {
      -> { percent: event.percent, stage: event.stage }
    }
    |> on event(job_done) where(job_id == id) {
      -> { percent: 100, stage: "done", url: event.url }
      |> close
    }
    |> on timeout(5min) {
      -> { error: "Timed out" }
      |> close
    }
  }
}
```

`close` terminates the SSE stream.

In OpenAPI output: exposed as `GET` with `text/event-stream` content type.

---

## 15. WS Endpoints

WebSocket endpoints.

```bp
WS /ws/chat {
  auth bearer

  on_connect {
    |> log "Connected: {connection.id}"
    -> { type: "connected" }
  }

  on_message {
    |> when message.type == "join" {
      |> join room(message.channel)
      -> { type: "joined", channel: message.channel }
    }
    |> when message.type == "leave" {
      |> leave room(message.channel)
    }
    |> when message.type == "message" {
      |> broadcast room(message.channel) { type: "message", text: message.text }
    }
  }

  on_disconnect {
    |> log "Disconnected: {connection.id}"
  }
}
```

### Realtime Built-ins

```bp
join room(name)                            # join a pub/sub room
leave room(name)                           # leave a room
broadcast room(name) { data }              # send to all in room
whisper connection(id) { data }            # send to specific connection
close                                      # close the connection
```

In OpenAPI output: exposed as `GET` with `101` response and `x-websocket: true`.

---

## 16. `worker`

Background job handlers, triggered by a queue.

```bp
@ "Process watermark job asynchronously"
worker process_watermark {
  trigger queue("watermark_jobs")
  retry   3 backoff(exponential, base: 1s, max: 30s)
  timeout 5min

  <- job_id uuid

  |> job = fetch job(job_id)
  |> guard job.status == "pending" -> skip "Already processed"
  |> update job { status: "processing" }
  |> result = watermark(job.input_url, job.params.text, job.params.position)
  |> update job { status: "done", output_url: result.url }

  on_fail {
    |> update job { status: "failed" }
    |> emit job_failed { job_id: job.id }
  }
}
```

### Worker Metadata

| Key | Syntax | Description |
|-----|--------|-------------|
| `trigger` | `trigger queue("name")` | BullMQ queue name |
| `retry` | `retry 3 backoff(exponential, base: 1s, max: 30s)` | Parsed retry policy; see current limitation below |
| `timeout` | `timeout 5min` | Max execution time |

The Node generator currently exports retry/backoff metadata with each worker,
but generated `enqueue` producers do not yet copy it into BullMQ job options.
BullMQ therefore uses one attempt unless another producer supplies `attempts`
and `backoff`. The generated timeout rejects the worker wait after the declared
duration but does not cancel work already in progress.

### `on_fail`

Runs after the final BullMQ attempt. With the generated producer today, that is
the first failure because worker retry metadata is not yet propagated into the
job options:

```bp
on_fail {
  |> update job { status: "failed" }
  |> emit job_failed { job_id: job.id }
}
```

---

## 17. `schedule`

Cron-based recurring jobs.

```bp
@ "Clean up expired jobs weekly"
schedule cleanup {
  cron "0 4 * * 0"    # 4am every Sunday

  |> old = query job where(created < 90.days.ago)
  |> map old: delete_s3_object(job.output_url)
  |> delete old
  |> log "Cleaned {old.count} expired jobs"
}

@ "Reset monthly usage counters"
schedule reset_quotas {
  cron "0 0 1 * *"    # midnight on the 1st of each month

  |> expired = query api_key where(ops_reset_at < now)
  |> map expired: update api_key { ops_used: 0, ops_reset_at: now }
  |> log "Reset quotas for {expired.count} API keys"
}
```

Standard 5-field cron syntax: `minute hour day-of-month month day-of-week`.

---

## 18. `external`

Declare external HTTP services to call.

```bp
secret AUTH_SERVICE_TOKEN required

external "auth-service" {
  url:     "https://auth.example.com"
  timeout: 5s
  auth:    bearer(secret.AUTH_SERVICE_TOKEN)
  retry:   2
}
```

**Usage:**

```bp
|> user = call auth-service GET /api/users/me
|> result = call auth-service POST /api/tokens { user_id: user.id }
```

The Node target supports `bearer`, `jwt`, `basic`, and `api_key` auth. Each form
must receive exactly one declared `secret.NAME` or `env.NAME`; generated code
reads that value through the validated env module. Bearer/JWT use
`Authorization: Bearer ...`, basic uses `Authorization: Basic ...` (the value
must already be encoded), and API-key auth uses `X-API-Key`.

`retry N` means N **additional**, immediate attempts. Each attempt gets a fresh
timeout. Network and timeout errors plus HTTP 408, 429, and 5xx responses are
retried; other 4xx responses fail immediately. Unsupported strategies,
undeclared credentials, duplicate `auth`/`retry` entries, and non-integer or
negative retry counts make Node codegen fail before returning files.

---

## 19. `subscribe`

Handle events published on the generated process-local event bus.

```bp
@ "Clean up when a user is deleted locally"
subscribe "user.deleted" {
  |> jobs = query job where(user_id == event.user_id)
  |> map jobs: delete_s3_object(job.output_url)
  |> delete jobs
  |> log "Cleaned up {jobs.count} jobs for deleted user {event.user_id}"
}
```

`event` is implicitly bound to the event payload.

The Node target registers this handler on Blueprint's in-process event bus; use
`emit` to publish to it. The parser accepts `from(service)`, but Node codegen
rejects that form until Blueprint has an external transport adapter—it never
silently treats an external subscription as local. Use `enqueue` for BullMQ
worker queues.

---

## 20. `test`

Define Node-target integration examples that generate Vitest test files. See
the [Testing Guide](/testing-guide) for the exact supported request/assertion
subset and Python contract-test behavior.

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

### Test Sections

| Section | Required | Description |
|---------|----------|-------------|
| `target` | Yes | `METHOD /path` |
| `setup` | No | `seed`/`save` data writes plus simple unbound `log`; other statements reject in `bp test` preflight |
| `request` | No | HTTP request definition; omitted requests use the target with defaults |
| `expect` | Yes | Response assertions |
| `cleanup` | No | Parsed but not emitted; `bp test` rejects it during preflight |

### Request Block

```bp
request {
  auth api_key(key.key_hash)          # API key auth
  auth bearer(token.value)            # Bearer token
  auth basic(encoded_credentials)     # value is not encoded for you

  body {
    field: value,
    file:  fixture("sample.png"),
  }
}
```

Request keys are case-sensitive: only lowercase `body` and `auth`, at most once
each. Choose one supported `auth` entry. Authored Node tests JSON-stringify the
body; GET/HEAD bodies, function calls/interpolation in request values, whole
setup rows as auth values, custom request entries, multipart/file inputs, and
`fixture(...)` values all fail `bp test` preflight. A scalar setup-row field such
as `key.key_hash` remains valid. Use the manual handling in the
[Testing Guide](/testing-guide#fixtures) for unsupported cases.

### Assertions

```bp
expect {
  # Status code
  status 200
  status 201
  status 4xx             # any 400-499

  # Body fields
  body.url exists
  body.optional not exists
  body.url is string
  body.url is uuid
  body.url is int
  body.url is bool
  body.count == 5
  body.name == "test"
  body.name != "wrong"

  # Headers
  header.Location == "/jobs/123"

  # Timing
  duration < 2s  # parsed; bp test rejects until timing assertions are emitted

  # Database side effects
  model job where(status == "done") exists
  model job where(id == body.job_id, status == "done") exists
}
```

Assertions are fail-closed: equality/inequality RHS values must be literals;
strings containing an apostrophe, backslash, or newline reject. Hyphenated
header assertions such as `header.Content-Type` reject. Use spaced `not exists`
(`not_exists` does not map to the emitter), and include at most one model
assertion per test.

### Repeat

```bp
request repeat(15) {
  body { title: "Load test" }
}
```

Runs the request sequentially 15 times (generates a `for` loop).
The repeat count must be positive; `repeat(0)` rejects during preflight.

### `seed`

In `setup`, use `seed` or `save` to insert test data; a simple unbound `log` is
also allowed. Other setup statements and calls reject:

```bp
setup {
  |> key  = seed api_key { key_hash: "test-key", plan: "pro" }
  |> user = seed user { name: "Test User", email: "test@example.com" }
}
```

---

## 21. `fixture`

Declare test data files or generated test data.

```bp
# File on disk
fixture "sample.png" from "testdata/sample.png"

# Generated binary
fixture "large.png" generated { type: "image/png", size: 15mb }
```

**Usage in test requests:**

```bp
request {
  body {
    file: fixture("sample.png"),
  }
}
```

---

## 22. `include`

Split a large service across multiple files.

```bp
# main.bp
blueprint "my-service" { ... }

include "models.bp"
include "middleware.bp"
include "endpoints/watermark.bp"
include "endpoints/jobs.bp"
include "tests.bp"
```

**Rules:**
- `blueprint` declaration only in the root file
- Paths are relative to the file containing `include`
- Circular includes are a parse error
- All names must be unique across included files
- Included files cannot themselves contain `include`

---

## 23. Expressions

### Comparisons

```bp
file.size < 10mb
user.plan == "pro"
status != "failed"
retries >= 3
file.type in ["image/png", "image/jpeg"]
```

Operators: `==`, `!=`, `<`, `>`, `<=`, `>=`, `in`

### Logical

```bp
file.size < 10mb and file.type in ["image/png"]
plan == "free" or plan == "trial"
not user.verified
```

Precedence (high to low): `not`, comparisons, `and`, `or`

### Arithmetic

```bp
price * quantity
total + tax
auth.ops_used + 1
file.size / 1mb
```

### Field Access

```bp
user.name
job.params.text
Plan[user.plan].rate_limit          # enum data lookup
event.data.object.customer_email    # deep access
header.X-API-Key                    # hyphenated header names
```

The Python target currently rejects field access when the base is a JSON/map
endpoint input or a declared function result typed as `json`; it does not emit
invalid `dict.attribute` code. Translating `payload.X` to `payload["X"]` remains
future work. Direct `header.X` and declared `env.X` expressions are supported,
but embedding header/env/dictionary-backed values inside raw string
interpolation currently fails closed.

### Function Calls

```bp
watermark(file, text, position, opacity)
hash(header.X-API-Key)
check_quota(key)
```

---

## 24. Built-in Operations

### Data Operations

```bp
fetch <model>(id)                          # get by primary key → record or null
query <model> where(...)                   # filter records
query <model> where(...) order(field, asc) # with ordering
query <model> where(...) paginate(page, n) # with pagination → { items, total }
query <model> where(...) first             # single record
query <model> with(relation)               # Node: one-level ref-backed LEFT JOIN
save <model> { field: value, ... }         # insert → inserted record
update <model> { field: value, ... }       # update bound model variable
delete <ref>                               # delete record(s)
count <model> where(...)                   # count matching records

# where(...) predicates support: ==, !=, <, >, <=, >=, in, or, and,
# and text-search shorthand (bare ident → ILIKE on text columns).
import_bundle <model>, <items>             # upsert-style bulk import for content/save data
export_bundle <model>                      # export ordered records for bundle generation
```

On the Python target, bare `query model where(q)` is text search only when `q`
is a string/text endpoint input. A filter accumulator or another bound value in
that position is rejected; Python does not yet implement Node's dynamic
key/value filter expansion.

#### Ref-backed relationship loading (Node)

`with(...)` eagerly loads one level of related records for `query`:

```bp
model author {
  id uuid primary
  name string required
}

model post {
  id uuid primary
  author_id uuid ref(author)
  title string required
}

GET /posts {
  |> posts = query post where(title != "") with(author) order(title, asc)
  -> 200 { posts: posts }
}
```

The relationship name is derived by removing `_id` from a persisted field:
`author_id ref(author)` defines `author`. The target must have a persisted `id`
field of the same type. Node emits a one-level `LEFT JOIN`, so the response
member is `author` and may be null. `with(...)` composes with structured
`where(...)`, `order(...)`, `paginate(...)`, and `first` modifiers.

This release does not provide join aliases. Self relationships, two requested
relationships that target the same model, duplicate/dynamic relationship
names, field-name collisions, and legacy positional/block query arguments all
reject. `with(...)` is supported only on `query`, not `fetch` or another data
operation, and it does not recursively load relationships of the joined model.
Python and Effect reject `query ... with(...)` before emitting files.

### Content and Lifecycle

```bp
publish(record)                            # mark content record as published
archive(record)                            # mark content record as archived
rollback(record, version)                  # restore a previous content version
transition(state_machine, from, to)        # runtime-enforced state transition
upgrade_save(save_decl, value)             # run generated save migrations
```

### Storage

```bp
upload(file, bucket)              # upload to S3/GCS → { url }
download(url)                     # download file → file
```

### Pipeline

```bp
pipe <name>(args)                 # call a named pipe
map <collection>: <expr>          # transform a collection
```

### Communication

```bp
call <service> GET /path          # external service call
call <service> POST /path { body }
emit <event> { data }             # publish event (in-process)
emit <event> to(service) { data } # reserved; targets reject without an adapter
enqueue "queue" { data }          # enqueue a job to a BullMQ worker queue
```

Node generates and awaits the in-process `emit` bus in routes, functions,
middleware, workers, schedules, STREAM, and WS handlers. `emit ... to(service)`
fails closed until an external event transport adapter is available; use an
explicit external HTTP `call` when that is the intended transport.

### Utility

```bp
log "message" level(info)         # structured logging (info|warn|error)
sleep 1s                          # delay execution
clock()                           # current timestamp in ms
hash(value)                       # SHA-256 hash
track("event", payload)          # analytics event dispatch
```

---

## 25. Error Handling

### `try` / `recover`

The only construct that introduces a second level of nesting.

```bp
|> try {
  |> result = watermark(file, text)
  |> url    = upload(result, env.BUCKET)
} recover {
  |> log "Failed: {error.message}" level(error)
  -> 500 { error: "Processing failed" }
}
```

Inside `recover`, `error` is implicitly bound with:
- `error.message` — error description
- `error.code` — error code
- `error.type` — error type string

`try/recover` cannot be nested.

### `on_error`

Default error handler for the entire endpoint:

```bp
POST /api/items {
  on_error -> 500 "Unexpected error: {error.message}"
  ...
}
```

If a step fails without a `try/recover`, `on_error` catches it. If absent, the runtime returns a generic 500.

---

## 26. Guards and When

### `guard` — Validation with Early Return

Exits immediately if the condition is false or the value is null/falsy:

```bp
|> guard file.size < 10mb             -> 413 "File too large"
|> guard file.type in allowed         -> 415 "Unsupported file type"
|> guard key                          -> 401 "Invalid API key"
|> guard job                          -> 404 "Job not found"
```

With an intent annotation:

```bp
|> @ "Reject files over the plan limit"
|> guard file.size < Plan[auth.plan].max_file -> 413 "File too large for your plan"
```

### `when` — Conditional Steps

Execute a step or block only when a condition is true:

```bp
# Inline — assign only when truthy
|> when status: filters.status = status

# Block — multiple steps
|> when event.type == "checkout.session.completed" {
  |> update api_key { plan: plan }
  |> log "Upgraded {email} to {plan}"
}
```

---

## 27. Game and Content Workflows

These constructs are useful when Blueprint drives a game backend, content platform, or live-ops service.

### `state`

State machines declare allowed transitions and generate runtime helpers.

```bp
state mission_status {
  draft -> reviewed
  reviewed -> published
}
```

Generated output includes transition metadata plus helper functions like `canTransitionMissionStatus(...)` and `transitionMissionStatus(...)` in `src/lib/state.ts`.

### `analytics`

Analytics blocks declare valid event names and delivery sinks.

```bp
analytics gameplay {
  event mission_started
  event mission_completed
  sink console
  sink http("https://analytics.example.com/events")
}
```

Generated output includes:

- `analyticsNamespaces`
- `analyticsSinks`
- batched HTTP delivery with retry/backoff
- the `track(...)` helper in `src/lib/analytics.ts`

### Bundle import/export

Use `import_bundle(...)` and `export_bundle(...)` to move authored data in and out of `content` tables.

```bp
POST /api/missions/import {
  <- bundle json required
  |> imported = import_bundle(mission, bundle)
  -> 200 { items: imported }
}

GET /api/missions/export {
  |> bundle = export_bundle(mission)
  -> 200 { items: bundle }
}
```

### End-to-end example

```bp
translation mission_text {
  key "mission.start"
}

state mission_status {
  draft -> reviewed
}

type MissionDefinition {
  title_key tkey(mission_text)
  status    mission_status
}

content mission {
  data json<MissionDefinition> required
}
```
