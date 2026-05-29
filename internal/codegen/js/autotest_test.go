package js

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const autoTestTodoSrc = `blueprint "todo-api" {
  version "1.0.0"
  port 3000
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

@ "Create a todo"
POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id, title: todo.title }
}

@ "Get a todo"
GET /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "Not found"
  -> 200 { id: todo.id, title: todo.title }
}
`

func buildWithGenTests(t *testing.T, src string) string {
	t.Helper()
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := New().WithGenTests(true).Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	return outDir
}

func readGen(t *testing.T, outDir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(outDir, rel))
	if err != nil {
		t.Fatalf("expected %s to exist: %v", rel, err)
	}
	return string(data)
}

func TestGen_AutoTests_HarnessFiles(t *testing.T) {
	outDir := buildWithGenTests(t, autoTestTodoSrc)

	ddl := readGen(t, outDir, "test/_harness/ddl.ts")
	if !strings.Contains(ddl, `CREATE TABLE IF NOT EXISTS "todos"`) {
		t.Errorf("ddl should create todos table, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, `"id" uuid PRIMARY KEY DEFAULT gen_random_uuid()`) {
		t.Errorf("ddl should declare uuid primary key, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, `"title" text NOT NULL`) {
		t.Errorf("ddl should mark title NOT NULL, got:\n%s", ddl)
	}

	db := readGen(t, outDir, "test/_harness/db.ts")
	for _, want := range []string{"@electric-sql/pglite", "drizzle-orm/pglite", "export const db", "export async function resetDb", "TRUNCATE"} {
		if !strings.Contains(db, want) {
			t.Errorf("db.ts missing %q, got:\n%s", want, db)
		}
	}

	setup := readGen(t, outDir, "test/_harness/setup.ts")
	if !strings.Contains(setup, "process.env.DATABASE_URL ||=") {
		t.Errorf("setup should default DATABASE_URL, got:\n%s", setup)
	}

	cfg := readGen(t, outDir, "vitest.config.ts")
	if !strings.Contains(cfg, "setupFiles: ['./test/_harness/setup.ts']") {
		t.Errorf("vitest.config should register setup file, got:\n%s", cfg)
	}

	// pglite devDep added to package.json.
	pkg := readGen(t, outDir, "package.json")
	if !strings.Contains(pkg, "@electric-sql/pglite") {
		t.Errorf("package.json should include pglite devDep, got:\n%s", pkg)
	}
}

func TestGen_AutoTests_ContractTest(t *testing.T) {
	outDir := buildWithGenTests(t, autoTestTodoSrc)
	test := readGen(t, outDir, "test/generated/todos.test.ts")

	for _, want := range []string{
		"vi.mock('../../src/lib/db'",
		"import app from '../../src/index.js'",
		"beforeEach(async () => { await resetDb(); });",
		"await app.request(",
		".toContain(res.status)",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("contract test missing %q, got:\n%s", want, test)
		}
	}

	// POST body is synthesized from the input schema.
	if !strings.Contains(test, "body: JSON.stringify({ title: 'test' })") {
		t.Errorf("expected synthesized POST body, got:\n%s", test)
	}
	// GET /:id seeds a row and uses its id in the path; 404 is a declared status.
	if !strings.Contains(test, "db.insert(schema.todo)") {
		t.Errorf("expected GET /:id to seed a todo, got:\n%s", test)
	}
	if !strings.Contains(test, "${_seed0.id}") {
		t.Errorf("expected seeded id used in path, got:\n%s", test)
	}
	if !strings.Contains(test, "404") {
		t.Errorf("expected 404 in declared statuses, got:\n%s", test)
	}
}

func TestGen_AutoTests_FKSeeding(t *testing.T) {
	src := `blueprint "shop" {
  version "1.0.0"
  port 3000
  runtime node
  database postgres
}

model product {
  id  uuid   primary
  sku string unique required
}

model cart_item {
  id         uuid primary
  product_id uuid ref(product) required
}

@ "Get cart item"
GET /api/cart_items/:id {
  <- id uuid required
  |> item = fetch cart_item(id)
  |> guard item -> 404 "Not found"
  -> 200 { id: item.id }
}
`
	outDir := buildWithGenTests(t, src)

	ddl := readGen(t, outDir, "test/_harness/ddl.ts")
	if !strings.Contains(ddl, `ALTER TABLE "cart_items" ADD CONSTRAINT "cart_items_product_id_fkey" FOREIGN KEY ("product_id") REFERENCES "products" ("id")`) {
		t.Errorf("ddl should add FK constraint, got:\n%s", ddl)
	}

	test := readGen(t, outDir, "test/generated/cart-items.test.ts")
	// Parent product must be seeded before the cart_item, and the cart_item references it.
	idxProduct := strings.Index(test, "db.insert(schema.product)")
	idxCart := strings.Index(test, "db.insert(schema.cartItem)")
	if idxProduct < 0 || idxCart < 0 {
		t.Fatalf("expected both product and cart_item seeds, got:\n%s", test)
	}
	if idxProduct > idxCart {
		t.Errorf("expected product seeded before cart_item, got:\n%s", test)
	}
	if !strings.Contains(test, "productId: _seed0.id") {
		t.Errorf("expected cart_item to reference seeded product id, got:\n%s", test)
	}
	// Unique string column seeded with a unique value.
	if !strings.Contains(test, "sku: crypto.randomUUID()") {
		t.Errorf("expected unique sku seed, got:\n%s", test)
	}
}

func TestGen_AutoTests_DisabledByDefault(t *testing.T) {
	file, errs := parser.ParseFile("test.bp", []byte(autoTestTodoSrc))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := New().Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "test/_harness/db.ts")); !os.IsNotExist(err) {
		t.Errorf("harness should not be generated without --gen-tests")
	}
	if _, err := os.Stat(filepath.Join(outDir, "vitest.config.ts")); !os.IsNotExist(err) {
		t.Errorf("vitest.config should not be generated without --gen-tests")
	}
	pkg := readGen(t, outDir, "package.json")
	if strings.Contains(pkg, "@electric-sql/pglite") {
		t.Errorf("pglite devDep should not be added without --gen-tests")
	}
}
