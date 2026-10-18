package js

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const httpCacheHeader = `blueprint "cache-test" {
  version "1.0.0"
  port 3000
  runtime node
}
`

func parseHTTPCacheFile(t *testing.T, body string) *ast.File {
	t.Helper()
	file, errs := parser.ParseFile("http_cache.bp", []byte(httpCacheHeader+body))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return file
}

func routeFileContent(t *testing.T, files []string, contents map[string]string) string {
	t.Helper()
	var b strings.Builder
	for _, path := range files {
		if strings.HasPrefix(path, "src/routes/") {
			b.WriteString(contents[path])
		}
	}
	return b.String()
}

func generatedFiles(t *testing.T, file *ast.File) (map[string]string, []string) {
	t.Helper()
	files, err := New().Files(file)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	contents := map[string]string{}
	var paths []string
	for _, f := range files {
		contents[f.Path] = string(f.Content)
		paths = append(paths, f.Path)
	}
	return contents, paths
}

func TestEndpointCacheScopesAndMethods(t *testing.T) {
	file := parseHTTPCacheFile(t, `
@ "public read"
GET /cached/public {
  cache 60s
  -> 200 { ok: true }
}

@ "reads the caller's header"
GET /cached/header {
  cache 5min
  -> 200 { token: header.authorization }
}

@ "rate limited, so responses depend on the caller's address"
GET /cached/limited {
  cache 5s
  limit 10/min
  -> 200 { ok: true }
}

@ "writes are never cached"
POST /cached/write {
  cache 5s
  -> 201 { ok: true }
}

@ "no cache metadata"
GET /cached/plain {
  -> 200 { ok: true }
}
`)
	contents, paths := generatedFiles(t, file)
	routes := routeFileContent(t, paths, contents)

	if !strings.Contains(contents["src/lib/http-cache.ts"], "export function httpCache(maxAgeSeconds: number, scope: 'public' | 'private'): MiddlewareHandler") {
		t.Fatalf("src/lib/http-cache.ts missing httpCache export:\n%s", contents["src/lib/http-cache.ts"])
	}
	if !strings.Contains(contents["src/lib/http-cache.ts"], "c.res.status >= 200 && c.res.status < 300") {
		t.Errorf("httpCache must gate on 2xx responses:\n%s", contents["src/lib/http-cache.ts"])
	}

	wantLines := []string{
		"cachedRoutes.get('/cached/public', httpCache(60, 'public'), async (c) => {",
		"cachedRoutes.get('/cached/header', httpCache(300, 'private'), async (c) => {",
		"cachedRoutes.get('/cached/limited', httpCache(5, 'private'), async (c) => {",
		"cachedRoutes.get('/cached/plain', async (c) => {",
		"cachedRoutes.post('/cached/write', async (c) => {",
		"// cache 5s ignored: Cache-Control is only set on GET endpoints",
	}
	for _, want := range wantLines {
		if !strings.Contains(routes, want) {
			t.Errorf("route file missing %q:\n%s", want, routes)
		}
	}
	if got := strings.Count(routes, "httpCache("); got != 3 {
		t.Errorf("expected exactly 3 httpCache(...) route entries (GET with cache), got %d:\n%s", got, routes)
	}
	if !strings.Contains(routes, "import { httpCache } from '../lib/http-cache.js';") {
		t.Errorf("route file must import httpCache:\n%s", routes)
	}
	if strings.Contains(routes, "Cache-Control") && !strings.Contains(routes, "// cache") {
		t.Errorf("route file should not set Cache-Control directly:\n%s", routes)
	}
}

func TestEndpointCacheGlobalUseMakesEveryCacheEntryPrivate(t *testing.T) {
	file, errs := parser.ParseFile("http_cache.bp", []byte(`blueprint "cache-test" {
  version "1.0.0"
  port 3000
  runtime node
  use cors
}

@ "public without global middleware"
GET /cached/open {
  cache 30s
  -> 200 { ok: true }
}
`))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	contents, paths := generatedFiles(t, file)
	routes := routeFileContent(t, paths, contents)
	if !strings.Contains(routes, "httpCache(30, 'private')") {
		t.Errorf("global use must force private scope:\n%s", routes)
	}
	if strings.Contains(routes, "'public'") {
		t.Errorf("global use must never emit public:\n%s", routes)
	}
}

func TestEndpointCacheAuthAndUseAreAlwaysPrivate(t *testing.T) {
	file := parseHTTPCacheFile(t, `
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Missing API key"
  }
}

@ "guarded by middleware"
GET /cached/guarded {
  use require_auth
  cache 10s
  -> 200 { ok: true }
}

@ "read via injected auth context"
GET /cached/context {
  cache 10s
  -> 200 { id: auth.id }
}
`)
	contents, paths := generatedFiles(t, file)
	routes := routeFileContent(t, paths, contents)
	if strings.Contains(routes, "httpCache(10, 'public')") {
		t.Errorf("user-scoped cached responses must not be public:\n%s", routes)
	}
	if got := strings.Count(routes, "httpCache(10, 'private')"); got != 2 {
		t.Errorf("expected 2 private httpCache entries, got %d:\n%s", got, routes)
	}
}

func TestEndpointCacheNotEmittedWithoutCacheEntries(t *testing.T) {
	file := parseHTTPCacheFile(t, `
@ "plain"
GET /cached/plain {
  -> 200 { ok: true }
}
`)
	contents, _ := generatedFiles(t, file)
	if _, ok := contents["src/lib/http-cache.ts"]; ok {
		t.Errorf("http-cache.ts must only be emitted when a GET endpoint uses cache")
	}
}

func TestEndpointCacheRejectsMalformedDurations(t *testing.T) {
	for _, value := range []string{"0s", "0min", "500ms", "1.5s", "5", "-5s"} {
		t.Run(value, func(t *testing.T) {
			file := parseHTTPCacheFile(t, `
GET /cached/bad {
  cache `+value+`
  -> 200 { ok: true }
}
`)
			files, err := New().Files(file)
			if err == nil {
				t.Fatalf("expected an error for cache %s, got %d files", value, len(files))
			}
			if files != nil {
				t.Errorf("malformed cache must not return files, got %d", len(files))
			}
			if !strings.Contains(err.Error(), "cache") {
				t.Errorf("error should mention cache: %v", err)
			}
		})
	}
}

func TestEndpointCacheMaxAgeSeconds(t *testing.T) {
	tests := map[string]int{
		"5s":     5,
		"1000ms": 1,
		"5min":   300,
		"1h":     3600,
		"1d":     86400,
	}
	for value, want := range tests {
		t.Run(value, func(t *testing.T) {
			file := parseHTTPCacheFile(t, `
GET /cached/ok {
  cache `+value+`
  -> 200 { ok: true }
}
`)
			ep := file.Blocks[len(file.Blocks)-1].(*ast.Endpoint)
			got, err := cacheMaxAgeSeconds(ep.Meta[0].Value)
			if err != nil || got != want {
				t.Errorf("cacheMaxAgeSeconds(%s) = %d, %v; want %d", value, got, err, want)
			}
		})
	}
}

// TestEndpointCacheRuntimeHeaders runs the generated middleware and route file
// under Hono: 2xx gets Cache-Control, 4xx does not, POST is never cached, and
// a caller-specific response is marked private.
func TestEndpointCacheRuntimeHeaders(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not available")
	}
	file := parseHTTPCacheFile(t, `
@ "public read"
GET /cached/ping {
  cache 5s
  -> 200 { ok: true }
}

@ "missing item returns 404"
GET /cached/items/:id {
  cache 5s
  <- id string required
  |> guard id == "ok" -> 404 "missing"
  -> 200 { id: id }
}

@ "caller-specific read"
GET /cached/me {
  cache 5min
  -> 200 { token: header.authorization }
}

@ "writes are not cached"
POST /cached/write {
  cache 5s
  -> 201 { ok: true }
}
`)
	outDir := t.TempDir()
	if err := New().Generate(file, outDir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	routeSrc, err := os.ReadFile(outDir + "/src/routes/cached.ts")
	if err != nil {
		t.Fatalf("read generated route file: %v", err)
	}
	exportName := regexp.MustCompile(`export const (\w+Routes)`).FindStringSubmatch(string(routeSrc))
	if exportName == nil {
		t.Fatalf("route file has no Routes export:\n%s", routeSrc)
	}
	requireTypeScriptCompile(t, outDir)

	script := `
import { Hono } from 'hono';
import * as routes from './src/routes/cached.ts';
const app = new Hono();
app.route('/', routes.` + exportName[1] + `);
const failures: string[] = [];
const expect = async (method: string, path: string, headers: Record<string, string>, status: number, cacheControl: string | null) => {
  const res = await app.request(path, { method, headers, body: method === 'POST' ? '{}' : undefined });
  const got = res.headers.get('Cache-Control');
  if (res.status !== status) failures.push(method + ' ' + path + ' status ' + res.status + ' want ' + status);
  if (got !== cacheControl) failures.push(method + ' ' + path + ' Cache-Control ' + got + ' want ' + cacheControl);
};
await expect('GET', '/cached/ping', {}, 200, 'public, max-age=5');
await expect('GET', '/cached/items/ok', {}, 200, 'public, max-age=5');
await expect('GET', '/cached/items/nope', {}, 404, null);
await expect('GET', '/cached/me', { authorization: 'Bearer secret' }, 200, 'private, max-age=300');
await expect('POST', '/cached/write', {}, 201, null);
if (failures.length > 0) throw new Error(failures.join('\n'));
`
	checkPath := filepath.Join(outDir, "http-cache-check.ts")
	if err := os.WriteFile(checkPath, []byte(script), 0o644); err != nil {
		t.Fatalf("write check script: %v", err)
	}
	cmd := exec.Command("bun", "run", checkPath)
	cmd.Dir = outDir
	tmp := t.TempDir()
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "BUN_TMPDIR="+tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cache runtime check failed: %v\n%s", err, out)
	}
}
