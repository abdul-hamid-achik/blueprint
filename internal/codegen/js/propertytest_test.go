package js

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func buildWithPropertyTests(t *testing.T, src string) string {
	t.Helper()
	file, errs := parser.ParseFile("properties.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := New().WithPropertyTests(true).Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	return outDir
}

func TestGen_PropertyTests_ImplyContractHarnessAndFastCheck(t *testing.T) {
	outDir := buildWithPropertyTests(t, autoTestTodoSrc)

	property := readGen(t, outDir, "test/generated/todos.property.test.ts")
	for _, want := range []string{
		"import fc from 'fast-check'",
		"vi.mock('../../src/lib/db'",
		"await resetDb();",
		"fc.asyncProperty(",
		"fc.string({ minLength: 0, maxLength: 64 })",
		"fc.uuid()",
		"numRuns: 32",
		"endOnFailure: true",
		"expect([201]).toContain(response.status)",
		"expect([200, 404]).toContain(response.status)",
	} {
		if !strings.Contains(property, want) {
			t.Errorf("property test missing %q, got:\n%s", want, property)
		}
	}
	if strings.Contains(property, "expect([201, 500])") {
		t.Errorf("property suite must not treat an undeclared global 500 as success, got:\n%s", property)
	}
	if _, err := os.Stat(filepath.Join(outDir, "test/generated/todos.test.ts")); err != nil {
		t.Errorf("property mode must imply the existing contract suite: %v", err)
	}
	pkg := readGen(t, outDir, "package.json")
	for _, dep := range []string{`"fast-check": "^3.23.2"`, `"@electric-sql/pglite"`} {
		if !strings.Contains(pkg, dep) {
			t.Errorf("property package missing %s, got:\n%s", dep, pkg)
		}
	}
}

func TestGen_PropertyTests_DeterministicOutput(t *testing.T) {
	first := buildWithPropertyTests(t, autoTestTodoSrc)
	second := buildWithPropertyTests(t, autoTestTodoSrc)
	a := readGen(t, first, "test/generated/todos.property.test.ts")
	b := readGen(t, second, "test/generated/todos.property.test.ts")
	if a != b {
		t.Fatal("property test output, including seeds, must be deterministic")
	}
}

func TestGen_PropertyTests_ContractOnlyModeUnchanged(t *testing.T) {
	outDir := buildWithGenTests(t, autoTestTodoSrc)
	if _, err := os.Stat(filepath.Join(outDir, "test/generated/todos.property.test.ts")); !os.IsNotExist(err) {
		t.Error("WithGenTests alone must remain contract-only")
	}
	pkg := readGen(t, outDir, "package.json")
	if strings.Contains(pkg, "fast-check") {
		t.Errorf("contract-only mode must not add fast-check, got:\n%s", pkg)
	}
}

func TestGen_PropertyTests_DatabaseFreeEndpoint(t *testing.T) {
	src := `blueprint "hello" {
  version "1.0.0"
  port 3000
  runtime node
}

GET /api/hello/:name {
  <- name string required min(0) max(20)
  -> 200 { message: "Hello, {name}!" }
}
`
	outDir := buildWithPropertyTests(t, src)
	property := readGen(t, outDir, "test/generated/hello.property.test.ts")
	if strings.Contains(property, "src/lib/db") || strings.Contains(property, "resetDb") {
		t.Errorf("database-free property must not import the db harness, got:\n%s", property)
	}
	for _, want := range []string{
		"fc.string({ minLength: 1, maxLength: 20 })",
		`path = path.replace(":name", encodeURIComponent(String(input.name)))`,
		"expect([200]).toContain(response.status)",
	} {
		if !strings.Contains(property, want) {
			t.Errorf("database-free property missing %q, got:\n%s", want, property)
		}
	}
	pkg := readGen(t, outDir, "package.json")
	if !strings.Contains(pkg, "fast-check") || strings.Contains(pkg, "@electric-sql/pglite") {
		t.Errorf("database-free property should need fast-check but not PGlite, got:\n%s", pkg)
	}
}

func TestGen_PropertyTests_RejectImpossiblePathDomain(t *testing.T) {
	tests := []struct {
		name        string
		constraints string
	}{
		{name: "max zero", constraints: "max(0)"},
		{name: "explicit empty only", constraints: "min(0) max(0)"},
		{name: "explicit empty only reversed", constraints: "max(0) min(0)"},
		{name: "reversed impossible bounds", constraints: "max(10) min(100)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := `blueprint "impossible-path" { version "1.0.0" port 3000 runtime node }
GET /api/items/:slug {
  <- slug string required ` + tc.constraints + `
  -> 200 { slug: slug }
}
`
			file, errs := parser.ParseFile(tc.name+".bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			files, err := New().WithPropertyTests(true).Files(file)
			if err == nil || !strings.Contains(err.Error(), "invalid length bounds") {
				t.Fatalf("expected impossible transport domain to fail closed, got files=%d err=%v", len(files), err)
			}
			if len(files) != 0 {
				t.Fatalf("failed property generation must return no files, got %d", len(files))
			}
		})
	}
}

func TestPropertyBoundsAggregateExplicitConstraints(t *testing.T) {
	constraints := []*ast.Constraint_{
		{Kind: "max", Value: &ast.IntLit{Value: "10"}},
		{Kind: "min", Value: &ast.IntLit{Value: "100"}},
		{Kind: "max", Value: &ast.IntLit{Value: "20"}},
		{Kind: "min", Value: &ast.IntLit{Value: "50"}},
	}

	if _, _, err := propertyLengthBounds(constraints, 0, 64); err == nil || !strings.Contains(err.Error(), "min=100 max=10") {
		t.Fatalf("length bounds must preserve the strongest explicit constraints, got %v", err)
	}
	if _, _, err := propertyIntegerBounds(constraints, -1000, 1000); err == nil || !strings.Contains(err.Error(), "min=100 max=10") {
		t.Fatalf("integer bounds must preserve the strongest explicit constraints, got %v", err)
	}
	if _, _, err := propertyNumberBounds(constraints, -1000, 1000); err == nil || !strings.Contains(err.Error(), "min=100 max=10") {
		t.Fatalf("number bounds must preserve the strongest explicit constraints, got %v", err)
	}
}

func TestGen_PropertyTests_RejectImpossibleFormattedStringDomains(t *testing.T) {
	tests := []struct {
		name   string
		format string
		max    string
		want   string
	}{
		{name: "email", format: "email", max: "5", want: "minimum length is 6"},
		{name: "url", format: "url", max: "10", want: "minimum length is 11"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := `blueprint "impossible-format" { version "1.0.0" port 3000 runtime node }
POST /api/items {
  <- value string required format(` + tc.format + `) max(` + tc.max + `)
  -> 200 { value: value }
}
`
			file, errs := parser.ParseFile(tc.name+".bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			files, err := New().WithPropertyTests(true).Files(file)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected impossible %s domain error containing %q, got files=%d err=%v", tc.format, tc.want, len(files), err)
			}
			if len(files) != 0 {
				t.Fatalf("failed property generation must return no files, got %d", len(files))
			}
		})
	}
}

func TestGen_PropertyTests_EmissionReturnsUnsupportedArbitraryError(t *testing.T) {
	ep := &ast.Endpoint{
		Method: "POST",
		Path:   "/api/upload",
		Stmts: []ast.ArrowStmt{
			&ast.InputStmt{Name: "attachment", Type: &ast.PrimitiveType{Name: "file"}},
		},
	}
	file, err := New().genPropertyTestFile("upload", []*ast.Endpoint{ep}, false, newPropertyTypeIndex(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "multipart transport") {
		t.Fatalf("expected emission to return the unsupported arbitrary error, got file=%q err=%v", file.Path, err)
	}
	if file.Path != "" || len(file.Content) != 0 {
		t.Fatalf("failed emission must not return a fallback property file, got %#v", file)
	}
}

func TestGen_PropertyTests_NamedTypeAliasAndEnumArbitraries(t *testing.T) {
	src := `blueprint "typed-properties" {
  version "1.0.0"
  port 3000
  runtime node
}

type Profile {
  display_name string required min(1) max(24)
  active bool optional
}

alias Score = int min(0) max(10)

enum Role {
  admin
  member
}

POST /api/profiles {
  <- profile Profile required
  <- score Score required
  <- role Role required
  <- tags list(string) optional max(3)
  -> 200 { profile: profile, score: score, role: role, tags: tags }
}
`
	outDir := buildWithPropertyTests(t, src)
	property := readGen(t, outDir, "test/generated/profiles.property.test.ts")
	for _, want := range []string{
		"profile: fc.record({ displayName: fc.string({ minLength: 1, maxLength: 24 }), active: fc.option(fc.boolean(), { nil: undefined }) })",
		"score: fc.integer({ min: 0, max: 10 })",
		`role: fc.constantFrom("admin", "member")`,
		"tags: fc.option(fc.array(fc.string({ minLength: 0, maxLength: 64 }), { minLength: 0, maxLength: 3 }), { nil: undefined })",
	} {
		if !strings.Contains(property, want) {
			t.Errorf("typed property missing %q, got:\n%s", want, property)
		}
	}
}

func TestGen_PropertyTests_FailClosedForUnsupportedInput(t *testing.T) {
	src := `blueprint "upload" {
  version "1.0.0"
  port 3000
  runtime node
}

POST /api/upload {
  <- attachment file required
  -> 200 { ok: true }
}
`
	file, errs := parser.ParseFile("upload.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	files, err := New().WithPropertyTests(true).Files(file)
	if err == nil {
		t.Fatal("expected file input to fail closed")
	}
	if len(files) != 0 {
		t.Fatalf("failed property generation must return no files, got %d", len(files))
	}
	for _, want := range []string{"attachment", "file inputs", "multipart"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestGen_PropertyTests_FailClosedForNonHermeticEndpoint(t *testing.T) {
	src := `blueprint "external-property" {
  version "1.0.0"
  port 3000
  runtime node
}

external "service" { url: "https://example.com" }

GET /api/value {
  |> value = call service GET /value
  -> 200 { value: value }
}
`
	file, errs := parser.ParseFile("external-property.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	files, err := New().WithPropertyTests(true).Files(file)
	if err == nil || !strings.Contains(err.Error(), "external HTTP calls are non-hermetic") {
		t.Fatalf("expected focused non-hermetic error, got files=%d err=%v", len(files), err)
	}
	if len(files) != 0 {
		t.Fatalf("failed property generation must return no files, got %d", len(files))
	}
}

func TestGen_PropertyTests_RejectRefBackedWritesWithoutParentSeed(t *testing.T) {
	src := `blueprint "foreign-key-property" {
  version "1.0.0"
  port 3000
  runtime node
  database postgres
}

model author {
  id uuid primary
}

model post {
  id uuid primary
  author_id uuid required ref(author)
}

POST /api/posts {
  <- author_id uuid required
  |> post = save post { author_id: author_id }
  -> 201 { id: post.id }
}
`
	file, errs := parser.ParseFile("foreign-key-property.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	files, err := New().WithPropertyTests(true).Files(file)
	if err == nil {
		t.Fatalf("expected ref-backed write to fail closed, got %d files", len(files))
	}
	for _, want := range []string{`ref-backed field "author_id"`, `model "post"`, "do not seed referenced"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if len(files) != 0 {
		t.Fatalf("failed property generation must return no files, got %d", len(files))
	}
}

func TestGen_PropertyTests_RejectRecursiveInlineCallGraphs(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "function",
			src: `blueprint "recursive-function" { version "1.0.0" port 3000 runtime node }
fn recurse {
  <- value int
  -> int
  logic {
    |> result = recurse(value)
    -> result
  }
}
POST /api/recurse {
  <- value int required
  |> result = recurse(value)
  -> 200 { result: result }
}
`,
			want: "recursive inline function call graph",
		},
		{
			name: "pipe",
			src: `blueprint "recursive-pipe" { version "1.0.0" port 3000 runtime node }
pipe recurse {
  <- value string
  |> result = pipe recurse(value)
  -> result
}
POST /api/recurse {
  <- value string required
  |> result = pipe recurse(value)
  -> 200 { result: result }
}
`,
			want: "recursive inline pipe call graph",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, errs := parser.ParseFile(tc.name+".bp", []byte(tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			files, err := New().WithPropertyTests(true).Files(file)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected recursive graph error containing %q, got files=%d err=%v", tc.want, len(files), err)
			}
			if len(files) != 0 {
				t.Fatalf("failed property generation must return no files, got %d", len(files))
			}
		})
	}
}

func TestGen_PropertyTests_FailClosedBoundaries(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "structural query",
			src: `blueprint "struct-query" { version "1.0.0" port 3000 runtime node }
type Filter { q string required }
GET /api/search {
  <- filter Filter required
  -> 200 { ok: true }
}
`,
			want: "query fuzzing is limited to scalar and enum values",
		},
		{
			name: "native implementation",
			src: `blueprint "native-property" { version "1.0.0" port 3000 runtime node }
fn transform {
  <- input string
  -> string
  impl node { module: "./internal/transform" }
}
POST /api/transform {
  <- input string required
  |> result = transform(input)
  -> 200 { result: result }
}
`,
			want: "not proven hermetic",
		},
		{
			name: "auth metadata",
			src: `blueprint "auth-property" { version "1.0.0" port 3000 runtime node }
secret SIGNING_KEY required
POST /api/webhook {
  auth webhook_sig using(secret.SIGNING_KEY)
  <- event json required
  -> 200 { ok: true }
}
`,
			want: "credential-aware request arbitraries",
		},
		{
			name: "header access",
			src: `blueprint "header-property" { version "1.0.0" port 3000 runtime node }
GET /api/whoami {
  -> 200 { authorization: header.Authorization }
}
`,
			want: "header-aware request arbitraries",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, errs := parser.ParseFile(tc.name+".bp", []byte(tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			files, err := New().WithPropertyTests(true).Files(file)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got files=%d err=%v", tc.want, len(files), err)
			}
			if len(files) != 0 {
				t.Fatalf("failed property generation must return no files, got %d", len(files))
			}
		})
	}
}

func TestGen_PropertyTests_RejectFrontendOnly(t *testing.T) {
	file, errs := parser.ParseFile("test.bp", []byte(autoTestTodoSrc))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	files, err := New().WithFrontendOnly(true).WithPropertyTests(true).Files(file)
	if err == nil || !strings.Contains(err.Error(), "runnable Node service") {
		t.Fatalf("expected frontend-only property rejection, got files=%d err=%v", len(files), err)
	}
}
