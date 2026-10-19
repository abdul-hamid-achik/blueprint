package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

// WebSocket endpoints on the Python target, mirroring the Node WS route
// (src/routes/<resource>.ts with src/lib/ws-connections.ts).
//
// One WS endpoint becomes one route in src/routes/<resource>_ws.py plus three
// sync helpers, one per handler (on_connect, on_message, on_disconnect). Each
// helper opens its own short-lived SessionLocal() when its body touches the
// database, runs off the event loop via run_in_threadpool, and returns a
// WsResult: the ordered effects it produced and, for on_connect, the bindings
// that later handlers of the same connection read. Effects are applied on the
// event loop by src/lib/realtime.py. Frames, room operations and close are
// effects, so the helper never touches a socket or a room itself.
//
// The handshake runs before accept(), in Node's order: auth, then declared path
// inputs, then limit. A refusal is an HTTP response (send_denial_response), or a
// close before accept when the server cannot send one.

// wsAsEndpoint views a WS endpoint as a GET endpoint, so the REST helpers that
// compute the auth, rate-limit and database metadata apply to it unchanged.
func wsAsEndpoint(we *ast.WsEndpoint, stmts []ast.ArrowStmt) *ast.Endpoint {
	return &ast.Endpoint{Loc: we.Loc, Intent: we.Intent, Method: "GET", Path: we.Path, Meta: we.Meta, Stmts: stmts}
}

// withRealtimeEndpoints returns endpoints followed by the STREAM and WS
// endpoints viewed as GET endpoints. File-level decisions (rate-limit and auth
// helpers, the app's exception handlers) use this list; route grouping does not.
func (g *Generator) withRealtimeEndpoints(endpoints []*ast.Endpoint) []*ast.Endpoint {
	out := g.withStreamEndpoints(endpoints)
	if g.file == nil {
		return out
	}
	for _, b := range g.file.Blocks {
		if we, ok := b.(*ast.WsEndpoint); ok {
			out = append(out, wsAsEndpoint(we, nil))
		}
	}
	return out
}

// wsPlan holds what the route and the three helpers of one endpoint share.
type wsPlan struct {
	we        *ast.WsEndpoint
	name      string           // ws_rooms_id; the helpers are _<name>_connect, _message, _disconnect
	jwt       bool             // claims are available to every handler
	inputs    []*ast.InputStmt // declared path inputs, validated before the handshake
	pathNames []string         // every :param of the path, in order
	hoisted   []string         // on_connect bindings and inject names that later handlers read
	allStmts  []ast.ArrowStmt  // inputs and all three handlers, for the body context
	auth      *enforcedAuth    // set when the endpoint has enforced auth
	limit     [2]int           // rate limit (count, window ms) when set
	hasLimit  bool
	uses      []useBinding // `use` middleware, run in the handshake before the path inputs
	injected  []string     // the names those middleware inject, passed to every handler
	headers   []string     // request headers any handler reads (header.X), captured at the handshake
}

// kwInjected lists the keyword-only values every handler takes: the `use` values
// and, when a handler reads one, the captured request headers.
func (p wsPlan) kwInjected() []string {
	names := append([]string{}, p.injected...)
	if len(p.headers) > 0 {
		names = append(names, headersDictName)
	}
	return names
}

func (g *Generator) newWsPlan(we *ast.WsEndpoint) wsPlan {
	p := wsPlan{we: we, name: wsHandlerName(we.Path), pathNames: common.ExtractPathParams(we.Path)}
	p.jwt = endpointJwtClaims(we.Meta)
	p.uses = g.useBindings(we.Meta)
	for _, u := range p.uses {
		p.injected = append(p.injected, u.alias)
	}
	for _, s := range we.Stmts {
		if in, ok := s.(*ast.InputStmt); ok {
			p.inputs = append(p.inputs, in)
			p.allStmts = append(p.allStmts, in)
		}
	}
	if ea, ok := endpointEnforcedAuthMeta(we.Meta); ok {
		p.auth = &ea
	}
	ep := wsAsEndpoint(we, nil)
	if limit, window, ok := endpointRateLimit(ep); ok {
		p.limit = [2]int{limit, window}
		p.hasLimit = true
	}
	seen := map[string]bool{}
	for _, body := range [][]ast.ArrowStmt{we.OnConnect, we.OnMessage, we.OnDisconnect} {
		p.allStmts = append(p.allStmts, body...)
	}
	for _, name := range wsConnectBindings(we.OnConnect) {
		if !seen[name] {
			seen[name] = true
			p.hoisted = append(p.hoisted, name)
		}
	}
	p.headers = directFieldReads("header", p.allStmts)
	return p
}

// wsConnectBindings lists the names on_connect binds for the rest of the
// connection: step bindings (also inside when blocks) and inject aliases.
func wsConnectBindings(stmts []ast.ArrowStmt) []string {
	var out []string
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.StepStmt:
			if v.Binding != "" {
				out = append(out, v.Binding)
			}
			if fn, ok := v.Expr.(*ast.FnCall); ok && fn.Name == "inject" && len(fn.Args) >= 2 {
				if name := identOrStringName(fn.Args[1]); name != "" {
					out = append(out, name)
				}
			}
		case *ast.WhenStmt:
			out = append(out, wsConnectBindings(v.Body)...)
		}
	}
	return out
}

// pathArgs renders the path parameter names as Python arguments.
func (p wsPlan) pathArgs() []string {
	return append([]string{}, p.pathNames...)
}

// handlerArgs is the positional argument list every helper takes after its message.
func (p wsPlan) handlerArgs() []string {
	args := p.pathArgs()
	if p.jwt {
		args = append(args, "claims")
	}
	return args
}

// bindingsExpr is the dict a handler returns for the connection's bindings, or
// "None" for a handler that binds nothing that later handlers read.
func (p wsPlan) bindingsExpr(connect bool) string {
	if !connect || len(p.hoisted) == 0 {
		return "None"
	}
	parts := make([]string, len(p.hoisted))
	for i, name := range p.hoisted {
		parts[i] = fmt.Sprintf("%q: %s", name, name)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// genWsRouteFile emits one src/routes/<resource>_ws.py holding every WS
// endpoint that maps to the resource.
func (g *Generator) genWsRouteFile(resource string, wes []*ast.WsEndpoint) codegen.OutputFile {
	module := pyModuleName(resource) + "_ws"
	var eps []*ast.Endpoint
	needsDB, needsAuth, needsLimit, needsUUID, needsUse := false, false, false, false, false
	middlewares := map[string]bool{}
	for _, we := range wes {
		p := g.newWsPlan(we)
		if stmtsTouchDB(p.allStmts) {
			needsDB = true
		}
		if p.auth != nil {
			needsAuth = true
		}
		if p.hasLimit {
			needsLimit = true
		}
		for _, u := range p.uses {
			needsUse = true
			needsDB = needsDB || u.touchesDB
			middlewares[u.name] = true
		}
		for _, in := range p.inputs {
			if pt, ok := in.Type.(*ast.PrimitiveType); ok && pt.Name == "uuid" {
				needsUUID = true
			}
		}
		eps = append(eps, wsAsEndpoint(we, nil))
	}

	needsEnv := false
	fnCalls := map[string]bool{}
	for _, we := range wes {
		p := g.newWsPlan(we)
		needsEnv = needsEnv || readsDirect("env", p.allStmts)
		for _, n := range g.userFnCallsIn(p.allStmts) {
			fnCalls[n] = true
		}
	}
	var fnNames []string
	for n := range fnCalls {
		fnNames = append(fnNames, n)
	}
	sort.Strings(fnNames)

	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	if needsUUID {
		b.WriteString("import uuid\n")
	}
	b.WriteString("from datetime import datetime, timezone\n")
	b.WriteString("from fastapi import APIRouter, WebSocket, WebSocketDisconnect\n")
	if needsUse {
		b.WriteString("from fastapi import HTTPException\n")
	}
	b.WriteString("from fastapi.concurrency import run_in_threadpool\n")
	if needsAuth {
		b.WriteString("from src.lib.auth import authenticate\n")
	}
	if needsAuth || needsEnv {
		b.WriteString("from src.lib.env import env\n")
	}
	if needsDB {
		b.WriteString("from src.lib.db import SessionLocal\n")
		b.WriteString("from src.models import schema\n")
	}
	if needsLimit {
		b.WriteString("from src.lib import rate_limit\n")
	}
	mwNames := make([]string, 0, len(middlewares))
	for name := range middlewares {
		mwNames = append(mwNames, name)
	}
	sort.Strings(mwNames)
	for _, name := range mwNames {
		fmt.Fprintf(&b, "from src.middleware.%s import %s\n", name, name)
	}
	g.writeUserFnImports(&b, fnNames)
	b.WriteString("from src.lib.log import logger\n")
	b.WriteString("from src.lib.realtime import (\n")
	b.WriteString("    WsConnection, WsGuardError, WsResult, apply_effects, evict, js_length, js_str, js_truthy,\n")
	b.WriteString("    refuse, ws_dumps, ws_get, ws_parse,\n")
	b.WriteString(")\n")
	b.WriteString("\nrouter = APIRouter()\n")

	for _, we := range wes {
		p := g.newWsPlan(we)
		b.WriteString("\n\n")
		g.emitWsEndpoint(&b, p)
	}
	return codegen.OutputFile{Path: "src/routes/" + module + ".py", Content: []byte(b.String())}
}

// emitWsEndpoint writes one WS endpoint: its three helpers, then the route.
func (g *Generator) emitWsEndpoint(b *strings.Builder, p wsPlan) {
	we := p.we
	ep := wsAsEndpoint(we, p.allStmts)
	for i, u := range p.uses {
		writeUseHelper(b, fmt.Sprintf("_use_%s_%d", p.name, i), u, "WsGuardError")
	}
	if we.Intent != nil {
		fmt.Fprintf(b, "# %s\n", we.Intent.Text)
	}
	g.writeWsHelper(b, p, ep, "connect", we.OnConnect)
	b.WriteString("\n\n")
	g.writeWsHelper(b, p, ep, "message", we.OnMessage)
	b.WriteString("\n\n")
	g.writeWsHelper(b, p, ep, "disconnect", we.OnDisconnect)
	b.WriteString("\n\n")
	g.writeWsRoute(b, p)
}

// writeWsHelper emits one handler as a sync function that runs on a worker thread.
func (g *Generator) writeWsHelper(b *strings.Builder, p wsPlan, ep *ast.Endpoint, kind string, stmts []ast.ArrowStmt) {
	connect := kind == "connect"
	fn := "_" + p.name + "_" + kind
	var params []string
	if kind == "message" {
		params = append(params, "message")
	}
	params = append(params, p.handlerArgs()...)
	params = append(params, "connection")
	// Keyword-only: the `use` values and captured headers (every handler) and,
	// after connect, the on_connect bindings. Connect binds the hoisted names.
	kwNames := p.kwInjected()
	if !connect {
		kwNames = append(kwNames, p.hoisted...)
	}
	if len(kwNames) > 0 {
		params = append(params, "*")
		for _, name := range kwNames {
			params = append(params, name+"=None")
		}
	}
	fmt.Fprintf(b, "def %s(%s):\n", fn, strings.Join(params, ", "))
	b.WriteString("    _fx = []\n")
	if connect {
		for _, name := range p.hoisted {
			fmt.Fprintf(b, "    %s = None\n", name)
		}
	}
	touchesDB := stmtsTouchDB(stmts)
	if touchesDB {
		b.WriteString("    db = SessionLocal()\n")
	}
	b.WriteString("    try:\n")
	ctx := g.newBodyCtx(ep)
	ctx.ws = true
	ctx.wsReturn = p.bindingsExpr(connect)
	if len(p.headers) > 0 {
		ctx.headersVar = headersDictName
	}
	inner := "        "
	for _, s := range stmts {
		emitBodyStmt(b, s, ctx, inner)
	}
	fmt.Fprintf(b, "%sreturn WsResult(_fx, %s)\n", inner, p.bindingsExpr(connect))
	switch kind {
	case "connect":
		// A failed guard answers {"error": ...} and closes with 1008, as Node does.
		fmt.Fprintf(b, "    except WsGuardError as exc:\n")
		b.WriteString("        _fx.append((\"send\", ws_dumps({\"error\": exc.message})))\n")
		b.WriteString("        _fx.append((\"close\", 1008, exc.message[:123]))\n")
		fmt.Fprintf(b, "        return WsResult(_fx, %s)\n", p.bindingsExpr(true))
		b.WriteString("    except Exception:\n")
		fmt.Fprintf(b, "        logger.exception(%q)\n", "WS "+p.we.Path+": on_connect failed")
		fmt.Fprintf(b, "        return WsResult(_fx, %s)\n", p.bindingsExpr(true))
	case "message":
		b.WriteString("    except WsGuardError as exc:\n")
		b.WriteString("        _fx.append((\"send\", ws_dumps({\"error\": exc.message})))\n")
		b.WriteString("        return WsResult(_fx)\n")
		b.WriteString("    except Exception:\n")
		fmt.Fprintf(b, "        logger.exception(%q)\n", "WS "+p.we.Path+": on_message failed")
		b.WriteString("        _fx.append((\"send\", ws_dumps({\"error\": \"Internal error\"})))\n")
		b.WriteString("        return WsResult(_fx)\n")
	default:
		// on_disconnect has no socket left to answer; a failure is only logged, as on Node.
		b.WriteString("    except WsGuardError as exc:\n")
		fmt.Fprintf(b, "        logger.error(\"WS %s: on_disconnect guard failed: %%s\", exc.message)\n", p.we.Path)
		b.WriteString("        return WsResult(_fx)\n")
		b.WriteString("    except Exception:\n")
		fmt.Fprintf(b, "        logger.exception(%q)\n", "WS "+p.we.Path+": on_disconnect failed")
		b.WriteString("        return WsResult(_fx)\n")
	}
	if touchesDB {
		b.WriteString("    finally:\n        db.close()\n")
	}
}

// we2path is the endpoint's path as written in the .bp source, for log lines.
func we2path(p wsPlan) string {
	return p.we.Path
}

// writeWsRoute emits the route: auth, path inputs and limit before accept(),
// then the connection loop with the connect and disconnect handlers around it.
func (g *Generator) writeWsRoute(b *strings.Builder, p wsPlan) {
	we := p.we
	pathSig := ""
	for _, name := range p.pathNames {
		pathSig += ", " + name + ": str"
	}
	fmt.Fprintf(b, "@router.websocket(%q)\n", pathBpToFastapi(we.Path))
	fmt.Fprintf(b, "async def %s(websocket: WebSocket%s):\n", p.name, pathSig)

	if p.auth != nil {
		fmt.Fprintf(b, "    claims = authenticate(%q, getattr(env, %q, None), authorization=websocket.headers.get(\"authorization\"), api_key=websocket.headers.get(\"x-api-key\"), query_token=websocket.query_params.get(\"token\"), query_api_key=websocket.query_params.get(\"api_key\"))\n",
			p.auth.Strategy, p.auth.Secret)
		b.WriteString("    if claims is None:\n        await refuse(websocket, 401)\n        return\n")
	}
	// `use` middleware runs after auth and before the path inputs and the limit,
	// so a guard refuses the handshake with its own status and no body.
	if len(p.uses) > 0 {
		b.WriteString("    try:\n")
		for i, u := range p.uses {
			call := []string{fmt.Sprintf("_use_%s_%d", p.name, i)}
			for _, h := range u.headers {
				call = append(call, fmt.Sprintf("websocket.headers.get(%q)", h))
			}
			fmt.Fprintf(b, "        %s = await run_in_threadpool(%s)\n", u.alias, strings.Join(call, ", "))
		}
		b.WriteString("    except WsGuardError as exc:\n        await refuse(websocket, exc.status_code)\n        return\n")
	}
	for _, in := range p.inputs {
		if in.Name == "" || !common.IsPathParam(in.Name, we.Path) {
			continue
		}
		pt, ok := in.Type.(*ast.PrimitiveType)
		if !ok {
			continue
		}
		var conv string
		switch pt.Name {
		case "uuid":
			conv = "uuid.UUID(" + in.Name + ")"
		case "int":
			conv = "int(" + in.Name + ")"
		default:
			continue
		}
		fmt.Fprintf(b, "    try:\n        %s = %s\n    except ValueError:\n", in.Name, conv)
		b.WriteString("        await refuse(websocket, 400)\n        return\n")
	}
	if p.hasLimit {
		fmt.Fprintf(b, "    if rate_limit.check(websocket, %d, %d) is not None:\n        await refuse(websocket, 429)\n        return\n",
			p.limit[0], p.limit[1])
	}

	args := p.handlerArgs()
	argList := strings.Join(args, ", ")
	keywords := func(names []string) string {
		if len(names) == 0 {
			return ""
		}
		parts := make([]string, len(names))
		for i, name := range names {
			parts[i] = name + "=" + name
		}
		return ", " + strings.Join(parts, ", ")
	}
	// The `use` values reach every handler; the on_connect bindings reach the
	// handlers after connect.
	connectKW := keywords(p.kwInjected())
	kw := keywords(append(p.kwInjected(), p.hoisted...))
	connectArgs := argList
	if connectArgs != "" {
		connectArgs += ", "
	}
	connectArgs += "connection"

	// The headers are read from the handshake, before accept(), so every handler
	// sees the values of the request that opened the connection.
	if len(p.headers) > 0 {
		fmt.Fprintf(b, "    %s = %s\n", headersDictName, headerCapture("websocket", p.headers))
	}
	b.WriteString("    await websocket.accept()\n")
	b.WriteString("    connection = WsConnection(websocket)\n")
	b.WriteString("    connection.start()\n")
	for _, name := range p.hoisted {
		fmt.Fprintf(b, "    %s = None\n", name)
	}
	b.WriteString("    try:\n")
	fmt.Fprintf(b, "        result = await run_in_threadpool(_%s_connect, %s%s)\n", p.name, connectArgs, connectKW)
	for _, name := range p.hoisted {
		fmt.Fprintf(b, "        %s = result.bindings.get(%q)\n", name, name)
	}
	b.WriteString("        await apply_effects(connection, result)\n")
	b.WriteString("        while connection.open:\n")
	b.WriteString("            frame = await websocket.receive()\n")
	b.WriteString("            if frame[\"type\"] == \"websocket.disconnect\":\n                break\n")
	b.WriteString("            text = frame.get(\"text\")\n")
	b.WriteString("            if text is None:\n")
	b.WriteString("                message = b\"\"\n")
	b.WriteString("            else:\n")
	b.WriteString("                try:\n                    message = ws_parse(text)\n")
	b.WriteString("                except ValueError:\n")
	b.WriteString("                    connection.send_direct(ws_dumps({\"error\": \"Invalid JSON\"}))\n")
	b.WriteString("                    continue\n")
	msgArgs := "message"
	if argList != "" {
		msgArgs += ", " + argList
	}
	fmt.Fprintf(b, "            result = await run_in_threadpool(_%s_message, %s, connection%s)\n", p.name, msgArgs, kw)
	b.WriteString("            await apply_effects(connection, result)\n")
	b.WriteString("    except WebSocketDisconnect:\n        pass\n")
	b.WriteString("    finally:\n")
	b.WriteString("        evict(connection)\n")
	discArgs := argList
	if discArgs != "" {
		discArgs += ", "
	}
	discArgs += "connection"
	fmt.Fprintf(b, "        result = await run_in_threadpool(_%s_disconnect, %s%s)\n", p.name, discArgs, kw)
	b.WriteString("        await apply_effects(connection, result)\n")
	b.WriteString("        await connection.finish()\n")
}

// wsFeatures returns the reasons a WS endpoint cannot be translated faithfully.
// An empty result means every handler translates; anything else fails the build.
func (g *Generator) wsFeatures(we *ast.WsEndpoint) []string {
	seen := map[string]bool{}
	add := func(reason string) {
		if reason != "" {
			seen[reason] = true
		}
	}
	// Meta: enforced auth (no webhook_sig), limit, and `use` middleware, which
	// the REST rules validate (a generated middleware, no arguments or body).
	var restMeta []*ast.EndpointMeta
	for _, m := range we.Meta {
		switch m.Kind {
		case "auth":
			if _, ok := endpointWebhookSecret(m.Value); ok {
				add("WS `auth webhook_sig` (webhook verification runs on POST endpoints only)")
				continue
			}
			add(pythonAuthFeature(m))
		case "limit", "use":
			restMeta = append(restMeta, m)
		default:
			add(fmt.Sprintf("WS endpoint metadata %q", m.Kind))
		}
	}
	if len(restMeta) > 0 {
		view := &ast.Endpoint{Loc: we.Loc, Method: "GET", Path: we.Path, Meta: restMeta}
		for _, r := range g.complexEndpointFeatures(view) {
			add(r)
		}
	}

	pathParams := map[string]bool{}
	for _, name := range common.ExtractPathParams(we.Path) {
		pathParams[name] = true
		if !validPythonIdentifier(name) {
			add(fmt.Sprintf("WS path parameter %q that is not a valid Python identifier", name))
		}
		switch name {
		case "message", "connection", "claims", "websocket", "connection_id":
			add(fmt.Sprintf("WS path parameter %q (a name the handler reserves)", name))
		}
	}
	for _, s := range we.Stmts {
		in, ok := s.(*ast.InputStmt)
		if !ok {
			add(fmt.Sprintf("WS statement %T before the handlers", s))
			continue
		}
		if !common.IsPathParam(in.Name, we.Path) {
			add(fmt.Sprintf("WS input %q that is not a path parameter", in.Name))
			continue
		}
		pt, ok := in.Type.(*ast.PrimitiveType)
		if !ok || (pt.Name != "string" && pt.Name != "uuid" && pt.Name != "int") {
			add(fmt.Sprintf("WS path input %q with a type other than string, int or uuid", in.Name))
		}
		for _, c := range in.Constraints {
			if c.Kind != "required" {
				add(fmt.Sprintf("WS path input constraint %q", c.Kind))
			}
		}
	}

	bindingReserved := map[string]bool{"message": true, "connection": true, "claims": true, "exc": true, "result": true, "frame": true, "text": true, "db": true, "_fx": true, "websocket": true, headersDictName: true}
	for name := range pathParams {
		bindingReserved[name] = true
	}
	for _, name := range wsConnectBindings(we.OnConnect) {
		if !validPythonIdentifier(name) || bindingReserved[name] {
			add(fmt.Sprintf("WS binding %q (a name the handler reserves or Python cannot use)", name))
		}
	}
	for _, u := range g.useBindings(we.Meta) {
		if !validPythonIdentifier(u.alias) || bindingReserved[u.alias] {
			add(fmt.Sprintf("WS `use %s` that injects %q (a name the handler reserves or Python cannot use)", u.name, u.alias))
		}
	}
	for _, body := range []struct {
		name  string
		stmts []ast.ArrowStmt
	}{{"on_connect", we.OnConnect}, {"on_message", we.OnMessage}, {"on_disconnect", we.OnDisconnect}} {
		g.wsStmtsFeatures(body.stmts, body.name, add)
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// wsStmtsFeatures checks one handler body against the statements the WS
// translator handles. Anything else is reported, never emitted as a stub.
func (g *Generator) wsStmtsFeatures(stmts []ast.ArrowStmt, handler string, add func(string)) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.IntentStep:
		case *ast.GuardStmt:
			g.wsExprFeatures(v.Condition, handler, add)
		case *ast.WhenStmt:
			g.wsExprFeatures(v.Condition, handler, add)
			if v.Inline != nil {
				// The inline form is one realtime step under the if, checked as a step.
				g.wsStepFeatures(&ast.StepStmt{Loc: v.Loc, Expr: v.Inline}, handler, add)
			}
			g.wsStmtsFeatures(v.Body, handler, add)
		case *ast.OutputStmt:
			blk, ok := v.Value.(*ast.BlockExpr)
			if !ok {
				add(handler + ": WS output that is not an object literal `-> { ... }`")
				continue
			}
			for _, kv := range blk.Entries {
				g.wsExprFeatures(kv.Value, handler, add)
			}
		case *ast.StepStmt:
			g.wsStepFeatures(v, handler, add)
		case *ast.TryRecover:
			// Node's try has a catch that sees every failure; a failed try keeps the
			// frames it already sent, and rolls back its writes when it wraps them.
			g.wsStmtsFeatures(v.Try, handler, add)
			g.wsStmtsFeatures(v.Recover, handler, add)
		default:
			add(fmt.Sprintf("%s: WS statement %T", handler, s))
		}
	}
}

// wsStepFeatures checks one `|>` step inside a WS handler.
func (g *Generator) wsStepFeatures(s *ast.StepStmt, handler string, add func(string)) {
	switch v := s.Expr.(type) {
	case *ast.Ident:
		if v.Name == "close" && s.Binding == "" {
			return
		}
		add(fmt.Sprintf("%s: WS step %q", handler, v.Name))
		return
	case *ast.BlockExpr:
		if s.Binding == "" {
			add(handler + ": WS object step without a binding")
		}
		for _, kv := range v.Entries {
			g.wsExprFeatures(kv.Value, handler, add)
		}
		return
	case *ast.FnCall:
		switch v.Name {
		case "join", "leave":
			if len(v.Args) != 1 {
				add(fmt.Sprintf("%s: `%s` takes one room argument", handler, v.Name))
			} else {
				g.wsExprFeatures(wsTargetArg(v.Args[0]), handler, add)
			}
		case "broadcast":
			if len(v.Args) < 1 || len(v.Args) > 2 {
				add(handler + ": `broadcast` takes a room and an optional object")
			} else {
				g.wsExprFeatures(wsTargetArg(v.Args[0]), handler, add)
				if len(v.Args) == 2 {
					g.wsExprFeatures(v.Args[1], handler, add)
				}
			}
		case "whisper":
			if len(v.Args) != 2 {
				add(handler + ": `whisper` takes a connection id and an object")
			} else {
				g.wsExprFeatures(wsTargetArg(v.Args[0]), handler, add)
				g.wsExprFeatures(v.Args[1], handler, add)
			}
		case "emit":
			for _, r := range emitStepFeatures(v) {
				add(handler + ": " + r)
			}
			for _, a := range v.Args {
				g.wsExprFeatures(a, handler, add)
			}
		case "inject":
			if len(v.Args) != 2 || s.Binding != "" || !isIdentArg(v.Args[1]) {
				add(handler + ": `inject` takes a value and `as name`")
			}
			if len(v.Args) > 0 {
				g.wsExprFeatures(v.Args[0], handler, add)
			}
		case "log":
			for _, r := range g.complexStmtFeatures(s) {
				add(handler + ": " + r)
			}
		case "save", "fetch", "query", "update", "delete":
			for _, r := range g.complexStmtFeatures(s) {
				add(handler + ": " + r)
			}
			for _, a := range v.Args {
				g.wsExprFeatures(a, handler, add)
			}
		default:
			if g.userFnNames()[v.Name] {
				// A declared fn runs in the handler's worker thread, as a REST body calls it.
				for _, a := range v.Args {
					g.wsExprFeatures(a, handler, add)
				}
				return
			}
			add(fmt.Sprintf("%s: WS step %q", handler, v.Name))
		}
		return
	}
	add(fmt.Sprintf("%s: WS step %T", handler, s.Expr))
}

// wsTargetArg unwraps the `room(id)` or `connection(id)` target of a room or
// whisper step to its id, as the Node target does for any one-argument call.
func wsTargetArg(e ast.Expr) ast.Expr {
	if fn, ok := e.(*ast.FnCall); ok && len(fn.Args) == 1 {
		return fn.Args[0]
	}
	return e
}

func isIdentArg(e ast.Expr) bool {
	_, ok := e.(*ast.Ident)
	return ok
}

// wsExprFeatures reports value expressions a WS handler cannot read faithfully.
func (g *Generator) wsExprFeatures(e ast.Expr, handler string, add func(string)) {
	if e == nil {
		return
	}
	if reason := headerEnvReadFeature(e); reason != "" {
		add(handler + ": " + reason)
	}
	if fa, ok := e.(*ast.FieldAccess); ok {
		if fa.Field == "length" && !isFieldChainRootedAt(fa, "message") {
			add(handler + ": `.length` on a value that is not a message field")
		}
	}
	for _, r := range g.pythonExpressionFeatures(e) {
		add(handler + ": " + r)
	}
	switch v := e.(type) {
	case *ast.BinaryExpr:
		g.wsExprFeatures(v.Left, handler, add)
		g.wsExprFeatures(v.Right, handler, add)
	case *ast.UnaryExpr:
		g.wsExprFeatures(v.Operand, handler, add)
	case *ast.ParenExpr:
		g.wsExprFeatures(v.Expr, handler, add)
	case *ast.FieldAccess:
		if !isDirectHeaderEnvRead(v) {
			g.wsExprFeatures(v.Base, handler, add)
		}
	case *ast.ListExpr:
		for _, el := range v.Elements {
			g.wsExprFeatures(el, handler, add)
		}
	case *ast.BlockExpr:
		for _, kv := range v.Entries {
			g.wsExprFeatures(kv.Value, handler, add)
		}
	}
}

// wsMessageChainPy renders a field chain rooted at the client's message as
// ws_get lookups. The message is parsed JSON, so attribute access would fail
// on a dict; .length maps to js_length. Field names are camel-cased, as on
// Node (message.sent_by reads the "sentBy" key).
func wsMessageChainPy(v *ast.FieldAccess) string {
	base := "message"
	if inner, ok := v.Base.(*ast.FieldAccess); ok {
		base = wsMessageChainPy(inner)
	}
	if v.Field == "length" {
		return "js_length(" + base + ")"
	}
	return "ws_get(" + base + ", " + pyStringLiteral(common.CamelCase(v.Field)) + ")"
}

// emitWsStep translates the WS-only steps (join, leave, broadcast, whisper,
// emit, inject, close) into effects or assignments. It reports false for any
// other step, which then takes the ordinary path.
func emitWsStep(b *strings.Builder, s *ast.StepStmt, ctx *bodyCtx, indent string) bool {
	switch v := s.Expr.(type) {
	case *ast.Ident:
		if v.Name != "close" || s.Binding != "" {
			return false
		}
		fmt.Fprintf(b, "%s_fx.append((\"close\", 1000, \"\"))\n", indent)
		fmt.Fprintf(b, "%sreturn WsResult(_fx, %s)\n", indent, ctx.wsReturn)
		return true
	case *ast.FnCall:
		switch v.Name {
		case "join", "leave":
			if len(v.Args) != 1 {
				return false
			}
			fmt.Fprintf(b, "%s_fx.append((%q, js_str(%s)))\n", indent, v.Name, exprToPyWithCtx(wsTargetArg(v.Args[0]), ctx))
		case "broadcast":
			if len(v.Args) < 1 {
				return false
			}
			data := "\"{}\""
			if len(v.Args) >= 2 {
				data = "ws_dumps(" + exprToPyWithCtx(v.Args[1], eventPayloadCtx(ctx)) + ")"
			}
			fmt.Fprintf(b, "%s_fx.append((\"broadcast\", js_str(%s), %s))\n", indent, exprToPyWithCtx(wsTargetArg(v.Args[0]), ctx), data)
		case "whisper":
			if len(v.Args) != 2 {
				return false
			}
			fmt.Fprintf(b, "%s_fx.append((\"whisper\", %s, ws_dumps(%s)))\n", indent,
				exprToPyWithCtx(wsTargetArg(v.Args[0]), ctx), exprToPyWithCtx(v.Args[1], eventPayloadCtx(ctx)))
		case "emit":
			if len(v.Args) < 1 {
				return false
			}
			payload := "{}"
			if len(v.Args) >= 2 {
				if blk, ok := v.Args[1].(*ast.BlockExpr); ok {
					payload = blockExprToPyDictWithCtx(blk, ctx)
				}
			}
			fmt.Fprintf(b, "%s_fx.append((\"emit\", %s, %s))\n", indent, pyStringLiteral(identOrStringName(v.Args[0])), payload)
		case "inject":
			if len(v.Args) != 2 {
				return false
			}
			fmt.Fprintf(b, "%s%s = %s\n", indent, identOrStringName(v.Args[1]), exprToPyWithCtx(v.Args[0], ctx))
		default:
			return false
		}
		return true
	}
	return false
}

// wsResource extracts the file-name component for a WS endpoint. WS routes
// commonly live under `/ws/<resource>/...` rather than `/api/<resource>`,
// so we skip the `ws` prefix the same way ExtractResource skips `api`.
func wsResource(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, p := range parts {
		if p == "ws" || p == "api" || p == "" {
			continue
		}
		if strings.HasPrefix(p, ":") {
			continue
		}
		return p
	}
	return "root"
}

// wsHandlerName builds a Python handler name for a WS endpoint.
// `/ws/rooms/:id` → `ws_rooms_id`.
func wsHandlerName(path string) string {
	var segs []string
	for _, s := range strings.Split(path, "/") {
		if s == "" || s == "ws" || s == "api" {
			continue
		}
		s = strings.TrimPrefix(s, ":")
		s = strings.ReplaceAll(s, "-", "_")
		segs = append(segs, common.SnakeCase(s))
	}
	if len(segs) == 0 {
		return "ws_root"
	}
	return "ws_" + strings.Join(segs, "_")
}
