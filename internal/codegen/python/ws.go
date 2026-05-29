package python

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

// genWsRoute emits one src/routes/<resource>_ws.py per WS endpoint.
//
// Phase 5 ships the FastAPI websocket route shape — `@router.websocket(...)`
// plus an async handler that accepts the connection, runs the `on_connect`
// pre-flight (input parsing + guards + initial broadcast/log calls become
// TODO-tagged stubs since they need a shared broadcaster), enters a
// `receive_text` loop that calls the `on_message` body for each message,
// and runs `on_disconnect` on `WebSocketDisconnect`. The route registers
// under the app at startup; what comes over the wire is up to the user to
// implement.
//
// Realtime semantics (join/leave/broadcast across processes, payload
// shapes, JWT validation in the connect handshake) are deferred to
// Phase 5b — they need either an in-process broadcaster or a Redis
// pub/sub backbone; the current emit puts every meaningful body line
// behind a TODO so a developer can wire it without re-reading the .bp
// source.
// genWsRouteFile emits one src/routes/<resource>_ws.py holding every WS
// endpoint that maps to the resource.
func (g *Generator) genWsRouteFile(resource string, wes []*ast.WsEndpoint) codegen.OutputFile {
	module := pyModuleName(resource) + "_ws"
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("from fastapi import APIRouter, WebSocket, WebSocketDisconnect\n\n")
	b.WriteString("router = APIRouter()\n\n")
	for i, we := range wes {
		if i > 0 {
			b.WriteString("\n")
		}
		emitWsEndpoint(&b, we)
	}
	return codegen.OutputFile{Path: "src/routes/" + module + ".py", Content: []byte(b.String())}
}

// emitWsEndpoint writes one WebSocket route + handler.
func emitWsEndpoint(b *strings.Builder, we *ast.WsEndpoint) {
	fnName := wsHandlerName(we.Path)
	fastapiPath := pathBpToFastapi(we.Path)

	if we.Intent != nil {
		fmt.Fprintf(b, "# %s\n", we.Intent.Text)
	}

	// Path params become handler parameters in addition to the WebSocket.
	pathParams := common.ExtractPathParams(we.Path)
	params := []string{"websocket: WebSocket"}
	for _, p := range pathParams {
		params = append(params, fmt.Sprintf("%s: str", p))
	}

	fmt.Fprintf(b, "@router.websocket(%q)\n", fastapiPath)
	fmt.Fprintf(b, "async def %s(%s):\n", fnName, strings.Join(params, ", "))
	b.WriteString("    await websocket.accept()\n")

	if len(we.OnConnect) > 0 {
		b.WriteString("    # on_connect:\n")
		emitWsStmtsAsComment(b, we.OnConnect)
	}

	b.WriteString("    try:\n")
	b.WriteString("        while True:\n")
	b.WriteString("            message = await websocket.receive_text()\n")
	if len(we.OnMessage) > 0 {
		b.WriteString("            # on_message:\n")
		emitWsStmtsAsComment(b, we.OnMessage)
		b.WriteString("            # TODO(python phase 5b): parse `message` JSON, validate,\n")
		b.WriteString("            # persist via `db`, and broadcast through the shared bus.\n")
	}
	b.WriteString("    except WebSocketDisconnect:\n")
	if len(we.OnDisconnect) > 0 {
		b.WriteString("        # on_disconnect:\n")
		emitWsStmtsAsComment(b, we.OnDisconnect)
	}
	// Always emit `pass` so the except branch has a statement even when
	// every on_disconnect line is a TODO comment.
	b.WriteString("        pass\n")
}

// emitWsStmtsAsComment renders WS body statements as commented Python pseudo-
// code so the generated handler reads the way the .bp source does. Comments
// stay attached to the statement they describe and survive a future
// re-emit (manifest tracks the file as generated, not user-owned).
func emitWsStmtsAsComment(b *strings.Builder, stmts []ast.ArrowStmt) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.StepStmt:
			fmt.Fprintf(b, "    #   |> %s\n", describeWsStep(v))
		case *ast.GuardStmt:
			fmt.Fprintf(b, "    #   |> guard %s -> %s %q\n",
				wsExprDesc(v.Condition), v.Status, v.Message)
		case *ast.OutputStmt:
			fmt.Fprintf(b, "    #   -> %s %s\n", v.Status, wsExprDesc(v.Value))
		case *ast.WhenStmt:
			fmt.Fprintf(b, "    #   |> when %s { ... }\n", wsExprDesc(v.Condition))
		case *ast.IntentStep:
			fmt.Fprintf(b, "    # %s\n", v.Text)
		}
	}
}

func describeWsStep(s *ast.StepStmt) string {
	if s.Binding != "" {
		return fmt.Sprintf("%s = %s", s.Binding, wsExprDesc(s.Expr))
	}
	return wsExprDesc(s.Expr)
}

// wsExprDesc renders a Blueprint expression as a short human-readable string
// (not Python code) for the on_connect / on_message comment block.
func wsExprDesc(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StringLit:
		return fmt.Sprintf("%q", v.Value)
	case *ast.IntLit:
		return v.Value
	case *ast.BoolLit:
		if v.Value {
			return "true"
		}
		return "false"
	case *ast.FieldAccess:
		return wsExprDesc(v.Base) + "." + v.Field
	case *ast.FnCall:
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = wsExprDesc(a)
		}
		return v.Name + "(" + strings.Join(args, ", ") + ")"
	case *ast.BinaryExpr:
		return wsExprDesc(v.Left) + " " + v.Op + " " + wsExprDesc(v.Right)
	case *ast.BlockExpr:
		parts := make([]string, 0, len(v.Entries))
		for _, kv := range v.Entries {
			parts = append(parts, kv.Key+": "+wsExprDesc(kv.Value))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	return fmt.Sprintf("<%T>", e)
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
