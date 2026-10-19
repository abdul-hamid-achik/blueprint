package js

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// modelBoundsSource covers each mapping in pydanticFieldBounds: length bounds
// for string, text, list, and map; value bounds for int, float, and money; and
// a text field that is optional, so the bounds sit before .nullable().
const modelBoundsSource = `blueprint "model-bounds" {
  version "1.0"
  runtime node
  database postgres
}
model listing {
  id uuid primary
  title string required min(3) max(120)
  body text optional min(0) max(5000)
  score int required min(0) max(100)
  rating float required min(0.5) max(5)
  price money required min(0) max(9999)
  tags list(string) required min(1) max(5)
  labels map(string, string) optional max(10)
  code uuid required min(1)
  active bool required
}
GET /api/listings {
  |> items = query listing
  -> 200 { items: items }
}`

func TestModelFieldBoundsReachFrontendSchemas(t *testing.T) {
	file, parseErrors := parser.ParseFile("model-bounds.bp", []byte(modelBoundsSource))
	if len(parseErrors) > 0 {
		t.Fatalf("parse errors: %v", parseErrors)
	}
	if errors := checker.Check(file); len(errors) > 0 {
		t.Fatalf("check errors: %v", errors)
	}
	dir := t.TempDir()
	if err := New().Generate(file, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	schemas, err := os.ReadFile(filepath.Join(dir, "src/types/schemas.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"export const ListingSchema = z.object({",
		"title: z.string().min(3).max(120),",
		"body: z.string().min(0).max(5000).nullable(),",
		"score: z.number().int().min(0).max(100),",
		"rating: z.number().min(0.5).max(5),",
		"price: z.number().min(0).max(9999),",
		"tags: z.array(z.string()).min(1).max(5),",
		"labels: z.record(z.string(), z.string()).refine((value) => Object.keys(value).length <= 10).nullable(),",
		// uuid carries no bounds on Python either, so the uuid field stays bare.
		"code: z.string().uuid(),",
		"active: z.boolean(),",
	} {
		if !strings.Contains(string(schemas), want) {
			t.Errorf("schemas.ts missing %q:\n%s", want, schemas)
		}
	}

	// Model bounds are documentation of the stored shape. They must not reach
	// the Drizzle table, which would change migrations.
	schemaTable, err := os.ReadFile(filepath.Join(dir, "src/models/schema.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(schemaTable), "check(") {
		t.Errorf("drizzle schema should not gain CHECK constraints from model bounds:\n%s", schemaTable)
	}
}

// TestFrontendModelFieldBoundsSkipUnsupportedValues pins the type and value
// gates: a non-integer bound on an int field and any bound on a bool field are
// dropped instead of emitted as invalid Zod.
func TestFrontendModelFieldBoundsSkipUnsupportedValues(t *testing.T) {
	prim := func(name string) ast.TypeExpr { return &ast.PrimitiveType{Name: name} }
	minFloat := &ast.FloatLit{Value: "1.5"}
	minInt := &ast.IntLit{Value: "1"}
	bound := func(v ast.Expr) []*ast.Constraint_ {
		return []*ast.Constraint_{{Kind: "min", Value: v}}
	}

	if got := frontendModelFieldBoundsZod(prim("int"), bound(minFloat)); got != "" {
		t.Errorf("int bound with a float literal should be skipped, got %q", got)
	}
	if got := frontendModelFieldBoundsZod(prim("bool"), bound(minInt)); got != "" {
		t.Errorf("bool field should carry no bounds, got %q", got)
	}
	if got := frontendModelFieldBoundsZod(prim("float"), bound(minFloat)); got != ".min(1.5)" {
		t.Errorf("float bound with a float literal should apply, got %q", got)
	}
	if got := frontendModelFieldBoundsZod(prim("string"), bound(minFloat)); got != "" {
		t.Errorf("string length bound with a float literal should be skipped, got %q", got)
	}
}
