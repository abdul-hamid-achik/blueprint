# Blueprint Documentation

Blueprint is a declarative language for writing web services. You describe what your service does — Blueprint compiles it to production-ready TypeScript (Hono + Drizzle + Zod).

## Documentation

| Document | What it covers |
|----------|---------------|
| [Getting Started](./getting-started.md) | Installation, quickstart, first project |
| [Language Reference](./language-reference.md) | Complete BP syntax — every construct |
| [CLI Reference](./cli-reference.md) | All `bp` commands with flags and examples |
| [Generated Output](./generated-output.md) | What gets generated and how to use it |
| [Examples](./examples.md) | Worked examples from hello-world to production API |
| [Architecture](./architecture.md) | Toolchain internals (for contributors) |

## What Blueprint Is

Write this:

```bp
@ "Create a new todo"
POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id, title: todo.title }
}
```

Get this (production-ready TypeScript with Hono + Drizzle + Zod):

```typescript
todosRoutes.post('/api/todos',
  zValidator('json', postTodosSchema),
  async (c) => {
    try {
      const { title } = c.req.valid('json');
      const todo = (await db.insert(schema.todos).values({ title }).returning())[0];
      return c.json({ id: todo.id, title: todo.title }, 201);
    } catch (err) {
      throw new BpError(500, 'Internal error');
    }
  }
);
```

## Core Concepts

**Arrows show data flow:**

| Arrow | Meaning | Example |
|-------|---------|---------|
| `<-` | Input | `<- name string required` |
| `\|>` | Step | `\|> user = fetch user(id)` |
| `->` | Output | `-> 200 { id: user.id }` |
| `@` | Intent | `@ "Create a user"` |
| `@>` | LLM slot | `@> implement this logic` |

**Flat by design:** Maximum one level of nesting. `try/recover` is the only exception.

**No if/else, no loops:** Use `guard` for validation and `when` for conditions. Use `map` for iteration.

## Quick Example

```bp
blueprint "my-api" {
  version  "1.0.0"
  port     3000
  runtime  node
  database postgres
}

secret DATABASE_URL required

model user {
  id    uuid   primary
  name  string required
  email string unique required
}

GET /api/users/:id {
  <- id uuid required
  |> user = fetch user(id)
  |> guard user -> 404 "Not found"
  -> 200 { id: user.id, name: user.name, email: user.email }
}

POST /api/users {
  <- name  string required
  <- email string required format(email)
  |> user = save user { name: name, email: email }
  -> 201 { id: user.id }
}
```

```bash
bp build my-api.bp
cd generated && npm install && npm start
```

## Philosophy

1. **Describe intent, not implementation** — Blueprint is read by humans and LLMs alike
2. **Zero runtime lock-in** — Generated code uses standard libraries you already know
3. **One way to do things** — The language has opinions; complexity has nowhere to hide
4. **LLM-native** — Intent blocks and `@>` slots make the codebase navigable by AI
