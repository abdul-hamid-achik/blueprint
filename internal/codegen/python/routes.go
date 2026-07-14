package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

// genRoute emits one src/routes/<resource>.py file with a FastAPI APIRouter
// holding every endpoint that maps to this resource. Imports are computed
// from the actual endpoint bodies — a static route file gets the lean Phase 1
// import set; a CRUD route file pulls in Session/Depends/HTTPException/select.
func (g *Generator) genRoute(resource string, eps []*ast.Endpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	g.writeRouteImports(&b, eps)
	b.WriteString("\nrouter = APIRouter()\n\n")

	for i, ep := range eps {
		if i > 0 {
			b.WriteString("\n")
		}
		g.emitEndpoint(&b, ep)
	}

	path := fmt.Sprintf("src/routes/%s.py", pyModuleName(resource))
	return codegen.OutputFile{Path: path, Content: []byte(b.String())}
}

// writeRouteImports emits exactly the imports the endpoints in this file
// actually use. Keeps emitted code idiomatic and lets static-only route files
// stay light. Also pulls in user fns + middleware modules that the endpoints
// dispatch to, plus `datetime`/`timezone` when any expression uses `now`.
func (g *Generator) writeRouteImports(b *strings.Builder, eps []*ast.Endpoint) {
	needsDB := false
	needsGuard := false
	needsPaginate := false
	needsEncoder := false
	needsNow := false
	needsTimedelta := false
	needsUUID := false
	needsBody := false
	needsQuery := false
	needsHeader := false
	needsEnv := false
	userFnCalls := map[string]bool{}
	middlewares := map[string]bool{}
	for _, ep := range eps {
		if endpointTouchesDB(ep) {
			needsDB = true
			needsEncoder = true
		}
		if endpointHasGuards(ep) {
			needsGuard = true
		}
		if endpointHasPaginatedQuery(ep) {
			needsPaginate = true
		}
		for _, s := range ep.Stmts {
			if input, ok := s.(*ast.InputStmt); ok {
				needsUUID = needsUUID || typeContainsPrimitive(input.Type, "uuid")
				needsNow = needsNow || typeContainsPrimitive(input.Type, "timestamp")
				if !common.IsPathParam(input.Name, ep.Path) {
					if strings.EqualFold(ep.Method, "GET") || strings.EqualFold(ep.Method, "DELETE") {
						needsQuery = true
					} else {
						needsBody = true
					}
				}
			}
			if outStmt, ok := s.(*ast.OutputStmt); ok {
				if _, isText := outStmt.Value.(*ast.StringLit); !isText && outStmt.Status != "204" {
					needsEncoder = true
				}
			}
		}
		// Collect every user fn the endpoint calls (anywhere in its stmts),
		walkEndpointExprs(ep, func(e ast.Expr) {
			if _, ok := e.(*ast.NowLit); ok {
				needsNow = true
			}
			// Duration expressions (N.days.ago, N.hours) need timedelta.
			if fa, ok := e.(*ast.FieldAccess); ok {
				if base, ok := fa.Base.(*ast.Ident); ok {
					if base.Name == "header" {
						needsHeader = true
					}
					if base.Name == "env" {
						needsEnv = true
					}
				}
				if fa.Field == "ago" || pyDurationField(fa.Field) != "" {
					needsTimedelta = true
					needsNow = true // ago uses datetime.now(timezone.utc)
				}
			}
			if fc, ok := e.(*ast.FnCall); ok && g.userFnNames()[fc.Name] {
				userFnCalls[fc.Name] = true
			}
		})
		for _, m := range ep.Meta {
			if m.Kind == "use" && m.Use != nil {
				middlewares[m.Use.Name] = true
			}
		}
	}
	// Middleware Depends always need Depends in fastapi import + a Session
	// will already be there if any endpoint also touches DB; if the only db
	// dep comes from a middleware, the endpoint signature still needs Depends.
	if len(middlewares) > 0 {
		needsDB = needsDB || g.middlewareTouchesDB(middlewares)
	}

	fastapiImports := []string{"APIRouter"}
	if needsBody {
		fastapiImports = append(fastapiImports, "Body")
	}
	if needsQuery {
		fastapiImports = append(fastapiImports, "Query")
	}
	if needsHeader {
		fastapiImports = append(fastapiImports, "Header")
	}
	if needsDB || needsGuard {
		fastapiImports = append(fastapiImports, "Depends")
	}
	if needsGuard {
		fastapiImports = append(fastapiImports, "HTTPException")
	}
	fmt.Fprintf(b, "from fastapi import %s\n", strings.Join(fastapiImports, ", "))
	b.WriteString("from fastapi.responses import JSONResponse, PlainTextResponse, Response\n")
	if needsEncoder {
		b.WriteString("from fastapi.encoders import jsonable_encoder\n")
	}
	if needsDB {
		sqlImports := []string{"select"}
		if needsPaginate {
			sqlImports = append(sqlImports, "func")
		}
		needsAnd := false
		needsOr := false
		for _, ep := range eps {
			if endpointHasMultiWhere(ep) {
				needsAnd = true
			}
			if endpointHasOrInWhere(ep) {
				needsOr = true
			}
		}
		if needsAnd {
			sqlImports = append(sqlImports, "and_")
		}
		if needsOr {
			sqlImports = append(sqlImports, "or_")
		}
		fmt.Fprintf(b, "from sqlalchemy import %s\n", strings.Join(sqlImports, ", "))
		b.WriteString("from sqlalchemy.orm import Session\n")
		b.WriteString("from src.lib.db import get_db\n")
		b.WriteString("from src.models import schema\n")
	}
	if needsPaginate {
		b.WriteString("from types import SimpleNamespace\n")
	}
	if needsNow {
		dtImports := []string{"datetime", "timezone"}
		if needsTimedelta {
			dtImports = append(dtImports, "timedelta")
		}
		fmt.Fprintf(b, "from datetime import %s\n", strings.Join(dtImports, ", "))
	}
	if needsUUID {
		b.WriteString("from uuid import UUID\n")
	}
	if needsEnv {
		b.WriteString("from src.lib.env import env\n")
	}
	// `use <middleware>` requires Depends + the middleware module import.
	if len(middlewares) > 0 {
		if !needsDB && !needsGuard {
			// fastapi import line didn't include Depends yet; add a separate line.
			b.WriteString("from fastapi import Depends\n")
		}
		names := make([]string, 0, len(middlewares))
		for n := range middlewares {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(b, "from src.middleware.%s import %s\n", n, n)
		}
	}
	// User fn imports.
	if len(userFnCalls) > 0 {
		names := make([]string, 0, len(userFnCalls))
		for n := range userFnCalls {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(b, "from src.functions.%s import %s\n", n, n)
		}
	}
}

func typeContainsPrimitive(t ast.TypeExpr, name string) bool {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		return v.Name == name
	case *ast.TypedJSONType:
		return typeContainsPrimitive(v.Inner, name)
	case *ast.ListType:
		return typeContainsPrimitive(v.Element, name)
	case *ast.MapType:
		return typeContainsPrimitive(v.Key, name) || typeContainsPrimitive(v.Value, name)
	default:
		return false
	}
}

// middlewareTouchesDB reports whether any of the named middlewares contains a
// data-op step — used to know if endpoints that `use` them still need the DB
// session imports even when their own body doesn't touch the DB.
func (g *Generator) middlewareTouchesDB(names map[string]bool) bool {
	for _, mw := range g.middlewares() {
		if !names[mw.Name] {
			continue
		}
		for _, s := range mw.Before {
			step, ok := s.(*ast.StepStmt)
			if !ok {
				continue
			}
			fn, ok := step.Expr.(*ast.FnCall)
			if !ok {
				continue
			}
			switch fn.Name {
			case "save", "fetch", "query", "update", "delete":
				return true
			}
		}
	}
	return false
}

// walkEndpointExprs visits every expression in an endpoint (across nested
// when/try bodies). Used by import detection.
func walkEndpointExprs(ep *ast.Endpoint, visit func(ast.Expr)) {
	var walkStmts func(stmts []ast.ArrowStmt)
	var walkExpr func(e ast.Expr)
	walkExpr = func(e ast.Expr) {
		if e == nil {
			return
		}
		visit(e)
		switch v := e.(type) {
		case *ast.FnCall:
			for _, a := range v.Args {
				walkExpr(a)
			}
		case *ast.BinaryExpr:
			walkExpr(v.Left)
			walkExpr(v.Right)
		case *ast.UnaryExpr:
			walkExpr(v.Operand)
		case *ast.ParenExpr:
			walkExpr(v.Expr)
		case *ast.FieldAccess:
			walkExpr(v.Base)
		case *ast.IndexAccess:
			walkExpr(v.Base)
			walkExpr(v.Index)
		case *ast.ListExpr:
			for _, el := range v.Elements {
				walkExpr(el)
			}
		case *ast.BlockExpr:
			for _, kv := range v.Entries {
				walkExpr(kv.Value)
			}
		}
	}
	walkStmts = func(stmts []ast.ArrowStmt) {
		for _, s := range stmts {
			switch v := s.(type) {
			case *ast.StepStmt:
				walkExpr(v.Expr)
			case *ast.GuardStmt:
				walkExpr(v.Condition)
			case *ast.OutputStmt:
				walkExpr(v.Value)
			case *ast.WhenStmt:
				walkExpr(v.Condition)
				if v.Inline != nil {
					walkExpr(v.Inline)
				}
				walkStmts(v.Body)
			case *ast.TryRecover:
				walkStmts(v.Try)
				walkStmts(v.Recover)
			}
		}
	}
	walkStmts(ep.Stmts)
}

// emitEndpoint writes the decorator + handler for one endpoint. Phase 1 (no
// data ops) uses the simple "return JSONResponse(dict, status)" path; Phase 3+
// (with data ops, when/try blocks, map, log, etc.) delegates to emitBody which
// walks every |> step + guard + control-flow construct.
func (g *Generator) emitEndpoint(b *strings.Builder, ep *ast.Endpoint) {
	method := strings.ToLower(ep.Method)
	fastapiPath := pathBpToFastapi(ep.Path)

	if ep.Intent != nil {
		fmt.Fprintf(b, "# %s\n", ep.Intent.Text)
	}

	var inputs []*ast.InputStmt
	hasSteps := endpointHasComplexBody(ep)
	for _, s := range ep.Stmts {
		if v, ok := s.(*ast.InputStmt); ok {
			inputs = append(inputs, v)
		}
	}

	params := signatureParams(ep.Method, ep.Path, inputs)
	for _, header := range endpointHeaderFields(ep) {
		if params != "" {
			params += ", "
		}
		params += fmt.Sprintf("%s: str | None = Header(None, alias=%q)", pythonHeaderParamName(header), header)
	}
	// `use <middleware>` becomes a Depends() parameter. Multiple `use` markers
	// chain. The alias name + model come from `inject X as Y` inside the
	// middleware's before block.
	for _, m := range ep.Meta {
		if m.Kind != "use" || m.Use == nil {
			continue
		}
		mw := g.findMiddleware(m.Use.Name)
		if mw == nil {
			continue
		}
		alias := findInjectedAlias(mw)
		model := findInjectedModel(mw)
		if alias == "" {
			alias = m.Use.Name
		}
		if params != "" {
			params += ", "
		}
		if model != "" {
			params += fmt.Sprintf("%s: schema.%s = Depends(%s)", alias, common.PascalCase(model), m.Use.Name)
		} else {
			params += fmt.Sprintf("%s = Depends(%s)", alias, m.Use.Name)
		}
	}
	if endpointTouchesDB(ep) || g.endpointNeedsDBSession(ep) {
		if params != "" {
			params += ", "
		}
		params += "db: Session = Depends(get_db)"
	}
	fnName := handlerName(ep.Method, ep.Path)

	fmt.Fprintf(b, "@router.%s(%q)\n", method, fastapiPath)
	// Sync `def` for DB-touching handlers so sync SQLAlchemy doesn't block
	// an event loop; `async def` for everything else so FastAPI doesn't pay
	// a threadpool hop on static responses.
	keyword := "async def"
	if endpointTouchesDB(ep) {
		keyword = "def"
	}
	fmt.Fprintf(b, "%s %s(%s):\n", keyword, fnName, params)

	if hasSteps {
		g.emitBody(b, ep)
		return
	}

	// Phase 1 fast path — no body translation needed.
	var out *ast.OutputStmt
	for _, s := range ep.Stmts {
		if o, ok := s.(*ast.OutputStmt); ok {
			out = o
			break
		}
	}
	if out == nil {
		b.WriteString("    return Response(status_code=204)\n")
		return
	}
	status := out.Status
	if status == "" {
		status = "200"
	}
	emitOutputReturn(b, status, out.Value)
}

// findMiddleware looks up a declared middleware by name, or returns nil.
func (g *Generator) findMiddleware(name string) *ast.Middleware {
	for _, mw := range g.middlewares() {
		if mw.Name == name {
			return mw
		}
	}
	return nil
}

// endpointNeedsDBSession reports whether an endpoint needs `db: Session` in
// its signature even though its own body doesn't touch the DB — typically
// because a middleware it `use`s does, and the dependency chain needs the
// session to be visible.
func (g *Generator) endpointNeedsDBSession(ep *ast.Endpoint) bool {
	for _, m := range ep.Meta {
		if m.Kind != "use" || m.Use == nil {
			continue
		}
		mw := g.findMiddleware(m.Use.Name)
		if mw == nil {
			continue
		}
		for _, s := range mw.Before {
			if step, ok := s.(*ast.StepStmt); ok {
				if fn, ok := step.Expr.(*ast.FnCall); ok {
					switch fn.Name {
					case "save", "fetch", "query", "update", "delete":
						return false // middleware handles its own db; not needed here
					}
				}
			}
		}
	}
	return false
}

// endpointHasComplexBody returns true when the endpoint contains any statement
// that needs the full body emitter (steps, guards, when, try/recover). Endpoints
// with only inputs + a single output use the Phase 1 fast path.
func endpointHasComplexBody(ep *ast.Endpoint) bool {
	for _, s := range ep.Stmts {
		switch s.(type) {
		case *ast.StepStmt, *ast.GuardStmt, *ast.WhenStmt, *ast.TryRecover, *ast.IntentStep:
			return true
		}
	}
	return false
}

// signatureParams renders the FastAPI handler signature. Path parameters stay
// ordinary required arguments. Every other input gets an explicit Body/Query
// marker so the generated transport matches the Blueprint HTTP contract.
func signatureParams(method, path string, inputs []*ast.InputStmt) string {
	var pathParams, requestParams []string
	query := strings.EqualFold(method, "GET") || strings.EqualFold(method, "DELETE")
	for _, in := range inputs {
		typ := pythonTypeAnnotation(in.Type)
		if common.IsPathParam(in.Name, path) {
			pathParams = append(pathParams, fmt.Sprintf("%s: %s", in.Name, typ))
			continue
		}

		defaultValue := "..."
		if value, ok := inputDefaultValue(in.Constraints); ok {
			defaultValue = value
		} else if isOptional(in.Constraints) {
			defaultValue = "None"
			typ += " | None"
		}
		args := []string{defaultValue}
		if !query {
			args = append(args, "embed=True")
		}
		args = append(args, inputValidationArgs(in.Type, in.Constraints)...)
		marker := "Body"
		if query {
			marker = "Query"
		}
		requestParams = append(requestParams,
			fmt.Sprintf("%s: %s = %s(%s)", in.Name, typ, marker, strings.Join(args, ", ")))
	}
	parts := append(pathParams, requestParams...)
	return strings.Join(parts, ", ")
}

func endpointHeaderFields(ep *ast.Endpoint) []string {
	seen := map[string]bool{}
	var headers []string
	walkEndpointExprs(ep, func(expr ast.Expr) {
		field, ok := expr.(*ast.FieldAccess)
		if !ok {
			return
		}
		base, ok := field.Base.(*ast.Ident)
		if !ok || base.Name != "header" || seen[field.Field] {
			return
		}
		seen[field.Field] = true
		headers = append(headers, field.Field)
	})
	return headers
}

func inputDefaultValue(constraints []*ast.Constraint_) (string, bool) {
	for _, constraint := range constraints {
		if constraint.Kind == "default" && constraint.Value != nil {
			return exprToPy(constraint.Value), true
		}
	}
	return "", false
}

func inputValidationArgs(t ast.TypeExpr, constraints []*ast.Constraint_) []string {
	var args []string
	for _, constraint := range constraints {
		if constraint.Kind != "min" && constraint.Kind != "max" {
			continue
		}
		name := "ge"
		if constraint.Kind == "max" {
			name = "le"
		}
		switch primitive := t.(type) {
		case *ast.PrimitiveType:
			if primitive.Name == "string" || primitive.Name == "text" {
				name = constraint.Kind + "_length"
			}
		case *ast.ListType, *ast.MapType:
			name = constraint.Kind + "_length"
		}
		args = append(args, fmt.Sprintf("%s=%s", name, exprToPy(constraint.Value)))
	}
	return args
}

func isOptional(cs []*ast.Constraint_) bool {
	for _, c := range cs {
		if c.Kind == "optional" {
			return true
		}
		if c.Kind == "required" {
			return false
		}
	}
	return false
}

// pathBpToFastapi rewrites Blueprint's `:param` segments into FastAPI's
// `{param}` syntax. e.g. `/api/hello/:name` → `/api/hello/{name}`.
func pathBpToFastapi(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			segs[i] = "{" + s[1:] + "}"
		}
	}
	return strings.Join(segs, "/")
}

// handlerName builds a Python-friendly handler name from the method + path.
// `GET /api/health` → `get_health`. `GET /api/hello/:name` → `get_hello_name`.
// `POST /api/cart/items` → `post_cart_items`. Acronyms and segment order are
// kept so resource grouping reads naturally in the source.
func handlerName(method, path string) string {
	var segs []string
	for _, s := range strings.Split(path, "/") {
		if s == "" || s == "api" {
			continue
		}
		s = strings.TrimPrefix(s, ":")
		// SnakeCase preserves hyphens; route paths often have `change-password`
		// style segments, so collapse hyphens to underscores first.
		s = strings.ReplaceAll(s, "-", "_")
		segs = append(segs, common.SnakeCase(s))
	}
	suffix := strings.Join(segs, "_")
	if suffix == "" {
		suffix = "root"
	}
	return strings.ToLower(method) + "_" + suffix
}

// emitOutputReturn writes the `return ...` line for an endpoint's output.
// For 204 the body is dropped. For string outputs we use PlainTextResponse.
// For object outputs we build a dict literal so FastAPI serialises it as JSON.
func emitOutputReturn(b *strings.Builder, status string, value ast.Expr) {
	if status == "204" {
		fmt.Fprintf(b, "    return Response(status_code=204)\n")
		return
	}

	switch v := value.(type) {
	case *ast.StringLit:
		fmt.Fprintf(b, "    return PlainTextResponse(%s, status_code=%s)\n",
			pyStringLiteral(v.Value), status)
	case *ast.BlockExpr:
		body := blockExprToPyDict(v)
		fmt.Fprintf(b, "    return JSONResponse(jsonable_encoder(%s), status_code=%s)\n", body, status)
	default:
		fmt.Fprintf(b, "    return JSONResponse(jsonable_encoder(%s), status_code=%s)\n",
			exprToPy(value), status)
	}
}

// blockExprToPyDict renders `{ k: v, ... }` Blueprint output as a Python dict
// literal. Keys keep their .bp form (snake_case) so the response JSON shape
// matches what the .bp source says.
func blockExprToPyDict(b *ast.BlockExpr) string {
	if len(b.Entries) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(b.Entries))
	for _, kv := range b.Entries {
		parts = append(parts, fmt.Sprintf("%q: %s", kv.Key, exprToPy(kv.Value)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// exprToPy converts a Blueprint expression to a Python expression. Handles
// every literal kind the parser produces, plus FieldAccess, BlockExpr, and
// the `now` literal. Identifier resolution that depends on per-endpoint
// state (FK aliases, user-fn return shape) lives in exprToPyWithCtx; that
// wrapper falls back to this base function for everything else.
func exprToPy(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StringLit:
		return pyStringLiteral(v.Value)
	case *ast.IntLit:
		return v.Value
	case *ast.FloatLit:
		return v.Value
	case *ast.BoolLit:
		if v.Value {
			return "True"
		}
		return "False"
	case *ast.NullLit:
		return "None"
	case *ast.NowLit:
		return "datetime.now(timezone.utc)"
	case *ast.DurationLit:
		return durationLiteralToPy(v.Value)
	case *ast.SizeLit:
		return sizeLiteralToPy(v.Value)
	case *ast.RateLit:
		return pyStringLiteral(v.Value)
	case *ast.Ident:
		return v.Name
	case *ast.FieldAccess:
		if base, ok := v.Base.(*ast.Ident); ok && base.Name == "env" {
			return "env." + v.Field
		}
		if base, ok := v.Base.(*ast.Ident); ok && base.Name == "header" {
			return pythonHeaderParamName(v.Field)
		}
		return exprToPy(v.Base) + "." + v.Field
	case *ast.IndexAccess:
		return exprToPy(v.Base) + "[" + exprToPy(v.Index) + "]"
	case *ast.BinaryExpr:
		op := v.Op
		if op == "and" || op == "or" {
			// already valid Python
		}
		return "(" + exprToPy(v.Left) + " " + op + " " + exprToPy(v.Right) + ")"
	case *ast.UnaryExpr:
		if v.Op == "not" {
			return "not " + exprToPy(v.Operand)
		}
		return v.Op + exprToPy(v.Operand)
	case *ast.ParenExpr:
		return "(" + exprToPy(v.Expr) + ")"
	case *ast.ListExpr:
		parts := make([]string, len(v.Elements))
		for i, el := range v.Elements {
			parts[i] = exprToPy(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.BlockExpr:
		parts := make([]string, 0, len(v.Entries))
		for _, kv := range v.Entries {
			parts = append(parts, fmt.Sprintf("%q: %s", kv.Key, exprToPy(kv.Value)))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ast.FnCall:
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = exprToPy(a)
		}
		return v.Name + "(" + strings.Join(args, ", ") + ")"
	case *ast.PathExpr:
		return pyStringLiteral(v.Value)
	default:
		return fmt.Sprintf("None  # TODO(python): unsupported expression %T", e)
	}
}

// durationLiteralToPy converts the duration tokens accepted by the lexer to
// an integer millisecond expression, matching the Node target's contract.
func durationLiteralToPy(value string) string {
	for _, unit := range []struct {
		suffix string
		factor string
	}{
		{"hours", "60 * 60 * 1000"},
		{"hour", "60 * 60 * 1000"},
		{"days", "24 * 60 * 60 * 1000"},
		{"day", "24 * 60 * 60 * 1000"},
		{"min", "60 * 1000"},
		{"ms", "1"},
		{"h", "60 * 60 * 1000"},
		{"d", "24 * 60 * 60 * 1000"},
		{"s", "1000"},
	} {
		if strings.HasSuffix(value, unit.suffix) {
			num := strings.TrimSuffix(value, unit.suffix)
			if unit.factor == "1" {
				return num
			}
			return num + " * " + unit.factor
		}
	}
	return value
}

// sizeLiteralToPy converts the size tokens accepted by the lexer to bytes.
func sizeLiteralToPy(value string) string {
	for _, unit := range []struct {
		suffix string
		factor string
	}{
		{"gb", "1024 * 1024 * 1024"},
		{"mb", "1024 * 1024"},
		{"kb", "1024"},
		{"b", "1"},
	} {
		if strings.HasSuffix(value, unit.suffix) {
			num := strings.TrimSuffix(value, unit.suffix)
			if unit.factor == "1" {
				return num
			}
			return num + " * " + unit.factor
		}
	}
	return value
}

// pyStringLiteral renders a .bp string literal as a Python literal,
// converting `{name}` interpolations into an f-string when any are present.
// FastAPI handlers expose path/query params as Python locals, so f-strings
// "just work" against the handler signature.
func pyStringLiteral(s string) string {
	if !strings.Contains(s, "{") {
		return fmt.Sprintf("%q", s)
	}
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "f\"" + escaped + "\""
}

// pythonTypeAnnotation maps a Blueprint type to a Python/FastAPI annotation.
// Phase 1 covers the primitives used by hello-world; anything else returns
// `str` plus a TODO comment so the file still parses.
func pythonTypeAnnotation(t ast.TypeExpr) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "string", "text":
			return "str"
		case "uuid":
			return "UUID"
		case "int", "money":
			return "int"
		case "float":
			return "float"
		case "bool":
			return "bool"
		case "timestamp":
			return "datetime"
		case "json":
			return "dict"
		}
	case *ast.TypedJSONType:
		return "dict"
	case *ast.ListType:
		return "list[" + pythonTypeAnnotation(v.Element) + "]"
	case *ast.MapType:
		return "dict[" + pythonTypeAnnotation(v.Key) + ", " + pythonTypeAnnotation(v.Value) + "]"
	}
	return "str  # TODO(python): unmapped Blueprint type"
}

// orderedResources returns the deterministic, deduplicated list of resource
// names across the given endpoints. Used by genAppPy.
func orderedResources(eps []*ast.Endpoint) []string {
	seen := map[string]bool{}
	var out []string
	for _, ep := range eps {
		r := common.ExtractResource(ep.Path)
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	// Sort for determinism — file order doesn't matter for FastAPI.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// pyModuleName makes a Python-safe module name from a Blueprint resource
// segment. Hyphens become underscores and the name is lowercased.
func pyModuleName(s string) string {
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ToLower(s)
	if pythonKeywords[s] {
		return "_" + s
	}
	return s
}
