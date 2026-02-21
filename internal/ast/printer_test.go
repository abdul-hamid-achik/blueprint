package ast_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

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
		`port 3000`,
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
		"id uuid primary",
		"name string required",
		"email string unique",
		"age int optional",
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
	if !strings.Contains(printed1, `version "2.3.1"`) {
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
