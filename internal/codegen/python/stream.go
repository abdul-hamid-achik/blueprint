package python

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

// genStreamRoute emits one src/routes/<resource>_stream.py per STREAM
// endpoint. Phase 5 ships the route shell — an SSE endpoint backed by
// sse-starlette's EventSourceResponse — and translates pre-stream
// statements (the same ones REST endpoints use: input, fetch, guard) so
// pre-condition errors (404s) work. Inside the `stream { on event(...) }`
// block we emit a generator that subscribes to a Redis pub/sub channel
// — the actual event-routing and `where(...)` filter logic is left as a
// TODO comment block so users can wire it to their event source.
//
// The pre-stream statements run synchronously to validate inputs and 404
// before opening the SSE connection (matching the JS target's behavior).
// Once we hand off to the event-source response the handler is async.
// genStreamRouteFile emits one src/routes/<resource>_stream.py per resource,
// holding every STREAM endpoint that maps to it. Multiple SSE endpoints on
// the same resource share a router.
func (g *Generator) genStreamRouteFile(resource string, ses []*ast.StreamEndpoint) codegen.OutputFile {
	module := pyModuleName(resource) + "_stream"
	anyDB := false
	for _, se := range ses {
		if g.streamTouchesDB(se) {
			anyDB = true
			break
		}
	}

	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import asyncio\n")
	b.WriteString("import json\n\n")
	b.WriteString("from fastapi import APIRouter, HTTPException\n")
	if anyDB {
		b.WriteString("from fastapi import Depends\n")
		b.WriteString("from sqlalchemy.orm import Session\n")
		b.WriteString("from src.lib.db import get_db\n")
		b.WriteString("from src.models import schema\n")
	}
	b.WriteString("from sse_starlette.sse import EventSourceResponse\n\n")
	b.WriteString("router = APIRouter()\n\n")

	for i, se := range ses {
		if i > 0 {
			b.WriteString("\n")
		}
		g.emitStreamEndpoint(&b, se)
	}
	return codegen.OutputFile{Path: "src/routes/" + module + ".py", Content: []byte(b.String())}
}

// emitStreamEndpoint writes the decorator + handler for one STREAM endpoint.
func (g *Generator) emitStreamEndpoint(b *strings.Builder, se *ast.StreamEndpoint) {
	fnName := streamHandlerName(se.Path)
	fastapiPath := pathBpToFastapi(se.Path)

	if se.Intent != nil {
		fmt.Fprintf(b, "# %s\n", se.Intent.Text)
	}
	fmt.Fprintf(b, "@router.get(%q)\n", fastapiPath)

	params := streamSignatureParams(se)
	if g.streamTouchesDB(se) {
		if params != "" {
			params += ", "
		}
		params += "db: Session = Depends(get_db)"
	}
	fmt.Fprintf(b, "async def %s(%s):\n", fnName, params)

	// Pre-stream statements: run sync (no async data ops in Phase 5).
	preCtx := g.newStreamPreCtx(se)
	wrotePre := false
	for _, s := range se.Stmts {
		switch v := s.(type) {
		case *ast.InputStmt, *ast.IntentStep:
			// inputs already in signature
		case *ast.StepStmt:
			emitStep(b, v, preCtx, "    ")
			wrotePre = true
		case *ast.GuardStmt:
			emitGuard(b, v, preCtx, "    ")
			wrotePre = true
		}
	}
	if wrotePre {
		b.WriteString("\n")
	}

	// Event generator. Phase 5 emits a placeholder that yields a ping at the
	// declared `on timeout(N)` interval (default 30s). The stream body's
	// event-routing semantics are tracked as Phase 5b.
	b.WriteString("    async def _events():\n")
	b.WriteString("        # TODO(python phase 5b): replace with a Redis pub/sub subscription\n")
	b.WriteString("        # that filters on the .bp `on event(...) where(...)` predicates.\n")
	timeout := streamFirstTimeoutSeconds(se)
	if timeout <= 0 {
		timeout = 30
	}
	fmt.Fprintf(b, "        while True:\n")
	fmt.Fprintf(b, "            await asyncio.sleep(%d)\n", timeout)
	b.WriteString("            yield {\"data\": json.dumps({\"type\": \"ping\"})}\n\n")

	b.WriteString("    return EventSourceResponse(_events())\n")
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

// streamSignatureParams renders FastAPI handler params for a STREAM endpoint.
// Same shape as REST: path params first, required next, optionals last.
func streamSignatureParams(se *ast.StreamEndpoint) string {
	var inputs []*ast.InputStmt
	for _, s := range se.Stmts {
		if in, ok := s.(*ast.InputStmt); ok {
			inputs = append(inputs, in)
		}
	}
	return signatureParams(se.Path, inputs)
}

// streamTouchesDB reports whether any pre-stream statement is a data op.
// We need the DB session in the handler signature only when it does.
func (g *Generator) streamTouchesDB(se *ast.StreamEndpoint) bool {
	for _, s := range se.Stmts {
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
	return false
}

// streamFirstTimeoutSeconds looks at the stream block's handlers for a
// `on timeout(N)` entry and returns N in seconds. Used as the keepalive
// interval. Returns 0 if no timeout handler is declared.
func streamFirstTimeoutSeconds(se *ast.StreamEndpoint) int {
	for _, h := range se.Handlers {
		if h.Timeout == "" {
			continue
		}
		if n := parseDurationSeconds(h.Timeout); n > 0 {
			return n
		}
	}
	return 0
}

// parseDurationSeconds converts a .bp duration literal like `5min`, `30s`,
// `1h` into seconds. Returns 0 on parse failure.
func parseDurationSeconds(s string) int {
	if s == "" {
		return 0
	}
	digits := []byte{}
	suffix := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			digits = append(digits, c)
		} else {
			suffix = append(suffix, c)
		}
	}
	if len(digits) == 0 {
		return 0
	}
	n := 0
	for _, d := range digits {
		n = n*10 + int(d-'0')
	}
	switch string(suffix) {
	case "ms":
		return n / 1000
	case "s":
		return n
	case "min":
		return n * 60
	case "h":
		return n * 3600
	}
	return n
}

// newStreamPreCtx builds a bodyCtx for the pre-stream statements (the bits
// that run before EventSourceResponse takes over). Phase 5 keeps it simple:
// inputs go in c.inputs, varModel gets seeded from data-op steps.
func (g *Generator) newStreamPreCtx(se *ast.StreamEndpoint) *bodyCtx {
	c := &bodyCtx{
		path:        se.Path,
		inputs:      map[string]bool{},
		varModel:    map[string]string{},
		cardinality: map[string]resolve.Cardinality{},
		fkAliases:   map[string]string{},
		models:      g.models(),
		userFns:     g.userFnNames(),
	}
	for _, s := range se.Stmts {
		if in, ok := s.(*ast.InputStmt); ok {
			c.inputs[in.Name] = true
		}
	}
	for _, s := range se.Stmts {
		step, ok := s.(*ast.StepStmt)
		if !ok || step.Binding == "" {
			continue
		}
		fn, ok := step.Expr.(*ast.FnCall)
		if !ok || len(fn.Args) == 0 {
			continue
		}
		id, ok := fn.Args[0].(*ast.Ident)
		if !ok {
			continue
		}
		switch fn.Name {
		case "save", "fetch", "update", "delete", "query":
			c.varModel[step.Binding] = id.Name
		}
	}
	return c
}
