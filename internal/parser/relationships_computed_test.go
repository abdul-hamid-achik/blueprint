package parser

import (
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

func TestParseComputedFieldsAndWithRelationships(t *testing.T) {
	file := parseString(t, `blueprint "test" { version "1.0" runtime node }
model author {
  id uuid primary
  first_name string required
  last_name string required
  computed full_name string = first_name + " " + last_name
}
model post { id uuid primary  author_id uuid ref(author) }
GET /posts {
  |> posts = query post where(id != "") with(author) order(id, asc)
  -> 200 { posts: posts }
}`)

	author := file.Blocks[0].(*ast.Model)
	if len(author.Fields) != 3 || len(author.ComputedFields) != 1 {
		t.Fatalf("persisted=%d computed=%d, want 3 and 1", len(author.Fields), len(author.ComputedFields))
	}
	computed := author.ComputedFields[0]
	if computed.Name != "full_name" {
		t.Fatalf("computed name=%q", computed.Name)
	}
	if _, ok := computed.Expr.(*ast.BinaryExpr); !ok {
		t.Fatalf("computed expression is %T, want binary expression", computed.Expr)
	}

	ep := file.Blocks[2].(*ast.Endpoint)
	query := ep.Stmts[0].(*ast.StepStmt).Expr.(*ast.FnCall)
	wantMarkers := []string{"where", "with", "order"}
	for i, want := range wantMarkers {
		marker, ok := query.Args[i+1].(*ast.FnCall)
		if !ok || marker.Name != want {
			t.Fatalf("query arg %d = %#v, want %s marker", i+1, query.Args[i+1], want)
		}
	}
}

func TestComputedIsAReservedContextualModelFieldName(t *testing.T) {
	_, errors := ParseFile("test.bp", []byte(`blueprint "test" { version "1.0" runtime node }
model record { computed string required }`))
	if len(errors) == 0 || errors[0].Message != "model field name 'computed' is reserved for computed declarations" {
		t.Fatalf("errors=%v", errors)
	}
	if errors[0].Hint == "" {
		t.Fatal("reserved-name diagnostic should explain the computed declaration form")
	}
}
