package python_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestPythonRejectsComputedFieldsAndRelationshipJoins(t *testing.T) {
	tests := map[string]struct {
		source string
		want   string
	}{
		"computed": {
			source: `blueprint "x" { version "1.0" runtime node database postgres }
model user { id uuid primary name string computed label string = name + "!" }`,
			want: "model computed fields",
		},
		"relationship": {
			source: `blueprint "x" { version "1.0" runtime node database postgres }
model author { id uuid primary }
model post { id uuid primary author_id uuid ref(author) }
GET /posts { |> posts = query post with(author) -> 200 { posts: posts } }`,
			want: "query ... with(...)` relationships",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			file, errors := parser.ParseFile("test.bp", []byte(test.source))
			if len(errors) > 0 {
				t.Fatalf("parse errors: %v", errors)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
			if name == "relationship" && strings.Contains(err.Error(), `value-expression call "with"`) {
				t.Fatalf("with(...) should be reported once as an unsupported query relationship, got: %v", err)
			}
		})
	}
}
