package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

// bodyCtx carries per-endpoint emit state: which variables are bound to which
// model, which are paginated, and which inputs are already declared via the
// handler signature. The resolver populates the fact maps once at the start
// of emitBody; the codegen reads them as it walks statements.
//
// fkAliases caches aliases for foreign-key sub-queries — keyed by the raw
// "varName.fieldName" access (e.g. "item.product"). The alias is the Python
// local emitted before the referencing statement (e.g. "_product"). Subsequent
// statements that reference the same FK reuse the alias instead of re-querying.
type bodyCtx struct {
	path        string
	inputs      map[string]bool   // input names already in the handler signature
	varModel    map[string]string // binding name -> model name
	cardinality map[string]resolve.Cardinality
	fkAliases   map[string]string // "var.field" -> Python alias (e.g. "_product")
	models      []*ast.Model      // for FK resolution
	userFns     map[string]bool   // declared fn/pipe names (for step-call dispatch)

	// orderedBindings lists every binding->model fact declared SO FAR (in
	// source order, middleware-injected aliases first). It is built
	// incrementally: each step registers its binding AFTER it is emitted, so
	// lastBindingForModel only sees bindings that precede the current
	// statement — preventing forward-reference NameErrors and resolving to
	// the correct preceding binding for `update <model>`/`delete <model>`.
	orderedBindings []resolve.StepFact

	// bindingModels maps every binding name in the block to its model,
	// pre-computed from resolve.ResolveBlock. Used to register bindings
	// incrementally into orderedBindings without re-deriving the model.
	bindingModels map[string]string

	// touchesDB mirrors endpointTouchesDB(ep) — computed once so
	// emitTryRecover can decide whether an unwrapped except branch still
	// needs a defensive db.rollback() (a failed flush/commit anywhere in the
	// handler leaves the SQLAlchemy session unusable until rolled back).
	touchesDB bool

	// inTxn is set while emitting statements inside a try body that
	// emitTryRecover wrapped in `with db.begin_nested():`. save/update/
	// delete/map emit db.flush() instead of db.commit() so the nested
	// transaction isn't ended early (flush still populates PKs for the
	// following db.refresh()).
	inTxn bool

	// inRecover is set while emitting a recover body's statements. Python
	// exceptions have no `.message` attribute, so exprToPyWithCtx and the
	// f-string interpolation path rewrite `error.message` to `str(error)`
	// only within this scope.
	inRecover bool
}

func (g *Generator) newBodyCtx(ep *ast.Endpoint) *bodyCtx {
	c := &bodyCtx{
		path:          ep.Path,
		inputs:        map[string]bool{},
		varModel:      map[string]string{},
		cardinality:   map[string]resolve.Cardinality{},
		fkAliases:     map[string]string{},
		bindingModels: map[string]string{},
		models:        g.models(),
		userFns:       g.userFnNames(),
		touchesDB:     endpointTouchesDB(ep),
	}
	for _, s := range ep.Stmts {
		if in, ok := s.(*ast.InputStmt); ok {
			c.inputs[in.Name] = true
		}
	}
	// Middleware-injected aliases (e.g. `current_user` from `inject user as current_user`)
	// become model-typed variables in the endpoint body. Seed varModel so subsequent
	// `update current_user { ... }` and FK access resolve correctly.
	for _, m := range ep.Meta {
		if m.Kind != "use" || m.Use == nil {
			continue
		}
		mw := g.findMiddleware(m.Use.Name)
		if mw == nil {
			continue
		}
		alias := findInjectedAlias(mw)
		model := findInjectedModel(mw)
		if alias != "" && model != "" {
			c.varModel[alias] = model
			c.orderedBindings = append(c.orderedBindings, resolve.StepFact{Name: alias, Model: model})
		}
	}
	facts := resolve.ResolveBlock(ep.Stmts)
	for _, f := range facts.DataOps {
		c.varModel[f.Name] = f.Model
		c.cardinality[f.Name] = f.Cardinality
		c.bindingModels[f.Name] = f.Model
	}
	for _, f := range facts.MapResults {
		c.varModel[f.Name] = f.Model
		c.cardinality[f.Name] = f.Cardinality
		c.bindingModels[f.Name] = f.Model
	}
	return c
}

// lastBindingForModel returns the most recently declared (in source order)
// binding name for the given model, per ctx.orderedBindings. Returns
// ok=false when no binding for that model was ever declared.
func lastBindingForModel(ctx *bodyCtx, model string) (string, bool) {
	name := ""
	found := false
	for _, f := range ctx.orderedBindings {
		if f.Model == model {
			name = f.Name
			found = true
		}
	}
	return name, found
}

// emitBody writes the body of an endpoint handler — every statement after
// the inputs (which are part of the signature). Caller has already written
// the `async def name(...):` line; emitBody writes the indented body.
func (g *Generator) emitBody(b *strings.Builder, ep *ast.Endpoint) {
	ctx := g.newBodyCtx(ep)
	wrote := false
	for _, s := range ep.Stmts {
		if emitBodyStmt(b, s, ctx, "    ") {
			wrote = true
		}
	}
	if !wrote {
		// Defensive: an endpoint that somehow has nothing must still return.
		b.WriteString("    return Response(status_code=204)\n")
	}
}

// emitBodyStmt dispatches one ArrowStmt at the given indent. Returns whether
// anything was emitted (so the empty-body defensive return only fires when
// nothing at all was produced).
func emitBodyStmt(b *strings.Builder, s ast.ArrowStmt, ctx *bodyCtx, indent string) bool {
	// Pre-scan: emit FK sub-queries for any FK access patterns in this
	// statement BEFORE the statement itself. Dedupe across sibling statements
	// via ctx.fkAliases so multiple references to `item.product.x` share one
	// `_product = db.get(...)` line.
	emitFKAliases(b, s, ctx, indent)

	switch v := s.(type) {
	case *ast.InputStmt:
		return false // already in signature
	case *ast.IntentStep:
		fmt.Fprintf(b, "%s# %s\n", indent, v.Text)
	case *ast.StepStmt:
		emitStep(b, v, ctx, indent)
		// Register this step's binding into orderedBindings AFTER emission
		// so lastBindingForModel is position-aware: `update <model>`/
		// `delete <model>` only resolve to bindings declared in preceding
		// statements, never forward references (or bindings inside recover).
		if v.Binding != "" {
			if model, ok := ctx.bindingModels[v.Binding]; ok {
				ctx.orderedBindings = append(ctx.orderedBindings, resolve.StepFact{Name: v.Binding, Model: model})
			}
		}
	case *ast.GuardStmt:
		emitGuard(b, v, ctx, indent)
	case *ast.WhenStmt:
		emitWhen(b, v, ctx, indent)
	case *ast.OutputStmt:
		emitReturn(b, v, ctx, indent)
	case *ast.TryRecover:
		emitTryRecover(b, v, ctx, indent)
	default:
		return false
	}
	return true
}

// emitFKAliases finds every FK access (e.g. `item.product.x`) referenced by
// stmt and emits one `_field = db.get(schema.Target, var.field_id)` per unique
// (var, field) pair that isn't already in ctx.fkAliases.
func emitFKAliases(b *strings.Builder, stmt ast.ArrowStmt, ctx *bodyCtx, indent string) {
	if len(ctx.models) == 0 {
		return
	}
	for _, fk := range resolve.FKAccessesInStmt(stmt, ctx.models, ctx.varModel) {
		key := fk.VarName + "." + fk.FieldName
		if _, already := ctx.fkAliases[key]; already {
			continue
		}
		alias := "_" + fk.FieldName
		className := common.PascalCase(fk.TargetModel)
		fmt.Fprintf(b, "%s%s = db.get(schema.%s, %s.%s)\n",
			indent, alias, className, fk.VarName, fk.FKColumn())
		ctx.fkAliases[key] = alias
	}
}

// emitStep translates one |> step into Python. Phase 3c supports the full
// data-op set (`save`/`fetch`/`update`/`delete`/`query`), plus `map`, `log`,
// and `where`/`order`/`paginate` markers on `query`. Step calls to user-defined
// fn/pipe names are still rejected upstream.
func emitStep(b *strings.Builder, s *ast.StepStmt, ctx *bodyCtx, indent string) {
	fn, ok := s.Expr.(*ast.FnCall)
	if !ok {
		fmt.Fprintf(b, "%s# TODO(python): non-data-op step %T\n", indent, s.Expr)
		return
	}
	switch fn.Name {
	case "save":
		emitSave(b, s.Binding, fn, ctx, indent)
	case "fetch":
		emitFetch(b, s.Binding, fn, ctx, indent)
	case "query":
		emitQuery(b, s.Binding, fn, ctx, indent)
	case "update":
		emitUpdate(b, s.Binding, fn, ctx, indent)
	case "delete":
		emitDelete(b, fn, ctx, indent)
	case "map":
		emitMap(b, s.Binding, fn, ctx, indent)
	case "log":
		emitLog(b, fn, ctx, indent)
	case "sum":
		emitSum(b, s.Binding, fn, ctx, indent)
	default:
		if ctx.userFns[fn.Name] {
			emitUserFnCall(b, s.Binding, fn, ctx, indent)
			return
		}
		fmt.Fprintf(b, "%s# TODO(python): unsupported step %q\n", indent, fn.Name)
	}
}

// emitSum translates `|> total = sum(<coll>.<field> ...arith...)` into a
// generator-expression sum:
//
//	total = sum(r.<field> ...arith... for r in <coll>)
//
// Mirrors the JS target's `.reduce(...)` rewrite. Every FieldAccess in the
// argument whose base is an Ident is treated as a column access on the
// shared collection; the loop variable `r` replaces the base.
func emitSum(b *strings.Builder, binding string, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	if len(fn.Args) < 1 {
		fmt.Fprintf(b, "%s# TODO(python): sum() with no argument\n", indent)
		return
	}
	collection, ok := extractSumCollection(fn.Args[0])
	if !ok {
		fmt.Fprintf(b, "%s# TODO(python): sum() body must reference a single collection\n", indent)
		return
	}
	body := rewriteSumBody(fn.Args[0], collection, ctx)
	v := bindingOrPlaceholder(binding, "_sum")
	fmt.Fprintf(b, "%s%s = sum(%s for r in %s)\n", indent, v, body, collection)
}

// extractSumCollection walks the sum() argument and returns the single
// collection name referenced (e.g. "order_items"). Returns ok=false if zero
// or multiple distinct collection bases are found.
func extractSumCollection(e ast.Expr) (string, bool) {
	seen := map[string]bool{}
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch v := e.(type) {
		case *ast.FieldAccess:
			if id, ok := v.Base.(*ast.Ident); ok {
				seen[id.Name] = true
				return
			}
			walk(v.Base)
		case *ast.BinaryExpr:
			walk(v.Left)
			walk(v.Right)
		case *ast.UnaryExpr:
			walk(v.Operand)
		case *ast.ParenExpr:
			walk(v.Expr)
		case *ast.FnCall:
			for _, a := range v.Args {
				walk(a)
			}
		}
	}
	walk(e)
	if len(seen) != 1 {
		return "", false
	}
	for k := range seen {
		return k, true
	}
	return "", false
}

// rewriteSumBody renders the sum() argument expression with every
// `<collection>.<field>` rewritten to `r.<field>`.
func rewriteSumBody(e ast.Expr, collection string, ctx *bodyCtx) string {
	switch v := e.(type) {
	case *ast.FieldAccess:
		if id, ok := v.Base.(*ast.Ident); ok && id.Name == collection {
			return "r." + v.Field
		}
		return rewriteSumBody(v.Base, collection, ctx) + "." + v.Field
	case *ast.BinaryExpr:
		return fmt.Sprintf("(%s %s %s)",
			rewriteSumBody(v.Left, collection, ctx),
			translateBinaryOp(v.Op),
			rewriteSumBody(v.Right, collection, ctx))
	case *ast.UnaryExpr:
		return translateUnaryOp(v.Op) + " " + rewriteSumBody(v.Operand, collection, ctx)
	case *ast.ParenExpr:
		return "(" + rewriteSumBody(v.Expr, collection, ctx) + ")"
	default:
		return exprToPyWithCtx(e, ctx)
	}
}

// emitUserFnCall dispatches a `|> binding = fn_name(args)` step where fn_name
// is a declared user `fn`. The Python module layout puts every fn behind
// `src.functions.<name>` so we just call the imported alias; the route file's
// import collector picks up the dependency.
func emitUserFnCall(b *strings.Builder, binding string, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	args := make([]string, len(fn.Args))
	for i, a := range fn.Args {
		args[i] = exprToPyWithCtx(a, ctx)
	}
	v := bindingOrPlaceholder(binding, "_result")
	fmt.Fprintf(b, "%s%s = %s(%s)\n", indent, v, fn.Name, strings.Join(args, ", "))
}

// emitSave: `binding = save M { k: v, ... }` →
//
//	binding = schema.M(k=v, ...)
//	db.add(binding); db.commit(); db.refresh(binding)
func emitSave(b *strings.Builder, binding string, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	if len(fn.Args) < 1 {
		return
	}
	model := identName(fn.Args[0])
	className := common.PascalCase(model)

	kwargs := ""
	if len(fn.Args) >= 2 {
		if block, ok := fn.Args[1].(*ast.BlockExpr); ok {
			parts := make([]string, 0, len(block.Entries))
			for _, kv := range block.Entries {
				parts = append(parts, fmt.Sprintf("%s=%s", kv.Key, exprToPyWithCtx(kv.Value, ctx)))
			}
			kwargs = strings.Join(parts, ", ")
		}
	}

	v := bindingOrPlaceholder(binding, "_saved")
	fmt.Fprintf(b, "%s%s = schema.%s(%s)\n", indent, v, className, kwargs)
	fmt.Fprintf(b, "%sdb.add(%s)\n", indent, v)
	emitCommitOrFlush(b, ctx, indent)
	fmt.Fprintf(b, "%sdb.refresh(%s)\n", indent, v)
}

// emitCommitOrFlush writes `db.commit()`, or `db.flush()` when the caller is
// emitting inside a try body that emitTryRecover wrapped in
// `with db.begin_nested():` (ctx.inTxn) — flush keeps db.refresh() working
// (it populates PKs against the open transaction) without ending the nested
// transaction early; the wrapper commits once after the whole body succeeds.
func emitCommitOrFlush(b *strings.Builder, ctx *bodyCtx, indent string) {
	if ctx.inTxn {
		fmt.Fprintf(b, "%sdb.flush()\n", indent)
		return
	}
	fmt.Fprintf(b, "%sdb.commit()\n", indent)
}

// emitFetch: `binding = fetch M(id)` → binding = db.get(schema.M, id)
func emitFetch(b *strings.Builder, binding string, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	if len(fn.Args) < 1 {
		return
	}
	model := identName(fn.Args[0])
	className := common.PascalCase(model)
	idExpr := "None"
	if len(fn.Args) >= 2 {
		idExpr = exprToPyWithCtx(fn.Args[1], ctx)
	}
	v := bindingOrPlaceholder(binding, "_fetched")
	fmt.Fprintf(b, "%s%s = db.get(schema.%s, %s)\n", indent, v, className, idExpr)
}

// emitQuery: plain `query M` → list; `query M paginate(page, per_page)` →
// SimpleNamespace(items=..., total=...); `query M where(c1 == v1, ...)` adds
// a .where() chain; `query M order(col, asc|desc)` adds .order_by() per call;
// `query M ... first` returns .scalars().first().
func emitQuery(b *strings.Builder, binding string, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	if len(fn.Args) < 1 {
		return
	}
	model := identName(fn.Args[0])
	className := common.PascalCase(model)
	v := bindingOrPlaceholder(binding, "_rows")

	var paginate, where *ast.FnCall
	var orders []*ast.FnCall
	hasFirst := false
	for _, arg := range fn.Args[1:] {
		if marker, ok := arg.(*ast.FnCall); ok {
			switch marker.Name {
			case "paginate":
				paginate = marker
			case "where":
				where = marker
			case "order":
				orders = append(orders, marker)
			}
		}
		if id, ok := arg.(*ast.Ident); ok && id.Name == "first" {
			hasFirst = true
		}
	}

	sel := fmt.Sprintf("select(schema.%s)", className)
	if where != nil {
		preds := whereConditions(where, className, ctx)
		switch len(preds) {
		case 1:
			sel = fmt.Sprintf("%s.where(%s)", sel, preds[0])
		case 0:
			// All predicates were unrecognised at codegen time — defensive
			// no-op; the upstream rejection should have caught this.
		default:
			sel = fmt.Sprintf("%s.where(and_(%s))", sel, strings.Join(preds, ", "))
		}
	}
	for _, ord := range orders {
		if clause := orderClause(ord, className); clause != "" {
			sel = fmt.Sprintf("%s.order_by(%s)", sel, clause)
		}
	}

	if paginate != nil {
		page := "1"
		perPage := "20"
		if len(paginate.Args) >= 1 {
			page = exprToPyWithCtx(paginate.Args[0], ctx)
		}
		if len(paginate.Args) >= 2 {
			perPage = exprToPyWithCtx(paginate.Args[1], ctx)
		}
		countSel := fmt.Sprintf("select(func.count()).select_from(schema.%s)", className)
		if where != nil && len(whereConditions(where, className, ctx)) > 0 {
			preds := whereConditions(where, className, ctx)
			if len(preds) == 1 {
				countSel = fmt.Sprintf("%s.where(%s)", countSel, preds[0])
			} else {
				countSel = fmt.Sprintf("%s.where(and_(%s))", countSel, strings.Join(preds, ", "))
			}
		}
		fmt.Fprintf(b, "%s_%s_total = db.scalar(%s)\n", indent, v, countSel)
		fmt.Fprintf(b, "%s_%s_items = db.execute(%s.offset((%s - 1) * %s).limit(%s)).scalars().all()\n",
			indent, v, sel, page, perPage, perPage)
		fmt.Fprintf(b, "%s%s = SimpleNamespace(items=_%s_items, total=_%s_total)\n", indent, v, v, v)
		return
	}

	if hasFirst {
		fmt.Fprintf(b, "%s%s = db.execute(%s).scalars().first()\n", indent, v, sel)
		return
	}
	fmt.Fprintf(b, "%s%s = db.execute(%s).scalars().all()\n", indent, v, sel)
}

// whereConditions converts a `where(...)` marker into SQLAlchemy predicates.
// Supports comparison operators (==, !=, <, >, <=, >=) and `in`:
//   - `col == val`    → `schema.M.col == val`
//   - `col != val`    → `schema.M.col != val`
//   - `col < val`     → `schema.M.col < val`
//   - `col > val`     → `schema.M.col > val`
//   - `col <= val`    → `schema.M.col <= val`
//   - `col >= val`    → `schema.M.col >= val`
//   - `col in list`   → `schema.M.col.in_(list)`
//   - `col in tags.fk` → `schema.M.col.in_([r.fk for r in tags])`
func whereConditions(where *ast.FnCall, className string, ctx *bodyCtx) []string {
	var preds []string
	for _, arg := range where.Args {
		bin, ok := arg.(*ast.BinaryExpr)
		if !ok {
			continue
		}
		lhsIdent, ok := bin.Left.(*ast.Ident)
		if !ok {
			continue
		}
		colRef := fmt.Sprintf("schema.%s.%s", className, lhsIdent.Name)
		switch bin.Op {
		case "==", "!=", "<", ">", "<=", ">=":
			preds = append(preds, fmt.Sprintf("%s %s %s",
				colRef, bin.Op, exprToPyWithCtx(bin.Right, ctx)))
		case "in":
			if fa, ok := bin.Right.(*ast.FieldAccess); ok {
				// `col in tags.tag_id` → `.in_([r.tag_id for r in tags])`
				coll := exprToPyWithCtx(fa.Base, ctx)
				preds = append(preds, fmt.Sprintf("%s.in_([r.%s for r in %s])",
					colRef, fa.Field, coll))
			} else {
				preds = append(preds, fmt.Sprintf("%s.in_(%s)",
					colRef, exprToPyWithCtx(bin.Right, ctx)))
			}
		}
	}
	return preds
}

// orderClause renders one `order(col, asc|desc)` marker as a SQLAlchemy
// column-method call: `schema.M.col.asc()` / `.desc()`. Direction defaults to
// `desc` to match the JS target's behaviour.
func orderClause(order *ast.FnCall, className string) string {
	if len(order.Args) < 1 {
		return ""
	}
	field := identOrStringName(order.Args[0])
	if field == "" {
		return ""
	}
	direction := "desc"
	if len(order.Args) >= 2 {
		if d := identOrStringName(order.Args[1]); d != "" {
			direction = d
		}
	}
	if direction != "asc" && direction != "desc" {
		direction = "desc"
	}
	return fmt.Sprintf("schema.%s.%s.%s()", className, field, direction)
}

// emitUpdate: bound or unbound, mutates an existing variable's columns and
// commits. The variable is either named explicitly (`x = update M {...}`) or
// is the same name as the model (`update M {...}` after an earlier fetch).
func emitUpdate(b *strings.Builder, binding string, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	if len(fn.Args) < 1 {
		return
	}
	target := identName(fn.Args[0])
	if _, isBinding := ctx.varModel[target]; !isBinding {
		// `target` isn't itself a bound variable — it's a model name
		// (`update account { ... }`). Resolve to the last binding declared
		// for that model, in source order (deterministic; see
		// bodyCtx.orderedBindings).
		if resolved, ok := lastBindingForModel(ctx, target); ok {
			target = resolved
		}
	}
	// If the caller bound the result to a new name, alias it to the same
	// instance (SQLAlchemy mutations stay on the original object).
	if binding != "" && binding != target {
		fmt.Fprintf(b, "%s%s = %s\n", indent, binding, target)
	}

	if len(fn.Args) >= 2 {
		if block, ok := fn.Args[1].(*ast.BlockExpr); ok {
			for _, kv := range block.Entries {
				fmt.Fprintf(b, "%s%s.%s = %s\n", indent, target, kv.Key, exprToPyWithCtx(kv.Value, ctx))
			}
		}
	}
	emitCommitOrFlush(b, ctx, indent)
	fmt.Fprintf(b, "%sdb.refresh(%s)\n", indent, target)
}

// emitDelete: `delete X` where X is either a known model name (delete the
// bound variable) or directly a variable name.
func emitDelete(b *strings.Builder, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	if len(fn.Args) < 1 {
		return
	}
	target := identName(fn.Args[0])
	if _, isBinding := ctx.varModel[target]; !isBinding {
		// `target` is a model name; look up the last-declared binding for it,
		// in source order (deterministic; see bodyCtx.orderedBindings).
		if resolved, ok := lastBindingForModel(ctx, target); ok {
			target = resolved
		}
	}
	// SQLAlchemy's db.delete() only accepts a mapped instance, not a list,
	// so `delete sessions` after a `query` would raise UnmappedInstanceError.
	// Iterate and delete each row when the target is a collection.
	if ctx.cardinality[target] == resolve.CollectionCard {
		fmt.Fprintf(b, "%sfor _row in %s:\n", indent, target)
		fmt.Fprintf(b, "%s    db.delete(_row)\n", indent)
	} else {
		fmt.Fprintf(b, "%sdb.delete(%s)\n", indent, target)
	}
	emitCommitOrFlush(b, ctx, indent)
}

// emitMap translates `|> result = map coll: save M { ... }` (bound) or
// `|> map coll: update M { ... }` (unbound) into an explicit Python for-loop.
//
// Bound + save:
//
//	result = []
//	for item in coll:
//	    _row = schema.M(...)
//	    db.add(_row)
//	    result.append(_row)
//	db.commit()
//	for _row in result:
//	    db.refresh(_row)
//
// Unbound + update:
//
//	for item in coll:
//	    <target>.<field> = <value>
//	db.commit()
func emitMap(b *strings.Builder, binding string, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	if len(fn.Args) < 2 {
		return
	}
	collection := identName(fn.Args[0])
	bodyFn, ok := fn.Args[1].(*ast.FnCall)
	if !ok {
		fmt.Fprintf(b, "%s# TODO(python): map body must be a data-op call, got %T\n", indent, fn.Args[1])
		return
	}

	// Inside the loop, `item` (Blueprint convention) is bound to the
	// element type. Track its model so FK accesses (`item.product.x`) and
	// field lookups (`item.quantity`) resolve correctly.
	innerCtx := *ctx
	innerCtx.varModel = map[string]string{}
	for k, v := range ctx.varModel {
		innerCtx.varModel[k] = v
	}
	innerCtx.fkAliases = map[string]string{}
	for k, v := range ctx.fkAliases {
		innerCtx.fkAliases[k] = v
	}
	if model, ok := ctx.varModel[collection]; ok {
		innerCtx.varModel["item"] = model
	}

	inner := indent + "    "

	switch bodyFn.Name {
	case "save":
		if len(bodyFn.Args) < 1 {
			return
		}
		model := identName(bodyFn.Args[0])
		className := common.PascalCase(model)

		// Emit FK sub-queries inside the loop (they reference `item`).
		var fkLines strings.Builder
		if len(ctx.models) > 0 {
			for _, fk := range resolve.FKAccessesInExpr(bodyFn, ctx.models, innerCtx.varModel) {
				key := fk.VarName + "." + fk.FieldName
				if _, already := innerCtx.fkAliases[key]; already {
					continue
				}
				alias := "_" + fk.FieldName
				fkClass := common.PascalCase(fk.TargetModel)
				fmt.Fprintf(&fkLines, "%s%s = db.get(schema.%s, %s.%s)\n",
					inner, alias, fkClass, fk.VarName, fk.FKColumn())
				innerCtx.fkAliases[key] = alias
			}
		}

		kwargs := ""
		if len(bodyFn.Args) >= 2 {
			if block, ok := bodyFn.Args[1].(*ast.BlockExpr); ok {
				parts := make([]string, 0, len(block.Entries))
				for _, kv := range block.Entries {
					parts = append(parts, fmt.Sprintf("%s=%s", kv.Key, exprToPyWithCtx(kv.Value, &innerCtx)))
				}
				kwargs = strings.Join(parts, ", ")
			}
		}

		// Unbound `map items: save M { ... }` is conventionally referenced
		// downstream by the snake-plural of the model (e.g. `order_items` for
		// `save order_item { ... }`) — matching the JS target. Naming the
		// result `_rows` (the old placeholder) made downstream references
		// like `sum(order_items.price_cents * ...)` NameError at runtime.
		implicitName := common.Pluralize(common.SnakeCase(model))
		result := bindingOrPlaceholder(binding, implicitName)
		fmt.Fprintf(b, "%s%s = []\n", indent, result)
		fmt.Fprintf(b, "%sfor item in %s:\n", indent, collection)
		b.WriteString(fkLines.String())
		fmt.Fprintf(b, "%s_row = schema.%s(%s)\n", inner, className, kwargs)
		fmt.Fprintf(b, "%sdb.add(_row)\n", inner)
		fmt.Fprintf(b, "%s%s.append(_row)\n", inner, result)
		emitCommitOrFlush(b, ctx, indent)
		fmt.Fprintf(b, "%sfor _row in %s:\n", indent, result)
		fmt.Fprintf(b, "%sdb.refresh(_row)\n", inner)
		// Track the result name's model + cardinality so subsequent steps
		// resolve (sum/where/etc.). Applies to both bound and unbound forms.
		trackName := binding
		if trackName == "" {
			trackName = implicitName
		}
		ctx.varModel[trackName] = model
		ctx.cardinality[trackName] = resolve.CollectionCard

	case "update":
		if len(bodyFn.Args) < 1 {
			return
		}
		// Determine the loop-local target: either the model name (the loop
		// variable `item` is the target) or a known binding.
		target := identName(bodyFn.Args[0])
		// For `map items: update product { ... }`, `product` is the target
		// model — but we don't have an existing instance per element, so the
		// FK alias `_product` (pre-emitted below) acts as the target.
		if _, isModel := isKnownModel(target, ctx.models); isModel {
			// Pre-scan FK accesses so we get a `_product = db.get(...)` to
			// mutate in-place.
			if len(ctx.models) > 0 {
				// Synthesise an FK access for the loop element pointing at
				// the target model (e.g. cart_item → product) so the loop
				// body can mutate the resolved alias.
				if itemModel, ok := innerCtx.varModel["item"]; ok {
					if _, has := resolve.ModelFieldRef(ctx.models, itemModel, target); has {
						key := "item." + target
						if _, already := innerCtx.fkAliases[key]; !already {
							innerCtx.fkAliases[key] = "_" + target
						}
					}
				}
			}
		}

		fmt.Fprintf(b, "%sfor item in %s:\n", indent, collection)

		// Emit FK sub-queries inside the loop body.
		emittedAlias := false
		if len(ctx.models) > 0 {
			if itemModel, ok := innerCtx.varModel["item"]; ok {
				if targetModel, has := resolve.ModelFieldRef(ctx.models, itemModel, target); has {
					alias := "_" + target
					fkClass := common.PascalCase(targetModel)
					fmt.Fprintf(b, "%s%s = db.get(schema.%s, item.%s_id)\n",
						inner, alias, fkClass, target)
					innerCtx.fkAliases["item."+target] = alias
					emittedAlias = true
					// Also pre-fetch FKs referenced in the body expressions.
					for _, fk := range resolve.FKAccessesInExpr(bodyFn, ctx.models, innerCtx.varModel) {
						key := fk.VarName + "." + fk.FieldName
						if _, already := innerCtx.fkAliases[key]; already {
							continue
						}
						aliasFK := "_" + fk.FieldName
						fkClassFK := common.PascalCase(fk.TargetModel)
						fmt.Fprintf(b, "%s%s = db.get(schema.%s, %s.%s)\n",
							inner, aliasFK, fkClassFK, fk.VarName, fk.FKColumn())
						innerCtx.fkAliases[key] = aliasFK
					}
				}
			}
		}
		if !emittedAlias {
			// FK aliases referenced inside the body still need pre-emission.
			for _, fk := range resolve.FKAccessesInExpr(bodyFn, ctx.models, innerCtx.varModel) {
				key := fk.VarName + "." + fk.FieldName
				if _, already := innerCtx.fkAliases[key]; already {
					continue
				}
				aliasFK := "_" + fk.FieldName
				fkClassFK := common.PascalCase(fk.TargetModel)
				fmt.Fprintf(b, "%s%s = db.get(schema.%s, %s.%s)\n",
					inner, aliasFK, fkClassFK, fk.VarName, fk.FKColumn())
				innerCtx.fkAliases[key] = aliasFK
			}
		}

		// Determine the actual Python target for the assignments.
		mutateTarget := target
		if alias, has := innerCtx.fkAliases["item."+target]; has {
			mutateTarget = alias
		} else if _, isModel := ctx.varModel[target]; !isModel {
			// `target` may be a model name without an FK; fall back to
			// the alias lookup (already handled above for FK case).
		}

		if len(bodyFn.Args) >= 2 {
			if block, ok := bodyFn.Args[1].(*ast.BlockExpr); ok {
				for _, kv := range block.Entries {
					fmt.Fprintf(b, "%s%s.%s = %s\n", inner, mutateTarget, kv.Key,
						exprToPyWithCtx(kv.Value, &innerCtx))
				}
			}
		}
		emitCommitOrFlush(b, ctx, indent)

	default:
		fmt.Fprintf(b, "%s# TODO(python): unsupported map body op %q\n", indent, bodyFn.Name)
	}
}

// isKnownModel reports whether name matches one of the declared models.
func isKnownModel(name string, models []*ast.Model) (string, bool) {
	for _, m := range models {
		if m.Name == name {
			return name, true
		}
	}
	return "", false
}

// emitLog: `log "Hello {x}"` → `print(f"Hello {x}")`. Extra args (e.g.
// `level(error)`) are dropped — Phase 3c keeps deps minimal; structured
// logging is a Phase 3d follow-up.
func emitLog(b *strings.Builder, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	if len(fn.Args) == 0 {
		fmt.Fprintf(b, "%sprint()\n", indent)
		return
	}
	fmt.Fprintf(b, "%sprint(%s)\n", indent, exprToPyWithCtx(fn.Args[0], ctx))
}

// emitWhen handles both forms:
//
//   - Inline: `|> when cond: var.field = expr` → `if cond: var.field = expr`
//     The inline expression must look like an assignment (FieldAccess `= ...`
//     or Ident `= ...`); anything else is emitted as a bare expression statement.
//   - Block: `|> when cond { ... }` → `if cond:` with the nested step / guard /
//     output / nested when statements indented one level deeper.
func emitWhen(b *strings.Builder, s *ast.WhenStmt, ctx *bodyCtx, indent string) {
	cond := exprToPyWithCtx(s.Condition, ctx)
	if s.Inline != nil {
		stmt := inlineWhenStatement(s.Inline, ctx)
		fmt.Fprintf(b, "%sif %s: %s\n", indent, cond, stmt)
		return
	}
	fmt.Fprintf(b, "%sif %s:\n", indent, cond)
	if len(s.Body) == 0 {
		fmt.Fprintf(b, "%s    pass\n", indent)
		return
	}
	inner := indent + "    "
	for _, stmt := range s.Body {
		emitBodyStmt(b, stmt, ctx, inner)
	}
}

// inlineWhenStatement renders the `|> when cond: <expr>` inline body as a
// Python statement (not expression). Today the common pattern is an assignment
// (`filters.q = q`); we render it as `filters.q = q`. Anything else falls back
// to the bare expression.
func inlineWhenStatement(e ast.Expr, ctx *bodyCtx) string {
	if bin, ok := e.(*ast.BinaryExpr); ok && bin.Op == "=" {
		return fmt.Sprintf("%s = %s",
			exprToPyWithCtx(bin.Left, ctx),
			exprToPyWithCtx(bin.Right, ctx))
	}
	return exprToPyWithCtx(e, ctx)
}

// emitTryRecover translates the block form `try { ... } recover { ... }` into
// a Python try/except. The recover body can reference `error` (bound by the
// `as error` clause; Python exceptions have no `.message`, so `error.message`
// is rewritten to `str(error)` — see exprToPyWithCtx/pyStringLiteralWithCtx).
//
// A try body with >=2 mutations and no guard/output (mirrors js/generator.go's
// tryBodyNeedsTransaction) is wrapped in `with db.begin_nested():` so a later
// failure rolls back the partial writes instead of leaving them committed:
//
//	try:
//	    with db.begin_nested():
//	        <body with db.flush() instead of db.commit()>
//	    db.commit()
//	except HTTPException:            # only when the try body has guards
//	    raise
//	except Exception as error:
//	    db.rollback()
//	    <recover body>
//
// A failed commit/flush leaves the SQLAlchemy session unusable until rolled
// back, so db.rollback() also leads the except branch for unwrapped
// try/recover whenever the endpoint touches the DB at all.
func emitTryRecover(b *strings.Builder, s *ast.TryRecover, ctx *bodyCtx, indent string) {
	inner := indent + "    "
	// When already inside a begin_nested() (ctx.inTxn), force a savepoint so
	// the inner try's writes are isolated from the outer transaction. Without
	// this, the inner try body's flush() writes go into the outer savepoint;
	// if the inner try fails, the recover branch's db.rollback() would roll
	// back the ENTIRE outer transaction — discarding the outer body's writes
	// and returning a phantom 201 from the recover body.
	wrap := tryBodyNeedsTransaction(s.Try) || ctx.inTxn
	fmt.Fprintf(b, "%stry:\n", indent)
	switch {
	case len(s.Try) == 0:
		fmt.Fprintf(b, "%spass\n", inner)
	case wrap:
		fmt.Fprintf(b, "%swith db.begin_nested():\n", inner)
		txnCtx := *ctx
		txnCtx.inTxn = true
		nested := inner + "    "
		for _, stmt := range s.Try {
			emitBodyStmt(b, stmt, &txnCtx, nested)
		}
		// When inside an outer transaction (ctx.inTxn), flush instead of
		// commit — db.commit() would commit the ENTIRE outer transaction,
		// not just release this savepoint. When this is the outermost
		// transaction, commit as before.
		if ctx.inTxn {
			fmt.Fprintf(b, "%sdb.flush()\n", inner)
		} else {
			fmt.Fprintf(b, "%sdb.commit()\n", inner)
		}
	default:
		for _, stmt := range s.Try {
			emitBodyStmt(b, stmt, ctx, inner)
		}
	}

	// A guard inside the try body raises HTTPException; without this clause
	// the generic `except Exception` below would swallow it and turn a
	// declared status (e.g. 402) into the recover branch's response.
	if stmtsHaveGuards(s.Try) {
		fmt.Fprintf(b, "%sexcept HTTPException:\n", indent)
		fmt.Fprintf(b, "%sraise\n", inner)
	}

	fmt.Fprintf(b, "%sexcept Exception as error:\n", indent)
	emittedInExcept := false
	// Suppress db.rollback() when already inside a nested transaction
	// (ctx.inTxn): the begin_nested() savepoint auto-rolls-back on
	// exception, and db.rollback() would roll back the entire outer
	// transaction instead of just this savepoint. The outer try/recover
	// (or get_db's defense-in-depth) handles the outer transaction's
	// rollback.
	if ctx.touchesDB && !ctx.inTxn {
		fmt.Fprintf(b, "%sdb.rollback()\n", inner)
		emittedInExcept = true
	}
	if len(s.Recover) == 0 {
		if !emittedInExcept {
			fmt.Fprintf(b, "%spass\n", inner)
		}
		return
	}
	recoverCtx := *ctx
	recoverCtx.inRecover = true
	for _, stmt := range s.Recover {
		emitBodyStmt(b, stmt, &recoverCtx, inner)
	}
}

// tryBodyNeedsTransaction reports whether a `try` body should be wrapped in a
// `with db.begin_nested():` block so that partial writes roll back if a later
// step in the body fails. Mirrors js/generator.go's Option A wrap: wrap only
// when the body performs >=2 mutations (a single write is already atomic)
// and contains no guard/output statement — a guard raises and an output
// returns before the wrapping `db.commit()` would run, so those bodies are
// left as a bare try/except instead.
func tryBodyNeedsTransaction(stmts []ast.ArrowStmt) bool {
	return countMutations(stmts) >= 2 && !stmtsReturnOrGuard(stmts)
}

// countMutations counts save/update/delete data ops (including those inside a
// `map` body or a `when` block) within stmts. Mirrors js/generator.go.
func countMutations(stmts []ast.ArrowStmt) int {
	n := 0
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.StepStmt:
			if isMutationExpr(v.Expr) {
				n++
			}
		case *ast.WhenStmt:
			n += countMutations(v.Body)
		case *ast.TryRecover:
			n += countMutations(v.Try) + countMutations(v.Recover)
		}
	}
	return n
}

func isMutationExpr(e ast.Expr) bool {
	fn, ok := e.(*ast.FnCall)
	if !ok {
		return false
	}
	switch fn.Name {
	case "save", "update", "delete":
		return true
	case "map":
		if len(fn.Args) >= 2 {
			return isMutationExpr(fn.Args[1])
		}
	}
	return false
}

// stmtsReturnOrGuard reports whether stmts contains a statement that would
// exit the wrapped block early (an output `->` or a `guard`), recursing into
// `when` blocks. Mirrors js/generator.go's stmtsReturn.
func stmtsReturnOrGuard(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.OutputStmt:
			return true
		case *ast.GuardStmt:
			return true
		case *ast.WhenStmt:
			if stmtsReturnOrGuard(v.Body) {
				return true
			}
		case *ast.TryRecover:
			if stmtsReturnOrGuard(v.Try) || stmtsReturnOrGuard(v.Recover) {
				return true
			}
		}
	}
	return false
}

// emitGuard: `guard <cond> -> NNN "msg"` → `if not (<cond>): raise HTTPException(...)`
func emitGuard(b *strings.Builder, s *ast.GuardStmt, ctx *bodyCtx, indent string) {
	cond := exprToPyWithCtx(s.Condition, ctx)
	status := s.Status
	if status == "" {
		status = "400"
	}
	fmt.Fprintf(b, "%sif not (%s):\n", indent, cond)
	fmt.Fprintf(b, "%s    raise HTTPException(status_code=%s, detail=%s)\n",
		indent, status, pyStringLiteral(s.Message))
}

// emitReturn: BlockExpr outputs become a JSONResponse around a dict-comprehension-
// free literal wrapped in jsonable_encoder (so UUID + datetime serialize cleanly).
// StringLit outputs use PlainTextResponse. Status 204 drops the body.
func emitReturn(b *strings.Builder, s *ast.OutputStmt, ctx *bodyCtx, indent string) {
	status := s.Status
	if status == "" {
		status = "200"
	}
	if status == "204" {
		fmt.Fprintf(b, "%sreturn Response(status_code=204)\n", indent)
		return
	}
	switch v := s.Value.(type) {
	case *ast.StringLit:
		fmt.Fprintf(b, "%sreturn PlainTextResponse(%s, status_code=%s)\n",
			indent, pyStringLiteral(v.Value), status)
	case *ast.BlockExpr:
		parts := make([]string, 0, len(v.Entries))
		for _, kv := range v.Entries {
			parts = append(parts, fmt.Sprintf("%q: %s", kv.Key, exprToPyWithCtx(kv.Value, ctx)))
		}
		body := "{" + strings.Join(parts, ", ") + "}"
		fmt.Fprintf(b, "%sreturn JSONResponse(jsonable_encoder(%s), status_code=%s)\n", indent, body, status)
	default:
		fmt.Fprintf(b, "%sreturn JSONResponse(jsonable_encoder(%s), status_code=%s)\n",
			indent, exprToPyWithCtx(s.Value, ctx), status)
	}
}

// exprToPyWithCtx is exprToPy + identifier resolution for variables the body
// has already bound. FieldAccess gets special-cased: a `var.fkField.x` access
// where ctx.fkAliases knows about (var, fkField) rewrites to `alias.x`.
func exprToPyWithCtx(e ast.Expr, ctx *bodyCtx) string {
	switch v := e.(type) {
	case *ast.StringLit:
		return pyStringLiteralWithCtx(v.Value, ctx)
	case *ast.IntLit:
		return v.Value
	case *ast.FloatLit:
		return v.Value
	case *ast.BoolLit:
		if v.Value {
			return "True"
		}
		return "False"
	case *ast.NullLit:
		return "None"
	case *ast.Ident:
		return v.Name
	case *ast.FieldAccess:
		// Python exceptions have no `.message` attribute (unlike JS Error);
		// `str(error)` is the portable equivalent. Only rewrite inside a
		// recover scope — `error` isn't bound anywhere else.
		if ctx.inRecover && v.Field == "message" {
			if baseIdent, ok := v.Base.(*ast.Ident); ok && baseIdent.Name == "error" {
				return "str(error)"
			}
		}
		// `header.X` in middleware context maps to the snake-cased FastAPI
		// Header parameter (e.g. `header.Authorization` → `authorization`).
		// We don't gate this on a middleware-only flag because endpoint
		// bodies never legitimately reference `header.X` directly.
		if baseIdent, ok := v.Base.(*ast.Ident); ok && baseIdent.Name == "header" {
			return common.SnakeCase(v.Field)
		}
		// Two-level FK access: `var.fkField.x` → `alias.x` when the alias
		// has been pre-emitted. Single-level `var.field` is left alone.
		if inner, ok := v.Base.(*ast.FieldAccess); ok {
			if baseIdent, ok := inner.Base.(*ast.Ident); ok {
				key := baseIdent.Name + "." + inner.Field
				if alias, ok := ctx.fkAliases[key]; ok {
					return alias + "." + v.Field
				}
			}
		}
		// `.count` on a collection binding resolves to Python `len(x)` —
		// `list.count` is the method object, not a number, so the original
		// emit (`items.count > 0`) would TypeError at runtime.
		if v.Field == "count" {
			if baseIdent, ok := v.Base.(*ast.Ident); ok {
				if ctx.cardinality[baseIdent.Name] == resolve.CollectionCard {
					return fmt.Sprintf("len(%s)", baseIdent.Name)
				}
			}
		}
		return exprToPyWithCtx(v.Base, ctx) + "." + v.Field
	case *ast.BinaryExpr:
		return fmt.Sprintf("(%s %s %s)",
			exprToPyWithCtx(v.Left, ctx),
			translateBinaryOp(v.Op),
			exprToPyWithCtx(v.Right, ctx))
	case *ast.UnaryExpr:
		return translateUnaryOp(v.Op) + " " + exprToPyWithCtx(v.Operand, ctx)
	case *ast.ParenExpr:
		return "(" + exprToPyWithCtx(v.Expr, ctx) + ")"
	}
	return exprToPy(e)
}

// pyStringLiteralWithCtx is pyStringLiteral with FK-alias rewriting applied
// to `{var.field.x}` interpolations. e.g. `"Stock {product.stock}"` is left
// alone, but `"Bought {item.product.name}"` becomes `f"Bought {_product.name}"`
// when `_product` is an active FK alias. Inside a recover scope, `{error.message}`
// is rewritten to `{str(error)}` — Python exceptions have no `.message`.
func pyStringLiteralWithCtx(s string, ctx *bodyCtx) string {
	if !strings.Contains(s, "{") || ctx == nil {
		return pyStringLiteral(s)
	}
	out := s
	if ctx.inRecover {
		out = strings.ReplaceAll(out, "{error.message}", "{str(error)}")
	}
	if len(ctx.fkAliases) == 0 {
		return pyStringLiteral(out)
	}
	// Apply rewrites in a deterministic order so two builds emit the same
	// text — map iteration would break idempotency.
	keys := make([]string, 0, len(ctx.fkAliases))
	for k := range ctx.fkAliases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		alias := ctx.fkAliases[key]
		// Only rewrite when followed by `.` inside `{...}`. The replace is
		// scoped to "{<key>." so we don't accidentally touch unrelated text.
		out = strings.ReplaceAll(out, "{"+key+".", "{"+alias+".")
	}
	return pyStringLiteral(out)
}

func translateBinaryOp(op string) string {
	switch op {
	case "==":
		return "=="
	case "!=":
		return "!="
	case "and":
		return "and"
	case "or":
		return "or"
	}
	return op
}

func translateUnaryOp(op string) string {
	if op == "not" {
		return "not"
	}
	return op
}

func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// identOrStringName accepts either an Ident or a StringLit and returns its
// textual name. Used for `order(col, asc)` where the direction can be written
// either way in the .bp source.
func identOrStringName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StringLit:
		return v.Value
	}
	return ""
}

func bindingOrPlaceholder(binding, placeholder string) string {
	if binding != "" {
		return binding
	}
	return placeholder
}

// endpointTouchesDB reports whether an endpoint has at least one data op
// anywhere in its body — including nested when / try / map bodies — so the
// handler signature can include `db: Session = Depends(get_db)`.
func endpointTouchesDB(ep *ast.Endpoint) bool {
	return stmtsTouchDB(ep.Stmts)
}

func stmtsTouchDB(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		if stmtTouchesDB(s) {
			return true
		}
	}
	return false
}

func stmtTouchesDB(s ast.ArrowStmt) bool {
	switch v := s.(type) {
	case *ast.StepStmt:
		if fn, ok := v.Expr.(*ast.FnCall); ok {
			switch fn.Name {
			case "save", "fetch", "query", "update", "delete":
				return true
			case "map":
				// map's body is a data-op call; that counts.
				if len(fn.Args) >= 2 {
					if body, ok := fn.Args[1].(*ast.FnCall); ok {
						switch body.Name {
						case "save", "fetch", "query", "update", "delete":
							return true
						}
					}
				}
			}
		}
	case *ast.WhenStmt:
		return stmtsTouchDB(v.Body)
	case *ast.TryRecover:
		return stmtsTouchDB(v.Try) || stmtsTouchDB(v.Recover)
	case *ast.GuardStmt:
		// Guards on FK-resolved fields still need the db dependency to
		// pre-fetch the FK aliases. Be conservative: any guard counts only
		// when it references a known FK, but for now we say no — top-level
		// guards on inputs don't need the db. The pre-scan above catches
		// the common case (a guard sits between fetch and the rest).
		return false
	}
	return false
}

// endpointHasGuards reports whether an endpoint emits any HTTPException —
// determines whether the route file needs to import HTTPException.
func endpointHasGuards(ep *ast.Endpoint) bool {
	return stmtsHaveGuards(ep.Stmts)
}

func stmtsHaveGuards(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.GuardStmt:
			return true
		case *ast.WhenStmt:
			if stmtsHaveGuards(v.Body) {
				return true
			}
		case *ast.TryRecover:
			if stmtsHaveGuards(v.Try) || stmtsHaveGuards(v.Recover) {
				return true
			}
		}
	}
	return false
}

// endpointHasPaginatedQuery reports whether the endpoint emits any
// SimpleNamespace pagination wrapper — for import gating.
func endpointHasPaginatedQuery(ep *ast.Endpoint) bool {
	return stmtsHavePaginated(ep.Stmts)
}

func stmtsHavePaginated(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		if stepIsQueryWithMarker(s, "paginate") {
			return true
		}
		switch v := s.(type) {
		case *ast.WhenStmt:
			if stmtsHavePaginated(v.Body) {
				return true
			}
		case *ast.TryRecover:
			if stmtsHavePaginated(v.Try) || stmtsHavePaginated(v.Recover) {
				return true
			}
		}
	}
	return false
}

// endpointHasMultiWhere reports whether the endpoint emits a `.where(and_(...))`,
// which requires `and_` in the sqlalchemy import. A single-predicate where
// uses `.where(...)` directly and doesn't need it.
func endpointHasMultiWhere(ep *ast.Endpoint) bool {
	return stmtsHaveMultiWhere(ep.Stmts)
}

func stmtsHaveMultiWhere(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.StepStmt:
			fn, ok := v.Expr.(*ast.FnCall)
			if !ok || fn.Name != "query" {
				continue
			}
			for _, arg := range fn.Args[1:] {
				if marker, ok := arg.(*ast.FnCall); ok && marker.Name == "where" {
					if len(marker.Args) > 1 {
						return true
					}
				}
			}
		case *ast.WhenStmt:
			if stmtsHaveMultiWhere(v.Body) {
				return true
			}
		case *ast.TryRecover:
			if stmtsHaveMultiWhere(v.Try) || stmtsHaveMultiWhere(v.Recover) {
				return true
			}
		}
	}
	return false
}

// endpointHasOrderedQuery reports whether the endpoint emits a `.order_by(...)`
// call. SQLAlchemy column methods (`schema.M.col.asc()/desc()`) don't need any
// extra imports, so this is only used to keep the import gate honest if we
// later switch to top-level `asc()`/`desc()` helpers.
func endpointHasOrderedQuery(ep *ast.Endpoint) bool {
	return stmtsHaveOrder(ep.Stmts)
}

func stmtsHaveOrder(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.StepStmt:
			fn, ok := v.Expr.(*ast.FnCall)
			if !ok || fn.Name != "query" {
				continue
			}
			for _, arg := range fn.Args[1:] {
				if marker, ok := arg.(*ast.FnCall); ok && marker.Name == "order" {
					return true
				}
			}
		case *ast.WhenStmt:
			if stmtsHaveOrder(v.Body) {
				return true
			}
		case *ast.TryRecover:
			if stmtsHaveOrder(v.Try) || stmtsHaveOrder(v.Recover) {
				return true
			}
		}
	}
	return false
}

func stepIsQueryWithMarker(s ast.ArrowStmt, markerName string) bool {
	step, ok := s.(*ast.StepStmt)
	if !ok {
		return false
	}
	fn, ok := step.Expr.(*ast.FnCall)
	if !ok || fn.Name != "query" {
		return false
	}
	for _, arg := range fn.Args[1:] {
		if m, ok := arg.(*ast.FnCall); ok && m.Name == markerName {
			return true
		}
	}
	return false
}
