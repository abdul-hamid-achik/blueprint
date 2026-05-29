package resolve_test

import (
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

// resolveEndpoint parses src, finds the single endpoint, and returns its
// resolved BlockFacts. The src fragment is wrapped in a minimal blueprint
// header automatically.
func resolveEndpoint(t *testing.T, body string) *resolve.BlockFacts {
	t.Helper()
	src := `blueprint "t" { version "1.0" port 3000 runtime node database postgres }
model todo { id uuid primary  title string required }
model cart_item { id uuid primary  product_id uuid required }
GET /api/x {
` + body + `
}
`
	file, errs := parser.ParseFile("t.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	for _, block := range file.Blocks {
		if ep, ok := block.(*ast.Endpoint); ok {
			return resolve.ResolveBlock(ep.Stmts)
		}
	}
	t.Fatal("no endpoint found in test source")
	return nil
}

func TestResolveFetchSingleCardinality(t *testing.T) {
	facts := resolveEndpoint(t, `
  <- id uuid required
  |> todo = fetch todo(id)
  -> 200 { id: todo.id }`)

	if len(facts.DataOps) != 1 {
		t.Fatalf("expected 1 data-op fact, got %d (%v)", len(facts.DataOps), facts.DataOps)
	}
	got := facts.DataOps[0]
	if got.Name != "todo" || got.Model != "todo" || got.Cardinality != resolve.SingleCard {
		t.Errorf("fetch should yield single-card todo binding, got %+v", got)
	}
}

func TestResolveQueryCollectionCardinality(t *testing.T) {
	facts := resolveEndpoint(t, `
  |> todos = query todo
  -> 200 { todos: todos }`)

	if len(facts.DataOps) != 1 {
		t.Fatalf("expected 1 data-op fact, got %d", len(facts.DataOps))
	}
	if facts.DataOps[0].Cardinality != resolve.CollectionCard {
		t.Errorf("query without paginate should be Collection, got %v", facts.DataOps[0].Cardinality)
	}
}

func TestResolveQueryPaginatedCardinality(t *testing.T) {
	facts := resolveEndpoint(t, `
  <- page int default(1)
  <- per_page int default(20)
  |> todos = query todo paginate(page, per_page)
  -> 200 { todos: todos.items }`)

	if len(facts.DataOps) != 1 {
		t.Fatalf("expected 1 data-op fact, got %d", len(facts.DataOps))
	}
	if facts.DataOps[0].Cardinality != resolve.PaginatedCard {
		t.Errorf("query with paginate() should be Paginated, got %v", facts.DataOps[0].Cardinality)
	}
}

func TestResolveDocumentOrderPreserved(t *testing.T) {
	facts := resolveEndpoint(t, `
  <- id uuid required
  |> first = fetch todo(id)
  |> second = query todo
  |> third = fetch todo(id)
  -> 200 { id: first.id }`)

	if len(facts.DataOps) != 3 {
		t.Fatalf("expected 3 data-op facts, got %d", len(facts.DataOps))
	}
	wantNames := []string{"first", "second", "third"}
	for i, want := range wantNames {
		if facts.DataOps[i].Name != want {
			t.Errorf("facts[%d].Name=%q, want %q", i, facts.DataOps[i].Name, want)
		}
	}
}

func TestResolveWalksWhenAndTryRecover(t *testing.T) {
	// when bodies and try/recover branches MUST surface bindings because the
	// current codegen treats them as flat scope. Pre-resolving must match.
	facts := resolveEndpoint(t, `
  <- id uuid required
  |> when id == id {
    |> inside_when = fetch todo(id)
  }
  |> try {
    |> inside_try = query todo
  } recover {
    |> inside_recover = fetch todo(id)
  }
  -> 200 { id: id }`)

	got := map[string]bool{}
	for _, f := range facts.DataOps {
		got[f.Name] = true
	}
	for _, want := range []string{"inside_when", "inside_try", "inside_recover"} {
		if !got[want] {
			t.Errorf("expected nested binding %q to be surfaced, got %v", want, got)
		}
	}
}

func TestResolveMapOuterBinding(t *testing.T) {
	facts := resolveEndpoint(t, `
  |> items = query cart_item
  |> results = map items: save todo { title: "x" }
  -> 200 { items: items }`)

	if len(facts.MapResults) != 1 {
		t.Fatalf("expected 1 map-result fact, got %d", len(facts.MapResults))
	}
	got := facts.MapResults[0]
	if got.Name != "results" || got.Model != "todo" {
		t.Errorf("map result should bind results→todo, got %+v", got)
	}
	if got.Cardinality != resolve.CollectionCard {
		t.Errorf("map result should be Collection, got %v", got.Cardinality)
	}
}

func TestResolveUnboundStepIgnored(t *testing.T) {
	// `|> guard todo` and `|> delete todo` have no Binding; the resolver must skip them.
	facts := resolveEndpoint(t, `
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "no"
  |> delete todo
  -> 200 { id: todo.id }`)

	if len(facts.DataOps) != 1 || facts.DataOps[0].Name != "todo" {
		t.Errorf("only the bound fetch should be recorded; got %+v", facts.DataOps)
	}
}
