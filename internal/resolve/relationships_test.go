package resolve_test

import (
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

func TestResolveQueryRelationshipsAndFirstCardinality(t *testing.T) {
	facts := resolveEndpoint(t, `
  |> todo = query todo with(author, editor) first
  -> 200 { todo: todo }`)
	if len(facts.DataOps) != 1 {
		t.Fatalf("facts=%+v", facts.DataOps)
	}
	fact := facts.DataOps[0]
	if fact.Cardinality != resolve.SingleCard {
		t.Fatalf("cardinality=%v, want SingleCard", fact.Cardinality)
	}
	if !reflect.DeepEqual(fact.Relationships, []string{"author", "editor"}) {
		t.Fatalf("relationships=%v", fact.Relationships)
	}
}

func TestResolveRecordMutationsHaveSingleCardinality(t *testing.T) {
	facts := resolveEndpoint(t, `
  |> created = save todo { title: "created" }
  |> seeded = seed todo { title: "seeded" }
  -> 200 { created: created, seeded: seeded }`)
	if len(facts.DataOps) != 2 {
		t.Fatalf("facts=%+v", facts.DataOps)
	}
	for _, fact := range facts.DataOps {
		if fact.Cardinality != resolve.SingleCard {
			t.Errorf("%s cardinality=%v, want SingleCard", fact.Name, fact.Cardinality)
		}
	}
}
