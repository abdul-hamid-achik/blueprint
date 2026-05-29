package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const helloWorldSrc = `@ "A simple hello world API"
blueprint "hello-world" {
  version "1.0.0"
  port 3000
  runtime node
}

@ "Health check endpoint"
GET /api/health {
  -> 200 { status: "ok", version: "1.0.0" }
}

@ "Greeting endpoint"
GET /api/hello/:name {
  <- name string required
  -> 200 { message: "Hello, {name}!" }
}
`

func buildPython(t *testing.T, src string) string {
	t.Helper()
	file, errs := parser.ParseFile("t.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := python.New().Generate(file, outDir); err != nil {
		t.Fatalf("python generate: %v", err)
	}
	return outDir
}

func readPy(t *testing.T, outDir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(outDir, rel))
	if err != nil {
		t.Fatalf("expected %s: %v", rel, err)
	}
	return string(data)
}

func TestPython_HelloWorldEmitsExpectedFiles(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	for _, rel := range []string{
		"pyproject.toml",
		"README.md",
		"src/__init__.py",
		"src/lib/__init__.py",
		"src/lib/env.py",
		"src/app.py",
		"src/routes/__init__.py",
		"src/routes/health.py",
		"src/routes/hello.py",
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestPython_AppPyWiresRouters(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	app := readPy(t, outDir, "src/app.py")
	for _, want := range []string{
		"from fastapi import FastAPI",
		"from src.routes import health",
		"from src.routes import hello",
		`FastAPI(title="hello-world", version="1.0.0")`,
		"app.include_router(health.router)",
		"app.include_router(hello.router)",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("src/app.py missing %q, got:\n%s", want, app)
		}
	}
}

func TestPython_RouteFileForPathParam(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	body := readPy(t, outDir, "src/routes/hello.py")
	for _, want := range []string{
		`@router.get("/api/hello/{name}")`,
		"async def get_hello_name(name: str):",
		`return JSONResponse({"message": f"Hello, {name}!"}, status_code=200)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("hello.py missing %q, got:\n%s", want, body)
		}
	}
}

func TestPython_RouteFileForStaticEndpoint(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	body := readPy(t, outDir, "src/routes/health.py")
	if !strings.Contains(body, `@router.get("/api/health")`) {
		t.Errorf("health route should be registered, got:\n%s", body)
	}
	if !strings.Contains(body, "async def get_health():") {
		t.Errorf("health handler missing, got:\n%s", body)
	}
}

func TestPython_PyProjectHasFastAPIAndUvicorn(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	body := readPy(t, outDir, "pyproject.toml")
	for _, want := range []string{
		`name = "hello-world"`,
		`version = "1.0.0"`,
		"fastapi",
		"uvicorn",
		"pydantic",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pyproject.toml missing %q, got:\n%s", want, body)
		}
	}
}

func TestPython_UnsupportedFeaturesAreRejected(t *testing.T) {
	cases := map[string]string{
		"pipe declaration": `blueprint "x" { version "1.0" port 3000 runtime node }
pipe validate { <- v string  -> v }
GET /api/x { -> 200 "ok" }
`,
		"worker declaration": `blueprint "x" { version "1.0" port 3000 runtime node }
worker process { trigger "job"  |> log "doing work" }
GET /api/x { -> 200 "ok" }
`,
		"storage": `blueprint "x" { version "1.0" port 3000 runtime node storage s3 }
GET /api/x { -> 200 "ok" }
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected unsupported-feature error for %s", name)
			}
			if !strings.Contains(err.Error(), "python target does not yet support") {
				t.Errorf("error should explain phase: %v", err)
			}
		})
	}
}

const phase2Src = `@ "Phase 2 minimal"
blueprint "phase2" {
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

@ "Health"
GET /api/health {
  -> 200 { status: "ok" }
}
`

func TestPython_Phase2EmitsModelLayer(t *testing.T) {
	outDir := buildPython(t, phase2Src)
	// The data layer + alembic skeleton is present.
	for _, rel := range []string{
		"src/models/__init__.py",
		"src/models/schema.py",
		"src/models/pydantic.py",
		"src/lib/db.py",
		"alembic.ini",
		"alembic/env.py",
		"alembic/script.py.mako",
		"alembic/versions/__init__.py",
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestPython_Phase2SQLAlchemyShape(t *testing.T) {
	outDir := buildPython(t, phase2Src)
	body := readPy(t, outDir, "src/models/schema.py")
	for _, want := range []string{
		"from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column",
		"class Base(DeclarativeBase):",
		`class Todo(Base):`,
		`__tablename__ = "todos"`,
		"id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, nullable=False, default=uuid.uuid4)",
		"title: Mapped[str] = mapped_column(String(), nullable=False)",
		"done: Mapped[bool] = mapped_column(Boolean(), default=False)",
		"created: Mapped[datetime] = mapped_column(DateTime(), default=datetime.utcnow)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("schema.py missing %q, got:\n%s", want, body)
		}
	}
}

func TestPython_Phase2PydanticShape(t *testing.T) {
	outDir := buildPython(t, phase2Src)
	body := readPy(t, outDir, "src/models/pydantic.py")
	for _, want := range []string{
		"from pydantic import BaseModel, ConfigDict",
		"from uuid import UUID",
		"from datetime import datetime",
		"class Todo(BaseModel):",
		"model_config = ConfigDict(from_attributes=True)",
		"id: UUID",
		"title: str",
		"done: bool",
		"created: datetime",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pydantic.py missing %q, got:\n%s", want, body)
		}
	}
}

func TestPython_Phase2PyProjectIncludesDBDeps(t *testing.T) {
	outDir := buildPython(t, phase2Src)
	body := readPy(t, outDir, "pyproject.toml")
	for _, want := range []string{"sqlalchemy", "psycopg", "alembic"} {
		if !strings.Contains(body, want) {
			t.Errorf("pyproject.toml should include %s when database is declared, got:\n%s", want, body)
		}
	}
}

func TestPython_Phase2EnvHasDatabaseURL(t *testing.T) {
	outDir := buildPython(t, phase2Src)
	body := readPy(t, outDir, "src/lib/env.py")
	if !strings.Contains(body, "DATABASE_URL: str") {
		t.Errorf("env.py should declare DATABASE_URL when database is in use, got:\n%s", body)
	}
}

func TestPython_Phase2ForeignKeyEmitted(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model product { id uuid primary }
model cart_item {
  id uuid primary
  product_id uuid ref(product) required
}
GET /api/health { -> 200 "ok" }
`
	outDir := buildPython(t, src)
	body := readPy(t, outDir, "src/models/schema.py")
	if !strings.Contains(body, `ForeignKey("products.id")`) {
		t.Errorf("schema.py should emit a foreign key on the FK column, got:\n%s", body)
	}
}

// ---------- Phase 3 ----------

const phase3CRUDSrc = `blueprint "todos" {
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
}

GET /api/todos {
  |> todos = query todo
  -> 200 { todos: todos, count: 0 }
}

POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id, title: todo.title }
}

GET /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "not found"
  -> 200 { id: todo.id, title: todo.title }
}

PATCH /api/todos/:id {
  <- id   uuid required
  <- done bool required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "not found"
  |> update todo { done: done }
  -> 200 { id: todo.id, done: done }
}

DELETE /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "not found"
  |> delete todo
  -> 204 "deleted"
}
`

func TestPython_Phase3CompilesCRUD(t *testing.T) {
	outDir := buildPython(t, phase3CRUDSrc)
	body := readPy(t, outDir, "src/routes/todos.py")

	for _, want := range []string{
		// signature carries the db dependency
		"db: Session = Depends(get_db)",
		// save → orm + commit + refresh
		"todo = schema.Todo(title=title)",
		"db.add(todo)",
		"db.commit()",
		"db.refresh(todo)",
		// fetch
		"todo = db.get(schema.Todo, id)",
		// guard
		"if not (todo):",
		"raise HTTPException(status_code=404, detail=\"not found\")",
		// update mutates the bound instance, doesn't re-query
		"todo.done = done",
		// delete
		"db.delete(todo)",
		// query (non-paginated)
		"todos = db.execute(select(schema.Todo)).scalars().all()",
		// 204 drops the body
		"return Response(status_code=204)",
		// 2xx with object output uses jsonable_encoder
		"return JSONResponse(jsonable_encoder(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("routes/todos.py missing %q\n--- got ---\n%s", want, body)
		}
	}
}

func TestPython_Phase3PaginatedQuery(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model todo { id uuid primary  title string required }
GET /api/todos {
  <- page int default(1)
  <- per_page int default(20)
  |> todos = query todo paginate(page, per_page)
  -> 200 { items: todos.items, total: todos.total }
}
`
	outDir := buildPython(t, src)
	body := readPy(t, outDir, "src/routes/todos.py")
	for _, want := range []string{
		"from sqlalchemy import select, func",
		"from types import SimpleNamespace",
		"_todos_total = db.scalar(select(func.count()).select_from(schema.Todo))",
		"_todos_items = db.execute(select(schema.Todo).offset((page - 1) * per_page).limit(per_page)).scalars().all()",
		"todos = SimpleNamespace(items=_todos_items, total=_todos_total)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("paginated query missing %q in:\n%s", want, body)
		}
	}
}

// TestPython_Phase3cOrderClause replaces the old Phase 3 "rejects order"
// test now that `order(col, asc|desc)` compiles. The Blueprint parser today
// accepts one `order(...)` per query (multi-column ordering is a future
// language change); here we verify both the `asc` direction and that order
// chains cleanly with `where` + `paginate` markers.
func TestPython_Phase3cOrderClause(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model todo { id uuid primary  done bool default(false)  name string required }
GET /api/todos {
  <- page int default(1)
  |> todos = query todo where(done == true) order(name, asc) paginate(page, 10)
  -> 200 { items: todos.items, total: todos.total }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/todos.py")
	for _, want := range []string{
		"select(schema.Todo).where(schema.Todo.done == True).order_by(schema.Todo.name.asc())",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("order chain missing %q in:\n%s", want, body)
		}
	}
}

// TestPython_Phase3cOrderDesc covers the `desc` direction independently.
func TestPython_Phase3cOrderDesc(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model log_entry { id uuid primary  created timestamp default(now) }
GET /api/logs {
  |> entries = query log_entry order(created, desc)
  -> 200 { items: entries }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/logs.py")
	if !strings.Contains(body, "select(schema.LogEntry).order_by(schema.LogEntry.created.desc())") {
		t.Errorf("desc order missing, got:\n%s", body)
	}
}

func TestPython_Phase3StaticEndpointStillAsync(t *testing.T) {
	// A static (no-data-op) handler should be `async def` — only DB-touching
	// handlers go sync. Mixing is intentional: static responses don't pay the
	// threadpool hop, DB calls don't block the event loop.
	outDir := buildPython(t, helloWorldSrc)
	body := readPy(t, outDir, "src/routes/health.py")
	if !strings.Contains(body, "async def get_health(") {
		t.Errorf("static handler should stay async, got:\n%s", body)
	}
}

// ---------- Phase 3b ----------

func TestPython_Phase3bSinglePredicateWhere(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model product { id uuid primary  active bool default(true) }
GET /api/products {
  <- active bool default(true)
  |> products = query product where(active == active)
  -> 200 { items: products }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/products.py")
	if !strings.Contains(body, "select(schema.Product).where(schema.Product.active == active)") {
		t.Errorf("single-predicate where missing or wrong, got:\n%s", body)
	}
	// Single-predicate where MUST NOT import and_ — the codegen should be
	// idiomatic, not blanket-import every helper.
	if strings.Contains(body, "and_") {
		t.Errorf("single-predicate where should not import and_, got:\n%s", body)
	}
}

func TestPython_Phase3bMultiPredicateWhere(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model cart_item { id uuid primary  session_id string required  product_id uuid required }
GET /api/cart {
  <- session_id string required
  <- product_id uuid required
  |> rows = query cart_item where(session_id == session_id, product_id == product_id)
  -> 200 { items: rows }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/cart.py")
	if !strings.Contains(body, "from sqlalchemy import select, and_") {
		t.Errorf("multi-predicate where should import and_, got:\n%s", body)
	}
	if !strings.Contains(body, ".where(and_(schema.CartItem.session_id == session_id, schema.CartItem.product_id == product_id))") {
		t.Errorf("multi-predicate where missing or wrong, got:\n%s", body)
	}
}

func TestPython_Phase3bFirstModifier(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model product { id uuid primary  sku string unique required }
POST /api/products {
  <- sku string required
  |> existing = query product where(sku == sku) first
  |> guard not existing -> 409 "taken"
  |> product = save product { sku: sku }
  -> 201 { id: product.id }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/products.py")
	if !strings.Contains(body, ".scalars().first()") {
		t.Errorf("first modifier missing, got:\n%s", body)
	}
	// And the non-first query path elsewhere in the same file (if there were
	// one) would still use .all() — sanity that we didn't break the default.
}

func TestPython_Phase3bWhenBlock(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model cart_item { id uuid primary  session_id string required  quantity int required default(1) }
POST /api/cart/items {
  <- session_id string required
  <- quantity int default(1)
  |> existing = query cart_item where(session_id == session_id) first
  |> when existing {
    |> update existing { quantity: existing.quantity + quantity }
  }
  |> when not existing {
    |> save cart_item { session_id: session_id, quantity: quantity }
  }
  -> 200 "ok"
}
`
	body := readPy(t, buildPython(t, src), "src/routes/cart.py")
	for _, want := range []string{
		"if existing:",
		"        existing.quantity = (existing.quantity + quantity)",
		"        db.commit()",
		"if not existing:",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("when block missing %q, got:\n%s", want, body)
		}
	}
}

func TestPython_Phase3bRejectsUnsupportedWherePredicate(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model product { id uuid primary  stock int required }
GET /api/products {
  |> rows = query product where(stock >= 10)
  -> 200 { items: rows }
}
`
	file, errs := parser.ParseFile("t.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	err := python.New().Generate(file, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "non-`==` predicates") {
		t.Errorf("expected non-`==` rejection, got: %v", err)
	}
}

// Phase 3c: the when-inline form compiles, but bare BlockExpr step
// expressions (`|> filters = {}`) are still rejected because they're not
// data operations. This keeps the runtime small — Phase 3d will add
// step-expression dict literals.
func TestPython_Phase3cRejectsBareBlockStep(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  <- q string optional
  |> filters = {}
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("t.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	err := python.New().Generate(file, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "step expressions") {
		t.Errorf("expected bare-block step rejection, got: %v", err)
	}
}

func TestPython_NoModelsKeepsPhase1Output(t *testing.T) {
	// A model-less spec must not include the data layer or alembic.
	outDir := buildPython(t, helloWorldSrc)
	for _, missing := range []string{
		"src/models/schema.py",
		"src/lib/db.py",
		"alembic.ini",
	} {
		if _, err := os.Stat(filepath.Join(outDir, missing)); err == nil {
			t.Errorf("model-less spec should not emit %s", missing)
		}
	}
}

func TestPython_GeneratedFilesAreIdempotent(t *testing.T) {
	// Second build into the same dir must not change manifest hashes — the
	// CI gate that already guards the JS target should apply here too.
	file, errs := parser.ParseFile("t.bp", []byte(helloWorldSrc))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	gen := python.New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("first build: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(outDir, ".blueprint/manifest.json"))
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("second build: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(outDir, ".blueprint/manifest.json"))
	if string(first) != string(second) {
		t.Errorf("python generator is not idempotent — manifest differs between builds")
	}
}

// ---------- Phase 3c ----------

// TestPython_Phase3cFKAccess covers the resolver-driven FK sub-query pattern:
// `item.product.stock` after `item = fetch cart_item(id)` emits one
// `_product = db.get(...)` line before the guard, and rewrites the access in
// the guard expression to use the alias. The same alias is reused by any
// follow-up references rather than re-querying.
func TestPython_Phase3cFKAccess(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model product { id uuid primary  stock int required }
model cart_item {
  id uuid primary
  product_id uuid ref(product) required
  quantity int required
}
PATCH /api/cart/items/:id {
  <- id       uuid required
  <- quantity int  required
  |> item = fetch cart_item(id)
  |> guard item                           -> 404 "Cart item not found"
  |> guard item.product.stock >= quantity -> 400 "Not enough stock"
  |> update item { quantity: quantity }
  -> 200 { id: item.id, stock: item.product.stock, quantity: quantity }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/cart.py")
	for _, want := range []string{
		// One pre-emitted FK sub-query before the guard.
		"_product = db.get(schema.Product, item.product_id)",
		// Guard rewrites item.product.stock to _product.stock.
		"if not ((_product.stock >= quantity)):",
		// Output also reuses the alias rather than re-querying.
		`"stock": _product.stock`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("FK access missing %q in:\n%s", want, body)
		}
	}
	// Dedup: only one db.get on _product across the whole handler.
	if n := strings.Count(body, "_product = db.get"); n != 1 {
		t.Errorf("FK alias should be emitted once, got %d:\n%s", n, body)
	}
}

// TestPython_Phase3cTryRecover verifies the try/recover shape: a Python
// `try:` followed by `except Exception as error:`, with the recover body
// indented one level deeper than its enclosing scope.
func TestPython_Phase3cTryRecover(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model order { id uuid primary  status string default("pending") }
POST /api/orders {
  <- session_id string required
  |> try {
    |> order = save order { status: "pending" }
    |> update order { status: "paid" }
  } recover {
    |> log "failed: {error}"
    -> 500 { error: "boom" }
  }
  -> 201 { id: order.id }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/orders.py")
	for _, want := range []string{
		"    try:",
		"        order = schema.Order(status=\"pending\")",
		"        db.add(order)",
		"    except Exception as error:",
		`        print(f"failed: {error}")`,
		`        return JSONResponse(jsonable_encoder({"error": "boom"}), status_code=500)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("try/recover missing %q in:\n%s", want, body)
		}
	}
}

// TestPython_Phase3cMapBoundSave covers `|> result = map coll: save M {...}`:
// the result is initialised as `[]`, the loop appends each saved row, a
// single `db.commit()` follows, and every row is refreshed in a second loop.
func TestPython_Phase3cMapBoundSave(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model cart_item { id uuid primary  product_id uuid required  quantity int required }
model order { id uuid primary }
model order_item {
  id uuid primary
  order_id uuid ref(order) required
  product_id uuid required
  quantity int required
}
POST /api/orders {
  <- session_id string required
  |> items = query cart_item where(session_id == session_id)
  |> order = save order { }
  |> rows = map items: save order_item {
    order_id: order.id,
    product_id: item.product_id,
    quantity: item.quantity,
  }
  -> 201 { count: rows }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/orders.py")
	for _, want := range []string{
		"rows = []",
		"for item in items:",
		"        _row = schema.OrderItem(order_id=order.id, product_id=item.product_id, quantity=item.quantity)",
		"        db.add(_row)",
		"        rows.append(_row)",
		"    db.commit()",
		"    for _row in rows:",
		"        db.refresh(_row)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("map+save missing %q in:\n%s", want, body)
		}
	}
}

// TestPython_Phase3cMapUnboundUpdate covers `|> map coll: update M { ... }`:
// the loop mutates a per-element FK alias (`_product`) and commits once after
// the loop. No collection is created (no bound binding).
func TestPython_Phase3cMapUnboundUpdate(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model product { id uuid primary  stock int required }
model cart_item {
  id uuid primary
  product_id uuid ref(product) required
  quantity int required
}
POST /api/orders {
  <- session_id string required
  |> items = query cart_item where(session_id == session_id)
  |> map items: update product { stock: item.product.stock - item.quantity }
  -> 200 "ok"
}
`
	body := readPy(t, buildPython(t, src), "src/routes/orders.py")
	for _, want := range []string{
		"for item in items:",
		"        _product = db.get(schema.Product, item.product_id)",
		"        _product.stock = (_product.stock - item.quantity)",
		"    db.commit()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("map+update missing %q in:\n%s", want, body)
		}
	}
	// Unbound map must NOT introduce a result variable.
	if strings.Contains(body, "_rows = []") || strings.Contains(body, "rows = []") {
		t.Errorf("unbound map should not create a result list, got:\n%s", body)
	}
}

// TestPython_Phase3cWhenInline covers `|> when cond: var.field = expr` ->
// a single-line Python `if cond: var.field = expr`. Block form continues to
// indent the body on its own lines.
func TestPython_Phase3cWhenInline(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model product { id uuid primary  name string required }
GET /api/products/:id {
  <- id uuid required
  |> product = fetch product(id)
  |> when product: product.name = "patched"
  -> 200 { id: product.id, name: product.name }
}
`
	body := readPy(t, buildPython(t, src), "src/routes/products.py")
	if !strings.Contains(body, `if product: product.name = "patched"`) {
		t.Errorf("when-inline missing or wrong, got:\n%s", body)
	}
}

// TestPython_Phase3cLogStep covers `|> log "msg"` -> `print(f"msg")`. The
// translator drops `level(error)` and other extras in 3c.
func TestPython_Phase3cLogStep(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x {
  <- name string required
  |> log "hello {name}"
  -> 200 "ok"
}
`
	body := readPy(t, buildPython(t, src), "src/routes/x.py")
	if !strings.Contains(body, `print(f"hello {name}")`) {
		t.Errorf("log step missing or wrong, got:\n%s", body)
	}
}

// TestPython_Phase3dDispatchesUserStepFn confirms that step calls to declared
// user fn names now compile (Phase 3d) rather than rejecting. The wrapper at
// src/functions/<name>.py + user-owned scaffold at src/impl/functions/... is
// what makes this work.
func TestPython_Phase3dDispatchesUserStepFn(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model order { id uuid primary  total_cents int required default(0) }
fn charge { <- amount int  -> json  impl node { module: "./x", func: "y" } }
POST /api/orders {
  <- amount int required
  |> order = save order { total_cents: amount }
  |> payment = charge(amount)
  -> 201 { id: order.id }
}
`
	outDir := buildPython(t, src)
	wrapper := readPy(t, outDir, "src/functions/charge.py")
	if !strings.Contains(wrapper, "def charge(amount):") {
		t.Errorf("expected charge wrapper to define charge(amount), got:\n%s", wrapper)
	}
	scaffold := readPy(t, outDir, "src/impl/functions/x.py")
	if !strings.Contains(scaffold, "raise NotImplementedError") {
		t.Errorf("expected scaffold to raise NotImplementedError, got:\n%s", scaffold)
	}
	route := readPy(t, outDir, "src/routes/orders.py")
	if !strings.Contains(route, "payment = charge(amount)") {
		t.Errorf("expected step call dispatch to charge(amount), got:\n%s", route)
	}
	if !strings.Contains(route, "from src.functions.charge import charge") {
		t.Errorf("expected charge import in route file, got:\n%s", route)
	}
}

// ---------- Phase 4: --gen-tests ----------

const phase4Src = `blueprint "todo-api" {
  version "1.0.0"
  port 3000
  runtime node
  database postgres
}

secret DATABASE_URL required

model todo {
  id    uuid    primary
  title string  required
  done  bool    default(false)
}

POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id, title: todo.title }
}

GET /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "not found"
  -> 200 { id: todo.id, title: todo.title }
}
`

// buildPythonGenTests is the --gen-tests variant of buildPython.
func buildPythonGenTests(t *testing.T, src string) string {
	t.Helper()
	file, errs := parser.ParseFile("t.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := python.New().WithGenTests(true).Generate(file, outDir); err != nil {
		t.Fatalf("python generate: %v", err)
	}
	return outDir
}

// TestPython_Phase4HarnessFiles asserts conftest.py and per-resource test
// files are emitted when --gen-tests is on, and the harness wires the
// testcontainers + TestClient + dependency_overrides stack the contract
// suite expects.
func TestPython_Phase4HarnessFiles(t *testing.T) {
	outDir := buildPythonGenTests(t, phase4Src)
	for _, rel := range []string{
		"tests/__init__.py",
		"tests/_harness/__init__.py",
		"tests/conftest.py",
		"tests/test_todos.py",
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

// TestPython_Phase4ConftestShape verifies the four fixtures the README +
// docstring promise: pg_container (session), engine (session, create_all),
// db (function, TRUNCATE), client (function, dependency_overrides).
func TestPython_Phase4ConftestShape(t *testing.T) {
	body := readPy(t, buildPythonGenTests(t, phase4Src), "tests/conftest.py")
	for _, want := range []string{
		"from fastapi.testclient import TestClient",
		"from testcontainers.postgres import PostgresContainer",
		"def pg_container() -> Iterator[PostgresContainer]:",
		`with PostgresContainer("postgres:16-alpine") as pg:`,
		"def engine(pg_container: PostgresContainer):",
		"from src.models.schema import Base",
		"Base.metadata.create_all(eng)",
		`_TABLES = ["todos"]`,
		"def db(engine) -> Iterator[Session]:",
		"TRUNCATE",
		"def client(db: Session) -> Iterator[TestClient]:",
		"app.dependency_overrides[get_db]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("conftest.py missing %q, got:\n%s", want, body)
		}
	}
}

// TestPython_Phase4ContractTestShape verifies the emitted contract tests for
// a CRUD resource: signature, lenient status set, opportunistic shape check,
// JSON body for non-GET, seeded path-param value.
func TestPython_Phase4ContractTestShape(t *testing.T) {
	body := readPy(t, buildPythonGenTests(t, phase4Src), "tests/test_todos.py")
	for _, want := range []string{
		"from src.models import schema",
		"from fastapi.testclient import TestClient",
		"from sqlalchemy.orm import Session",
		// POST: signature, body, lenient status, shape check.
		"def test_post_api_todos(client: TestClient) -> None:",
		`res = client.post("/api/todos", json={"title": "test"})`,
		"assert res.status_code in {201, 400, 422, 500}",
		`assert "id" in body`,
		`assert "title" in body`,
		// GET /:id seeds a row and uses its id in the URL; 404 is in the set.
		"def test_get_api_todos_id(client: TestClient, db: Session) -> None:",
		`_seed0 = schema.Todo(title="test")`,
		"db.add(_seed0)",
		"res = client.get(f\"/api/todos/{_seed0.id}\")",
		"assert res.status_code in {200, 400, 404, 422, 500}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("contract test missing %q, got:\n%s", want, body)
		}
	}
}

// TestPython_Phase4PyprojectAddsTestcontainers verifies the dev-dep is added
// only when --gen-tests is on. Mirrors the JS pglite gate.
func TestPython_Phase4PyprojectAddsTestcontainers(t *testing.T) {
	withFlag := readPy(t, buildPythonGenTests(t, phase4Src), "pyproject.toml")
	if !strings.Contains(withFlag, "testcontainers[postgresql]") {
		t.Errorf("pyproject.toml should include testcontainers when --gen-tests is on, got:\n%s", withFlag)
	}

	// Without the flag, the dep must not appear.
	withoutFlag := readPy(t, buildPython(t, phase4Src), "pyproject.toml")
	if strings.Contains(withoutFlag, "testcontainers") {
		t.Errorf("pyproject.toml should NOT include testcontainers without --gen-tests, got:\n%s", withoutFlag)
	}
}

// TestPython_Phase4DisabledByDefault confirms the harness is opt-in.
func TestPython_Phase4DisabledByDefault(t *testing.T) {
	outDir := buildPython(t, phase4Src)
	if _, err := os.Stat(filepath.Join(outDir, "tests/conftest.py")); !os.IsNotExist(err) {
		t.Errorf("tests/conftest.py should not be emitted without --gen-tests")
	}
	if _, err := os.Stat(filepath.Join(outDir, "tests/test_todos.py")); !os.IsNotExist(err) {
		t.Errorf("tests/test_todos.py should not be emitted without --gen-tests")
	}
}

// TestPython_Phase4FKSeeding mirrors the JS-side FK-seeding contract: the
// parent (product) is inserted before the cart_item; the child references
// the parent's id; unique string columns get a unique seed value.
func TestPython_Phase4FKSeeding(t *testing.T) {
	src := `blueprint "shop" {
  version "1.0.0"
  port 3000
  runtime node
  database postgres
}

secret DATABASE_URL required

model product {
  id  uuid   primary
  sku string unique required
}

model cart_item {
  id         uuid primary
  product_id uuid ref(product) required
}

GET /api/cart_items/:id {
  <- id uuid required
  |> item = fetch cart_item(id)
  |> guard item -> 404 "not found"
  -> 200 { id: item.id }
}
`
	body := readPy(t, buildPythonGenTests(t, src), "tests/test_cart_items.py")
	idxProduct := strings.Index(body, "schema.Product(")
	idxCart := strings.Index(body, "schema.CartItem(")
	if idxProduct < 0 || idxCart < 0 {
		t.Fatalf("expected both product and cart_item seeds, got:\n%s", body)
	}
	if idxProduct > idxCart {
		t.Errorf("expected product seeded before cart_item, got:\n%s", body)
	}
	if !strings.Contains(body, "product_id=_seed0.id") {
		t.Errorf("expected cart_item to reference seeded product id, got:\n%s", body)
	}
	if !strings.Contains(body, "sku=str(__import__('uuid').uuid4())") {
		t.Errorf("expected unique sku seed, got:\n%s", body)
	}
}

// TestPython_Phase4Idempotent guards against non-determinism in the autotest
// emitter (map iteration, status set ordering, etc.) — same input must hash
// to the same manifest across builds.
func TestPython_Phase4Idempotent(t *testing.T) {
	file, errs := parser.ParseFile("t.bp", []byte(phase4Src))
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	outDir := t.TempDir()
	gen := python.New().WithGenTests(true)
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("first build: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(outDir, ".blueprint/manifest.json"))
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("second build: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(outDir, ".blueprint/manifest.json"))
	if string(first) != string(second) {
		t.Errorf("--gen-tests build is not idempotent — manifest differs")
	}
}
