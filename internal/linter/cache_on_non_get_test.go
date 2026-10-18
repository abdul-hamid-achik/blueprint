package linter_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/linter"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func cacheOnNonGetIssues(t *testing.T, body string) []linter.Issue {
	t.Helper()
	file, errs := parser.ParseFile("test.bp", []byte(minimalHeader+body))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	var out []linter.Issue
	for _, iss := range linter.Lint(file) {
		if iss.Rule == "cache-on-non-get" {
			out = append(out, iss)
		}
	}
	return out
}

func TestLint_CacheOnNonGet_Flagged(t *testing.T) {
	issues := cacheOnNonGetIssues(t, `
@ "create an item"
POST /api/items {
  cache 5s
  -> 201 { ok: true }
}

@ "replace an item"
PUT /api/items/:id {
  cache 5s
  <- id string required
  -> 200 { ok: true }
}
`)
	if len(issues) != 2 {
		t.Fatalf("expected 2 cache-on-non-get issues, got %d: %v", len(issues), issues)
	}
	for _, iss := range issues {
		if iss.Level != "warning" {
			t.Errorf("level = %q, want warning", iss.Level)
		}
		if !strings.Contains(iss.Message, "cache is ignored on") {
			t.Errorf("unexpected message: %s", iss.Message)
		}
	}
	if !strings.Contains(issues[0].Message, "POST /api/items") {
		t.Errorf("message should name the endpoint: %s", issues[0].Message)
	}
}

func TestLint_CacheOnGet_NoIssue(t *testing.T) {
	issues := cacheOnNonGetIssues(t, `
@ "read an item"
GET /api/items/:id {
  cache 5s
  <- id string required
  -> 200 { id: id }
}
`)
	if len(issues) != 0 {
		t.Errorf("GET with cache should not be flagged, got %v", issues)
	}
}

func TestLint_NonGetWithoutCache_NoIssue(t *testing.T) {
	issues := cacheOnNonGetIssues(t, `
@ "create an item"
POST /api/items {
  -> 201 { ok: true }
}
`)
	if len(issues) != 0 {
		t.Errorf("POST without cache should not be flagged, got %v", issues)
	}
}
