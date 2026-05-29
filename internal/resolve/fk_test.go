package resolve_test

import (
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

// parseForFK parses a fragment that defines product + cart_item + an
// endpoint, and returns the model list and the endpoint's first StepStmt with
// a guard/output that references item.product.<x>. Used by FK tests below.
func parseForFK(t *testing.T, body string) (models []*ast.Model, stmts []ast.ArrowStmt) {
	t.Helper()
	src := `blueprint "t" { version "1.0" port 3000 runtime node database postgres }
model product   { id uuid primary  stock int required }
model cart_item { id uuid primary  product_id uuid ref(product) required }
GET /api/x {
` + body + `
}
`
	file, errs := parser.ParseFile("t.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	for _, b := range file.Blocks {
		if m, ok := b.(*ast.Model); ok {
			models = append(models, m)
		}
		if ep, ok := b.(*ast.Endpoint); ok {
			stmts = ep.Stmts
		}
	}
	return
}

func TestModelFieldRef(t *testing.T) {
	models, _ := parseForFK(t, `<- id uuid required
  |> item = fetch cart_item(id)
  -> 200 { id: item.id }`)

	target, ok := resolve.ModelFieldRef(models, "cart_item", "product")
	if !ok || target != "product" {
		t.Errorf("cart_item.product should resolve to product, got (%q, %v)", target, ok)
	}
	if _, ok := resolve.ModelFieldRef(models, "cart_item", "nonexistent"); ok {
		t.Errorf("nonexistent field must not resolve")
	}
	if _, ok := resolve.ModelFieldRef(models, "no_such_model", "product"); ok {
		t.Errorf("unknown model must not resolve")
	}
}

func TestFKAccessesInExprFindsRelation(t *testing.T) {
	models, stmts := parseForFK(t, `<- id uuid required
  |> item = fetch cart_item(id)
  |> guard item.product.stock > 0 -> 400 "out"
  -> 200 { id: item.id }`)

	varModels := map[string]string{"item": "cart_item"}
	// The guard is the second statement after the input.
	var guard *ast.GuardStmt
	for _, s := range stmts {
		if g, ok := s.(*ast.GuardStmt); ok {
			guard = g
			break
		}
	}
	if guard == nil {
		t.Fatal("guard not found")
	}
	fks := resolve.FKAccessesInExpr(guard.Condition, models, varModels)
	if len(fks) != 1 {
		t.Fatalf("expected 1 FK access, got %d (%v)", len(fks), fks)
	}
	got := fks[0]
	if got.VarName != "item" || got.FieldName != "product" || got.TargetModel != "product" {
		t.Errorf("unexpected FKAccess: %+v", got)
	}
	if got.FKColumn() != "product_id" {
		t.Errorf("FKColumn() = %q, want product_id", got.FKColumn())
	}
}

func TestFKAccessesDedup(t *testing.T) {
	models, stmts := parseForFK(t, `<- id uuid required
  |> item = fetch cart_item(id)
  -> 200 { stock: item.product.stock, also: item.product.stock }`)

	varModels := map[string]string{"item": "cart_item"}
	var out *ast.OutputStmt
	for _, s := range stmts {
		if o, ok := s.(*ast.OutputStmt); ok {
			out = o
			break
		}
	}
	if out == nil {
		t.Fatal("output not found")
	}
	fks := resolve.FKAccessesInExpr(out.Value, models, varModels)
	if len(fks) != 1 {
		t.Fatalf("FKAccess should be deduplicated; got %d (%v)", len(fks), fks)
	}
}

func TestFKAccessIgnoresNonRefFields(t *testing.T) {
	models, stmts := parseForFK(t, `<- id uuid required
  |> item = fetch cart_item(id)
  -> 200 { id: item.id }`)

	varModels := map[string]string{"item": "cart_item"}
	var out *ast.OutputStmt
	for _, s := range stmts {
		if o, ok := s.(*ast.OutputStmt); ok {
			out = o
			break
		}
	}
	fks := resolve.FKAccessesInExpr(out.Value, models, varModels)
	if len(fks) != 0 {
		t.Errorf("item.id is not a FK relation; got %v", fks)
	}
}

func TestFKAccessesInStmtCoversAllExprSlots(t *testing.T) {
	// Verifies FKAccessesInStmt extracts expressions from each ArrowStmt kind
	// it covers (step / guard / when condition+inline / output).
	models, stmts := parseForFK(t, `<- id uuid required
  |> item = fetch cart_item(id)
  |> guard item.product.stock > 0 -> 400 "out"
  -> 200 { stock: item.product.stock }`)

	varModels := map[string]string{"item": "cart_item"}
	seen := map[string]bool{}
	for _, s := range stmts {
		for _, fk := range resolve.FKAccessesInStmt(s, models, varModels) {
			seen[fk.VarName+"."+fk.FieldName] = true
		}
	}
	if !seen["item.product"] {
		t.Errorf("expected item.product in collected accesses, got %v", seen)
	}
}

func TestFKAccessesLookupCamelFallback(t *testing.T) {
	// When the caller writes varModels with the camel form (as map outer
	// bindings do today), the lookup must still find the FK.
	models, stmts := parseForFK(t, `<- id uuid required
  |> item = fetch cart_item(id)
  -> 200 { stock: item.product.stock }`)

	varModels := map[string]string{"item": "cart_item"} // raw form works
	var out *ast.OutputStmt
	for _, s := range stmts {
		if o, ok := s.(*ast.OutputStmt); ok {
			out = o
		}
	}
	if got := resolve.FKAccessesInExpr(out.Value, models, varModels); len(got) != 1 {
		t.Errorf("raw key lookup failed, got %v", got)
	}

	// Now drop the raw form and use only a snake variant resolved via camel.
	// Source uses `item` already, but exercise the snake_case→camel path with
	// an underscored variable name reachable via lookupVarModel.
	models2, stmts2 := parseForFK(t, `<- id uuid required
  |> cart_item = fetch cart_item(id)
  -> 200 { stock: cart_item.product.stock }`)
	varModels2 := map[string]string{"cartItem": "cart_item"} // camel form only
	var out2 *ast.OutputStmt
	for _, s := range stmts2 {
		if o, ok := s.(*ast.OutputStmt); ok {
			out2 = o
		}
	}
	if got := resolve.FKAccessesInExpr(out2.Value, models2, varModels2); len(got) != 1 {
		t.Errorf("camel-only fallback lookup failed, got %v", got)
	}
}
