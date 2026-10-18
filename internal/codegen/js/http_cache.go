package js

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// Endpoint `cache <duration>` on the Node target.
//
// A GET endpoint with `cache` gets a per-route httpCache middleware (see
// src/lib/http-cache.ts). It sets Cache-Control only on 2xx responses, and only
// when the handler has not already set one. The scope is `private` whenever the
// response could depend on the caller (any `use`, `auth`, `limit`, or a body
// that reads headers, cookies, session, or auth context) and `public` otherwise.

// validateEndpointCacheMetadata rejects malformed `cache` values before any
// files are generated, so a bad endpoint never yields a partial project.
func validateEndpointCacheMetadata(file *ast.File) error {
	if file == nil {
		return nil
	}
	for _, block := range file.Blocks {
		ep, ok := block.(*ast.Endpoint)
		if !ok {
			continue
		}
		for _, meta := range ep.Meta {
			if meta.Kind != "cache" {
				continue
			}
			if _, err := cacheMaxAgeSeconds(meta.Value); err != nil {
				return fmt.Errorf("node target cannot generate endpoint cache for %s %s at %s: %w", ep.Method, ep.Path, meta.Loc, err)
			}
		}
	}
	return nil
}

// cacheMaxAgeSeconds converts a `cache` value to whole seconds. It accepts only
// a duration literal with a unit (for example 5s, 5min, 1h) that is positive
// and a whole number of seconds.
func cacheMaxAgeSeconds(expr ast.Expr) (int, error) {
	dur, ok := expr.(*ast.DurationLit)
	if !ok {
		return 0, fmt.Errorf("cache expects a duration with a unit such as 5s or 5min, got %s", exprToString(expr))
	}
	number, multiplier, ok := splitDurationLiteral(dur.Value)
	if !ok {
		return 0, fmt.Errorf("cache duration %q is malformed; use a non-negative number with a unit such as 5s or 5min", dur.Value)
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, fmt.Errorf("cache duration %q is malformed", dur.Value)
	}
	totalMS := value * float64(multiplier)
	if totalMS <= 0 {
		return 0, fmt.Errorf("cache duration %q must be greater than zero", dur.Value)
	}
	if math.Mod(totalMS, 1000) != 0 {
		return 0, fmt.Errorf("cache duration %q must be a whole number of seconds", dur.Value)
	}
	return int(totalMS / 1000), nil
}

// cacheUserScopedIdents are identifiers whose presence in an endpoint body means
// the response may depend on the caller. `auth` is included because it is the
// injected auth context, even when the auth comes from a middleware.
var cacheUserScopedIdents = map[string]bool{
	"header":  true,
	"headers": true,
	"cookie":  true,
	"cookies": true,
	"session": true,
	"auth":    true,
}

// endpointCacheIsPrivate reports whether a cached response of ep could be
// specific to one caller. Such responses must use `private` so shared caches
// never store them. The check is intentionally conservative.
func endpointCacheIsPrivate(ep *ast.Endpoint, bp *ast.Blueprint) bool {
	if bp != nil && len(bp.Uses) > 0 {
		return true
	}
	for _, meta := range ep.Meta {
		switch meta.Kind {
		case "use", "auth", "limit":
			return true
		}
	}
	private := false
	walkStmts(ep.Stmts, func(expr ast.Expr) {
		if ident, ok := expr.(*ast.Ident); ok && cacheUserScopedIdents[ident.Name] {
			private = true
		}
	})
	return private
}

// endpointCacheMiddleware returns the httpCache(...) expression for a GET
// endpoint with a `cache` entry, or "" when the endpoint has no active cache.
// Values are checked by validateEndpointCacheMetadata before routes are emitted.
func endpointCacheMiddleware(ep *ast.Endpoint, bp *ast.Blueprint) string {
	if ep.Method != "GET" {
		return ""
	}
	for _, meta := range ep.Meta {
		if meta.Kind != "cache" {
			continue
		}
		seconds, err := cacheMaxAgeSeconds(meta.Value)
		if err != nil {
			return ""
		}
		scope := "public"
		if endpointCacheIsPrivate(ep, bp) {
			scope = "private"
		}
		return fmt.Sprintf("httpCache(%d, '%s')", seconds, scope)
	}
	return ""
}

// hasHTTPCacheEndpoints reports whether any endpoint needs the httpCache helper.
func hasHTTPCacheEndpoints(endpoints []*ast.Endpoint, bp *ast.Blueprint) bool {
	for _, ep := range endpoints {
		if endpointCacheMiddleware(ep, bp) != "" {
			return true
		}
	}
	return false
}

// genHTTPCache emits src/lib/http-cache.ts, the Hono middleware used by GET
// endpoints with `cache`.
func (g *Generator) genHTTPCache() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`import type { MiddlewareHandler } from 'hono';

// httpCache sets Cache-Control on successful (2xx) responses only. Error
// responses and responses that already carry Cache-Control are left unchanged.
export function httpCache(maxAgeSeconds: number, scope: 'public' | 'private'): MiddlewareHandler {
  const value = ` + "`${scope}, max-age=${maxAgeSeconds}`" + `;
  return async (c, next) => {
    await next();
    if (c.res.status >= 200 && c.res.status < 300 && !c.res.headers.has('Cache-Control')) {
      c.header('Cache-Control', value);
    }
  };
}
`)
	return codegen.OutputFile{Path: "src/lib/http-cache.ts", Content: []byte(b.String())}
}
