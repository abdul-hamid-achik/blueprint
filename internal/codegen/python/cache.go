package python

import "github.com/abdul-hamid-achik/blueprint/internal/codegen"

// genCachePy emits src/lib/cache.py — a Redis client wrapper with a tiny
// `cached(key, ttl_seconds, fn)` helper that mirrors the JS target's
// signature so spec-level `cache(...)` markers translate uniformly.
func (g *Generator) genCachePy() codegen.OutputFile {
	content := fileHeader(g.sourceFile) + `import json
from typing import Any, Callable

import redis

from src.lib.env import env

redis_client = redis.Redis.from_url(env.REDIS_URL, decode_responses=True)


def cached(key: str, ttl_seconds: int, fn: Callable[[], Any]) -> Any:
    """Return cached JSON for ` + "`key`" + ` or compute via ` + "`fn()`" + `, cache it, and return."""
    hit = redis_client.get(key)
    if hit is not None:
        return json.loads(hit)
    result = fn()
    redis_client.set(key, json.dumps(result), ex=ttl_seconds)
    return result
`
	return codegen.OutputFile{Path: "src/lib/cache.py", Content: []byte(content)}
}
