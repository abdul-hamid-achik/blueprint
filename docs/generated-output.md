# Generated Output

When you run `bp build my-service.bp`, Blueprint compiles your `.bp` file into a complete Node.js project. This page explains what gets generated and how to use it.

## Output Structure

```
generated/
├── package.json
├── tsconfig.json
├── Dockerfile
├── .env.example
├── frontend/
│   ├── package.json
│   ├── README.md
│   ├── tsconfig.json
│   └── src/
│       ├── index.ts
│       ├── api.ts
│       ├── schemas.ts
│       ├── client.ts
│       ├── i18n.ts
│       └── react-query.ts   # optional via --react-query
└── src/
    ├── index.ts
    ├── types.ts
    ├── types/
    │   ├── api.ts
    │   ├── schemas.ts
    │   ├── client.ts
    │   └── react-query.ts   # optional via --react-query
    ├── models/
    │   └── schema.ts
    ├── validation/
    │   └── schemas.ts
    ├── lib/
    │   ├── analytics.ts
    │   ├── db.ts
    │   ├── env.ts
    │   ├── errors.ts
    │   ├── i18n.ts
    │   ├── save-migrations.ts
    │   ├── storage.ts
    │   ├── state.ts
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
    ├── saves/
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

TypeScript type definitions, including custom types, aliases, state-machine unions, and rich enum config objects.

```typescript
export type Plan = 'free' | 'pro' | 'enterprise';

export const PlanConfig = {
  free:       { rate_limit: '10/min',   max_file: 5 * 1024 * 1024,    monthly_ops: 50 },
  pro:        { rate_limit: '60/min',   max_file: 50 * 1024 * 1024,   monthly_ops: 10000 },
  enterprise: { rate_limit: '1000/min', max_file: 500 * 1024 * 1024,  monthly_ops: 100000 },
} as const;
```

When you use `state mission_status { ... }`, this file also includes generated state unions and transition metadata.

### `src/lib/i18n.ts`

Localization metadata and translation bundles declared with `locale` and `translation`.

```typescript
export const locales = ["en", "fr-FR"] as const;
export const defaultLocale = "en";
export const localeFallbacks = { "en": "en", "fr-FR": "en" } as const;
export const translationNamespaces = { missionText: ["mission.start"] } as const;
export const translationValues = {
  missionText: {
    en: { "mission.start": "Start mission" },
  },
} as const;
```

### `src/lib/state.ts`

Runtime helpers for `state` declarations.

```typescript
export function canTransitionMissionStatus(from: string, to: string): boolean {
  return ((MissionStatusTransitions as any)[from] ?? []).includes(to);
}

export function transitionMissionStatus<T extends string>(from: T, to: T): T {
  if (!canTransitionMissionStatus(from, to)) throw new InvalidTransitionError("mission_status", from, to);
  return to;
}
```

### `src/lib/analytics.ts`

Analytics event metadata plus the generated `track(...)` helper. HTTP sinks are queued, batched, and retried with backoff.

```typescript
await track("mission_started", { missionId: "m_123" });
```

### `src/lib/save-migrations.ts`

Save upgrade helpers generated from `save` declarations.

```typescript
const upgraded = await upgradePlayerProgressSave(saveData);
```

If you declare `migrate 1 -> 2 using "./custom-player-progress"`, this file imports that hook and the stub is generated in `src/saves/custom-player-progress.ts`.

### `src/types/api.ts`

Frontend-safe contract types generated from models, endpoint inputs and responses, SSE handlers, and WebSocket messages.

```typescript
export interface Todo {
  id: string;
  title: string;
  done: boolean;
  created: Date;
}

export interface GetTodosRequest {
  page?: number;
  per_page?: number;
}

export type GetTodosResponse = {
  todos: Todo[];
  total: number;
  page: number;
};
```

### `src/types/schemas.ts`

Zod schemas that mirror `src/types/api.ts`. These are designed for frontend form validation and runtime response parsing.

```typescript
import { z } from 'zod';

export const TodoSchema = z.object({
  id: z.string().uuid(),
  title: z.string(),
  done: z.boolean(),
  created: z.coerce.date(),
});

export const GetTodosRequestSchema = z.object({
  page: z.number().int().min(1).default(1),
  per_page: z.number().int().min(1).max(100).default(20),
});
```

### `src/types/client.ts`

Typed REST, SSE, and WebSocket clients. Requests and responses are validated with Zod by default.

```typescript
import { createApiClient } from './types/client';

const client = createApiClient({
  baseUrl: 'http://localhost:3000',
  validateResponses: true,
});

const todos = await client.rest.getTodos({ page: 1, per_page: 20 });
```

### `src/types/react-query.ts` (optional)

If you build with `bp build my-service.bp --react-query`, Blueprint also generates TanStack React Query hooks on top of the typed REST client.

```typescript
import { useGetTodosQuery, usePostTodosMutation } from './types/react-query';

const todosQuery = useGetTodosQuery(
  { page: 1, per_page: 20 },
  { baseUrl: 'http://localhost:3000' }
);

const createTodo = usePostTodosMutation({
  baseUrl: 'http://localhost:3000',
});
```

### `frontend/`

A standalone frontend package that mirrors the generated contract files in `src/types/`. This package is designed for monorepos, shared workspaces, or copying into a separate web app.

```bash
cd generated/frontend
bun install
bun run build
```

The package exports:

- `frontend/src/api.ts`
- `frontend/src/schemas.ts`
- `frontend/src/client.ts`
- `frontend/src/i18n.ts`
- `frontend/src/react-query.ts` when `--react-query` is enabled
- `frontend/src/index.ts` as a convenience barrel export

It also includes publish-oriented metadata in `frontend/package.json` and a small `frontend/README.md` so the package can be built and published with minimal cleanup.

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

Function wrapper files. Contain generated wrappers. Local native implementation
modules are scaffolded separately under `src/impl/functions/` and are
user-owned.

```typescript
// Generated wrapper
import { apply as applyImpl } from '../impl/functions/internal/watermark.js';

export async function watermark(
  file: File,
  text: string,
  position: string,
  opacity: number,
): Promise<File> {
  return applyImpl(file, text, position, opacity);
}
```

For `impl node { module: "./internal/watermark" func: "apply" }`, Blueprint
also creates `src/impl/functions/internal/watermark.ts` if it does not exist:

```typescript
// Blueprint implementation scaffold. This file is user-owned; bp build will not overwrite it.
export async function apply(
  file: File,
  text: string,
  position: string,
  opacity: number,
): Promise<any> {
  throw new Error('Not implemented: apply');
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
cp .env.example .env
# Edit .env with your actual values

bun install
```

### Frontend Contracts

The generated `src/types/` directory is safe to copy or publish into a frontend app. A common setup is:

```bash
bp build api.bp --out generated --react-query
cp -R generated/src/types ../web/src/lib/blueprint
```

That gives the frontend one source of truth for request types, response types, Zod schemas, and optional React Query hooks.

If you prefer a standalone package boundary instead of copying files directly, consume `generated/frontend/` as a workspace package.

### Frontend-Only Build

If you do not need the backend output at all, build just the frontend package:

```bash
bp build my-service.bp --frontend-only --out web-contract
# or:
bp frontend my-service.bp --out web-contract
```

That output directory becomes the frontend package root directly:

```text
web-contract/
├── package.json
├── README.md
├── tsconfig.json
├── .gitignore
└── src/
    ├── index.ts
    ├── api.ts
    ├── schemas.ts
    ├── client.ts
    └── react-query.ts   # optional via --react-query
```

To verify the package is ready to publish without actually publishing it:

```bash
bp frontend publish my-service.bp --out web-contract --react-query

# If dependencies are already installed in web-contract/
bp frontend publish my-service.bp --out web-contract --react-query --skip-install
```

### Database (if using `database postgres`)

```bash
# Push schema to database (development)
bunx drizzle-kit push

# Generate migration file (production)
bunx drizzle-kit generate
bunx drizzle-kit migrate
```

### Start the Server

```bash
bun run start
# my-service listening on port 3000
```

### Run Tests

```bash
bun run test
```

### Available Commands

| Script | Description |
|--------|-------------|
| `bun run start` | Start the server |
| `bun run dev` | Watch mode with tsx |
| `bun run test` | Run Vitest tests |
| `bunx drizzle-kit push` | Push Drizzle schema to database |
| `bunx drizzle-kit generate` | Generate SQL migration |
| `bunx drizzle-kit migrate` | Apply pending migrations |
| `bunx drizzle-kit studio` | Open Drizzle Studio |

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

You can edit generated files directly — they're standard TypeScript with no hidden magic. If you re-run `bp build`, files tracked in `.blueprint/manifest.json` are rewritten from the `.bp` source. To preserve manual edits, either:

1. Eject: stop using `bp build` and maintain the TypeScript directly
2. Use `impl node { module: "./internal/..." }` to point `fn` blocks to user-owned TypeScript modules under `src/impl/functions/internal/...`

Blueprint creates implementation scaffolds once and does not track them in `.blueprint/manifest.json`, so later builds leave your custom implementation code untouched.
