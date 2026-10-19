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

**Intent annotations.** A `@ "..."` line can precede any top-level declaration,
including the `blueprint` block, `secret`, `env`, `locale`, `translation`,
`include`, `type`, `alias`, `enum`, `external`, and `fixture`. The string is
the declaration's intent: `bp fmt` keeps it in place, and the language server
shows it on hover.

```bp
blueprint "intent-demo" {
  version  "0.1.0"
  port     3000
  runtime  node
}

@ "Log verbosity for the service"
env LOG_LEVEL "info"

@ "Email address alias"
alias Email = string format(email)

@ "Users who can sign in"
model user {
  id    uuid  primary
  email Email required
}
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
| `timestamp` | ISO 8601 datetime, stored as Postgres `timestamptz` (an absolute instant) | `now`, `2024-01-01` |
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

Secrets are sensitive values loaded from environment variables. A required
secret that is missing or empty causes a startup error, so an unfilled
`NAME=` line copied from `.env.example` stops the service.

```bp
secret DATABASE_URL required
secret STRIPE_KEY   required
secret WEBHOOK_KEY  required
secret SENTRY_DSN   optional default("")
```

Declared secrets are exposed by generated projects through the same validated
environment object as other configuration: use `env.DATABASE_URL` or
`env.STRIPE_KEY` in arrow expressions. `secret.KEY` is not a general expression
namespace; it is interpreted only by `auth <strategy> using(secret.KEY)`.

Declare a secret used by `auth ... using(...)` as `required`, so a missing
value stops the service at startup. An `optional` auth secret that is unset or
empty does not open the endpoint: every request to it answers `401`. The
`optional-auth-secret` lint warns about it, and about an auth secret with a
non-empty `default(...)`, which puts the key in the `.bp` source.

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
| `format(kind)` | Format validation on `string` (`email`, `url`, `uuid`, `ip`, `date`) | Zod `.email()`, `.url()`, `.uuid()`, `.ip()`, `.date()` |

### Constraint Values

The checker validates each `default`, `min`, `max`, and `format` value against
the declared type. A value that cannot apply fails `bp check` with error
[C052](./error-codes.md#c052--constraint-value-does-not-fit-the-type), and no
files are generated. The rules are:

| Constraint | Accepted | Rejected |
|------------|----------|----------|
| `default` on `int` | `default(-1)` | `default("abc")` |
| `default` on `float`, `money` | `default(0.5)`, `default(1)` | `default("1")` |
| `default` on `bool` | `default(true)` | `default(1)` |
| `default` on `string` | `default("x")`, `default(center)` (bare word) | `default(5)` |
| `default` on `uuid` | `default("123e4567-e89b-12d3-a456-426614174000")` | `default("not-a-uuid")` |
| `default` on `timestamp` | `default(now)`, `default("2026-01-01")` | `default("notadate")` |
| `default` on an enum | one of its variants, bare or quoted | `default(c)` for `enum(a, b)` |
| `min`, `max` on `int`, `float`, `money` | `min(0) max(150)`, `min(0.0) max(1.0)` | `min("1")` |
| `min`, `max` on `string`, `uuid`, lists | a whole, non-negative length: `max(255)` | `min(1.5)`, `min(1kb)` |
| `min`, `max` on `bool`, enum, `timestamp` | not allowed | `max(3)` on `bool`, `min(1)` on `timestamp` |
| `min` with `max` | `min(1) max(50)` | `min(5) max(1)` |
| `format` | `string format(email)` | `bool format(email)`, `uuid format(ip)` |
| `format` kind | `email`, `url`, `uuid`, `ip`, `date` | `format(phone)` |

When both bounds are literals, `min` must not be greater than `max`. Size and
duration literals such as `100mb` or `5s` are accepted on numeric fields only.

How `format` is enforced depends on the target:

| `format` | Node (Zod) | Python (`src/lib/formats.py`) |
|----------|------------|-------------------------------|
| `email` | `.email()` | `formats.Email` |
| `url` | `.url()` | `formats.Url` |
| `uuid` | `.uuid()` | `formats.Uuid` |
| `ip` | `.ip()` | `formats.IpAddress` |
| `date` | `.date()` | `formats.Date` |

On Python, each of these is a `str` subclass, so the parsed value is still a
plain `str`. A value that fails the check answers `422`, FastAPI's validation
status. The generated module is stdlib only. Python applies `format` to endpoint
inputs and model fields.

`min` and `max` on model fields are carried into each target's generated model
schemas, using the same type mapping. Length bounds apply to `string`, `text`,
lists, and maps, and value bounds to `int`, `float`, and `money`. On Python they
become pydantic `Field` bounds in `src/models/pydantic.py` (`min_length`/`max_length`,
`ge`/`le`). On Node they become Zod `.min`/`.max` in `src/types/schemas.ts`,
with a key-count check for maps. Neither target checks saved rows against these
bounds: requests are checked against endpoint input constraints, and the Drizzle
and SQLAlchemy tables carry no bounds.

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

On `--target python`, an inline enum endpoint input (query, body, or path) becomes `Literal["a", "b"]`, and a `default(...)` names one of its variants. An inline enum model column keeps its string values and is a named Postgres type, `lowercase(model + Field)` (`job.kind` → `jobkind`), the same name Node uses.

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

On `--target python`, a `type` becomes a Pydantic `BaseModel` in `src/models/types.py`. Its fields accept `required`, `optional`, `min`, `max`, and `format`; a `default` on a type field is rejected. Endpoint inputs of a `type` take a body (`POST`, `PUT`, `PATCH`), not a query. A model field typed by a `type` is rejected for now. An endpoint input typed `json<T>` over a declared `type` or enum becomes that class on Python.

### `alias` — Type Aliases

Single-type aliases with optional constraints.

```bp
alias UserId   = uuid
alias Email    = string format(email)
alias Url      = string format(url)
alias FileSize = int min(0) max(100mb)
```

Supported formats: `email`, `url`, `uuid`, `ip`, `date`

On `--target python`, an alias becomes `Name = Annotated[...]` in `src/models/types.py`, with `min`, `max`, and `format` applied. An alias of another alias is supported; `format` on that second level is rejected, so put the format on the base alias. A model field typed by a scalar alias stores as that scalar, and its read model uses the alias type. A primary key typed by an alias is rejected.

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

On `--target python`, a simple enum becomes a `str` Enum in `src/models/enums.py` whose values are the variant strings. A model column typed by the enum uses a SQLAlchemy `Enum` named with the lowercased enum name (`Status` → `status`), the same Postgres type name Node's `pgEnum` creates. Endpoint inputs typed by the enum validate the value and answer `422` for anything else. Variant names must not start with `_` or be `mro`, which Python reserves. Rich enums (variants with bodies) are not yet supported on Python.

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

On `--target python`, each pipe becomes `src/pipes/<name>.py`, a synchronous function that takes the `<-` input (or `input` when there is none) and returns the `->` value. Guards raise an `HTTPException` with the guard's message. The Python target accepts only these pipe bodies: guards, `->` outputs without a status code, block literal bindings, and calls to declared `fn` or `pipe` names. Data operations, `log`, `when`, `try`/`recover`, `env`/`header` reads, nested field access, and recursive pipe call graphs fail the build with an error that names the construct.

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
| `auth` | `auth jwt using(secret.JWT_SECRET)` | Enforced for `bearer`, `jwt`, `api_key`, and `basic` with `using(secret.NAME)`, and for `webhook_sig`; metadata only otherwise (see Auth Strategies). On Python, `auth bearer|jwt|api_key|basic|webhook_sig` with `using(secret.NAME)` are accepted |
| `limit` | `limit 60/min` | Enforced by an in-process, per-instance counter keyed by client address and path (behind a reverse proxy, set `FORWARDED_ALLOW_IPS`; see [Deployment](deployment.md#multi-instance-and-security-caveats)) |
| `cache` | `cache 5min` | GET only: sets `Cache-Control` on 2xx responses (see below); ignored on other methods |
| `tags` | `tags ["a", "b"]` | Included in generated OpenAPI metadata |
| `timeout` | `timeout 30s` | Parsed, but REST handler cancellation is not generated yet |

`cache <duration>` takes a duration literal with a unit (`5s`, `5min`, `1h`,
`1d`). It must be positive and a whole number of seconds; a bare number such as
`cache 5`, a zero value, or a sub-second value such as `500ms` fails codegen
with no files written. On the Node target, a GET endpoint with `cache` returns
`Cache-Control: <scope>, max-age=N` on 2xx responses only. Error responses and
handlers that already set `Cache-Control` are left alone. The scope is:

- `private` when the endpoint has any `use`, `auth`, or `limit` metadata, when
  the blueprint declares any global `use`, or when the body reads `header`,
  `headers`, `cookie`, `cookies`, `session`, or `auth`. Shared caches must not
  store these responses.
- `public` otherwise.

A non-GET endpoint with `cache` emits no header, and neither does a STREAM or WS
endpoint. The build still succeeds, and the `cache-on-non-get` lint warning
reports it.

On `--target python`, the same rules apply through a `cache_control(...)` helper
in `src/lib/http_cache.py`. The helper sets the header on the returned 2xx
response, so a guard's 4xx or any other error response carries none. `limit` is
enforced by `src/lib/rate_limit.py` with the same client-address-and-path key and fixed
window as Node, and an over-limit request answers `429` with
`{"error": "Rate limit exceeded"}`. `tags` become the route's FastAPI tags, which
show in the generated OpenAPI. `timeout` metadata and bare `auth` lines still
fail the Python build; `auth bearer|jwt|api_key|basic|webhook_sig using(secret.NAME)`
is enforced (see Auth Strategies).

### Auth Strategies

| Strategy | Node runtime status |
|----------|---------------------|
| `auth bearer using(secret.NAME)` | Enforced: `Authorization: Bearer <token>` must equal the secret value |
| `auth jwt using(secret.NAME)` | Enforced: `Authorization: Bearer <jwt>`; HS256 only, signed with the secret; `exp` and `nbf` are checked |
| `auth api_key using(secret.NAME)` | Enforced: the `X-API-Key` header must equal the secret value |
| `auth basic using(secret.NAME)` | Enforced: `Authorization: Basic <base64(user:password)>`; the secret holds `user:password` |
| `auth webhook_sig using(secret.KEY)` | Enforced with HMAC-SHA256 (hex) over the raw body, against `X-Webhook-Signature`. An unset or empty secret fails closed. A bad, non-hex, or missing signature, or one made with an empty key, answers `401` with `{ "error": "Invalid signature" }`. A verified body that is not JSON answers `400` with `{ "error": "Invalid payload" }`. The parsed body is bound to `data`. Python enforces the same contract in `src/lib/webhook.py`. The signature covers the body, so it is accepted only on `POST`, `PUT`, `PATCH`, and `DELETE`; on `GET`, STREAM, and WS it is error `C056` |
| `auth bearer`, `auth jwt`, `auth api_key`, `auth basic`, `auth session` (no `using`) | Parsed metadata; does not verify anything by itself |

An enforced check runs before the handler, before `use` middleware and input
validation. When it fails, the response is `401` with `{ "error": "Unauthorized" }`
and the handler does not run. On WS, the upgrade is refused. `bearer` and `jwt`
send `WWW-Authenticate: Bearer`, `basic` sends `WWW-Authenticate: Basic realm="<blueprint name>"`,
and `api_key` sends no challenge. Secrets are compared in constant time. An unset
or empty secret fails closed.

STREAM and WS also accept the credential in the query string, because browsers
cannot set headers on `EventSource` or `WebSocket`: `?token=` for `bearer` and
`jwt`, `?api_key=` for `api_key`. `basic` and REST endpoints accept headers only.

The verified token payload of an `auth jwt using(secret.NAME)` endpoint is
available as `claims` in every handler of that endpoint: the REST body, the
STREAM body and its `stream` block, and the WS top-level statements, `on_connect`,
`on_message`, and `on_disconnect`. Read any field as `claims.<field>`, including
nested fields such as `claims.org.id`. Claim names are used as written, so
`claims.user_id` reads the `user_id` claim. Only `jwt` carries claims: `bearer`,
`api_key`, and `basic` do not.

```bp
GET /api/me {
  auth jwt using(secret.JWT_SECRET)
  |> user = query user where(id == claims.sub) first
  -> user
}
```

`claims` is reserved on a jwt endpoint. A path parameter or `<- claims` input,
a step binding `|> claims = ...`, an `inject ... as claims`, or a `use`
middleware that injects `claims` is C055. Elsewhere `claims` is an ordinary
name: unbound (C035) unless the endpoint binds it itself. On `--target python`,
`claims` is the first handler parameter of a REST jwt endpoint, and `claims.<field>`
renders as a dict lookup, `claims.get("<field>")`.

The generated check lives in `src/lib/auth.ts`, which is emitted only when an
endpoint enforces a strategy. Without `using(...)`, the strategy stays metadata
only. The `unenforced-auth` lint warns about REST and STREAM endpoints that
declare it that way, unless the endpoint or the blueprint has a `use` middleware.
For sessions, and for checks the strategies above do not cover, declare and
apply a middleware whose `before` block validates the credential and `inject`s
the authenticated value. Do not rely on a bare `auth` line as a security boundary.

On `--target python`, the four REST strategies with `using(secret.NAME)` are
enforced by `src/lib/auth.py`, as a FastAPI route dependency that runs before
`use` middleware and input validation. It answers with the same 401 body,
challenges, and fail-closed secret handling as Node, and it has no query-string
fallback. Bare `auth` lines fail the Python build. A
request whose JSON body cannot be parsed at all gets `422` before the check,
because FastAPI parses the body first, while a body that parses but fails
validation gets `401`.

`auth webhook_sig using(secret.NAME)` is verified by `src/lib/webhook.py` on
Python REST endpoints, with the same `401` and `400` bodies as Node. The check
runs before input validation, so an unsigned request gets `401` even when its
body is malformed. The exception is an endpoint with a `<-` body input, when the
request is sent as `application/json`: FastAPI parses that body before the check,
so an unparseable JSON body gets `422` first. Node's body validator also runs
before the signature check. The verified payload is bound to `data`. A field read such as `data.items` or
`data.customer.id` returns that key as on Node, even when the key names a dict
method, and a missing key or a missing nested object reads as `None`. Bare
`auth webhook_sig` fails the Python build.

### Input Sources

| Method | Default source |
|--------|----------------|
| GET, DELETE | Query string |
| POST, PUT, PATCH | JSON body |
| Any | `header.X-Name` for headers |
| Any | `:param` in path for path params |

On the Node target, every declared REST path input is validated at runtime
against its declared type and constraints. Numbers, booleans, and enums are
parsed from the segment text (`int`, `float`, `bool`, inline enums, and named
enums), and `min`/`max` apply to the parsed value or the string length. A
segment that fails answers `400` with the `{ success: false, error }` body
before the handler runs, so `GET /api/items/abc` for `<- id int` answers `400`.
`optional` and `default` have no effect on a REST path input, since a matched
segment is always present.

On the Python target, GET/DELETE inputs become FastAPI `Query(...)` parameters,
POST/PUT/PATCH inputs become embedded `Body(...)` parameters, and supported
`min`/`max` constraints are preserved. Path inputs keep `min`/`max` as FastAPI
`Path(...)` bounds, so an out-of-range segment answers `422` rather than Node's
`400`. A path input with `optional` or `default` fails the Python build, because
the target does not support those constraints on path parameters.

STREAM and WS path inputs are validated the same way on the Node target (see
[STREAM Endpoints](#14-stream-endpoints) and [WS Endpoints](#15-ws-endpoints)).
Unlike REST, those endpoints reject `optional` and `default` on a path input
with `bp check` (C022).

On the Python target, `format(email|url|uuid|ip|date)` is enforced on endpoint
inputs and on model fields through the generated `src/lib/formats.py` (stdlib
only; no `email-validator` dependency), and a violating value answers `422`. See
[Constraint Values](#constraint-values) for the mapping. Direct header
references, including hyphenated names such as `header.X-API-Key`, become valid
`Header(..., alias="X-API-Key")` parameters. An `env.FIELD` reference must be
backed by a declared `secret FIELD` or by an infrastructure setting Blueprint
generates (for example `DATABASE_URL`); otherwise Python codegen fails.

### Route Conflicts

Two endpoints cannot register the same route. A GET, STREAM, and WS endpoint
are all GET routes, and path parameter names do not make a route distinct, so
`GET /rooms/:id`, `STREAM /rooms/:room_id`, and `WS /rooms/:x` conflict with
each other. Each duplicate fails `bp check` with C005. Change the path to keep
both endpoints.

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
    }
    |> on timeout(5min) {
      -> { error: "Timed out" }
    }
  }
}
```

`|> close` is only valid in WS handlers; using it in a STREAM handler fails
`bp check` with C035.

When the service runs on more than one instance, set `REDIS_URL`. A STREAM
listener then receives events that `emit` publishes on any instance: each
`emit` is also published to Redis on the channel `bp:stream:<event>`, and every
other instance forwards it only to its own STREAM listeners. Subscribe handlers
never run from Redis, so their side effects happen once, on the emitting
instance. Without `REDIS_URL`, or when Redis is unreachable, listeners receive
only events emitted on their own instance, and one error is logged. Event
payloads cross instances as JSON, so `Date` values arrive as strings.

Each connected client has a bounded write queue. Writes for one client never run
concurrently, and `emit` never waits for a slow reader. When a client stays more
than `BP_SSE_MAX_PENDING` events (default 256) behind for 5 seconds, or reaches 16
times that many pending events, the server stops delivering to it: it aborts that
stream, removes its listeners, and logs one `slow consumer` warning with the
endpoint path. A burst emitted in one tick does not drop a client that keeps up.
Other clients are unaffected. The limit is read from the environment once at
startup; a missing, non-numeric, or non-positive value falls back to 256.

`<-` inputs on a STREAM must name a `:param` segment of the path, be a primitive
or inline enum type, and not be `optional` or `default`. Each one is validated
(e.g. `<- id uuid required` for `/api/jobs/:id/progress`) before the stream
opens; an invalid value returns `400` with the `{ success: false, error }` body
that REST body and query validation returns. Other `<-` inputs are rejected by
`bp check`.

A STREAM can use `use` middleware. As on REST routes, the variable that
middleware injects keeps its declared type: `auth` from `|> inject key as auth`
has the injected model's row type, not `any`.

In OpenAPI output: exposed as `GET` with `text/event-stream` content type.

### Python target

`bp build --target python` emits STREAM endpoints with the same frames and
headers as Node: each `on event` sends `event: <name>` and `data: <JSON>`, each
`on timeout` sends `event: ping`, and nothing else is sent. Responses are
`text/event-stream` with `Cache-Control: no-cache` and `Connection:
keep-alive`. Differences:

- Input errors answer `422` (FastAPI validation), not `400`.
- Pre-stream statements run before the response starts. A guard answers
  `{"error": "..."}` with its status, as on Node. The statements run on a
  short-lived database session that closes before streaming, so a long stream
  holds no pooled connection.
- `use` middleware runs before the stream opens. Each one reads the request's
  headers, runs on a short-lived database session, and injects its value. The
  injected name is readable in pre-stream statements and in every `on event` and
  `on timeout` handler. A failing guard answers its status with `{"error": ...}`
  and no stream starts. The injected value is a detached row, so read loaded
  columns only, not relationships.
- Credentials are verified as on REST, with the same `401` body. On STREAM only,
  `?token=` (bearer, jwt) and `?api_key=` (api_key) are accepted when the header
  is absent. Basic stays header-only.
- Payload keys are camel-cased, as on Node: `emit new_message { room_id: id }`
  sends the `roomId` key, `event.room_id` and `where(room_id == id)` read
  `roomId`, and STREAM frame keys are camel-cased too. REST JSON responses and
  WS direct replies keep the `.bp` spelling on both targets.
- An event field that is missing from the payload drops its key from the
  frame, as Node does. A field that is present with a null value sends `null`.
- Timestamps in frames use FastAPI's encoding (`2026-10-19T12:00:00.123456`),
  not JavaScript's `toISOString()` form (`2026-10-19T12:00:00.123Z`).
- Backpressure counts queued messages per client, like Node. It does not count
  socket bytes on either target. `BP_SSE_MAX_PENDING`, the 5s grace period, the
  16x hard cap, and the `slow consumer` warning behave the same.
- With `REDIS_URL` set in the process environment, emitted events fan out on
  `bp:stream:<event>` as on Node. Cross-language interop is not a goal: a Node
  instance and a Python instance on the same Redis would read each other's
  messages, but the payload keys differ (`roomId` versus `room_id`), so run one
  runtime per Redis deployment.
- A client that disconnects is unsubscribed at once and its timers are cancelled.
- `header.X` in a pre-stream statement or a handler reads the request that opened
  the stream. The values are read once, before the stream opens. An absent header,
  or an unset `env.X`, drops its key from the frame, as on Node.
- `try/recover` before the stream block runs on the pre-stream session. A guard
  inside its `try` goes to the `recover` branch, as on Node.
- A handler that calls a `fn` runs the call in a worker thread and sends the frame
  when the call returns. Two events whose calls finish out of order send their
  frames out of order, on both targets.

Not supported on Python yet: `cache`, a bare `header` or `env`
name or a read deeper than one field (`header.X.y`), and handler output that is
not an object literal. `bp build` names the reason. WS endpoints are translated on
Python; section 15 lists their notes.

---

## 15. WS Endpoints

WebSocket endpoints.

```bp
WS /ws/chat {
  auth bearer using(secret.CHAT_TOKEN)

  on_connect {
    |> log "Client connected"
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
    |> log "Client disconnected"
  }
}
```

`<-` inputs on a WS must name a `:param` segment of the path. They are validated
before the handshake with the same rules as STREAM inputs. An invalid value gets
an HTTP `400` and the upgrade is refused; the JSON error body is only visible to
non-upgrade requests, since the upgrade socket carries status alone.

Room broadcasts check each socket before sending. A socket that is not open is
skipped. A socket whose unsent buffer exceeds `BP_WS_MAX_BUFFERED_BYTES` (default
1 MiB) is a slow consumer: the server removes it from all of its rooms, closes it
with code 1013 and reason `slow consumer`, and logs one warning. Replies sent
directly to the current socket are not limited by this check. The limit is read
once at startup; an invalid or non-positive value falls back to the default.

`auth` on a WS endpoint is checked before the upgrade. Use an enforced strategy,
such as `auth jwt using(secret.JWT_SECRET)`; the client sends the token in the
`Authorization` header or, since browsers cannot set WebSocket headers, in
`?token=`. A bare `auth bearer` (no `using`) is metadata only, and the
`unenforceable-ws-auth` lint warns about it. With `auth jwt`, the verified payload
is `claims` in `on_connect`, `on_message`, and `on_disconnect`, for browser clients
that pass `?token=` too. A WS endpoint that needs the sender's identity reads
`claims.sub` rather than decoding the header.

```bp
WS /ws/rooms/:id {
  <- id uuid required

  on_connect {
    -> { type: "connected", room: id }
  }
}
```

### Realtime Built-ins

```bp
join room(name)                            # join a pub/sub room
leave room(name)                           # leave a room
broadcast room(name) { data }              # send to all in room
whisper connection(id) { data }            # send to one connection
close                                      # close this connection
```

`join`, `leave`, and `broadcast` take a room name as a value, so a WS endpoint
needs no `model room` declaration. They are only valid inside `on_connect`,
`on_message`, and `on_disconnect`; in a REST or STREAM handler, `bp check`
reports C035.

`|> close` closes the current connection. It is only valid inside `on_connect`,
`on_message`, and `on_disconnect`; anywhere else, including STREAM and REST
handlers, `bp check` reports C035:

```bp
WS /ws/chat/:channel {
  <- channel string required

  on_message {
    |> when message.type == "leave" {
      |> leave room(channel)
      |> close
    }
  }
}
```

Every WS connection gets an id when it opens: `connection.id`, a UUID string.
`connection` is bound only inside `on_connect`, `on_message`, and
`on_disconnect`, and its only field is `id`; `connection.user` is a C054 error,
and `connection` anywhere else is C035. The name is reserved in WS endpoints, so
a `:connection` path parameter or a step binding named `connection` is also
C054. Send the id to another client, store it, or use it as the target of
`whisper`:

```bp
WS /ws/chat {
  on_connect {
    -> { type: "connected", id: connection.id }
  }

  on_message {
    |> when message.type == "dm" {
      |> whisper connection(message.to) { type: "dm", from: connection.id, body: message.body }
    }
  }
}
```

`whisper connection(id) { data }` sends the JSON-encoded `data` to the one
connection whose `connection.id` is `id`. Like `close`, it is only valid inside
WS handlers. The target must be `connection(...)` with one id argument, and the
data block is required: `whisper room(name)` is C054, so use
`broadcast room(name) { data }` for rooms. A whisper to an id that is not
connected anywhere is a no-op, not an error.

Delivery is per process. When the id is connected to the instance that runs the
handler, the message is written directly to that socket. Otherwise, with
`REDIS_URL` set, the instance publishes it on the `bp:ws:whisper` channel, every
instance subscribes to that channel, and the instance that holds the connection
delivers it. Without `REDIS_URL`, a whisper to a connection on another instance
is dropped. Whisper sends use the same slow-consumer limit as room broadcasts;
a slow target is closed with 1013 and removed from the registry.

A WS endpoint can use `use` middleware too. The variables it injects are typed
inside the handlers as they are on REST routes. The middleware runs in the
handshake, after enforced `auth`; a failing guard refuses the upgrade with its
status.

### Python target

The Python target translates WS endpoints. Each endpoint becomes a route in
`src/routes/<resource>_ws.py`, backed by `src/lib/realtime.py` (rooms, whisper,
the send queue, and Redis fan-out). The client-visible behavior matches Node,
with these differences:

- The handshake runs `auth`, then `use` middleware, then path inputs, then
  `limit`, then the upgrade, in the same order as Node. A refusal carries its
  status and no body: `401` for auth, the guard's status for a failing `use`
  middleware, `400` for a path input, and `429` for `limit`. Python sends it
  through the `websocket.http.response` extension, which uvicorn implements. A
  server without the extension closes the socket before accept, and the server
  answers `403`.
- Node's source also sets a JSON body (`{"error": ...}`) and, for bearer and jwt,
  `WWW-Authenticate: Bearer` on a refused handshake. `@hono/node-ws` drops both on
  an upgrade request, so the client never sees them. Python does not send them
  either, so a client gets the same response from both targets.
- Each handler (`on_connect`, each `on_message`, `on_disconnect`) runs on a
  worker thread with its own short-lived database session. No session lives for
  the connection. Bindings made in `on_connect` (for example `room` and `sender`)
  are detached objects for the rest of the connection. Columns loaded by the
  `fetch` are readable; a relationship that was not loaded, or an attribute
  expired by a commit in `on_connect`, is not.
- The value a `use` middleware injects is readable in `on_connect`, `on_message`,
  and `on_disconnect`, detached like an `on_connect` binding. The middleware
  runs once per handshake on a short-lived session, before the socket accepts.
- Room membership is per process, shared by every WS endpoint of the app. On
  Node each WS route file keeps its own room map.
- A room broadcast or whisper sends the same JSON text as Node, on the same
  `room:<id>` and `bp:ws:whisper` channels, with the same `{to, data}` whisper
  envelope. With `REDIS_URL` set in the process environment (a value only in `.env`
  is not read on Python, as with STREAM), each instance receives a publish through its own
  subscription, so a message reaches each socket once, including on the sender's
  instance. On both targets, a broadcast after the first join of a room on an
  instance waits for Redis to confirm that room's subscription, so the joiner's
  own `joined` frame arrives.
- Frames are encoded like `JSON.stringify`: compact, in insertion order, with
  `Date` values as ISO strings with milliseconds and a `Z` suffix. A value that is
  `undefined` on Node is `null` on Python, so the key stays in the frame.
- Invalid JSON, an empty or overlong `message.body`, and an unexpected error in a
  handler answer the same `{"error": ...}` frames as Node, and the process keeps
  serving. A guard in `on_connect` sends the error and closes with `1008`.
- A path input must be `string`, `int`, or `uuid`. A value that fails the type
  answers `400` before the upgrade, with no body, as on Node.
- `|> close` closes with code `1000` and an empty reason, as on Node.
- `BP_WS_MAX_BUFFERED_BYTES` (default 1 MiB) has the same meaning. Python counts
  the bytes it has queued for a socket, Node counts `bufferedAmount`, and both
  drop a stalled reader with `1013 slow consumer` once the limit is passed. Direct
  replies to the current socket are not limited on either target.
- Timestamps in WS frames use the Node form (`toISOString()`), not the FastAPI
  form used by STREAM frames.
- `header.X` in a handler reads the handshake's header. The headers are read
  before the socket accepts, so every handler sees the values of the request that
  opened the connection. `env.X` reads the same settings as in REST. An absent
  header, or an unset `env.X`, drops its key from the frame, as on Node.
- `try/recover` runs as on Node. Frames that a failed `try` already sent stay sent.
  Its database writes roll back when the `try` holds two or more writes and no
  guard or output, the same rule as REST (a SAVEPOINT on Python). A guard inside
  the `try` goes to the `recover` branch.
- An inline `when` runs its step under the condition, for example
  `|> when message.body == "close": close`.
- A handler calls a declared `fn` on its worker thread, as a REST body does.

Still rejected on Python, with a reason from `bp build`: a bare `header` or `env`
name or a read deeper than one field, `.length` on a value that is not a message
field, a step outside the translated set, and a handler output that is not an
object literal. (`auth webhook_sig` on WS and STREAM is error `C056` on both
targets: the handshake has no body to sign.) The
translated set is the realtime built-ins (`join`, `leave`, `broadcast`, `whisper`,
`close`), `emit`, `inject`, `log`, the data operations (`save`, `fetch`, `query`,
`update`, `delete`), `when`, `guard`, `try`/`recover`, and declared `fn` calls.

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
| `retry` | `retry 3 backoff(exponential, base: 1s, max: 30s)` | Three retries after the initial attempt, with capped exponential delay |
| `timeout` | `timeout 5min` | Max execution time |

On the Node target, `retry N` means N additional attempts after the initial
attempt, so the generated BullMQ producer receives `attempts: N + 1`. A plain
`retry N` retries immediately. `backoff(fixed, base: ...)` and
`backoff(exponential, base: ...)` emit BullMQ backoff options; `max` is enforced
by a generated custom capped strategy. A backoff clause must declare `base` or
`delay`, and unsupported strategies or keys fail before files are returned.

`enqueue "queue" { ... }` sends the payload with camelCase keys, as `emit`
does, and a worker input `<- job_id` reads the `jobId` key. A producer on
another runtime must send the same keys.

`enqueue` is currently supported only inside HTTP endpoint bodies. Its static
queue name must resolve to exactly one worker trigger and its payload is
required. Missing or ambiguous workers, malformed calls, and enqueue calls in
functions, pipes, middleware, realtime handlers, workers, schedules,
subscriptions, or authored test setup fail closed instead of producing a
module with an undefined queue binding.

**On the Python target** (`--target python`), workers and `enqueue` use the
official `bullmq` 3.4.x client, which runs the same Lua scripts as Node BullMQ,
so a Node and a Python service sharing one Redis exchange jobs. `enqueue` is a
call to `enqueue_job` in `src/lib/queue.py` from the handler's threadpool; the
producer job options are the same as on Node. Each worker becomes
`src/workers/<name>.py` with `run_<name>` and `on_fail_<name>` functions and
runs in a `Worker` started by the app lifespan (`src/lib/workers.py`). The
worker timeout rejects the wait but does not cancel the body, as on Node. A
`guard` in a worker body logs its message and returns, as on Node. A worker
has no response, so `->` outputs, `emit`, and `header` reads are rejected, and
so is a bare `update <model>` or `delete <model>` that has no binding from an
earlier step in the same body.

Payload keys are camelCase on the wire, as for `emit`: `enqueue "q" { job_id: x }`
sends `jobId`, and a worker reads its `<- job_id` input from `jobId`. `REDIS_URL`
is required whenever a spec declares a worker, an `enqueue`, or a `schedule`.
Under pytest, workers and schedules do not start, and `enqueue_job` raises
instead of waiting for a Redis connection, so the generated contract tests omit
endpoints that enqueue.

Startup does not wait for Redis on either target. Node workers reconnect on
their own; on Python the app lifespan starts the workers once Redis answers
`PING`, logging a warning and retrying every 5s while it does not. While Redis
is down, a Node `enqueue` waits for the connection to come back, and a Python
`enqueue` answers `500` after 30s.

The generated timeout rejects the worker wait after the declared duration but
does not cancel work already in progress.

### `on_fail`

Runs after the final ordinary processor attempt declared by `retry`. Earlier
processor failures are rethrown for BullMQ to retry without running the
compensation body:

```bp
on_fail {
  |> update job { status: "failed" }
  |> emit job_failed { job_id: job.id }
}
```

This hook currently runs from the generated processor catch. BullMQ terminal
paths that bypass that catch—notably stall exhaustion or an early
`UnrecoverableError`—do not yet invoke `on_fail`.

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

**On the Python target** (`--target python`), each schedule is a BullMQ job
scheduler on the `__bp_scheduler` queue, the same mechanism as on Node. At
startup the app registers one scheduler per declaration and removes schedulers
whose names are no longer declared, and one `Worker` on that queue runs the
schedule body. A tick therefore runs once across all instances that share the
Redis, as on Node. The body runs the same statements as an endpoint body, minus
the request: `->` responses, `<-` inputs, `header` reads, and `emit` are
rejected at build time. A failing run is retried by BullMQ 3 times with
exponential backoff from 1s, as on Node. Both targets use the process-local
timezone. The scheduler starts once Redis answers; until then the app serves
requests and logs a warning every 5s. It does not start under pytest.

**On the Node target** (`--target node`), each schedule is a BullMQ 6 job
scheduler on the `__bp_scheduler` Redis queue, whose id and job name are the
schedule name. Startup upserts one scheduler per declared schedule and removes
any scheduler whose name is no longer declared, so deleting or renaming a
schedule stops its old runs. A failing run is retried with 3 attempts and
exponential backoff from 1s. Registration runs in the background and retries
every 5s, so the HTTP server starts while Redis is down.

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

Subscribe handlers run only on the instance that called `emit`. Unlike STREAM
listeners, they are not sent to other instances through Redis.

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

The operands must be compatible: numbers (`int`, `float`, `money`) compare with
each other, strings with strings, and bools with bools. The checker reports
`C050` when both sides have a known type and the pair is incompatible, for
example `name > 3` with a string `name`. Arithmetic (`+`, `-`, `*`, `/`) and
unary `-` are checked the same way. Values of unknown type are not checked.

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
endpoint input or a declared function result typed as `json`. An endpoint input
typed `json<T>` over a declared `type` is a generated class, so `payload.field`
works there. The rejection does not emit
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
upload(body, bucket)              # upload to S3 → { url, key }
download(url)                     # read an s3:// URL → bytes
```

`upload` stores the body (a string or bytes) under a random UUID key with
`application/octet-stream` and returns `{ url: "s3://<bucket>/<key>", key }`.
`download` takes that URL and returns the object's bytes: a `Buffer` on Node.
Put the bytes in a JSON response only through a derived value such as
`bytes.length`; Node and Python serialize raw bytes differently. Node emits S3
for any `storage` value; `gcs` and `local` have no implementation yet.

**On the Python target**, `storage s3` is implemented with boto3
(`src/lib/storage.py`). `upload` stores the body under a random UUID key and
returns the same `s3://<bucket>/<key>` URL as Node. `download` takes that URL
and returns the object's bytes. The S3 settings are the standard AWS variables
(`AWS_REGION`, `AWS_ENDPOINT_URL`, `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`), read from the environment or
`.env`. Only `storage s3` is accepted on Python; `gcs` and `local` backends and
`delete_s3_object` are rejected there.

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

Python generates `emit` in REST handlers and in STREAM pre-stream statements. It
delivers to in-process STREAM listeners and, with `REDIS_URL` set, to other
instances. Payload keys are camel-cased, as on Node, so the two targets share
one Redis without a key mismatch. `emit` in WS handlers is
generated on Python too, and it uses the same bus as STREAM listeners.

### Utility

```bp
log "message" level(info)         # logging (debug|info|warn|error)
sleep 1s                          # delay execution
clock()                           # current timestamp in ms
hash(value)                       # SHA-256 hash
track("event", payload)          # analytics event dispatch
```

`level(...)` accepts `debug`, `info`, `warn`, or `error`. Any other name, such as
`level(warning)`, fails `bp check` with C051 (see
[C051](./error-codes.md#c051--unknown-log-level)).

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

A `guard` inside `try` on a REST endpoint answers its own status and message;
`recover` does not run. In WS handlers and before a STREAM block, a failing
`guard` inside `try` goes to `recover`, like any other failure. Both targets
behave this way.

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
