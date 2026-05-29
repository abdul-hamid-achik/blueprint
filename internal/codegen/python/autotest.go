package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

// Auto-generated tests + testcontainers-backed harness.
//
// When --gen-tests is set, genAutoTests emits a self-contained pytest suite
// that runs each endpoint against a real Postgres container managed by
// `testcontainers[postgresql]`. The JS target uses PGlite (in-process), but
// SQLite/PGlite's dialect drifts too far from Postgres FK/JSON/enum semantics
// for contract-test signal, so Phase 4 chooses a real container instead.
//
// Generated files:
//
//	tests/__init__.py
//	tests/_harness/__init__.py
//	tests/conftest.py            pg_container + engine + db + client fixtures
//	tests/test_<resource>.py     one contract test per endpoint
//
// Assertions are deliberately lenient: a request must return one of the
// statuses the endpoint (and its guards) can declare, and a 2xx BlockExpr
// output's top-level keys are asserted on the response. Tighter assertions
// belong in authored `test {}` blocks; the contract suite exists to catch the
// "route stopped responding entirely" class of regression.

// zeroUUIDPy is the literal UUID we use whenever no seeded row provides one.
// Matches the JS autotest behaviour so the two suites stay symmetric.
const zeroUUIDPy = "00000000-0000-0000-0000-000000000000"

// genAutoTests builds the testcontainers harness + per-endpoint contract tests.
func (g *Generator) genAutoTests(endpoints []*ast.Endpoint, models []*ast.Model, secrets []*ast.Secret, hasDB bool) []codegen.OutputFile {
	var files []codegen.OutputFile
	files = append(files,
		emptyInit("tests/__init__.py", g.sourceFile),
		emptyInit("tests/_harness/__init__.py", g.sourceFile),
	)
	files = append(files, g.genConftestPy(models, secrets, hasDB))

	groups := map[string][]*ast.Endpoint{}
	var order []string
	for _, ep := range endpoints {
		r := common.ExtractResource(ep.Path)
		if _, ok := groups[r]; !ok {
			order = append(order, r)
		}
		groups[r] = append(groups[r], ep)
	}
	sort.Strings(order)
	for _, r := range order {
		files = append(files, g.genContractTestFile(r, groups[r], models))
	}
	return files
}

// genConftestPy emits tests/conftest.py with the four shared fixtures:
//
//   - pg_container (session scope): a PostgresContainer; yields the
//     connection URL that engine/SessionLocal point at for the rest of the run.
//   - engine (session scope): SQLAlchemy engine + Base.metadata.create_all
//     against the container. Phase 4 deliberately bypasses Alembic so the
//     suite is hermetic — Phase 5 can revisit.
//   - db (function scope): a Session that TRUNCATEs every table before yield
//     so each test starts from a clean slate.
//   - client (function scope): FastAPI TestClient with get_db overridden
//     through app.dependency_overrides so every route uses the test session.
//
// secrets / DATABASE_URL handling mirrors the JS setup: env vars get dummy
// values via monkeypatch (a session-scoped fixture) so src/lib/env.py's
// import-time validation passes without a real .env file. Required secrets
// the user didn't seed default to "test".
func (g *Generator) genConftestPy(models []*ast.Model, secrets []*ast.Secret, hasDB bool) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	b.WriteString("\"\"\"Pytest fixtures for the auto-generated contract suite.\n\n")
	b.WriteString("The suite runs against a real Postgres provisioned by\n")
	b.WriteString("testcontainers[postgresql] (one container per session). Each test gets a\n")
	b.WriteString("clean database via TRUNCATE; the FastAPI TestClient is wired so every route\n")
	b.WriteString("uses the test session through app.dependency_overrides.\n\n")
	b.WriteString("Run with: uv run pytest. Docker must be available locally; CI workflows\n")
	b.WriteString("without Docker should skip the suite (see docs/production-readiness.md).\n")
	b.WriteString("\"\"\"\n\n")

	b.WriteString("import os\n")
	b.WriteString("from typing import Iterator\n\n")
	b.WriteString("import pytest\n")
	b.WriteString("from fastapi.testclient import TestClient\n")
	b.WriteString("from sqlalchemy import create_engine, text\n")
	b.WriteString("from sqlalchemy.orm import Session, sessionmaker\n")
	b.WriteString("from testcontainers.postgres import PostgresContainer\n\n")

	// Dummy env vars must be set BEFORE importing the app so Pydantic Settings
	// validation passes. Use a session-autouse fixture instead of monkeypatch
	// at module scope so pytest's collection still works on machines without
	// Docker (we never touch settings until tests actually run).
	b.WriteString(`@pytest.fixture(scope="session", autouse=True)
def _seed_env() -> None:
    """Populate dummy env vars so src/lib/env.py import-time validation passes.

    The real DATABASE_URL is overridden by the engine fixture below; the value
    set here is just a placeholder that satisfies Pydantic Settings on import.
    """
    os.environ.setdefault("DATABASE_URL", "postgresql+psycopg://test:test@localhost:5432/test")
`)
	// Extra secrets the user declared — required ones get default "test".
	seen := map[string]bool{"DATABASE_URL": true}
	for _, s := range secrets {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		fmt.Fprintf(&b, "    os.environ.setdefault(%q, \"test\")\n", s.Name)
	}
	b.WriteString("\n\n")

	// pg_container — session-scoped real Postgres.
	b.WriteString(`@pytest.fixture(scope="session")
def pg_container() -> Iterator[PostgresContainer]:
    """Spin up a single Postgres container for the whole test session."""
    with PostgresContainer("postgres:16-alpine") as pg:
        yield pg


`)

	// engine — session-scoped, points at the container; creates tables once.
	if hasDB && len(models) > 0 {
		b.WriteString(`@pytest.fixture(scope="session")
def engine(pg_container: PostgresContainer):
    """Engine pointing at the test container with all tables created.

    Phase 4 uses Base.metadata.create_all instead of Alembic so the suite is
    self-contained. Alembic still ships for production migrations; Phase 5
    can revisit if testing migrations themselves becomes a priority.
    """
    from src.models.schema import Base
    url = pg_container.get_connection_url().replace("postgresql://", "postgresql+psycopg://", 1)
    os.environ["DATABASE_URL"] = url
    eng = create_engine(url, pool_pre_ping=True, future=True)
    Base.metadata.create_all(eng)
    yield eng
    eng.dispose()


`)
	} else {
		// No DB: still expose an engine fixture for symmetry, but make it a
		// no-op that yields None so client-only tests can depend on the rest
		// of the chain without crashing.
		b.WriteString(`@pytest.fixture(scope="session")
def engine(pg_container: PostgresContainer):
    """No-op engine for spec without a database — keeps fixtures compatible."""
    url = pg_container.get_connection_url().replace("postgresql://", "postgresql+psycopg://", 1)
    os.environ["DATABASE_URL"] = url
    eng = create_engine(url, pool_pre_ping=True, future=True)
    yield eng
    eng.dispose()


`)
	}

	// db — function scope, truncates between tests.
	if hasDB && len(models) > 0 {
		// Build the list of tables to truncate.
		tables := make([]string, 0, len(models))
		for _, m := range models {
			tables = append(tables, common.Pluralize(m.Name))
		}
		sort.Strings(tables)
		quoted := make([]string, len(tables))
		for i, t := range tables {
			quoted[i] = fmt.Sprintf("%q", t)
		}

		fmt.Fprintf(&b, "_TABLES = [%s]\n\n\n", strings.Join(quoted, ", "))

		b.WriteString(`@pytest.fixture()
def db(engine) -> Iterator[Session]:
    """Function-scoped Session. TRUNCATEs all tables before yield for isolation."""
    SessionLocal = sessionmaker(bind=engine, autoflush=False, autocommit=False, future=True)
    session = SessionLocal()
    if _TABLES:
        joined = ", ".join(f'"{t}"' for t in _TABLES)
        session.execute(text(f"TRUNCATE {joined} RESTART IDENTITY CASCADE"))
        session.commit()
    try:
        yield session
    finally:
        session.close()


`)
	} else {
		b.WriteString(`@pytest.fixture()
def db(engine) -> Iterator[Session]:
    """No-op DB fixture for spec without a database."""
    SessionLocal = sessionmaker(bind=engine, autoflush=False, autocommit=False, future=True)
    session = SessionLocal()
    try:
        yield session
    finally:
        session.close()


`)
	}

	// client — function scope, overrides get_db.
	if hasDB {
		b.WriteString(`@pytest.fixture()
def client(db: Session) -> Iterator[TestClient]:
    """TestClient with get_db overridden so every route uses the test session."""
    from src.app import app
    from src.lib.db import get_db

    def _override() -> Iterator[Session]:
        yield db

    app.dependency_overrides[get_db] = _override
    try:
        with TestClient(app) as c:
            yield c
    finally:
        app.dependency_overrides.pop(get_db, None)
`)
	} else {
		b.WriteString(`@pytest.fixture()
def client() -> Iterator[TestClient]:
    """TestClient for a spec without a database — no overrides needed."""
    from src.app import app
    with TestClient(app) as c:
        yield c
`)
	}

	return codegen.OutputFile{Path: "tests/conftest.py", Content: []byte(b.String())}
}

// genContractTestFile emits tests/test_<resource>.py with one contract test
// per endpoint in the resource group.
func (g *Generator) genContractTestFile(resource string, eps []*ast.Endpoint, models []*ast.Model) codegen.OutputFile {
	var body strings.Builder
	needSchemaImport := false
	for _, ep := range eps {
		if g.emitContractTest(&body, ep, models) {
			needSchemaImport = true
		}
	}

	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(fmt.Sprintf("\"\"\"Generated contract tests for the %s resource.\"\"\"\n\n", resource))
	if needSchemaImport {
		b.WriteString("from src.models import schema\n")
	}
	b.WriteString("from fastapi.testclient import TestClient\n")
	if needSchemaImport {
		b.WriteString("from sqlalchemy.orm import Session\n")
	}
	b.WriteString("\n")
	b.WriteString(body.String())

	path := fmt.Sprintf("tests/test_%s.py", pyModuleName(resource))
	return codegen.OutputFile{Path: path, Content: []byte(b.String())}
}

// emitContractTest writes one `def test_<...>` per endpoint. Returns true when
// the test seeded any rows (so the file knows it needs the schema/Session imports).
func (g *Generator) emitContractTest(b *strings.Builder, ep *ast.Endpoint, models []*ast.Model) bool {
	method := strings.ToUpper(ep.Method)
	testName := contractTestName(ep)
	indent := "    "
	seeded := false

	pathParams := common.ExtractPathParams(ep.Path)
	seedParamVar := map[string]string{} // path-param name -> seeded row var
	var seedBody strings.Builder
	seedCounter := 0
	if len(pathParams) > 0 {
		if modelName := firstDataOpModelInStmts(ep.Stmts); modelName != "" {
			if v := emitSeedModelPy(&seedBody, modelName, models, indent, &seedCounter, map[string]bool{}); v != "" {
				for _, p := range pathParams {
					seedParamVar[p] = v
				}
				seeded = true
			}
		}
	}

	// Group input statements by destination.
	var pathInputs, queryInputs, bodyInputs []*ast.InputStmt
	useQuery := method == "GET" || method == "DELETE"
	for _, s := range ep.Stmts {
		inp, ok := s.(*ast.InputStmt)
		if !ok {
			continue
		}
		switch {
		case common.IsPathParam(inp.Name, ep.Path):
			pathInputs = append(pathInputs, inp)
		case useQuery:
			queryInputs = append(queryInputs, inp)
		default:
			bodyInputs = append(bodyInputs, inp)
		}
	}
	_ = pathInputs

	url := buildRequestPathPy(ep.Path, seedParamVar)
	if len(queryInputs) > 0 {
		qs := make([]string, 0, len(queryInputs))
		for _, inp := range queryInputs {
			qs = append(qs, inp.Name+"="+sampleQueryValuePy(inp.Type, inp.Constraints))
		}
		// url is a single-quoted Python string OR an f-string. Either way,
		// append the querystring inside the closing quote.
		url = appendQueryToURL(url, strings.Join(qs, "&"))
	}

	// Signature: every test takes client; if we seeded, db too.
	sig := "client: TestClient"
	if seeded {
		sig = "client: TestClient, db: Session"
	}
	fmt.Fprintf(b, "def %s(%s) -> None:\n", testName, sig)
	if seeded {
		b.WriteString(seedBody.String())
	}

	// Request call.
	callArgs := []string{url}
	if len(bodyInputs) > 0 {
		kvs := make([]string, 0, len(bodyInputs))
		for _, inp := range bodyInputs {
			kvs = append(kvs, fmt.Sprintf("%q: %s", inp.Name, sampleJSONValuePy(inp.Type, inp.Constraints)))
		}
		callArgs = append(callArgs, "json={"+strings.Join(kvs, ", ")+"}")
	}
	fmt.Fprintf(b, "%sres = client.%s(%s)\n", indent, strings.ToLower(method), strings.Join(callArgs, ", "))

	// Status containment.
	statuses := collectAllowedStatusesPy(ep)
	fmt.Fprintf(b, "%sassert res.status_code in {%s}\n", indent, strings.Join(statuses, ", "))

	// Opportunistic shape checks for 2xx BlockExpr outputs.
	for _, sc := range collectShapeChecksPy(ep.Stmts) {
		fmt.Fprintf(b, "%sif res.status_code == %s:\n", indent, sc.status)
		fmt.Fprintf(b, "%s    body = res.json()\n", indent)
		for _, k := range sc.keys {
			fmt.Fprintf(b, "%s    assert %q in body\n", indent, k)
		}
	}

	b.WriteString("\n\n")
	return seeded
}

// contractTestName builds a pytest-style function name from the endpoint.
//
//	GET  /api/todos      -> test_get_api_todos
//	POST /api/cart/items -> test_post_api_cart_items
//	GET  /api/todos/:id  -> test_get_api_todos_id
func contractTestName(ep *ast.Endpoint) string {
	var segs []string
	segs = append(segs, strings.ToLower(ep.Method))
	for _, s := range strings.Split(ep.Path, "/") {
		if s == "" {
			continue
		}
		s = strings.TrimPrefix(s, ":")
		segs = append(segs, common.SnakeCase(s))
	}
	return "test_" + strings.Join(segs, "_")
}

// buildRequestPathPy renders a Python string literal for the endpoint path,
// substituting seeded ids for bound path params and a zero UUID for the rest.
// When at least one seeded var is interpolated, the result is an f-string.
func buildRequestPathPy(path string, seedParamVar map[string]string) string {
	hasInterp := false
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if !strings.HasPrefix(seg, ":") {
			continue
		}
		name := seg[1:]
		if v, ok := seedParamVar[name]; ok {
			segs[i] = "{" + v + ".id}"
			hasInterp = true
		} else {
			segs[i] = zeroUUIDPy
		}
	}
	joined := strings.Join(segs, "/")
	if hasInterp {
		return "f\"" + joined + "\""
	}
	return "\"" + joined + "\""
}

// appendQueryToURL appends a querystring inside the URL's closing quote.
// Handles both plain "..." and f"..." strings.
func appendQueryToURL(url, qs string) string {
	if strings.HasPrefix(url, "f\"") && strings.HasSuffix(url, "\"") {
		return strings.TrimSuffix(url, "\"") + "?" + qs + "\""
	}
	if strings.HasPrefix(url, "\"") && strings.HasSuffix(url, "\"") {
		return strings.TrimSuffix(url, "\"") + "?" + qs + "\""
	}
	return url
}

// emitSeedModelPy inserts a row for modelName (seeding ref parents first) and
// returns the Python variable holding the inserted row, or "" if the model is
// unknown. Mirrors the JS-side emitSeedModel.
func emitSeedModelPy(b *strings.Builder, modelName string, models []*ast.Model, indent string, counter *int, visiting map[string]bool) string {
	m := findModel(models, modelName)
	if m == nil || visiting[modelName] {
		return ""
	}
	visiting[modelName] = true
	defer delete(visiting, modelName)

	// Seed parents first; collect the variable name to wire as the FK column.
	fkValues := map[string]string{} // fk column name (e.g. "product_id") -> "_seedN.id"
	for _, f := range m.Fields {
		ref := refTargetPy(f)
		if ref == "" {
			continue
		}
		if pv := emitSeedModelPy(b, ref, models, indent, counter, visiting); pv != "" {
			fkValues[f.Name] = pv + ".id"
		}
	}

	var kvs []string
	for _, f := range m.Fields {
		if v, ok := fkValues[f.Name]; ok {
			kvs = append(kvs, fmt.Sprintf("%s=%s", f.Name, v))
			continue
		}
		if !fieldNeedsSeedValuePy(f) {
			continue
		}
		kvs = append(kvs, fmt.Sprintf("%s=%s", f.Name, modelFieldSamplePy(f)))
	}

	v := fmt.Sprintf("_seed%d", *counter)
	*counter++
	className := common.PascalCase(m.Name)
	fmt.Fprintf(b, "%s%s = schema.%s(%s)\n", indent, v, className, strings.Join(kvs, ", "))
	fmt.Fprintf(b, "%sdb.add(%s)\n", indent, v)
	fmt.Fprintf(b, "%sdb.commit()\n", indent)
	fmt.Fprintf(b, "%sdb.refresh(%s)\n", indent, v)
	return v
}

// fieldNeedsSeedValuePy reports whether a field needs an explicit value when
// seeding (required + no default + not auto-generated primary).
func fieldNeedsSeedValuePy(f *ast.Field) bool {
	required, hasDefault, primary := false, false, false
	for _, c := range f.Constraints {
		switch c.Kind {
		case "required":
			required = true
		case "default":
			hasDefault = true
		case "primary":
			primary = true
		}
	}
	return required && !hasDefault && !primary
}

// modelFieldSamplePy synthesizes a Python expression for a seed value. Unique
// string columns get a uuid4 so test runs don't collide.
func modelFieldSamplePy(f *ast.Field) string {
	for _, c := range f.Constraints {
		if c.Kind == "unique" {
			if pt, ok := f.Type.(*ast.PrimitiveType); ok && (pt.Name == "string" || pt.Name == "text") {
				return "str(__import__('uuid').uuid4())"
			}
		}
	}
	return sampleJSONValuePy(f.Type, f.Constraints)
}

// refTargetPy returns the target model name of a ref() constraint, or "".
func refTargetPy(f *ast.Field) string {
	for _, c := range f.Constraints {
		if c.Kind == "ref" {
			if id, ok := c.Value.(*ast.Ident); ok {
				return id.Name
			}
			if s, ok := c.Value.(*ast.StringLit); ok {
				return s.Value
			}
		}
	}
	return ""
}

// findModel returns the model with the matching name, or nil.
func findModel(models []*ast.Model, name string) *ast.Model {
	for _, m := range models {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// firstDataOpModelInStmts returns the model name of the first data operation
// in stmts (e.g. "todo" from `fetch todo(id)`), recursing into when / try.
func firstDataOpModelInStmts(stmts []ast.ArrowStmt) string {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.StepStmt:
			if fn, ok := v.Expr.(*ast.FnCall); ok && isDataOpPy(fn.Name) && len(fn.Args) > 0 {
				if id, ok := fn.Args[0].(*ast.Ident); ok {
					return id.Name
				}
			}
		case *ast.WhenStmt:
			if m := firstDataOpModelInStmts(v.Body); m != "" {
				return m
			}
		case *ast.TryRecover:
			if m := firstDataOpModelInStmts(v.Try); m != "" {
				return m
			}
		}
	}
	return ""
}

// isDataOpPy reports whether a step name is a data operation.
func isDataOpPy(name string) bool {
	switch name {
	case "save", "fetch", "query", "update", "delete":
		return true
	}
	return false
}

// --- Value synthesis ---

// sampleJSONValuePy returns a Python expression literal for a JSON body /
// kwarg value honoring the type and its constraints.
func sampleJSONValuePy(t ast.TypeExpr, constraints []*ast.Constraint_) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "int", "money":
			return intSampleStrPy(constraints)
		case "float":
			return "1.5"
		case "bool":
			return "True"
		case "uuid":
			return "\"" + zeroUUIDPy + "\""
		case "timestamp":
			return "\"2025-01-01T00:00:00Z\""
		default:
			return "\"" + stringSamplePy(constraints) + "\""
		}
	case *ast.TypedJSONType:
		return "{}"
	case *ast.ListType:
		return "[]"
	case *ast.MapType:
		return "{}"
	case *ast.EnumInline:
		if len(v.Variants) > 0 {
			return "\"" + v.Variants[0] + "\""
		}
		return "\"test\""
	}
	return "\"test\""
}

// sampleQueryValuePy returns a raw (unquoted) value for a querystring.
func sampleQueryValuePy(t ast.TypeExpr, constraints []*ast.Constraint_) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "int", "money":
			return intSampleStrPy(constraints)
		case "float":
			return "1.5"
		case "bool":
			return "true"
		case "uuid":
			return zeroUUIDPy
		default:
			return stringSamplePy(constraints)
		}
	case *ast.EnumInline:
		if len(v.Variants) > 0 {
			return v.Variants[0]
		}
		return "test"
	}
	return "test"
}

func intSampleStrPy(constraints []*ast.Constraint_) string {
	for _, c := range constraints {
		if c.Kind == "min" {
			if il, ok := c.Value.(*ast.IntLit); ok {
				return il.Value
			}
		}
	}
	return "1"
}

func stringSamplePy(constraints []*ast.Constraint_) string {
	for _, c := range constraints {
		if c.Kind == "format" {
			if s, ok := c.Value.(*ast.StringLit); ok {
				switch s.Value {
				case "email":
					return "test@example.com"
				case "url":
					return "https://example.com"
				case "uuid":
					return zeroUUIDPy
				}
			}
			if id, ok := c.Value.(*ast.Ident); ok {
				switch id.Name {
				case "email":
					return "test@example.com"
				case "url":
					return "https://example.com"
				case "uuid":
					return zeroUUIDPy
				}
			}
		}
	}
	return "test"
}

// --- Endpoint analysis ---

// collectAllowedStatusesPy returns the sorted set of HTTP statuses an endpoint
// may return. Mirrors the JS-side collector: declared outputs + declared guard
// statuses + always-possible validation/runtime fallbacks (400, 500).
func collectAllowedStatusesPy(ep *ast.Endpoint) []string {
	set := map[string]bool{"400": true, "422": true, "500": true}
	collectStatusesPy(ep.Stmts, set)
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func collectStatusesPy(stmts []ast.ArrowStmt, set map[string]bool) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.OutputStmt:
			st := v.Status
			if st == "" {
				st = "200"
			}
			set[st] = true
		case *ast.GuardStmt:
			if v.Status != "" {
				set[v.Status] = true
			}
		case *ast.WhenStmt:
			collectStatusesPy(v.Body, set)
		case *ast.TryRecover:
			collectStatusesPy(v.Try, set)
			collectStatusesPy(v.Recover, set)
		}
	}
}

type shapeCheckPy struct {
	status string
	keys   []string
}

// collectShapeChecksPy finds 2xx outputs whose body is an object literal and
// returns the declared top-level keys to assert on the response body.
func collectShapeChecksPy(stmts []ast.ArrowStmt) []shapeCheckPy {
	var checks []shapeCheckPy
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.OutputStmt:
			status := v.Status
			if status == "" {
				status = "200"
			}
			if !strings.HasPrefix(status, "2") {
				continue
			}
			if blk, ok := v.Value.(*ast.BlockExpr); ok && len(blk.Entries) > 0 {
				keys := make([]string, 0, len(blk.Entries))
				for _, kv := range blk.Entries {
					keys = append(keys, kv.Key)
				}
				checks = append(checks, shapeCheckPy{status: status, keys: keys})
			}
		case *ast.WhenStmt:
			checks = append(checks, collectShapeChecksPy(v.Body)...)
		case *ast.TryRecover:
			checks = append(checks, collectShapeChecksPy(v.Try)...)
		}
	}
	return checks
}
