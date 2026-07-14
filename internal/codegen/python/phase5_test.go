package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 5 unit tests cover the supported cache (redis), middleware dispatch
// via `use`, and `sum()` rewrite. Realtime STREAM/WS declarations now fail
// closed at Generator.Files until Phase 5b translates their handler bodies;
// generator_test.go pins that public boundary.

// ---------- Phase 5: realtime fail-closed boundary ----------

// STREAM and WS route emitters remain in the package as Phase 5b groundwork,
// but Generator.Files must reject those declarations until their authored
// event/lifecycle bodies execute rather than becoming ping loops or comments.
// The precise unsupported-feature cases live in generator_test.go so every
// successful public generator entrypoint is covered.

// ---------- Phase 5: cache redis ----------

// TestPython_Phase5CacheRedisEmitted covers genCachePy — emitted only when
// `cache redis` appears in the blueprint block. We assert the redis import,
// the cached() helper, REDIS_URL in env, and the redis dep in pyproject.toml.
func TestPython_Phase5CacheRedisEmitted(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node cache redis }
GET /api/x { -> 200 "ok" }
`
	outDir := buildPython(t, src)

	cache := readPy(t, outDir, "src/lib/cache.py")
	for _, want := range []string{
		"import redis",
		"redis_client = redis.Redis.from_url(env.REDIS_URL, decode_responses=True)",
		"def cached(key: str, ttl_seconds: int, fn: Callable[[], Any]) -> Any:",
		"hit = redis_client.get(key)",
		"redis_client.set(key, json.dumps(result), ex=ttl_seconds)",
	} {
		if !strings.Contains(cache, want) {
			t.Errorf("cache.py missing %q, got:\n%s", want, cache)
		}
	}

	pyproject := readPy(t, outDir, "pyproject.toml")
	if !strings.Contains(pyproject, `"redis>=5.0"`) {
		t.Errorf("pyproject.toml should pin redis>=5.0, got:\n%s", pyproject)
	}

	env := readPy(t, outDir, "src/lib/env.py")
	if !strings.Contains(env, "REDIS_URL") {
		t.Errorf("env.py should declare REDIS_URL when cache redis is on, got:\n%s", env)
	}
}

// TestPython_Phase5CacheNotEmittedWithoutDeclaration confirms the cache.py
// helper is opt-in — without `cache redis` we don't drop a Redis client in.
func TestPython_Phase5CacheNotEmittedWithoutDeclaration(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	if _, err := readMaybe(outDir, "src/lib/cache.py"); err == nil {
		t.Errorf("cache.py should not be emitted without `cache redis`")
	}
	pyproject := readPy(t, outDir, "pyproject.toml")
	if strings.Contains(pyproject, `"redis>=5.0"`) {
		t.Errorf("pyproject.toml should not pin redis without `cache redis`, got:\n%s", pyproject)
	}
}

// ---------- Phase 5: middleware + `use` dispatch ----------

// TestPython_Phase5MiddlewareInjectedViaUse covers genMiddlewares +
// genMiddleware + scanMiddlewareUsage + writeMiddlewareImports plus the
// endpoint side: the route file imports the middleware module and wires a
// Depends(<mw>) parameter named after the alias from `inject ... as`.
func TestPython_Phase5MiddlewareInjectedViaUse(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model user { id uuid primary  email string required }
fn verify_jwt { <- token string  -> json  impl node { module: "./auth", func: "verify" } }

middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "missing"
    |> payload = verify_jwt(header.Authorization)
    |> user = fetch user(payload.sub)
    |> inject user as current_user
  }
}

GET /api/me {
  use require_auth
  -> 200 { id: current_user.id, email: current_user.email }
}
`
	outDir := buildPython(t, src)

	// Middleware module: scanMiddlewareUsage picked up the header field, the
	// DB op, the guard, and the user-fn call; writeMiddlewareImports emitted
	// exactly those imports.
	mw := readPy(t, outDir, "src/middleware/require_auth.py")
	for _, want := range []string{
		"from fastapi import Header, Depends, HTTPException",
		"from sqlalchemy.orm import Session",
		"from src.lib.db import get_db",
		"from src.models import schema",
		"from src.functions.verify_jwt import verify_jwt",
		// Signature: Authorization header param + db Session
		`def require_auth(authorization: str | None = Header(None, alias="Authorization"), db: Session = Depends(get_db)):`,
		// Body: guard / fn call / fetch / return-injected-var
		`raise HTTPException(status_code=401, detail="missing")`,
		"payload = verify_jwt(authorization)",
		"user = db.get(schema.User, payload.sub)",
		"return user",
	} {
		if !strings.Contains(mw, want) {
			t.Errorf("require_auth.py missing %q, got:\n%s", want, mw)
		}
	}

	// Route: imports the middleware + receives the injected user as
	// `current_user` (the alias from `inject user as current_user`), typed
	// as schema.User (findInjectedModel walked the bound var to its model).
	route := readPy(t, outDir, "src/routes/me.py")
	for _, want := range []string{
		"from src.middleware.require_auth import require_auth",
		"current_user: schema.User = Depends(require_auth)",
		`"id": current_user.id`,
		`"email": current_user.email`,
	} {
		if !strings.Contains(route, want) {
			t.Errorf("me.py missing %q, got:\n%s", want, route)
		}
	}
}

// ---------- Phase 5: sum() rewrite ----------

// TestPython_Phase5SumGeneratorExpression covers emitSum +
// extractSumCollection + rewriteSumBody. The arithmetic on two columns of the
// same collection must collapse into a single `sum(... for r in <coll>)` line
// where every `<coll>.<col>` is rewritten to `r.<col>`.
func TestPython_Phase5SumGeneratorExpression(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model order_item { id uuid primary  price_cents int required  quantity int required }
GET /api/totals {
  |> order_items = query order_item
  |> total = sum(order_items.price_cents * order_items.quantity)
  -> 200 { total: total }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/totals.py")
	// Generator expression — extractSumCollection picked "order_items"
	// (the only base), rewriteSumBody substituted both bases with `r`.
	for _, want := range []string{
		"order_items = db.execute(select(schema.OrderItem)).scalars().all()",
		"total = sum((r.price_cents * r.quantity) for r in order_items)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("totals.py missing %q, got:\n%s", want, body)
		}
	}
}

// TestPython_Phase5SumSingleField covers the simpler single-field shape
// (sum(coll.field) with no arithmetic), to exercise rewriteSumBody's
// non-BinaryExpr base case.
func TestPython_Phase5SumSingleField(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model line_item { id uuid primary  amount int required }
GET /api/sum {
  |> line_items = query line_item
  |> total = sum(line_items.amount)
  -> 200 { total: total }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/sum.py")
	if !strings.Contains(body, "total = sum(r.amount for r in line_items)") {
		t.Errorf("single-field sum should be `sum(r.amount for r in line_items)`, got:\n%s", body)
	}
}

// readMaybe is a stat-like helper that returns "" + non-nil error when the
// file does not exist. Used by the negative cache test where we want to
// assert non-existence without t.Fatal'ing inside readPy.
func readMaybe(outDir, rel string) (string, error) {
	data, err := os.ReadFile(filepath.Join(outDir, rel))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
