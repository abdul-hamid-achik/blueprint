package ast_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestPrint_PreservesCommentsAndIsIdempotent(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "valid", "comments.bp")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	file := parseForPrint(t, string(src))
	formatted := ast.Print(file)
	if got, want := strings.Count(formatted, "#"), strings.Count(string(src), "#"); got != want {
		t.Fatalf("formatting changed the comment count: got %d, want %d\n%s", got, want, formatted)
	}

	reparsed := parseForPrint(t, formatted)
	second := ast.Print(reparsed)
	if second != formatted {
		t.Fatalf("comment-aware formatting is not idempotent\nfirst:\n%s\nsecond:\n%s", formatted, second)
	}

	for _, want := range []string{
		"# Top-level comment",
		"secret API_KEY required  # inline comment",
		"  # Comment inside block",
		`  -> 200 { status: "ok" }  # trailing comment`,
	} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatted output lost comment placement %q", want)
		}
	}
}

func TestPrint_IncludeFragmentWithoutBlueprint(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "valid", "includes", "models.bp")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, errs := parser.ParsePartialFile(path, src)
	if len(errs) > 0 {
		t.Fatalf("parse fragment: %v", errs)
	}
	formatted := ast.Print(file)
	if formatted != string(src) {
		t.Fatalf("include fragment changed during formatting\nwant:\n%s\ngot:\n%s", src, formatted)
	}
	reparsed, errs := parser.ParsePartialFile(path, []byte(formatted))
	if len(errs) > 0 {
		t.Fatalf("reparse formatted fragment: %v", errs)
	}
	if second := ast.Print(reparsed); second != formatted {
		t.Fatalf("include-fragment formatting is not idempotent")
	}
}

// parseForPrint is a helper that parses src and fatals on errors.
func parseForPrint(t *testing.T, src string) *ast.File {
	t.Helper()
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return file
}

func TestPrint_Blueprint(t *testing.T) {
	src := `blueprint "myapp" {
  version "1.0.0"
  port    3000
  runtime node
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	checks := []string{
		`blueprint "myapp" {`,
		`version "1.0.0"`,
		`port    3000`,
		`runtime node`,
		"}",
	}
	for _, want := range checks {
		if !strings.Contains(output, want) {
			t.Errorf("Print output missing %q\nfull output:\n%s", want, output)
		}
	}
}

func TestPrint_Model(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

model user {
  id    uuid   primary
  name  string required
  email string unique
  age   int    optional
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	checks := []string{
		"model user {",
		"id    uuid   primary",
		"name  string required",
		"email string unique",
		"age   int    optional",
	}
	for _, want := range checks {
		if !strings.Contains(output, want) {
			t.Errorf("Print output missing %q\nfull output:\n%s", want, output)
		}
	}
}

func TestPrint_ModelWithConstraints(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

model product {
  id    uuid   primary
  price int    min(0)
  title string required
  stock int    default(0)
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	if !strings.Contains(output, "model product {") {
		t.Errorf("output missing model declaration: %s", output)
	}
	if !strings.Contains(output, "min(0)") {
		t.Errorf("output missing min(0) constraint: %s", output)
	}
	if !strings.Contains(output, "default(0)") {
		t.Errorf("output missing default(0) constraint: %s", output)
	}
}

func TestPrint_Endpoint(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

@ "Create a new item"
POST /api/items {
  <- name string required
  <- price int min(0)
  -> 201 { name: name, price: price }
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	checks := []string{
		`@ "Create a new item"`,
		"POST /api/items {",
		"<-",
		"name",
		"string",
		"required",
		"->",
		"201",
	}
	for _, want := range checks {
		if !strings.Contains(output, want) {
			t.Errorf("Print output missing %q\nfull output:\n%s", want, output)
		}
	}
}

func TestPrint_EndpointGET(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

@ "Health check"
GET /api/health {
  -> 200 "ok"
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	if !strings.Contains(output, "GET /api/health {") {
		t.Errorf("output missing GET endpoint header: %s", output)
	}
	if !strings.Contains(output, `-> 200 "ok"`) {
		t.Errorf("output missing output stmt: %s", output)
	}
}

func TestPrint_Enum(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

enum color {
  red
  green
  blue
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	checks := []string{
		"enum color {",
		"red",
		"green",
		"blue",
	}
	for _, want := range checks {
		if !strings.Contains(output, want) {
			t.Errorf("Print output missing %q\nfull output:\n%s", want, output)
		}
	}
}

func TestPrint_Intent(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

@ "Returns all available items from the store"
GET /api/items {
  -> 200 "ok"
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	if !strings.Contains(output, `@ "Returns all available items from the store"`) {
		t.Errorf("output missing intent annotation\nfull output:\n%s", output)
	}
}

func TestPrint_Secret(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

secret DB_URL required
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	if !strings.Contains(output, "secret DB_URL required") {
		t.Errorf("output missing 'secret DB_URL required'\nfull output:\n%s", output)
	}
}

func TestPrint_SecretOptional(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

secret OPTIONAL_KEY optional
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	if !strings.Contains(output, "secret OPTIONAL_KEY optional") {
		t.Errorf("output missing 'secret OPTIONAL_KEY optional'\nfull output:\n%s", output)
	}
}

func TestPrint_Roundtrip(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

secret DB_URL required

model item {
  id    uuid   primary
  name  string required
  price int    min(0)
}

enum status {
  active
  inactive
}

@ "List items"
GET /api/items {
  <- page int default(1)
  -> 200 "ok"
}

@ "Create item"
POST /api/items {
  <- name  string required
  <- price int    min(0)
  -> 201 "created"
}
`
	// First parse + print
	file1 := parseForPrint(t, src)
	printed1 := ast.Print(file1)

	// Second parse of the printed output + print again
	file2, errs2 := parser.ParseFile("test.bp", []byte(printed1))
	if len(errs2) > 0 {
		t.Fatalf("re-parse errors after first print: %v\nprinted:\n%s", errs2, printed1)
	}
	printed2 := ast.Print(file2)

	// The two printed outputs must be identical (idempotent).
	if printed1 != printed2 {
		t.Errorf("Print is not idempotent:\n--- first print ---\n%s\n--- second print ---\n%s", printed1, printed2)
	}
}

func TestPrint_RoundtripPreservesBlueprint(t *testing.T) {
	src := `blueprint "my-service" {
  version "2.3.1"
  port    9090
  runtime node
  database postgres
}
`
	file1 := parseForPrint(t, src)
	printed1 := ast.Print(file1)

	file2, errs2 := parser.ParseFile("test.bp", []byte(printed1))
	if len(errs2) > 0 {
		t.Fatalf("re-parse errors: %v\nprinted:\n%s", errs2, printed1)
	}
	printed2 := ast.Print(file2)

	if printed1 != printed2 {
		t.Errorf("blueprint block roundtrip not idempotent:\n%s\nvs\n%s", printed1, printed2)
	}

	if !strings.Contains(printed1, `blueprint "my-service"`) {
		t.Errorf("printed output missing blueprint name: %s", printed1)
	}
	if !strings.Contains(printed1, `version  "2.3.1"`) {
		t.Errorf("printed output missing version: %s", printed1)
	}
}

func TestPrint_NonEmpty(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	if output == "" {
		t.Error("Print should not return empty string")
	}
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("Print output should end with newline, got: %q", output)
	}
}

func TestPrint_DataOpShorthand(t *testing.T) {
	// bp fmt must preserve the README idiom for data-op steps:
	//   |> todos = query todo paginate(page, per_page)
	// rather than rewriting them into the equivalent function-call form
	//   |> todos = query(todo, paginate(page, per_page))
	src := `blueprint "test" {
  version "1.0.0"
  port 8080
  runtime node
}

model todo {
  id    uuid   primary
  title string required
}

GET /api/todos {
  <- page int default(1)
  <- per_page int default(20)
  |> todos = query todo paginate(page, per_page)
  -> 200 { items: todos.items }
}

POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id }
}

GET /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "not found"
  -> 200 { id: todo.id }
}

DELETE /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> delete todo
  -> 204 "deleted"
}
`
	file := parseForPrint(t, src)
	out := ast.Print(file)

	mustContain := []string{
		"query todo paginate(page, per_page)",
		"save todo { title: title }",
		"fetch todo(id)",
		"|> delete todo",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("printer dropped data-op shorthand %q\nfull output:\n%s", want, out)
		}
	}

	forbidden := []string{
		"query(todo,",
		"save(todo,",
		"fetch(todo,",
		"delete(todo)",
	}
	for _, bad := range forbidden {
		if strings.Contains(out, bad) {
			t.Errorf("printer emitted function-call form for data op (%q)\nfull output:\n%s", bad, out)
		}
	}

	// Idempotence: a second round-trip must produce identical output.
	file2, errs := parser.ParseFile("test.bp", []byte(out))
	if len(errs) > 0 {
		t.Fatalf("re-parse errors: %v\nprinted:\n%s", errs, out)
	}
	out2 := ast.Print(file2)
	if out != out2 {
		t.Errorf("data-op printing is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out, out2)
	}
}

func TestPrint_DataOpWithMarkers(t *testing.T) {
	// where(...) and order(...) markers should also stay in shorthand form,
	// alongside paginate(...) and the trailing `first` modifier.
	src := `blueprint "test" {
  version "1.0.0"
  port 8080
  runtime node
}

model job {
  id      uuid   primary
  status  string required
  created timestamp default(now)
}

GET /api/jobs {
  |> active = query job where(status == "active") order(created) first
  -> 200 { job: active }
}
`
	file := parseForPrint(t, src)
	out := ast.Print(file)

	// All markers must appear in shorthand order, no surrounding parens
	// around the whole call.
	wantSubstr := "query job where(status == \"active\") order(created) first"
	if !strings.Contains(out, wantSubstr) {
		t.Errorf("printer dropped marker shorthand\nwant substring: %s\nfull output:\n%s", wantSubstr, out)
	}
	if strings.Contains(out, "query(job,") {
		t.Errorf("printer emitted call-form for marker-bearing query\nfull output:\n%s", out)
	}
}

// TestPrint_RoundtripAllBlockTypes exercises every block type the printer
// handles — worker, schedule, stream, WS, try/recover, test, fixture, fn,
// middleware, external — via a parse -> print -> parse -> print idempotency
// check. This pins the printer's output for block types that previously had
// 0% test coverage.
func TestPrint_RoundtripAllBlockTypes(t *testing.T) {
	src := `blueprint "roundtrip-test" {
  version "1.0.0"
  port    3000
  runtime node
  database postgres
  queue    redis
}

secret DATABASE_URL required
secret REDIS_URL    required

model task {
  id     uuid   primary
  title  string required
  status string default("pending")
}

model log_entry {
  id      uuid       primary
  task_id uuid       ref(task)
  message string     required
  created timestamp  default(now)
}

fn process_task {
  <- task_id uuid

  -> string

  impl node {
    module: "./internal/process-task"
  }
}

middleware auth {
  before {
    |> guard header.Authorization -> 401 "Missing auth"
  }
}

worker cleanup_worker {
  trigger queue("cleanup")
  retry   3
  timeout 5min

  <- task_id uuid

  |> task = fetch task(task_id)
  |> guard task.status == "done" -> 409 "Already done"
  |> update task { status: "cleaning" }
  |> result = process_task(task_id)
  |> log "Cleaned task {task_id}"
  -> 200 { result: result }

  on_fail {
    |> log "Worker failed for {task_id}"
  }
}

schedule nightly_cleanup {
  cron "0 4 * * *"

  |> old = query task where(status == "done")
  |> delete old
  |> log "Cleaned {old.count} done tasks"
}

GET /api/tasks {
  <- status string default("pending")
  |> tasks = query task where(status == status)
  -> 200 { tasks: tasks }
}

POST /api/tasks {
  <- title string required
  |> task = save task { title: title }
  -> 201 { id: task.id }
}

POST /api/tasks/:id/process {
  <- id uuid required
  |> try {
    |> task = fetch task(id)
    |> guard task.status != "done" -> 409 "Already done"
    |> update task { status: "processing" }
  } recover {
    |> log "Failed to process {id}"
    -> 500 "Processing failed"
  }
  -> 200 { id: id }
}

STREAM /api/events {
  stream {
    |> on event(task_created) {
      |> log "Task created"
    }
    |> on timeout(30s) {
      |> log "Heartbeat"
    }
  }
}

WS /ws/tasks/:id {
  on_connect {
    |> log "Connected to task {id}"
  }
  on_message {
    |> broadcast room(id) { body: message.body }
  }
  on_disconnect {
    |> leave room(id)
  }
}

fixture "test-data" from "testdata/seed.json"

fixture "sample_task" seed task {
  title  "Sample task"
  status "pending"
}

test create_task {
  target POST /api/tasks

  setup {
    |> log "Setting up test"
  }

  request {
    body {
      title: "Test task",
    }
  }

  expect {
    status 201
    body.id is uuid
  }

  cleanup {
    |> log "Cleaning up test"
  }
}
`

	// First parse + print
	file1 := parseForPrint(t, src)
	printed1 := ast.Print(file1)

	// Second parse of the printed output + print again
	file2, errs2 := parser.ParseFile("test.bp", []byte(printed1))
	if len(errs2) > 0 {
		t.Fatalf("re-parse errors after first print: %v\nprinted:\n%s", errs2, printed1)
	}
	printed2 := ast.Print(file2)

	// The two printed outputs must be identical (idempotent).
	if printed1 != printed2 {
		lines1 := strings.Split(printed1, "\n")
		lines2 := strings.Split(printed2, "\n")
		maxLen := len(lines1)
		if len(lines2) > maxLen {
			maxLen = len(lines2)
		}
		firstDiff := -1
		for i := 0; i < maxLen; i++ {
			var l1, l2 string
			if i < len(lines1) {
				l1 = lines1[i]
			}
			if i < len(lines2) {
				l2 = lines2[i]
			}
			if l1 != l2 {
				firstDiff = i
				break
			}
		}
		t.Errorf("Print is not idempotent (first diff at line %d):\n--- first print ---\n%s\n--- second print ---\n%s", firstDiff, printed1, printed2)
	}
}

// TestPrint_UnaryNot guards against the silent data loss observed in v0.9:
// `not existing` round-tripped through `bp fmt` into `notexisting`, which
// the checker happily treated as a brand-new identifier.
func TestPrint_UnaryNot(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port 8080
  runtime node
}

model item {
  id   uuid   primary
  name string required
}

POST /api/items {
  <- name string required
  |> existing = query item where(name == name) first
  |> guard not existing -> 409 "exists"
  -> 201 "ok"
}
`
	file := parseForPrint(t, src)
	out := ast.Print(file)
	if !strings.Contains(out, "not existing") {
		t.Errorf("printer collapsed word-form unary op: missing 'not existing'\nfull output:\n%s", out)
	}
	if strings.Contains(out, "notexisting") {
		t.Errorf("printer produced the corrupted form 'notexisting'\nfull output:\n%s", out)
	}
}

// TestPrint_BlockExprIndent guards against multi-line BlockExpr emit at
// column 0 — the closing `}` should match the opening line's indent,
// otherwise downstream re-parses degrade visually and the file stops being
// idempotent under repeated bp fmt.
func TestPrint_BlockExprIndent(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port 8080
  runtime node
}

GET /api/x {
  <- id uuid required
  -> 200 {
    a: 1,
    b: 2,
    c: 3,
    d: 4,
  }
}
`
	file := parseForPrint(t, src)
	out := ast.Print(file)
	// The closing `}` of the multi-line block must be indented to align
	// with the `-> 200 {` line that opened it (two spaces in this fixture).
	if !strings.Contains(out, "\n  }\n") {
		t.Errorf("multi-line BlockExpr did not close at the caller's indent\nfull output:\n%s", out)
	}
	// And the entries themselves must be indented further than the parent.
	if !strings.Contains(out, "\n    a: 1,") {
		t.Errorf("multi-line BlockExpr entries not indented under the brace\nfull output:\n%s", out)
	}
	// Round-trip + idempotence on the printed output.
	file2, errs := parser.ParseFile("test.bp", []byte(out))
	if len(errs) > 0 {
		t.Fatalf("re-parse failed after indented block emit: %v\n%s", errs, out)
	}
	out2 := ast.Print(file2)
	if out != out2 {
		t.Errorf("indented block emit is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out, out2)
	}
}

// TestPrint_StreamShorthand guards inject/join/leave/broadcast/log emit so
// they reflect the source idiom rather than synthesising a function-call
// form that the parser rejects.
func TestPrint_StreamShorthand(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port 8080
  runtime node
}

model room {
  id   uuid   primary
  name string required
}

WS /ws/x {
  on_connect {
    |> inject payload.user as sender
    |> join room(id)
    |> log "joined" level(info)
    |> broadcast room(id) { type: "joined", sender: sender }
  }
  on_disconnect {
    |> leave room(id)
  }
}
`
	file := parseForPrint(t, src)
	out := ast.Print(file)

	mustContain := []string{
		"|> inject payload.user as sender",
		"|> join room(id)",
		`|> log "joined" level(info)`,
		"|> broadcast room(id) { type: \"joined\", sender: sender }",
		"|> leave room(id)",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("stream shorthand missing %q\nfull output:\n%s", want, out)
		}
	}
	forbidden := []string{
		"inject(payload.user",
		"join(room",
		"log(\"joined\"",
		"broadcast(room(id),",
		"leave(room",
	}
	for _, bad := range forbidden {
		if strings.Contains(out, bad) {
			t.Errorf("stream op emitted as plain FnCall (%q)\nfull output:\n%s", bad, out)
		}
	}

	// Round-trip: the printed output must parse cleanly.
	if _, errs := parser.ParseFile("test.bp", []byte(out)); len(errs) > 0 {
		t.Fatalf("printed stream ops failed to re-parse: %v\n%s", errs, out)
	}
}

// TestPrint_ImplInline keeps small `impl strategy { module: ..., func: ... }`
// blocks on a single line, mirroring the way they appear in
// examples/auth-service.bp.
func TestPrint_ImplInline(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port 8080
  runtime node
}

fn hash_password {
  <- password string required
  -> string
  impl node { module: "./internal/auth", func: "hashPassword" }
}
`
	file := parseForPrint(t, src)
	out := ast.Print(file)
	want := `impl node { module: "./internal/auth", func: "hashPassword" }`
	if !strings.Contains(out, want) {
		t.Errorf("small impl block was inflated to multi-line\nwant: %s\nfull output:\n%s", want, out)
	}
}

func TestPrint_Middleware(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}

middleware log_request {
  before {
    |> log "request started"
  }
  after {
    |> log "request ended"
  }
}
`
	file := parseForPrint(t, src)
	output := ast.Print(file)

	if !strings.Contains(output, "middleware log_request {") {
		t.Errorf("output missing middleware declaration: %s", output)
	}
	if !strings.Contains(output, "before {") {
		t.Errorf("output missing before block: %s", output)
	}
	if !strings.Contains(output, "after {") {
		t.Errorf("output missing after block: %s", output)
	}
}
