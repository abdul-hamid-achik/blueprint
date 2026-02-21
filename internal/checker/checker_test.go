package checker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// --- Helpers ---

func check(t *testing.T, src string) []CheckError {
	t.Helper()
	file, parseErrors := parser.ParseFile("test.bp", []byte(src))
	if len(parseErrors) > 0 {
		var msgs []string
		for _, e := range parseErrors {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("unexpected parse errors:\n%s", strings.Join(msgs, "\n"))
	}
	return Check(file)
}

func expectErrors(t *testing.T, errors []CheckError, count int) {
	t.Helper()
	if len(errors) != count {
		var msgs []string
		for _, e := range errors {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("expected %d error(s), got %d:\n%s", count, len(errors), strings.Join(msgs, "\n"))
	}
}

func expectNoErrors(t *testing.T, errors []CheckError) {
	t.Helper()
	expectErrors(t, errors, 0)
}

func expectErrorContaining(t *testing.T, errors []CheckError, substr string) {
	t.Helper()
	for _, e := range errors {
		if strings.Contains(e.Message, substr) {
			return
		}
	}
	var msgs []string
	for _, e := range errors {
		msgs = append(msgs, e.Message)
	}
	t.Fatalf("expected error containing %q, got:\n%s", substr, strings.Join(msgs, "\n"))
}

const header = `blueprint "test" {
  version "1.0"
  port    3000
  runtime node
}
`

const headerWithDB = `blueprint "test" {
  version  "1.0"
  port     3000
  runtime  node
  database postgres
}
`

// ═══════════════════════════════════════════════
// Name Uniqueness Tests (1-6)
// ═══════════════════════════════════════════════

func TestDuplicateModelNames(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id uuid primary
}
model user {
  id uuid primary
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate model")
}

func TestDuplicateFnNames(t *testing.T) {
	errs := check(t, header+`
fn process {
  <- data string
  impl node { file: "process.js" }
}
fn process {
  <- data string
  impl node { file: "process.js" }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate fn")
}

func TestDuplicatePipeNames(t *testing.T) {
	errs := check(t, header+`
pipe validate {
  <- name string
  -> name
}
pipe validate {
  <- name string
  -> name
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate pipe")
}

func TestDuplicateMiddlewareNames(t *testing.T) {
	errs := check(t, header+`
middleware auth {
  before {
    |> log "checking auth"
  }
}
middleware auth {
  before {
    |> log "checking auth"
  }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate middleware")
}

func TestDuplicateEnumNames(t *testing.T) {
	errs := check(t, header+`
enum Status {
  active
  inactive
}
enum Status {
  pending
  done
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate enum")
}

func TestDuplicateSecretNames(t *testing.T) {
	errs := check(t, header+`
secret API_KEY required
secret API_KEY required
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate secret")
}

// ═══════════════════════════════════════════════
// Naming Convention: snake_case (7-12)
// ═══════════════════════════════════════════════

func TestModelNameNotSnakeCase(t *testing.T) {
	errs := check(t, headerWithDB+`
model MyModel {
  id uuid primary
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestFieldNameNotSnakeCase(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id       uuid   primary
  userName string required
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestPipeNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
pipe ValidateName {
  <- name string
  -> name
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestFnNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
fn ProcessData {
  <- data string
  impl node { file: "process.js" }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestWorkerNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
worker ProcessQueue {
  trigger queue("jobs")
  |> log "processing"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestScheduleNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
schedule DailyCleanup {
  cron "0 0 * * *"
  |> log "cleaning"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

// ═══════════════════════════════════════════════
// Naming Convention: SCREAMING_SNAKE_CASE (13-14)
// ═══════════════════════════════════════════════

func TestSecretNotScreamingSnakeCase(t *testing.T) {
	errs := check(t, header+`
secret apiKey required
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be SCREAMING_SNAKE_CASE")
}

func TestEnvNotScreamingSnakeCase(t *testing.T) {
	errs := check(t, header+`
env maxSize 10mb
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be SCREAMING_SNAKE_CASE")
}

// ═══════════════════════════════════════════════
// Naming Convention: PascalCase (15-17)
// ═══════════════════════════════════════════════

func TestEnumNotPascalCase(t *testing.T) {
	errs := check(t, header+`
enum status {
  active
  inactive
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be PascalCase")
}

func TestTypeNotPascalCase(t *testing.T) {
	errs := check(t, header+`
type image_file {
  url    string
  width  int
  height int
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be PascalCase")
}

func TestAliasNotPascalCase(t *testing.T) {
	errs := check(t, header+`
alias email = string format(email)
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be PascalCase")
}

// ═══════════════════════════════════════════════
// Structural Validation (18-22)
// ═══════════════════════════════════════════════

func TestMissingBlueprintBlock(t *testing.T) {
	// Parse a file that has no blueprint block
	src := `model item {
  id uuid primary
}
`
	file, _ := parser.ParseFile("test.bp", []byte(src))
	errs := Check(file)
	expectErrorContaining(t, errs, "missing blueprint block")
}

func TestArrowOrderingInputAfterStep(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  |> log "step"
  <- name string required
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "input (<-) must come before")
}

func TestArrowOrderingInputAfterOutput(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  -> 200 "ok"
  <- name string required
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "input (<-) must come before")
}

func TestArrowOrderingStepAfterOutput(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  <- name string required
  -> 200 "ok"
  |> log "too late"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "steps (|>) must come before outputs")
}

func TestNestedTryRecover(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  |> try {
    |> try {
      |> log "nested"
    } recover {
      |> log "inner recover"
    }
  } recover {
    |> log "outer recover"
  }
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "try/recover cannot be nested")
}

// ═══════════════════════════════════════════════
// Reference Validation (23-27)
// ═══════════════════════════════════════════════

func TestUnknownTypeRef(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id    uuid      primary
  data  UnknownType required
}
`)
	expectErrorContaining(t, errs, `unknown type "UnknownType"`)
}

func TestUnknownModelRef(t *testing.T) {
	errs := check(t, headerWithDB+`
model post {
  id      uuid primary
  user_id uuid ref(user)
}
`)
	expectErrorContaining(t, errs, `ref references unknown model "user"`)
}

func TestValidModelRef(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id uuid primary
}
model post {
  id      uuid primary
  user_id uuid ref(user)
}
`)
	expectNoErrors(t, errs)
}

func TestTestGroupReferencesUnknownTest(t *testing.T) {
	errs := check(t, header+`
test_group all_tests {
  tests [nonexistent_test]
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `test_group references unknown test "nonexistent_test"`)
}

func TestMiddlewareRefWrongKind(t *testing.T) {
	errs := check(t, headerWithDB+`
model auth {
  id uuid primary
}
POST /api/test {
  use auth
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `"auth" is a model, not a middleware`)
}

// ═══════════════════════════════════════════════
// Valid Files — No Errors Expected (28-34)
// ═══════════════════════════════════════════════

func TestValidMinimalFile(t *testing.T) {
	errs := check(t, header)
	expectNoErrors(t, errs)
}

func TestValidModelWithPrimitives(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id      uuid      primary
  name    string    required
  count   int       default(0)
  price   float     optional
  active  bool      default(true)
  created timestamp default(now)
  data    json      optional
}
`)
	expectNoErrors(t, errs)
}

func TestValidEndpointWithArrows(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id   uuid   primary
  name string required
}
POST /api/items {
  <- name string required
  |> log "creating item"
  -> 201 { name: name }
}
`)
	expectNoErrors(t, errs)
}

func TestValidPipe(t *testing.T) {
	errs := check(t, header+`
pipe validate_name {
  <- name string
  |> guard name != "" -> 400 "Name required"
  -> name
}
`)
	expectNoErrors(t, errs)
}

func TestValidEnumAndAlias(t *testing.T) {
	errs := check(t, header+`
enum Status {
  active
  inactive
}
alias Email = string format(email)
`)
	expectNoErrors(t, errs)
}

func TestValidSecretAndEnv(t *testing.T) {
	errs := check(t, header+`
secret API_KEY required
secret DB_URL  required
env MAX_SIZE   10mb
env LOG_LEVEL  "info"
`)
	expectNoErrors(t, errs)
}

func TestValidMiddleware(t *testing.T) {
	errs := check(t, header+`
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Missing auth"
  }
}
`)
	expectNoErrors(t, errs)
}

// ═══════════════════════════════════════════════
// Edge Cases (35-40)
// ═══════════════════════════════════════════════

func TestSingleWordSnakeCase(t *testing.T) {
	// Single word names are valid snake_case
	errs := check(t, headerWithDB+`
model item {
  id   uuid   primary
  name string required
}
`)
	expectNoErrors(t, errs)
}

func TestSingleWordScreamingSnakeCase(t *testing.T) {
	// Single uppercase word is valid SCREAMING_SNAKE_CASE
	errs := check(t, header+`
secret TOKEN required
env PORT 3000
`)
	expectNoErrors(t, errs)
}

func TestMultipleErrors(t *testing.T) {
	errs := check(t, headerWithDB+`
model MyModel {
  id       uuid   primary
  UserName string required
}
`)
	// Should get errors for both model name and field name
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 errors, got %d", len(errs))
	}
}

func TestArrowOrderingInPipe(t *testing.T) {
	errs := check(t, header+`
pipe bad_pipe {
  |> log "step first"
  <- name string
  -> name
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "input (<-) must come before")
}

func TestValidTypeWithFields(t *testing.T) {
	errs := check(t, header+`
type ImageFile {
  url    string
  width  int
  height int
}
`)
	expectNoErrors(t, errs)
}

func TestTypeFieldNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
type BadType {
  url       string
  imageSize int
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestPathParameterNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
GET /api/items/:itemId {
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "path parameter")
}

func TestValidPathParameter(t *testing.T) {
	errs := check(t, header+`
GET /api/items/:item_id {
  -> 200 "ok"
}
`)
	expectNoErrors(t, errs)
}

// ═══════════════════════════════════════════════
// File-Based Tests — Valid Fixtures (41)
// ═══════════════════════════════════════════════

func TestValidFixturesPassChecker(t *testing.T) {
	files, err := filepath.Glob("../../testdata/valid/*.bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no valid fixtures found")
	}

	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			file, parseErrors := parser.ParseFile(name, src)
			if len(parseErrors) > 0 {
				t.Skipf("parse errors (not a checker concern): %v", parseErrors)
			}

			checkErrors := Check(file)
			if len(checkErrors) > 0 {
				var msgs []string
				for _, e := range checkErrors {
					msgs = append(msgs, e.Error())
				}
				t.Errorf("checker errors in valid fixture:\n%s", strings.Join(msgs, "\n"))
			}
		})
	}
}

func TestInvalidFixturesFailChecker(t *testing.T) {
	// Files that should produce checker (semantic) errors.
	checkerFiles := map[string]bool{
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
	}

	files, err := filepath.Glob("../../testdata/invalid/*.bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no invalid fixtures found")
	}

	for _, f := range files {
		name := filepath.Base(f)
		if !checkerFiles[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			file, parseErrors := parser.ParseFile(name, src)
			if len(parseErrors) > 0 {
				t.Skipf("has parser errors (not checker-only): %v", parseErrors)
			}

			checkErrors := Check(file)
			if len(checkErrors) == 0 {
				t.Fatalf("%s: expected checker errors, got none", name)
			}
		})
	}
}

// ═══════════════════════════════════════════════
// Error Formatting (42)
// ═══════════════════════════════════════════════

func TestFormatCheckError(t *testing.T) {
	src := []byte("model Bad {\n  id uuid primary\n}\n")
	err := CheckError{
		Loc:     lexer.Loc{File: "test.bp", Line: 1, Col: 7},
		Message: `model name "Bad" must be snake_case`,
		Hint:    "Use lowercase letters",
	}
	formatted := FormatCheckError(err, src)
	if !strings.Contains(formatted, "model Bad") {
		t.Errorf("expected source line in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "snake_case") {
		t.Errorf("expected error message in output, got:\n%s", formatted)
	}
}

// ═══════════════════════════════════════════════
// Scope Tests (43-44)
// ═══════════════════════════════════════════════

func TestScopeDefineAndLookup(t *testing.T) {
	scope := NewScope(nil)
	sym := &Symbol{Name: "foo", Kind: SymModel}
	if existing := scope.Define(sym); existing != nil {
		t.Fatal("expected no existing symbol")
	}
	if found := scope.Lookup("foo"); found == nil {
		t.Fatal("expected to find symbol")
	}
	if found := scope.Lookup("bar"); found != nil {
		t.Fatal("expected nil for undefined symbol")
	}
}

func TestScopeParentLookup(t *testing.T) {
	parent := NewScope(nil)
	parent.Define(&Symbol{Name: "global_var", Kind: SymModel})

	child := NewScope(parent)
	child.Define(&Symbol{Name: "local_var", Kind: SymVariable})

	// Child can see parent symbols
	if found := child.Lookup("global_var"); found == nil {
		t.Fatal("child should find parent symbol")
	}
	// Child can see own symbols
	if found := child.Lookup("local_var"); found == nil {
		t.Fatal("child should find own symbol")
	}
	// Parent can't see child symbols
	if found := parent.Lookup("local_var"); found != nil {
		t.Fatal("parent should not find child symbol")
	}
}

func TestScopeDuplicateDefine(t *testing.T) {
	scope := NewScope(nil)
	sym1 := &Symbol{Name: "foo", Kind: SymModel}
	sym2 := &Symbol{Name: "foo", Kind: SymFn}

	if existing := scope.Define(sym1); existing != nil {
		t.Fatal("first define should succeed")
	}
	if existing := scope.Define(sym2); existing == nil {
		t.Fatal("second define should return existing")
	}
}

// ═══════════════════════════════════════════════
// Naming Helper Unit Tests (45-47)
// ═══════════════════════════════════════════════

func TestIsSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"hello", true},
		{"hello_world", true},
		{"a", true},
		{"a1", true},
		{"my_var_2", true},
		{"", false},
		{"Hello", false},
		{"hello_World", false},
		{"_hello", false},
		{"hello_", false},
		{"hello__world", false},
		{"hello-world", false},
	}
	for _, tt := range tests {
		if got := isSnakeCase(tt.input); got != tt.want {
			t.Errorf("isSnakeCase(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsScreamingSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"HELLO", true},
		{"HELLO_WORLD", true},
		{"A", true},
		{"API_KEY_2", true},
		{"", false},
		{"hello", false},
		{"Hello", false},
		{"HELLO_", false},
		{"_HELLO", false},
		{"HELLO__WORLD", false},
		{"HELLO-WORLD", false},
	}
	for _, tt := range tests {
		if got := isScreamingSnakeCase(tt.input); got != tt.want {
			t.Errorf("isScreamingSnakeCase(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsPascalCase(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Hello", true},
		{"HelloWorld", true},
		{"Plan", true},
		{"ImageFile", true},
		{"Url", true},
		{"", false},
		{"hello", false},
		{"HELLO", false},
		{"hello_world", false},
		{"Hello_World", false},
		{"H", false}, // no lowercase
	}
	for _, tt := range tests {
		if got := isPascalCase(tt.input); got != tt.want {
			t.Errorf("isPascalCase(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

