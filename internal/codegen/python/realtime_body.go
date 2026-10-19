package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// Shared pieces of the STREAM and WS bodies: the request headers they read,
// the declared fn calls they make (run in a worker thread), and the object
// literals whose absent header or env values drop their key, as on Node.

// headersDictName is the Python name of the request headers a STREAM or WS body
// reads. The route captures them once, before the body runs, so every handler
// sees the values of the request that opened the connection.
const headersDictName = "bp_headers"

// bodyStmtsOfStream views a STREAM's handlers as statements, so the expression
// walkers that take statements visit every `where` condition and output.
func bodyStmtsOfStream(se *ast.StreamEndpoint) []ast.ArrowStmt {
	var out []ast.ArrowStmt
	for _, h := range se.Handlers {
		out = append(out, &ast.WhenStmt{Loc: h.Loc, Condition: h.Condition, Body: h.Body})
	}
	return out
}

// directFieldReads returns the field names of every direct `root.field` read in
// stmts (header.X gives "X"). A nested read such as header.X.y is reported by the
// feature check, not here.
func directFieldReads(root string, stmts []ast.ArrowStmt) []string {
	seen := map[string]bool{}
	var out []string
	walkEndpointExprs(&ast.Endpoint{Stmts: stmts}, func(e ast.Expr) {
		fa, ok := e.(*ast.FieldAccess)
		if !ok {
			return
		}
		if base, ok := fa.Base.(*ast.Ident); ok && base.Name == root && !seen[fa.Field] {
			seen[fa.Field] = true
			out = append(out, fa.Field)
		}
	})
	sort.Strings(out)
	return out
}

// readsDirect reports whether stmts contain a direct `root.field` read.
func readsDirect(root string, stmts []ast.ArrowStmt) bool {
	return len(directFieldReads(root, stmts)) > 0
}

// headerCapture renders the dict literal that captures each named header of the
// request, keyed by its lower-case name. A missing header is None.
func headerCapture(request string, fields []string) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		name := strings.ToLower(f)
		parts[i] = fmt.Sprintf("%q: %s.headers.get(%q)", name, request, name)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// userFnCallsIn returns the declared fn names called anywhere in stmts, sorted.
func (g *Generator) userFnCallsIn(stmts []ast.ArrowStmt) []string {
	fns := g.userFnNames()
	seen := map[string]bool{}
	var out []string
	walkEndpointExprs(&ast.Endpoint{Stmts: stmts}, func(e ast.Expr) {
		if fc, ok := e.(*ast.FnCall); ok && fns[fc.Name] && !seen[fc.Name] {
			seen[fc.Name] = true
			out = append(out, fc.Name)
		}
	})
	sort.Strings(out)
	return out
}

// writeUserFnImports imports the fn wrappers a body calls, as REST routes do.
func (g *Generator) writeUserFnImports(b *strings.Builder, names []string) {
	pipes := g.pipeNames()
	for _, n := range names {
		fmt.Fprintf(b, "from %s import %s\n", userCallModule(n, pipes), n)
	}
}

// handlerCallsFn reports whether a STREAM handler calls a declared fn. Such a
// handler runs in a worker thread, because a fn may block the event loop.
func (g *Generator) handlerCallsFn(h *ast.StreamHandler) bool {
	return len(g.userFnCallsIn([]ast.ArrowStmt{&ast.WhenStmt{Condition: h.Condition, Body: h.Body}})) > 0
}

// streamEntryPy renders the value of a STREAM frame entry. A direct header or
// env read that is absent drops its key on Node, so it becomes MISSING here,
// which frame_data removes.
func streamEntryPy(e ast.Expr, value string) string {
	if isDirectMaybeMissing(e) {
		return "or_missing(" + value + ")"
	}
	return value
}

// wsObjectEntry renders one `key: value` entry of a WS object literal. A direct
// header or env read that is absent contributes no key, as on Node.
func wsObjectEntry(key string, e ast.Expr, value string) string {
	if isDirectMaybeMissing(e) {
		return fmt.Sprintf("**({%q: %s} if (%s) is not None else {})", key, value, value)
	}
	return fmt.Sprintf("%q: %s", key, value)
}

// isDirectMaybeMissing reports whether e is header.X or env.X: the reads whose
// value Node leaves undefined when absent.
func isDirectMaybeMissing(e ast.Expr) bool {
	fa, ok := e.(*ast.FieldAccess)
	if !ok {
		return false
	}
	base, ok := fa.Base.(*ast.Ident)
	return ok && (base.Name == "header" || base.Name == "env")
}

// wsOutputPy renders a WS object literal with direct header and env entries
// dropped when absent. Keys keep their .bp spelling, as WS replies do.
func wsOutputPy(blk *ast.BlockExpr, ctx *bodyCtx) string {
	if len(blk.Entries) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(blk.Entries))
	for _, kv := range blk.Entries {
		parts = append(parts, wsObjectEntry(kv.Key, kv.Value, exprToPyWithCtx(kv.Value, ctx)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// isDirectHeaderEnvRead reports whether v is header.X or env.X, whose base is a
// bare name that the walkers must not treat as a value of its own.
func isDirectHeaderEnvRead(v *ast.FieldAccess) bool {
	base, ok := v.Base.(*ast.Ident)
	return ok && (base.Name == "header" || base.Name == "env")
}

// headerEnvReadFeature names a `header` or `env` reference that a WS or STREAM
// body cannot translate: a bare name, or a read deeper than one field. A direct
// read (header.X, env.X) is supported and reports nothing here.
func headerEnvReadFeature(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		if v.Name == "header" || v.Name == "env" {
			return fmt.Sprintf("bare `%s` (read a field: %s.NAME)", v.Name, v.Name)
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
		if base, ok := root.Base.(*ast.Ident); ok && root != v && (base.Name == "header" || base.Name == "env") {
			return fmt.Sprintf("nested %s read (use %s.NAME)", base.Name, base.Name)
		}
	}
	return ""
}
