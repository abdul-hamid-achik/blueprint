package linter

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// Issue represents a lint finding.
type Issue struct {
	File    string
	Line    int
	Col     int
	Level   string // "error", "warning", "info"
	Rule    string
	Message string
	Hint    string
}

func (i Issue) String() string {
	return fmt.Sprintf("%s:%d:%d [%s] %s: %s", i.File, i.Line, i.Col, i.Level, i.Rule, i.Message)
}

// Lint runs all lint rules against the parsed file and returns a list of issues.
func Lint(f *ast.File) []Issue {
	var issues []Issue
	issues = append(issues, checkBlockOrdering(f)...)
	issues = append(issues, checkIntentOnEndpoints(f)...)
	issues = append(issues, checkEmptyEndpoints(f)...)
	issues = append(issues, checkWherePredicateSelfEqual(f)...)
	issues = append(issues, checkUnusedInput(f)...)
	issues = append(issues, checkUnenforceableWsAuth(f)...)
	return issues
}

// blockPriority returns a numeric ordering priority for a top-level block type.
// Lower numbers come first in canonical order.
func blockPriority(block ast.TopLevel) int {
	switch block.(type) {
	case *ast.Blueprint:
		return 1
	case *ast.Include:
		return 2
	case *ast.Secret:
		return 3
	case *ast.Env:
		return 4
	case *ast.Locale, *ast.Translation:
		return 5
	case *ast.StateMachine:
		return 5
	case *ast.Analytics, *ast.SaveSchema:
		return 6
	case *ast.TypeDecl, *ast.Alias, *ast.Enum:
		return 5
	case *ast.Model:
		return 6
	case *ast.Content:
		return 6
	case *ast.Fn:
		return 7
	case *ast.Pipe:
		return 8
	case *ast.Middleware:
		return 9
	case *ast.Endpoint, *ast.StreamEndpoint, *ast.WsEndpoint:
		return 10
	case *ast.Worker:
		return 11
	case *ast.Schedule:
		return 12
	case *ast.External:
		return 13
	case *ast.Subscribe:
		return 14
	case *ast.Fixture:
		return 15
	case *ast.Test, *ast.TestGroup:
		return 16
	default:
		return 99
	}
}

// blockName returns a human-readable name for a top-level block.
func blockName(block ast.TopLevel) string {
	switch n := block.(type) {
	case *ast.Blueprint:
		return fmt.Sprintf("blueprint %q", n.Name)
	case *ast.Include:
		return fmt.Sprintf("include %q", n.Path)
	case *ast.Secret:
		return fmt.Sprintf("secret %s", n.Name)
	case *ast.Env:
		return fmt.Sprintf("env %s", n.Name)
	case *ast.Locale:
		return fmt.Sprintf("locale %s", n.Code)
	case *ast.Translation:
		return fmt.Sprintf("translation %s", n.Name)
	case *ast.StateMachine:
		return fmt.Sprintf("state %s", n.Name)
	case *ast.Analytics:
		return fmt.Sprintf("analytics %s", n.Name)
	case *ast.SaveSchema:
		return fmt.Sprintf("save %s", n.Name)
	case *ast.TypeDecl:
		return fmt.Sprintf("type %s", n.Name)
	case *ast.Alias:
		return fmt.Sprintf("alias %s", n.Name)
	case *ast.Enum:
		return fmt.Sprintf("enum %s", n.Name)
	case *ast.Model:
		return fmt.Sprintf("model %s", n.Name)
	case *ast.Content:
		return fmt.Sprintf("content %s", n.Name)
	case *ast.Fn:
		return fmt.Sprintf("fn %s", n.Name)
	case *ast.Pipe:
		return fmt.Sprintf("pipe %s", n.Name)
	case *ast.Middleware:
		return fmt.Sprintf("middleware %s", n.Name)
	case *ast.Endpoint:
		return fmt.Sprintf("%s %s", n.Method, n.Path)
	case *ast.StreamEndpoint:
		return fmt.Sprintf("STREAM %s", n.Path)
	case *ast.WsEndpoint:
		return fmt.Sprintf("WS %s", n.Path)
	case *ast.Worker:
		return fmt.Sprintf("worker %s", n.Name)
	case *ast.Schedule:
		return fmt.Sprintf("schedule %s", n.Name)
	case *ast.External:
		return fmt.Sprintf("external %q", n.Name)
	case *ast.Subscribe:
		return fmt.Sprintf("subscribe %q", n.Event)
	case *ast.Fixture:
		return fmt.Sprintf("fixture %q", n.Name)
	case *ast.Test:
		return fmt.Sprintf("test %s", n.Name)
	case *ast.TestGroup:
		return fmt.Sprintf("test_group %s", n.Name)
	default:
		return fmt.Sprintf("unknown %T", block)
	}
}

// blockTypeName returns the canonical category name for ordering messages.
func blockTypeName(block ast.TopLevel) string {
	switch block.(type) {
	case *ast.Blueprint:
		return "blueprint"
	case *ast.Include:
		return "include"
	case *ast.Secret:
		return "secret"
	case *ast.Env:
		return "env"
	case *ast.Locale:
		return "locale"
	case *ast.Translation:
		return "translation"
	case *ast.StateMachine:
		return "state"
	case *ast.Analytics:
		return "analytics"
	case *ast.SaveSchema:
		return "save"
	case *ast.TypeDecl:
		return "type"
	case *ast.Alias:
		return "alias"
	case *ast.Enum:
		return "enum"
	case *ast.Model:
		return "model"
	case *ast.Content:
		return "content"
	case *ast.Fn:
		return "fn"
	case *ast.Pipe:
		return "pipe"
	case *ast.Middleware:
		return "middleware"
	case *ast.Endpoint:
		return "endpoint"
	case *ast.StreamEndpoint:
		return "stream endpoint"
	case *ast.WsEndpoint:
		return "ws endpoint"
	case *ast.Worker:
		return "worker"
	case *ast.Schedule:
		return "schedule"
	case *ast.External:
		return "external"
	case *ast.Subscribe:
		return "subscribe"
	case *ast.Fixture:
		return "fixture"
	case *ast.Test:
		return "test"
	case *ast.TestGroup:
		return "test_group"
	default:
		return "unknown"
	}
}

// checkBlockOrdering checks that top-level blocks follow the canonical ordering from SPEC §3.1.
func checkBlockOrdering(f *ast.File) []Issue {
	var issues []Issue

	allBlocks := make([]ast.TopLevel, 0, 1+len(f.Blocks))
	if f.Blueprint != nil {
		allBlocks = append(allBlocks, f.Blueprint)
	}
	allBlocks = append(allBlocks, f.Blocks...)

	maxPriority := 0
	for _, block := range allBlocks {
		prio := blockPriority(block)
		if prio < maxPriority {
			loc := block.Location()
			issues = append(issues, Issue{
				File:    loc.File,
				Line:    loc.Line,
				Col:     loc.Col,
				Level:   "warning",
				Rule:    "block-ordering",
				Message: fmt.Sprintf("%s should appear before %s blocks", blockName(block), blockTypeName(block)),
				Hint:    "Reorder blocks to follow the canonical order: blueprint, include, secret, env, type/alias/enum, model, fn, pipe, middleware, endpoint, worker, schedule, external, subscribe, fixture, test",
			})
		} else {
			maxPriority = prio
		}
	}

	return issues
}

// checkIntentOnEndpoints checks that all endpoints have an intent annotation.
func checkIntentOnEndpoints(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		switch n := block.(type) {
		case *ast.Endpoint:
			if n.Intent == nil {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "intent-on-endpoints",
					Message: fmt.Sprintf("Endpoint %s %s is missing an @ intent description", n.Method, n.Path),
					Hint:    fmt.Sprintf("Add `@ \"describe what this endpoint does\"` before `%s %s`", n.Method, n.Path),
				})
			}
		case *ast.StreamEndpoint:
			if n.Intent == nil {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "intent-on-endpoints",
					Message: fmt.Sprintf("Endpoint STREAM %s is missing an @ intent description", n.Path),
					Hint:    fmt.Sprintf("Add `@ \"describe what this stream does\"` before `STREAM %s`", n.Path),
				})
			}
		case *ast.WsEndpoint:
			if n.Intent == nil {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "intent-on-endpoints",
					Message: fmt.Sprintf("Endpoint WS %s is missing an @ intent description", n.Path),
					Hint:    fmt.Sprintf("Add `@ \"describe what this websocket does\"` before `WS %s`", n.Path),
				})
			}
		}
	}

	return issues
}

// checkEmptyEndpoints checks for endpoints with no inputs and no arrow statements.
func checkEmptyEndpoints(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		switch n := block.(type) {
		case *ast.Endpoint:
			hasInputs := false
			for _, stmt := range n.Stmts {
				if _, ok := stmt.(*ast.InputStmt); ok {
					hasInputs = true
					break
				}
			}
			if !hasInputs && len(n.Stmts) == 0 {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "empty-endpoint",
					Message: fmt.Sprintf("Endpoint %s %s has no inputs or statements", n.Method, n.Path),
					Hint:    "Add inputs (`<-`) or statements (`|>`, `->`) to implement this endpoint",
				})
			}
		case *ast.StreamEndpoint:
			if len(n.Stmts) == 0 && len(n.Handlers) == 0 {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "empty-endpoint",
					Message: fmt.Sprintf("Endpoint STREAM %s has no inputs or statements", n.Path),
					Hint:    "Add inputs or statements to implement this stream endpoint",
				})
			}
		case *ast.WsEndpoint:
			if len(n.OnConnect) == 0 && len(n.OnMessage) == 0 && len(n.OnDisconnect) == 0 {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "empty-endpoint",
					Message: fmt.Sprintf("Endpoint WS %s has no on_connect/on_message/on_disconnect handlers", n.Path),
					Hint:    "Add on_connect, on_message, or on_disconnect blocks to implement this WebSocket endpoint",
				})
			}
		}
	}

	return issues
}

// checkUnenforceableWsAuth flags `auth` meta on WS endpoints that codegen
// cannot enforce. genWsRoute only wires real enforcement for `auth webhook_sig
// using(secret.NAME)` (an HMAC signature check run as middleware before the
// handshake) — bare identifiers like `bearer`, `jwt`, `api_key(...)`, or
// `basic` have no generic verification codegen anywhere in this target (the
// same is true for REST's genRoute), so the connection ships without the
// declared auth actually being checked unless it's verified by hand (e.g. in
// on_connect, or via a `use` middleware).
func checkUnenforceableWsAuth(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		ws, ok := block.(*ast.WsEndpoint)
		if !ok {
			continue
		}
		for _, meta := range ws.Meta {
			if meta.Kind != "auth" || isWebhookSigAuth(meta.Value) {
				continue
			}
			issues = append(issues, Issue{
				File:    meta.Loc.File,
				Line:    meta.Loc.Line,
				Col:     meta.Loc.Col,
				Level:   "warning",
				Rule:    "unenforceable-ws-auth",
				Message: fmt.Sprintf("auth %s on WS %s is not enforced by codegen", authMetaName(meta.Value), ws.Path),
				Hint:    "WS transports only auto-enforce `auth webhook_sig using(secret.NAME)` — verify bearer/jwt/api_key/basic tokens yourself (e.g. in on_connect, or via a `use` middleware) or the connection ships unauthenticated",
			})
		}
	}

	return issues
}

// isWebhookSigAuth reports whether an auth meta value is a `webhook_sig` call
// — the only auth form genWsRoute/genStreamRoute/genRoute actually enforce.
func isWebhookSigAuth(expr ast.Expr) bool {
	fn, ok := expr.(*ast.FnCall)
	return ok && fn.Name == "webhook_sig"
}

// authMetaName renders an auth meta value's identifying name for messages
// (e.g. "bearer", "api_key", or "basic").
func authMetaName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.FnCall:
		return v.Name
	default:
		return "auth"
	}
}

// checkWherePredicateSelfEqual flags `where(X == X)` predicates where both
// sides are the same identifier name. In Blueprint's data-op convention the
// left side is taken to be a column reference and the right side an in-scope
// variable; when both identifiers are spelled the same the intent is
// ambiguous to the reader. We only warn when there is NO in-scope binding
// (input, step binding, or for-loop variable) with that name — i.e. the
// expression cannot be disambiguated by the codegen's positional convention
// and the comparison really would be column-against-itself.
func checkWherePredicateSelfEqual(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		bindings := collectScopeBindings(block)
		walkArrowContainersForWhere(block, func(stmts []ast.ArrowStmt) {
			for _, stmt := range stmts {
				issues = append(issues, findSelfEqualWheres(stmt, bindings)...)
			}
		})
	}

	return issues
}

// findSelfEqualWheres recursively inspects expressions within an arrow
// statement looking for FnCall{Name:"where"} whose args are BinaryExpr with
// Op "==" and both sides identical Ident.Name, where that name is not bound
// in scope.
func findSelfEqualWheres(stmt ast.ArrowStmt, bindings map[string]bool) []Issue {
	var issues []Issue
	switch n := stmt.(type) {
	case *ast.StepStmt:
		issues = append(issues, scanExprForSelfEqualWhere(n.Expr, bindings)...)
	case *ast.GuardStmt:
		issues = append(issues, scanExprForSelfEqualWhere(n.Condition, bindings)...)
	case *ast.WhenStmt:
		issues = append(issues, scanExprForSelfEqualWhere(n.Condition, bindings)...)
		issues = append(issues, scanExprForSelfEqualWhere(n.Inline, bindings)...)
		for _, s := range n.Body {
			issues = append(issues, findSelfEqualWheres(s, bindings)...)
		}
	case *ast.OutputStmt:
		issues = append(issues, scanExprForSelfEqualWhere(n.Value, bindings)...)
	case *ast.TryRecover:
		for _, s := range n.Try {
			issues = append(issues, findSelfEqualWheres(s, bindings)...)
		}
		for _, s := range n.Recover {
			issues = append(issues, findSelfEqualWheres(s, bindings)...)
		}
	}
	return issues
}

func scanExprForSelfEqualWhere(e ast.Expr, bindings map[string]bool) []Issue {
	var issues []Issue
	if e == nil {
		return issues
	}
	switch n := e.(type) {
	case *ast.FnCall:
		if n.Name == "where" {
			for _, arg := range n.Args {
				if bin, ok := arg.(*ast.BinaryExpr); ok && bin.Op == "==" {
					leftIdent, lok := bin.Left.(*ast.Ident)
					rightIdent, rok := bin.Right.(*ast.Ident)
					if lok && rok && leftIdent.Name == rightIdent.Name {
						if !bindings[leftIdent.Name] {
							loc := bin.Loc
							issues = append(issues, Issue{
								File:    loc.File,
								Line:    loc.Line,
								Col:     loc.Col,
								Level:   "warning",
								Rule:    "where-predicate-self-equal",
								Message: fmt.Sprintf("where predicate `%s == %s` compares an identifier to itself; no input or binding named %q is in scope", leftIdent.Name, rightIdent.Name, leftIdent.Name),
								Hint:    fmt.Sprintf("Did you mean `%s == :<input-name>`? Add an `<- %s ...` input or reference a bound variable on the right side.", leftIdent.Name, leftIdent.Name),
							})
						}
					}
				}
				// Also recurse into the arg for nested where(...) (defensive).
				issues = append(issues, scanExprForSelfEqualWhere(arg, bindings)...)
			}
		}
		// Recurse into all args regardless (e.g. query model where(...) order(...)).
		for _, a := range n.Args {
			issues = append(issues, scanExprForSelfEqualWhere(a, bindings)...)
		}
	case *ast.BinaryExpr:
		issues = append(issues, scanExprForSelfEqualWhere(n.Left, bindings)...)
		issues = append(issues, scanExprForSelfEqualWhere(n.Right, bindings)...)
	case *ast.UnaryExpr:
		issues = append(issues, scanExprForSelfEqualWhere(n.Operand, bindings)...)
	case *ast.FieldAccess:
		issues = append(issues, scanExprForSelfEqualWhere(n.Base, bindings)...)
	case *ast.IndexAccess:
		issues = append(issues, scanExprForSelfEqualWhere(n.Base, bindings)...)
		issues = append(issues, scanExprForSelfEqualWhere(n.Index, bindings)...)
	case *ast.ParenExpr:
		issues = append(issues, scanExprForSelfEqualWhere(n.Expr, bindings)...)
	case *ast.ListExpr:
		for _, el := range n.Elements {
			issues = append(issues, scanExprForSelfEqualWhere(el, bindings)...)
		}
	case *ast.BlockExpr:
		for _, kv := range n.Entries {
			issues = append(issues, scanExprForSelfEqualWhere(kv.Value, bindings)...)
		}
	}
	return issues
}

// walkArrowContainersForWhere invokes fn for every []ArrowStmt slice that
// belongs to the given top-level block. This lets the where-predicate rule
// run against every place a `where(...)` can appear (endpoint stmts, worker
// stmts, stream handlers, ws handlers, fn logic, …).
func walkArrowContainersForWhere(block ast.TopLevel, fn func([]ast.ArrowStmt)) {
	switch n := block.(type) {
	case *ast.Endpoint:
		fn(n.Stmts)
	case *ast.StreamEndpoint:
		fn(n.Stmts)
		for _, h := range n.Handlers {
			fn(h.Body)
		}
	case *ast.WsEndpoint:
		fn(n.OnConnect)
		fn(n.OnMessage)
		fn(n.OnDisconnect)
	case *ast.Worker:
		fn(n.Stmts)
		fn(n.OnFail)
	case *ast.Schedule:
		fn(n.Stmts)
	case *ast.Subscribe:
		fn(n.Stmts)
	case *ast.Pipe:
		fn(n.Stmts)
	case *ast.Middleware:
		fn(n.Before)
		fn(n.After)
	case *ast.Fn:
		if n.Logic != nil {
			fn(n.Logic.Stmts)
		}
	}
}

// collectScopeBindings returns the set of identifier names bound in the
// given top-level block: inputs (<-), step bindings (|> x = ...), URL path
// params (e.g. /api/foo/:id binds id), and meta names referenced in stream
// handlers (event name).
func collectScopeBindings(block ast.TopLevel) map[string]bool {
	bindings := map[string]bool{}
	var stmts []ast.ArrowStmt
	var path string

	switch n := block.(type) {
	case *ast.Endpoint:
		stmts = n.Stmts
		path = n.Path
	case *ast.StreamEndpoint:
		stmts = n.Stmts
		path = n.Path
		for _, h := range n.Handlers {
			stmts = append(stmts, h.Body...)
		}
	case *ast.WsEndpoint:
		stmts = append(stmts, n.OnConnect...)
		stmts = append(stmts, n.OnMessage...)
		stmts = append(stmts, n.OnDisconnect...)
		path = n.Path
	case *ast.Worker:
		stmts = append(stmts, n.Stmts...)
		stmts = append(stmts, n.OnFail...)
	case *ast.Schedule:
		stmts = n.Stmts
	case *ast.Subscribe:
		stmts = n.Stmts
	case *ast.Pipe:
		stmts = n.Stmts
	case *ast.Middleware:
		stmts = append(stmts, n.Before...)
		stmts = append(stmts, n.After...)
	case *ast.Fn:
		for _, in := range n.Inputs {
			bindings[in.Name] = true
		}
		if n.Logic != nil {
			stmts = n.Logic.Stmts
		}
	}

	// URL path params: tokens prefixed with ':' bind identifiers.
	for _, seg := range splitPath(path) {
		if len(seg) > 1 && seg[0] == ':' {
			bindings[seg[1:]] = true
		}
	}

	collectBindingsFromStmts(stmts, bindings)
	return bindings
}

func collectBindingsFromStmts(stmts []ast.ArrowStmt, bindings map[string]bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.InputStmt:
			bindings[n.Name] = true
		case *ast.StepStmt:
			if n.Binding != "" {
				bindings[n.Binding] = true
			}
		case *ast.WhenStmt:
			collectBindingsFromStmts(n.Body, bindings)
		case *ast.TryRecover:
			collectBindingsFromStmts(n.Try, bindings)
			collectBindingsFromStmts(n.Recover, bindings)
		}
	}
}

// splitPath splits a URL path on '/' returning each segment.
func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if i > start {
				out = append(out, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		out = append(out, path[start:])
	}
	return out
}

// checkUnusedInput flags `<- foo type ...` inputs that are never referenced
// in the endpoint body (steps, output, guard conditions, etc).
func checkUnusedInput(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		issues = append(issues, unusedInputsForBlock(block)...)
	}

	return issues
}

func unusedInputsForBlock(block ast.TopLevel) []Issue {
	var inputs []*ast.InputStmt
	var bodies [][]ast.ArrowStmt

	var ctxName string
	switch n := block.(type) {
	case *ast.Endpoint:
		ctxName = fmt.Sprintf("%s %s", n.Method, n.Path)
		for _, s := range n.Stmts {
			if in, ok := s.(*ast.InputStmt); ok {
				inputs = append(inputs, in)
			}
		}
		bodies = append(bodies, n.Stmts)
	case *ast.StreamEndpoint:
		ctxName = fmt.Sprintf("STREAM %s", n.Path)
		for _, s := range n.Stmts {
			if in, ok := s.(*ast.InputStmt); ok {
				inputs = append(inputs, in)
			}
		}
		bodies = append(bodies, n.Stmts)
		for _, h := range n.Handlers {
			bodies = append(bodies, h.Body)
		}
	case *ast.WsEndpoint:
		ctxName = fmt.Sprintf("WS %s", n.Path)
		for _, s := range n.OnConnect {
			if in, ok := s.(*ast.InputStmt); ok {
				inputs = append(inputs, in)
			}
		}
		bodies = append(bodies, n.OnConnect, n.OnMessage, n.OnDisconnect)
	default:
		return nil
	}

	if len(inputs) == 0 {
		return nil
	}

	// Collect all identifier references that appear anywhere in the body
	// (excluding the input statements themselves).
	used := map[string]bool{}
	for _, body := range bodies {
		for _, stmt := range body {
			if _, ok := stmt.(*ast.InputStmt); ok {
				continue
			}
			collectIdentRefsFromStmt(stmt, used)
		}
	}

	var issues []Issue
	for _, in := range inputs {
		if used[in.Name] {
			continue
		}
		// Auto-applied search/filter inputs are picked up by the codegen by
		// name even without an explicit AST reference; skip them so we
		// don't false-positive on the common `<- search string optional`
		// pattern.
		if isAutoAppliedSearchInput(in.Name) {
			continue
		}
		issues = append(issues, Issue{
			File:    in.Loc.File,
			Line:    in.Loc.Line,
			Col:     in.Loc.Col,
			Level:   "warning",
			Rule:    "unused-input",
			Message: fmt.Sprintf("Input %q in endpoint %s is never referenced in the body", in.Name, ctxName),
			Hint:    "Remove the input or use it in the endpoint body",
		})
	}
	return issues
}

func collectIdentRefsFromStmt(stmt ast.ArrowStmt, used map[string]bool) {
	switch n := stmt.(type) {
	case *ast.StepStmt:
		collectIdentRefsFromExpr(n.Expr, used)
	case *ast.GuardStmt:
		collectIdentRefsFromExpr(n.Condition, used)
	case *ast.WhenStmt:
		collectIdentRefsFromExpr(n.Condition, used)
		collectIdentRefsFromExpr(n.Inline, used)
		for _, s := range n.Body {
			collectIdentRefsFromStmt(s, used)
		}
	case *ast.OutputStmt:
		collectIdentRefsFromExpr(n.Value, used)
	case *ast.TryRecover:
		for _, s := range n.Try {
			collectIdentRefsFromStmt(s, used)
		}
		for _, s := range n.Recover {
			collectIdentRefsFromStmt(s, used)
		}
	}
}

func collectIdentRefsFromExpr(e ast.Expr, used map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Ident:
		used[n.Name] = true
	case *ast.StringLit:
		// String interpolation: any `{ident...}` segment references identifiers.
		for _, name := range extractInterpolationIdents(n.Value) {
			used[name] = true
		}
	case *ast.BinaryExpr:
		collectIdentRefsFromExpr(n.Left, used)
		collectIdentRefsFromExpr(n.Right, used)
	case *ast.UnaryExpr:
		collectIdentRefsFromExpr(n.Operand, used)
	case *ast.FnCall:
		for _, a := range n.Args {
			collectIdentRefsFromExpr(a, used)
		}
	case *ast.FieldAccess:
		collectIdentRefsFromExpr(n.Base, used)
	case *ast.IndexAccess:
		collectIdentRefsFromExpr(n.Base, used)
		collectIdentRefsFromExpr(n.Index, used)
	case *ast.ParenExpr:
		collectIdentRefsFromExpr(n.Expr, used)
	case *ast.ListExpr:
		for _, el := range n.Elements {
			collectIdentRefsFromExpr(el, used)
		}
	case *ast.BlockExpr:
		for _, kv := range n.Entries {
			collectIdentRefsFromExpr(kv.Value, used)
			// Block expr keys may also reference inputs as shorthand
			// (`{ name: name }` already covered by the value walk; the
			// key itself is a label, not a reference).
		}
	}
}

// extractInterpolationIdents walks a string template and returns the
// identifier root of every `{expr}` segment. For `"Hello {user.name}!"` it
// returns ["user"]; for `"{name} is {count} long"` it returns
// ["name", "count"]. Used to detect inputs referenced only inside string
// interpolations (e.g. `-> 200 { msg: "Hello, {name}!" }`).
func extractInterpolationIdents(s string) []string {
	var idents []string
	i := 0
	for i < len(s) {
		if s[i] == '{' {
			i++
			var body strings.Builder
			depth := 1
			for i < len(s) && depth > 0 {
				if s[i] == '{' {
					depth++
				} else if s[i] == '}' {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
				body.WriteByte(s[i])
				i++
			}
			text := strings.TrimSpace(body.String())
			// Take the head identifier (before '.', '[', '(', whitespace, etc.).
			end := 0
			for end < len(text) {
				c := text[end]
				if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
					end++
					continue
				}
				break
			}
			if end > 0 {
				idents = append(idents, text[:end])
			}
		} else {
			i++
		}
	}
	return idents
}

// isAutoAppliedSearchInput reports whether an input name matches the codegen
// convention that auto-applies it as a search filter (case-insensitive LIKE
// across the model's text columns) without an explicit AST reference.
func isAutoAppliedSearchInput(name string) bool {
	switch name {
	case "q", "search", "query", "keyword", "term", "filter":
		return true
	}
	return false
}
