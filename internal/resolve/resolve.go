// Package resolve produces semantic facts about a Blueprint AST that the
// code generator (and other downstream passes) can consume as ground truth
// instead of re-deriving heuristically at emit time.
//
// This is the first slice of the resolver/typed-IR work tracked in BACKLOG.md.
// Today it produces per-block variable facts: for each ArrowStmt that binds a
// variable from a data operation (or `map`), the resolver records the target
// model and the result's cardinality (Single from record-returning operations
// such as `fetch`/`save`/`update`, Collection from `query` without
// `paginate()` or from `map`, Paginated from `query` with `paginate()`).
// Subsequent slices will move FK access, async-ness, and
// `let`/`const` decisions out of the codegen heuristics into this package.
//
// Design constraints:
//   - resolve must not import internal/codegen — it has to be importable by
//     every code generator (JS today, future targets later).
//   - For now the resolver mirrors the current codegen's intentionally flat
//     scope: bindings inside `when` / `try` / `recover` bodies leak to the
//     surrounding block, exactly as codegen treats them today. Future slices
//     can tighten this if needed.
package resolve

import "github.com/abdul-hamid-achik/blueprint/internal/ast"

// Cardinality describes the shape of a bound variable's value.
type Cardinality int

const (
	// UnknownCard means the resolver could not determine cardinality.
	UnknownCard Cardinality = iota
	// SingleCard is one record — produced by `fetch`, `save`, `seed`, and
	// `update`, plus `query ... first`.
	SingleCard
	// CollectionCard is an unordered list of records — produced by `query`
	// without `paginate()` and by `map(...)` aggregates.
	CollectionCard
	// PaginatedCard is the `{ items, total, page, per_page }` shape produced
	// by `query ... paginate(page, per_page)`.
	PaginatedCard
)

// StepFact describes one variable binding produced by an ArrowStmt step.
type StepFact struct {
	// Name is the binding as written in the .bp source (snake_case typically).
	Name string
	// Model is the name of the model the data operation targets.
	Model string
	// Cardinality of the binding's value.
	Cardinality Cardinality
	// Relationships lists ref-backed names requested by query with(...), in
	// source order. Their targets are validated by the checker and resolved by
	// the target generator against the model declarations.
	Relationships []string
}

// BlockFacts holds the resolved facts for a block of ArrowStmts (an endpoint
// body, function `logic`, pipe body, middleware before/after, worker body,
// schedule body, test setup/cleanup).
//
// DataOps lists bindings produced by data operations (`query`, `save`, `fetch`,
// `update`, `delete`, `count`, `seed`, `import_bundle`, `export_bundle`).
// MapResults lists outer bindings produced by `map items: <data-op>` —
// separated because the current codegen tracks them under `varModels` only,
// without also registering them in `boundVars`/`singleVars`. Keeping them
// distinct lets the caller mirror that distinction exactly.
//
// Both slices are in document order so that callers seeding state in the same
// order get last-write-wins semantics identical to the old incremental path.
type BlockFacts struct {
	DataOps    []StepFact
	MapResults []StepFact
}

// ResolveBlock walks stmts and returns the block's variable facts. Nested
// `when` / `try` / `recover` bodies are walked too, matching codegen's current
// (flat) scope semantics.
func ResolveBlock(stmts []ast.ArrowStmt) *BlockFacts {
	b := &BlockFacts{}
	walkStmts(stmts, b)
	return b
}

func walkStmts(stmts []ast.ArrowStmt, b *BlockFacts) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.StepStmt:
			recordStepFact(v, b)
		case *ast.WhenStmt:
			walkStmts(v.Body, b)
		case *ast.TryRecover:
			walkStmts(v.Try, b)
			walkStmts(v.Recover, b)
		}
	}
}

func recordStepFact(s *ast.StepStmt, b *BlockFacts) {
	if s.Binding == "" {
		return
	}
	fn, ok := s.Expr.(*ast.FnCall)
	if !ok {
		return
	}

	// `|> result = map <coll>: <data-op>` — outer binding holds a collection
	// of the body's data-op model.
	if fn.Name == "map" && len(fn.Args) >= 2 {
		bodyFn, ok := fn.Args[1].(*ast.FnCall)
		if !ok || !isDataOp(bodyFn.Name) || len(bodyFn.Args) == 0 {
			return
		}
		id, ok := bodyFn.Args[0].(*ast.Ident)
		if !ok {
			return
		}
		b.MapResults = append(b.MapResults, StepFact{
			Name:        s.Binding,
			Model:       id.Name,
			Cardinality: CollectionCard,
		})
		return
	}

	if !isDataOp(fn.Name) || len(fn.Args) == 0 {
		return
	}
	id, ok := fn.Args[0].(*ast.Ident)
	if !ok {
		return
	}
	card := CollectionCard
	switch {
	case fn.Name == "fetch" || fn.Name == "save" || fn.Name == "seed" || fn.Name == "update":
		card = SingleCard
	case fn.Name == "query" && queryIsFirst(fn):
		card = SingleCard
	case fn.Name == "query" && queryIsPaginated(fn):
		card = PaginatedCard
	}
	b.DataOps = append(b.DataOps, StepFact{
		Name:          s.Binding,
		Model:         id.Name,
		Cardinality:   card,
		Relationships: queryRelationships(fn),
	})
}

func queryIsFirst(fn *ast.FnCall) bool {
	for _, arg := range fn.Args[1:] {
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == "first" {
			return true
		}
	}
	return false
}

func queryRelationships(fn *ast.FnCall) []string {
	if fn.Name != "query" || len(fn.Args) < 2 {
		return nil
	}
	var relationships []string
	for _, arg := range fn.Args[1:] {
		marker, ok := arg.(*ast.FnCall)
		if !ok || marker.Name != "with" {
			continue
		}
		for _, relationExpr := range marker.Args {
			if relation, ok := relationExpr.(*ast.Ident); ok {
				relationships = append(relationships, relation.Name)
			}
		}
	}
	return relationships
}

// isDataOp reports whether name is a Blueprint data operation. Kept in sync
// with internal/codegen/js/helpers.go: isDataOp.
func isDataOp(name string) bool {
	switch name {
	case "query", "save", "fetch", "update", "delete", "count",
		"seed", "import_bundle", "export_bundle":
		return true
	}
	return false
}

// queryIsPaginated reports whether a `query` FnCall carries a `paginate(...)`
// modifier among its trailing args. Kept in sync with
// internal/codegen/js/generator.go: queryIsPaginated.
func queryIsPaginated(fn *ast.FnCall) bool {
	if len(fn.Args) < 2 {
		return false
	}
	for _, arg := range fn.Args[1:] {
		if m, ok := arg.(*ast.FnCall); ok && m.Name == "paginate" {
			return true
		}
	}
	return false
}
