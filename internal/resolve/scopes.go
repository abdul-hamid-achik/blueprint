package resolve

import "github.com/abdul-hamid-achik/blueprint/internal/ast"

// Variable-mutation facts that targets use to decide things like JS `let`
// vs `const`, Python type annotations on rebound names, Rust `mut`, etc.
//
// All returned sets are keyed by RAW (.bp source) names. Target generators
// apply their own naming conventions at lookup time.

// InputsReassignedInSteps returns the set of <- input names that are later
// reassigned by a |> step in the same block. (Used by JS to switch the
// input declaration from `const` to `let`.)
func InputsReassignedInSteps(stmts []ast.ArrowStmt) map[string]bool {
	inputs := map[string]bool{}
	reassigned := map[string]bool{}
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.InputStmt:
			inputs[v.Name] = true
		case *ast.StepStmt:
			if v.Binding != "" && inputs[v.Binding] {
				reassigned[v.Binding] = true
			}
		}
	}
	return reassigned
}

// FetchVarsReassignedByUnboundUpdate detects fetch-bound variables that are
// implicitly reassigned by a later unbound `update <model>`:
//
//	|> note = fetch note(id)
//	...
//	|> update note { title: title }
//
// The fetch binding ("note") must be mutable in targets where rebinding
// matters (JS: `let`; Rust: `let mut`). Returned set is keyed by the
// binding name (raw).
func FetchVarsReassignedByUnboundUpdate(stmts []ast.ArrowStmt) map[string]bool {
	// model name -> raw fetch binding name
	fetchBindings := map[string]string{}
	for _, s := range stmts {
		step, ok := s.(*ast.StepStmt)
		if !ok || step.Binding == "" {
			continue
		}
		fn, ok := step.Expr.(*ast.FnCall)
		if !ok || fn.Name != "fetch" || len(fn.Args) == 0 {
			continue
		}
		if id, ok := fn.Args[0].(*ast.Ident); ok {
			fetchBindings[id.Name] = step.Binding
		}
	}

	result := map[string]bool{}
	for _, s := range stmts {
		step, ok := s.(*ast.StepStmt)
		if !ok || step.Binding != "" {
			continue
		}
		fn, ok := step.Expr.(*ast.FnCall)
		if !ok || fn.Name != "update" || len(fn.Args) == 0 {
			continue
		}
		id, ok := fn.Args[0].(*ast.Ident)
		if !ok {
			continue
		}
		if binding, found := fetchBindings[id.Name]; found {
			result[binding] = true
		}
	}
	return result
}

// VarsWithPropertyMutation finds variables targeted by an inline
// `when cond: var.field = value` assignment. Targets that emit static types
// for these (e.g. JS's `Record<string, any>`) read this set to widen the
// declared type. Keys are the raw variable names appearing on the LHS base.
func VarsWithPropertyMutation(stmts []ast.ArrowStmt) map[string]bool {
	mutated := map[string]bool{}
	for _, s := range stmts {
		when, ok := s.(*ast.WhenStmt)
		if !ok || when.Inline == nil {
			continue
		}
		bin, ok := when.Inline.(*ast.BinaryExpr)
		if !ok || bin.Op != "=" {
			continue
		}
		fa, ok := bin.Left.(*ast.FieldAccess)
		if !ok {
			continue
		}
		if id, ok := fa.Base.(*ast.Ident); ok {
			mutated[id.Name] = true
		}
	}
	return mutated
}
