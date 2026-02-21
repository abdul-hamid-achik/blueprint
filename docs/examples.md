# Examples

Worked examples from minimal to production-ready.

## Hello World

The simplest possible Blueprint service.

```bp
@ "A simple hello world API"
blueprint "hello-world" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Health check"
GET /api/health {
  -> 200 { status: "ok", version: "1.0.0" }
}

@ "Greeting"
GET /api/hello/:name {
  <- name string required
  -> 200 { message: "Hello, {name}!" }
}
```

```bash
bp build hello-world.bp
cd generated && npm install && npm start
curl http://localhost:3000/api/hello/world
# {"message":"Hello, world!"}
```

---

## TODO API

A classic CRUD example with a database.

```bp
@ "A simple TODO API with CRUD operations"
blueprint "todo-api" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
}

secret DATABASE_URL required

model todo {
  id        uuid      primary
  title     string    required
  done      bool      default(false)
  created   timestamp default(now)
}

@ "List todos with pagination"
GET /api/todos {
  <- page     int default(1)  min(1)
  <- per_page int default(20) min(1) max(100)

  |> todos = query todo paginate(page, per_page)

  -> 200 {
    todos: todos.items,
    total: todos.total,
    page:  page,
  }
}

@ "Create a todo"
POST /api/todos {
  <- title string required

  |> todo = save todo { title: title }

  -> 201 { id: todo.id, title: todo.title, done: todo.done }
}

@ "Get a specific todo"
GET /api/todos/:id {
  <- id uuid required

  |> todo = fetch todo(id)
  |> guard todo -> 404 "Todo not found"

  -> 200 {
    id:      todo.id,
    title:   todo.title,
    done:    todo.done,
    created: todo.created,
  }
}

@ "Toggle todo completion"
PATCH /api/todos/:id {
  <- id   uuid required
  <- done bool required

  |> todo = fetch todo(id)
  |> guard todo -> 404 "Todo not found"
  |> update todo { done: done }

  -> 200 { id: todo.id, done: done }
}

@ "Delete a todo"
DELETE /api/todos/:id {
  <- id uuid required

  |> todo = fetch todo(id)
  |> guard todo -> 404 "Todo not found"
  |> delete todo

  -> 204 "deleted"
}
```

---

## API with Authentication

Adding API key authentication and rate limiting.

```bp
blueprint "my-api" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres

  use request_logger
}

secret DATABASE_URL required

model api_key {
  id        uuid   primary
  key_hash  string unique required
  plan      enum(free, pro) default(free)
  ops_used  int    default(0)
}

enum Plan {
  free { rate_limit: 10/min, monthly_ops: 100  }
  pro  { rate_limit: 60/min, monthly_ops: 10000 }
}

@ "Authenticate via API key and enforce quota"
middleware require_auth {
  before {
    |> guard header.X-API-Key               -> 401 "Missing API key"
    |> key = query api_key where(key_hash == hash(header.X-API-Key)) first
    |> guard key                            -> 401 "Invalid API key"
    |> guard key.ops_used < Plan[key.plan].monthly_ops -> 429 "Quota exceeded"
    |> inject key as auth
  }
}

@ "Process something"
POST /api/process {
  use   require_auth
  limit Plan[auth.plan].rate_limit

  <- input string required

  |> result = process_input(input)
  |> update api_key { ops_used: auth.ops_used + 1 }

  -> 200 { result: result }
}
```

---

## File Upload Service

Handling file uploads with validation and cloud storage.

```bp
blueprint "upload-service" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
  storage  s3
}

secret DATABASE_URL required
secret S3_BUCKET    required

env MAX_FILE_SIZE   10mb
env ALLOWED_TYPES   ["image/png", "image/jpeg", "image/webp", "application/pdf"]

model upload {
  id         uuid      primary
  url        string    required
  filename   string    required
  size       int       required
  mime_type  string    required
  created    timestamp default(now)
}

@ "Reject oversized or wrong-type files"
pipe validate_file {
  <- file file

  |> guard file.size < env.MAX_FILE_SIZE     -> 413 "File too large (max {env.MAX_FILE_SIZE})"
  |> guard file.type in env.ALLOWED_TYPES    -> 415 "Unsupported file type"

  -> file
}

@ "Upload a file"
POST /api/uploads {
  <- file     file    required
  <- filename string  required

  |> file = pipe validate_file(file)

  |> try {
    |> stored = upload(file, secret.S3_BUCKET)
    |> record = save upload {
      url:       stored.url,
      filename:  filename,
      size:      file.size,
      mime_type: file.type,
    }
  } recover {
    |> log "Upload failed: {error.message}" level(error)
    -> 500 { error: "Upload failed" }
  }

  -> 201 { id: record.id, url: stored.url, filename: filename }
}

@ "Get upload info"
GET /api/uploads/:id {
  <- id uuid required

  |> upload = fetch upload(id)
  |> guard upload -> 404 "Upload not found"

  -> 200 {
    id:        upload.id,
    url:       upload.url,
    filename:  upload.filename,
    size:      upload.size,
    mime_type: upload.mime_type,
    created:   upload.created,
  }
}
```

---

## Webhook Handler

Handling Stripe webhooks with signature verification.

```bp
blueprint "billing-service" {
  version "1.0.0"
  port    3000
  runtime node
}

secret STRIPE_KEY required

@ "Handle Stripe payment events"
POST /webhooks/stripe {
  auth webhook_sig using(secret.STRIPE_KEY)
  tags ["billing", "webhooks"]

  |> when data.type == "checkout.session.completed" {
    |> email = data.object.customer_email
    |> plan  = data.object.metadata.plan
    |> log "Payment received: {email} upgraded to {plan}"
    |> emit user_upgraded { email: email, plan: plan }
  }

  |> when data.type == "customer.subscription.deleted" {
    |> email = data.object.customer_email
    |> log "Subscription cancelled: {email}"
    |> emit subscription_cancelled { email: email }
  }

  -> 200 { received: true }
}
```

The `auth webhook_sig using(secret.STRIPE_KEY)` directive automatically generates HMAC-SHA256 signature verification:

```typescript
const _payload = await c.req.text();
const _sig = c.req.header('X-Webhook-Signature') ?? '';
const _expected = createHmac('sha256', process.env.STRIPE_KEY!).update(_payload).digest('hex');
if (_sig !== _expected) return c.json({ error: 'Invalid signature' }, 401);
const data = JSON.parse(_payload);
```

---

## Background Job Processing

Worker queues for async processing.

```bp
blueprint "processor" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
}

secret DATABASE_URL required

model job {
  id         uuid      primary
  status     enum(pending, processing, done, failed)
  input      string    required
  output     string    optional
  error      string    optional
  created    timestamp default(now)
}

@ "Enqueue a processing job"
POST /api/jobs {
  <- input string required

  |> job = save job { status: "pending", input: input }
  |> emit process_job { job_id: job.id }

  -> 202 { job_id: job.id, status: "pending" }
}

@ "Check job status"
GET /api/jobs/:id {
  <- id uuid required

  |> job = fetch job(id)
  |> guard job -> 404 "Job not found"

  -> 200 {
    id:     job.id,
    status: job.status,
    output: job.output,
    error:  job.error,
  }
}

@ "Process a job in the background"
worker process_job_worker {
  trigger queue("process_job")
  retry   3 backoff(exponential, base: 1s, max: 30s)
  timeout 5min

  <- job_id uuid

  |> job = fetch job(job_id)
  |> guard job.status == "pending" -> skip "Already processed"

  |> update job { status: "processing" }

  |> try {
    |> result = process_input(job.input)
    |> update job { status: "done", output: result }
  } recover {
    |> update job { status: "failed", error: error.message }
  }

  on_fail {
    |> update job { status: "failed", error: "Max retries exceeded" }
    |> emit job_failed { job_id: job.id }
  }
}
```

---

## Real-time Updates with SSE

Streaming job progress to clients.

```bp
blueprint "streaming-service" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
}

secret DATABASE_URL required

model job {
  id      uuid   primary
  status  string required
  percent int    default(0)
}

POST /api/jobs {
  <- input string required
  |> job = save job { status: "pending", percent: 0 }
  -> 202 { job_id: job.id }
}

@ "Stream job progress via SSE"
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
      -> { percent: 100, stage: "done", result: event.result }
      |> close
    }
    |> on timeout(30s) {
      -> { error: "Timed out waiting for job" }
      |> close
    }
  }
}
```

---

## Tests and Fixtures

Writing integration tests in Blueprint.

```bp
blueprint "test-service" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
}

secret DATABASE_URL required

model item {
  id    uuid   primary
  name  string required
  price int    required
}

POST /api/items {
  <- name  string required
  <- price int    required min(0)
  |> item = save item { name: name, price: price }
  -> 201 { id: item.id, name: item.name, price: item.price }
}

GET /api/items/:id {
  <- id uuid required
  |> item = fetch item(id)
  |> guard item -> 404 "Not found"
  -> 200 { id: item.id, name: item.name, price: item.price }
}

# --- Tests ---

@ "Create item should return 201 with correct fields"
test create_item {
  target POST /api/items

  request {
    body {
      name:  "Widget",
      price: 999,
    }
  }

  expect {
    status 201
    body.id is uuid
    body.name == "Widget"
    body.price == 999
    model item where(name == "Widget") exists
  }
}

@ "Get non-existent item should return 404"
test get_missing_item {
  target GET /api/items/00000000-0000-0000-0000-000000000000

  request {}

  expect {
    status 404
  }
}
```

---

## Scheduled Maintenance

Cron jobs for housekeeping.

```bp
blueprint "maintenance" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
  storage  s3
}

secret DATABASE_URL required
secret S3_BUCKET    required

model upload {
  id      uuid      primary
  url     string    required
  created timestamp default(now)
}

@ "Delete uploads older than 90 days every Sunday at 4am"
schedule cleanup_old_uploads {
  cron "0 4 * * 0"

  |> old = query upload where(created < 90.days.ago)
  |> map old: delete_s3_object(upload.url)
  |> delete old
  |> log "Cleaned {old.count} old uploads" level(info)
}
```

---

## Multi-file Project

Splitting a large service across files.

```bp
# main.bp
blueprint "my-service" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres

  use request_logger
  use cors { origins: ["https://myapp.com"] }
}

include "config.bp"
include "models.bp"
include "middleware.bp"
include "endpoints/users.bp"
include "endpoints/items.bp"
include "workers.bp"
include "schedules.bp"
```

```bp
# models.bp
model user {
  id    uuid   primary
  name  string required
  email string unique required
}

model item {
  id       uuid   primary
  owner_id uuid   ref(user)
  name     string required
}
```

```bp
# middleware.bp
middleware require_auth {
  before {
    |> guard header.Authorization   -> 401 "Missing token"
    |> user = authenticate(header.Authorization)
    |> guard user                   -> 401 "Invalid token"
    |> inject user as current_user
  }
}
```

```bp
# endpoints/items.bp
GET /api/items {
  use require_auth
  |> items = query item where(owner_id == current_user.id)
  -> 200 { items: items }
}
```
