package js

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

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

	// Import worker handlers
	if len(workers) > 0 {
		b.WriteString("import { Worker } from 'bullmq';\n")
		for _, w := range workers {
			name := toCamelCase(w.Name)
			onFailName := name + "OnFail"
			queueName := name + "QueueName"
			timeoutName := name + "TimeoutMs"
			fmt.Fprintf(&b, "import { %s, %s, %s, %s } from './workers/%s.js';\n",
				name, onFailName, queueName, timeoutName, toKebabCase(w.Name))
		}
	}

	// Import schedule handlers
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

	// Start workers
	if len(workers) > 0 {
		b.WriteString("// Start workers\n")
		for _, w := range workers {
			name := toCamelCase(w.Name)
			queueName := name + "QueueName"
			timeoutName := name + "TimeoutMs"
			fmt.Fprintf(&b, "new Worker(%s, async (job) => {\n", queueName)
			b.WriteString("  try {\n")
			fmt.Fprintf(&b, "    if (%s > 0) {\n", timeoutName)
			fmt.Fprintf(&b, "      await Promise.race([%s(job.data), new Promise((_, reject) => setTimeout(() => reject(new Error('Worker timeout')), %s))]);\n", name, timeoutName)
			b.WriteString("    } else {\n")
			fmt.Fprintf(&b, "      await %s(job.data);\n", name)
			b.WriteString("    }\n")
			b.WriteString("  } catch (error) {\n")
			fmt.Fprintf(&b, "    await %sOnFail(job.data, error instanceof Error ? error : new Error(String(error)));\n", name)
			b.WriteString("    throw error;\n")
			b.WriteString("  }\n")
			b.WriteString("}, {\n")
			b.WriteString("  connection: { url: env.REDIS_URL }\n")
			b.WriteString("});\n")
		}
		b.WriteString("\n")
	}

	// Register scheduled jobs
	if len(schedules) > 0 {
		b.WriteString("// Register scheduled jobs\n")
		b.WriteString("const schedulerQueue = new Queue('scheduler', { connection: { url: env.REDIS_URL } });\n")
		for _, s := range schedules {
			name := toCamelCase(s.Name)
			cronName := name + "Cron"
			fmt.Fprintf(&b, "schedulerQueue.add('%s', {}, { repeat: { pattern: %s } });\n", s.Name, cronName)
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

	// Start the server and register shutdown handlers — skipped under test runners
	// (Vitest) so importing the app for `app.request(...)` never binds a port.
	b.WriteString("if (!process.env.VITEST) {\n")
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

	b.WriteString("export default app;\n")

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
	needsZodPathParam := false
	needsRateLimit := false

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
					// Check if typed path param needs Zod validation
					tn := pathParamTypeName(inp.Type)
					if tn == "uuid" || tn == "int" {
						needsZodPathParam = true
					}
				}
			}
		}
		// Check for middleware, auth, and rate limit
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				middlewareNames[meta.Use.Name] = true
			}
			if meta.Kind == "auth" && webhookAuthSecretKey(meta.Value) != "" {
				needsWebhookAuth = true
			}
			if meta.Kind == "limit" {
				needsRateLimit = true
			}
		}
	}

	if needsZValidator {
		b.WriteString("import { zValidator } from '@hono/zod-validator';\n")
	}
	if needsZodPathParam {
		b.WriteString("import { z } from 'zod';\n")
	}
	if needsDB {
		b.WriteString("import { db } from '../lib/db.js';\n")
		b.WriteString("import * as schema from '../models/schema.js';\n")
		b.WriteString("import { eq, and, or, lt, gt, lte, gte, ne, sql, desc, asc, inArray } from 'drizzle-orm';\n")
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
	b.WriteString("import { BpError } from '../lib/errors.js';\n")
	if needsWebhookAuth {
		b.WriteString("import { createHmac, timingSafeEqual } from 'node:crypto';\n")
	}

	// Collect additional imports from endpoint bodies (pipes, functions, storage, env, enums)
	ic := newImportCollector()
	for _, ep := range endpoints {
		ic.merge(g.collectImports(ep.Stmts))
	}
	if needsWebhookAuth {
		ic.needsEnv = true
	}
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	// Collect context variables injected by middleware for Hono type parameter.
	// We track both the var set and a camelCasedVar -> Blueprint model name map
	// so the emitted `Hono<{ Variables: { ... } }>` types each slot to the
	// underlying Drizzle row type instead of falling back to `any`.
	allCtxVars := make(map[string]bool)
	ctxVarModel := make(map[string]string)
	for _, ep := range endpoints {
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				if mw, ok := g.middlewares[meta.Use.Name]; ok {
					for k := range extractInjectedVars(mw.Before) {
						allCtxVars[toCamelCase(k)] = true
					}
					for k := range extractInjectedVars(mw.After) {
						allCtxVars[toCamelCase(k)] = true
					}
					// extractInjectedModelMap returns model -> injected var name;
					// invert to var -> model so we can look up by Hono slot.
					for model, varName := range extractInjectedModelMap(mw.Before) {
						ctxVarModel[toCamelCase(varName)] = model
					}
					for model, varName := range extractInjectedModelMap(mw.After) {
						ctxVarModel[toCamelCase(varName)] = model
					}
				}
			}
		}
	}

	routeVar := toCamelCase(resource) + "Routes"
	if len(allCtxVars) > 0 {
		// Type the Hono app with Variables so c.get()/c.set() are type-safe.
		// For middleware-injected vars whose source model is known we wrap
		// the Drizzle row type with a mapped NonNullable so columns without
		// `required` (just `default(...)`) don't show as `T | null` and
		// break downstream handler code that's already gated by a middleware
		// `guard` — Blueprint's contract is "if it got injected, it's usable".
		// Tracked-but-nullable-aware schemas can be added later; for now this
		// matches the de-facto runtime behavior the previous `: any` shipped.
		varKeys := sortedKeys2(allCtxVars)
		var fields []string
		for _, k := range varKeys {
			typeStr := "any"
			if model, ok := ctxVarModel[k]; ok {
				rowT := "typeof schema." + toCamelCase(model) + ".$inferSelect"
				typeStr = fmt.Sprintf("{ [K in keyof %s]: NonNullable<%s[K]> }", rowT, rowT)
			}
			fields = append(fields, k+": "+typeStr)
		}
		fmt.Fprintf(&b, "export const %s = new Hono<{ Variables: { %s } }>();\n\n",
			routeVar, strings.Join(fields, "; "))
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
		method := strings.ToLower(ep.Method)
		fmt.Fprintf(&b, "// %s %s\n", ep.Method, ep.Path)
		if ep.Intent != nil {
			fmt.Fprintf(&b, "// %s\n", ep.Intent.Text)
		}

		// Build handler: routeVar.method('path', ...middleware, zValidator, async (c) => { ... })
		fmt.Fprintf(&b, "%s.%s('%s'", routeVar, method, ep.Path)

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
				fmt.Fprintf(&b, "  // cache: %s\n", exprToJS(meta.Value))
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

// --- Stream Endpoints ---

// genStreamRoute generates a route file for a group of stream (SSE) endpoints sharing the same resource.
// fileKey is the filename stem (may have "-stream" suffix when conflicting with a REST route).
func (g *Generator) genStreamRoute(resource, fileKey string, endpoints []*ast.StreamEndpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { Hono } from 'hono';\n")
	b.WriteString("import { streamSSE } from 'hono/streaming';\n")

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
	if needsDB {
		writeDataImports(&b)
	}

	b.WriteString("import { BpError } from '../lib/errors.js';\n")

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
		b.WriteString("import { on } from '../lib/events.js';\n")
	}
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(fileKey) + "Routes"
	fmt.Fprintf(&b, "export const %s = new Hono();\n\n", routeVar)

	for _, ep := range endpoints {
		fmt.Fprintf(&b, "// STREAM %s\n", ep.Path)
		if ep.Intent != nil {
			fmt.Fprintf(&b, "// %s\n", ep.Intent.Text)
		}

		fmt.Fprintf(&b, "%s.get('%s', async (c) => {\n", routeVar, ep.Path)

		// Extract path params at the start of the handler
		pathParams := extractPathParams(ep.Path)
		for _, param := range pathParams {
			fmt.Fprintf(&b, "  const %s = c.req.param('%s');\n", toCamelCase(param), param)
		}

		b.WriteString("  return streamSSE(c, async (stream) => {\n")

		ctx := emitCtx{
			kind:        "function",
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}
		// Mark path params as declared so emitArrowStmts doesn't re-declare them
		for _, param := range pathParams {
			ctx.declared[toCamelCase(param)] = true
		}

		// Emit setup statements
		if len(ep.Stmts) > 0 {
			g.emitArrowStmts(&b, ep.Stmts, "    ", ctx)
		}

		// Emit each handler block as an event subscription or timeout
		for _, h := range ep.Handlers {
			if h.Timeout != "" {
				// Timeout handler: setInterval that sends a periodic SSE message
				ms := durationToMS(h.Timeout)
				sseData := extractOutputValue(h.Body, &ctx)
				fmt.Fprintf(&b, "    // on timeout: %s\n", h.Timeout)
				b.WriteString("    const _interval = setInterval(async () => {\n")
				fmt.Fprintf(&b, "      await stream.writeSSE({ event: 'ping', data: JSON.stringify(%s) });\n", sseData)
				fmt.Fprintf(&b, "    }, %s);\n", ms)
				b.WriteString("    stream.onAbort(() => clearInterval(_interval));\n")
			} else if h.EventName != "" {
				// Event subscription: on('event_name', async (eventData) => { ... })
				fmt.Fprintf(&b, "    // on event: %s\n", h.EventName)
				fmt.Fprintf(&b, "    on('%s', async (eventData: any) => {\n", h.EventName)

				// Build handler context with eventData available
				handlerCtx := ctx
				handlerCtx.declared = make(map[string]bool)
				for k, v := range ctx.declared {
					handlerCtx.declared[k] = v
				}
				handlerCtx.declared["eventData"] = true
				// Map 'event' -> 'eventData' so event.sender becomes eventData.sender
				if handlerCtx.boundVars == nil {
					handlerCtx.boundVars = make(map[string]string)
				}
				handlerCtx.boundVars["event"] = "eventData"

				// Emit condition with eventData prefix for event fields
				if h.Condition != nil {
					condCtx := handlerCtx
					cond := streamCondToJS(h.Condition, &condCtx)
					fmt.Fprintf(&b, "      if (%s) {\n", cond)
					// Get the output value from the handler body for writeSSE data
					sseData := extractOutputValue(h.Body, &handlerCtx)
					fmt.Fprintf(&b, "        await stream.writeSSE({ event: '%s', data: JSON.stringify(%s) });\n", h.EventName, sseData)
					b.WriteString("      }\n")
				} else {
					sseData := extractOutputValue(h.Body, &handlerCtx)
					fmt.Fprintf(&b, "      await stream.writeSSE({ event: '%s', data: JSON.stringify(%s) });\n", h.EventName, sseData)
				}
				b.WriteString("    });\n")
			}
		}

		// Keep the stream open (await abort signal)
		b.WriteString("    await new Promise<void>((resolve) => stream.onAbort(resolve));\n")
		b.WriteString("  });\n")
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
	b.WriteString("import { Hono } from 'hono';\n")
	b.WriteString("import type { UpgradeWebSocket, WSContext, WSMessageReceive } from 'hono/ws';\n")

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
	}

	b.WriteString("import { BpError } from '../lib/errors.js';\n")
	// Import emit from events lib if any handler uses it
	needsEmit := false
	for _, ep := range endpoints {
		if stmtsHaveCall(ep.OnConnect, "emit") || stmtsHaveCall(ep.OnMessage, "emit") || stmtsHaveCall(ep.OnDisconnect, "emit") {
			needsEmit = true
			break
		}
	}
	if needsEmit {
		b.WriteString("import { emit } from '../lib/events.js';\n")
	}
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	// Use a factory function so index.ts can pass the Node.js upgradeWebSocket at runtime
	routeVar := toCamelCase(fileKey) + "Routes"
	fmt.Fprintf(&b, "export function create%sRoutes(upgradeWebSocket: UpgradeWebSocket) {\n",
		toPascalCase(fileKey))
	fmt.Fprintf(&b, "  const %s = new Hono();\n\n", routeVar)

	// Room management: Map<roomId, Set<WebSocket>>
	b.WriteString("  // Room management for join/leave/broadcast\n")
	b.WriteString("  const _rooms = new Map<string, Set<any>>();\n\n")

	for _, ep := range endpoints {
		fmt.Fprintf(&b, "// WS %s\n", ep.Path)
		if ep.Intent != nil {
			fmt.Fprintf(&b, "// %s\n", ep.Intent.Text)
		}

		// Extract path params for this endpoint
		pathParams := extractPathParams(ep.Path)

		fmt.Fprintf(&b, "%s.get('%s', upgradeWebSocket((c) => {\n", routeVar, ep.Path)

		// Extract path params into closure-scoped variables so all handlers can access them
		for _, param := range pathParams {
			fmt.Fprintf(&b, "  const %s = c.req.param('%s') || '';\n", toCamelCase(param), param)
		}

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

		// Build base context with WS kind, path params, and hoisted vars marked as declared
		baseCtx := emitCtx{
			kind:        "ws",
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

		// onOpen handler
		b.WriteString("    async onOpen(evt: Event, ws: WSContext) {\n")
		if len(ep.OnConnect) > 0 {
			onConnectCtx := baseCtx
			onConnectCtx.declared = make(map[string]bool)
			for k, v := range baseCtx.declared {
				onConnectCtx.declared[k] = v
			}
			g.emitArrowStmts(&b, ep.OnConnect, "      ", onConnectCtx)
		}
		b.WriteString("    },\n")

		// onMessage handler
		b.WriteString("    async onMessage(event: MessageEvent<WSMessageReceive>, ws: WSContext) {\n")
		b.WriteString("      const message = typeof event.data === 'string' ? JSON.parse(event.data) : event.data;\n")
		if len(ep.OnMessage) > 0 {
			onMessageCtx := baseCtx
			onMessageCtx.declared = make(map[string]bool)
			for k, v := range baseCtx.declared {
				onMessageCtx.declared[k] = v
			}
			onMessageCtx.declared["message"] = true
			g.emitArrowStmts(&b, ep.OnMessage, "      ", onMessageCtx)
		}
		b.WriteString("    },\n")

		// onClose handler
		b.WriteString("    async onClose(evt: CloseEvent, ws: WSContext) {\n")
		if len(ep.OnDisconnect) > 0 {
			onDisconnectCtx := baseCtx
			onDisconnectCtx.declared = make(map[string]bool)
			for k, v := range baseCtx.declared {
				onDisconnectCtx.declared[k] = v
			}
			g.emitArrowStmts(&b, ep.OnDisconnect, "      ", onDisconnectCtx)
		}
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
