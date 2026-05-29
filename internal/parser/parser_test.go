package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// --- Helpers ---

func parseString(t *testing.T, src string) *ast.File {
	t.Helper()
	f, errs := ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		for _, e := range errs {
			t.Logf("  error: %s", e)
		}
		t.Fatalf("unexpected %d error(s)", len(errs))
	}
	return f
}

func parseStringExpectErrors(t *testing.T, src string, minErrors int) (*ast.File, []ParseError) {
	t.Helper()
	f, errs := ParseFile("test.bp", []byte(src))
	if len(errs) < minErrors {
		t.Fatalf("expected at least %d error(s), got %d", minErrors, len(errs))
	}
	return f, errs
}

func loadFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	return data
}

// --- Blueprint Block Tests ---

func TestParseBlueprintMinimal(t *testing.T) {
	f := parseString(t, `blueprint "myapp" {
  version "1.0.0"
  port    3000
  runtime node
}`)
	if f.Blueprint == nil {
		t.Fatal("expected Blueprint node")
	}
	if f.Blueprint.Name != "myapp" {
		t.Errorf("expected name 'myapp', got %q", f.Blueprint.Name)
	}
	if len(f.Blueprint.Entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(f.Blueprint.Entries))
	}
	// Check entries
	if f.Blueprint.Entries[0].Key != "version" {
		t.Errorf("expected key 'version', got %q", f.Blueprint.Entries[0].Key)
	}
}

func TestParseBlueprintWithUse(t *testing.T) {
	f := parseString(t, `blueprint "myapp" {
  version "1.0.0"
  port    3000
  runtime node
  use auth
  use cors
}`)
	if len(f.Blueprint.Uses) != 2 {
		t.Errorf("expected 2 use statements, got %d", len(f.Blueprint.Uses))
	}
	if f.Blueprint.Uses[0].Name != "auth" {
		t.Errorf("expected use 'auth', got %q", f.Blueprint.Uses[0].Name)
	}
	if f.Blueprint.Uses[1].Name != "cors" {
		t.Errorf("expected use 'cors', got %q", f.Blueprint.Uses[1].Name)
	}
}

func TestBlueprintMissing(t *testing.T) {
	_, errs := parseStringExpectErrors(t, `secret API_KEY required`, 1)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "blueprint") {
			found = true
		}
	}
	if !found {
		t.Error("expected error about missing 'blueprint' declaration")
	}
}

// --- Secret Tests ---

func TestParseSecretRequired(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
secret API_KEY required`)
	if len(f.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(f.Blocks))
	}
	s, ok := f.Blocks[0].(*ast.Secret)
	if !ok {
		t.Fatalf("expected *ast.Secret, got %T", f.Blocks[0])
	}
	if s.Name != "API_KEY" {
		t.Errorf("expected name 'API_KEY', got %q", s.Name)
	}
	if !s.Required {
		t.Error("expected Required to be true")
	}
}

func TestParseSecretOptionalWithDefault(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
secret TOKEN optional default("")`)
	s := f.Blocks[0].(*ast.Secret)
	if s.Required {
		t.Error("expected Required to be false")
	}
	if s.Default == nil {
		t.Error("expected Default to be set")
	}
}

// --- Env Tests ---

func TestParseEnvString(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
env LOG_LEVEL "info"`)
	e, ok := f.Blocks[0].(*ast.Env)
	if !ok {
		t.Fatalf("expected *ast.Env, got %T", f.Blocks[0])
	}
	if e.Name != "LOG_LEVEL" {
		t.Errorf("expected name 'LOG_LEVEL', got %q", e.Name)
	}
	lit, ok := e.Value.(*ast.StringLit)
	if !ok {
		t.Fatalf("expected *ast.StringLit, got %T", e.Value)
	}
	if lit.Value != "info" {
		t.Errorf("expected value 'info', got %q", lit.Value)
	}
}

func TestParseEnvSize(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
env MAX_SIZE 10mb`)
	e := f.Blocks[0].(*ast.Env)
	lit, ok := e.Value.(*ast.SizeLit)
	if !ok {
		t.Fatalf("expected *ast.SizeLit, got %T", e.Value)
	}
	if lit.Value != "10mb" {
		t.Errorf("expected value '10mb', got %q", lit.Value)
	}
}

// --- Include Tests ---

func TestParseInclude(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
include "models.bp"
include "endpoints/api.bp"`)
	if len(f.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(f.Blocks))
	}
	inc0 := f.Blocks[0].(*ast.Include)
	inc1 := f.Blocks[1].(*ast.Include)
	if inc0.Path != "models.bp" {
		t.Errorf("expected path 'models.bp', got %q", inc0.Path)
	}
	if inc1.Path != "endpoints/api.bp" {
		t.Errorf("expected path 'endpoints/api.bp', got %q", inc1.Path)
	}
}

// --- Type Declaration Tests ---

func TestParseTypeDecl(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
type ImageFile {
  url    string
  width  int
  height int
}`)
	td, ok := f.Blocks[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected *ast.TypeDecl, got %T", f.Blocks[0])
	}
	if td.Name != "ImageFile" {
		t.Errorf("expected name 'ImageFile', got %q", td.Name)
	}
	if len(td.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(td.Fields))
	}
	if td.Fields[0].Name != "url" {
		t.Errorf("expected field 'url', got %q", td.Fields[0].Name)
	}
}

// --- Alias Tests ---

func TestParseAlias(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
alias Email = string format(email)`)
	a, ok := f.Blocks[0].(*ast.Alias)
	if !ok {
		t.Fatalf("expected *ast.Alias, got %T", f.Blocks[0])
	}
	if a.Name != "Email" {
		t.Errorf("expected name 'Email', got %q", a.Name)
	}
	pt, ok := a.Type.(*ast.PrimitiveType)
	if !ok {
		t.Fatalf("expected *ast.PrimitiveType, got %T", a.Type)
	}
	if pt.Name != "string" {
		t.Errorf("expected type 'string', got %q", pt.Name)
	}
	if len(a.Constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(a.Constraints))
	}
	if a.Constraints[0].Kind != "format" {
		t.Errorf("expected constraint 'format', got %q", a.Constraints[0].Kind)
	}
}

func TestParseAliasMultipleConstraints(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
alias FileSize = int min(0) max(100)`)
	a := f.Blocks[0].(*ast.Alias)
	if len(a.Constraints) != 2 {
		t.Fatalf("expected 2 constraints, got %d", len(a.Constraints))
	}
	if a.Constraints[0].Kind != "min" {
		t.Errorf("expected constraint 'min', got %q", a.Constraints[0].Kind)
	}
	if a.Constraints[1].Kind != "max" {
		t.Errorf("expected constraint 'max', got %q", a.Constraints[1].Kind)
	}
}

// --- Enum Tests ---

func TestParseEnumSimple(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
enum Status {
  pending
  processing
  done
  failed
}`)
	e, ok := f.Blocks[0].(*ast.Enum)
	if !ok {
		t.Fatalf("expected *ast.Enum, got %T", f.Blocks[0])
	}
	if e.Name != "Status" {
		t.Errorf("expected name 'Status', got %q", e.Name)
	}
	if len(e.Variants) != 4 {
		t.Fatalf("expected 4 variants, got %d", len(e.Variants))
	}
	names := []string{"pending", "processing", "done", "failed"}
	for i, n := range names {
		if e.Variants[i].Name != n {
			t.Errorf("variant %d: expected %q, got %q", i, n, e.Variants[i].Name)
		}
	}
}

// --- Model Tests ---

func TestParseModel(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
  database postgres
}
model user {
  id       uuid      primary
  name     string    required
  email    string    unique
  age      int       optional
  active   bool      default(true)
  created  timestamp default(now)
}`)
	m, ok := f.Blocks[0].(*ast.Model)
	if !ok {
		t.Fatalf("expected *ast.Model, got %T", f.Blocks[0])
	}
	if m.Name != "user" {
		t.Errorf("expected name 'user', got %q", m.Name)
	}
	if len(m.Fields) != 6 {
		t.Fatalf("expected 6 fields, got %d", len(m.Fields))
	}
	// Check first field
	if m.Fields[0].Name != "id" {
		t.Errorf("expected field name 'id', got %q", m.Fields[0].Name)
	}
	pt, ok := m.Fields[0].Type.(*ast.PrimitiveType)
	if !ok {
		t.Fatalf("expected *ast.PrimitiveType for id field, got %T", m.Fields[0].Type)
	}
	if pt.Name != "uuid" {
		t.Errorf("expected type 'uuid', got %q", pt.Name)
	}
	// Check primary constraint
	if len(m.Fields[0].Constraints) != 1 {
		t.Fatalf("expected 1 constraint on id, got %d", len(m.Fields[0].Constraints))
	}
	if m.Fields[0].Constraints[0].Kind != "primary" {
		t.Errorf("expected constraint 'primary', got %q", m.Fields[0].Constraints[0].Kind)
	}
}

func TestParseModelFieldWithDefault(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
  database postgres
}
model item {
  status string default("pending")
}`)
	m := f.Blocks[0].(*ast.Model)
	if m.Fields[0].Constraints[0].Kind != "default" {
		t.Errorf("expected constraint 'default', got %q", m.Fields[0].Constraints[0].Kind)
	}
	if m.Fields[0].Constraints[0].Value == nil {
		t.Fatal("expected constraint value to be set")
	}
}

func TestParseContent(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}

content mission {
  data json<string>
}`)

	c, ok := f.Blocks[0].(*ast.Content)
	if !ok {
		t.Fatalf("expected *ast.Content, got %T", f.Blocks[0])
	}
	if c.Name != "mission" {
		t.Fatalf("expected content mission, got %q", c.Name)
	}
	if len(c.Fields) != 1 {
		t.Fatalf("expected 1 explicit field, got %d", len(c.Fields))
	}
	model := c.AsModel()
	if len(model.Fields) < 6 {
		t.Fatalf("expected content to expand to versioned model fields, got %d", len(model.Fields))
	}
}

func TestParseLocaleAndTranslation(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
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
}`)

	loc1, ok := f.Blocks[0].(*ast.Locale)
	if !ok {
		t.Fatalf("expected first block to be locale, got %T", f.Blocks[0])
	}
	if loc1.Code != "en" || !loc1.Default {
		t.Fatalf("unexpected first locale: %+v", loc1)
	}
	loc2, ok := f.Blocks[1].(*ast.Locale)
	if !ok {
		t.Fatalf("expected second block to be locale, got %T", f.Blocks[1])
	}
	if loc2.Code != "fr-FR" || loc2.Fallback != "en" {
		t.Fatalf("unexpected second locale: %+v", loc2)
	}
	tr, ok := f.Blocks[2].(*ast.Translation)
	if !ok {
		t.Fatalf("expected translation block, got %T", f.Blocks[2])
	}
	if tr.Name != "mission_text" || len(tr.Keys) != 2 {
		t.Fatalf("unexpected translation: %+v", tr)
	}
	if len(tr.Bundles) != 1 || tr.Bundles[0].Locale != "en" || tr.Bundles[0].Values[0].Key != "mission.start" {
		t.Fatalf("unexpected translation bundles: %+v", tr.Bundles)
	}
}

func TestParseStateAnalyticsAndSaveSchema(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}

state mission_status {
  draft -> published
}

analytics gameplay {
  event mission_started
  sink console
}

save player_progress {
  model save_slot
  version_field save_version
  latest 3
  migrate 1 -> 2 using "./player-progress"
}`)

	if _, ok := f.Blocks[0].(*ast.StateMachine); !ok {
		t.Fatalf("expected state machine, got %T", f.Blocks[0])
	}
	if _, ok := f.Blocks[1].(*ast.Analytics); !ok {
		t.Fatalf("expected analytics block, got %T", f.Blocks[1])
	}
	save, ok := f.Blocks[2].(*ast.SaveSchema)
	if !ok {
		t.Fatalf("expected save schema, got %T", f.Blocks[2])
	}
	if save.Model != "save_slot" || save.VersionField != "save_version" || save.Latest != 3 {
		t.Fatalf("unexpected save schema: %+v", save)
	}
	if len(save.Migrations) != 1 || save.Migrations[0].From != 1 || save.Migrations[0].To != 2 || save.Migrations[0].Module != "./player-progress" {
		t.Fatalf("unexpected save migrations: %+v", save.Migrations)
	}
}

// --- Endpoint Tests ---

func TestParseSimpleGetEndpoint(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/health {
  -> 200 { status: "ok" }
}`)
	ep, ok := f.Blocks[0].(*ast.Endpoint)
	if !ok {
		t.Fatalf("expected *ast.Endpoint, got %T", f.Blocks[0])
	}
	if ep.Method != "GET" {
		t.Errorf("expected method 'GET', got %q", ep.Method)
	}
	if ep.Path != "/api/health" {
		t.Errorf("expected path '/api/health', got %q", ep.Path)
	}
	if len(ep.Stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(ep.Stmts))
	}
	out, ok := ep.Stmts[0].(*ast.OutputStmt)
	if !ok {
		t.Fatalf("expected *ast.OutputStmt, got %T", ep.Stmts[0])
	}
	if out.Status != "200" {
		t.Errorf("expected status '200', got %q", out.Status)
	}
}

func TestParsePostWithInputs(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
  database postgres
}
model user {
  id    uuid   primary
  name  string required
  email string unique
}
POST /api/users {
  <- name  string required
  <- email string required
  |> user = save user { name: name, email: email }
  -> 201 { id: user.id, name: user.name }
}`)
	ep := f.Blocks[1].(*ast.Endpoint)
	if ep.Method != "POST" {
		t.Errorf("expected method 'POST', got %q", ep.Method)
	}
	if ep.Path != "/api/users" {
		t.Errorf("expected path '/api/users', got %q", ep.Path)
	}
	// Should have 2 inputs, 1 step, 1 output
	inputs := 0
	steps := 0
	outputs := 0
	for _, s := range ep.Stmts {
		switch s.(type) {
		case *ast.InputStmt:
			inputs++
		case *ast.StepStmt:
			steps++
		case *ast.OutputStmt:
			outputs++
		}
	}
	if inputs != 2 {
		t.Errorf("expected 2 inputs, got %d", inputs)
	}
	if steps != 1 {
		t.Errorf("expected 1 step, got %d", steps)
	}
	if outputs != 1 {
		t.Errorf("expected 1 output, got %d", outputs)
	}
}

func TestParseEndpointWithPathParams(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/items/:id {
  <- id uuid required
  -> 200 { id: id }
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	if ep.Path != "/api/items/:id" {
		t.Errorf("expected path '/api/items/:id', got %q", ep.Path)
	}
}

// --- Guard Tests ---

func TestParseGuard(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  <- id uuid required
  |> item = fetch item(id)
  |> guard item -> 404 "Item not found"
  -> 200 { id: item.id }
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	var guard *ast.GuardStmt
	for _, s := range ep.Stmts {
		if g, ok := s.(*ast.GuardStmt); ok {
			guard = g
			break
		}
	}
	if guard == nil {
		t.Fatal("expected guard statement")
	}
	if guard.Status != "404" {
		t.Errorf("expected status '404', got %q", guard.Status)
	}
	if guard.Message != "Item not found" {
		t.Errorf("expected message 'Item not found', got %q", guard.Message)
	}
}

func TestParseGuardWithComparison(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> guard item.user_id == auth.id -> 403 "Not your item"
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	guard := ep.Stmts[0].(*ast.GuardStmt)
	if guard.Status != "403" {
		t.Errorf("expected status '403', got %q", guard.Status)
	}
	// Condition should be a BinaryExpr
	_, ok := guard.Condition.(*ast.BinaryExpr)
	if !ok {
		t.Errorf("expected *ast.BinaryExpr, got %T", guard.Condition)
	}
}

// --- When Tests ---

func TestParseWhenInline(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
POST /api/process {
  <- mode string optional
  |> when mode == "fast": log "Using fast mode"
  -> 200 { status: "done" }
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	var when *ast.WhenStmt
	for _, s := range ep.Stmts {
		if w, ok := s.(*ast.WhenStmt); ok {
			when = w
			break
		}
	}
	if when == nil {
		t.Fatal("expected when statement")
	}
	if when.Inline == nil {
		t.Error("expected inline expression for when")
	}
	if len(when.Body) != 0 {
		t.Error("expected no body for inline when")
	}
}

func TestParseWhenBlock(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
POST /api/test {
  |> when plan == "pro" {
    |> log "Upgrading to pro"
  }
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	when := ep.Stmts[0].(*ast.WhenStmt)
	if when.Inline != nil {
		t.Error("expected no inline expression for block when")
	}
	if len(when.Body) != 1 {
		t.Errorf("expected 1 body statement, got %d", len(when.Body))
	}
}

// --- Try/Recover Tests ---

func TestParseTryRecover(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
POST /api/process {
  <- data string required
  |> try {
    |> result = transform(data)
    |> log "Success: {result}"
  } recover {
    |> log "Failed: {error.message}"
    -> 500 { error: "Processing failed" }
  }
  -> 200 { result: result }
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	var tr *ast.TryRecover
	for _, s := range ep.Stmts {
		if t2, ok := s.(*ast.TryRecover); ok {
			tr = t2
			break
		}
	}
	if tr == nil {
		t.Fatal("expected try/recover statement")
	}
	if len(tr.Try) != 2 {
		t.Errorf("expected 2 try statements, got %d", len(tr.Try))
	}
	if len(tr.Recover) != 2 {
		t.Errorf("expected 2 recover statements, got %d", len(tr.Recover))
	}
}

// --- Pipe Tests ---

func TestParsePipe(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
pipe validate_input {
  <- data string
  |> guard data != "" -> 400 "Data cannot be empty"
  -> data
}`)
	p, ok := f.Blocks[0].(*ast.Pipe)
	if !ok {
		t.Fatalf("expected *ast.Pipe, got %T", f.Blocks[0])
	}
	if p.Name != "validate_input" {
		t.Errorf("expected name 'validate_input', got %q", p.Name)
	}
	if len(p.Stmts) != 3 {
		t.Errorf("expected 3 statements, got %d", len(p.Stmts))
	}
}

// --- Fn Tests ---

func TestParseFnWithImpl(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
fn process_data {
  <- input  string
  <- format string
  -> string
  impl node {
    module: "./internal/process"
    func:   "run"
  }
}`)
	fn, ok := f.Blocks[0].(*ast.Fn)
	if !ok {
		t.Fatalf("expected *ast.Fn, got %T", f.Blocks[0])
	}
	if fn.Name != "process_data" {
		t.Errorf("expected name 'process_data', got %q", fn.Name)
	}
	if len(fn.Inputs) != 2 {
		t.Errorf("expected 2 inputs, got %d", len(fn.Inputs))
	}
	if len(fn.Outputs) != 1 {
		t.Errorf("expected 1 output, got %d", len(fn.Outputs))
	}
	if fn.Impl == nil {
		t.Error("expected impl block")
	}
	if fn.Impl.Strategy != "node" {
		t.Errorf("expected strategy 'node', got %q", fn.Impl.Strategy)
	}
}

func TestParseFnWithLogic(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
fn calculate_price {
  <- plan       string
  <- operations int
  -> float
  logic {
    |> when plan == "free"       -> 0
    |> when plan == "pro"        -> operations * 0.01
    |> when plan == "enterprise" -> operations * 0.005
  }
}`)
	fn := f.Blocks[0].(*ast.Fn)
	if fn.Logic == nil {
		t.Fatal("expected logic block")
	}
	// Each `|> when cond -> value` produces a WhenStmt + OutputStmt pair
	if len(fn.Logic.Stmts) != 6 {
		t.Errorf("expected 6 logic statements, got %d", len(fn.Logic.Stmts))
	}
	// Verify alternating WhenStmt/OutputStmt pattern
	for i := 0; i < len(fn.Logic.Stmts) && i < 6; i += 2 {
		if _, ok := fn.Logic.Stmts[i].(*ast.WhenStmt); !ok {
			t.Errorf("stmt[%d]: expected *ast.WhenStmt, got %T", i, fn.Logic.Stmts[i])
		}
		if _, ok := fn.Logic.Stmts[i+1].(*ast.OutputStmt); !ok {
			t.Errorf("stmt[%d]: expected *ast.OutputStmt, got %T", i+1, fn.Logic.Stmts[i+1])
		}
	}
}

// --- Middleware Tests ---

func TestParseMiddlewareBefore(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Missing auth"
    |> inject token as auth
  }
}`)
	mw, ok := f.Blocks[0].(*ast.Middleware)
	if !ok {
		t.Fatalf("expected *ast.Middleware, got %T", f.Blocks[0])
	}
	if mw.Name != "require_auth" {
		t.Errorf("expected name 'require_auth', got %q", mw.Name)
	}
	if len(mw.Before) != 2 {
		t.Errorf("expected 2 before statements, got %d", len(mw.Before))
	}
}

// --- Worker Tests ---

func TestParseWorker(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
  database postgres
  queue redis
}
model job {
  id     uuid   primary
  status string default("pending")
}
worker process_job {
  trigger queue("jobs")
  retry   3
  timeout 5min
  <- job_id uuid
  |> job = fetch job(job_id)
  on_fail {
    |> log "Job failed"
  }
}`)
	w, ok := f.Blocks[1].(*ast.Worker)
	if !ok {
		t.Fatalf("expected *ast.Worker, got %T", f.Blocks[1])
	}
	if w.Name != "process_job" {
		t.Errorf("expected name 'process_job', got %q", w.Name)
	}
	if len(w.Meta) != 3 {
		t.Errorf("expected 3 meta entries, got %d", len(w.Meta))
	}
	if len(w.OnFail) != 1 {
		t.Errorf("expected 1 on_fail statement, got %d", len(w.OnFail))
	}
}

// --- Schedule Tests ---

func TestParseSchedule(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
  database postgres
}
model job {
  id      uuid      primary
  created timestamp default(now)
}
schedule cleanup {
  cron "0 4 * * 0"
  |> old = query job where(created < 90.days.ago)
  |> delete old
  |> log "Cleaned {old.count} expired jobs"
}`)
	s, ok := f.Blocks[1].(*ast.Schedule)
	if !ok {
		t.Fatalf("expected *ast.Schedule, got %T", f.Blocks[1])
	}
	if s.Name != "cleanup" {
		t.Errorf("expected name 'cleanup', got %q", s.Name)
	}
	if s.Cron != "0 4 * * 0" {
		t.Errorf("expected cron '0 4 * * 0', got %q", s.Cron)
	}
	if len(s.Stmts) != 3 {
		t.Errorf("expected 3 statements, got %d", len(s.Stmts))
	}
}

// --- External Tests ---

func TestParseExternal(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
external "auth-service" {
  url:     "http://auth:3001"
  timeout: 5s
  retry:   2
}`)
	ext, ok := f.Blocks[0].(*ast.External)
	if !ok {
		t.Fatalf("expected *ast.External, got %T", f.Blocks[0])
	}
	if ext.Name != "auth-service" {
		t.Errorf("expected name 'auth-service', got %q", ext.Name)
	}
	if len(ext.Entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(ext.Entries))
	}
}

// --- Expression Tests ---

func TestParseExprBinary(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> x = a + b * c
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	step := ep.Stmts[0].(*ast.StepStmt)
	if step.Binding != "x" {
		t.Errorf("expected binding 'x', got %q", step.Binding)
	}
	// a + (b * c) due to precedence
	bin, ok := step.Expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected *ast.BinaryExpr, got %T", step.Expr)
	}
	if bin.Op != "+" {
		t.Errorf("expected op '+', got %q", bin.Op)
	}
	// Right should be b * c
	right, ok := bin.Right.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected right to be *ast.BinaryExpr, got %T", bin.Right)
	}
	if right.Op != "*" {
		t.Errorf("expected right op '*', got %q", right.Op)
	}
}

func TestParseExprComparison(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> guard a >= 10 -> 400 "too small"
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	guard := ep.Stmts[0].(*ast.GuardStmt)
	bin, ok := guard.Condition.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected *ast.BinaryExpr, got %T", guard.Condition)
	}
	if bin.Op != ">=" {
		t.Errorf("expected op '>=', got %q", bin.Op)
	}
}

func TestParseExprLogical(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> guard a and b -> 400 "nope"
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	guard := ep.Stmts[0].(*ast.GuardStmt)
	bin, ok := guard.Condition.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected *ast.BinaryExpr, got %T", guard.Condition)
	}
	if bin.Op != "and" {
		t.Errorf("expected op 'and', got %q", bin.Op)
	}
}

func TestParseExprFieldAccess(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> x = user.name
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	step := ep.Stmts[0].(*ast.StepStmt)
	fa, ok := step.Expr.(*ast.FieldAccess)
	if !ok {
		t.Fatalf("expected *ast.FieldAccess, got %T", step.Expr)
	}
	if fa.Field != "name" {
		t.Errorf("expected field 'name', got %q", fa.Field)
	}
}

func TestParseHeaderHyphenatedName(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  <- x string
  |> key = header.X-API-Key
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	step := ep.Stmts[1].(*ast.StepStmt) // Stmts[0] is the <- input
	if step.Binding != "key" {
		t.Errorf("expected binding 'key', got %q", step.Binding)
	}
	fa, ok := step.Expr.(*ast.FieldAccess)
	if !ok {
		t.Fatalf("expected *ast.FieldAccess, got %T", step.Expr)
	}
	base, ok := fa.Base.(*ast.Ident)
	if !ok {
		t.Fatalf("expected *ast.Ident base, got %T", fa.Base)
	}
	if base.Name != "header" {
		t.Errorf("expected base 'header', got %q", base.Name)
	}
	if fa.Field != "X-API-Key" {
		t.Errorf("expected field 'X-API-Key', got %q", fa.Field)
	}
}

func TestParseExprFnCall(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> x = transform(data)
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	step := ep.Stmts[0].(*ast.StepStmt)
	fn, ok := step.Expr.(*ast.FnCall)
	if !ok {
		t.Fatalf("expected *ast.FnCall, got %T", step.Expr)
	}
	if fn.Name != "transform" {
		t.Errorf("expected name 'transform', got %q", fn.Name)
	}
	if len(fn.Args) != 1 {
		t.Errorf("expected 1 arg, got %d", len(fn.Args))
	}
}

func TestParseExprLiterals(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  -> 200 { s: "hello", i: 42, f: 3.14, b: true, n: null }
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	out := ep.Stmts[0].(*ast.OutputStmt)
	block, ok := out.Value.(*ast.BlockExpr)
	if !ok {
		t.Fatalf("expected *ast.BlockExpr, got %T", out.Value)
	}
	if len(block.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(block.Entries))
	}
	// Check string literal
	if _, ok := block.Entries[0].Value.(*ast.StringLit); !ok {
		t.Errorf("expected StringLit, got %T", block.Entries[0].Value)
	}
	// Check int literal
	if _, ok := block.Entries[1].Value.(*ast.IntLit); !ok {
		t.Errorf("expected IntLit, got %T", block.Entries[1].Value)
	}
	// Check float literal
	if _, ok := block.Entries[2].Value.(*ast.FloatLit); !ok {
		t.Errorf("expected FloatLit, got %T", block.Entries[2].Value)
	}
	// Check bool literal
	if _, ok := block.Entries[3].Value.(*ast.BoolLit); !ok {
		t.Errorf("expected BoolLit, got %T", block.Entries[3].Value)
	}
	// Check null literal
	if _, ok := block.Entries[4].Value.(*ast.NullLit); !ok {
		t.Errorf("expected NullLit, got %T", block.Entries[4].Value)
	}
}

func TestParseExprDuration(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> sleep 5s
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	// sleep should produce a StepStmt
	if len(ep.Stmts) < 1 {
		t.Fatal("expected at least 1 statement")
	}
}

func TestParseExprList(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
env TYPES ["a", "b", "c"]`)
	e := f.Blocks[0].(*ast.Env)
	list, ok := e.Value.(*ast.ListExpr)
	if !ok {
		t.Fatalf("expected *ast.ListExpr, got %T", e.Value)
	}
	if len(list.Elements) != 3 {
		t.Errorf("expected 3 elements, got %d", len(list.Elements))
	}
}

func TestParseExprNot(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> guard not active -> 400 "inactive"
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	guard := ep.Stmts[0].(*ast.GuardStmt)
	un, ok := guard.Condition.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected *ast.UnaryExpr, got %T", guard.Condition)
	}
	if un.Op != "not" {
		t.Errorf("expected op 'not', got %q", un.Op)
	}
}

// --- Input Statement Tests ---

func TestParseInputWithConstraints(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
POST /api/test {
  <- name string required min(1) max(100)
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	input := ep.Stmts[0].(*ast.InputStmt)
	if input.Name != "name" {
		t.Errorf("expected name 'name', got %q", input.Name)
	}
	pt, ok := input.Type.(*ast.PrimitiveType)
	if !ok {
		t.Fatalf("expected *ast.PrimitiveType, got %T", input.Type)
	}
	if pt.Name != "string" {
		t.Errorf("expected type 'string', got %q", pt.Name)
	}
	if len(input.Constraints) != 3 {
		t.Errorf("expected 3 constraints, got %d", len(input.Constraints))
	}
}

func TestParseTypedJSONType(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}

type MissionDefinition {
  title string required
}

POST /api/test {
  <- mission json<MissionDefinition> required
  -> 200 "ok"
}`)

	ep := f.Blocks[1].(*ast.Endpoint)
	input := ep.Stmts[0].(*ast.InputStmt)
	jt, ok := input.Type.(*ast.TypedJSONType)
	if !ok {
		t.Fatalf("expected *ast.TypedJSONType, got %T", input.Type)
	}
	inner, ok := jt.Inner.(*ast.NamedType)
	if !ok {
		t.Fatalf("expected typed json inner to be *ast.NamedType, got %T", jt.Inner)
	}
	if inner.Name != "MissionDefinition" {
		t.Errorf("expected inner type MissionDefinition, got %q", inner.Name)
	}
}

func TestParseTranslationKeyType(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}

translation mission_text {
  key "mission.start"
}

type MissionDefinition {
  title_key tkey(mission_text) required
}`)

	td := f.Blocks[1].(*ast.TypeDecl)
	tk, ok := td.Fields[0].Type.(*ast.TranslationKeyType)
	if !ok {
		t.Fatalf("expected translation key type, got %T", td.Fields[0].Type)
	}
	if tk.Namespace != "mission_text" {
		t.Fatalf("expected mission_text namespace, got %q", tk.Namespace)
	}
}

// --- Output Statement Tests ---

func TestParseOutputWithBlockBody(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  -> 200 {
    id:   user.id,
    name: user.name,
  }
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	out := ep.Stmts[0].(*ast.OutputStmt)
	if out.Status != "200" {
		t.Errorf("expected status '200', got %q", out.Status)
	}
	block, ok := out.Value.(*ast.BlockExpr)
	if !ok {
		t.Fatalf("expected *ast.BlockExpr, got %T", out.Value)
	}
	if len(block.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(block.Entries))
	}
}

func TestParseOutputStringOnly(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	out := ep.Stmts[0].(*ast.OutputStmt)
	if out.Status != "200" {
		t.Errorf("expected status '200', got %q", out.Status)
	}
	sl, ok := out.Value.(*ast.StringLit)
	if !ok {
		t.Fatalf("expected *ast.StringLit, got %T", out.Value)
	}
	if sl.Value != "ok" {
		t.Errorf("expected value 'ok', got %q", sl.Value)
	}
}

// --- Step Statement Tests ---

func TestParseStepWithBinding(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/test {
  |> result = transform(data)
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	step := ep.Stmts[0].(*ast.StepStmt)
	if step.Binding != "result" {
		t.Errorf("expected binding 'result', got %q", step.Binding)
	}
}

func TestParseStepSaveOperation(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
  database postgres
}
model item {
  id   uuid   primary
  name string required
}
POST /api/items {
  <- name string required
  |> item = save item { name: name }
  -> 201 { id: item.id }
}`)
	ep := f.Blocks[1].(*ast.Endpoint)
	step := ep.Stmts[1].(*ast.StepStmt)
	if step.Binding != "item" {
		t.Errorf("expected binding 'item', got %q", step.Binding)
	}
}

// --- Test Block Tests ---

func TestParseTestBlock(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
  database postgres
}
model user {
  id   uuid   primary
  name string required
}
test create_user {
  target POST /api/users
  request {
    body {
      name: "Test User",
    }
  }
  expect {
    status 200
    body.name == "Test User"
  }
}`)
	test, ok := f.Blocks[1].(*ast.Test)
	if !ok {
		t.Fatalf("expected *ast.Test, got %T", f.Blocks[1])
	}
	if test.Name != "create_user" {
		t.Errorf("expected name 'create_user', got %q", test.Name)
	}
	if test.Target == nil {
		t.Fatal("expected target")
	}
	if test.Target.Method != "POST" {
		t.Errorf("expected target method 'POST', got %q", test.Target.Method)
	}
}

// --- Fixture Tests ---

func TestParseFixtureFrom(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
fixture "test-data" from "testdata/seed.json"`)
	fix, ok := f.Blocks[0].(*ast.Fixture)
	if !ok {
		t.Fatalf("expected *ast.Fixture, got %T", f.Blocks[0])
	}
	if fix.Name != "test-data" {
		t.Errorf("expected name 'test-data', got %q", fix.Name)
	}
	if fix.FromPath != "testdata/seed.json" {
		t.Errorf("expected from path 'testdata/seed.json', got %q", fix.FromPath)
	}
}

// --- Error Recovery Tests ---

func TestErrorRecoverySkipsBadBlock(t *testing.T) {
	src := `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
model user |
  id uuid primary
}
secret API_KEY required`
	f, errs := ParseFile("test.bp", []byte(src))
	if len(errs) == 0 {
		t.Fatal("expected at least 1 error")
	}
	// Despite the bad model block, should still parse the secret
	found := false
	for _, b := range f.Blocks {
		if _, ok := b.(*ast.Secret); ok {
			found = true
		}
	}
	if !found {
		t.Error("expected recovery to parse the secret block after bad model")
	}
}

func TestErrorMissingClosingBrace(t *testing.T) {
	src := `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
model user {
  id   uuid   primary
  name string required
`
	_, errs := ParseFile("test.bp", []byte(src))
	if len(errs) == 0 {
		t.Fatal("expected error for missing closing brace")
	}
}

func TestErrorUnexpectedToken(t *testing.T) {
	src := `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
12345
model user {
  id uuid primary
}`
	f, errs := ParseFile("test.bp", []byte(src))
	if len(errs) == 0 {
		t.Fatal("expected error for unexpected token")
	}
	// Should still recover and parse the model
	found := false
	for _, b := range f.Blocks {
		if _, ok := b.(*ast.Model); ok {
			found = true
		}
	}
	if !found {
		t.Error("expected recovery to parse the model block after unexpected token")
	}
}

func TestErrorBadArrow(t *testing.T) {
	src := `blueprint "test" {
  version "0.1.0"
  port    3000
  runtime node
}
GET /api/test {
  !> data string
  -> 200 "ok"
}`
	_, errs := ParseFile("test.bp", []byte(src))
	if len(errs) == 0 {
		t.Fatal("expected error for bad arrow")
	}
}

func TestErrorMissingPath(t *testing.T) {
	src := `blueprint "test" {
  version "0.1.0"
  port    3000
  runtime node
}
POST {
  <- name string required
  -> 200 "ok"
}`
	_, errs := ParseFile("test.bp", []byte(src))
	if len(errs) == 0 {
		t.Fatal("expected error for missing path")
	}
}

func TestErrorEmptyFile(t *testing.T) {
	src := `# This file is empty except for a comment
`
	_, errs := ParseFile("test.bp", []byte(src))
	if len(errs) == 0 {
		t.Fatal("expected error for empty file with no blueprint")
	}
}

// --- File-based Tests ---

func TestValidFixtures(t *testing.T) {
	fixtures, err := filepath.Glob("../../testdata/valid/*.bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no valid fixtures found")
	}
	for _, fixture := range fixtures {
		name := filepath.Base(fixture)
		t.Run(name, func(t *testing.T) {
			src := loadFixture(t, fixture)
			_, errs := ParseFile(name, src)
			if len(errs) > 0 {
				for _, e := range errs {
					t.Logf("  error: %s", e)
				}
				t.Fatalf("%s: expected no errors, got %d", name, len(errs))
			}
		})
	}
}

func TestInvalidFixtures(t *testing.T) {
	// Files that only produce checker (semantic) errors, not parser errors.
	// These are validated by the checker tests, not the parser.
	checkerOnly := map[string]bool{
		"duplicate_model_name.bp": true,
		"duplicate_endpoint.bp":   true,
		"lowercase_secret.bp":     true,
		"lowercase_type.bp":       true,
		"uppercase_model.bp":      true,
		"unknown_type.bp":         true,
		"output_before_step.bp":   true,
		"wrong_arrow_order.bp":    true,
		"nested_try.bp":           true,
		"deep_nesting.bp":         true,
		"unknown_ref_target.bp":   true,
		"unknown_external.bp":     true,
	}
	// Files that require future-milestone features to detect errors.
	futureFeature := map[string]bool{
		"blueprint_in_include.bp": true,
		"circular_include.bp":     true,
		"invalid_cron.bp":         true,
		"unknown_function.bp":     true,
		"empty_endpoint.bp":       true,
	}

	fixtures, err := filepath.Glob("../../testdata/invalid/*.bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no invalid fixtures found")
	}
	for _, fixture := range fixtures {
		name := filepath.Base(fixture)
		t.Run(name, func(t *testing.T) {
			if futureFeature[name] {
				t.Skipf("%s: requires future milestone feature", name)
				return
			}
			src := loadFixture(t, fixture)
			_, errs := ParseFile(name, src)
			if checkerOnly[name] {
				// These files are syntactically valid; errors come from the checker.
				if len(errs) > 0 {
					t.Fatalf("%s: expected no parser errors (checker-only), got %d", name, len(errs))
				}
				return
			}
			if len(errs) == 0 {
				t.Fatalf("%s: expected parser errors, got none", name)
			}
		})
	}
}

// --- Source Location Tests ---

func TestBlueprintSourceLocation(t *testing.T) {
	src := `blueprint "myapp" {
  version "1.0.0"
  port    3000
  runtime node
}`
	f := parseString(t, src)
	if f.Blueprint.Loc.Line != 1 {
		t.Errorf("expected blueprint on line 1, got %d", f.Blueprint.Loc.Line)
	}
	if f.Blueprint.Loc.Col != 1 {
		t.Errorf("expected blueprint at col 1, got %d", f.Blueprint.Loc.Col)
	}
}

func TestEndpointSourceLocation(t *testing.T) {
	src := `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
GET /api/health {
  -> 200 "ok"
}`
	f := parseString(t, src)
	ep := f.Blocks[0].(*ast.Endpoint)
	if ep.Loc.Line != 6 {
		t.Errorf("expected endpoint on line 6, got %d", ep.Loc.Line)
	}
}

// --- Error Format Tests ---

func TestFormatError(t *testing.T) {
	src := []byte("blueprint \"test\" {\n  version \"0.1.0\"\n  bad token\n}")
	_, errs := ParseFile("test.bp", src)
	if len(errs) == 0 {
		// May not error depending on parsing, skip
		t.Skip("no errors to format")
	}
	formatted := FormatError(errs[0], src)
	if !strings.Contains(formatted, "error:") {
		t.Error("formatted error should contain 'error:'")
	}
}

// --- Complex Integration Tests ---

func TestParseMultipleBlockTypes(t *testing.T) {
	src := `blueprint "complex" {
  version "0.1.0"
  port 3000
  runtime node
  database postgres
}

secret DB_URL required

env LOG_LEVEL "debug"

include "models.bp"

model user {
  id   uuid   primary
  name string required
}

enum Status {
  active
  inactive
}

alias Email = string format(email)

type Pagination {
  page int default(1)
  total int
}

GET /api/users {
  -> 200 { users: [] }
}

POST /api/users {
  <- name string required
  |> user = save user { name: name }
  -> 201 { id: user.id }
}`
	f := parseString(t, src)
	// Count block types
	counts := map[string]int{}
	for _, b := range f.Blocks {
		switch b.(type) {
		case *ast.Secret:
			counts["secret"]++
		case *ast.Env:
			counts["env"]++
		case *ast.Include:
			counts["include"]++
		case *ast.Model:
			counts["model"]++
		case *ast.Enum:
			counts["enum"]++
		case *ast.Alias:
			counts["alias"]++
		case *ast.TypeDecl:
			counts["type"]++
		case *ast.Endpoint:
			counts["endpoint"]++
		}
	}
	if counts["secret"] != 1 {
		t.Errorf("expected 1 secret, got %d", counts["secret"])
	}
	if counts["env"] != 1 {
		t.Errorf("expected 1 env, got %d", counts["env"])
	}
	if counts["include"] != 1 {
		t.Errorf("expected 1 include, got %d", counts["include"])
	}
	if counts["model"] != 1 {
		t.Errorf("expected 1 model, got %d", counts["model"])
	}
	if counts["enum"] != 1 {
		t.Errorf("expected 1 enum, got %d", counts["enum"])
	}
	if counts["alias"] != 1 {
		t.Errorf("expected 1 alias, got %d", counts["alias"])
	}
	if counts["type"] != 1 {
		t.Errorf("expected 1 type, got %d", counts["type"])
	}
	if counts["endpoint"] != 2 {
		t.Errorf("expected 2 endpoints, got %d", counts["endpoint"])
	}
}

func TestParseEndpointWithMeta(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
POST /api/upload {
  use auth
  limit 10mb
  timeout 30s
  <- file string required
  -> 200 "ok"
}`)
	ep := f.Blocks[0].(*ast.Endpoint)
	if len(ep.Meta) != 3 {
		t.Errorf("expected 3 meta entries, got %d", len(ep.Meta))
	}
}

func TestParseSubscribe(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
subscribe "user.created" from(auth_service) {
  |> log "User created"
}`)
	sub, ok := f.Blocks[0].(*ast.Subscribe)
	if !ok {
		t.Fatalf("expected *ast.Subscribe, got %T", f.Blocks[0])
	}
	if sub.Event != "user.created" {
		t.Errorf("expected event 'user.created', got %q", sub.Event)
	}
	if sub.From != "auth_service" {
		t.Errorf("expected from 'auth_service', got %q", sub.From)
	}
	if len(sub.Stmts) != 1 {
		t.Errorf("expected 1 statement, got %d", len(sub.Stmts))
	}
}

func TestParseEnumRich(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
enum Plan {
  free {
    limit: 100,
    rate:  10/min,
  }
  pro {
    limit: 10000,
    rate:  100/min,
  }
}`)
	e := f.Blocks[0].(*ast.Enum)
	if len(e.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(e.Variants))
	}
	if e.Variants[0].Name != "free" {
		t.Errorf("expected variant 'free', got %q", e.Variants[0].Name)
	}
	if e.Variants[0].Body == nil {
		t.Error("expected body on 'free' variant")
	}
}

func TestParseMiddlewareBeforeAndAfter(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
middleware logging {
  before {
    |> inject start_time as start
  }
  after {
    |> log "Request took {elapsed}ms"
  }
}`)
	mw := f.Blocks[0].(*ast.Middleware)
	if len(mw.Before) != 1 {
		t.Errorf("expected 1 before statement, got %d", len(mw.Before))
	}
	if len(mw.After) != 1 {
		t.Errorf("expected 1 after statement, got %d", len(mw.After))
	}
}

func TestParseTestWithSetupCleanup(t *testing.T) {
	f := parseString(t, `blueprint "t" {
  version "0.1.0"
  port 3000
  runtime node
}
test my_test {
  target GET /api/health
  setup {
    |> log "setup"
  }
  request {
    body {
      key: "value",
    }
  }
  expect {
    status 200
  }
  cleanup {
    |> log "cleanup"
  }
}`)
	test := f.Blocks[0].(*ast.Test)
	if len(test.Setup) != 1 {
		t.Errorf("expected 1 setup statement, got %d", len(test.Setup))
	}
	if len(test.Cleanup) != 1 {
		t.Errorf("expected 1 cleanup statement, got %d", len(test.Cleanup))
	}
	if len(test.Expect) != 1 {
		t.Errorf("expected 1 assertion, got %d", len(test.Expect))
	}
}

func TestParseAllFeaturesFixture(t *testing.T) {
	// This is the most comprehensive test — parse the complete all_features.bp
	src := loadFixture(t, "../../testdata/valid/all_features.bp")
	f, errs := ParseFile("all_features.bp", src)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Logf("  error: %s", e)
		}
		t.Fatalf("expected no errors parsing all_features.bp, got %d", len(errs))
	}
	if f.Blueprint == nil {
		t.Fatal("expected Blueprint node")
	}
	if len(f.Blocks) == 0 {
		t.Fatal("expected at least 1 top-level block beyond blueprint")
	}
}
