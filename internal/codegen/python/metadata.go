package python

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// Endpoint metadata that the Python target emits: `tags`, `cache`, and `limit`.
// Each mirrors the Node target (internal/codegen/js/http_cache.go and the
// `limit` case in routes.go). The Node helpers are unexported in package js, so
// the small pieces needed here are restated instead of importing that package.

// pythonCacheUserScopedIdents are identifiers whose presence in an endpoint
// body means the response may depend on the caller. Same list as Node.
var pythonCacheUserScopedIdents = map[string]bool{
	"header":  true,
	"headers": true,
	"cookie":  true,
	"cookies": true,
	"session": true,
	"auth":    true,
}

// cacheMaxAgeSeconds converts a `cache` value to whole seconds. It accepts only
// a positive duration literal with a unit that is a whole number of seconds.
func cacheMaxAgeSeconds(expr ast.Expr) (int, error) {
	dur, ok := expr.(*ast.DurationLit)
	if !ok {
		return 0, fmt.Errorf("cache expects a duration with a unit such as 5s or 5min")
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

// splitDurationLiteral splits "5min" into ("5", 60000). Units are checked
// longest-first so "hours" is not read as "s".
func splitDurationLiteral(s string) (string, int, bool) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix     string
		multiplier int
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
	for _, unit := range units {
		if !strings.HasSuffix(s, unit.suffix) {
			continue
		}
		number := strings.TrimSpace(strings.TrimSuffix(s, unit.suffix))
		value, err := strconv.ParseFloat(number, 64)
		if err != nil || value < 0 {
			return "", 0, false
		}
		return number, unit.multiplier, true
	}
	return "", 0, false
}

// endpointCacheIsPrivate reports whether a cached response could be specific to
// one caller, in which case the header must say `private` so shared caches do
// not store it. The check is conservative, as on Node.
func endpointCacheIsPrivate(ep *ast.Endpoint) bool {
	for _, meta := range ep.Meta {
		switch meta.Kind {
		case "use", "auth", "limit":
			return true
		}
	}
	private := false
	walkEndpointExprs(ep, func(expr ast.Expr) {
		if ident, ok := expr.(*ast.Ident); ok && pythonCacheUserScopedIdents[ident.Name] {
			private = true
		}
	})
	return private
}

// endpointCacheValue returns the Cache-Control value for a GET endpoint with a
// `cache` entry, or "" when the endpoint sets no header. Non-GET endpoints get
// no header, as on Node.
func endpointCacheValue(ep *ast.Endpoint) string {
	if !strings.EqualFold(ep.Method, "GET") {
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
		if endpointCacheIsPrivate(ep) {
			scope = "private"
		}
		return fmt.Sprintf("%s, max-age=%d", scope, seconds)
	}
	return ""
}

// endpointRateLimit returns the request budget and fixed window in milliseconds
// for an endpoint's `limit` entry. The lexer only produces min, hour, and day
// units for rates, so every accepted value maps to a window.
func endpointRateLimit(ep *ast.Endpoint) (int, int, bool) {
	for _, meta := range ep.Meta {
		if meta.Kind != "limit" {
			continue
		}
		rate, ok := meta.Value.(*ast.RateLit)
		if !ok {
			return 0, 0, false
		}
		limit, window, ok := parsePythonRate(rate.Value)
		return limit, window, ok
	}
	return 0, 0, false
}

// parsePythonRate parses "60/min" the way Node's parseRateLimit does for the
// units the lexer accepts.
func parsePythonRate(rate string) (int, int, bool) {
	parts := strings.SplitN(rate, "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	limit, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || limit < 0 {
		return 0, 0, false
	}
	switch strings.TrimSpace(parts[1]) {
	case "min":
		return limit, 60 * 1000, true
	case "hour":
		return limit, 60 * 60 * 1000, true
	case "day":
		return limit, 24 * 60 * 60 * 1000, true
	}
	return 0, 0, false
}

// endpointTags returns the string literals of a `tags` entry.
func endpointTags(ep *ast.Endpoint) []string {
	for _, meta := range ep.Meta {
		if meta.Kind != "tags" {
			continue
		}
		list, ok := meta.Value.(*ast.ListExpr)
		if !ok {
			return nil
		}
		var out []string
		for _, el := range list.Elements {
			if s, ok := el.(*ast.StringLit); ok {
				out = append(out, s.Value)
			}
		}
		return out
	}
	return nil
}

// pythonMetaFeature validates one non-`use` endpoint metadata entry. handled is
// false for kinds this file does not own (auth, timeout, ...), which the caller
// rejects. A non-empty reason rejects the entry with a specific message.
func pythonMetaFeature(meta *ast.EndpointMeta) (reason string, handled bool) {
	switch meta.Kind {
	case "tags":
		list, ok := meta.Value.(*ast.ListExpr)
		if !ok {
			return "endpoint `tags` metadata that is not a list", true
		}
		for _, el := range list.Elements {
			if _, ok := el.(*ast.StringLit); !ok {
				return "endpoint `tags` metadata with non-string entries", true
			}
		}
		return "", true
	case "cache":
		if _, err := cacheMaxAgeSeconds(meta.Value); err != nil {
			return "endpoint `cache` metadata: " + err.Error(), true
		}
		return "", true
	case "limit":
		if _, ok := meta.Value.(*ast.RateLit); !ok {
			return "endpoint `limit` metadata that is not a rate such as 60/min", true
		}
		if _, _, ok := parsePythonRate(meta.Value.(*ast.RateLit).Value); !ok {
			return "endpoint `limit` metadata with an unsupported rate", true
		}
		return "", true
	}
	return "", false
}

// usesEndpointMeta reports whether any endpoint carries the given metadata
// kind, which decides whether the shared helper modules are emitted.
func usesEndpointMeta(endpoints []*ast.Endpoint, kind string) bool {
	for _, ep := range endpoints {
		for _, meta := range ep.Meta {
			if meta.Kind == kind {
				return true
			}
		}
	}
	return false
}

// anyEndpointCache reports whether any endpoint sets a Cache-Control header.
func anyEndpointCache(endpoints []*ast.Endpoint) bool {
	for _, ep := range endpoints {
		if endpointCacheValue(ep) != "" {
			return true
		}
	}
	return false
}

// wrapCacheReturn wraps a response expression so a 2xx response carries the
// endpoint's Cache-Control. Non-2xx responses (including HTTPException raised
// by guards) pass through unchanged, so they never carry the header.
func wrapCacheReturn(expr, cacheValue string) string {
	if cacheValue == "" {
		return expr
	}
	return fmt.Sprintf("cache_control(%s, %q)", expr, cacheValue)
}

// genHTTPCachePy emits src/lib/http_cache.py, the helper that sets Cache-Control
// on successful responses. FastAPI ignores headers set on an injected Response
// when the handler returns its own Response object, which generated handlers
// always do, so the header is set on the returned object instead.
func (g *Generator) genHTTPCachePy() codegen.OutputFile {
	content := fileHeader(g.sourceFile) + `"""Cache-Control for endpoints declared with ` + "`cache`" + `. Stdlib and FastAPI only."""

from fastapi.responses import Response


def cache_control(response: Response, value: str) -> Response:
    """Set Cache-Control on a 2xx response that does not already carry one."""
    if 200 <= response.status_code < 300 and "cache-control" not in response.headers:
        response.headers["Cache-Control"] = value
    return response
`
	return codegen.OutputFile{Path: "src/lib/http_cache.py", Content: []byte(content)}
}

// genRateLimitPy emits src/lib/rate_limit.py, an in-process fixed-window limiter
// keyed by x-forwarded-for (or "unknown") and the request path, as on Node.
// Handlers run in a threadpool when they are sync, so the store is locked.
func (g *Generator) genRateLimitPy() codegen.OutputFile {
	content := fileHeader(g.sourceFile) + `"""In-process fixed-window rate limit for endpoints declared with ` + "`limit`" + `."""

import threading
import time

from fastapi import Request
from fastapi.responses import JSONResponse

_lock = threading.Lock()
_store: dict[str, tuple[int, float]] = {}
_last_sweep = 0.0
_SWEEP_INTERVAL_MS = 60_000


def check(request: Request, limit: int, window_ms: int) -> JSONResponse | None:
    """Count one request for this client and path.

    Returns a 429 response once the window's limit is reached, otherwise None.
    """
    global _last_sweep
    now = time.monotonic() * 1000
    client = request.headers.get("x-forwarded-for") or "unknown"
    key = f"{client}:{request.url.path}"
    with _lock:
        if now - _last_sweep >= _SWEEP_INTERVAL_MS:
            for stale in [k for k, (_, reset_at) in _store.items() if reset_at <= now]:
                del _store[stale]
            _last_sweep = now
        entry = _store.get(key)
        if entry is not None and entry[1] > now:
            count, reset_at = entry
            if count >= limit:
                return JSONResponse({"error": "Rate limit exceeded"}, status_code=429)
            _store[key] = (count + 1, reset_at)
        else:
            _store[key] = (1, now + window_ms)
    return None
`
	return codegen.OutputFile{Path: "src/lib/rate_limit.py", Content: []byte(content)}
}
