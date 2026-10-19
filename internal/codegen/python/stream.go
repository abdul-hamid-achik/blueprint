package python

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

// STREAM (Server-Sent Events) endpoints on the Python target.
//
// Shape of one generated endpoint (see the Node target's genStreamRoute):
//
//   - Pre-stream statements (inputs are validated by FastAPI, then fetch,
//     query, guard, log, ...) run BEFORE the response starts. They run in a
//     sync helper on a short-lived SQLAlchemy session, off the event loop, so a
//     long stream never holds a pooled connection. A guard raises StreamError,
//     which answers {"error": ...} with its status, not a stream.
//   - The handler registers one Stream (src/lib/events.py) with one callback per
//     `on event(...)` and one timer per `on timeout(...)`, then returns an
//     EventStream response. The callbacks filter (`where`) and render the frame
//     data; the Stream owns the bounded queue, backpressure and cleanup.

// streamAsEndpoint views a STREAM endpoint as a GET endpoint, so the REST helpers
// that compute imports, auth and rate limits apply to it unchanged.
func streamAsEndpoint(se *ast.StreamEndpoint) *ast.Endpoint {
	return &ast.Endpoint{Loc: se.Loc, Intent: se.Intent, Method: "GET", Path: se.Path, Meta: se.Meta, Stmts: se.Stmts}
}

// streamInputs returns the path and query inputs declared before the stream block.
func streamInputs(se *ast.StreamEndpoint) []*ast.InputStmt {
	var inputs []*ast.InputStmt
	for _, s := range se.Stmts {
		if in, ok := s.(*ast.InputStmt); ok {
			inputs = append(inputs, in)
		}
	}
	return inputs
}

// streamBindings lists the names bound by top-level pre-stream steps, in source order.
func streamBindings(se *ast.StreamEndpoint) []string {
	var out []string
	for _, s := range se.Stmts {
		if step, ok := s.(*ast.StepStmt); ok && step.Binding != "" {
			out = append(out, step.Binding)
		}
	}
	return out
}

// streamScope is the set of names a STREAM body may reference besides event
// fields: its inputs, its pre-stream bindings, the values its `use` middleware
// injects and, on jwt endpoints, claims.
func (g *Generator) streamScope(se *ast.StreamEndpoint) map[string]bool {
	scope := map[string]bool{}
	for _, in := range streamInputs(se) {
		scope[in.Name] = true
	}
	for _, name := range streamBindings(se) {
		scope[name] = true
	}
	for _, u := range g.useBindings(se.Meta) {
		scope[u.alias] = true
	}
	if endpointJwtClaims(se.Meta) {
		scope["claims"] = true
	}
	return scope
}

// genStreamRouteFile emits one src/routes/<resource>_stream.py per resource,
// holding every STREAM endpoint that maps to it. Multiple SSE endpoints on the
// same resource share a router.
func (g *Generator) genStreamRouteFile(resource string, ses []*ast.StreamEndpoint) codegen.OutputFile {
	module := pyModuleName(resource) + "_stream"
	var eps []*ast.Endpoint
	anyDB := false
	anyGuard := false
	anyUse, anyUseDB, anyUseHeaders, anyLimit := false, false, false, false
	for _, se := range ses {
		ep := streamAsEndpoint(se)
		eps = append(eps, ep)
		if stmtsTouchDB(se.Stmts) {
			anyDB = true
		}
		if endpointHasGuards(ep) {
			anyGuard = true
		}
		if _, _, ok := endpointRateLimit(ep); ok {
			anyLimit = true
		}
		for _, u := range g.useBindings(se.Meta) {
			anyUse = true
			anyUseDB = anyUseDB || u.touchesDB
			anyUseHeaders = anyUseHeaders || len(u.headers) > 0
		}
	}

	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	g.writeRouteImports(&b, eps)
	b.WriteString("from fastapi.concurrency import run_in_threadpool\n")
	if anyDB || anyUseDB {
		b.WriteString("from src.lib.db import SessionLocal\n")
	}
	// writeRouteImports imports HTTPException and Request only for guards and rate
	// limits; a `use` helper needs both on its own.
	if anyUse && !anyGuard {
		b.WriteString("from fastapi import HTTPException\n")
	}
	if anyUseHeaders && !anyLimit {
		b.WriteString("from fastapi import Request\n")
	}
	imports := []string{"MISSING", "EventStream", "Stream", "frame_data", "wire"}
	if anyGuard || anyUse {
		imports = append(imports, "StreamError")
	}
	fmt.Fprintf(&b, "from src.lib.events import %s\n", strings.Join(imports, ", "))
	b.WriteString("\nrouter = APIRouter()\n\n")

	for i, se := range ses {
		if i > 0 {
			b.WriteString("\n\n")
		}
		g.emitStreamEndpoint(&b, se)
	}
	return codegen.OutputFile{Path: "src/routes/" + module + ".py", Content: []byte(b.String())}
}

// emitStreamEndpoint writes one STREAM endpoint: its pre-stream helper (when it
// has statements), then the route handler.
func (g *Generator) emitStreamEndpoint(b *strings.Builder, se *ast.StreamEndpoint) {
	ep := streamAsEndpoint(se)
	fnName := streamHandlerName(se.Path)
	inputs := streamInputs(se)
	var inputNames []string
	for _, in := range inputs {
		inputNames = append(inputNames, in.Name)
	}
	ea, hasEnforced := endpointEnforcedAuthMeta(se.Meta)
	jwt := hasEnforced && ea.Strategy == "jwt"
	scope := g.streamScope(se)
	uses := g.useBindings(se.Meta)

	// Pre-stream helper: a sync function on its own session, run in a threadpool.
	ctx := g.newBodyCtx(ep)
	ctx.streamPre = true
	var helperArgs []string
	helperArgs = append(helperArgs, inputNames...)
	if jwt {
		helperArgs = append(helperArgs, "claims")
	}
	// `use` values are computed before the pre-stream helper and passed into it.
	for _, u := range uses {
		helperArgs = append(helperArgs, u.alias)
	}
	var pre strings.Builder
	for i, u := range uses {
		writeUseHelper(&pre, fmt.Sprintf("_use_%s_%d", fnName, i), u, "StreamError")
	}
	var preNames []string
	if hasPreStatements(se) {
		preNames = streamBindings(se)
		g.writePreStreamHelper(&pre, fnName, helperArgs, se, ctx)
		for _, fk := range sortedAliasValues(ctx.fkAliases) {
			preNames = append(preNames, fk)
		}
	}

	// Handler signature: claims (jwt) and inputs, as on REST.
	params := g.signatureParams("GET", se.Path, inputs)
	if jwt {
		claimsParam := "claims: Annotated[dict[str, Any], " + streamAuthDependency(ea, g.blueprintNameForAuth()) + "]"
		if params != "" {
			params = claimsParam + ", " + params
		} else {
			params = claimsParam
		}
	}
	limitPreamble := ""
	limit, window, hasLimit := endpointRateLimit(ep)
	readsHeaders := false
	for _, u := range uses {
		if len(u.headers) > 0 {
			readsHeaders = true
		}
	}
	if hasLimit || readsHeaders {
		if params != "" {
			params = "bp_request: Request, " + params
		} else {
			params = "bp_request: Request"
		}
	}
	if hasLimit {
		limitPreamble = fmt.Sprintf(
			"    if (_limited := rate_limit.check(bp_request, %d, %d)) is not None:\n        return _limited\n",
			limit, window)
	}

	var decoArgs []string
	if tags := endpointTags(ep); len(tags) > 0 {
		quoted := make([]string, len(tags))
		for i, tag := range tags {
			quoted[i] = fmt.Sprintf("%q", tag)
		}
		decoArgs = append(decoArgs, fmt.Sprintf("tags=[%s]", strings.Join(quoted, ", ")))
	}
	if hasEnforced {
		if !jwt {
			decoArgs = append(decoArgs, fmt.Sprintf("dependencies=[%s]", streamAuthDependency(ea, g.blueprintNameForAuth())))
		}
		decoArgs = append(decoArgs, `responses={401: {"description": "Unauthorized"}}`)
	}
	suffix := ""
	if len(decoArgs) > 0 {
		suffix = ", " + strings.Join(decoArgs, ", ")
	}

	b.WriteString(pre.String())
	if pre.Len() > 0 {
		b.WriteString("\n")
	}
	if se.Intent != nil {
		fmt.Fprintf(b, "# %s\n", se.Intent.Text)
	}
	fmt.Fprintf(b, "@router.get(%q%s)\n", pathBpToFastapi(se.Path), suffix)
	fmt.Fprintf(b, "async def %s(%s):\n", fnName, params)
	// `use` middleware runs first, as on Node, so a refused request never reaches
	// the rate limit or the pre-stream statements.
	for i, u := range uses {
		call := []string{fmt.Sprintf("_use_%s_%d", fnName, i)}
		for _, h := range u.headers {
			call = append(call, fmt.Sprintf("bp_request.headers.get(%q)", h))
		}
		fmt.Fprintf(b, "    %s = await run_in_threadpool(%s)\n", u.alias, strings.Join(call, ", "))
	}
	b.WriteString(limitPreamble)

	if hasPreStatements(se) {
		fmt.Fprintf(b, "    _pre = await run_in_threadpool(%s)\n", strings.Join(append([]string{"_pre_" + fnName}, helperArgs...), ", "))
		for _, name := range preNames {
			fmt.Fprintf(b, "    %s = _pre[%q]\n", name, name)
		}
	}

	// Callbacks: one per `on event`, one per `on timeout`. They read the
	// pre-stream bindings and inputs from this closure.
	handlerCtx := *ctx
	handlerCtx.streamPre = false
	var registrations []string
	var timers []string
	var callbacks strings.Builder
	for i, h := range se.Handlers {
		if h.Timeout != "" {
			ms, _ := streamDurationMS(h.Timeout)
			out := streamOutputOf(h)
			fmt.Fprintf(&callbacks, "    def _tick_%d():\n", i)
			fmt.Fprintf(&callbacks, "        return frame_data(%s)\n\n", streamOutputPy(out, &handlerCtx, scope, false))
			timers = append(timers, fmt.Sprintf("    stream.every(\"ping\", %s, _tick_%d)\n",
				strconv.FormatFloat(ms/1000, 'f', -1, 64), i))
			continue
		}
		fmt.Fprintf(&callbacks, "    def _on_event_%d(event):\n", i)
		out := streamOutputOf(h)
		value := "frame_data(" + streamOutputPy(out, &handlerCtx, scope, false) + ")"
		if h.Condition != nil {
			cond := streamExprPy(h.Condition, &handlerCtx, scope, true)
			fmt.Fprintf(&callbacks, "        if not (%s):\n            return None\n", cond)
		}
		fmt.Fprintf(&callbacks, "        return (%s, %s)\n\n", pyStringLiteral(h.EventName), value)
		registrations = append(registrations, fmt.Sprintf("    stream.on_event(%s, _on_event_%d)\n", pyStringLiteral(h.EventName), i))
	}
	b.WriteString(callbacks.String())
	fmt.Fprintf(b, "    stream = Stream(%s)\n", pyStringLiteral(se.Path))
	for _, r := range registrations {
		b.WriteString(r)
	}
	for _, t := range timers {
		b.WriteString(t)
	}
	b.WriteString("    stream.start()\n")
	b.WriteString("    return EventStream(stream)\n")
}

// hasPreStatements reports whether the stream has any statement before its
// stream block that needs running (anything beyond inputs and intent text).
func hasPreStatements(se *ast.StreamEndpoint) bool {
	for _, s := range se.Stmts {
		switch s.(type) {
		case *ast.StepStmt, *ast.GuardStmt:
			return true
		}
	}
	return false
}

// writePreStreamHelper emits the sync function that runs the pre-stream
// statements and returns their bindings. It opens its own session when the
// statements touch the database, and closes it before the response starts.
func (g *Generator) writePreStreamHelper(b *strings.Builder, fnName string, args []string, se *ast.StreamEndpoint, ctx *bodyCtx) {
	touchesDB := stmtsTouchDB(se.Stmts)
	fmt.Fprintf(b, "def _pre_%s(%s):\n", fnName, strings.Join(args, ", "))
	indent := "    "
	if touchesDB {
		b.WriteString("    db = SessionLocal()\n    try:\n")
		indent = "        "
	}
	for _, s := range se.Stmts {
		emitBodyStmt(b, s, ctx, indent)
	}
	names := streamBindings(se)
	for _, fk := range sortedAliasValues(ctx.fkAliases) {
		names = append(names, fk)
	}
	var entries []string
	for _, name := range names {
		entries = append(entries, fmt.Sprintf("%q: %s", name, name))
	}
	fmt.Fprintf(b, "%sreturn {%s}\n", indent, strings.Join(entries, ", "))
	if touchesDB {
		b.WriteString("    finally:\n        db.close()\n")
	}
	b.WriteString("\n\n")
}

// sortedAliasValues returns the Python aliases of FK sub-queries in a stable order.
func sortedAliasValues(aliases map[string]string) []string {
	out := make([]string, 0, len(aliases))
	for _, v := range aliases {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// streamOutputOf returns the single `-> { ... }` object a handler sends. The
// feature check guarantees the body has exactly one such output.
func streamOutputOf(h *ast.StreamHandler) *ast.BlockExpr {
	for _, s := range h.Body {
		if out, ok := s.(*ast.OutputStmt); ok {
			if blk, ok := out.Value.(*ast.BlockExpr); ok {
				return blk
			}
		}
	}
	return &ast.BlockExpr{}
}

// streamOutputPy renders a handler's object literal. Keys are camel-cased as on
// Node (room_id -> "roomId"). Direct event-field entries use MISSING as the
// default, so an absent field is dropped from the frame, as JSON.stringify
// drops undefined on Node.
func streamOutputPy(blk *ast.BlockExpr, ctx *bodyCtx, scope map[string]bool, where bool) string {
	if len(blk.Entries) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(blk.Entries))
	for _, kv := range blk.Entries {
		parts = append(parts, fmt.Sprintf("%q: %s", common.CamelCase(kv.Key), streamExprPy(kv.Value, ctx, scope, where)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// streamExprPy renders one expression inside a STREAM handler. where marks a
// condition: there, a bare identifier that is not in scope is an event field
// (as on Node), and values read from scope are passed through wire() so they
// compare in the JSON form emit payloads arrive in.
func streamExprPy(e ast.Expr, ctx *bodyCtx, scope map[string]bool, where bool) string {
	switch v := e.(type) {
	case *ast.BinaryExpr:
		return "(" + streamExprPy(v.Left, ctx, scope, where) + " " + translateBinaryOp(v.Op) + " " + streamExprPy(v.Right, ctx, scope, where) + ")"
	case *ast.UnaryExpr:
		return "(" + translateUnaryOp(v.Op) + " " + streamExprPy(v.Operand, ctx, scope, where) + ")"
	case *ast.ParenExpr:
		return "(" + streamExprPy(v.Expr, ctx, scope, where) + ")"
	case *ast.Ident:
		if scope[v.Name] {
			if where {
				return "wire(" + v.Name + ")"
			}
			return v.Name
		}
		return "event.get(" + pyStringLiteral(common.CamelCase(v.Name)) + ", MISSING)"
	case *ast.FieldAccess:
		if base, ok := v.Base.(*ast.Ident); ok && base.Name == "event" {
			return "event.get(" + pyStringLiteral(common.CamelCase(v.Field)) + ", MISSING)"
		}
		if isFieldChainRootedAt(v, "event") {
			return eventFieldChainPy(v)
		}
		if ctx.jwtClaims && isClaimsAccess(v) {
			return claimsFieldPy(v)
		}
		if where {
			return "wire(" + exprToPyWithCtx(v, ctx) + ")"
		}
		return exprToPyWithCtx(v, ctx)
	case *ast.ListExpr:
		parts := make([]string, 0, len(v.Elements))
		for _, el := range v.Elements {
			parts = append(parts, streamExprPy(el, ctx, scope, where))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.BlockExpr:
		// A nested object drops its absent fields too, as JSON.stringify does on Node.
		return "frame_data(" + streamOutputPy(v, ctx, scope, where) + ")"
	default:
		return exprToPyWithCtx(e, ctx)
	}
}

// eventFieldChainPy renders a field chain rooted at event (event.a.b) as .get
// lookups on camel-cased keys, the way Node reads eventData.a.b. Nested access
// stays safe when an object field is missing.
func eventFieldChainPy(v *ast.FieldAccess) string {
	return eventLookupPy(v, true)
}

// eventLookupPy renders one link of an event field chain. Only the outermost
// lookup defaults to MISSING, so an absent field is dropped from the frame.
func eventLookupPy(v *ast.FieldAccess, outer bool) string {
	base := "event"
	if inner, ok := v.Base.(*ast.FieldAccess); ok {
		base = "(" + eventLookupPy(inner, false) + " or {})"
	}
	def := ""
	if outer {
		def = ", MISSING"
	}
	return base + ".get(" + pyStringLiteral(common.CamelCase(v.Field)) + def + ")"
}

// streamExprFeatures reports the expression forms a STREAM handler cannot
// translate faithfully: calls, headers and env, names that are neither in scope
// nor event fields (outside a where condition), and anything pythonExpressionFeatures
// already rejects.
func (g *Generator) streamExprFeatures(e ast.Expr, scope map[string]bool, where bool) []string {
	var out []string
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch v := e.(type) {
		case nil:
		case *ast.FnCall:
			out = append(out, fmt.Sprintf("STREAM handler call to %q", v.Name))
		case *ast.Ident:
			if v.Name == "event" {
				out = append(out, "bare `event` in a STREAM handler (use event.<field>)")
			} else if !scope[v.Name] && !where {
				out = append(out, fmt.Sprintf("STREAM handler name %q that is not an input, a bound step, or an event field", v.Name))
			}
		case *ast.FieldAccess:
			root := v
			for {
				next, ok := root.Base.(*ast.FieldAccess)
				if !ok {
					break
				}
				root = next
			}
			if base, ok := root.Base.(*ast.Ident); ok {
				switch {
				case base.Name == "header" || base.Name == "env":
					out = append(out, fmt.Sprintf("%s references in a STREAM handler", base.Name))
				case base.Name == "event" || scope[base.Name]:
				case base.Name == "claims":
					out = append(out, "`claims` in a STREAM handler of a non-jwt endpoint")
				default:
					out = append(out, fmt.Sprintf("nested event field %q in a STREAM handler (use event.%s)", base.Name, base.Name))
				}
			}
			if !isFieldChainRootedAt(v, "event") && !isClaimsAccess(v) {
				walk(v.Base)
			}
		case *ast.BinaryExpr:
			walk(v.Left)
			walk(v.Right)
		case *ast.UnaryExpr:
			walk(v.Operand)
		case *ast.ParenExpr:
			walk(v.Expr)
		case *ast.ListExpr:
			for _, el := range v.Elements {
				walk(el)
			}
		case *ast.BlockExpr:
			for _, kv := range v.Entries {
				walk(kv.Value)
			}
		}
	}
	walk(e)
	out = append(out, g.pythonExpressionFeatures(e)...)
	return out
}

// streamFeatures returns the reasons a STREAM endpoint is rejected. An empty
// result means the endpoint is translated faithfully. Anything not listed here
// is supported; WS endpoints stay rejected in generator.go.
func (g *Generator) streamFeatures(se *ast.StreamEndpoint) []string {
	seen := map[string]bool{}
	add := func(reason string) {
		if reason != "" {
			seen[reason] = true
		}
	}
	for _, m := range se.Meta {
		switch m.Kind {
		case "auth":
			if _, ok := endpointWebhookSecret(m.Value); ok {
				add("STREAM `auth webhook_sig` (webhook verification runs on POST endpoints only)")
				continue
			}
			add(pythonAuthFeature(m))
		case "tags", "limit", "use":
			// Validated with the REST metadata rules by complexEndpointFeatures below.
		default:
			add(fmt.Sprintf("STREAM endpoint metadata %q", m.Kind))
		}
	}
	ep := streamAsEndpoint(se)
	for _, r := range g.complexEndpointFeatures(ep) {
		add(r)
	}
	for _, u := range g.useBindings(se.Meta) {
		if !validPythonIdentifier(u.alias) {
			add(fmt.Sprintf("STREAM `use %s` that injects a name Python cannot use (%q)", u.name, u.alias))
		}
	}
	for _, s := range se.Stmts {
		switch s.(type) {
		case *ast.InputStmt, *ast.StepStmt, *ast.GuardStmt, *ast.IntentStep:
		default:
			add(fmt.Sprintf("STREAM statement %T before the stream block", s))
		}
	}
	scope := g.streamScope(se)
	for _, h := range se.Handlers {
		switch {
		case h.Timeout != "":
			if _, ok := streamDurationMS(h.Timeout); !ok {
				add(fmt.Sprintf("STREAM `on timeout(%s)` duration", h.Timeout))
			}
		case h.EventName == "":
			add("STREAM handler without an event or timeout")
		}
		if h.Condition != nil {
			for _, r := range g.streamExprFeatures(h.Condition, scope, true) {
				add(r)
			}
		}
		outputs := 0
		for _, s := range h.Body {
			out, ok := s.(*ast.OutputStmt)
			if !ok {
				add("STREAM handler statements other than `-> { ... }`")
				continue
			}
			outputs++
			if _, ok := out.Value.(*ast.BlockExpr); !ok {
				add("STREAM handler output that is not an object literal `-> { ... }`")
				continue
			}
			for _, r := range g.streamExprFeatures(out.Value, scope, false) {
				add(r)
			}
		}
		if outputs != 1 {
			add("STREAM handler without exactly one `-> { ... }` output")
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// streamDurationMS converts a .bp duration (5min, 30s, 500ms, 1h, 2d, or a bare
// number of milliseconds) to milliseconds, the way the Node target does.
// It reports false for zero, negative and unparseable values.
func streamDurationMS(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		ms     float64
	}{
		{"hours", 60 * 60 * 1000},
		{"hour", 60 * 60 * 1000},
		{"days", 24 * 60 * 60 * 1000},
		{"day", 24 * 60 * 60 * 1000},
		{"min", 60 * 1000},
		{"ms", 1},
		{"h", 60 * 60 * 1000},
		{"d", 24 * 60 * 60 * 1000},
		{"s", 1000},
	}
	for _, u := range units {
		if !strings.HasSuffix(s, u.suffix) {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
		if err != nil || n <= 0 {
			return 0, false
		}
		return n * u.ms, true
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// streamResource extracts the file-name component for a STREAM endpoint.
// Mirrors the JS target's grouping: `/api/rooms/:id/live` → "rooms".
func streamResource(path string) string {
	return common.ExtractResource(path)
}

// streamHandlerName builds a Python handler name for a STREAM endpoint.
// `/api/rooms/:id/live` → `stream_rooms_live`.
func streamHandlerName(path string) string {
	var segs []string
	for _, s := range strings.Split(path, "/") {
		if s == "" || s == "api" {
			continue
		}
		s = strings.TrimPrefix(s, ":")
		s = strings.ReplaceAll(s, "-", "_")
		segs = append(segs, common.SnakeCase(s))
	}
	if len(segs) == 0 {
		return "stream_root"
	}
	return "stream_" + strings.Join(segs, "_")
}

// withStreamEndpoints returns endpoints followed by the STREAM endpoints viewed as
// GET endpoints. Only file-level decisions (rate-limit and auth helpers, the
// app's exception handlers) use the combined list; route grouping does not.
func (g *Generator) withStreamEndpoints(endpoints []*ast.Endpoint) []*ast.Endpoint {
	out := append([]*ast.Endpoint{}, endpoints...)
	if g.file == nil {
		return out
	}
	for _, b := range g.file.Blocks {
		if se, ok := b.(*ast.StreamEndpoint); ok {
			out = append(out, streamAsEndpoint(se))
		}
	}
	return out
}
