package checker

import (
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// claimsReservedHint is the fix shown for every C055 diagnostic.
const claimsReservedHint = "claims is reserved on auth jwt endpoints for the verified token payload; rename it"

// jwtClaimsEndpoint reports whether the endpoint's first enforced auth is
// `auth jwt using(secret.NAME)`. Only the jwt strategy exposes its verified
// payload as claims; bearer, api_key, and basic carry no claims. Codegen picks
// the same declaration (endpointEnforcedAuthMeta in the js generator).
func jwtClaimsEndpoint(meta []*ast.EndpointMeta) bool {
	for _, m := range meta {
		if m == nil || m.Kind != "auth" {
			continue
		}
		fn, ok := m.Value.(*ast.FnCall)
		if !ok || !enforcedAuthStrategies[fn.Name] || len(fn.Args) == 0 {
			continue
		}
		using, ok := fn.Args[0].(*ast.FnCall)
		if !ok || using.Name != "using" {
			continue
		}
		if _, ok := authUsingSecretName(using); ok {
			return fn.Name == "jwt"
		}
	}
	return false
}

// isJwtClaimsBinding reports whether sym is the claims identifier that an
// endpoint scope binds for its auth jwt declaration.
func isJwtClaimsBinding(sym *Symbol) bool {
	if sym == nil || sym.Name != "claims" || sym.Kind != SymVariable {
		return false
	}
	switch sym.Node.(type) {
	case *ast.Endpoint, *ast.StreamEndpoint, *ast.WsEndpoint:
		return true
	}
	return false
}

// checkReservedClaims rejects every declaration named claims on an auth jwt
// endpoint: a path parameter, a <- input, a step binding, an inject alias, or
// a use middleware that injects it. Those would shadow or collide with the
// generated claims value.
func (c *Checker) checkReservedClaims(owner ast.Node, path string, meta []*ast.EndpointMeta, stmts []ast.ArrowStmt, handlers []*ast.StreamHandler) {
	if !jwtClaimsEndpoint(meta) {
		return
	}
	reject := func(loc lexer.Loc) {
		c.addErrorCode(loc, CodeReservedClaims, "claims is reserved on auth jwt endpoints", claimsReservedHint)
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ":claims" {
			reject(owner.Location())
		}
	}
	for _, m := range meta {
		if m == nil || m.Kind != "use" || m.Use == nil {
			continue
		}
		sym := c.global.Lookup(m.Use.Name)
		if sym == nil || sym.Kind != SymMiddleware {
			continue
		}
		mw, ok := sym.Node.(*ast.Middleware)
		if !ok {
			continue
		}
		for _, injectStmts := range [][]ast.ArrowStmt{mw.Before, mw.After} {
			if _, injected := injectedArrowNames(injectStmts)["claims"]; injected {
				reject(m.Loc)
			}
		}
	}
	var walk func(stmts []ast.ArrowStmt)
	walk = func(stmts []ast.ArrowStmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.InputStmt:
				if s.Name == "claims" {
					reject(s.Loc)
				}
			case *ast.StepStmt:
				if s.Binding == "claims" {
					reject(s.Loc)
				}
				if call, ok := s.Expr.(*ast.FnCall); ok && call.Name == "inject" && len(call.Args) >= 2 {
					if alias, ok := call.Args[1].(*ast.Ident); ok && alias.Name == "claims" {
						reject(alias.Loc)
					}
				}
			case *ast.WhenStmt:
				walk(s.Body)
			case *ast.TryRecover:
				walk(s.Try)
				walk(s.Recover)
			}
		}
	}
	walk(stmts)
	for _, h := range handlers {
		if h != nil {
			walk(h.Body)
		}
	}
}
