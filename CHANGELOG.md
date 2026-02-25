# Changelog

All notable changes to this project will be documented in this file.

## [0.3.3] - 2026-02-24

### Fixed

- **Response JSON keys preserve `.bp` snake_case**: Output blocks like `-> 200 { created_at: note.created_at }` now emit `{ created_at: note.createdAt }` instead of `{ createdAt: note.createdAt }`. The JSON key matches the `.bp` source declaration while the value accessor correctly uses Drizzle's camelCase property. This applies to all endpoints including list responses (Bugs 1 & 8).
- **PATCH returns updated data via `.returning()`**: `update` data operations now use `.returning()` so the response contains the actual updated row, not stale pre-update data (Bug 2).
- **PATCH only sets defined fields**: PATCH handlers wrap set values with `Object.fromEntries(Object.entries({...}).filter(([_, v]) => v !== undefined))` so that fields not included in the request body aren't overwritten to `NULL` (Bug 3).
- **Auto timestamp fields bumped on update**: Model fields with the `auto` modifier (e.g., `updated_at timestamp default(now) auto`) now inject `updatedAt: new Date()` into every `update` set call (Bug 4).
- **`inArray` guarded against empty arrays**: Junction table queries like `where(id in links.tag_id)` now emit `(links.length > 0 ? inArray(...) : sql\`1 = 0\`)` to prevent Drizzle from crashing on empty arrays (Bug 5).
- **Validation schema uses original field names**: Zod schemas now use `.bp`-declared names (`tag_id`, `per_page`) instead of camelCase (`tagId`, `perPage`). Input extraction also reads from the original name: `c.req.valid('json').tag_id` (Bug 6).
- **CORS middleware auto-generated**: `index.ts` now always imports and applies `cors()` from `hono/cors` unless already declared via `use cors` in the blueprint block. This ensures SPAs and mobile apps can connect out of the box (Bug 7).

## [0.3.2] - 2026-02-24

### Fixed

- **Search ILIKE targets columns, not table**: `where(q)` now generates `or(sql\`${schema.note.title} ILIKE ...\`, sql\`${schema.note.content} ILIKE ...\`)` across all string/text columns in the model, instead of the whole table reference.
- **`text` type support**: `text` is now a recognized primitive type throughout the toolchain (parser, checker, codegen). Maps to Drizzle `text()` column, TypeScript `string`, and Zod `z.string()`.

## [0.3.1] - 2026-02-24

### Fixed

- **`where()` with optional filter params**: `query note where(q, pinned)` now generates proper SQL predicates — text search params (q, search) produce `ILIKE` patterns, boolean/enum params produce `eq()` with null guards, and optional conditions are filtered with `.filter(Boolean)`.
- **`.items` on non-paginated queries**: `all_tags.items` on a plain `query tag` (no `paginate()`) now resolves to the array itself instead of accessing a nonexistent `.items` property. Only paginated query results retain `.items`/`.total` access.
- **`inArray` in where clauses**: `where(id in links.tag_id)` now generates `inArray(schema.tag.id, links.map((r: any) => r.tagId))` instead of the incorrect `links.tagId.includes(id)`.
- **Compound `where` in `fetch`**: `fetch note_tag where(note_id == id, tag_id == tag_id)` now generates `and(eq(schema.noteTag.noteId, id), eq(schema.noteTag.tagId, tagId))` instead of nesting `where()` inside `eq()`.
- **`.env` propagation in `bp migrate`**: `bp migrate push` now copies the project's `.env` file into `generated/` before running `drizzle-kit`, so `DATABASE_URL` is available.
- **`tags` usable as identifier**: `tags` keyword is now allowed in expression/binding contexts (e.g., `|> tags = query tag`), fixing a parser restriction that prevented using `tags` as a variable name.

## [0.3.0] - 2026-02-24

### Added

- **FK relation access**: `item.product.stock` through `ref()` foreign keys now resolves via sub-queries. The codegen pre-scans expressions for FK access patterns and emits Drizzle lookups before the referencing statement.
- **`sum()` builtin**: `sum(items.price * items.quantity)` compiles to `.reduce((acc, r) => acc + r.price * r.quantity, 0)`.
- **Unbound map result capture**: `map items: save order_item { ... }` auto-captures the result when subsequent statements reference `orderItems`.
- **WS factory pattern**: WebSocket route files export `createXxxRoutes(upgradeWebSocket)` factory functions. `index.ts` imports `@hono/node-ws` and passes the runtime `upgradeWebSocket` at startup.
- **WS variable hoisting**: Variables assigned in `on_connect` (e.g., `room`, `sender`) are hoisted to the outer closure scope so `on_message` and `on_disconnect` can access them.
- **WS builtins**: `join`, `leave`, `broadcast` emit TODO comments; `emit` compiles to `emit('event_name', data)` with events lib import.
- **STREAM event subscriptions**: Event handlers compile to `on('event_name', async (eventData) => { ... })` using the events lib. Conditions prefix undeclared identifiers with `eventData.`.
- **STREAM timeouts**: `on timeout(5min)` compiles to `setInterval` with `stream.onAbort` cleanup.
- **ContentfulStatusCode typing**: All status codes use `N as const` for Hono's `ContentfulStatusCode` type. `BpError.statusCode` typed as `ContentfulStatusCode`.
- **Hono context variable typing**: Routes with middleware inject get `new Hono<{ Variables: { key: any } }>()` for type-safe `c.get()`/`c.set()`.
- **Context variable model resolution**: `update current_user { ... }` resolves to the real model via `varModels` mapping.
- **Stub merging**: Multiple functions sharing the same `impl` module produce a single merged stub file.
- **Events lib for STREAM/WS**: Events lib generated when STREAM endpoints have event handlers or WS handlers use `emit`.
- **Import collector cleanup**: Builtins and model names excluded from TODO stub generation.
- **Blueprint required fields**: Checker validates `version` and `runtime` are present.
- **Duplicate model field detection**: Checker reports duplicate field names within a model.
- **Parser recursion depth limit**: `maxExprDepth = 256` prevents stack overflow on deeply nested expressions.
- **Parser error count limit**: `maxErrors = 50` prevents runaway error cascading.
- **Duration `1d` support**: Lexer accepts `d` as a duration unit.
- **`@>` generate error**: Top-level `@>` steps emit a proper error instead of silent discard.
- **Todo-api golden test**: Snapshot test with 13 golden files for the todo-api example.
- **WS factory test coverage**: 5 explicit checks for the WS factory pattern in `index.ts`.
- **`@hono/node-ws` dependency**: Added to `package.json` when WS endpoints are present.
- **`REDIS_URL` env var**: Auto-added to `env.ts` schema when cache is enabled.

### Changed

- **Map loop variable**: Always uses `item` instead of model-derived name for consistency with bp convention.
- **Update ID fallback**: When `update` targets a model inside `map`, falls back to `item.id` from bound vars.
- **Webhook auth**: `Buffer.from(_sig, 'hex')` wrapped in try-catch to handle invalid hex input.
- **WS handler signatures**: `onOpen`, `onMessage`, `onClose` have typed parameters (`Event`, `MessageEvent<WSMessageReceive>`, `CloseEvent`).
- **WS message parsing**: `onMessage` parses `event.data` as JSON automatically.
- **STREAM handler structure**: Event handlers wrapped in `on()` callbacks with proper `eventData` typing instead of bare conditional blocks.
- **STREAM path params**: Extracted at handler start and marked as declared in context.
- **Rate limit store**: Confirmed at module scope (not per-request) with periodic cleanup.
- **Graceful shutdown**: SIGTERM/SIGINT handlers close server and drain DB pool.
- **`boundVars` in expression resolution**: General `Ident` case checks `boundVars` for variable aliasing (e.g., `event` to `eventData`).

### Fixed

- **57 TypeScript errors reduced to 0**: All 5 examples (hello-world, todo-api, auth-service, ecommerce-api, realtime-chat) pass `tsc --noEmit` cleanly.
- **`as any` removed**: Status codes, `BpError.statusCode`, and `c.get()` no longer use `as any` casts.
- **WebSocket extra paren**: Fixed `}))))` to `})))` in WS route generation.
- **`upgradeWebSocket` import**: No longer imports non-existent runtime export from `hono/ws`; uses factory pattern with `@hono/node-ws` instead.
- **`sender` undefined in WS**: `inject X as Y` in WS context generates variable assignment instead of `c.set()`.
- **`room(id)` not callable**: WS builtins resolve `room(id)` pattern to just `id` instead of calling the variable as a function.
- **`newMessage` undefined**: `emit new_message` now passes event name as string `"new_message"`.
- **`roomId` undefined in STREAM**: Event conditions prefix undeclared identifiers with `eventData.`.
- **STREAM return type**: Removed bare `return` before `writeSSE` that caused dead code and type mismatch.
- **Missing events lib**: Events lib now generated for STREAM/WS usage, not just `subscribe` blocks.
- **`room` stub function**: Model names no longer generate TODO stubs in import collector.

## [0.2.0] - 2025-12-15

### Added

- Production readiness overhaul: rate limiting, graceful shutdown, health endpoint
- `bp eject` command to remove generated headers
- "Did you mean?" suggestions for unknown identifiers
- Deterministic output via sorted map iterations
- Fuzz tests, CLI integration tests, golden file tests
- CI: race detection, coverage, tsc validation for all 5 examples

## [0.1.0] - 2025-11-01

### Added

- Initial release: lexer, parser, checker, JS codegen
- Blueprint DSL with models, endpoints, middleware, functions, pipes
- Hono + Drizzle + Zod code generation
- 5 example applications
