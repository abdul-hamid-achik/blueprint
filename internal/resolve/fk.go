package resolve

import "github.com/abdul-hamid-achik/blueprint/internal/ast"

// FKAccess describes a foreign-key relation traversal found in an expression.
//
// Source patterns like `item.product.stock` resolve to one FKAccess when
// `item` holds a record of model M and M has a `ref(<TargetModel>)` constraint
// on a field named "<FieldName>_id". All names are in their .bp source form
// (typically snake_case); downstream code generators apply their own naming
// conventions when emitting code.
type FKAccess struct {
	// VarName is the source variable as it appears in the .bp source.
	VarName string
	// FieldName is the FK relation field WITHOUT the "_id" suffix. For
	// `item.product.stock`, FieldName is "product".
	FieldName string
	// TargetModel is the model the ref() points to.
	TargetModel string
}

// FKColumn is the underlying foreign-key column name on the source model
// (e.g. "product_id"). Provided as a convenience so generators don't have to
// reconstruct it.
func (a FKAccess) FKColumn() string { return a.FieldName + "_id" }

// ModelFieldRef returns the target model of a `ref()` constraint declared on
// modelName.<fieldName>_id, if any. A pure AST lookup; takes no scope.
//
// Example: with `model cart_item { ... product_id uuid ref(product) }`,
// ModelFieldRef(models, "cart_item", "product") returns ("product", true).
func ModelFieldRef(models []*ast.Model, modelName, fieldName string) (string, bool) {
	for _, m := range models {
		if m.Name != modelName {
			continue
		}
		for _, f := range m.Fields {
			if f.Name != fieldName+"_id" {
				continue
			}
			for _, c := range f.Constraints {
				if c.Kind != "ref" {
					continue
				}
				if id, ok := c.Value.(*ast.Ident); ok {
					return id.Name, true
				}
			}
		}
	}
	return "", false
}

// FKAccessesInExpr walks expr and returns deduplicated FKAccess entries —
// one per distinct (variable, field) pair where the variable's model has a
// matching `ref()` constraint.
//
// varModels is the caller's resolved variable -> model lookup (today the
// codegen's emit context map; future callers may pass a synthesized map).
// The lookup is tried under both the raw name and a camelCased fallback to
// mirror how codegen currently writes that map.
func FKAccessesInExpr(expr ast.Expr, models []*ast.Model, varModels map[string]string) []FKAccess {
	seen := map[string]bool{}
	var out []FKAccess
	walkExpr(expr, func(node ast.Expr) {
		fa, ok := node.(*ast.FieldAccess)
		if !ok {
			return
		}
		baseIdent, ok := fa.Base.(*ast.Ident)
		if !ok {
			return
		}
		modelName, ok := lookupVarModel(varModels, baseIdent.Name)
		if !ok {
			return
		}
		target, has := ModelFieldRef(models, modelName, fa.Field)
		if !has {
			return
		}
		// Dedupe by the raw access pattern; downstream conversions (camelCase
		// etc.) cannot introduce extra collisions because they're applied
		// after deduplication.
		key := baseIdent.Name + "." + fa.Field
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, FKAccess{
			VarName:     baseIdent.Name,
			FieldName:   fa.Field,
			TargetModel: target,
		})
	})
	return out
}

// FKAccessesInStmt collects FK accesses across every expression carried by
// stmt (the step's expression, a guard or when's condition, a when's inline
// body, an output's value). Nested when/try/recover bodies are NOT walked —
// callers iterate stmts at their own scope level so each enclosing block can
// emit FK sub-queries at the right depth.
func FKAccessesInStmt(stmt ast.ArrowStmt, models []*ast.Model, varModels map[string]string) []FKAccess {
	var exprs []ast.Expr
	switch s := stmt.(type) {
	case *ast.StepStmt:
		exprs = append(exprs, s.Expr)
	case *ast.GuardStmt:
		exprs = append(exprs, s.Condition)
	case *ast.WhenStmt:
		exprs = append(exprs, s.Condition)
		if s.Inline != nil {
			exprs = append(exprs, s.Inline)
		}
	case *ast.OutputStmt:
		if s.Value != nil {
			exprs = append(exprs, s.Value)
		}
	}
	seen := map[string]bool{}
	var out []FKAccess
	for _, e := range exprs {
		for _, fk := range FKAccessesInExpr(e, models, varModels) {
			key := fk.VarName + "." + fk.FieldName
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, fk)
		}
	}
	return out
}

// lookupVarModel tries the raw key first, then a camelCased fallback —
// mirroring codegen's mixed-convention writes (data-op steps populate the
// raw form; map outer bindings populate the camel form).
func lookupVarModel(varModels map[string]string, name string) (string, bool) {
	if m, ok := varModels[name]; ok {
		return m, true
	}
	if m, ok := varModels[snakeToCamel(name)]; ok {
		return m, true
	}
	return "", false
}

// snakeToCamel converts snake_case to camelCase. Kept local to avoid the
// resolve package depending on a sibling helpers package.
func snakeToCamel(s string) string {
	out := make([]byte, 0, len(s))
	upper := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			upper = true
			continue
		}
		if upper && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		upper = false
		out = append(out, c)
	}
	return string(out)
}

// walkExpr applies fn to every node in the expression tree, parents first.
// Mirrors the visitor used by codegen; kept internal so the resolve package
// stays self-contained.
func walkExpr(e ast.Expr, fn func(ast.Expr)) {
	if e == nil {
		return
	}
	fn(e)
	switch v := e.(type) {
	case *ast.BinaryExpr:
		walkExpr(v.Left, fn)
		walkExpr(v.Right, fn)
	case *ast.UnaryExpr:
		walkExpr(v.Operand, fn)
	case *ast.ParenExpr:
		walkExpr(v.Expr, fn)
	case *ast.FnCall:
		for _, arg := range v.Args {
			walkExpr(arg, fn)
		}
	case *ast.FieldAccess:
		walkExpr(v.Base, fn)
	case *ast.IndexAccess:
		walkExpr(v.Base, fn)
		walkExpr(v.Index, fn)
	case *ast.ListExpr:
		for _, el := range v.Elements {
			walkExpr(el, fn)
		}
	case *ast.BlockExpr:
		for _, kv := range v.Entries {
			walkExpr(kv.Value, fn)
		}
	}
}
