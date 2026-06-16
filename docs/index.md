---
layout: home
title: Blueprint
titleTemplate: A declarative language for web services

hero:
  name: Blueprint
  text: A declarative language for web services
  tagline: Describe what your service does. Blueprint compiles it to typed services -- TypeScript by default, Python with --target python.
  image:
    src: /logo.svg
    alt: Blueprint
  actions:
    - theme: brand
      text: Get Started
      link: /getting-started
    - theme: alt
      text: Language Reference
      link: /language-reference
    - theme: alt
      text: View on GitHub
      link: https://github.com/abdul-hamid-achik/blueprint

features:
  - title: Intent-First Design
    details: Every block starts with what it should do. Arrows show data flow at a glance -- inputs, steps, outputs.
  - title: Zero Runtime Lock-In
    details: Generated code uses standard libraries you already know -- Hono, Drizzle, and Zod by default, or FastAPI, SQLAlchemy, and Pydantic with --target python. No Blueprint runtime dependency.
  - title: LLM-Native
    details: Intent blocks and generation slots make the codebase navigable by AI. Use bp generate to fill in implementation details.
  - title: One Way to Do Things
    details: No if/else, no loops, no deep nesting. Guards for validation, when for conditions, map for iteration. Flat by force.
---

## What Blueprint Looks Like

Write this:

```bp
@ "Create a new todo"
POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id, title: todo.title }
}
```

Get this (TypeScript with Hono + Drizzle + Zod):

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

That's the default `node` target. Run `bp build --target python` for FastAPI + SQLAlchemy + Alembic, or `--target effect` for the early Effect-TS scaffold -- see the [Multi-Target Codegen guide](/multi-target-codegen).

## The Arrow System

Arrows on the left margin show data flow at a glance:

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
cd generated && bun install && bun run start
```

## Philosophy

1. **Describe intent, not implementation** -- Blueprint is read by humans and LLMs alike
2. **Zero runtime lock-in** -- Generated code uses standard libraries you already know
3. **One way to do things** -- The language has opinions; complexity has nowhere to hide
4. **LLM-native** -- Intent blocks and `@>` slots make the codebase navigable by AI
