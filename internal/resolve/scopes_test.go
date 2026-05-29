package resolve_test

import (
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

// stmtsFromEndpoint parses a fragment and returns the first endpoint's stmts.
func stmtsFromEndpoint(t *testing.T, body string) []ast.ArrowStmt {
	t.Helper()
	src := `blueprint "t" { version "1.0" port 3000 runtime node database postgres }
model note { id uuid primary  title string required }
GET /api/x {
` + body + `
}
`
	file, errs := parser.ParseFile("t.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	for _, b := range file.Blocks {
		if ep, ok := b.(*ast.Endpoint); ok {
			return ep.Stmts
		}
	}
	t.Fatal("no endpoint")
	return nil
}

func TestInputsReassignedInSteps(t *testing.T) {
	// Input `q` is later rebound by a step; expect it in the set.
	stmts := stmtsFromEndpoint(t, `
  <- q string optional
  <- page int default(1)
  |> q = "filtered"
  -> 200 { ok: page }`)

	got := resolve.InputsReassignedInSteps(stmts)
	want := map[string]bool{"q": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestInputsReassignedIgnoresNonInputBindings(t *testing.T) {
	// Step binds a new var `notes` that's not an input — must not appear.
	stmts := stmtsFromEndpoint(t, `
  <- page int default(1)
  |> notes = query note
  -> 200 { notes: notes }`)

	if got := resolve.InputsReassignedInSteps(stmts); len(got) != 0 {
		t.Errorf("non-input bindings must not appear, got %v", got)
	}
}

func TestFetchVarsReassignedByUnboundUpdate(t *testing.T) {
	stmts := stmtsFromEndpoint(t, `
  <- id uuid required
  |> note = fetch note(id)
  |> update note { title: "x" }
  -> 200 { id: note.id }`)

	got := resolve.FetchVarsReassignedByUnboundUpdate(stmts)
	if !got["note"] {
		t.Errorf("expected note to be flagged as reassigned by unbound update, got %v", got)
	}
}

func TestFetchVarsIgnoredWhenUpdateIsBound(t *testing.T) {
	// Bound update doesn't implicitly reassign the fetch result.
	stmts := stmtsFromEndpoint(t, `
  <- id uuid required
  |> note = fetch note(id)
  |> updated = update note { title: "x" }
  -> 200 { id: updated.id }`)

	if got := resolve.FetchVarsReassignedByUnboundUpdate(stmts); got["note"] {
		t.Errorf("bound update should not trigger implicit reassignment, got %v", got)
	}
}

func TestVarsWithPropertyMutation(t *testing.T) {
	// `when q: filters.q = q` should flag `filters` as property-mutated.
	stmts := stmtsFromEndpoint(t, `
  <- q string optional
  |> filters = {}
  |> when q: filters.q = q
  -> 200 { ok: true }`)

	got := resolve.VarsWithPropertyMutation(stmts)
	if !got["filters"] {
		t.Errorf("expected filters to be flagged, got %v", got)
	}
}
