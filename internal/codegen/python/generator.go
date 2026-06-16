// Package python generates a runnable FastAPI project from a Blueprint AST.
//
// What this package supports today (Phase 2):
//   - The `blueprint { name version port runtime database? }` block
//   - REST endpoints with a static body (`<-` inputs + a single `->` output);
//     endpoint bodies with `|>` data ops are Phase 3.
//   - `model` declarations → SQLAlchemy 2.0 declarative classes,
//     Pydantic v2 read models, `src/lib/db.py` sync session + `get_db`
//     dependency, and a working Alembic config (`alembic.ini` +
//     `alembic/env.py` + `alembic/versions/`) wired to the schema's metadata.
//   - Endpoint grouping by resource (one file per resource, matching the JS
//     target convention).
//
// Still rejected with a clear roadmap error (middleware, pipes, fns, workers,
// schedules, subscribe, content, state machines, analytics, save migrations,
// endpoint bodies with `|>` data ops, STREAM/WS). The unsupported-feature
// list IS the roadmap; track progress in BACKLOG.md.
package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

// Generator produces a Python/FastAPI project from a Blueprint AST.
type Generator struct {
	file       *ast.File
	sourceFile string
	genTests   bool
}

// WithGenTests enables auto-generated contract tests under tests/ backed by a
// testcontainers-managed Postgres. Mirrors js.Generator.WithGenTests so the
// CLI flag dispatches uniformly across targets.
func (g *Generator) WithGenTests(enabled bool) *Generator {
	g.genTests = enabled
	return g
}

// models returns every `model` declaration in the parsed file. Used by the
// endpoint-body emitter for FK resolution; computed on demand because the
// generator is single-use per Generate call.
// userFnNames returns the set of declared fn + pipe names — the names a
// step call can dispatch to. Endpoint bodies and middleware bodies use
// this set to distinguish a Blueprint builtin (save/fetch/...) from a
// user-defined function whose call must go through the generated wrapper.
func (g *Generator) userFnNames() map[string]bool {
	if g.file == nil {
		return nil
	}
	out := map[string]bool{}
	for _, b := range g.file.Blocks {
		switch n := b.(type) {
		case *ast.Fn:
			out[n.Name] = true
		case *ast.Pipe:
			out[n.Name] = true
		}
	}
	return out
}

func (g *Generator) middlewares() []*ast.Middleware {
	if g.file == nil {
		return nil
	}
	var out []*ast.Middleware
	for _, b := range g.file.Blocks {
		if mw, ok := b.(*ast.Middleware); ok {
			out = append(out, mw)
		}
	}
	return out
}

func (g *Generator) fns() []*ast.Fn {
	if g.file == nil {
		return nil
	}
	var out []*ast.Fn
	for _, b := range g.file.Blocks {
		if fn, ok := b.(*ast.Fn); ok {
			out = append(out, fn)
		}
	}
	return out
}

func (g *Generator) models() []*ast.Model {
	if g.file == nil {
		return nil
	}
	var out []*ast.Model
	for _, b := range g.file.Blocks {
		if m, ok := b.(*ast.Model); ok {
			out = append(out, m)
		}
	}
	return out
}

// New creates a new Python generator.
func New() *Generator { return &Generator{} }

// Files implements codegen.Generator: it returns the generated Python project
// as in-memory OutputFiles without touching disk. It returns an error
// explaining which features are not yet supported when the spec uses any.
func (g *Generator) Files(file *ast.File) ([]codegen.OutputFile, error) {
	g.file = file
	g.sourceFile = file.Loc.File
	if g.sourceFile == "" {
		g.sourceFile = "main.bp"
	}
	return g.generateAll()
}

// Generate writes the Python project to outDir. Returns an error explaining
// which features are not yet supported when the spec uses any of them.
func (g *Generator) Generate(file *ast.File, outDir string) error {
	files, err := g.Files(file)
	if err != nil {
		return err
	}
	return codegen.WriteOutputFiles(outDir, files)
}

func (g *Generator) generateAll() ([]codegen.OutputFile, error) {
	if g.file.Blueprint == nil {
		return nil, fmt.Errorf("python target: missing blueprint block (this should have been caught by the checker)")
	}
	if missing := g.unsupportedFeatures(); len(missing) > 0 {
		return nil, fmt.Errorf(
			"python target does not yet support: %s.\n"+
				"  Track progress in BACKLOG.md (\"Python target\") and docs/production-readiness.md.\n"+
				"  Use --target node for the full feature set today.",
			strings.Join(missing, ", "))
	}

	bp := g.file.Blueprint
	var endpoints []*ast.Endpoint
	var streams []*ast.StreamEndpoint
	var wss []*ast.WsEndpoint
	var secrets []*ast.Secret
	var models []*ast.Model
	var fns []*ast.Fn
	var mws []*ast.Middleware
	for _, b := range g.file.Blocks {
		switch n := b.(type) {
		case *ast.Endpoint:
			endpoints = append(endpoints, n)
		case *ast.StreamEndpoint:
			streams = append(streams, n)
		case *ast.WsEndpoint:
			wss = append(wss, n)
		case *ast.Secret:
			secrets = append(secrets, n)
		case *ast.Model:
			models = append(models, n)
		case *ast.Fn:
			fns = append(fns, n)
		case *ast.Middleware:
			mws = append(mws, n)
		}
	}
	hasDB := blueprintEntry(bp, "database") != ""
	hasCache := blueprintEntry(bp, "cache") != ""

	var out []codegen.OutputFile
	out = append(out, g.genPyProjectTOML(bp, hasDB, hasCache, len(streams) > 0))
	out = append(out, g.genReadme(bp, hasDB))
	out = append(out, emptyInit("src/__init__.py", g.sourceFile))
	out = append(out, emptyInit("src/lib/__init__.py", g.sourceFile))
	out = append(out, emptyInit("src/routes/__init__.py", g.sourceFile))
	out = append(out, g.genEnvPy(secrets, hasDB, hasCache))
	out = append(out, g.genAppPy(bp, endpoints))

	// Phase 2: data layer (models + db session + alembic) when the spec
	// declares any models. Endpoints can still be Phase 1 static bodies.
	if len(models) > 0 {
		out = append(out, emptyInit("src/models/__init__.py", g.sourceFile))
		out = append(out, g.genSchemaPy(models))
		out = append(out, g.genPydanticPy(models))
	}
	if hasDB {
		out = append(out, g.genDBPy())
		out = append(out, g.genAlembicIni())
		out = append(out, g.genAlembicEnv())
		out = append(out, g.genAlembicScriptMako())
		out = append(out, g.genAlembicVersionsInit())
	}
	if hasCache {
		out = append(out, g.genCachePy())
	}

	// Phase 3d/5: declared functions + middleware. fn declarations produce
	// a generated wrapper and a user-owned scaffold (`raise NotImplementedError`);
	// middleware declarations produce FastAPI dependency functions that
	// endpoints reference via `use <name>`.
	if len(fns) > 0 {
		out = append(out, g.genFunctions(fns)...)
	}
	if len(mws) > 0 {
		out = append(out, g.genMiddlewares(mws)...)
	}

	groups := map[string][]*ast.Endpoint{}
	var order []string
	for _, ep := range endpoints {
		r := common.ExtractResource(ep.Path)
		if _, ok := groups[r]; !ok {
			order = append(order, r)
		}
		groups[r] = append(groups[r], ep)
	}
	sort.Strings(order)
	for _, r := range order {
		out = append(out, g.genRoute(r, groups[r]))
	}

	// Phase 5: STREAM (Server-Sent Events) and WebSocket endpoints, grouped
	// by resource (same convention REST routes use) so multiple SSE streams
	// or WS handlers on the same resource share one file. The lean REST-route
	// imports don't drag SSE / WebSocket helpers in because these are
	// separate files.
	streamByResource := map[string][]*ast.StreamEndpoint{}
	var streamOrder []string
	for _, se := range streams {
		r := streamResource(se.Path)
		if _, ok := streamByResource[r]; !ok {
			streamOrder = append(streamOrder, r)
		}
		streamByResource[r] = append(streamByResource[r], se)
	}
	sort.Strings(streamOrder)
	for _, r := range streamOrder {
		out = append(out, g.genStreamRouteFile(r, streamByResource[r]))
	}

	wsByResource := map[string][]*ast.WsEndpoint{}
	var wsOrder []string
	for _, we := range wss {
		r := wsResource(we.Path)
		if _, ok := wsByResource[r]; !ok {
			wsOrder = append(wsOrder, r)
		}
		wsByResource[r] = append(wsByResource[r], we)
	}
	sort.Strings(wsOrder)
	for _, r := range wsOrder {
		out = append(out, g.genWsRouteFile(r, wsByResource[r]))
	}

	// Auto-generated contract tests + testcontainers harness (opt-in via
	// --gen-tests). Mirrors the JS target: harness + one test_<resource>.py
	// per route group. Real Postgres rather than an in-memory shim because
	// SQLite/PGlite dialect drifts too far from Postgres FK/JSON/enum semantics.
	if g.genTests && len(endpoints) > 0 {
		out = append(out, g.genAutoTests(endpoints, models, secrets, hasDB)...)
	}
	return out, nil
}

// unsupportedFeatures walks the file blocks and returns a sorted, deduplicated
// list of feature names that this phase doesn't handle. Empty result = clean
// for codegen. Phase 1 scope deliberately mirrors what hello-world.bp uses;
// every other example will report what it needs and exit cleanly.
func (g *Generator) unsupportedFeatures() []string {
	seen := map[string]bool{}
	add := func(name string) { seen[name] = true }

	// `database` (P2) and `cache` (P5) are supported; `storage` still isn't.
	if blueprintEntry(g.file.Blueprint, "storage") != "" {
		add("`storage`")
	}

	for _, b := range g.file.Blocks {
		switch n := b.(type) {
		case *ast.Content:
			add("`content` declarations")
		case *ast.Pipe:
			add("`pipe` declarations")
		case *ast.Worker:
			add("`worker` declarations")
		case *ast.Schedule:
			add("`schedule` declarations")
		case *ast.Subscribe:
			add("`subscribe` declarations")
		case *ast.External:
			add("`external` declarations")
		case *ast.StateMachine:
			add("`state` machines")
		case *ast.Analytics:
			add("`analytics` declarations")
		case *ast.SaveSchema:
			add("`save` declarations")
		case *ast.Translation:
			add("`translation` declarations")
		case *ast.Locale:
			add("`locale` declarations")
		case *ast.Endpoint:
			for _, reason := range g.complexEndpointFeatures(n) {
				add(reason)
			}
		case *ast.Test, *ast.TestGroup, *ast.Fixture:
			// Authored tests are silently ignored in phase 1.
		}
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// complexEndpointFeatures reports which Phase-3-unsupported features an
// endpoint uses. Returning the specific feature name (rather than a single
// "complex endpoint" flag) lets the unsupported-features error point users at
// the right BACKLOG row.
//
// Supported in Phase 3: input + output + `guard` + `|>` data-op steps
// (`save`/`fetch`/`update`/`delete`/`query [paginate]`).
//
// Everything else surfaces as a clear "not yet supported" error so the
// generator never emits a half-translated handler.
func (g *Generator) complexEndpointFeatures(ep *ast.Endpoint) []string {
	seen := map[string]bool{}
	for _, s := range ep.Stmts {
		for _, r := range g.complexStmtFeatures(s) {
			seen[r] = true
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	return out
}

// complexStmtFeatures walks one ArrowStmt — used both at the endpoint top
// level and recursively inside `when` / `try` / `recover` bodies — and returns
// the list of not-yet-supported features the statement uses. Phase 3c added
// support for `try`/`recover`, the `when` inline form, `map`, `log`, `order`
// markers, and FK access; the rejections below mirror Phase 3d's roadmap.
func (g *Generator) complexStmtFeatures(s ast.ArrowStmt) []string {
	switch v := s.(type) {
	case *ast.InputStmt, *ast.OutputStmt, *ast.IntentStep, *ast.GuardStmt:
		return nil
	case *ast.StepStmt:
		return g.complexStepFeatures(v)
	case *ast.WhenStmt:
		// Inline form is supported in Phase 3c.
		var out []string
		for _, inner := range v.Body {
			out = append(out, g.complexStmtFeatures(inner)...)
		}
		return out
	case *ast.TryRecover:
		var out []string
		for _, inner := range v.Try {
			out = append(out, g.complexStmtFeatures(inner)...)
		}
		for _, inner := range v.Recover {
			out = append(out, g.complexStmtFeatures(inner)...)
		}
		return out
	default:
		return []string{fmt.Sprintf("endpoint statement %T", s)}
	}
}

// complexStepFeatures inspects a |> step and reports any sub-feature that
// Python codegen doesn't translate yet. Phase 3c expanded the supported set
// to include `order`, `map` (save/update bodies), `log`, and FK access; the
// remaining rejections (non-`==` predicates, `or`/`in` predicates, step calls
// to user-defined fn/pipe names) are tracked under Phase 3d in BACKLOG.md.
func (g *Generator) complexStepFeatures(s *ast.StepStmt) []string {
	fn, ok := s.Expr.(*ast.FnCall)
	if !ok {
		return []string{"step expressions that aren't data operations"}
	}
	// Step calls to a declared user fn / pipe are supported in Phase 3d —
	// they dispatch through the generated wrapper in src/functions/<name>.py.
	if g.userFnNames()[fn.Name] {
		return nil
	}
	switch fn.Name {
	case "save", "fetch", "update", "delete":
		return nil
	case "query":
		var out []string
		for _, arg := range fn.Args[1:] {
			if id, ok := arg.(*ast.Ident); ok && id.Name == "first" {
				continue // `first` modifier is supported in Phase 3b
			}
			marker, ok := arg.(*ast.FnCall)
			if !ok {
				continue
			}
			switch marker.Name {
			case "paginate", "order":
				continue
			case "where":
				// Phase 3b accepts where(col == val, ...) — every arg must
				// be `==` with an identifier on the LHS. Anything else
				// (text-search, !=, <, >, in, function calls) is Phase 3d.
				for _, pred := range marker.Args {
					if !isSupportedWherePredicate(pred) {
						out = append(out, "`query ... where(...)` with non-`==` predicates")
						break
					}
				}
			}
		}
		return out
	case "map":
		// Phase 3c supports save/update map bodies. Anything else (a call
		// to a user fn/pipe inside `map`, or `map` over a non-data-op body)
		// is Phase 3d.
		if len(fn.Args) < 2 {
			return []string{"`map` with no body"}
		}
		bodyFn, ok := fn.Args[1].(*ast.FnCall)
		if !ok {
			return []string{"`map` body must be a data-op call"}
		}
		switch bodyFn.Name {
		case "save", "update":
			return nil
		default:
			return []string{fmt.Sprintf("`map` body op %q", bodyFn.Name)}
		}
	case "log", "sum":
		return nil
	default:
		return []string{fmt.Sprintf("step calls to %q", fn.Name)}
	}
}

// isSupportedWherePredicate reports whether a single `where(...)` predicate is
// in the subset Phase 3b can translate (identifier `==` anything).
func isSupportedWherePredicate(e ast.Expr) bool {
	bin, ok := e.(*ast.BinaryExpr)
	if !ok || bin.Op != "==" {
		return false
	}
	_, ok = bin.Left.(*ast.Ident)
	return ok
}

// blueprintEntry returns the value of a key in the blueprint block as a string
// (or "" if absent). Local copy because the JS-side helper is in package js.
func blueprintEntry(bp *ast.Blueprint, key string) string {
	if bp == nil {
		return ""
	}
	for _, e := range bp.Entries {
		if e.Key == key {
			if s, ok := e.Value.(*ast.StringLit); ok {
				return s.Value
			}
			if id, ok := e.Value.(*ast.Ident); ok {
				return id.Name
			}
		}
	}
	return ""
}

// Ensure Generator implements codegen.Generator.
var _ codegen.Generator = (*Generator)(nil)
