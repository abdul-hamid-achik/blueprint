package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestPython_TagsEmitOnRouteDecorator(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/items {
  tags ["items", "catalog"]
  -> 200 "ok"
}
`)
	routes := readPy(t, outDir, "src/routes/items.py")
	if !strings.Contains(routes, `@router.get("/api/items", tags=["items", "catalog"])`) {
		t.Errorf("tags should be on the route decorator:\n%s", routes)
	}
}

func TestPython_CachePublicAndPrivateOnGetOnly(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/public {
  cache 5min
  -> 200 { status: "ok" }
}

GET /api/private {
  cache 10s
  <- token string optional
  -> 200 { token: header.Authorization }
}

POST /api/write {
  cache 5s
  -> 200 { status: "ok" }
}
`)
	public := readPy(t, outDir, "src/routes/public.py")
	if !strings.Contains(public, `cache_control(JSONResponse(jsonable_encoder({"status": "ok"}), status_code=200), "public, max-age=300")`) {
		t.Errorf("cached GET should wrap its 2xx return:\n%s", public)
	}
	private := readPy(t, outDir, "src/routes/private.py")
	if !strings.Contains(private, `"private, max-age=10"`) {
		t.Errorf("a response that reads a header must be private:\n%s", private)
	}
	write := readPy(t, outDir, "src/routes/write.py")
	if strings.Contains(write, "cache_control") {
		t.Errorf("non-GET endpoints must not set Cache-Control:\n%s", write)
	}
	readPy(t, outDir, "src/lib/http_cache.py")
	helper := readPy(t, outDir, "src/lib/http_cache.py")
	if !strings.Contains(helper, "200 <= response.status_code < 300") {
		t.Errorf("helper must only mark 2xx responses:\n%s", helper)
	}
}

func TestPython_CacheWrapsEveryReturnInBody(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/guarded/:id {
  cache 5s
  <- id string required
  |> guard id == "ok" -> 404 "missing"
  -> 200 { id: id }
}
`)
	routes := readPy(t, outDir, "src/routes/guarded.py")
	if !strings.Contains(routes, `raise HTTPException(status_code=404, detail="missing")`) {
		t.Errorf("guard should still raise its 4xx:\n%s", routes)
	}
	if !strings.Contains(routes, `return cache_control(JSONResponse(jsonable_encoder({"id": id}), status_code=200), "public, max-age=5")`) {
		t.Errorf("the 2xx return must carry Cache-Control:\n%s", routes)
	}
}

func TestPython_LimitEmitsCheckAndStore(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/search {
  limit 60/min
  <- q string optional
  -> 200 { q: q }
}
`)
	routes := readPy(t, outDir, "src/routes/search.py")
	for _, want := range []string{
		"from fastapi import APIRouter, Query, Request",
		"from src.lib import rate_limit",
		"async def get_search(bp_request: Request, q: str | None = Query(None)):",
		"if (_limited := rate_limit.check(bp_request, 60, 60000)) is not None:",
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("route missing %q:\n%s", want, routes)
		}
	}
	store := readPy(t, outDir, "src/lib/rate_limit.py")
	for _, want := range []string{
		`JSONResponse({"error": "Rate limit exceeded"}, status_code=429)`,
		`request.headers.get("x-forwarded-for") or "unknown"`,
		`key = f"{client}:{request.url.path}"`,
		"threading.Lock()",
	} {
		if !strings.Contains(store, want) {
			t.Errorf("rate_limit.py missing %q:\n%s", want, store)
		}
	}
}

func TestPython_LimitRunsBeforeSyncDBBody(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
model item { id uuid primary title string required }
GET /api/items/:id {
  limit 5/hour
  <- id uuid required
  |> item = fetch item(id)
  |> guard item -> 404 "Item not found"
  -> 200 item
}
`)
	routes := readPy(t, outDir, "src/routes/items.py")
	if !strings.Contains(routes, "def get_items_id(bp_request: Request, id: UUID, db: Session = Depends(get_db)):\n    if (_limited := rate_limit.check(bp_request, 5, 3600000)) is not None:") {
		t.Errorf("limit check must be the first statement of a DB-backed handler:\n%s", routes)
	}
}

func TestPython_NoSharedHelpersWithoutMetadata(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/plain {
  -> 200 { status: "ok" }
}
`)
	for _, rel := range []string{"src/lib/http_cache.py", "src/lib/rate_limit.py"} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err == nil {
			t.Errorf("%s should not be emitted without the metadata that needs it", rel)
		}
	}
}

func TestPython_MetadataStillFailsClosed(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"auth metadata": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  auth jwt
  -> 200 "ok"
}
`,
			want: `endpoint metadata "auth"`,
		},
		"sub-second cache": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  cache 500ms
  -> 200 "ok"
}
`,
			want: "must be a whole number of seconds",
		},
		"cache without unit": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  cache 5
  -> 200 "ok"
}
`,
			want: "endpoint `cache` metadata",
		},
		"limit that is not a rate": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  limit 5
  -> 200 "ok"
}
`,
			want: "endpoint `limit` metadata that is not a rate",
		},
		"tags with a non-string entry": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  tags ["ok", 3]
  -> 200 "ok"
}
`,
			want: "endpoint `tags` metadata with non-string entries",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected a rejection containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err.Error(), tc.want)
			}
		})
	}
}
