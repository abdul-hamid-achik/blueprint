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
	for _, u := range bp.Uses {
		if pkg, ok := builtinMiddleware[u.Name]; ok {
			b.WriteString(fmt.Sprintf("import { %s } from '%s';\n", u.Name, pkg))
		} else {
			b.WriteString(fmt.Sprintf("import { %s } from './middleware/%s.js';\n",
				toCamelCase(u.Name), toKebabCase(u.Name)))
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
		b.WriteString(fmt.Sprintf("import { %sRoutes } from './routes/%s.js';\n", toCamelCase(res), toKebabCase(res)))
	}

	// Import stream route files
	for _, res := range sortedKeys2(streamResources) {
		fk := fileKeyForStream(res)
		b.WriteString(fmt.Sprintf("import { %sRoutes } from './routes/%s.js';\n", toCamelCase(fk), toKebabCase(fk)))
	}

	// Import WS route files
	for _, res := range sortedKeys2(wsResources) {
		fk := fileKeyForWs(res)
		b.WriteString(fmt.Sprintf("import { %sRoutes } from './routes/%s.js';\n", toCamelCase(fk), toKebabCase(fk)))
	}

	// Import subscription handlers and event emitter
	if len(subscribes) > 0 {
		b.WriteString("import { on } from './lib/events.js';\n")
		for _, sub := range subscribes {
			handlerName := "on" + toPascalCase(strings.ReplaceAll(sub.Event, ".", "_"))
			fileName := toKebabCase(strings.ReplaceAll(sub.Event, ".", "-"))
			b.WriteString(fmt.Sprintf("import { %s } from './subscriptions/%s.js';\n", handlerName, fileName))
		}
	}

	// Import worker handlers
	if len(workers) > 0 {
		b.WriteString("import { Worker } from 'bullmq';\n")
		for _, w := range workers {
			name := toCamelCase(w.Name)
			onFailName := name + "OnFail"
			b.WriteString(fmt.Sprintf("import { %s, %s } from './workers/%s.js';\n",
				name, onFailName, toKebabCase(w.Name)))
		}
	}

	// Import schedule handlers
	if len(schedules) > 0 {
		b.WriteString("import { Queue } from 'bullmq';\n")
		for _, s := range schedules {
			name := toCamelCase(s.Name)
			cronName := name + "Cron"
			b.WriteString(fmt.Sprintf("import { %s, %s } from './schedules/%s.js';\n",
				name, cronName, toKebabCase(s.Name)))
		}
	}

	b.WriteString("\n")

	b.WriteString("const app = new Hono();\n\n")

	// Security headers (X-Content-Type-Options, X-Frame-Options, etc.)
	b.WriteString("app.use('*', secureHeaders());\n\n")

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
			b.WriteString(fmt.Sprintf("app.use('*', %s({ %s }));\n",
				toCamelCase(u.Name), strings.Join(configParts, ", ")))
		} else {
			// Simple middleware
			if _, ok := builtinMiddleware[u.Name]; ok {
				b.WriteString(fmt.Sprintf("app.use('*', %s());\n", toCamelCase(u.Name)))
			} else {
				b.WriteString(fmt.Sprintf("app.use('*', %s);\n", toCamelCase(u.Name)))
			}
		}
	}
	if len(bp.Uses) > 0 {
		b.WriteString("\n")
	}

	// Mount regular routes (sorted for deterministic output)
	for _, res := range sortedKeys2(routeResources) {
		b.WriteString(fmt.Sprintf("app.route('/', %sRoutes);\n", toCamelCase(res)))
	}

	// Mount stream routes
	for _, res := range sortedKeys2(streamResources) {
		fk := fileKeyForStream(res)
		b.WriteString(fmt.Sprintf("app.route('/', %sRoutes);\n", toCamelCase(fk)))
	}

	// Mount WS routes
	for _, res := range sortedKeys2(wsResources) {
		fk := fileKeyForWs(res)
		b.WriteString(fmt.Sprintf("app.route('/', %sRoutes);\n", toCamelCase(fk)))
	}

	b.WriteString("\n")

	// Register event subscriptions
	if len(subscribes) > 0 {
		b.WriteString("// Register event subscriptions\n")
		for _, sub := range subscribes {
			handlerName := "on" + toPascalCase(strings.ReplaceAll(sub.Event, ".", "_"))
			b.WriteString(fmt.Sprintf("on('%s', %s);\n", sub.Event, handlerName))
		}
		b.WriteString("\n")
	}

	// Start workers
	if len(workers) > 0 {
		b.WriteString("// Start workers\n")
		for _, w := range workers {
			name := toCamelCase(w.Name)
			queueName := w.Name
			b.WriteString(fmt.Sprintf("new Worker('%s', async (job) => { await %s(job.data); }, {\n", queueName, name))
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
			b.WriteString(fmt.Sprintf("schedulerQueue.add('%s', {}, { repeat: { pattern: %s } });\n", s.Name, cronName))
		}
		b.WriteString("\n")
	}

	// Health check endpoint
	b.WriteString("app.get('/health', (c) => c.json({ status: 'ok', uptime: process.uptime() }));\n\n")

	// Global error handler — suppress internal details
	b.WriteString("app.onError((err, c) => {\n")
	b.WriteString("  console.error(err);\n")
	b.WriteString("  return c.json({ error: 'Internal server error' }, 500);\n")
	b.WriteString("});\n\n")

	b.WriteString(fmt.Sprintf(`console.log('%%s listening on port %%d', '%s', %d);`+"\n", bp.Name, port))

	// If WS: use injectWebSocket(serve(...)), otherwise capture server ref for graceful shutdown
	if len(ws) > 0 {
		b.WriteString(fmt.Sprintf("const server = serve({ fetch: app.fetch, port: %d });\n", port))
		b.WriteString("injectWebSocket(server);\n\n")
	} else {
		b.WriteString(fmt.Sprintf("const server = serve({ fetch: app.fetch, port: %d });\n\n", port))
	}

	// Graceful shutdown
	b.WriteString("// Graceful shutdown\n")
	b.WriteString("const shutdown = async () => {\n")
	b.WriteString("  console.log('Shutting down gracefully...');\n")
	b.WriteString("  server.close();\n")
	if hasDB {
		b.WriteString("  // Close database pool\n")
		b.WriteString("  await db.$client.end();\n")
	}
	b.WriteString("  process.exit(0);\n")
	b.WriteString("};\n")
	b.WriteString("process.on('SIGTERM', shutdown);\n")
	b.WriteString("process.on('SIGINT', shutdown);\n\n")

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
		b.WriteString(fmt.Sprintf("import { %s } from '../validation/schemas.js';\n",
			strings.Join(names, ", ")))
	}
	// Import middleware
	for _, mwName := range sortedKeys2(middlewareNames) {
		b.WriteString(fmt.Sprintf("import { %s } from '../middleware/%s.js';\n",
			toCamelCase(mwName), toKebabCase(mwName)))
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
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(resource) + "Routes"
	b.WriteString(fmt.Sprintf("export const %s = new Hono();\n\n", routeVar))

	// Module-level rate limit store (shared across all handlers in this file)
	if needsRateLimit {
		b.WriteString("// Module-level rate limit store\n")
		b.WriteString("const _rateLimitStore = new Map<string, { count: number, resetAt: number }>();\n\n")
	}

	for _, ep := range endpoints {
		method := strings.ToLower(ep.Method)
		b.WriteString(fmt.Sprintf("// %s %s\n", ep.Method, ep.Path))
		if ep.Intent != nil {
			b.WriteString(fmt.Sprintf("// %s\n", ep.Intent.Text))
		}

		// Build handler: routeVar.method('path', ...middleware, zValidator, async (c) => { ... })
		b.WriteString(fmt.Sprintf("%s.%s('%s'", routeVar, method, ep.Path))

		// Add middleware
		for _, meta := range ep.Meta {
			if meta.Kind == "use" && meta.Use != nil {
				b.WriteString(fmt.Sprintf(", %s", toCamelCase(meta.Use.Name)))
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
			b.WriteString(fmt.Sprintf(", zValidator(%s, %s)", source, schemaName))
		}

		b.WriteString(", async (c) => {\n")

		// Emit meta as comments for rate limit, cache; webhook auth as verification code
		for _, meta := range ep.Meta {
			switch meta.Kind {
			case "limit":
				rateStr := exprToJS(meta.Value)
				rateLimit, rateWindowMS := parseRateLimit(rateStr)
				b.WriteString(fmt.Sprintf("  // rate limit: %s\n", rateStr))
				// rate limit store is at module scope (see needsRateLimit above)
				b.WriteString("  const _clientIp = c.req.header('x-forwarded-for') || 'unknown';\n")
				b.WriteString("  const _now = Date.now();\n")
				b.WriteString("  const _rateKey = `${_clientIp}:${c.req.path}`;\n")
				b.WriteString("  const _rateEntry = _rateLimitStore.get(_rateKey);\n")
				b.WriteString("  if (_rateEntry && _rateEntry.resetAt > _now) {\n")
				b.WriteString(fmt.Sprintf("    if (_rateEntry.count >= %d) {\n", rateLimit))
				b.WriteString("      return c.json({ error: 'Rate limit exceeded' }, 429);\n")
				b.WriteString("    }\n")
				b.WriteString("    _rateEntry.count++;\n")
				b.WriteString("  } else {\n")
				b.WriteString(fmt.Sprintf("    _rateLimitStore.set(_rateKey, { count: 1, resetAt: _now + %d });\n", rateWindowMS))
				b.WriteString("  }\n")
			case "cache":
				b.WriteString(fmt.Sprintf("  // cache: %s\n", exprToJS(meta.Value)))
			case "auth":
				if secretKey := webhookAuthSecretKey(meta.Value); secretKey != "" {
					b.WriteString(fmt.Sprintf("  const _payload = await c.req.text();\n"))
					b.WriteString(fmt.Sprintf("  const _sig = c.req.header('X-Webhook-Signature') ?? '';\n"))
					b.WriteString(fmt.Sprintf("  const _expected = createHmac('sha256', process.env.%s!).update(_payload).digest('hex');\n", secretKey))
					b.WriteString("  if (_sig.length !== _expected.length || !timingSafeEqual(Buffer.from(_sig, 'hex'), Buffer.from(_expected, 'hex'))) return c.json({ error: 'Invalid signature' }, 401);\n")
					b.WriteString("  let data: any;\n")
					b.WriteString("  try { data = JSON.parse(_payload); } catch { return c.json({ error: 'Invalid payload' }, 400); }\n")
				}
			}
		}

		b.WriteString("  try {\n")

		// Build context vars from middleware inject statements
		ctxVars := make(map[string]bool)
		boundVars := make(map[string]string)
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
					// e.g., api_key -> auth (from "inject key as auth" where key was queried from api_key)
					for modelName, ctxName := range extractInjectedModelMap(mw.Before) {
						boundVars[modelName] = "c.get('" + toCamelCase(ctxName) + "')"
					}
					for modelName, ctxName := range extractInjectedModelMap(mw.After) {
						boundVars[modelName] = "c.get('" + toCamelCase(ctxName) + "')"
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
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(fileKey) + "Routes"
	b.WriteString(fmt.Sprintf("export const %s = new Hono();\n\n", routeVar))

	for _, ep := range endpoints {
		b.WriteString(fmt.Sprintf("// STREAM %s\n", ep.Path))
		if ep.Intent != nil {
			b.WriteString(fmt.Sprintf("// %s\n", ep.Intent.Text))
		}

		b.WriteString(fmt.Sprintf("%s.get('%s', async (c) => {\n", routeVar, ep.Path))
		b.WriteString("  return streamSSE(c, async (stream) => {\n")

		ctx := emitCtx{
			kind:        "function",
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}

		// Emit setup statements
		if len(ep.Stmts) > 0 {
			g.emitArrowStmts(&b, ep.Stmts, "    ", ctx)
		}

		// Emit each handler block
		for _, h := range ep.Handlers {
			if h.EventName != "" {
				b.WriteString(fmt.Sprintf("    // on event: %s\n", h.EventName))
			}
			if h.Condition != nil {
				cond := exprToJSWithCtx(h.Condition, &ctx)
				b.WriteString(fmt.Sprintf("    if (%s) {\n", cond))

				handlerCtx := ctx
				handlerCtx.declared = make(map[string]bool)
				for k, v := range ctx.declared {
					handlerCtx.declared[k] = v
				}
				// Emit the handler body statements
				g.emitArrowStmts(&b, h.Body, "      ", handlerCtx)
				// Emit a stream.writeSSE call for the event
				b.WriteString(fmt.Sprintf("      await stream.writeSSE({ event: '%s', data: JSON.stringify({}) });\n", h.EventName))
				b.WriteString("    }\n")
			} else {
				// Emit the handler body statements
				g.emitArrowStmts(&b, h.Body, "    ", ctx)
				// Emit stream.writeSSE call
				b.WriteString(fmt.Sprintf("    await stream.writeSSE({ event: '%s', data: JSON.stringify({}) });\n", h.EventName))
			}
		}

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
	b.WriteString("import { upgradeWebSocket } from 'hono/ws';\n")

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
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	routeVar := toCamelCase(fileKey) + "Routes"
	b.WriteString(fmt.Sprintf("export const %s = new Hono();\n\n", routeVar))

	for _, ep := range endpoints {
		b.WriteString(fmt.Sprintf("// WS %s\n", ep.Path))
		if ep.Intent != nil {
			b.WriteString(fmt.Sprintf("// %s\n", ep.Intent.Text))
		}

		b.WriteString(fmt.Sprintf("%s.get('%s', upgradeWebSocket((c) => ({\n", routeVar, ep.Path))

		ctx := emitCtx{
			kind:        "function",
			declared:    make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}

		// onOpen handler
		b.WriteString("  onOpen(event, ws) {\n")
		if len(ep.OnConnect) > 0 {
			onConnectCtx := ctx
			onConnectCtx.declared = make(map[string]bool)
			g.emitArrowStmts(&b, ep.OnConnect, "    ", onConnectCtx)
		}
		b.WriteString("  },\n")

		// onMessage handler
		b.WriteString("  onMessage(event, ws) {\n")
		b.WriteString("    const message = event.data;\n")
		if len(ep.OnMessage) > 0 {
			onMessageCtx := ctx
			onMessageCtx.declared = make(map[string]bool)
			onMessageCtx.declared["message"] = true
			g.emitArrowStmts(&b, ep.OnMessage, "    ", onMessageCtx)
		}
		b.WriteString("  },\n")

		// onClose handler
		b.WriteString("  onClose(event, ws) {\n")
		if len(ep.OnDisconnect) > 0 {
			onDisconnectCtx := ctx
			onDisconnectCtx.declared = make(map[string]bool)
			g.emitArrowStmts(&b, ep.OnDisconnect, "    ", onDisconnectCtx)
		}
		b.WriteString("  },\n")

		b.WriteString("}))));\n\n")
	}

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/routes/%s.ts", toKebabCase(fileKey)),
		Content: []byte(b.String()),
	}
}
