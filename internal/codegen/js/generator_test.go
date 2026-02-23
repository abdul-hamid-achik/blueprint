package js

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestHelpers(t *testing.T) {
	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{"toCamelCase basic", func() string { return toCamelCase("api_key") }, "apiKey"},
		{"toCamelCase single", func() string { return toCamelCase("name") }, "name"},
		{"toCamelCase multi", func() string { return toCamelCase("user_email_address") }, "userEmailAddress"},
		{"toPascalCase", func() string { return toPascalCase("api_key") }, "ApiKey"},
		{"toPascalCase single", func() string { return toPascalCase("name") }, "Name"},
		{"toKebabCase", func() string { return toKebabCase("api_key") }, "api-key"},
		{"pluralize regular", func() string { return pluralize("job") }, "jobs"},
		{"pluralize s", func() string { return pluralize("status") }, "statuses"},
		{"pluralize y", func() string { return pluralize("entry") }, "entries"},
		{"pluralize key", func() string { return pluralize("api_key") }, "api_keys"},
		{"pluralize day", func() string { return pluralize("day") }, "days"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateMinimal(t *testing.T) {
	src := `blueprint "test-app" {
  version "1.0.0"
  port    3000
  runtime node
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Check expected files exist
	expectedFiles := []string{
		"package.json",
		"tsconfig.json",
		".env.example",
		"src/index.ts",
		"src/lib/env.ts",
		"src/lib/errors.ts",
		"Dockerfile",
	}
	for _, f := range expectedFiles {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", f)
		}
	}

	// Check index.ts contains app name
	indexContent, _ := os.ReadFile(filepath.Join(outDir, "src/index.ts"))
	if !strings.Contains(string(indexContent), "test-app") {
		t.Error("index.ts should contain app name")
	}
	if !strings.Contains(string(indexContent), "port: 3000") {
		t.Error("index.ts should contain port 3000")
	}

	// Check package.json
	pkgContent, _ := os.ReadFile(filepath.Join(outDir, "package.json"))
	if !strings.Contains(string(pkgContent), `"test-app"`) {
		t.Error("package.json should contain app name")
	}
}

func TestGenerateWithModel(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

model user {
  id    uuid   primary
  name  string required
  email string unique
  age   int    optional
  created timestamp default(now)
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Check schema.ts exists and contains table definition
	schemaContent, err := os.ReadFile(filepath.Join(outDir, "src/models/schema.ts"))
	if err != nil {
		t.Fatal("schema.ts should exist")
	}
	schema := string(schemaContent)
	if !strings.Contains(schema, "pgTable('users'") {
		t.Error("schema should contain pgTable('users')")
	}
	if !strings.Contains(schema, ".primaryKey()") {
		t.Error("schema should contain .primaryKey()")
	}
	if !strings.Contains(schema, ".unique()") {
		t.Error("schema should contain .unique()")
	}
	if !strings.Contains(schema, "export type User") {
		t.Error("schema should export User type")
	}

	// Check .defaultNow() instead of .default(new Date())
	if !strings.Contains(schema, ".defaultNow()") {
		t.Error("schema should use .defaultNow() for timestamp default(now)")
	}
	if strings.Contains(schema, ".default(new Date())") {
		t.Error("schema should NOT use .default(new Date()), should use .defaultNow()")
	}

	// Check .defaultRandom() for uuid primary keys
	if !strings.Contains(schema, ".defaultRandom().primaryKey()") {
		t.Error("schema should use .defaultRandom().primaryKey() for uuid primary keys")
	}

	// Check db.ts exists and imports schema
	dbContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/db.ts"))
	if err != nil {
		t.Fatal("db.ts should exist when database is declared")
	}
	if !strings.Contains(string(dbContent), "import * as schema from '../models/schema.js'") {
		t.Error("db.ts should import schema")
	}
	if !strings.Contains(string(dbContent), "drizzle(pool, { schema })") {
		t.Error("db.ts should pass schema to drizzle()")
	}
}

func TestGenerateWithEndpoint(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

GET /api/health {
  -> 200 "ok"
}

POST /api/items {
  <- name string required
  <- price int min(0)
  -> 201 { name: name, price: price }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Check route files
	healthRoute, err := os.ReadFile(filepath.Join(outDir, "src/routes/health.ts"))
	if err != nil {
		t.Fatal("routes/health.ts should exist")
	}
	if !strings.Contains(string(healthRoute), ".get('/api/health'") {
		t.Error("health route should contain GET handler")
	}

	itemsRoute, err := os.ReadFile(filepath.Join(outDir, "src/routes/items.ts"))
	if err != nil {
		t.Fatal("routes/items.ts should exist")
	}
	itemsStr := string(itemsRoute)
	if !strings.Contains(itemsStr, ".post('/api/items'") {
		t.Error("items route should contain POST handler")
	}

	// Check zValidator wiring
	if !strings.Contains(itemsStr, "zValidator('json', postItemsSchema)") {
		t.Error("items route should wire zValidator with JSON schema for POST")
	}
	if !strings.Contains(itemsStr, "import { zValidator } from '@hono/zod-validator'") {
		t.Error("items route should import zValidator")
	}

	// Check input extraction uses c.req.valid('json')
	if !strings.Contains(itemsStr, "c.req.valid('json').name") {
		t.Error("POST input should use c.req.valid('json').name")
	}

	// Check validation schemas
	validation, err := os.ReadFile(filepath.Join(outDir, "src/validation/schemas.ts"))
	if err != nil {
		t.Fatal("validation/schemas.ts should exist")
	}
	valStr := string(validation)
	if !strings.Contains(valStr, "z.string()") {
		t.Error("validation should contain z.string() for name")
	}

	// Check schema name is correctly cased
	if !strings.Contains(valStr, "export const postItemsSchema") {
		t.Error("validation should have correctly cased schema name 'postItemsSchema'")
	}
	if strings.Contains(valStr, "pOSTItemsSchema") {
		t.Error("validation should NOT have incorrectly cased 'pOSTItemsSchema'")
	}
}

func TestGenerateWithMiddleware(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

middleware log_request {
  before {
    |> log "request started"
  }
  after {
    |> log "request ended"
  }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	mw, err := os.ReadFile(filepath.Join(outDir, "src/middleware/log-request.ts"))
	if err != nil {
		t.Fatal("middleware file should exist")
	}
	mwStr := string(mw)
	if !strings.Contains(mwStr, "createMiddleware") {
		t.Error("middleware should use createMiddleware")
	}
	if !strings.Contains(mwStr, "await next()") {
		t.Error("middleware should call next()")
	}
	// Check log is mapped to console.log
	if !strings.Contains(mwStr, "console.log") {
		t.Error("middleware should map log() to console.log()")
	}
}

func TestGenerateAllFeatures(t *testing.T) {
	src, err := os.ReadFile("../../../testdata/valid/all_features.bp")
	if err != nil {
		t.Skip("all_features.bp not found")
	}

	file, parseErrs := parser.ParseFile("all_features.bp", src)
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}

	checkErrs := checker.Check(file)
	if len(checkErrs) > 0 {
		t.Fatalf("checker errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Verify all expected output files exist
	expectedFiles := []string{
		"package.json",
		"tsconfig.json",
		".env.example",
		"Dockerfile",
		"src/index.ts",
		"src/lib/env.ts",
		"src/lib/errors.ts",
		"src/lib/db.ts",
		"src/lib/cache.ts",
		"src/lib/storage.ts",
		"src/types.ts",
		"src/models/schema.ts",
		"src/validation/schemas.ts",
		"src/routes/watermark.ts",
		"src/routes/jobs.ts",
		"src/routes/usage.ts",
		"src/functions/watermark.ts",
		"src/functions/check-quota.ts",
		"src/pipes/validate-image.ts",
		"src/middleware/require-auth.ts",
		"src/middleware/request-logger.ts",
		"src/schedules/cleanup.ts",
		"src/schedules/reset-quotas.ts",
		"test/watermark-success.test.ts",
		"test/watermark-oversized.test.ts",
	}

	for _, f := range expectedFiles {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", f)
		}
	}

	// Verify file headers
	indexContent, _ := os.ReadFile(filepath.Join(outDir, "src/index.ts"))
	if !strings.Contains(string(indexContent), "Generated by Blueprint") {
		t.Error("generated files should have Blueprint header")
	}

	// Verify global middleware wiring
	indexStr := string(indexContent)
	if !strings.Contains(indexStr, "app.use('*', requestLogger)") {
		t.Error("index should wire requestLogger middleware")
	}
	if !strings.Contains(indexStr, "app.use('*', cors(") {
		t.Error("index should wire cors middleware")
	}

	// Count total generated files
	count := 0
	filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			count++
		}
		return nil
	})
	if count < 25 {
		t.Errorf("expected at least 25 generated files, got %d", count)
	}
	t.Logf("Generated %d files from all_features.bp", count)
}

func TestExprToJS(t *testing.T) {
	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{"durationToMS seconds", func() string { return durationToMS("5s") }, "5 * 1000"},
		{"durationToMS ms", func() string { return durationToMS("100ms") }, "100"},
		{"durationToMS min", func() string { return durationToMS("10min") }, "10 * 60 * 1000"},
		{"sizeToBytes mb", func() string { return sizeToBytes("10mb") }, "10 * 1024 * 1024"},
		{"sizeToBytes kb", func() string { return sizeToBytes("512kb") }, "512 * 1024"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEndpointSchemaName(t *testing.T) {
	tests := []struct {
		method, path, want string
	}{
		{"GET", "/api/todos", "getTodosSchema"},
		{"POST", "/api/todos", "postTodosSchema"},
		{"PATCH", "/api/todos/:id", "patchTodosSchema"},
		{"DELETE", "/api/todos/:id", "deleteTodosSchema"},
		{"POST", "/api/watermark", "postWatermarkSchema"},
		{"GET", "/api/jobs/:id", "getJobsSchema"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			got := endpointSchemaName(tt.method, tt.path)
			if got != tt.want {
				t.Errorf("endpointSchemaName(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestConstraintZodOrdering(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

POST /api/items {
  <- page int default(1) min(1) max(100)
  -> 200 "ok"
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	validation, _ := os.ReadFile(filepath.Join(outDir, "src/validation/schemas.ts"))
	valStr := string(validation)

	// .min() and .max() should come before .default()
	minIdx := strings.Index(valStr, ".min(1)")
	maxIdx := strings.Index(valStr, ".max(100)")
	defIdx := strings.Index(valStr, ".default(1)")

	if minIdx == -1 || maxIdx == -1 || defIdx == -1 {
		t.Fatalf("expected .min(1), .max(100), and .default(1) in validation schema, got:\n%s", valStr)
	}
	if defIdx < minIdx {
		t.Error(".default() should come after .min() in Zod chain")
	}
	if defIdx < maxIdx {
		t.Error(".default() should come after .max() in Zod chain")
	}
}

func TestDataOpCodegen(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

model item {
  id    uuid   primary
  name  string required
  done  bool   default(false)
}

POST /api/items {
  <- name string required
  |> item = save item { name: name }
  -> 201 { id: item.id }
}

GET /api/items/:id {
  <- id uuid required
  |> item = fetch item(id)
  |> guard item -> 404 "Not found"
  -> 200 item
}

DELETE /api/items/:id {
  <- id uuid required
  |> item = fetch item(id)
  |> guard item -> 404 "Not found"
  |> delete item
  -> 204 "deleted"
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	route, err := os.ReadFile(filepath.Join(outDir, "src/routes/items.ts"))
	if err != nil {
		t.Fatal("routes/items.ts should exist")
	}
	routeStr := string(route)

	// Check save generates Drizzle insert
	if !strings.Contains(routeStr, "db.insert(schema.item)") {
		t.Error("save should generate db.insert()")
	}
	if !strings.Contains(routeStr, ".values(") {
		t.Error("save should generate .values()")
	}
	if !strings.Contains(routeStr, ".returning()") {
		t.Error("save should generate .returning()")
	}

	// Check fetch generates Drizzle select with where
	if !strings.Contains(routeStr, "db.select().from(schema.item)") {
		t.Error("fetch should generate db.select().from()")
	}
	if !strings.Contains(routeStr, "eq(schema.item.id, id)") {
		t.Error("fetch should use eq() for where clause")
	}

	// Check delete generates Drizzle delete
	if !strings.Contains(routeStr, "db.delete(schema.item)") {
		t.Error("delete should generate db.delete()")
	}

	// Check 204 uses c.body(null, 204) instead of c.json
	if !strings.Contains(routeStr, "c.body(null, 204)") {
		t.Error("204 response should use c.body(null, 204)")
	}

	// Check drizzle-orm imports
	if !strings.Contains(routeStr, "import { eq,") {
		t.Error("route file should import drizzle-orm operators")
	}
	if !strings.Contains(routeStr, "import * as schema from '../models/schema.js'") {
		t.Error("route file should import schema")
	}
}

func TestInputExtractionByMethod(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

GET /api/items {
  <- page int default(1)
  -> 200 { page: page }
}

GET /api/items/:id {
  <- id string required
  -> 200 { id: id }
}

POST /api/items {
  <- name string required
  -> 201 { name: name }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	route, _ := os.ReadFile(filepath.Join(outDir, "src/routes/items.ts"))
	routeStr := string(route)

	// GET query param should use c.req.valid('query')
	if !strings.Contains(routeStr, "c.req.valid('query').page") {
		t.Error("GET query param should use c.req.valid('query').page")
	}

	// GET path param should use c.req.param()
	if !strings.Contains(routeStr, "c.req.param('id')") {
		t.Error("GET path param should use c.req.param('id')")
	}

	// POST body param should use c.req.valid('json')
	if !strings.Contains(routeStr, "c.req.valid('json').name") {
		t.Error("POST body param should use c.req.valid('json').name")
	}
}

func TestPackageJSONDeterministic(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
  cache    redis
  storage  s3
}

secret DATABASE_URL required

GET /api/health {
  -> 200 "ok"
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	gen := New()

	// Generate twice and compare
	dir1 := t.TempDir()
	if err := gen.Generate(file, dir1); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	pkg1, _ := os.ReadFile(filepath.Join(dir1, "package.json"))

	dir2 := t.TempDir()
	gen2 := New()
	if err := gen2.Generate(file, dir2); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	pkg2, _ := os.ReadFile(filepath.Join(dir2, "package.json"))

	if string(pkg1) != string(pkg2) {
		t.Error("package.json should be deterministic across runs")
		t.Logf("Run 1:\n%s", pkg1)
		t.Logf("Run 2:\n%s", pkg2)
	}

	// Verify @hono/zod-validator is included
	if !strings.Contains(string(pkg1), "@hono/zod-validator") {
		t.Error("package.json should include @hono/zod-validator dependency")
	}
}

func TestDockerfileCMD(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	dockerfile, _ := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
	dfStr := string(dockerfile)

	if !strings.Contains(dfStr, `CMD ["npx", "tsx", "src/index.ts"]`) {
		t.Error("Dockerfile should use npx tsx, not node --import tsx")
	}
	if strings.Contains(dfStr, `node --import`) {
		t.Error("Dockerfile should NOT use node --import tsx")
	}
}

func TestTSConfigModuleResolution(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	tsconfig, _ := os.ReadFile(filepath.Join(outDir, "tsconfig.json"))
	tscStr := string(tsconfig)

	if !strings.Contains(tscStr, `"moduleResolution": "nodenext"`) {
		t.Error("tsconfig should use moduleResolution: nodenext")
	}
	if strings.Contains(tscStr, `"moduleResolution": "bundler"`) {
		t.Error("tsconfig should NOT use moduleResolution: bundler")
	}
	if !strings.Contains(tscStr, `"module": "NodeNext"`) {
		t.Error("tsconfig should use module: NodeNext")
	}
}

func TestFunctionOutputContext(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

fn add_numbers {
  <- a int
  <- b int
  -> int

  logic {
    |> result = a + b
    |> -> result
  }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	fn, err := os.ReadFile(filepath.Join(outDir, "src/functions/add-numbers.ts"))
	if err != nil {
		t.Fatal("function file should exist")
	}
	fnStr := string(fn)

	// Function output should be plain return, not c.json
	if strings.Contains(fnStr, "c.json") {
		t.Error("function output should not use c.json()")
	}
	if !strings.Contains(fnStr, "return result") {
		t.Error("function output should use plain return")
	}
}

func TestSchemaNameDedup(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

GET /api/items {
  <- page int default(1)
  -> 200 { page: page }
}

GET /api/items/:id {
  <- id string required
  -> 200 { id: id }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	validation, _ := os.ReadFile(filepath.Join(outDir, "src/validation/schemas.ts"))
	valStr := string(validation)

	// Should have getItemsSchema for the GET /api/items endpoint (has non-path inputs)
	if !strings.Contains(valStr, "getItemsSchema") {
		t.Error("should have getItemsSchema for GET /api/items")
	}

	// GET /api/items/:id has only path param 'id' — should NOT generate a schema
	count := strings.Count(valStr, "export const")
	if count != 1 {
		t.Errorf("expected 1 schema export (getItemsSchema), got %d exports:\n%s", count, valStr)
	}
}

func TestGenerateStreamEndpoint(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

STREAM /api/updates {
  stream {
    |> on event(update) {
      |> log "sending update"
    }
    |> on event(error) {
      |> log "sending error"
    }
  }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Check stream route file exists
	routeContent, err := os.ReadFile(filepath.Join(outDir, "src/routes/updates.ts"))
	if err != nil {
		t.Fatal("src/routes/updates.ts should exist for STREAM endpoint")
	}
	routeStr := string(routeContent)

	// Check streamSSE import
	if !strings.Contains(routeStr, "import { streamSSE } from 'hono/streaming'") {
		t.Error("stream route should import streamSSE from hono/streaming")
	}

	// Check route handler uses streamSSE
	if !strings.Contains(routeStr, "streamSSE(c, async (stream) =>") {
		t.Error("stream route should use streamSSE")
	}

	// Check path is registered
	if !strings.Contains(routeStr, ".get('/api/updates'") {
		t.Error("stream route should register GET handler at /api/updates")
	}

	// Check event handlers are emitted
	if !strings.Contains(routeStr, "stream.writeSSE") {
		t.Error("stream route should emit stream.writeSSE calls")
	}

	// Check index.ts imports the stream route
	indexContent, err := os.ReadFile(filepath.Join(outDir, "src/index.ts"))
	if err != nil {
		t.Fatal("src/index.ts should exist")
	}
	indexStr := string(indexContent)
	if !strings.Contains(indexStr, "updatesRoutes") {
		t.Error("index.ts should import updatesRoutes")
	}
}

func TestGenerateWsEndpoint(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

WS /ws/chat {
  on_connect {
    |> log "client connected"
  }
  on_message {
    |> log "received message"
  }
  on_disconnect {
    |> log "client disconnected"
  }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Check ws route file exists
	routeContent, err := os.ReadFile(filepath.Join(outDir, "src/routes/ws.ts"))
	if err != nil {
		t.Fatal("src/routes/ws.ts should exist for WS endpoint")
	}
	routeStr := string(routeContent)

	// Check upgradeWebSocket import
	if !strings.Contains(routeStr, "import { upgradeWebSocket } from 'hono/ws'") {
		t.Error("ws route should import upgradeWebSocket from hono/ws")
	}

	// Check route uses upgradeWebSocket
	if !strings.Contains(routeStr, "upgradeWebSocket(") {
		t.Error("ws route should use upgradeWebSocket")
	}

	// Check path is registered
	if !strings.Contains(routeStr, ".get('/ws/chat'") {
		t.Error("ws route should register GET handler at /ws/chat")
	}

	// Check lifecycle handlers
	if !strings.Contains(routeStr, "onOpen(event, ws)") {
		t.Error("ws route should have onOpen handler")
	}
	if !strings.Contains(routeStr, "onMessage(event, ws)") {
		t.Error("ws route should have onMessage handler")
	}
	if !strings.Contains(routeStr, "onClose(event, ws)") {
		t.Error("ws route should have onClose handler")
	}

	// Check message binding
	if !strings.Contains(routeStr, "const message = event.data") {
		t.Error("ws route onMessage should bind event.data to message")
	}

	// Check index.ts imports node-ws adapter
	indexContent, err := os.ReadFile(filepath.Join(outDir, "src/index.ts"))
	if err != nil {
		t.Fatal("src/index.ts should exist")
	}
	indexStr := string(indexContent)
	if !strings.Contains(indexStr, "createNodeWebSocket") {
		t.Error("index.ts should import createNodeWebSocket for WS endpoints")
	}
	if !strings.Contains(indexStr, "injectWebSocket") {
		t.Error("index.ts should set up injectWebSocket")
	}
}

func TestGenerateSubscribe(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

subscribe "user.created" from(auth_service) {
  |> log "User created"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Check events lib was generated
	eventsContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/events.ts"))
	if err != nil {
		t.Fatal("src/lib/events.ts should exist when subscribe blocks exist")
	}
	eventsStr := string(eventsContent)
	if !strings.Contains(eventsStr, "export type EventHandler") {
		t.Error("events.ts should export EventHandler type")
	}
	if !strings.Contains(eventsStr, "export function on(") {
		t.Error("events.ts should export on() function")
	}
	if !strings.Contains(eventsStr, "export async function emit(") {
		t.Error("events.ts should export emit() function")
	}

	// Check subscription handler was generated
	subContent, err := os.ReadFile(filepath.Join(outDir, "src/subscriptions/user-created.ts"))
	if err != nil {
		t.Fatal("src/subscriptions/user-created.ts should exist")
	}
	subStr := string(subContent)
	if !strings.Contains(subStr, "export async function onUserCreated(") {
		t.Error("subscription handler should export onUserCreated function")
	}
	if !strings.Contains(subStr, "event: unknown") {
		t.Error("subscription handler should accept event: unknown param")
	}

	// Check index.ts registers the subscription
	indexContent, err := os.ReadFile(filepath.Join(outDir, "src/index.ts"))
	if err != nil {
		t.Fatal("src/index.ts should exist")
	}
	indexStr := string(indexContent)
	if !strings.Contains(indexStr, "import { on } from './lib/events.js'") {
		t.Error("index.ts should import on from events lib")
	}
	if !strings.Contains(indexStr, "import { onUserCreated }") {
		t.Error("index.ts should import onUserCreated subscription handler")
	}
	if !strings.Contains(indexStr, "on('user.created', onUserCreated)") {
		t.Error("index.ts should register onUserCreated for user.created event")
	}
}

func TestGen_Worker(t *testing.T) {
	// Worker meta uses trigger/retry/timeout (not queue/concurrency — those aren't in the AST).
	// The genWorker function emits:
	//   export async function {camelCaseName}(data: any): Promise<void> { ... }
	// and if on_fail is present:
	//   export async function {camelCaseName}OnFail(data: any, error: Error): Promise<void> { ... }
	src := `blueprint "x" {
  version "1.0"
  port 8080
  runtime node
}
worker process_job {
  trigger "job.created"
  retry 3
  |> log "Processing job"
  -> 200 "done"

  on_fail {
    |> log "Job failed"
  }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "src/workers/process-job.ts"))
	if err != nil {
		t.Fatal("src/workers/process-job.ts should exist")
	}
	workerStr := string(content)

	// Worker function must be camelCase of 'process_job' → 'processJob'
	if !strings.Contains(workerStr, "processJob") {
		t.Errorf("expected processJob function name, got:\n%s", workerStr)
	}

	// Must be an async function accepting (data: any)
	if !strings.Contains(workerStr, "export async function processJob(data: any)") {
		t.Errorf("expected 'export async function processJob(data: any)', got:\n%s", workerStr)
	}

	// Must return Promise<void>
	if !strings.Contains(workerStr, "Promise<void>") {
		t.Errorf("expected Promise<void> return type, got:\n%s", workerStr)
	}

	// |> log "Processing job" → console.log("Processing job")
	if !strings.Contains(workerStr, `console.log("Processing job")`) {
		t.Errorf("expected console.log for log step, got:\n%s", workerStr)
	}

	// on_fail block should generate a separate OnFail function
	if !strings.Contains(workerStr, "processJobOnFail") {
		t.Errorf("expected processJobOnFail function for on_fail block, got:\n%s", workerStr)
	}
	if !strings.Contains(workerStr, "export async function processJobOnFail(data: any, error: Error)") {
		t.Errorf("expected 'export async function processJobOnFail(data: any, error: Error)', got:\n%s", workerStr)
	}

	// on_fail log step should also produce console.log
	if !strings.Contains(workerStr, `console.log("Job failed")`) {
		t.Errorf("expected console.log(\"Job failed\") in on_fail handler, got:\n%s", workerStr)
	}

	// File should have the Blueprint generated header
	if !strings.Contains(workerStr, "Generated by Blueprint") {
		t.Errorf("expected Generated by Blueprint header, got:\n%s", workerStr)
	}
}

func TestGen_CallExternal(t *testing.T) {
	src := `blueprint "test-call-external" {
  version "0.1.0"
  port    3000
  runtime node
}

secret INTERNAL_TOKEN required

external "auth-service" {
  url:     "http://auth:3001"
  timeout: 5s
  retry:   2
}

GET /api/me {
  |> user = call auth_service GET /api/users/me
  |> guard user -> 404 "User not found"

  -> 200 { name: user.name, email: user.email }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// src/lib/external.ts must exist and export the config object + call helper
	extContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/external.ts"))
	if err != nil {
		t.Fatal("src/lib/external.ts should exist")
	}
	extStr := string(extContent)

	// Config object exported as authService
	if !strings.Contains(extStr, "export const authService = {") {
		t.Errorf("external.ts should export authService config object, got:\n%s", extStr)
	}
	// Call helper function exported
	if !strings.Contains(extStr, "export async function callAuthService(") {
		t.Errorf("external.ts should export callAuthService helper, got:\n%s", extStr)
	}
	// Helper uses fetch
	if !strings.Contains(extStr, "await fetch(") {
		t.Errorf("callAuthService should use fetch, got:\n%s", extStr)
	}

	// The route file for /api/me should import callAuthService from external
	routeContent, err := os.ReadFile(filepath.Join(outDir, "src/routes/me.ts"))
	if err != nil {
		t.Fatal("src/routes/me.ts should exist")
	}
	routeStr := string(routeContent)

	if !strings.Contains(routeStr, "import { callAuthService }") {
		t.Errorf("route should import callAuthService from external.js, got:\n%s", routeStr)
	}
	if !strings.Contains(routeStr, "from '../lib/external.js'") {
		t.Errorf("route should import from '../lib/external.js', got:\n%s", routeStr)
	}
	// The call statement should become: const user = await callAuthService('GET', '/api/users/me');
	if !strings.Contains(routeStr, `await callAuthService("GET", "/api/users/me")`) {
		t.Errorf("route should emit await callAuthService(\"GET\", \"/api/users/me\"), got:\n%s", routeStr)
	}
}

func TestGen_ImplExec(t *testing.T) {
	src := `blueprint "test-impl-exec" {
  version "0.1.0"
  port    3000
  runtime node
}

fn run_script {
  <- filename string

  -> string

  impl exec {
    cmd: "process.sh"
  }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "src/functions/run-script.ts"))
	if err != nil {
		t.Fatal("src/functions/run-script.ts should exist")
	}
	fnStr := string(content)

	if !strings.Contains(fnStr, "import { execFile } from 'child_process'") {
		t.Errorf("exec impl should import execFile, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, "import { promisify } from 'util'") {
		t.Errorf("exec impl should import promisify, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, "const execFileAsync = promisify(execFile)") {
		t.Errorf("exec impl should create execFileAsync, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, "export async function runScript(") {
		t.Errorf("exec impl should export runScript function, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, `execFileAsync("process.sh"`) {
		t.Errorf("exec impl should call execFileAsync with cmd, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, "return stdout") {
		t.Errorf("exec impl should return stdout, got:\n%s", fnStr)
	}
}

func TestGen_ImplHTTP(t *testing.T) {
	src := `blueprint "test-impl-http" {
  version "0.1.0"
  port    3000
  runtime node
}

fn notify_user {
  <- email string

  -> string

  impl http {
    method: "POST"
    url:    "https://hooks.example.com/notify"
  }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "src/functions/notify-user.ts"))
	if err != nil {
		t.Fatal("src/functions/notify-user.ts should exist")
	}
	fnStr := string(content)

	if !strings.Contains(fnStr, "export async function notifyUser(") {
		t.Errorf("http impl should export notifyUser function, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, "await fetch(") {
		t.Errorf("http impl should use fetch, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, `"https://hooks.example.com/notify"`) {
		t.Errorf("http impl should include the URL, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, `"POST"`) {
		t.Errorf("http impl should include POST method, got:\n%s", fnStr)
	}
	if !strings.Contains(fnStr, "return res.json()") {
		t.Errorf("http impl should return res.json(), got:\n%s", fnStr)
	}
}

func TestGen_HyphenatedHeaderName(t *testing.T) {
	src := `blueprint "x" {
  version "1.0"
  port    8080
  runtime node
}
middleware require_auth {
  before {
    |> guard header.X-API-Key -> 401 "Missing API key"
    |> key = header.X-API-Key
    |> guard key             -> 401 "Invalid API key"
    |> inject key as auth
  }
}
GET /api/test {
  use require_auth
  -> 200 "ok"
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	checkErrs := checker.Check(file)
	if len(checkErrs) > 0 {
		t.Fatalf("check errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "src/middleware/require-auth.ts"))
	if err != nil {
		t.Fatal("src/middleware/require-auth.ts should exist")
	}
	if !strings.Contains(string(content), "c.req.header('X-API-Key')") {
		t.Errorf("expected hyphenated header access in middleware, got:\n%s", string(content))
	}
}

func TestGen_TestCodegen(t *testing.T) {
	src := `blueprint "x" {
  version "1.0"
  port    8080
  runtime node
  database postgres
}

secret DATABASE_URL required

model api_key {
  id       uuid   primary
  key_hash string required
  plan     string required
}

fixture "sample.png" from "testdata/sample.png"

@ "Test with proper assertions and fixtures"
test upload_success {
  target POST /api/upload

  setup {
    |> key = seed api_key { plan: "pro", key_hash: "abc123" }
  }

  request {
    auth api_key(key.key_hash)
    body {
      file: fixture("sample.png"),
      name: "test",
    }
  }

  expect {
    status 201
    body.url is string
    body.id is uuid
    body.name == "test"
  }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	checkErrs := checker.Check(file)
	if len(checkErrs) > 0 {
		t.Fatalf("check errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "test/upload-success.test.ts"))
	if err != nil {
		t.Fatal("test/upload-success.test.ts should exist")
	}
	ts := string(content)

	// Hoisted variable declaration
	if !strings.Contains(ts, "let key: any;") {
		t.Errorf("should hoist setup variable; got:\n%s", ts)
	}
	// Setup uses assignment (not const) for hoisted var
	if !strings.Contains(ts, "key = (await db.insert") {
		t.Errorf("setup should assign hoisted var; got:\n%s", ts)
	}
	// Fixture resolved to readFileSync
	if !strings.Contains(ts, "readFileSync(join(__dirname, '..', 'testdata/sample.png'))") {
		t.Errorf("fixture should resolve to readFileSync; got:\n%s", ts)
	}
	// Auth header
	if !strings.Contains(ts, "'X-API-Key': key.keyHash") {
		t.Errorf("should emit X-API-Key header; got:\n%s", ts)
	}
	// Body parse
	if !strings.Contains(ts, "const body = await res.json() as any;") {
		t.Errorf("should parse response body; got:\n%s", ts)
	}
	// Status assertion
	if !strings.Contains(ts, "expect(res.status).toBe(201);") {
		t.Errorf("should emit status assertion; got:\n%s", ts)
	}
	// Type assertions
	if !strings.Contains(ts, "expect(typeof body.url).toBe('string');") {
		t.Errorf("should emit typeof body.url assertion; got:\n%s", ts)
	}
	if !strings.Contains(ts, "expect(body.id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i);") {
		t.Errorf("should emit uuid regex assertion for body.id; got:\n%s", ts)
	}
	// Equality assertion with string value
	if !strings.Contains(ts, "expect(body.name).toBe('test');") {
		t.Errorf("should emit equality assertion; got:\n%s", ts)
	}
}

func TestGen_ModelAssertionInTest(t *testing.T) {
	src := `blueprint "test-app" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

model job {
  id     uuid   primary
  status string required
}

POST /api/jobs {
  <- status string required
  |> j = save job { status: status }
  -> 201 { id: j.id }
}

test create_job_exists {
  target POST /api/jobs

  request {
    body {
      status: "pending",
    }
  }

  expect {
    status 201
    model job where(id == body.id, status == "pending") exists
  }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	checkErrs := checker.Check(file)
	if len(checkErrs) > 0 {
		t.Fatalf("check errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "test/create-job-exists.test.ts"))
	if err != nil {
		t.Fatal("test/create-job-exists.test.ts should exist")
	}
	ts := string(content)

	// Should import db/schema/drizzle
	if !strings.Contains(ts, "import { db } from '../src/lib/db.js';") {
		t.Errorf("should import db; got:\n%s", ts)
	}
	if !strings.Contains(ts, "import * as schema from '../src/models/schema.js';") {
		t.Errorf("should import schema; got:\n%s", ts)
	}
	if !strings.Contains(ts, "import { eq, and } from 'drizzle-orm';") {
		t.Errorf("should import drizzle operators; got:\n%s", ts)
	}
	// Should emit db.select() query
	if !strings.Contains(ts, "db.select().from(schema.jobs)") {
		t.Errorf("should emit db.select().from(schema.jobs); got:\n%s", ts)
	}
	// Should include eq conditions
	if !strings.Contains(ts, "eq(schema.jobs.id") {
		t.Errorf("should emit eq(schema.jobs.id, ...); got:\n%s", ts)
	}
	if !strings.Contains(ts, "eq(schema.jobs.status, 'pending')") {
		t.Errorf("should emit eq(schema.jobs.status, 'pending'); got:\n%s", ts)
	}
	// Should check length > 0 for exists
	if !strings.Contains(ts, "expect(_row.length).toBeGreaterThan(0);") {
		t.Errorf("should emit toBeGreaterThan(0); got:\n%s", ts)
	}
}

func TestGen_WebhookAuthCodegen(t *testing.T) {
	src := `blueprint "test-app" {
  version "1.0.0"
  port    3000
  runtime node
}

secret STRIPE_KEY required

POST /webhooks/stripe {
  auth webhook_sig using(secret.STRIPE_KEY)
  -> 200 "ok"
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	checkErrs := checker.Check(file)
	if len(checkErrs) > 0 {
		t.Fatalf("check errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "src/routes/webhooks.ts"))
	if err != nil {
		t.Fatal("src/routes/webhooks.ts should exist")
	}
	ts := string(content)

	// Should import createHmac and timingSafeEqual
	if !strings.Contains(ts, "import { createHmac, timingSafeEqual } from 'node:crypto';") {
		t.Errorf("should import createHmac and timingSafeEqual; got:\n%s", ts)
	}
	// Should emit payload reading
	if !strings.Contains(ts, "const _payload = await c.req.text();") {
		t.Errorf("should read raw payload; got:\n%s", ts)
	}
	// Should emit HMAC verification with secret key
	if !strings.Contains(ts, "process.env.STRIPE_KEY!") {
		t.Errorf("should use STRIPE_KEY env var; got:\n%s", ts)
	}
	// Should emit timing-safe signature comparison
	if !strings.Contains(ts, "timingSafeEqual(Buffer.from(_sig, 'hex'), Buffer.from(_expected, 'hex'))") {
		t.Errorf("should use timingSafeEqual for comparison; got:\n%s", ts)
	}
	// Should return 401 on bad signature
	if !strings.Contains(ts, "return c.json({ error: 'Invalid signature' }, 401);") {
		t.Errorf("should return 401 on bad signature; got:\n%s", ts)
	}
	// Should wrap JSON.parse in try/catch
	if !strings.Contains(ts, "try { data = JSON.parse(_payload); } catch { return c.json({ error: 'Invalid payload' }, 400); }") {
		t.Errorf("should wrap JSON.parse in try/catch; got:\n%s", ts)
	}
}
