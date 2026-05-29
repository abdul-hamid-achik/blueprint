package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

// genMiddlewares emits one src/middleware/<name>.py per declared middleware.
// Each module exports a single function whose name matches the middleware
// (snake_case) — that function becomes a FastAPI dependency the route layer
// wires via Depends.
//
// Phase 3d/5 supports the common middleware shape: a `before {}` body with
// guard / fetch / fn-call / inject statements that produce a single value to
// be injected into the endpoint context. The injected value is the function's
// return; FastAPI exposes it to the handler under the name the user picks via
// `inject <var> as <alias>`.
func (g *Generator) genMiddlewares(mws []*ast.Middleware) []codegen.OutputFile {
	if len(mws) == 0 {
		return nil
	}
	var out []codegen.OutputFile
	out = append(out, emptyInit("src/middleware/__init__.py", g.sourceFile))
	for _, mw := range mws {
		out = append(out, g.genMiddleware(mw))
	}
	return out
}

func (g *Generator) genMiddleware(mw *ast.Middleware) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Compute what the body needs and emit only those imports.
	used := scanMiddlewareUsage(mw, g.userFnNames(), g.models())
	writeMiddlewareImports(&b, used)
	b.WriteString("\n")

	// Determine the header parameter(s) (e.g. `header.Authorization`).
	headerParams := used.headerFields // ordered

	// Build the function signature.
	params := []string{}
	for _, h := range headerParams {
		params = append(params, fmt.Sprintf("%s: str | None = Header(None, alias=%q)", common.SnakeCase(h), h))
	}
	if used.touchesDB {
		params = append(params, "db: Session = Depends(get_db)")
	}

	if mw.Intent != nil {
		fmt.Fprintf(&b, "# %s\n", mw.Intent.Text)
	}
	fmt.Fprintf(&b, "def %s(%s):\n", mw.Name, strings.Join(params, ", "))

	// Emit the body. The bodyCtx is a stripped-down version of the endpoint
	// ctx: same emitters, no endpoint-specific knowledge.
	ctx := newMiddlewareBodyCtx(mw, g.models(), g.userFnNames(), headerParams)
	wrote := false
	for _, s := range mw.Before {
		switch v := s.(type) {
		case *ast.StepStmt:
			if isInjectStep(v) {
				// Captured as the return value.
				continue
			}
			emitStep(&b, v, ctx, "    ")
			wrote = true
		case *ast.GuardStmt:
			emitGuard(&b, v, ctx, "    ")
			wrote = true
		case *ast.IntentStep:
			fmt.Fprintf(&b, "    # %s\n", v.Text)
		}
	}
	// inject <var> as <alias> → return <var>
	if injected := findInjectedVar(mw); injected != "" {
		fmt.Fprintf(&b, "    return %s\n", injected)
		wrote = true
	}
	if !wrote {
		b.WriteString("    return None\n")
	}

	return codegen.OutputFile{
		Path:    "src/middleware/" + mw.Name + ".py",
		Content: []byte(b.String()),
	}
}

// middlewareUsage records which imports a middleware body needs.
type middlewareUsage struct {
	headerFields []string
	touchesDB    bool
	hasGuards    bool
	callsUserFn  bool
	userFnCalls  []string // ordered, unique
}

func scanMiddlewareUsage(mw *ast.Middleware, userFns map[string]bool, models []*ast.Model) middlewareUsage {
	u := middlewareUsage{}
	seenHeaders := map[string]bool{}
	seenFns := map[string]bool{}

	var visitExpr func(e ast.Expr)
	visitExpr = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch v := e.(type) {
		case *ast.FieldAccess:
			if id, ok := v.Base.(*ast.Ident); ok && id.Name == "header" {
				if !seenHeaders[v.Field] {
					seenHeaders[v.Field] = true
					u.headerFields = append(u.headerFields, v.Field)
				}
			}
			visitExpr(v.Base)
		case *ast.FnCall:
			if userFns[v.Name] && !seenFns[v.Name] {
				seenFns[v.Name] = true
				u.userFnCalls = append(u.userFnCalls, v.Name)
				u.callsUserFn = true
			}
			for _, arg := range v.Args {
				visitExpr(arg)
			}
		case *ast.BinaryExpr:
			visitExpr(v.Left)
			visitExpr(v.Right)
		case *ast.UnaryExpr:
			visitExpr(v.Operand)
		case *ast.ParenExpr:
			visitExpr(v.Expr)
		case *ast.IndexAccess:
			visitExpr(v.Base)
			visitExpr(v.Index)
		case *ast.ListExpr:
			for _, el := range v.Elements {
				visitExpr(el)
			}
		}
	}

	for _, s := range mw.Before {
		switch v := s.(type) {
		case *ast.GuardStmt:
			u.hasGuards = true
			visitExpr(v.Condition)
		case *ast.StepStmt:
			if fn, ok := v.Expr.(*ast.FnCall); ok {
				switch fn.Name {
				case "save", "fetch", "query", "update", "delete":
					u.touchesDB = true
				}
			}
			visitExpr(v.Expr)
		}
	}
	return u
}

func writeMiddlewareImports(b *strings.Builder, u middlewareUsage) {
	fastapi := []string{}
	if len(u.headerFields) > 0 {
		fastapi = append(fastapi, "Header")
	}
	if u.touchesDB {
		fastapi = append(fastapi, "Depends")
	}
	if u.hasGuards {
		fastapi = append(fastapi, "HTTPException")
	}
	if len(fastapi) > 0 {
		fmt.Fprintf(b, "from fastapi import %s\n", strings.Join(fastapi, ", "))
	}
	if u.touchesDB {
		b.WriteString("from sqlalchemy import select\n")
		b.WriteString("from sqlalchemy.orm import Session\n")
		b.WriteString("from src.lib.db import get_db\n")
		b.WriteString("from src.models import schema\n")
	}
	sort.Strings(u.userFnCalls)
	for _, name := range u.userFnCalls {
		fmt.Fprintf(b, "from src.functions.%s import %s\n", name, name)
	}
}

// isInjectStep reports whether a step is `|> inject <var> as <alias>`.
func isInjectStep(s *ast.StepStmt) bool {
	fn, ok := s.Expr.(*ast.FnCall)
	return ok && fn.Name == "inject"
}

// findInjectedVar returns the variable name an inject step exports, or "".
// Format: `|> inject <varIdent> as <aliasIdent>` — we return the var (LHS).
func findInjectedVar(mw *ast.Middleware) string {
	for _, s := range mw.Before {
		step, ok := s.(*ast.StepStmt)
		if !ok || !isInjectStep(step) {
			continue
		}
		fn := step.Expr.(*ast.FnCall)
		if len(fn.Args) >= 1 {
			if id, ok := fn.Args[0].(*ast.Ident); ok {
				return id.Name
			}
		}
	}
	return ""
}

// findInjectedAlias returns the alias an inject step exports (RHS of `as`).
// The endpoint signature will name its Depends-injected parameter after this.
// Returns the inject base var name as a fallback.
func findInjectedAlias(mw *ast.Middleware) string {
	for _, s := range mw.Before {
		step, ok := s.(*ast.StepStmt)
		if !ok || !isInjectStep(step) {
			continue
		}
		fn := step.Expr.(*ast.FnCall)
		if len(fn.Args) >= 2 {
			if id, ok := fn.Args[1].(*ast.Ident); ok {
				return id.Name
			}
		}
		if len(fn.Args) >= 1 {
			if id, ok := fn.Args[0].(*ast.Ident); ok {
				return id.Name
			}
		}
	}
	return ""
}

// findInjectedModel returns the model an inject step exports (so the endpoint
// signature can type the Depends-injected parameter as that model). Looks up
// the var bound earlier in the middleware body.
func findInjectedModel(mw *ast.Middleware) string {
	varName := findInjectedVar(mw)
	if varName == "" {
		return ""
	}
	for _, s := range mw.Before {
		step, ok := s.(*ast.StepStmt)
		if !ok || step.Binding != varName {
			continue
		}
		fn, ok := step.Expr.(*ast.FnCall)
		if !ok || len(fn.Args) == 0 {
			continue
		}
		if id, ok := fn.Args[0].(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// newMiddlewareBodyCtx mirrors newBodyCtx but seeded from a middleware. We
// pre-populate fact maps from the resolver so emitStep / emitGuard reuse
// every endpoint-body helper.
func newMiddlewareBodyCtx(mw *ast.Middleware, models []*ast.Model, userFns map[string]bool, headers []string) *bodyCtx {
	c := &bodyCtx{
		inputs:      map[string]bool{},
		varModel:    map[string]string{},
		cardinality: map[string]resolve.Cardinality{},
		fkAliases:   map[string]string{},
		models:      models,
		userFns:     userFns,
	}
	for _, h := range headers {
		c.inputs[common.SnakeCase(h)] = true
	}
	// Re-resolve facts. We don't have a global block-resolver function for
	// non-endpoint bodies yet; do a small walk inline for data-op bindings.
	for _, s := range mw.Before {
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
