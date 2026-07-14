package ast_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

func TestPrintComputedFieldsAndWithRelationships(t *testing.T) {
	source := `blueprint "test" { version "1.0" runtime node }
model author {
  id uuid primary
  first_name string required
  last_name string required
  computed full_name string = first_name + " " + last_name
}
model post { id uuid primary author_id uuid ref(author) }
GET /posts {
  |> posts = query post with(author) where(id != "")
  -> 200 { posts: posts }
}`
	printed := ast.Print(parseForPrint(t, source))
	for _, want := range []string{
		`computed full_name string = first_name + " " + last_name`,
		`query post with(author) where(id != "")`,
	} {
		if !strings.Contains(printed, want) {
			t.Fatalf("printed source missing %q:\n%s", want, printed)
		}
	}
	parseForPrint(t, printed)
}
