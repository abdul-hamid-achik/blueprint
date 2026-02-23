# Getting Started

## Prerequisites

- Go 1.22+ (for building from source)
- Node.js 20+ (for running generated output)
- PostgreSQL (for services with a database)

## Installation

### Homebrew (macOS / Linux)

```bash
brew install abdul-hamid-achik/tap/bp
```

### Download a Release Binary

Download the latest binary for your platform from the [releases page](https://github.com/abdul-hamid-achik/blueprint/releases), then move it to your PATH:

```bash
# macOS (Apple Silicon)
curl -L https://github.com/abdul-hamid-achik/blueprint/releases/latest/download/bp_darwin_arm64.tar.gz | tar xz
sudo mv bp /usr/local/bin/

# Linux (amd64)
curl -L https://github.com/abdul-hamid-achik/blueprint/releases/latest/download/bp_linux_amd64.tar.gz | tar xz
sudo mv bp /usr/local/bin/
```

### Build from Source

```bash
git clone https://github.com/abdul-hamid-achik/blueprint
cd blueprint
go build -o bin/bp ./cmd/bp
sudo mv bin/bp /usr/local/bin/
```

### Verify Installation

```bash
bp version
# bp version 0.1.0
```

## Your First Service

### 1. Scaffold a new project

```bash
bp init my-service
cd my-service
```

This creates:

```
my-service/
└── my-service.bp      # your Blueprint source file
```

### 2. Look at the generated `.bp` file

```bp
@ "my-service — describe your service here"
blueprint "my-service" {
  version  "0.1.0"
  port     8080
  runtime  node
  database postgres
}

secret DATABASE_URL required

model item {
  id      uuid      primary
  name    string    required
  created timestamp default(now)
}

@ "List all items"
GET /api/items {
  <- page     int default(1) min(1)
  <- per_page int default(20) max(100)

  |> items = query item order(created desc) paginate(page, per_page)

  -> 200 { items: items.items, total: items.total }
}

@ "Create an item"
POST /api/items {
  <- name string required

  |> item = save item { name: name }

  -> 201 { id: item.id, name: item.name }
}
```

### 3. Validate and build

```bash
# Check for errors
bp check my-service.bp

# Compile to TypeScript
bp build my-service.bp
```

### 4. Run the generated service

```bash
cd generated
npm install
npm start
# Listening on port 8080
```

```bash
curl http://localhost:8080/api/items
# {"items":[],"total":0}
```

## Building a Service with a Database

### 1. Create the service file

```bp
@ "A simple TODO API"
blueprint "todo-api" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
}

secret DATABASE_URL required

model todo {
  id      uuid      primary
  title   string    required
  done    bool      default(false)
  created timestamp default(now)
}

@ "List todos"
GET /api/todos {
  |> todos = query todo
  -> 200 { todos: todos }
}

@ "Create a todo"
POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id, title: todo.title }
}

@ "Complete a todo"
PATCH /api/todos/:id {
  <- id   uuid required
  <- done bool required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "Not found"
  |> update todo { done: done }
  -> 200 { id: todo.id, done: done }
}

@ "Delete a todo"
DELETE /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "Not found"
  |> delete todo
  -> 204 "deleted"
}
```

### 2. Build and configure

```bash
bp build todo-api.bp

cd generated
cp ../.env.example .env
# Edit .env and set DATABASE_URL=postgresql://user:pass@localhost/mydb
```

### 3. Apply the database schema

```bash
npm install
npm run db:push    # create tables from Drizzle schema
```

### 4. Start the server

```bash
npm start
# todo-api listening on port 3000
```

## Development Mode

Watch mode rebuilds and restarts when you save:

```bash
bp dev todo-api.bp --out generated
```

## Running Tests

Write tests in your `.bp` file:

```bp
test list_todos {
  target GET /api/todos
  request {}
  expect {
    status 200
    body.todos exists
  }
}
```

Then run:

```bash
bp test todo-api.bp
```

## Next Steps

- [Language Reference](./language-reference.md) — learn all Blueprint constructs
- [CLI Reference](./cli-reference.md) — full command documentation
- [Examples](./examples.md) — complete worked examples
