package js

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func requireTypeScriptCompile(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is not available")
	}
	run := func(timeout time.Duration, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "npm", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("npm %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
		}
	}
	run(3*time.Minute, "install", "--silent")
	run(2*time.Minute, "exec", "--", "tsc", "--noEmit")
}

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

func TestGenerateTypedJSONContracts(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

type MissionDefinition {
  title string required
  tags  list(string)
}

model mission {
  id   uuid primary
  data json<MissionDefinition> required
}

POST /api/missions {
  <- data json<MissionDefinition> required
  |> mission = save mission { data: data }
  -> 201 { id: mission.id, data: mission.data }
}`

	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if checkErrs := checker.Check(file); len(checkErrs) > 0 {
		t.Fatalf("checker errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	schemaContent, err := os.ReadFile(filepath.Join(outDir, "src/models/schema.ts"))
	if err != nil {
		t.Fatal(err)
	}
	schema := string(schemaContent)
	if !strings.Contains(schema, "import type { MissionDefinition } from '../types.js';") {
		t.Error("schema should import named types used by typed json fields")
	}
	if !strings.Contains(schema, "jsonb('data').$type<MissionDefinition>()") {
		t.Error("schema should preserve typed json field types in Drizzle")
	}

	validationContent, err := os.ReadFile(filepath.Join(outDir, "src/validation/schemas.ts"))
	if err != nil {
		t.Fatal(err)
	}
	validation := string(validationContent)
	if !strings.Contains(validation, "import { MissionDefinitionSchema } from '../types/schemas.js';") {
		t.Error("validation schema should import typed json schemas from generated types schemas")
	}
	if !strings.Contains(validation, "data: MissionDefinitionSchema") {
		t.Error("validation schema should use the referenced typed json schema")
	}

	apiContent, err := os.ReadFile(filepath.Join(outDir, "src/types/api.ts"))
	if err != nil {
		t.Fatal(err)
	}
	api := string(apiContent)
	if !strings.Contains(api, "data: MissionDefinition;") {
		t.Error("API contracts should expose typed json payloads as concrete types")
	}
}

func TestGenerateContentBlockAsVersionedModel(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

type MissionDefinition {
  title string required
}

content mission {
  data json<MissionDefinition> required
}
`

	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if checkErrs := checker.Check(file); len(checkErrs) > 0 {
		t.Fatalf("checker errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	schemaContent, err := os.ReadFile(filepath.Join(outDir, "src/models/schema.ts"))
	if err != nil {
		t.Fatal(err)
	}
	schema := string(schemaContent)
	for _, expected := range []string{"pgTable('missions'", "key: text('key').notNull().unique()", "version: integer('version').default(1)", "status: missionStatusEnum('status').default(\"draft\")", "published: timestamp('published')", "created: timestamp('created').defaultNow()", "updated: timestamp('updated').defaultNow()"} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("expected generated content schema to contain %q\n%s", expected, schema)
		}
	}

	apiContent, err := os.ReadFile(filepath.Join(outDir, "src/types/api.ts"))
	if err != nil {
		t.Fatal(err)
	}
	api := string(apiContent)
	if !strings.Contains(api, "export interface Mission") {
		t.Fatal("content blocks should generate frontend model contracts")
	}
	if !strings.Contains(api, "data: MissionDefinition;") {
		t.Fatal("content data field should remain typed in frontend contracts")
	}
}

func TestGenerateContentWorkflowBuiltins(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

type MissionDefinition {
  title string required
}

content mission {
  data json<MissionDefinition> required
}

POST /api/admin/missions/:key/publish {
  <- key string required

  |> mission = fetch mission where(key == key)
  |> guard mission -> 404 "Mission not found"
  |> published = publish(mission)

  -> 200 { key: published.key, status: published.status }
}

POST /api/admin/missions/:key/rollback {
  <- key string required
  <- version int required

  |> mission = fetch mission where(key == key)
  |> guard mission -> 404 "Mission not found"
  |> restored = rollback(mission, version)

  -> 200 { key: restored.key, version: restored.version, status: restored.status }
}`

	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if checkErrs := checker.Check(file); len(checkErrs) > 0 {
		t.Fatalf("checker errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	routeContent, err := os.ReadFile(filepath.Join(outDir, "src/routes/admin.ts"))
	if err != nil {
		t.Fatal(err)
	}
	route := string(routeContent)
	for _, expected := range []string{
		"db.update(schema.mission).set({ status: \"published\", published: new Date(), updated: new Date() })",
		"db.update(schema.mission).set({ status: \"archived\", updated: new Date() })",
		"eq(schema.mission.version, version)",
		"return (await db.update(schema.mission).set({ status: \"published\", published: new Date(), updated: new Date() })",
	} {
		if !strings.Contains(route, expected) {
			t.Fatalf("expected content workflow code in route: %q\n%s", expected, route)
		}
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

func TestGenerateFrontendTypes(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

model todo {
  id      uuid      primary
  title   string    required
  done    bool      default(false)
  created timestamp default(now)
}

GET /api/todos {
  <- page int default(1) min(1)
  |> todos = query todo paginate(page, 10)
  -> 200 { todos: todos.items, total: todos.total, page: page }
}

POST /api/todos {
  <- title string required min(1)
  |> todo = save todo { title: title }
  -> 201 { id: todo.id, title: todo.title, done: todo.done }
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

	apiTypes, err := os.ReadFile(filepath.Join(outDir, "src/types/api.ts"))
	if err != nil {
		t.Fatal("src/types/api.ts should exist")
	}
	apiStr := string(apiTypes)
	if !strings.Contains(apiStr, "export interface Todo") {
		t.Error("api.ts should export model interface")
	}
	if !strings.Contains(apiStr, "export interface GetTodosRequest") {
		t.Error("api.ts should export request type")
	}
	if !strings.Contains(apiStr, "export type GetTodosResponse") {
		t.Error("api.ts should export response type")
	}
	if !strings.Contains(apiStr, "getTodos(input: GetTodosRequest)") {
		t.Error("api.ts should describe typed rest client methods")
	}

	schemas, err := os.ReadFile(filepath.Join(outDir, "src/types/schemas.ts"))
	if err != nil {
		t.Fatal("src/types/schemas.ts should exist")
	}
	schemasStr := string(schemas)
	if !strings.Contains(schemasStr, "export const TodoSchema") {
		t.Error("schemas.ts should export model schema")
	}
	if !strings.Contains(schemasStr, "export const GetTodosRequestSchema") {
		t.Error("schemas.ts should export request schema")
	}
	if !strings.Contains(schemasStr, "export const GetTodosResponseSchema") {
		t.Error("schemas.ts should export response schema")
	}

	client, err := os.ReadFile(filepath.Join(outDir, "src/types/client.ts"))
	if err != nil {
		t.Fatal("src/types/client.ts should exist")
	}
	clientStr := string(client)
	if !strings.Contains(clientStr, "export function createApiClient") {
		t.Error("client.ts should export createApiClient")
	}
	if !strings.Contains(clientStr, "Schemas.GetTodosRequestSchema.parse(input)") {
		t.Error("client.ts should validate requests with Zod")
	}
	if !strings.Contains(clientStr, "Schemas.GetTodosResponseSchema") {
		t.Error("client.ts should validate responses with Zod")
	}
}

func TestGenerateReactQueryHooks(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

GET /api/todos {
  <- page int default(1)
  -> 200 { page: page }
}

POST /api/todos {
  <- title string required
  -> 201 { ok: true }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New().WithReactQuery(true)
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	hooks, err := os.ReadFile(filepath.Join(outDir, "src/types/react-query.ts"))
	if err != nil {
		t.Fatal("src/types/react-query.ts should exist when react query is enabled")
	}
	hooksStr := string(hooks)
	if !strings.Contains(hooksStr, "from '@tanstack/react-query'") {
		t.Error("react-query.ts should import TanStack React Query")
	}
	if !strings.Contains(hooksStr, "export function useGetTodosQuery") {
		t.Error("react-query.ts should export query hooks for GET endpoints")
	}
	if !strings.Contains(hooksStr, "export function usePostTodosMutation") {
		t.Error("react-query.ts should export mutation hooks for non-GET endpoints")
	}
	if !strings.Contains(hooksStr, "export const getTodosQueryKey") {
		t.Error("react-query.ts should export query key helpers")
	}

	pkg, err := os.ReadFile(filepath.Join(outDir, "package.json"))
	if err != nil {
		t.Fatal("package.json should exist")
	}
	pkgStr := string(pkg)
	if !strings.Contains(pkgStr, `"@tanstack/react-query"`) {
		t.Error("package.json should include @tanstack/react-query when hooks are enabled")
	}
	if !strings.Contains(pkgStr, `"react"`) {
		t.Error("package.json should include react when hooks are enabled")
	}
}

func TestGenerateFrontendPackage(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

GET /api/health {
  -> 200 { ok: true }
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

	frontendPkg, err := os.ReadFile(filepath.Join(outDir, "frontend/package.json"))
	if err != nil {
		t.Fatal("frontend/package.json should exist")
	}
	frontendPkgStr := string(frontendPkg)
	if !strings.Contains(frontendPkgStr, `"name": "test-frontend"`) {
		t.Error("frontend package should derive a package name from the blueprint name")
	}
	if !strings.Contains(frontendPkgStr, `"./client"`) {
		t.Error("frontend package should expose client subpath exports")
	}
	if !strings.Contains(frontendPkgStr, `"publishConfig"`) {
		t.Error("frontend package should include publish metadata")
	}
	if !strings.Contains(frontendPkgStr, `"license": "MIT"`) {
		t.Error("frontend package should include a default license")
	}

	frontendReadme, err := os.ReadFile(filepath.Join(outDir, "frontend/README.md"))
	if err != nil {
		t.Fatal("frontend/README.md should exist")
	}
	frontendReadmeStr := string(frontendReadme)
	if !strings.Contains(frontendReadmeStr, "Generated frontend SDK") {
		t.Error("frontend package should include a publishable README")
	}

	frontendGitignore, err := os.ReadFile(filepath.Join(outDir, "frontend/.gitignore"))
	if err != nil {
		t.Fatal("frontend/.gitignore should exist")
	}
	frontendGitignoreStr := string(frontendGitignore)
	if !strings.Contains(frontendGitignoreStr, "node_modules/") {
		t.Error("frontend package should ignore node_modules")
	}
	if strings.Contains(frontendGitignoreStr, "frontend/node_modules/") {
		t.Error("frontend package gitignore should be package-local")
	}

	frontendIndex, err := os.ReadFile(filepath.Join(outDir, "frontend/src/index.ts"))
	if err != nil {
		t.Fatal("frontend/src/index.ts should exist")
	}
	indexStr := string(frontendIndex)
	if !strings.Contains(indexStr, "export * from './api.js'") {
		t.Error("frontend index should re-export api.ts")
	}
	if !strings.Contains(indexStr, "export * from './client.js'") {
		t.Error("frontend index should re-export client.ts")
	}

	frontendAPI, err := os.ReadFile(filepath.Join(outDir, "frontend/src/api.ts"))
	if err != nil {
		t.Fatal("frontend/src/api.ts should exist")
	}
	if !strings.Contains(string(frontendAPI), "export type GetHealthResponse") {
		t.Error("frontend package should contain copied frontend contract files")
	}

	gitignore, err := os.ReadFile(filepath.Join(outDir, ".gitignore"))
	if err != nil {
		t.Fatal("generated .gitignore should exist")
	}
	gitignoreStr := string(gitignore)
	if !strings.Contains(gitignoreStr, "frontend/node_modules/") {
		t.Error("generated .gitignore should ignore frontend package dependencies")
	}
	if !strings.Contains(gitignoreStr, "frontend/dist/") {
		t.Error("generated .gitignore should ignore frontend package build output")
	}
}

func TestGenerateFrontendOnlyPackage(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

GET /api/health {
  -> 200 { ok: true }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New().WithFrontendOnly(true)
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	for _, f := range []string{"package.json", "tsconfig.json", ".gitignore", "README.md", "src/index.ts", "src/api.ts", "src/schemas.ts", "src/client.ts"} {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected frontend-only file %s to exist", f)
		}
	}
	for _, f := range []string{"Dockerfile", ".env.example", "frontend/package.json", "src/routes"} {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("did not expect backend artifact %s in frontend-only output", f)
		}
	}

	pkg, err := os.ReadFile(filepath.Join(outDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pkg), `"name": "test-frontend"`) {
		t.Error("frontend-only package should use frontend package metadata at the root")
	}
	if !strings.Contains(string(pkg), `"publishConfig"`) {
		t.Error("frontend-only package should include publish metadata")
	}

	readme, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "Generated frontend SDK") {
		t.Error("frontend-only package should include a README")
	}

	gitignore, err := os.ReadFile(filepath.Join(outDir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	gitignoreStr := string(gitignore)
	if strings.Contains(gitignoreStr, "frontend/node_modules/") {
		t.Error("frontend-only root gitignore should not reference nested frontend paths")
	}
	if !strings.Contains(gitignoreStr, "node_modules/") || !strings.Contains(gitignoreStr, "dist/") {
		t.Error("frontend-only root gitignore should ignore package build artifacts")
	}
}

func TestGenerateFrontendRealtimeClients(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

STREAM /api/updates/:id {
  stream {
    |> on event(update) {
      -> { ok: true, id: id }
    }
    |> on timeout(5s) {
      -> { type: "ping" }
    }
  }
}

WS /ws/chat/:id {
  on_connect {
    -> { type: "connected", id: id }
  }
  on_message {
    -> { type: "message", id: id }
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

	apiTypes, err := os.ReadFile(filepath.Join(outDir, "src/types/api.ts"))
	if err != nil {
		t.Fatal("src/types/api.ts should exist")
	}
	apiStr := string(apiTypes)
	if !strings.Contains(apiStr, "export interface StreamUpdatesByIdHandlers") {
		t.Error("api.ts should export typed stream handlers")
	}
	if !strings.Contains(apiStr, "subscribeUpdatesById") {
		t.Error("api.ts should expose typed stream subscribe method")
	}
	if !strings.Contains(apiStr, "export type WsChatByIdMessage") {
		t.Error("api.ts should export WebSocket message union type")
	}
	if !strings.Contains(apiStr, "connectChatById") {
		t.Error("api.ts should expose typed WebSocket connect method")
	}

	schemas, err := os.ReadFile(filepath.Join(outDir, "src/types/schemas.ts"))
	if err != nil {
		t.Fatal("src/types/schemas.ts should exist")
	}
	schemasStr := string(schemas)
	if !strings.Contains(schemasStr, "StreamUpdatesByIdUpdateEventSchema") {
		t.Error("schemas.ts should export stream event schemas")
	}
	if !strings.Contains(schemasStr, "WsChatByIdMessageSchema") {
		t.Error("schemas.ts should export ws message schema")
	}

	client, err := os.ReadFile(filepath.Join(outDir, "src/types/client.ts"))
	if err != nil {
		t.Fatal("src/types/client.ts should exist")
	}
	clientStr := string(client)
	if !strings.Contains(clientStr, "source.addEventListener('update'") {
		t.Error("client.ts should register typed stream listeners")
	}
	if !strings.Contains(clientStr, "socket.addEventListener('message'") {
		t.Error("client.ts should register typed websocket listeners")
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
		"frontend/.gitignore",
		"frontend/package.json",
		"frontend/README.md",
		"frontend/tsconfig.json",
		"frontend/src/index.ts",
		"frontend/src/api.ts",
		"frontend/src/schemas.ts",
		"frontend/src/client.ts",
		"src/index.ts",
		"src/lib/env.ts",
		"src/lib/errors.ts",
		"src/lib/db.ts",
		"src/lib/cache.ts",
		"src/lib/storage.ts",
		"src/types.ts",
		"src/types/api.ts",
		"src/types/schemas.ts",
		"src/types/client.ts",
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
	if err := filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			count++
		}
		return nil
	}); err != nil {
		t.Fatalf("walk generated output: %v", err)
	}
	if count < 36 {
		t.Errorf("expected at least 36 generated files, got %d", count)
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

	// Check 204 uses c.body(null, 204 as const) instead of c.json
	if !strings.Contains(routeStr, "c.body(null, 204 as const)") {
		t.Error("204 response should use c.body(null, 204 as const)")
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

	if !strings.Contains(dfStr, `CMD ["node", "dist/index.js"]`) {
		t.Error("Dockerfile should use node dist/index.js with compiled TypeScript")
	}
	if !strings.Contains(dfStr, "FROM node:22-slim AS builder") {
		t.Error("Dockerfile should use multi-stage build")
	}
	if !strings.Contains(dfStr, "RUN npx tsc") {
		t.Error("Dockerfile should compile TypeScript during build")
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

	// Check type-only imports (upgradeWebSocket is passed at runtime via factory)
	if !strings.Contains(routeStr, "import type { UpgradeWebSocket, WSContext, WSMessageReceive } from 'hono/ws'") {
		t.Error("ws route should import UpgradeWebSocket, WSContext and WSMessageReceive types from hono/ws")
	}
	// Check factory function export
	if !strings.Contains(routeStr, "export function create") {
		t.Error("ws route should export a factory function")
	}

	// Check route uses upgradeWebSocket
	if !strings.Contains(routeStr, "upgradeWebSocket(") {
		t.Error("ws route should use upgradeWebSocket")
	}

	// Check path is registered
	if !strings.Contains(routeStr, ".get('/ws/chat'") {
		t.Error("ws route should register GET handler at /ws/chat")
	}

	// Check lifecycle handlers with type annotations
	if !strings.Contains(routeStr, "onOpen(evt: Event, ws: WSContext)") {
		t.Error("ws route should have typed onOpen handler")
	}
	if !strings.Contains(routeStr, "onMessage(event: MessageEvent<WSMessageReceive>, ws: WSContext)") {
		t.Error("ws route should have typed onMessage handler")
	}
	if !strings.Contains(routeStr, "onClose(evt: CloseEvent, ws: WSContext)") {
		t.Error("ws route should have typed onClose handler")
	}

	// Check message binding with JSON parse
	if !strings.Contains(routeStr, "const message = typeof event.data === 'string' ? JSON.parse(event.data) : event.data") {
		t.Error("ws route onMessage should parse event.data as JSON")
	}

	// Check index.ts imports node-ws adapter
	indexContent, err := os.ReadFile(filepath.Join(outDir, "src/index.ts"))
	if err != nil {
		t.Fatal("src/index.ts should exist")
	}
	indexStr := string(indexContent)

	// 1. Check index.ts imports createNodeWebSocket from @hono/node-ws
	if !strings.Contains(indexStr, "import { createNodeWebSocket } from '@hono/node-ws'") {
		t.Error("index.ts should import createNodeWebSocket from @hono/node-ws")
	}

	// 2. Check index.ts creates upgradeWebSocket via createNodeWebSocket({ app })
	if !strings.Contains(indexStr, "createNodeWebSocket({ app })") {
		t.Error("index.ts should create upgradeWebSocket via createNodeWebSocket({ app })")
	}

	// 3. Check that index.ts imports the factory function (e.g., createWsRoutes)
	if !strings.Contains(indexStr, "createWsRoutes") {
		t.Error("index.ts should import createWsRoutes factory function")
	}

	// 4. Check that index.ts calls createWsRoutes(upgradeWebSocket) to mount WS routes
	if !strings.Contains(indexStr, "createWsRoutes(upgradeWebSocket)") {
		t.Error("index.ts should call createWsRoutes(upgradeWebSocket)")
	}

	// 5. Check that index.ts calls injectWebSocket(server) after starting the server
	if !strings.Contains(indexStr, "injectWebSocket(server)") {
		t.Error("index.ts should call injectWebSocket(server) after starting the server")
	}

	// Check package.json includes @hono/node-ws dependency
	pkgContent, err := os.ReadFile(filepath.Join(outDir, "package.json"))
	if err != nil {
		t.Fatal("package.json should exist")
	}
	pkgStr := string(pkgContent)
	if !strings.Contains(pkgStr, "@hono/node-ws") {
		t.Error("package.json should include @hono/node-ws dependency for WS endpoints")
	}
}

func TestGenerateWsPathParams(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

WS /ws/rooms/:id {
  on_connect {
    |> log "connected to room"
  }
  on_message {
    |> log "received message"
  }
  on_disconnect {
    |> log "disconnected"
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

	// extractResource("/ws/rooms/:id") returns "ws" (first non-api, non-param segment)
	routeContent, err := os.ReadFile(filepath.Join(outDir, "src/routes/ws.ts"))
	if err != nil {
		t.Fatal("src/routes/ws.ts should exist for WS /ws/rooms/:id endpoint")
	}
	routeStr := string(routeContent)

	// Check path params are extracted from closure
	if !strings.Contains(routeStr, "const id = c.req.param('id')") {
		t.Error("ws route should extract path param 'id' via c.req.param('id')")
	}

	// Check the handler signature includes types
	if !strings.Contains(routeStr, "onOpen(evt: Event, ws: WSContext)") {
		t.Error("ws route onOpen should have typed parameters")
	}

	// Check path is registered with param
	if !strings.Contains(routeStr, ".get('/ws/rooms/:id'") {
		t.Error("ws route should register GET handler at /ws/rooms/:id")
	}
}

func TestGenerateStreamPathParams(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

model room {
  id   uuid   primary
  name string required
}

STREAM /api/rooms/:id/live {
  stream {
    |> on event(update) {
      |> log "sending update"
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

	routeContent, err := os.ReadFile(filepath.Join(outDir, "src/routes/rooms.ts"))
	if err != nil {
		t.Fatal("src/routes/rooms.ts should exist for STREAM /api/rooms/:id/live endpoint")
	}
	routeStr := string(routeContent)

	// Check path params are extracted
	if !strings.Contains(routeStr, "const id = c.req.param('id')") {
		t.Error("stream route should extract path param 'id' via c.req.param('id')")
	}

	// Check streamSSE is still used
	if !strings.Contains(routeStr, "streamSSE(c, async (stream) =>") {
		t.Error("stream route should use streamSSE")
	}
}

func TestGenerateEnvRedisURL(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  cache   redis
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

	envContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/env.ts"))
	if err != nil {
		t.Fatal("src/lib/env.ts should exist")
	}
	envStr := string(envContent)

	if !strings.Contains(envStr, "REDIS_URL: z.string()") {
		t.Error("env.ts should include REDIS_URL in the env schema when cache is redis")
	}
}

func TestGenerateEnvRedisURLNotDuplicated(t *testing.T) {
	// When REDIS_URL is already declared as a secret, it should not appear twice
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  cache   redis
}

secret REDIS_URL required`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	envContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/env.ts"))
	if err != nil {
		t.Fatal("src/lib/env.ts should exist")
	}
	envStr := string(envContent)

	// Count occurrences of REDIS_URL — should appear exactly once
	count := strings.Count(envStr, "REDIS_URL")
	if count != 1 {
		t.Errorf("REDIS_URL should appear exactly once in env schema when also declared as secret, got %d", count)
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

	if !strings.Contains(workerStr, "processJob") {
		t.Errorf("expected processJob function name, got:\n%s", workerStr)
	}
	if !strings.Contains(workerStr, "export async function processJob(data: any)") {
		t.Errorf("expected 'export async function processJob(data: any)', got:\n%s", workerStr)
	}
	if !strings.Contains(workerStr, "Promise<void>") {
		t.Errorf("expected Promise<void> return type, got:\n%s", workerStr)
	}
	if !strings.Contains(workerStr, `console.log("Processing job")`) {
		t.Errorf("expected console.log for log step, got:\n%s", workerStr)
	}
	if !strings.Contains(workerStr, "processJobOnFail") {
		t.Errorf("expected processJobOnFail function for on_fail block, got:\n%s", workerStr)
	}
	if !strings.Contains(workerStr, "export async function processJobOnFail(data: any, error: Error)") {
		t.Errorf("expected 'export async function processJobOnFail(data: any, error: Error)', got:\n%s", workerStr)
	}
	if !strings.Contains(workerStr, `console.log("Job failed")`) {
		t.Errorf("expected console.log(\"Job failed\") in on_fail handler, got:\n%s", workerStr)
	}
	if !strings.Contains(workerStr, "Generated by Blueprint") {
		t.Errorf("expected Generated by Blueprint header, got:\n%s", workerStr)
	}
}

func TestGen_WorkerMetadataWiring(t *testing.T) {
	src := `blueprint "x" {
  version "1.0"
  port 8080
  runtime node
  queue redis
}

secret REDIS_URL required

worker process_job {
  trigger queue("process_jobs")
  retry 3 backoff(exponential, base: 1s, max: 30s)
  timeout 5min

  |> log "Processing job"

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

	workerContent, err := os.ReadFile(filepath.Join(outDir, "src/workers/process-job.ts"))
	if err != nil {
		t.Fatal(err)
	}
	workerStr := string(workerContent)
	for _, expected := range []string{
		`export const processJobQueueName = "process_jobs";`,
		`export const processJobTimeoutMs = 300000;`,
		`export const processJobRetryCount = 3;`,
		`export const processJobBackoff = { strategy: exponential, base: 1 * 1000, max: 30 * 1000 };`,
		`export async function processJobOnFail(data: any, error: Error): Promise<void> {`,
	} {
		if !strings.Contains(workerStr, expected) {
			t.Fatalf("expected worker metadata output %q\n%s", expected, workerStr)
		}
	}

	indexContent, err := os.ReadFile(filepath.Join(outDir, "src/index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	indexStr := string(indexContent)
	for _, expected := range []string{
		`import { processJob, processJobOnFail, processJobQueueName, processJobTimeoutMs } from './workers/process-job.js'`,
		`new Worker(processJobQueueName, async (job) => {`,
		`await Promise.race([processJob(job.data), new Promise((_, reject) => setTimeout(() => reject(new Error('Worker timeout')), processJobTimeoutMs))]);`,
		`await processJobOnFail(job.data, error instanceof Error ? error : new Error(String(error)));`,
	} {
		if !strings.Contains(indexStr, expected) {
			t.Fatalf("expected worker runtime wiring %q\n%s", expected, indexStr)
		}
	}
}

func TestGenerateLocalizationMetadata(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

locale en default
locale "fr-FR" fallback(en)

translation mission_text {
  key "mission.start"
  key "mission.complete"
  locale en {
    "mission.start": "Start mission"
  }
}

type MissionDefinition {
  title_key tkey(mission_text) required
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

	i18nContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/i18n.ts"))
	if err != nil {
		t.Fatal(err)
	}
	i18n := string(i18nContent)
	for _, expected := range []string{
		`export const locales = ["en", "fr-FR"] as const;`,
		`export const defaultLocale = "en";`,
		`export const localeFallbacks = { "en": "en", "fr-FR": "en" } as const;`,
		`missionText: ["mission.start", "mission.complete"]`,
		`export const translationValues = { missionText: { "en": { "mission.start": "Start mission" } } } as const;`,
		`export type LocaleCode = (typeof locales)[number];`,
	} {
		if !strings.Contains(i18n, expected) {
			t.Fatalf("expected i18n metadata %q\n%s", expected, i18n)
		}
	}

	frontendI18n, err := os.ReadFile(filepath.Join(outDir, "frontend/src/i18n.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frontendI18n), `export const locales = ["en", "fr-FR"] as const;`) {
		t.Fatal("frontend package should include generated i18n metadata")
	}
	frontendIndex, err := os.ReadFile(filepath.Join(outDir, "frontend/src/index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frontendIndex), "export * from './i18n.js';") {
		t.Fatal("frontend package index should re-export i18n metadata")
	}
	typesContent, err := os.ReadFile(filepath.Join(outDir, "src/types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(typesContent), `titleKey: "mission.start" | "mission.complete";`) {
		t.Fatal("translation key types should generate literal unions in TypeScript contracts")
	}
}

func TestGenerateStateMachineAndAnalyticsAndSaveHelpers(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

state mission_status {
  draft -> reviewed
  reviewed -> published
}

analytics gameplay {
  event mission_started
  sink console
  sink http("https://analytics.example.com/events")
}

model save_slot {
  id uuid primary
  save_version int default(1)
}

save player_progress {
  model save_slot
  version_field save_version
  latest 3
  migrate 1 -> 2 using "./custom-player-progress"
}

POST /api/test {
  <- payload json required
  |> track("mission_started", payload)
  |> status = transition(mission_status, "draft", "reviewed")
  |> upgraded = upgrade_save(player_progress, payload)
  -> 200 { ok: true, status: status, upgraded: upgraded }
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

	typesContent, err := os.ReadFile(filepath.Join(outDir, "src/types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	types := string(typesContent)
	for _, expected := range []string{
		`export const MissionStatusStates = ["draft", "reviewed", "published"] as const;`,
		`export type MissionStatus = (typeof MissionStatusStates)[number];`,
		`export const MissionStatusTransitions = {`,
	} {
		if !strings.Contains(types, expected) {
			t.Fatalf("expected state machine output %q\n%s", expected, types)
		}
	}

	stateContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/state.ts"))
	if err != nil {
		t.Fatal(err)
	}
	stateLib := string(stateContent)
	for _, expected := range []string{
		`export function canTransitionMissionStatus(from: string, to: string): boolean {`,
		`export function transitionMissionStatus<T extends string>(from: T, to: T): T {`,
		`if (!canTransitionMissionStatus(from, to)) throw new InvalidTransitionError("mission_status", from, to);`,
	} {
		if !strings.Contains(stateLib, expected) {
			t.Fatalf("expected state lib output %q\n%s", expected, stateLib)
		}
	}

	analyticsContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/analytics.ts"))
	if err != nil {
		t.Fatal(err)
	}
	analytics := string(analyticsContent)
	for _, expected := range []string{
		`export const analyticsNamespaces = { gameplay: ["mission_started"] } as const;`,
		`export const analyticsSinks = { gameplay: [{ kind: "console" }, { kind: "http", target: "https://analytics.example.com/events" }] } as const;`,
		`const analyticsBatchSize = 25;`,
		`async function sendAnalyticsBatch(target: string, batch: Array<{ namespace: AnalyticsNamespace; event: string; payload: unknown; at: string }>): Promise<void> {`,
		`queueAnalytics(String(sink.target), { namespace, event, payload, at: new Date().toISOString() });`,
	} {
		if !strings.Contains(analytics, expected) {
			t.Fatalf("expected analytics output %q\n%s", expected, analytics)
		}
	}

	saveContent, err := os.ReadFile(filepath.Join(outDir, "src/lib/save-migrations.ts"))
	if err != nil {
		t.Fatal(err)
	}
	saveLib := string(saveContent)
	for _, expected := range []string{
		`export const playerProgressSaveConfig = { model: "save_slot", versionField: "save_version", latest: 3 } as const;`,
		`export async function upgradePlayerProgressSave<T extends Record<string, any>>(save: T): Promise<T> {`,
		`current = await migratePlayerProgressSaveFrom1To2(current);`,
		`current = await migratePlayerProgressSaveFrom2To3(current);`,
		`import { migratePlayerProgressSaveFrom1To2 } from "../saves/custom-player-progress.js";`,
	} {
		if !strings.Contains(saveLib, expected) {
			t.Fatalf("expected save migration output %q\n%s", expected, saveLib)
		}
	}

	customMigrationContent, err := os.ReadFile(filepath.Join(outDir, "src/saves/custom-player-progress.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(customMigrationContent), `export async function migratePlayerProgressSaveFrom1To2(save: any): Promise<any> {`) {
		t.Fatal("custom migration hook stubs should be generated for using paths")
	}

	routeContent, err := os.ReadFile(filepath.Join(outDir, "src/routes/test.ts"))
	if err != nil {
		t.Fatal(err)
	}
	route := string(routeContent)
	for _, expected := range []string{
		`import { track } from '../lib/analytics.js';`,
		`import { upgradePlayerProgressSave } from '../lib/save-migrations.js';`,
		`import { transitionMissionStatus } from '../lib/state.js';`,
		`await track("mission_started", payload)`,
		`transitionMissionStatus("draft", "reviewed")`,
		`await upgradePlayerProgressSave(payload)`,
	} {
		if !strings.Contains(route, expected) {
			t.Fatalf("expected route integration %q\n%s", expected, route)
		}
	}
}

func TestGenerateContentBundleOps(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

type MissionDefinition {
  name string required
}

content mission {
  data json<MissionDefinition> required
}

POST /api/missions/import {
  <- bundle json required
  |> imported = import_bundle(mission, bundle)
  -> 200 { items: imported }
}

GET /api/missions/export {
  |> bundle = export_bundle(mission)
  -> 200 { items: bundle }
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
	routeContent, err := os.ReadFile(filepath.Join(outDir, "src/routes/missions.ts"))
	if err != nil {
		t.Fatal(err)
	}
	route := string(routeContent)
	for _, expected := range []string{
		`await Promise.all((bundle ?? []).map(async (item: any) => {`,
		`db.select().from(schema.mission).where(and(eq(schema.mission.key, item.key), eq(schema.mission.version, item.version))).limit(1)`,
		`db.update(schema.mission).set({ ...item, updated: new Date(), published: item.published ?? null })`,
		`await db.select().from(schema.mission).orderBy(asc(schema.mission.key), desc(schema.mission.version))`,
	} {
		if !strings.Contains(route, expected) {
			t.Fatalf("expected content bundle op code %q\n%s", expected, route)
		}
	}
}

func TestGenerateGamePlatformTypeScriptCompiles(t *testing.T) {
	src := `blueprint "game-platform" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
}

secret DATABASE_URL required

locale en default
locale "fr-FR" fallback(en)

translation mission_text {
  key "mission.start"
  key "mission.complete"

  locale en {
    "mission.start": "Start mission"
    "mission.complete": "Mission complete"
  }
}

state mission_status {
  draft -> reviewed
  reviewed -> published
}

analytics gameplay {
  event mission_started
  sink console
  sink http("https://analytics.example.com/events")
}

type MissionDefinition {
  title_key tkey(mission_text) required
  status    mission_status required
}

content mission {
  data json<MissionDefinition> required
}

model save_slot {
  id           uuid primary
  save_version int default(1)
  status       mission_status default("draft")
}

save player_progress {
  model save_slot
  version_field save_version
  latest 3
  migrate 1 -> 2 using "./custom-player-progress"
}

POST /api/missions/import {
  <- bundle json required
  |> imported = import_bundle(mission, bundle)
  -> 200 { items: imported }
}

GET /api/missions/export {
  |> bundle = export_bundle(mission)
  -> 200 { items: bundle }
}

POST /api/test {
  <- payload json required
  |> track("mission_started", payload)
  |> status = transition(mission_status, "draft", "reviewed")
  |> upgraded = upgrade_save(player_progress, payload)
  -> 200 { ok: true, status: status, upgraded: upgraded }
}
`
	file, errs := parser.ParseFile("game-platform.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if checkErrs := checker.Check(file); len(checkErrs) > 0 {
		t.Fatalf("checker errors: %v", checkErrs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	requireTypeScriptCompile(t, outDir)
	requireTypeScriptCompile(t, filepath.Join(outDir, "frontend"))
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
	if !strings.Contains(ts, "readFileSync(join(import.meta.dirname, '..', 'testdata/sample.png'))") {
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
	// Should emit db.select() query — schema exports use singular model name
	if !strings.Contains(ts, "db.select().from(schema.job)") {
		t.Errorf("should emit db.select().from(schema.job); got:\n%s", ts)
	}
	// Should include eq conditions
	if !strings.Contains(ts, "eq(schema.job.id") {
		t.Errorf("should emit eq(schema.job.id, ...); got:\n%s", ts)
	}
	if !strings.Contains(ts, "eq(schema.job.status, 'pending')") {
		t.Errorf("should emit eq(schema.job.status, 'pending'); got:\n%s", ts)
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
	// Should emit HMAC verification with validated env secret key
	if !strings.Contains(ts, "env.STRIPE_KEY") {
		t.Errorf("should use validated env.STRIPE_KEY; got:\n%s", ts)
	}
	// Should emit timing-safe signature comparison
	if !strings.Contains(ts, "timingSafeEqual(_sigBuf, Buffer.from(_expected, 'hex'))") {
		t.Errorf("should use timingSafeEqual for comparison; got:\n%s", ts)
	}
	// Should return 401 on bad signature
	if !strings.Contains(ts, "return c.json({ error: 'Invalid signature' }, 401 as const)") {
		t.Errorf("should return 401 on bad signature; got:\n%s", ts)
	}
	// Should wrap JSON.parse in try/catch
	if !strings.Contains(ts, "try { data = JSON.parse(_payload); } catch { return c.json({ error: 'Invalid payload' }, 400 as const); }") {
		t.Errorf("should wrap JSON.parse in try/catch; got:\n%s", ts)
	}
}

func TestQueryWithOptionalWhereParams(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port 3000
  runtime node
}
model note {
  title    string
  body     string
  pinned   bool
}
GET /api/notes {
  <- q      string optional
  <- pinned bool   optional
  |> notes = query note where(q, pinned)
  -> 200 { notes: notes }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	checker.Check(file)
	outDir := t.TempDir()
	g := New()
	if err := g.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	tsBytes, err := os.ReadFile(filepath.Join(outDir, "src/routes/notes.ts"))
	if err != nil {
		t.Fatalf("notes.ts not generated: %v", err)
	}
	ts := string(tsBytes)
	// q should generate ILIKE targeting specific text columns, not the whole table
	if !strings.Contains(ts, "schema.note.title} ILIKE") {
		t.Errorf("search param 'q' should target schema.note.title; got:\n%s", ts)
	}
	if !strings.Contains(ts, "schema.note.body} ILIKE") {
		t.Errorf("search param 'q' should target schema.note.body; got:\n%s", ts)
	}
	// pinned should generate eq() with null check
	if !strings.Contains(ts, "eq(schema.note.pinned, pinned)") {
		t.Errorf("boolean filter 'pinned' should generate eq(); got:\n%s", ts)
	}
	// Should filter out undefined optional conditions
	if !strings.Contains(ts, ".filter(Boolean)") {
		t.Errorf("optional where params should use .filter(Boolean); got:\n%s", ts)
	}
}

func TestNonPaginatedQueryNoItems(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port 3000
  runtime node
}
model tag {
  name string
}
GET /api/tags {
  |> all_tags = query tag
  -> 200 { tag_list: all_tags.items }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	checker.Check(file)
	outDir := t.TempDir()
	g := New()
	if err := g.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	tsBytes, err := os.ReadFile(filepath.Join(outDir, "src/routes/tags.ts"))
	if err != nil {
		t.Fatalf("tags.ts not generated: %v", err)
	}
	ts := string(tsBytes)
	// Non-paginated query — .items should be stripped (just use allTags directly)
	if strings.Contains(ts, "allTags.items") {
		t.Errorf("non-paginated query should not use .items; got:\n%s", ts)
	}
	// Should just reference allTags directly with original key name
	if !strings.Contains(ts, "tag_list: allTags") {
		t.Errorf("should reference allTags directly with original key name; got:\n%s", ts)
	}
}

func TestFetchWithCompoundWhere(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port 3000
  runtime node
}
model note_tag {
  note_id uuid ref(note)
  tag_id  uuid ref(tag)
}
model note {
  title string
}
model tag {
  name string
}
DELETE /api/notes/:note_id/tags/:tag_id {
  <- note_id uuid required
  <- tag_id  uuid required
  |> link = fetch note_tag where(note_id == note_id, tag_id == tag_id)
  -> 204
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	checker.Check(file)
	outDir := t.TempDir()
	g := New()
	if err := g.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	// Route could be note-tags.ts or notes.ts
	ts := ""
	for _, name := range []string{"src/routes/note-tags.ts", "src/routes/notes.ts"} {
		if data, err := os.ReadFile(filepath.Join(outDir, name)); err == nil {
			ts = string(data)
			break
		}
	}
	if ts == "" {
		t.Fatal("route file not generated")
	}
	// Should use and() with eq() conditions, not nested where() as a value
	if strings.Contains(ts, "eq(schema.noteTag.id, where(") {
		t.Errorf("fetch should not nest where() inside eq(); got:\n%s", ts)
	}
	// Should generate and(eq(...), eq(...))
	if !strings.Contains(ts, "and(eq(schema.noteTag.noteId,") {
		t.Errorf("compound fetch where should use and(eq(...)); got:\n%s", ts)
	}
}

func TestInArrayInWhereClause(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port 3000
  runtime node
}
model tag {
  name string
}
model note_tag {
  note_id uuid ref(note)
  tag_id  uuid ref(tag)
}
model note {
  title string
}
GET /api/notes/:id/tags {
  <- id uuid required
  |> links = query note_tag where(note_id == id)
  |> tags = query tag where(id in links.tag_id)
  -> 200 { tags: tags }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	checker.Check(file)
	outDir := t.TempDir()
	g := New()
	if err := g.Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	ts := ""
	for _, name := range []string{"src/routes/notes.ts", "src/routes/tags.ts", "src/routes/note-tags.ts"} {
		if data, err := os.ReadFile(filepath.Join(outDir, name)); err == nil {
			ts = string(data)
			break
		}
	}
	if ts == "" {
		entries, _ := os.ReadDir(filepath.Join(outDir, "src/routes"))
		for _, e := range entries {
			t.Logf("generated route: %s", e.Name())
		}
		t.Fatal("notes route not generated")
	}
	// Should use inArray(), not .includes()
	if strings.Contains(ts, ".includes(") {
		t.Errorf("where(id in collection.field) should use inArray, not .includes(); got:\n%s", ts)
	}
	if !strings.Contains(ts, "inArray(schema.tag.id,") {
		t.Errorf("should generate inArray(schema.tag.id, ...); got:\n%s", ts)
	}
	// Should use .map() to extract the field from the collection
	if !strings.Contains(ts, ".map((r: any) => r.tagId)") {
		t.Errorf("should .map() to extract tag_id field; got:\n%s", ts)
	}
}
