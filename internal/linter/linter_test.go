package linter_test

import (
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
