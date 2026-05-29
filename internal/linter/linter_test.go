package linter_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/linter"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const minimalHeader = `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}
`

func TestLint_Clean(t *testing.T) {
	src := minimalHeader + `
model item {
  id   uuid   primary
  name string required
}

@ "List all items"
GET /api/items {
  -> 200 "ok"
}

@ "Get a single item"
GET /api/items/:id {
  <- id uuid required
  -> 200 { id: id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for clean file, got %d:", len(issues))
		for _, iss := range issues {
			t.Errorf("  %s", iss)
		}
	}
}

func TestLint_MissingIntent(t *testing.T) {
	src := minimalHeader + `
GET /api/items {
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "intent-on-endpoints" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
		}
	}
	if !found {
		t.Errorf("expected at least one issue with rule 'intent-on-endpoints', got: %v", issues)
	}
}

func TestLint_MissingIntent_Multiple(t *testing.T) {
	src := minimalHeader + `
GET /api/items {
  -> 200 "ok"
}

POST /api/items {
  <- name string required
  -> 201 "created"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	count := 0
	for _, iss := range issues {
		if iss.Rule == "intent-on-endpoints" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 intent-on-endpoints issues (one per endpoint), got %d", count)
	}
}

func TestLint_BlockOrdering(t *testing.T) {
	// Model placed after endpoint violates canonical ordering.
	src := minimalHeader + `
@ "List items"
GET /api/items {
  -> 200 "ok"
}

model item {
  id   uuid   primary
  name string required
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "block-ordering" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
		}
	}
	if !found {
		t.Errorf("expected at least one issue with rule 'block-ordering', got: %v", issues)
	}
}

func TestLint_EmptyEndpoint(t *testing.T) {
	// An endpoint with no inputs AND no stmts should trigger empty-endpoint.
	// We need to work around the "no stmts" constraint — an endpoint with only
	// the closing brace is truly empty.
	src := minimalHeader + `
@ "Empty endpoint"
GET /api/empty {
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "empty-endpoint" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
		}
	}
	if !found {
		t.Errorf("expected at least one issue with rule 'empty-endpoint', got: %v", issues)
	}
}

func TestLint_EmptyEndpoint_WithOutput_NotEmpty(t *testing.T) {
	// An endpoint with only an output stmt has no inputs but has stmts — NOT empty.
	src := minimalHeader + `
@ "Health check"
GET /api/health {
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "empty-endpoint" {
			t.Errorf("endpoint with output stmt should not be flagged as empty, but got: %s", iss)
		}
	}
}

func TestLint_MultipleIssues(t *testing.T) {
	// Combine: missing intent + block ordering (model after endpoint) + empty endpoint.
	src := minimalHeader + `
GET /api/items {
  -> 200 "ok"
}

GET /api/empty {
}

model item {
  id   uuid   primary
  name string required
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	if len(issues) < 3 {
		t.Errorf("expected at least 3 issues (missing intent x2, block-ordering x1, empty-endpoint x1), got %d:", len(issues))
		for _, iss := range issues {
			t.Errorf("  %s", iss)
		}
	}

	rules := map[string]int{}
	for _, iss := range issues {
		rules[iss.Rule]++
	}
	if rules["intent-on-endpoints"] < 2 {
		t.Errorf("expected at least 2 intent-on-endpoints issues, got %d", rules["intent-on-endpoints"])
	}
	if rules["block-ordering"] < 1 {
		t.Errorf("expected at least 1 block-ordering issue, got %d", rules["block-ordering"])
	}
	if rules["empty-endpoint"] < 1 {
		t.Errorf("expected at least 1 empty-endpoint issue, got %d", rules["empty-endpoint"])
	}
}

func TestLint_IssueString(t *testing.T) {
	issue := linter.Issue{
		File:    "test.bp",
		Line:    10,
		Col:     1,
		Level:   "warning",
		Rule:    "intent-on-endpoints",
		Message: "Endpoint GET /api/items is missing an @ intent description",
	}
	s := issue.String()
	if s == "" {
		t.Error("Issue.String() should not be empty")
	}
	// Should contain file, line, level, rule, message
	for _, want := range []string{"test.bp", "10", "warning", "intent-on-endpoints"} {
		found := false
		for i := 0; i+len(want) <= len(s); i++ {
			if s[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Issue.String() = %q, expected it to contain %q", s, want)
		}
	}
}

func TestLint_IntentAnnotated_NoIssue(t *testing.T) {
	// Endpoint WITH intent should produce 0 intent-on-endpoints issues.
	src := minimalHeader + `
@ "Returns health status of the service"
GET /api/health {
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "intent-on-endpoints" {
			t.Errorf("endpoint with intent should not produce intent-on-endpoints issue, got: %s", iss)
		}
	}
}

func TestLint_WherePredicateSelfEqual_NoMatchingInput(t *testing.T) {
	// `where(name == name)` with NO input named `name` in scope — the
	// predicate is genuinely column-against-itself and ambiguous.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Buggy search"
GET /api/widgets {
  |> all = query widget where(name == name)
  -> 200 { items: all }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "where-predicate-self-equal" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
			if iss.Hint == "" {
				t.Errorf("expected non-empty Hint, got %q", iss.Hint)
			}
		}
	}
	if !found {
		t.Errorf("expected where-predicate-self-equal issue, got: %v", issues)
	}
}

func TestLint_WherePredicateSelfEqual_WithMatchingInput_NoIssue(t *testing.T) {
	// `where(name == name)` WITH input `name` — codegen convention applies
	// (left=column, right=input variable). Don't flag.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Lookup by name"
POST /api/widgets {
  <- name string required
  |> existing = query widget where(name == name) first
  |> guard not existing -> 409 "Already exists"
  |> w = save widget { name: name }
  -> 201 { id: w.id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "where-predicate-self-equal" {
			t.Errorf("predicate with matching input should not flag, got: %s", iss)
		}
	}
}

func TestLint_WherePredicateSelfEqual_PathParam_NoIssue(t *testing.T) {
	// URL path param `:id` binds `id`; `where(id == id)` should NOT flag.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Get widget"
GET /api/widgets/:id {
  |> w = query widget where(id == id) first
  -> 200 { id: w.id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "where-predicate-self-equal" {
			t.Errorf("path-param binding should disambiguate predicate, got: %s", iss)
		}
	}
}

func TestLint_WherePredicateSelfEqual_StepBinding_NoIssue(t *testing.T) {
	// A prior step `|> name = ...` binds `name`; `where(name == name)` should not flag.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Compute and lookup"
GET /api/widgets/lookup {
  |> name = "alpha"
  |> w = query widget where(name == name) first
  -> 200 { id: w.id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "where-predicate-self-equal" {
			t.Errorf("step-binding should disambiguate predicate, got: %s", iss)
		}
	}
}

func TestLint_UnusedInput(t *testing.T) {
	// `unused` is declared but never referenced.
	src := minimalHeader + `
@ "Has unused input"
GET /api/items {
  <- unused string required
  <- limit  int default(10)
  -> 200 { items: [], limit: limit }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var foundUnused bool
	var foundLimit bool
	for _, iss := range issues {
		if iss.Rule == "unused-input" {
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
			if iss.Message == "" || iss.Hint == "" {
				t.Errorf("expected non-empty Message and Hint, got Message=%q Hint=%q", iss.Message, iss.Hint)
			}
			// Find which input it is.
			if iss.Line == 0 {
				continue
			}
			// The unused input is at line 14 (header is 5 lines + blank + 2 lines).
			// Rather than hardcoding lines, check by message content.
			switch {
			case strings.Contains(iss.Message, `"unused"`):
				foundUnused = true
			case strings.Contains(iss.Message, `"limit"`):
				foundLimit = true
			}
		}
	}
	if !foundUnused {
		t.Errorf("expected unused-input issue for `unused`, got: %v", issues)
	}
	if foundLimit {
		t.Errorf("input `limit` IS used in output, should not be flagged")
	}
}

func TestLint_UnusedInput_StringInterpolation_NoIssue(t *testing.T) {
	// Input `name` used only inside a string interpolation should NOT be flagged.
	src := minimalHeader + `
@ "Greeting"
GET /api/hello/:name {
  <- name string required
  -> 200 { message: "Hello, {name}!" }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "unused-input" {
			t.Errorf("input used in string interpolation should not flag, got: %s", iss)
		}
	}
}

func TestLint_UnusedInput_SearchParamConvention_NoIssue(t *testing.T) {
	// `<- search string optional` is auto-applied by codegen as an ILIKE filter.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "List widgets"
GET /api/widgets {
  <- search string optional
  |> items = query widget
  -> 200 { items: items }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "unused-input" {
			t.Errorf("search-convention input should not flag, got: %s", iss)
		}
	}
}

func TestLint_UnusedInput_BlockExprShorthand_NoIssue(t *testing.T) {
	// `save widget { name: name }` references `name` in the block expression value.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Create"
POST /api/widgets {
  <- name string required
  |> w = save widget { name: name }
  -> 201 { id: w.id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "unused-input" {
			t.Errorf("block-expr value reference should count as usage, got: %s", iss)
		}
	}
}

func TestLint_SecretBeforeModel_Clean(t *testing.T) {
	// secret → model → endpoint: canonical ordering, expect 0 block-ordering issues.
	src := minimalHeader + `
secret DB_URL required

model user {
  id   uuid   primary
  name string required
}

@ "List users"
GET /api/users {
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "block-ordering" {
			t.Errorf("canonical ordering should not produce block-ordering issue, got: %s", iss)
		}
	}
}
