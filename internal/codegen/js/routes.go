package js

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

func validateNodeQueuePolicies(file *ast.File) (*resolve.QueuePolicyFacts, error) {
	if file != nil {
		for _, block := range file.Blocks {
			worker, ok := block.(*ast.Worker)
			if !ok {
				continue
			}
			if err := validateWorkerDeliveryPolicy(worker); err != nil {
				return nil, fmt.Errorf("node target cannot generate worker delivery policy: %w", err)
			}
		}
	}
	facts, err := resolve.ResolveQueuePolicies(file)
	if err != nil {
		return nil, fmt.Errorf("node target cannot resolve enqueue delivery policy: %w", err)
	}
	for _, site := range facts.Enqueues {
		if site.Context == resolve.QueueEnqueueEndpoint {
			continue
		}
		return nil, fmt.Errorf(
			"node target supports enqueue only in HTTP endpoint bodies; enqueue queue %q in %s %q (%s) at %s would generate a producer without a BullMQ Queue binding",
			site.Queue, site.Context, site.Owner, site.Section, site.Loc,
		)
	}
	return facts, nil
}

// --- Index (entrypoint) ---

func (g *Generator) genIndex(bp *ast.Blueprint, endpoints []*ast.Endpoint, streams []*ast.StreamEndpoint, ws []*ast.WsEndpoint, middlewares []*ast.Middleware, subscribes []*ast.Subscribe, workers []*ast.Worker, schedules []*ast.Schedule, hasDB bool) codegen.OutputFile {
	port := blueprintEntryInt(bp, "port")
	if port == 0 {
		port = 3000
	}

	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { Hono } from 'hono';\nimport { serve } from '@hono/node-server';\n")

	// If there are WS endpoints, import createNodeWebSocket
	if len(ws) > 0 {
		b.WriteString("import { createNodeWebSocket } from '@hono/node-ws';\n")
	}

	b.WriteString("import { secureHeaders } from 'hono/secure-headers';\n")
	b.WriteString("import { env } from './lib/env.js';\n")
	if hasDB {
		b.WriteString("import { db } from './lib/db.js';\n")
	}

	// Collect global middleware imports
	builtinMiddleware := map[string]string{
		"cors":     "hono/cors",
		"compress": "hono/compress",
	}
	// Check if cors is explicitly declared in blueprint uses
	hasCors := false
	for _, u := range bp.Uses {
		if u.Name == "cors" {
			hasCors = true
		}
	}
	// Always import cors for SPA/mobile compatibility
	if !hasCors {
		b.WriteString("import { cors } from 'hono/cors';\n")
	}
	for _, u := range bp.Uses {
		if pkg, ok := builtinMiddleware[u.Name]; ok {
			fmt.Fprintf(&b, "import { %s } from '%s';\n", u.Name, pkg)
		} else {
			fmt.Fprintf(&b, "import { %s } from './middleware/%s.js';\n",
				toCamelCase(u.Name), toKebabCase(u.Name))
		}
	}

	// Build resource sets for conflict detection (same logic as generateAll)
	routeResources := make(map[string]bool)
	for _, ep := range endpoints {
		routeResources[extractResource(ep.Path)] = true
	}
	streamResources := make(map[string]bool)
	for _, se := range streams {
		streamResources[extractResource(se.Path)] = true
	}
	wsResources := make(map[string]bool)
	for _, we := range ws {
		wsResources[extractResource(we.Path)] = true
	}

	// fileKeyForStream/Ws mirrors the suffix logic in generateAll
	fileKeyForStream := func(res string) string {
		if routeResources[res] {
			return res + "-stream"
		}
		return res
	}
	fileKeyForWs := func(res string) string {
		if routeResources[res] || streamResources[res] {
			return res + "-ws"
		}
		return res
	}

	// Import regular route files (sorted for deterministic output)
	for _, res := range sortedKeys2(routeResources) {
		fmt.Fprintf(&b, "import { %sRoutes } from './routes/%s.js';\n", toCamelCase(res), toKebabCase(res))
	}

	// Import stream route files
	for _, res := range sortedKeys2(streamResources) {
		fk := fileKeyForStream(res)
		fmt.Fprintf(&b, "import { %sRoutes } from './routes/%s.js';\n", toCamelCase(fk), toKebabCase(fk))
	}

	// Import WS route factory functions
	for _, res := range sortedKeys2(wsResources) {
		fk := fileKeyForWs(res)
		fmt.Fprintf(&b, "import { create%sRoutes } from './routes/%s.js';\n", toPascalCase(fk), toKebabCase(fk))
	}

	// Import subscription handlers and event emitter
	if len(subscribes) > 0 {
		b.WriteString("import { on } from './lib/events.js';\n")
		for _, sub := range subscribes {
			handlerName := "on" + toPascalCase(strings.ReplaceAll(sub.Event, ".", "_"))
			fileName := toKebabCase(strings.ReplaceAll(sub.Event, ".", "-"))
			fmt.Fprintf(&b, "import { %s } from './subscriptions/%s.js';\n", handlerName, fileName)
		}
	}

	// Import worker + schedule handlers. Both need `Worker` from bullmq:
	// workers consume their own queue, and schedules consume the shared
	// 'scheduler' queue (see below) — without a consumer there, the cron
	// entries added via schedulerQueue.add() would be enqueued but never run.
	if len(workers) > 0 || len(schedules) > 0 {
		b.WriteString("import { Worker } from 'bullmq';\n")
	}
	for _, w := range workers {
		name := toCamelCase(w.Name)
		onFailName := name + "OnFail"
		queueName := name + "QueueName"
		timeoutName := name + "TimeoutMs"
		imports := []string{name, onFailName, queueName, timeoutName}
		if workerCustomBackoffStrategyExpr(w) != "" {
			imports = append(imports, name+"BackoffStrategy")
		}
		fmt.Fprintf(&b, "import { %s } from './workers/%s.js';\n",
			strings.Join(imports, ", "), toKebabCase(w.Name))
	}
	if len(schedules) > 0 {
		b.WriteString("import { Queue } from 'bullmq';\n")
		for _, s := range schedules {
			name := toCamelCase(s.Name)
			cronName := name + "Cron"
			fmt.Fprintf(&b, "import { %s, %s } from './schedules/%s.js';\n",
				name, cronName, toKebabCase(s.Name))
		}
	}

	b.WriteString("\n")

	b.WriteString("const app = new Hono();\n\n")

	// Security headers (X-Content-Type-Options, X-Frame-Options, etc.)
	b.WriteString("app.use('*', secureHeaders());\n")

	// CORS middleware — always enabled for SPA/mobile compatibility
	if !hasCors {
		b.WriteString("app.use('*', cors());\n")
	}
	b.WriteString("\n")

	// If there are WS endpoints, set up upgradeWebSocket + injectWebSocket
	if len(ws) > 0 {
		b.WriteString("const { injectWebSocket, upgradeWebSocket } = createNodeWebSocket({ app });\n\n")
	}

	// Wire global middleware
	for _, u := range bp.Uses {
		if u.Body != nil && len(u.Body.Entries) > 0 {
			// Middleware with config (e.g., cors { origins: [...] })
			configParts := make([]string, len(u.Body.Entries))
			for i, kv := range u.Body.Entries {
				key := toCamelCase(kv.Key)
				// Map Blueprint config keys to framework-specific keys
				if u.Name == "cors" && key == "origins" {
					key = "origin"
				}
				configParts[i] = fmt.Sprintf("%s: %s", key, exprToJS(kv.Value))
			}
			fmt.Fprintf(&b, "app.use('*', %s({ %s }));\n",
				toCamelCase(u.Name), strings.Join(configParts, ", "))
		} else {
			// Simple middleware
			if _, ok := builtinMiddleware[u.Name]; ok {
				fmt.Fprintf(&b, "app.use('*', %s());\n", toCamelCase(u.Name))
			} else {
				fmt.Fprintf(&b, "app.use('*', %s);\n", toCamelCase(u.Name))
			}
		}
	}
	if len(bp.Uses) > 0 {
		b.WriteString("\n")
	}

	// Mount regular routes (sorted for deterministic output)
	for _, res := range sortedKeys2(routeResources) {
		fmt.Fprintf(&b, "app.route('/', %sRoutes);\n", toCamelCase(res))
	}

	// Mount stream routes
	for _, res := range sortedKeys2(streamResources) {
		fk := fileKeyForStream(res)
		fmt.Fprintf(&b, "app.route('/', %sRoutes);\n", toCamelCase(fk))
	}

	// Mount WS routes (created via factory with runtime upgradeWebSocket)
	for _, res := range sortedKeys2(wsResources) {
		fk := fileKeyForWs(res)
		fmt.Fprintf(&b, "app.route('/', create%sRoutes(upgradeWebSocket));\n", toPascalCase(fk))
	}

	b.WriteString("\n")

	// Register event subscriptions
	if len(subscribes) > 0 {
		b.WriteString("// Register event subscriptions\n")
		for _, sub := range subscribes {
			handlerName := "on" + toPascalCase(strings.ReplaceAll(sub.Event, ".", "_"))
			fmt.Fprintf(&b, "on('%s', %s);\n", sub.Event, handlerName)
		}
		b.WriteString("\n")
	}

	// Health check endpoint
	b.WriteString("app.get('/health', (c) => c.json({ status: 'ok', uptime: process.uptime() }));\n\n")

	// Global error handler — suppress internal details
	b.WriteString("app.onError((err, c) => {\n")
	b.WriteString("  console.error(err);\n")
	b.WriteString("  return c.json({ error: 'Internal server error' }, 500 as const);\n")
	b.WriteString("});\n\n")

	// Start the server, workers/schedules, and register shutdown handlers —
	// skipped under test runners (Vitest) so importing the app for
	// `app.request(...)` never binds a port or opens real Redis connections
	// for background workers/schedules.
	b.WriteString("if (!process.env.VITEST) {\n")

	// Start workers
	if len(workers) > 0 {
		b.WriteString("  // Start workers\n")
		for _, w := range workers {
			name := toCamelCase(w.Name)
			queueName := name + "QueueName"
			timeoutName := name + "TimeoutMs"
			customBackoffStrategy := workerCustomBackoffStrategyExpr(w)
			backoffStrategyName := name + "BackoffStrategy"
			fmt.Fprintf(&b, "  new Worker(%s, async (job) => {\n", queueName)
			b.WriteString("    try {\n")
			fmt.Fprintf(&b, "      if (%s > 0) {\n", timeoutName)
			fmt.Fprintf(&b, "        await Promise.race([%s(job.data), new Promise((_, reject) => setTimeout(() => reject(new Error('Worker timeout')), %s))]);\n", name, timeoutName)
			b.WriteString("      } else {\n")
			fmt.Fprintf(&b, "        await %s(job.data);\n", name)
			b.WriteString("      }\n")
			b.WriteString("    } catch (error) {\n")
			b.WriteString("      // Compensate ordinary processor exceptions only on the configured final attempt.\n")
			b.WriteString("      // Stalled-job exhaustion happens outside this catch; UnrecoverableError can terminate before this configured final attempt.\n")
			b.WriteString("      if (job.attemptsMade + 1 >= (job.opts.attempts ?? 1)) {\n")
			fmt.Fprintf(&b, "        await %sOnFail(job.data, error instanceof Error ? error : new Error(String(error)));\n", name)
			b.WriteString("      }\n")
			b.WriteString("      throw error;\n")
			b.WriteString("    }\n")
			b.WriteString("  }, {\n")
			if customBackoffStrategy != "" {
				b.WriteString("    connection: { url: env.REDIS_URL },\n")
				fmt.Fprintf(&b, "    settings: { backoffStrategy: %s }\n", backoffStrategyName)
			} else {
				b.WriteString("    connection: { url: env.REDIS_URL }\n")
			}
			b.WriteString("  });\n")
		}
		b.WriteString("\n")
	}

	// Register scheduled jobs and the consumer that actually runs them —
	// without this Worker, jobs added to the 'scheduler' queue below would
	// sit there forever (cron handlers would be dead code at runtime).
	if len(schedules) > 0 {
		b.WriteString("  // Register scheduled jobs\n")
		b.WriteString("  const schedulerQueue = new Queue('__bp_scheduler', { connection: { url: env.REDIS_URL } });\n")
		for _, s := range schedules {
			name := toCamelCase(s.Name)
			cronName := name + "Cron"
			fmt.Fprintf(&b, "  schedulerQueue.add('%s', {}, { repeat: { pattern: %s }, attempts: 3, backoff: { type: 'exponential', delay: 1000 } });\n", s.Name, cronName)
		}
		b.WriteString("  new Worker('__bp_scheduler', async (job) => {\n")
		b.WriteString("    switch (job.name) {\n")
		for _, s := range schedules {
			name := toCamelCase(s.Name)
			fmt.Fprintf(&b, "      case '%s':\n", s.Name)
			fmt.Fprintf(&b, "        return %s();\n", name)
		}
		b.WriteString("      default:\n")
		b.WriteString("        throw new Error(`Unknown scheduled job: ${job.name}`);\n")
		b.WriteString("    }\n")
		b.WriteString("  }, {\n")
		b.WriteString("    connection: { url: env.REDIS_URL }\n")
		b.WriteString("  });\n")
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "  console.log('%%s listening on port %%d', %q, %d);\n", bp.Name, port)

	// If WS: use injectWebSocket(serve(...)), otherwise capture server ref for graceful shutdown
	if len(ws) > 0 {
		fmt.Fprintf(&b, "  const server = serve({ fetch: app.fetch, port: %d });\n", port)
		b.WriteString("  injectWebSocket(server);\n")
	} else {
		fmt.Fprintf(&b, "  const server = serve({ fetch: app.fetch, port: %d });\n", port)
	}

	// Graceful shutdown
	b.WriteString("  const shutdown = async () => {\n")
	b.WriteString("    console.log('Shutting down gracefully...');\n")
	b.WriteString("    await new Promise<void>((resolve) => server.close(() => resolve()));\n")
	if hasDB {
		b.WriteString("    // Close database pool\n")
		b.WriteString("    await db.$client.end();\n")
	}
	b.WriteString("    process.exit(0);\n")
	b.WriteString("  };\n")
	b.WriteString("  process.on('SIGTERM', shutdown);\n")
	b.WriteString("  process.on('SIGINT', shutdown);\n")
	b.WriteString("}\n\n")

	// A named export, not a default one: Bun serves an entrypoint's default export
	// when it has a fetch method, which would open a second listener on :3000
	// and crash `bun dist/index.js` with EADDRINUSE when the declared port is 3000.
	b.WriteString("export { app };\n")

	return codegen.OutputFile{Path: "src/index.ts", Content: []byte(b.String())}
}

// --- Routes ---

func (g *Generator) genRoute(resource string, endpoints []*ast.Endpoint, hasDB bool) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { Hono } from 'hono';\n")

	// Determine what imports are needed
	schemaNames := make(map[string]bool)
	middlewareNames := make(map[string]bool)
	needsZValidator := false
	needsWebhookAuth := false
	needsRequireAuth := false
	needsZodPathParam := false
	pathEnumSchemas := make(map[string]bool)
	needsRateLimit := false
	needsHTTPCache := false

	// Check if any endpoint in this route file uses data operations (local check).
	// This supplements the global hasDB flag so that routes with data ops always
	// get db/schema imports, even if the check is done per-file rather than globally.
	needsDB := hasDB
	if !needsDB {
		for _, ep := range endpoints {
			if stmtsHaveDataOps(ep.Stmts) {
				needsDB = true
				break
			}
		}
	}

	for _, ep := range endpoints {
		// Check for non-path-param inputs
		for _, s := range ep.Stmts {
			if inp, ok := s.(*ast.InputStmt); ok {
				if !isPathParam(inp.Name, ep.Path) {
					needsZValidator = true
					schemaNames[endpointSchemaName(ep.Method, ep.Path)] = true
				} else {
					// Every declared path input is validated (restPathParamSchema),
					// so every route file needs zod; a named enum also needs its
					// schema from types/schemas.ts.
					needsZodPathParam = true
					if name, ok := g.pathEnumName(inp.Type); ok {
						pathEnumSchemas[frontendSchemaName(name)] = true
					}
				}
			}
		}
		if endpointCacheMiddleware(ep, g.file.Blueprint) != "" {
			needsHTTPCache = true
		}
		// Check for middleware, auth, and rate limit
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				middlewareNames[meta.Use.Name] = true
			}
			if meta.Kind == "auth" && webhookAuthSecretKey(meta.Value) != "" {
				needsWebhookAuth = true
			}
			if _, ok := endpointEnforcedAuth(meta.Value); ok && meta.Kind == "auth" {
				needsRequireAuth = true
			}
			if meta.Kind == "limit" {
				needsRateLimit = true
			}
		}
	}

	// Detect enqueue calls to know which BullMQ Queue instances to create.
	enqueueQueues := map[string]bool{}
	for _, ep := range endpoints {
		walkStmts(ep.Stmts, func(expr ast.Expr) {
			if call, ok := expr.(*ast.FnCall); ok && call.Name == "enqueue" && len(call.Args) == 2 {
				if name := exprToString(call.Args[0]); name != "" {
					enqueueQueues[name] = true
				}
			}
		})
	}

	// Hono Variables typing for middleware-injected context (see honoVariablesType).
	var allMetas []*ast.EndpointMeta
	for _, ep := range endpoints {
		allMetas = append(allMetas, ep.Meta...)
	}
	envType, needsSchema := g.honoVariablesType(allMetas)

	if needsZValidator {
		b.WriteString("import { zValidator } from '@hono/zod-validator';\n")
	}
	if needsZodPathParam {
		b.WriteString("import { z } from 'zod';\n")
	}
	if len(pathEnumSchemas) > 0 {
		fmt.Fprintf(&b, "import { %s } from '../types/schemas.js';\n",
			strings.Join(sortedKeys2(pathEnumSchemas), ", "))
	}
	if needsDB {
		b.WriteString("import { db } from '../lib/db.js';\n")
		b.WriteString("import * as schema from '../models/schema.js';\n")
		b.WriteString("import { eq, and, or, lt, gt, lte, gte, ne, sql, desc, asc, inArray } from 'drizzle-orm';\n")
	} else if needsSchema {
		// Typed Variables reference schema.<model> even when no handler queries the DB.
		b.WriteString("import * as schema from '../models/schema.js';\n")
	}
	// Import validation schemas
	if len(schemaNames) > 0 {
		names := sortedKeys2(schemaNames)
		fmt.Fprintf(&b, "import { %s } from '../validation/schemas.js';\n",
			strings.Join(names, ", "))
	}
	// Import middleware
	for _, mwName := range sortedKeys2(middlewareNames) {
		fmt.Fprintf(&b, "import { %s } from '../middleware/%s.js';\n",
			toCamelCase(mwName), toKebabCase(mwName))
	}
	if needsHTTPCache {
		b.WriteString("import { httpCache } from '../lib/http-cache.js';\n")
	}
	b.WriteString("import { BpError } from '../lib/errors.js';\n")
	if len(enqueueQueues) > 0 {
		b.WriteString("import { Queue } from 'bullmq';\n")
		for _, queueName := range sortedKeys2(enqueueQueues) {
			policy := g.queuePolicies[queueName]
			fmt.Fprintf(
				&b,
				"import { %sJobOptions as %s } from '../workers/%s.js';\n",
				toCamelCase(policy.WorkerName), workerJobOptionsIdentifier(policy.WorkerName), toKebabCase(policy.WorkerName),
			)
		}
	}
	if needsWebhookAuth {
		b.WriteString("import { createHmac, timingSafeEqual } from 'node:crypto';\n")
	}

	if needsRequireAuth {
		b.WriteString("import { requireAuth as _requireAuth } from '../lib/auth.js';\n")
	}

	// Collect additional imports from endpoint bodies (pipes, functions, storage, env, enums)
	ic := newImportCollector()
	for _, ep := range endpoints {
		ic.merge(g.collectImports(ep.Stmts))
	}
	if needsWebhookAuth || needsRequireAuth {
		ic.needsEnv = true
	}
	if len(enqueueQueues) > 0 {
		ic.needsEnv = true
	}
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(resource) + "Routes"
	if envType != "" {
		fmt.Fprintf(&b, "export const %s = new Hono<%s>();\n\n", routeVar, envType)
	} else {
		fmt.Fprintf(&b, "export const %s = new Hono();\n\n", routeVar)
	}

	// Module-level rate limit store (shared across all handlers in this file)
	if needsRateLimit {
		b.WriteString("// Module-level rate limit store with periodic cleanup\n")
		b.WriteString("const _rateLimitStore = new Map<string, { count: number, resetAt: number }>();\n")
		b.WriteString("setInterval(() => { const now = Date.now(); for (const [k, v] of _rateLimitStore) { if (v.resetAt <= now) _rateLimitStore.delete(k); } }, 60000);\n\n")
	}
	// Module-level BullMQ Queue instances for enqueue calls.
	if len(enqueueQueues) > 0 {
		for _, qName := range sortedKeys2(enqueueQueues) {
			policy := g.queuePolicies[qName]
			fmt.Fprintf(&b, "const %s = new Queue(%q, { connection: { url: env.REDIS_URL } });\n", workerProducerQueueIdentifier(policy.WorkerName), qName)
		}
		b.WriteString("\n")
	}

	for _, ep := range endpoints {
		method := strings.ToLower(ep.Method)
		fmt.Fprintf(&b, "// %s %s\n", ep.Method, ep.Path)
		if ep.Intent != nil {
			fmt.Fprintf(&b, "// %s\n", ep.Intent.Text)
		}

		// Build handler: routeVar.method('path', ...middleware, zValidator, async (c) => { ... })
		fmt.Fprintf(&b, "%s.%s('%s'", routeVar, method, ep.Path)

		// Cache-Control for GET endpoints with `cache` runs first so it wraps
		// every other middleware and handler.
		if cacheMW := endpointCacheMiddleware(ep, g.file.Blueprint); cacheMW != "" {
			fmt.Fprintf(&b, ", %s", cacheMW)
		}

		// Enforced auth runs before use middleware and zValidator, so a bad
		// body or a user middleware never runs for an unauthenticated request.
		if ea, ok := endpointEnforcedAuthMeta(ep.Meta); ok {
			fmt.Fprintf(&b, ", %s", requireAuthMiddleware(ea, false, g.blueprintNameOf()))
		}

		// Add middleware
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				fmt.Fprintf(&b, ", %s", toCamelCase(meta.Use.Name))
			}
		}

		// Add zValidator for non-path-param inputs
		hasNonPathInputs := false
		for _, s := range ep.Stmts {
			if inp, ok := s.(*ast.InputStmt); ok {
				if !isPathParam(inp.Name, ep.Path) {
					hasNonPathInputs = true
					break
				}
			}
		}
		if hasNonPathInputs {
			schemaName := endpointSchemaName(ep.Method, ep.Path)
			source := "'json'"
			if ep.Method == "GET" || ep.Method == "DELETE" {
				source = "'query'"
			}
			fmt.Fprintf(&b, ", zValidator(%s, %s)", source, schemaName)
		}

		b.WriteString(", async (c) => {\n")

		// Emit meta as comments for rate limit, cache; webhook auth as verification code
		for _, meta := range ep.Meta {
			switch meta.Kind {
			case "limit":
				rateStr := exprToJS(meta.Value)
				rateLimit, rateWindowMS := parseRateLimit(rateStr)
				fmt.Fprintf(&b, "  // rate limit: %s\n", rateStr)
				// rate limit store is at module scope (see needsRateLimit above)
				b.WriteString("  const _clientIp = c.req.header('x-forwarded-for') || 'unknown';\n")
				b.WriteString("  const _now = Date.now();\n")
				b.WriteString("  const _rateKey = `${_clientIp}:${c.req.path}`;\n")
				b.WriteString("  const _rateEntry = _rateLimitStore.get(_rateKey);\n")
				b.WriteString("  if (_rateEntry && _rateEntry.resetAt > _now) {\n")
				fmt.Fprintf(&b, "    if (_rateEntry.count >= %d) {\n", rateLimit)
				b.WriteString("      return c.json({ error: 'Rate limit exceeded' }, 429 as const);\n")
				b.WriteString("    }\n")
				b.WriteString("    _rateEntry.count++;\n")
				b.WriteString("  } else {\n")
				fmt.Fprintf(&b, "    _rateLimitStore.set(_rateKey, { count: 1, resetAt: _now + %d });\n", rateWindowMS)
				b.WriteString("  }\n")
			case "cache":
				if ep.Method != "GET" {
					fmt.Fprintf(&b, "  // cache %s ignored: Cache-Control is only set on GET endpoints\n", exprToString(meta.Value))
				} else {
					fmt.Fprintf(&b, "  // cache: %s (Cache-Control set by httpCache middleware)\n", exprToString(meta.Value))
				}
			case "auth":
				if secretKey := webhookAuthSecretKey(meta.Value); secretKey != "" {
					b.WriteString("  const _payload = await c.req.text();\n")
					b.WriteString("  const _sig = c.req.header('X-Webhook-Signature') ?? '';\n")
					fmt.Fprintf(&b, "  const _expected = createHmac('sha256', env.%s).update(_payload).digest('hex');\n", secretKey)
					b.WriteString("  let _sigBuf: Buffer; try { _sigBuf = Buffer.from(_sig, 'hex'); } catch { return c.json({ error: 'Invalid signature' }, 401 as const); }\n")
					b.WriteString("  if (_sigBuf.length !== Buffer.from(_expected, 'hex').length || !timingSafeEqual(_sigBuf, Buffer.from(_expected, 'hex'))) return c.json({ error: 'Invalid signature' }, 401 as const);\n")
					b.WriteString("  let data: any;\n")
					b.WriteString("  try { data = JSON.parse(_payload); } catch { return c.json({ error: 'Invalid payload' }, 400 as const); }\n")
				}
			}
		}

		b.WriteString("  try {\n")

		// Build context vars from middleware inject statements
		ctxVars := make(map[string]bool)
		boundVars := make(map[string]string)
		varModels := make(map[string]string)
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				if mw, ok := g.middlewares[meta.Use.Name]; ok {
					for k, v := range extractInjectedVars(mw.Before) {
						ctxVars[k] = v
					}
					for k, v := range extractInjectedVars(mw.After) {
						ctxVars[k] = v
					}
					// Map model names to their context variable names
					// e.g., user -> c.get('currentUser') (from "inject user as current_user")
					for modelName, ctxName := range extractInjectedModelMap(mw.Before) {
						ctxRef := "c.get('" + toCamelCase(ctxName) + "')"
						boundVars[modelName] = ctxRef
						// Also map the context variable name back to the real model
						// so "update current_user { ... }" resolves to schema.user
						boundVars[ctxName] = ctxRef
						camelCtx := toCamelCase(ctxName)
						boundVars[camelCtx] = ctxRef
						varModels[ctxName] = modelName
						varModels[camelCtx] = modelName
					}
					for modelName, ctxName := range extractInjectedModelMap(mw.After) {
						ctxRef := "c.get('" + toCamelCase(ctxName) + "')"
						boundVars[modelName] = ctxRef
						boundVars[ctxName] = ctxRef
						camelCtx := toCamelCase(ctxName)
						boundVars[camelCtx] = ctxRef
						varModels[ctxName] = modelName
						varModels[camelCtx] = modelName
					}
				}
			}
		}

		// Emit arrow statements with endpoint context
		ctx := emitCtx{
			kind:        "endpoint",
			method:      ep.Method,
			path:        ep.Path,
			ctxVars:     ctxVars,
			boundVars:   boundVars,
			varModels:   varModels,
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}
		g.emitArrowStmts(&b, ep.Stmts, "    ", ctx)

		b.WriteString("  } catch (err) {\n")
		b.WriteString("    if (err instanceof BpError) return c.json({ error: err.message }, err.statusCode);\n")
		b.WriteString("    throw err;\n")
		b.WriteString("  }\n")
		b.WriteString("});\n\n")
	}

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/routes/%s.ts", toKebabCase(resource)),
		Content: []byte(b.String()),
	}
}

// --- Realtime path-param validation (STREAM and WS) ---

// realtimePathInputs returns the <- inputs of a STREAM or WS endpoint. The
// checker guarantees each one names a :param segment of the endpoint path.
func realtimePathInputs(stmts []ast.ArrowStmt) []*ast.InputStmt {
	var inputs []*ast.InputStmt
	for _, s := range stmts {
		if inp, ok := s.(*ast.InputStmt); ok {
			inputs = append(inputs, inp)
		}
	}
	return inputs
}

// realtimePathParamCheck returns the Zod object that validates a STREAM or WS
// endpoint's declared path inputs, and the object literal of the raw route
// params it is applied to. Field schemas use the same mapping REST inputs use
// (typeToZodCoerce + constraintsToZod), since path params arrive as strings.
func (g *Generator) realtimePathParamCheck(inputs []*ast.InputStmt) (schema, raw string) {
	fields := make([]string, 0, len(inputs))
	values := make([]string, 0, len(inputs))
	for _, inp := range inputs {
		fields = append(fields, inp.Name+": "+g.typeToZodCoerce(inp.Type)+constraintsToZod(inp.Constraints))
		values = append(values, fmt.Sprintf("%s: c.req.param('%s')", inp.Name, inp.Name))
	}
	return "z.object({ " + strings.Join(fields, ", ") + " })",
		"{ " + strings.Join(values, ", ") + " }"
}

// restPathParamSchema returns the Zod schema that validates a declared REST
// path input. Path segments always arrive as strings, so the derivation is the
// realtime one (typeToZodCoerce + constraintsToZod). optional and default are
// dropped here: a matched segment is never undefined, so they cannot change the
// outcome. A named enum resolves to its generated schema (types/schemas.ts),
// which needs the import genRoute adds for it.
func (g *Generator) restPathParamSchema(inp *ast.InputStmt) string {
	var base string
	if name, ok := g.pathEnumName(inp.Type); ok {
		base = frontendSchemaName(name)
	} else {
		base = g.typeToZodCoerce(inp.Type)
	}
	return base + constraintsToZod(pathParamConstraints(inp.Constraints))
}

// pathEnumName reports the declared enum a path input's type names, if any.
// Aliases are excluded: they are lowered to their primitive by typeToZodCoerce.
func (g *Generator) pathEnumName(t ast.TypeExpr) (string, bool) {
	nt, ok := t.(*ast.NamedType)
	if !ok || g.lookupAlias(nt.Name) != nil || len(g.enumVariants[nt.Name]) == 0 {
		return "", false
	}
	return nt.Name, true
}

// pathParamConstraints drops the constraints that never apply to a path
// segment (see restPathParamSchema).
func pathParamConstraints(cs []*ast.Constraint_) []*ast.Constraint_ {
	var out []*ast.Constraint_
	for _, c := range cs {
		if c.Kind == "optional" || c.Kind == "default" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// --- Stream Endpoints ---

// genStreamRoute generates a route file for a group of stream (SSE) endpoints sharing the same resource.
// fileKey is the filename stem (may have "-stream" suffix when conflicting with a REST route).
func (g *Generator) genStreamRoute(resource, fileKey string, endpoints []*ast.StreamEndpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { Hono } from 'hono';\n")
	b.WriteString("import { streamSSE } from 'hono/streaming';\n")
	b.WriteString("import { createSseWriter } from '../lib/backpressure.js';\n")
	for _, ep := range endpoints {
		if len(realtimePathInputs(ep.Stmts)) > 0 {
			b.WriteString("import { z } from 'zod';\n")
			break
		}
	}

	// Collect imports from all endpoint bodies
	ic := newImportCollector()
	for _, ep := range endpoints {
		ic.merge(g.collectImports(ep.Stmts))
		for _, h := range ep.Handlers {
			ic.merge(g.collectImports(h.Body))
		}
	}

	// Check if any endpoint uses data operations
	needsDB := false
	for _, ep := range endpoints {
		if stmtsHaveDataOps(ep.Stmts) {
			needsDB = true
			break
		}
		for _, h := range ep.Handlers {
			if stmtsHaveDataOps(h.Body) {
				needsDB = true
				break
			}
		}
	}
	// Typed Variables for middleware-injected context (see honoVariablesType).
	var allMetas []*ast.EndpointMeta
	for _, ep := range endpoints {
		allMetas = append(allMetas, ep.Meta...)
	}
	envType, needsSchema := g.honoVariablesType(allMetas)
	if needsDB {
		writeDataImports(&b)
	} else if needsSchema {
		b.WriteString("import * as schema from '../models/schema.js';\n")
	}

	// Collect middleware, webhook-auth, and rate-limit meta the same way genRoute
	// does — STREAM endpoints previously parsed but silently dropped this meta.
	middlewareNames := make(map[string]bool)
	needsWebhookAuth := false
	needsRequireAuth := false
	needsRateLimit := false
	for _, ep := range endpoints {
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				middlewareNames[meta.Use.Name] = true
			}
			if meta.Kind == "auth" && webhookAuthSecretKey(meta.Value) != "" {
				needsWebhookAuth = true
			}
			if _, ok := endpointEnforcedAuth(meta.Value); ok && meta.Kind == "auth" {
				needsRequireAuth = true
			}
			if meta.Kind == "limit" {
				needsRateLimit = true
			}
		}
	}
	for _, mwName := range sortedKeys2(middlewareNames) {
		fmt.Fprintf(&b, "import { %s } from '../middleware/%s.js';\n",
			toCamelCase(mwName), toKebabCase(mwName))
	}

	b.WriteString("import { BpError } from '../lib/errors.js';\n")
	if needsWebhookAuth {
		b.WriteString("import { createHmac, timingSafeEqual } from 'node:crypto';\n")
	}

	if needsRequireAuth {
		b.WriteString("import { requireAuth as _requireAuth } from '../lib/auth.js';\n")
	}

	// Check if any handler uses event subscriptions (needs events lib)
	hasEventHandlers := false
	for _, ep := range endpoints {
		for _, h := range ep.Handlers {
			if h.EventName != "" && h.Timeout == "" {
				hasEventHandlers = true
				break
			}
		}
	}
	if hasEventHandlers {
		// offStream() unsubscribes a handler on stream.onAbort — per-connection
		// subscriptions that are never removed accumulate for the lifetime
		// of the process (unbounded memory leak, silent because hono
		// swallows writes to an already-closed stream).
		b.WriteString("import { onStream, offStream } from '../lib/events.js';\n")
	}
	if needsWebhookAuth || needsRequireAuth {
		ic.needsEnv = true
	}
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(fileKey) + "Routes"
	if envType != "" {
		fmt.Fprintf(&b, "export const %s = new Hono<%s>();\n\n", routeVar, envType)
	} else {
		fmt.Fprintf(&b, "export const %s = new Hono();\n\n", routeVar)
	}

	// Module-level rate limit store (shared across all handlers in this file)
	if needsRateLimit {
		b.WriteString("// Module-level rate limit store with periodic cleanup\n")
		b.WriteString("const _rateLimitStore = new Map<string, { count: number, resetAt: number }>();\n")
		b.WriteString("setInterval(() => { const now = Date.now(); for (const [k, v] of _rateLimitStore) { if (v.resetAt <= now) _rateLimitStore.delete(k); } }, 60000);\n\n")
	}

	for _, ep := range endpoints {
		fmt.Fprintf(&b, "// STREAM %s\n", ep.Path)
		if ep.Intent != nil {
			fmt.Fprintf(&b, "// %s\n", ep.Intent.Text)
		}

		// Build handler: routeVar.get('path', ...middleware, async (c) => { ... })
		fmt.Fprintf(&b, "%s.get('%s'", routeVar, ep.Path)
		// Enforced auth runs first, before use middleware and input validation,
		// and accepts the query-string credential (browsers cannot set headers).
		if ea, ok := endpointEnforcedAuthMeta(ep.Meta); ok {
			fmt.Fprintf(&b, ", %s", requireAuthMiddleware(ea, true, g.blueprintNameOf()))
		}
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				fmt.Fprintf(&b, ", %s", toCamelCase(meta.Use.Name))
			}
		}
		b.WriteString(", async (c) => {\n")

		// Validate declared path inputs first (400 before the stream starts,
		// with the same body zValidator returns for REST inputs), then extract
		// path params. Undeclared params stay raw strings.
		pathParams := extractPathParams(ep.Path)
		pathInputs := realtimePathInputs(ep.Stmts)
		declaredParams := map[string]bool{}
		if len(pathInputs) > 0 {
			schema, raw := g.realtimePathParamCheck(pathInputs)
			fmt.Fprintf(&b, "  const _pathParams = %s.safeParse(%s);\n", schema, raw)
			b.WriteString("  if (!_pathParams.success) return c.json(_pathParams, 400 as const);\n")
			for _, inp := range pathInputs {
				declaredParams[inp.Name] = true
			}
		}
		for _, param := range pathParams {
			if declaredParams[param] {
				fmt.Fprintf(&b, "  const %s = _pathParams.data.%s;\n", toCamelCase(param), param)
			} else {
				fmt.Fprintf(&b, "  const %s = c.req.param('%s');\n", toCamelCase(param), param)
			}
		}

		// Emit rate-limit and webhook-auth checks, mirroring genRoute — these
		// return early with the declared status before any setup logic runs.
		for _, meta := range ep.Meta {
			switch meta.Kind {
			case "limit":
				rateStr := exprToJS(meta.Value)
				rateLimit, rateWindowMS := parseRateLimit(rateStr)
				fmt.Fprintf(&b, "  // rate limit: %s\n", rateStr)
				b.WriteString("  const _clientIp = c.req.header('x-forwarded-for') || 'unknown';\n")
				b.WriteString("  const _now = Date.now();\n")
				b.WriteString("  const _rateKey = `${_clientIp}:${c.req.path}`;\n")
				b.WriteString("  const _rateEntry = _rateLimitStore.get(_rateKey);\n")
				b.WriteString("  if (_rateEntry && _rateEntry.resetAt > _now) {\n")
				fmt.Fprintf(&b, "    if (_rateEntry.count >= %d) {\n", rateLimit)
				b.WriteString("      return c.json({ error: 'Rate limit exceeded' }, 429 as const);\n")
				b.WriteString("    }\n")
				b.WriteString("    _rateEntry.count++;\n")
				b.WriteString("  } else {\n")
				fmt.Fprintf(&b, "    _rateLimitStore.set(_rateKey, { count: 1, resetAt: _now + %d });\n", rateWindowMS)
				b.WriteString("  }\n")
			case "auth":
				if secretKey := webhookAuthSecretKey(meta.Value); secretKey != "" {
					b.WriteString("  const _payload = await c.req.text();\n")
					b.WriteString("  const _sig = c.req.header('X-Webhook-Signature') ?? '';\n")
					fmt.Fprintf(&b, "  const _expected = createHmac('sha256', env.%s).update(_payload).digest('hex');\n", secretKey)
					b.WriteString("  let _sigBuf: Buffer; try { _sigBuf = Buffer.from(_sig, 'hex'); } catch { return c.json({ error: 'Invalid signature' }, 401 as const); }\n")
					b.WriteString("  if (_sigBuf.length !== Buffer.from(_expected, 'hex').length || !timingSafeEqual(_sigBuf, Buffer.from(_expected, 'hex'))) return c.json({ error: 'Invalid signature' }, 401 as const);\n")
				}
			}
		}

		// Build context vars from middleware inject statements (same wiring genRoute uses)
		ctxVars := make(map[string]bool)
		boundVars := make(map[string]string)
		varModels := make(map[string]string)
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				if mw, ok := g.middlewares[meta.Use.Name]; ok {
					for k, v := range extractInjectedVars(mw.Before) {
						ctxVars[k] = v
					}
					for k, v := range extractInjectedVars(mw.After) {
						ctxVars[k] = v
					}
					for modelName, ctxName := range extractInjectedModelMap(mw.Before) {
						ctxRef := "c.get('" + toCamelCase(ctxName) + "')"
						boundVars[modelName] = ctxRef
						boundVars[ctxName] = ctxRef
						camelCtx := toCamelCase(ctxName)
						boundVars[camelCtx] = ctxRef
						varModels[ctxName] = modelName
						varModels[camelCtx] = modelName
					}
					for modelName, ctxName := range extractInjectedModelMap(mw.After) {
						ctxRef := "c.get('" + toCamelCase(ctxName) + "')"
						boundVars[modelName] = ctxRef
						boundVars[ctxName] = ctxRef
						camelCtx := toCamelCase(ctxName)
						boundVars[camelCtx] = ctxRef
						varModels[ctxName] = modelName
						varModels[camelCtx] = modelName
					}
				}
			}
		}

		ctx := emitCtx{
			kind:        "function",
			ctxVars:     ctxVars,
			boundVars:   boundVars,
			varModels:   varModels,
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}
		// Mark path params as declared so emitArrowStmts doesn't re-declare them
		for _, param := range pathParams {
			ctx.declared[toCamelCase(param)] = true
		}

		b.WriteString("  try {\n")

		// Emit setup statements (fetch/guard/etc.) BEFORE entering the SSE
		// stream, wrapped in the same BpError try/catch shape REST handlers
		// use. Previously this ran inside the detached streamSSE callback, so
		// a `guard` threw after the 200 had already been sent and the
		// declared status code (e.g. 404) never reached the client.
		if len(ep.Stmts) > 0 {
			g.emitArrowStmts(&b, ep.Stmts, "    ", ctx)
		}

		b.WriteString("  return streamSSE(c, async (stream) => {\n")
		// Every write for this client goes through one bounded queue: writes
		// never run concurrently, and a client that falls more than
		// BP_SSE_MAX_PENDING events behind is dropped (see src/lib/backpressure.ts).
		fmt.Fprintf(&b, "    const _sse = createSseWriter(stream, %q);\n", ep.Path)
		b.WriteString("    stream.onAbort(() => _sse.close());\n")

		// Emit each handler block as an event subscription or timeout
		for i, h := range ep.Handlers {
			if h.Timeout != "" {
				// Timeout handler: setInterval that sends a periodic SSE message
				ms := durationToMS(h.Timeout)
				sseData := extractOutputValue(h.Body, &ctx)
				fmt.Fprintf(&b, "    // on timeout: %s\n", h.Timeout)
				fmt.Fprintf(&b, "    const _interval%d = setInterval(async () => {\n", i)
				fmt.Fprintf(&b, "      _sse.send({ event: 'ping', data: JSON.stringify(%s) });\n", sseData)
				fmt.Fprintf(&b, "    }, %s);\n", ms)
				fmt.Fprintf(&b, "    stream.onAbort(() => clearInterval(_interval%d));\n", i)
			} else if h.EventName != "" {
				// Event subscription: bind the handler to a const so it can be
				// unsubscribed via off() on abort — otherwise it stays
				// registered against the shared event bus forever after the
				// client disconnects.
				handlerVar := fmt.Sprintf("_streamHandler%d", i)
				fmt.Fprintf(&b, "    // on event: %s\n", h.EventName)
				fmt.Fprintf(&b, "    const %s = async (eventData: any) => {\n", handlerVar)

				// Build handler context with eventData available
				handlerCtx := ctx
				handlerCtx.declared = make(map[string]bool)
				for k, v := range ctx.declared {
					handlerCtx.declared[k] = v
				}
				handlerCtx.declared["eventData"] = true
				// Map 'event' -> 'eventData' so event.sender becomes eventData.sender
				handlerCtx.boundVars = make(map[string]string)
				for k, v := range ctx.boundVars {
					handlerCtx.boundVars[k] = v
				}
				handlerCtx.boundVars["event"] = "eventData"

				// Emit condition with eventData prefix for event fields
				if h.Condition != nil {
					condCtx := handlerCtx
					cond := streamCondToJS(h.Condition, &condCtx)
					fmt.Fprintf(&b, "      if (%s) {\n", cond)
					// Get the output value from the handler body for writeSSE data
					sseData := extractOutputValue(h.Body, &handlerCtx)
					fmt.Fprintf(&b, "        _sse.send({ event: '%s', data: JSON.stringify(%s) });\n", h.EventName, sseData)
					b.WriteString("      }\n")
				} else {
					sseData := extractOutputValue(h.Body, &handlerCtx)
					fmt.Fprintf(&b, "      _sse.send({ event: '%s', data: JSON.stringify(%s) });\n", h.EventName, sseData)
				}
				b.WriteString("    };\n")
				fmt.Fprintf(&b, "    onStream('%s', %s);\n", h.EventName, handlerVar)
				fmt.Fprintf(&b, "    stream.onAbort(() => offStream('%s', %s));\n", h.EventName, handlerVar)
			}
		}

		// Keep the stream open (await abort signal)
		b.WriteString("    await new Promise<void>((resolve) => stream.onAbort(resolve));\n")
		b.WriteString("  });\n")
		b.WriteString("  } catch (err) {\n")
		b.WriteString("    if (err instanceof BpError) return c.json({ error: err.message }, err.statusCode);\n")
		b.WriteString("    throw err;\n")
		b.WriteString("  }\n")
		b.WriteString("});\n\n")
	}

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/routes/%s.ts", toKebabCase(fileKey)),
		Content: []byte(b.String()),
	}
}

// --- WebSocket Endpoints ---

// genWsRoute generates a route file for a group of WebSocket endpoints sharing the same resource.
// fileKey is the filename stem (may have "-ws" suffix when conflicting with REST/stream route files).
func (g *Generator) genWsRoute(resource, fileKey string, endpoints []*ast.WsEndpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	// Typed Variables for middleware-injected context (see honoVariablesType).
	// The env is applied to both the Hono app and the upgradeWebSocket context,
	// because handlers read c.get(...) from the upgrade callback's `c`.
	var allMetas []*ast.EndpointMeta
	for _, ep := range endpoints {
		allMetas = append(allMetas, ep.Meta...)
	}
	envType, needsSchema := g.honoVariablesType(allMetas)
	if envType != "" {
		b.WriteString("import { Hono, type Context } from 'hono';\n")
	} else {
		b.WriteString("import { Hono } from 'hono';\n")
	}
	b.WriteString("import type { UpgradeWebSocket, WSContext, WSMessageReceive } from 'hono/ws';\n")
	b.WriteString("import { wsFanOutSend } from '../lib/backpressure.js';\n")
	// whisper connection(id) needs the process-wide connection registry, which
	// every WS route file shares (src/lib/ws-connections.ts).
	usesWhisper := fileHasCall(g.file, "whisper")
	if usesWhisper {
		b.WriteString("import { registerWsConnection, unregisterWsSocket, wsWhisper } from '../lib/ws-connections.js';\n")
	}
	for _, ep := range endpoints {
		if len(realtimePathInputs(ep.Stmts)) > 0 {
			b.WriteString("import { z } from 'zod';\n")
			break
		}
	}

	// Collect imports from all endpoint bodies
	ic := newImportCollector()
	for _, ep := range endpoints {
		ic.merge(g.collectImports(ep.OnConnect))
		ic.merge(g.collectImports(ep.OnMessage))
		ic.merge(g.collectImports(ep.OnDisconnect))
	}

	// Check if any endpoint uses data operations
	needsDB := false
	for _, ep := range endpoints {
		if stmtsHaveDataOps(ep.OnConnect) || stmtsHaveDataOps(ep.OnMessage) || stmtsHaveDataOps(ep.OnDisconnect) {
			needsDB = true
			break
		}
	}
	if needsDB {
		writeDataImports(&b)
	} else if needsSchema {
		b.WriteString("import * as schema from '../models/schema.js';\n")
	}

	// Collect middleware, webhook-auth, and rate-limit meta the same way genRoute
	// does — WS endpoints previously parsed but silently dropped this meta. Bare
	// auth identifiers (e.g. `auth bearer`) still can't be enforced here: there's
	// no generic bearer/jwt/api_key/basic verification codegen anywhere in this
	// target (genRoute doesn't enforce them either), so inventing one here would
	// risk conflicting with a hand-written check in on_connect. The linter flags
	// that gap instead — see checkUnenforceableWsAuth in internal/linter.
	middlewareNames := make(map[string]bool)
	needsWebhookAuth := false
	needsRequireAuth := false
	needsRateLimit := false
	for _, ep := range endpoints {
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				middlewareNames[meta.Use.Name] = true
			}
			if meta.Kind == "auth" && webhookAuthSecretKey(meta.Value) != "" {
				needsWebhookAuth = true
			}
			if _, ok := endpointEnforcedAuth(meta.Value); ok && meta.Kind == "auth" {
				needsRequireAuth = true
			}
			if meta.Kind == "limit" {
				needsRateLimit = true
			}
		}
	}
	for _, mwName := range sortedKeys2(middlewareNames) {
		fmt.Fprintf(&b, "import { %s } from '../middleware/%s.js';\n",
			toCamelCase(mwName), toKebabCase(mwName))
	}

	b.WriteString("import { BpError } from '../lib/errors.js';\n")
	// Every WS connection gets its id from randomUUID (connection.id).
	if needsWebhookAuth {
		b.WriteString("import { createHmac, randomUUID, timingSafeEqual } from 'node:crypto';\n")
	} else {
		b.WriteString("import { randomUUID } from 'node:crypto';\n")
	}
	if needsWebhookAuth {
		ic.needsEnv = true
	}
	if needsRequireAuth {
		b.WriteString("import { requireAuth as _requireAuth } from '../lib/auth.js';\n")
	}
	// WS handler bodies may read env.NAME; env.ts is always generated.
	ic.needsEnv = true
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	// Use a factory function so index.ts can pass the Node.js upgradeWebSocket at runtime
	routeVar := toCamelCase(fileKey) + "Routes"
	if envType != "" {
		fmt.Fprintf(&b, "type RouteEnv = %s;\n\n", envType)
	}
	fmt.Fprintf(&b, "export function create%sRoutes(upgradeWebSocket: UpgradeWebSocket) {\n",
		toPascalCase(fileKey))
	if envType != "" {
		fmt.Fprintf(&b, "  const %s = new Hono<RouteEnv>();\n\n", routeVar)
	} else {
		fmt.Fprintf(&b, "  const %s = new Hono();\n\n", routeVar)
	}

	// Room management: Map<roomId, Set<WebSocket>> for local connections,
	// plus Redis pub/sub for multi-instance broadcast.
	b.WriteString("  // Room management for join/leave/broadcast (multi-instance via Redis pub/sub)\n")
	b.WriteString("  const _rooms = new Map<string, Set<any>>();\n")
	b.WriteString("  let _redisSub: any = null;\n")
	b.WriteString("  let _redisPub: any = null;\n")
	b.WriteString("  let _redisInit: Promise<void> | null = null;\n")
	b.WriteString("  function _ensureRedis(): Promise<void> {\n")
	// REDIS_URL is optional here (read from process.env, like the STREAM
	// fan-out): env.ts only declares it when cache/queue/workers need it, and
	// without it rooms stay local to this instance.
	b.WriteString("    const _redisUrl = process.env.REDIS_URL;\n")
	b.WriteString("    if (!_redisInit && _redisUrl) {\n")
	b.WriteString("      _redisInit = (async () => {\n")
	b.WriteString("        const { createClient } = await import('redis');\n")
	b.WriteString("        _redisSub = createClient({ url: _redisUrl });\n")
	b.WriteString("        _redisPub = createClient({ url: _redisUrl });\n")
	b.WriteString("        await _redisSub.connect();\n")
	b.WriteString("        await _redisPub.connect();\n")
	b.WriteString("      })().catch((e: any) => { console.error('Redis pub/sub init failed:', e); });\n")
	b.WriteString("    }\n")
	b.WriteString("    return _redisInit ?? Promise.resolve();\n")
	b.WriteString("  }\n\n")

	// Every room delivery (local broadcast, Redis subscriber callback) goes
	// through _fanOut, which applies the slow-consumer limit to each socket.
	b.WriteString("  // Remove a socket from every room; used on disconnect and when it is a slow consumer.\n")
	b.WriteString("  function _evictSocket(_w: any) {\n")
	b.WriteString("    for (const [k, s] of _rooms) { s.delete(_w); if (s.size === 0) { _rooms.delete(k); if (_redisSub?.isOpen) _redisSub.unsubscribe(`room:${k}`); } }\n")
	if usesWhisper {
		b.WriteString("    unregisterWsSocket(_w);\n")
	}
	b.WriteString("  }\n")
	b.WriteString("  function _fanOut(roomId: string, data: string) {\n")
	b.WriteString("    for (const _w of [...(_rooms.get(roomId) ?? [])]) wsFanOutSend(_w, data, 'room ' + roomId, _evictSocket);\n")
	b.WriteString("  }\n\n")

	// Module-level rate limit store (shared across all handlers in this file)
	if needsRateLimit {
		b.WriteString("  // Module-level rate limit store with periodic cleanup\n")
		b.WriteString("  const _rateLimitStore = new Map<string, { count: number, resetAt: number }>();\n")
		b.WriteString("  setInterval(() => { const now = Date.now(); for (const [k, v] of _rateLimitStore) { if (v.resetAt <= now) _rateLimitStore.delete(k); } }, 60000);\n\n")
	}

	for _, ep := range endpoints {
		fmt.Fprintf(&b, "// WS %s\n", ep.Path)
		if ep.Intent != nil {
			fmt.Fprintf(&b, "// %s\n", ep.Intent.Text)
		}

		// Extract path params for this endpoint
		pathParams := extractPathParams(ep.Path)

		// Build handler: routeVar.get('path', ...use middleware, [rate-limit/webhook-auth middleware], upgradeWebSocket(...))
		fmt.Fprintf(&b, "%s.get('%s'", routeVar, ep.Path)
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				fmt.Fprintf(&b, ", %s", toCamelCase(meta.Use.Name))
			}
		}

		// Enforced auth runs before path validation and the handshake, so an
		// unauthenticated client gets 401 and never reaches the upgrade.
		if ea, ok := endpointEnforcedAuthMeta(ep.Meta); ok {
			fmt.Fprintf(&b, ", %s", requireAuthMiddleware(ea, true, g.blueprintNameOf()))
		}

		// Declared path inputs are validated before the handshake: a failing
		// middleware returns 400 and upgradeWebSocket's factory never runs.
		pathInputs := realtimePathInputs(ep.Stmts)
		declaredParams := map[string]bool{}
		var pathSchema, pathRaw string
		if len(pathInputs) > 0 {
			pathSchema, pathRaw = g.realtimePathParamCheck(pathInputs)
			for _, inp := range pathInputs {
				declaredParams[inp.Name] = true
			}
			fmt.Fprintf(&b, ", async (c, next) => {\n  const _pathParams = %s.safeParse(%s);\n", pathSchema, pathRaw)
			b.WriteString("  if (!_pathParams.success) return c.json(_pathParams, 400 as const);\n")
			b.WriteString("  return next();\n}")
		}

		// Rate-limit and webhook-auth meta can't run inside upgradeWebSocket's
		// event factory (it can only return WS event handlers, not an HTTP
		// response), so — exactly like genRoute's checks — they run as a
		// plain middleware that can reject the upgrade before the handshake.
		hasLimitMeta := false
		hasWebhookAuthMeta := false
		for _, meta := range ep.Meta {
			if meta.Kind == "limit" {
				hasLimitMeta = true
			}
			if meta.Kind == "auth" && webhookAuthSecretKey(meta.Value) != "" {
				hasWebhookAuthMeta = true
			}
		}
		if hasLimitMeta || hasWebhookAuthMeta {
			b.WriteString(", async (c, next) => {\n")
			for _, meta := range ep.Meta {
				switch meta.Kind {
				case "limit":
					rateStr := exprToJS(meta.Value)
					rateLimit, rateWindowMS := parseRateLimit(rateStr)
					fmt.Fprintf(&b, "  // rate limit: %s\n", rateStr)
					b.WriteString("  const _clientIp = c.req.header('x-forwarded-for') || 'unknown';\n")
					b.WriteString("  const _now = Date.now();\n")
					b.WriteString("  const _rateKey = `${_clientIp}:${c.req.path}`;\n")
					b.WriteString("  const _rateEntry = _rateLimitStore.get(_rateKey);\n")
					b.WriteString("  if (_rateEntry && _rateEntry.resetAt > _now) {\n")
					fmt.Fprintf(&b, "    if (_rateEntry.count >= %d) {\n", rateLimit)
					b.WriteString("      return c.json({ error: 'Rate limit exceeded' }, 429 as const);\n")
					b.WriteString("    }\n")
					b.WriteString("    _rateEntry.count++;\n")
					b.WriteString("  } else {\n")
					fmt.Fprintf(&b, "    _rateLimitStore.set(_rateKey, { count: 1, resetAt: _now + %d });\n", rateWindowMS)
					b.WriteString("  }\n")
				case "auth":
					if secretKey := webhookAuthSecretKey(meta.Value); secretKey != "" {
						b.WriteString("  const _payload = await c.req.text();\n")
						b.WriteString("  const _sig = c.req.header('X-Webhook-Signature') ?? '';\n")
						fmt.Fprintf(&b, "  const _expected = createHmac('sha256', env.%s).update(_payload).digest('hex');\n", secretKey)
						b.WriteString("  let _sigBuf: Buffer; try { _sigBuf = Buffer.from(_sig, 'hex'); } catch { return c.json({ error: 'Invalid signature' }, 401 as const); }\n")
						b.WriteString("  if (_sigBuf.length !== Buffer.from(_expected, 'hex').length || !timingSafeEqual(_sigBuf, Buffer.from(_expected, 'hex'))) return c.json({ error: 'Invalid signature' }, 401 as const);\n")
					}
				}
			}
			b.WriteString("  return next();\n")
			b.WriteString("}")
		}

		if envType != "" {
			b.WriteString(", upgradeWebSocket((c: Context<RouteEnv>) => {\n")
		} else {
			b.WriteString(", upgradeWebSocket((c) => {\n")
		}

		// Extract path params into closure-scoped variables so all handlers can access them.
		// Declared inputs were validated by the middleware above, so re-parse to get typed values.
		for _, param := range pathParams {
			if declaredParams[param] {
				fmt.Fprintf(&b, "  const %s = %s.parse(%s).%s;\n", toCamelCase(param), pathSchema, pathRaw, param)
			} else {
				fmt.Fprintf(&b, "  const %s = c.req.param('%s') || '';\n", toCamelCase(param), param)
			}
		}

		// connection.id: a fresh id per socket, shared by onOpen, onMessage and
		// onClose because this factory runs once per upgrade request.
		b.WriteString("  const _connectionId = randomUUID();\n")
		b.WriteString("  const connection = { id: _connectionId };\n\n")

		// Hoist variables from onConnect that are used across handlers.
		// Variables assigned in onConnect (step bindings + inject) need to be
		// accessible in onMessage and onClose, so we declare them in the outer scope.
		hoistedVars := collectBindings(ep.OnConnect)
		// Also collect inject-as names
		for _, name := range collectInjectNames(ep.OnConnect) {
			hoistedVars[name] = true
		}
		// Emit hoisted let declarations in the outer closure scope
		for _, name := range sortedKeys2(hoistedVars) {
			fmt.Fprintf(&b, "  let %s: any;\n", name)
		}
		b.WriteString("\n")

		b.WriteString("  return {\n")

		// Build context vars from middleware inject statements (same wiring genRoute uses)
		ctxVars := make(map[string]bool)
		boundVars := make(map[string]string)
		varModels := make(map[string]string)
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				if mw, ok := g.middlewares[meta.Use.Name]; ok {
					for k, v := range extractInjectedVars(mw.Before) {
						ctxVars[k] = v
					}
					for k, v := range extractInjectedVars(mw.After) {
						ctxVars[k] = v
					}
					for modelName, ctxName := range extractInjectedModelMap(mw.Before) {
						ctxRef := "c.get('" + toCamelCase(ctxName) + "')"
						boundVars[modelName] = ctxRef
						boundVars[ctxName] = ctxRef
						camelCtx := toCamelCase(ctxName)
						boundVars[camelCtx] = ctxRef
						varModels[ctxName] = modelName
						varModels[camelCtx] = modelName
					}
					for modelName, ctxName := range extractInjectedModelMap(mw.After) {
						ctxRef := "c.get('" + toCamelCase(ctxName) + "')"
						boundVars[modelName] = ctxRef
						boundVars[ctxName] = ctxRef
						camelCtx := toCamelCase(ctxName)
						boundVars[camelCtx] = ctxRef
						varModels[ctxName] = modelName
						varModels[camelCtx] = modelName
					}
				}
			}
		}

		// Build base context with WS kind, path params, and hoisted vars marked as declared
		baseCtx := emitCtx{
			kind:        "ws",
			ctxVars:     ctxVars,
			boundVars:   boundVars,
			varModels:   varModels,
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}
		for _, param := range pathParams {
			baseCtx.declared[toCamelCase(param)] = true
		}
		// Mark hoisted vars as declared so emitArrowStmts emits assignments, not const declarations
		for name := range hoistedVars {
			baseCtx.declared[name] = true
		}

		// onOpen handler — wrapped in try/catch because @hono/node-ws invokes
		// onOpen/onMessage/onClose without awaiting them. An async handler
		// that throws (e.g. a `guard` failing) rejects a promise nobody is
		// watching; Node's default unhandledRejection behavior is to crash
		// the process, taking down every other connection with it.
		b.WriteString("    async onOpen(evt: Event, ws: WSContext) {\n")
		if usesWhisper {
			b.WriteString("      registerWsConnection(_connectionId, ws);\n")
		}
		b.WriteString("      try {\n")
		if len(ep.OnConnect) > 0 {
			onConnectCtx := baseCtx
			onConnectCtx.declared = make(map[string]bool)
			for k, v := range baseCtx.declared {
				onConnectCtx.declared[k] = v
			}
			g.emitArrowStmts(&b, ep.OnConnect, "        ", onConnectCtx)
		}
		b.WriteString("      } catch (err) {\n")
		b.WriteString("        if (err instanceof BpError) { const _reason = err.message.slice(0, 123); ws.send(JSON.stringify({ error: err.message })); ws.close(1008, _reason); return; }\n")
		b.WriteString("        console.error(err);\n")
		b.WriteString("      }\n")
		b.WriteString("    },\n")

		// onMessage handler — event.data is fully client-controlled, so the
		// JSON.parse is guarded separately from the rest of the handler: a
		// malformed payload gets an error frame back instead of an unhandled
		// throw, and a thrown BpError from guards/logic also becomes an
		// error frame while leaving the socket open for the next message.
		b.WriteString("    async onMessage(event: MessageEvent<WSMessageReceive>, ws: WSContext) {\n")
		b.WriteString("      let message: any;\n")
		b.WriteString("      try {\n")
		b.WriteString("        message = typeof event.data === 'string' ? JSON.parse(event.data) : event.data;\n")
		b.WriteString("      } catch {\n")
		b.WriteString("        ws.send(JSON.stringify({ error: 'Invalid JSON' }));\n")
		b.WriteString("        return;\n")
		b.WriteString("      }\n")
		b.WriteString("      try {\n")
		if len(ep.OnMessage) > 0 {
			onMessageCtx := baseCtx
			onMessageCtx.declared = make(map[string]bool)
			for k, v := range baseCtx.declared {
				onMessageCtx.declared[k] = v
			}
			onMessageCtx.declared["message"] = true
			g.emitArrowStmts(&b, ep.OnMessage, "        ", onMessageCtx)
		}
		b.WriteString("      } catch (err) {\n")
		b.WriteString("        ws.send(JSON.stringify({ error: err instanceof BpError ? err.message : 'Internal error' }));\n")
		b.WriteString("        if (!(err instanceof BpError)) console.error(err);\n")
		b.WriteString("      }\n")
		b.WriteString("    },\n")

		// onClose handler — evict this socket from every room it's still in
		// (a user `leave` only removes it from one room) so dead sockets and
		// emptied-out room entries don't accumulate in _rooms forever.
		b.WriteString("    async onClose(evt: CloseEvent, ws: WSContext) {\n")
		if usesWhisper {
			b.WriteString("      unregisterWsSocket(ws);\n")
		}
		b.WriteString("      for (const [k, s] of _rooms) { s.delete(ws); if (s.size === 0) { _rooms.delete(k); if (_redisSub?.isOpen) _redisSub.unsubscribe(`room:${k}`); } }\n")
		b.WriteString("      try {\n")
		if len(ep.OnDisconnect) > 0 {
			onDisconnectCtx := baseCtx
			onDisconnectCtx.declared = make(map[string]bool)
			for k, v := range baseCtx.declared {
				onDisconnectCtx.declared[k] = v
			}
			g.emitArrowStmts(&b, ep.OnDisconnect, "        ", onDisconnectCtx)
		}
		b.WriteString("      } catch (err) {\n")
		b.WriteString("        console.error(err);\n")
		b.WriteString("      }\n")
		b.WriteString("    },\n")

		b.WriteString("  };\n")
		b.WriteString("  }));\n\n")
	}

	fmt.Fprintf(&b, "  return %s;\n", routeVar)
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/routes/%s.ts", toKebabCase(fileKey)),
		Content: []byte(b.String()),
	}
}

// collectInjectNames scans arrow statements for inject calls and returns
// the camelCase names of injected variables.
func collectInjectNames(stmts []ast.ArrowStmt) []string {
	var names []string
	for _, s := range stmts {
		if step, ok := s.(*ast.StepStmt); ok {
			if fn, ok := step.Expr.(*ast.FnCall); ok && fn.Name == "inject" && len(fn.Args) >= 2 {
				names = append(names, toCamelCase(exprToString(fn.Args[1])))
			}
		}
	}
	return names
}

// extractOutputValue finds the first OutputStmt in a list of arrow statements
// and returns its value as a JS expression. Returns "{}" if no output is found.
func extractOutputValue(stmts []ast.ArrowStmt, ctx *emitCtx) string {
	for _, s := range stmts {
		if out, ok := s.(*ast.OutputStmt); ok {
			return exprToJSWithCtx(out.Value, ctx)
		}
	}
	return "{}"
}

// streamCondToJS converts a STREAM event handler condition to JavaScript.
// Identifiers not in the declared context are prefixed with "eventData."
// since they refer to fields on the event payload.
func streamCondToJS(e ast.Expr, ctx *emitCtx) string {
	switch v := e.(type) {
	case *ast.BinaryExpr:
		left := streamCondToJS(v.Left, ctx)
		right := streamCondToJS(v.Right, ctx)
		op := v.Op
		if op == "==" {
			op = "==="
		}
		if op == "!=" {
			op = "!=="
		}
		return fmt.Sprintf("%s %s %s", left, op, right)
	case *ast.Ident:
		name := toCamelCase(v.Name)
		if ctx != nil && ctx.declared[name] {
			return name
		}
		// Not in scope — it's an event field
		return "eventData." + name
	case *ast.FieldAccess:
		obj := streamCondToJS(v.Base, ctx)
		return fmt.Sprintf("%s.%s", obj, toCamelCase(v.Field))
	default:
		return exprToJSWithCtx(e, ctx)
	}
}

// walkEndpointStmts visits every ArrowStmt in an endpoint (including nested
// when/try bodies). Used by import/feature detection scans.
func walkEndpointStmts(stmts []ast.ArrowStmt, visit func(ast.ArrowStmt)) {
	for _, s := range stmts {
		visit(s)
		switch v := s.(type) {
		case *ast.WhenStmt:
			walkEndpointStmts(v.Body, visit)
		case *ast.TryRecover:
			walkEndpointStmts(v.Try, visit)
			walkEndpointStmts(v.Recover, visit)
		}
	}
}
