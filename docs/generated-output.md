# Generated Output

When you run `bp build my-service.bp`, Blueprint compiles your `.bp` file into a complete Node.js project. This page explains what gets generated and how to use it.

## Output Structure

```
generated/
├── package.json
├── tsconfig.json
├── Dockerfile
├── .env.example
└── src/
    ├── index.ts
    ├── types.ts
    ├── models/
    │   └── schema.ts
    ├── validation/
    │   └── schemas.ts
    ├── lib/
    │   ├── db.ts
    │   ├── env.ts
    │   ├── errors.ts
    │   ├── storage.ts
    │   ├── cache.ts
    │   └── queue.ts
    ├── routes/
    │   └── <resource>.ts
    ├── functions/
    │   └── <name>.ts
    ├── pipes/
    │   └── <name>.ts
    ├── middleware/
    │   └── <name>.ts
    ├── workers/
    │   └── <name>.ts
    └── schedules/
        └── <name>.ts
test/
└── <name>.test.ts
```

## Technology Stack

| Blueprint Concept | Library | Notes |
|-------------------|---------|-------|
| HTTP server | Hono | Lightweight, edge-compatible |
| Database ORM | Drizzle ORM | Type-safe, SQL-first |
| Validation | Zod | Request/response schemas |
| Background jobs | BullMQ | Redis-based job queue |
| Testing | Vitest | Fast, ESM-native |
| File storage | `@aws-sdk/client-s3` | S3 and compatible APIs |
| Auth (JWT) | jose | Standards-compliant |
| Logging | pino | Structured JSON logs |
| Migrations | Drizzle Kit | Pairs with Drizzle ORM |
| Redis | ioredis | Cache and queue backend |

The generated code has **no Blueprint runtime dependency**. Everything uses standard, widely-adopted libraries.

---

## File-by-File Reference

### `src/index.ts`

The Hono server entrypoint. Wires together all routes, middleware, and startup logic.

```typescript
import { Hono } from 'hono';
import { serve } from '@hono/node-server';
import { cors } from 'hono/cors';
import { requestLogger } from './middleware/request-logger.js';
import { todosRoutes } from './routes/todos.js';

const app = new Hono();

// Global middleware
app.use('*', requestLogger);
app.use('*', cors({ origin: ['https://example.com'] }));

// Routes
app.route('/', todosRoutes);

console.log('todo-api listening on port 3000');
serve({ fetch: app.fetch, port: 3000 });

export default app;
```

### `src/models/schema.ts`

Drizzle ORM table definitions. Maps each `model` block to a typed table.

```typescript
import { pgTable, uuid, varchar, boolean, timestamp } from 'drizzle-orm/pg-core';

export const todos = pgTable('todos', {
  id:      uuid('id').primaryKey().defaultRandom(),
  title:   varchar('title').notNull(),
  done:    boolean('done').default(false),
  created: timestamp('created').defaultNow(),
});
```

### `src/validation/schemas.ts`

Zod schemas for request validation. One schema per endpoint that has non-path inputs.

```typescript
import { z } from 'zod';

export const postTodosSchema = z.object({
  title: z.string(),
});

export const patchTodosSchema = z.object({
  done: z.boolean(),
});
```

### `src/types.ts`

TypeScript type definitions, including custom types, aliases, and rich enum config objects.

```typescript
export type Plan = 'free' | 'pro' | 'enterprise';

export const PlanConfig = {
  free:       { rate_limit: '10/min',   max_file: 5 * 1024 * 1024,    monthly_ops: 50 },
  pro:        { rate_limit: '60/min',   max_file: 50 * 1024 * 1024,   monthly_ops: 10000 },
  enterprise: { rate_limit: '1000/min', max_file: 500 * 1024 * 1024,  monthly_ops: 100000 },
} as const;
```

### `src/routes/<resource>.ts`

Hono route handlers. One file per URL resource group (e.g., `/api/todos` → `todos.ts`).

```typescript
import { Hono } from 'hono';
import { zValidator } from '@hono/zod-validator';
import { db } from '../lib/db.js';
import * as schema from '../models/schema.js';
import { postTodosSchema } from '../validation/schemas.js';
import { BpError } from '../lib/errors.js';

export const todosRoutes = new Hono();

// POST /api/todos
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

### `src/middleware/<name>.ts`

Hono middleware functions.

```typescript
import { createMiddleware } from 'hono/factory';
import { db } from '../lib/db.js';
import * as schema from '../models/schema.js';
import { eq } from 'drizzle-orm';
import { hash } from './lib/utils.js';
import { BpError } from '../lib/errors.js';

export const requireAuth = createMiddleware(async (c, next) => {
  const apiKey = c.req.header('X-API-Key');
  if (!apiKey) throw new BpError(401, 'Missing API key');
  const key = (await db.select().from(schema.apiKeys)
    .where(eq(schema.apiKeys.keyHash, hash(apiKey))).limit(1))[0];
  if (!key) throw new BpError(401, 'Invalid API key');
  (c as any).set('auth', key);
  await next();
});
```

### `src/functions/<name>.ts`

Function wrapper files. Contain the generated wrapper plus a stub for the implementation.

```typescript
// Generated wrapper
export async function watermark(
  file: File,
  text: string,
  position: string,
  opacity: number,
): Promise<File> {
  const { apply } = await import('./internal/watermark.js');
  return apply(file, text, position, opacity);
}
```

### `src/pipes/<name>.ts`

Pipe functions.

```typescript
import { BpError } from '../lib/errors.js';

export async function validateImage(file: File): Promise<File> {
  if (file.size >= 10 * 1024 * 1024) throw new BpError(413, 'File too large');
  const allowed = ['image/png', 'image/jpeg', 'image/webp'];
  if (!allowed.includes(file.type)) throw new BpError(415, 'Unsupported file type');
  return file;
}
```

### `src/workers/<name>.ts`

BullMQ worker definitions.

```typescript
import { Worker } from 'bullmq';
import { db } from '../lib/db.js';

new Worker('watermark_jobs', async (job) => {
  const { jobId } = job.data;
  // ... worker logic
}, { connection: redis });
```

### `src/schedules/<name>.ts`

Cron job handlers using `node-cron`.

```typescript
import cron from 'node-cron';
import { db } from '../lib/db.js';

cron.schedule('0 4 * * 0', async () => {
  // ... cleanup logic
});
```

### `src/lib/db.ts`

Drizzle database connection.

```typescript
import { drizzle } from 'drizzle-orm/postgres-js';
import postgres from 'postgres';
import * as schema from '../models/schema.js';

const client = postgres(process.env.DATABASE_URL!);
export const db = drizzle(client, { schema });
```

### `src/lib/errors.ts`

`BpError` class for typed HTTP errors.

```typescript
export class BpError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}
```

### `test/<name>.test.ts`

Vitest integration tests. One file per `test` block in the `.bp` source.

```typescript
import { describe, it, expect, beforeAll } from 'vitest';
import app from '../src/index.js';

describe('createTodo', () => {
  it('create_todo', async () => {
    const res = await app.request('/api/todos', {
      method: 'POST',
      body: JSON.stringify({ title: 'Buy milk' }),
    });
    const body = await res.json() as any;
    expect(res.status).toBe(201);
    expect(typeof body.id).toBe('string');
    expect(body.title).toBe('Buy milk');
  });
});
```

---

## Running the Generated Project

### Initial Setup

```bash
cd generated
cp ../.env.example .env
# Edit .env with your actual values

npm install
```

### Database (if using `database postgres`)

```bash
# Push schema to database (development)
npm run db:push

# Generate migration file (production)
npm run db:generate
npm run db:migrate
```

### Start the Server

```bash
npm start
# my-service listening on port 3000
```

### Run Tests

```bash
npm test
# or: npx vitest run
```

### Available npm Scripts

| Script | Description |
|--------|-------------|
| `npm start` | Start the server |
| `npm run dev` | Watch mode with tsx |
| `npm test` | Run Vitest tests |
| `npm run db:push` | Push Drizzle schema to database |
| `npm run db:generate` | Generate SQL migration |
| `npm run db:migrate` | Apply pending migrations |
| `npm run db:studio` | Open Drizzle Studio |

---

## Deploying

### Docker

A `Dockerfile` is generated automatically:

```bash
docker build -t my-service .
docker run -e DATABASE_URL=... -e STRIPE_KEY=... -p 3000:3000 my-service
```

### Environment Variables

Copy `.env.example` and fill in all values. Required secrets cause a startup error if missing.

```bash
cp .env.example .env
# Fill in values
```

---

## Modifying Generated Code

The generated code is yours. Every file starts with:

```typescript
// Generated by Blueprint v0.1.0 from my-service.bp
// Do not edit directly — modify the .bp source and run `bp build`
```

You can edit generated files directly — they're standard TypeScript with no hidden magic. If you re-run `bp build`, the output directory is regenerated. To preserve manual edits, either:

1. Eject: stop using `bp build` and maintain the TypeScript directly
2. Use `impl node { module: "..." }` to point `fn` blocks to your own TypeScript modules, which are not overwritten

The `src/functions/<name>-impl.ts` stub files are generated once and not overwritten on subsequent builds, so custom implementations there are safe.
