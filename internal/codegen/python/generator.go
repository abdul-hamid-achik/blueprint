// Package python generates a runnable FastAPI project from a Blueprint AST.
//
// The advanced-beta target emits FastAPI routes, Pydantic/SQLAlchemy models,
// Alembic migrations, middleware dependencies, native-function scaffolds,
// endpoint data operations, and generated pytest contracts. Constructs that
// would otherwise be lost—including realtime handlers, workers, pipes,
// authored tests, and inline fn logic—must be rejected by unsupportedFeatures
// until their semantics are implemented.
package python

import (
	"fmt"
	"sort"
	"strconv"
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
	if err := codegen.RejectUnresolvedGenerateSteps(file); err != nil {
		return nil, err
	}
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
	// Models imply the PostgreSQL data layer even when the optional blueprint
	// database entry is omitted. Conversely, an explicitly configured database
	// still needs an empty Base schema so Alembic's import is always valid.
	hasDB := blueprintEntry(bp, "database") != "" || len(models) > 0
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
	if hasDB {
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

	if g.file.Blueprint != nil {
		entryNames := map[string]bool{}
		for _, entry := range g.file.Blueprint.Entries {
			if entryNames[entry.Key] {
				add(fmt.Sprintf("duplicate blueprint entry %q", entry.Key))
			}
			entryNames[entry.Key] = true
			switch entry.Key {
			case "version":
				if _, ok := entry.Value.(*ast.StringLit); !ok {
					add("non-string blueprint version")
				}
			case "port":
				value, ok := entry.Value.(*ast.IntLit)
				if !ok {
					add("non-integer blueprint port")
					continue
				}
				port, err := strconv.Atoi(value.Value)
				if err != nil || port < 1 || port > 65535 {
					add(fmt.Sprintf("blueprint port %q outside 1..65535", value.Value))
				}
			case "runtime":
				if _, ok := entry.Value.(*ast.Ident); !ok {
					add("non-identifier blueprint runtime")
				}
			case "database":
				if value := blueprintEntry(g.file.Blueprint, "database"); value != "postgres" {
					add(fmt.Sprintf("database backend %q (Python currently emits PostgreSQL)", value))
				}
			case "cache":
				if value := blueprintEntry(g.file.Blueprint, "cache"); value != "redis" {
					add(fmt.Sprintf("cache backend %q (Python currently emits Redis)", value))
				}
			default:
				add(fmt.Sprintf("blueprint entry %q", entry.Key))
			}
		}
		if len(g.file.Blueprint.Uses) > 0 {
			add("blueprint-level `use` middleware")
		}
	}

	for _, b := range g.file.Blocks {
		switch n := b.(type) {
		case *ast.Secret:
			if n.Required && n.Default != nil {
				add(fmt.Sprintf("required secret %q with a default", n.Name))
			}
			if n.Default != nil {
				if _, ok := n.Default.(*ast.StringLit); !ok {
					add(fmt.Sprintf("non-string default for secret %q", n.Name))
				}
			}
		case *ast.Model:
			if len(n.ComputedFields) > 0 {
				add("model computed fields")
			}
			for _, field := range n.Fields {
				if !validPythonIdentifier(field.Name) {
					add(fmt.Sprintf("model field %q that is not a valid Python identifier", field.Name))
				}
			}
		case *ast.Env:
			add("`env` declarations")
		case *ast.Include:
			// The CLI resolves includes before codegen. Reject a raw Include AST
			// passed through the Generator API rather than silently dropping it.
			add("unresolved `include` declarations")
		case *ast.TypeDecl:
			add("`type` declarations")
		case *ast.Alias:
			add("`alias` declarations")
		case *ast.Enum:
			add("named `enum` declarations")
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
			for _, reason := range pythonArrowNameFeatures(n.Stmts) {
				add(reason)
			}
			for _, reason := range g.complexEndpointFeatures(n) {
				add(reason)
			}
		case *ast.StreamEndpoint:
			// The current emitter only generates a ping loop and comments for
			// event/filter behavior. Reject the declaration until Phase 5b can
			// preserve the authored stream semantics.
			add("`STREAM` endpoints (event delivery and filters)")
		case *ast.WsEndpoint:
			// The current emitter accepts a socket but comments out every
			// lifecycle body. A successful build must not imply those handlers
			// execute.
			add("`WS` endpoints (lifecycle handler bodies)")
		case *ast.Fn:
			if !validPythonIdentifier(n.Name) {
				add(fmt.Sprintf("fn name %q that is not a valid Python identifier", n.Name))
			}
			for _, input := range n.Inputs {
				if !validPythonIdentifier(input.Name) {
					add(fmt.Sprintf("fn input %q that is not a valid Python identifier", input.Name))
				}
			}
			if n.Logic != nil {
				add("`fn logic` bodies")
			}
			if n.Impl != nil && n.Impl.Strategy != "node" && n.Impl.Strategy != "python" {
				add(fmt.Sprintf("`fn impl %s` strategy", n.Impl.Strategy))
			}
			for _, reason := range pythonFnImplementationFeatures(n) {
				add(reason)
			}
		case *ast.Middleware:
			if !validPythonIdentifier(n.Name) {
				add(fmt.Sprintf("middleware name %q that is not a valid Python identifier", n.Name))
			}
			for _, reason := range pythonArrowNameFeatures(n.Before) {
				add(reason)
			}
			for _, reason := range g.middlewareFeatures(n) {
				add(reason)
			}
		case *ast.Test, *ast.TestGroup, *ast.Fixture:
			add("authored tests and fixtures")
		default:
			// New declarations must make an explicit support decision here. This
			// prevents a future AST node from being silently erased by Python
			// codegen just because generateAll does not know about it yet.
			add(fmt.Sprintf("unsupported declaration %T", b))
		}
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func pythonArrowNameFeatures(stmts []ast.ArrowStmt) []string {
	var out []string
	for _, stmt := range stmts {
		switch node := stmt.(type) {
		case *ast.InputStmt:
			if !validPythonIdentifier(node.Name) {
				out = append(out, fmt.Sprintf("input name %q that is not a valid Python identifier", node.Name))
			}
		case *ast.StepStmt:
			if node.Binding != "" && !validPythonIdentifier(node.Binding) {
				out = append(out, fmt.Sprintf("step binding %q that is not a valid Python identifier", node.Binding))
			}
			if call, ok := node.Expr.(*ast.FnCall); ok && call.Name == "inject" && len(call.Args) >= 2 {
				if alias, ok := call.Args[1].(*ast.Ident); ok && !validPythonIdentifier(alias.Name) {
					out = append(out, fmt.Sprintf("injected alias %q that is not a valid Python identifier", alias.Name))
				}
			}
		case *ast.WhenStmt:
			out = append(out, pythonArrowNameFeatures(node.Body)...)
		case *ast.TryRecover:
			out = append(out, pythonArrowNameFeatures(node.Try)...)
			out = append(out, pythonArrowNameFeatures(node.Recover)...)
		}
	}
	return out
}

func pythonFnImplementationFeatures(fn *ast.Fn) []string {
	if fn == nil || fn.Impl == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, entry := range fn.Impl.Entries {
		if seen[entry.Key] {
			out = append(out, fmt.Sprintf("duplicate %q entry in fn %q implementation", entry.Key, fn.Name))
			continue
		}
		seen[entry.Key] = true
		value, ok := entry.Value.(*ast.StringLit)
		if !ok {
			out = append(out, fmt.Sprintf("non-string %q entry in fn %q implementation", entry.Key, fn.Name))
			continue
		}
		switch entry.Key {
		case "module":
			if !validPythonImplModule(value.Value) {
				out = append(out, fmt.Sprintf("unsafe or invalid Python module %q in fn %q implementation", value.Value, fn.Name))
			}
		case "func":
			if !validPythonIdentifier(value.Value) {
				out = append(out, fmt.Sprintf("invalid Python function name %q in fn %q implementation", value.Value, fn.Name))
			}
		default:
			out = append(out, fmt.Sprintf("unknown %q entry in fn %q implementation", entry.Key, fn.Name))
		}
	}
	return out
}

func validPythonImplModule(raw string) bool {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, ":") {
		return false
	}
	raw = strings.TrimPrefix(raw, "./")
	for _, extension := range []string{".js", ".ts", ".py", ".mjs", ".cjs"} {
		raw = strings.TrimSuffix(raw, extension)
	}
	if raw == "" {
		return false
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" || segment == "." || segment == ".." || !validPythonIdentifier(segment) {
			return false
		}
	}
	return true
}

func validPythonIdentifier(name string) bool {
	if name == "" || pythonKeywords[name] {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') {
				return false
			}
			continue
		}
		if r != '_' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
	"match": true, "case": true,
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
	dictInputs := map[string]bool{}
	inputNames := map[string]bool{}
	for _, meta := range ep.Meta {
		if meta.Kind != "use" {
			seen[fmt.Sprintf("endpoint metadata %q", meta.Kind)] = true
			continue
		}
		if meta.Use == nil || g.findMiddleware(meta.Use.Name) == nil {
			name := "<missing>"
			if meta.Use != nil {
				name = meta.Use.Name
			}
			seen[fmt.Sprintf("endpoint use of non-generated middleware %q", name)] = true
			continue
		}
		if len(meta.Use.Args) > 0 || meta.Use.Body != nil {
			seen[fmt.Sprintf("configured endpoint middleware use %q", meta.Use.Name)] = true
		}
	}
	if ep.OnError != nil {
		seen["endpoint `on_error` handlers"] = true
	}
	for _, s := range ep.Stmts {
		if input, ok := s.(*ast.InputStmt); ok {
			inputNames[input.Name] = true
			if pythonInputUsesDict(input.Type) {
				dictInputs[input.Name] = true
			}
			if common.IsPathParam(input.Name, ep.Path) {
				for _, constraint := range input.Constraints {
					if constraint.Kind != "required" {
						seen[fmt.Sprintf("path input constraint %q", constraint.Kind)] = true
					}
				}
			} else if (strings.EqualFold(ep.Method, "GET") || strings.EqualFold(ep.Method, "DELETE")) && !pythonQueryInputTypeSupported(input.Type) {
				seen[fmt.Sprintf("query transport for endpoint input %q", input.Name)] = true
			}
		}
		for _, r := range g.complexStmtFeatures(s) {
			seen[r] = true
		}
	}
	var collectDictBindings func([]ast.ArrowStmt)
	collectDictBindings = func(stmts []ast.ArrowStmt) {
		for _, stmt := range stmts {
			switch node := stmt.(type) {
			case *ast.StepStmt:
				if node.Binding == "" {
					continue
				}
				if call, ok := node.Expr.(*ast.FnCall); ok && g.pythonFnReturnsJSON(call.Name) {
					dictInputs[node.Binding] = true
				}
			case *ast.WhenStmt:
				collectDictBindings(node.Body)
			case *ast.TryRecover:
				collectDictBindings(node.Try)
				collectDictBindings(node.Recover)
			}
		}
	}
	collectDictBindings(ep.Stmts)
	for _, reason := range pythonBareWhereFeatures(ep) {
		seen[reason] = true
	}
	headerParams := map[string]string{}
	walkEndpointExprs(ep, func(expr ast.Expr) {
		if value, ok := expr.(*ast.StringLit); ok {
			for _, root := range pythonInterpolationRoots(value.Value) {
				if dictInputs[root] {
					seen[fmt.Sprintf("string interpolation from dictionary-backed value %q", root)] = true
				}
			}
		}
		field, ok := expr.(*ast.FieldAccess)
		if !ok {
			return
		}
		base, direct := field.Base.(*ast.Ident)
		if !direct {
			return
		}
		if dictInputs[base.Name] {
			seen[fmt.Sprintf("attribute access on dictionary-backed input %q", base.Name)] = true
		}
		if base.Name == "header" {
			param := pythonHeaderParamName(field.Field)
			if previous, exists := headerParams[param]; exists && previous != field.Field {
				seen[fmt.Sprintf("endpoint headers %q and %q normalize to the same Python parameter", previous, field.Field)] = true
			}
			headerParams[param] = field.Field
			if inputNames[param] {
				seen[fmt.Sprintf("endpoint header %q conflicts with input %q", field.Field, param)] = true
			}
		}
	})
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	return out
}

func (g *Generator) pythonFnReturnsJSON(name string) bool {
	for _, fn := range g.fns() {
		if fn.Name != name {
			continue
		}
		for _, output := range fn.Outputs {
			if ident, ok := output.Value.(*ast.Ident); ok && ident.Name == "json" {
				return true
			}
		}
	}
	return false
}

// pythonBareWhereFeatures distinguishes the supported text-search shorthand
// `where(q)` from filter accumulators and other bound values. The Python
// emitter currently interprets a bare identifier as ILIKE across string
// columns; accepting a dict/collection binding here would silently change the
// query instead of applying its key/value filters.
func pythonBareWhereFeatures(ep *ast.Endpoint) []string {
	searchInputs := map[string]bool{}
	for _, stmt := range ep.Stmts {
		input, ok := stmt.(*ast.InputStmt)
		if !ok {
			continue
		}
		if primitive, ok := input.Type.(*ast.PrimitiveType); ok && (primitive.Name == "string" || primitive.Name == "text") {
			searchInputs[input.Name] = true
		}
	}
	seen := map[string]bool{}
	var inspectStmts func([]ast.ArrowStmt)
	inspectStmts = func(stmts []ast.ArrowStmt) {
		for _, stmt := range stmts {
			switch node := stmt.(type) {
			case *ast.StepStmt:
				call, ok := node.Expr.(*ast.FnCall)
				if !ok || call.Name != "query" {
					continue
				}
				for _, arg := range call.Args[1:] {
					where, ok := arg.(*ast.FnCall)
					if !ok || where.Name != "where" {
						continue
					}
					for _, predicate := range where.Args {
						if ident, ok := predicate.(*ast.Ident); ok && !searchInputs[ident.Name] {
							seen[fmt.Sprintf("bare `where(%s)` value that is not a string endpoint input", ident.Name)] = true
						}
					}
				}
			case *ast.WhenStmt:
				inspectStmts(node.Body)
			case *ast.TryRecover:
				inspectStmts(node.Try)
				inspectStmts(node.Recover)
			}
		}
	}
	inspectStmts(ep.Stmts)
	out := make([]string, 0, len(seen))
	for reason := range seen {
		out = append(out, reason)
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
	case *ast.InputStmt:
		var out []string
		if reason := pythonInputTypeFeature(v.Type); reason != "" {
			out = append(out, reason)
		}
		for _, c := range v.Constraints {
			out = append(out, g.pythonExpressionFeatures(c.Value)...)
			if reason := pythonInputConstraintFeature(v.Type, c); reason != "" {
				out = append(out, reason)
			}
		}
		return out
	case *ast.OutputStmt:
		return g.pythonExpressionFeatures(v.Value)
	case *ast.IntentStep:
		return nil
	case *ast.GuardStmt:
		return g.pythonExpressionFeatures(v.Condition)
	case *ast.StepStmt:
		out := g.pythonStepExpressionFeatures(v.Expr)
		return append(out, g.complexStepFeatures(v)...)
	case *ast.WhenStmt:
		// Inline form is supported in Phase 3c.
		out := g.pythonExpressionFeatures(v.Condition)
		if v.Inline != nil {
			out = append(out, g.pythonExpressionFeatures(v.Inline)...)
			if assignment, ok := v.Inline.(*ast.BinaryExpr); !ok || assignment.Op != "=" {
				out = append(out, "inline `when` expressions that are not assignments")
			}
		}
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

// middlewareFeatures mirrors what genMiddleware actually emits. Endpoint
// bodies support when/try/output, but middleware codegen intentionally handles
// only before-step, guard, intent, and inject statements today.
func (g *Generator) middlewareFeatures(mw *ast.Middleware) []string {
	var out []string
	if len(mw.Entries) > 0 {
		out = append(out, "configuration-style middleware entries")
	}
	if len(mw.After) > 0 {
		out = append(out, "`middleware after` bodies")
	}
	injectCount := 0
	for _, stmt := range mw.Before {
		switch v := stmt.(type) {
		case *ast.IntentStep:
			continue
		case *ast.GuardStmt:
			out = append(out, g.pythonExpressionFeatures(v.Condition)...)
		case *ast.StepStmt:
			out = append(out, g.pythonStepExpressionFeatures(v.Expr)...)
			if isInjectStep(v) {
				injectCount++
				fn := v.Expr.(*ast.FnCall)
				if len(fn.Args) != 2 {
					out = append(out, "`inject` steps without a value and alias")
				} else {
					if _, ok := fn.Args[0].(*ast.Ident); !ok {
						out = append(out, "`inject` values that are not local identifiers")
					}
					if _, ok := fn.Args[1].(*ast.Ident); !ok {
						out = append(out, "`inject` aliases that are not identifiers")
					}
				}
				continue
			}
			fn, ok := v.Expr.(*ast.FnCall)
			if !ok {
				out = append(out, "middleware step expressions that are not calls")
				continue
			}
			if !g.userFnNames()[fn.Name] {
				switch fn.Name {
				case "fetch", "log":
					// These are the middleware operations whose imports and
					// binding semantics are implemented today.
				default:
					out = append(out, fmt.Sprintf("middleware step call %q", fn.Name))
				}
			}
		default:
			out = append(out, fmt.Sprintf("middleware statement %T", stmt))
		}
	}
	if injectCount > 1 {
		out = append(out, "middleware with multiple `inject` steps")
	}
	return out
}

// pythonInputTypeFeature reports endpoint input types whose validation or
// transport semantics the FastAPI signature emitter cannot preserve yet.
func pythonInputTypeFeature(t ast.TypeExpr) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "string", "text", "uuid", "int", "float", "bool", "timestamp", "json", "money":
			return ""
		default:
			return fmt.Sprintf("endpoint input type %q", v.Name)
		}
	case *ast.TypedJSONType:
		return "typed `json<T>` endpoint inputs"
	case *ast.ListType:
		return pythonInputTypeFeature(v.Element)
	case *ast.MapType:
		if reason := pythonInputTypeFeature(v.Key); reason != "" {
			return reason
		}
		return pythonInputTypeFeature(v.Value)
	case *ast.NamedType:
		return fmt.Sprintf("named endpoint input type %q", v.Name)
	case *ast.EnumInline:
		return "inline-enum endpoint inputs"
	case *ast.MimeTypeExpr:
		return "file/MIME endpoint inputs"
	case *ast.TranslationKeyType:
		return "translation-key endpoint inputs"
	default:
		return fmt.Sprintf("endpoint input type %T", t)
	}
}

func pythonInputConstraintFeature(t ast.TypeExpr, constraint *ast.Constraint_) string {
	if constraint == nil {
		return ""
	}
	switch constraint.Kind {
	case "required", "optional":
		return ""
	case "min", "max":
		if !pythonConstraintValueMatches(t, constraint.Value) {
			return fmt.Sprintf("endpoint input %s constraint value %T for %T", constraint.Kind, constraint.Value, t)
		}
		switch primitive := t.(type) {
		case *ast.PrimitiveType:
			switch primitive.Name {
			case "string", "text", "int", "float", "money":
				return ""
			}
		case *ast.ListType, *ast.MapType:
			return ""
		}
		return fmt.Sprintf("endpoint input %s constraint on %T", constraint.Kind, t)
	case "default":
		if pythonDefaultMatchesType(t, constraint.Value) {
			return ""
		}
		return fmt.Sprintf("endpoint input default expression %T for %T", constraint.Value, t)
	default:
		return fmt.Sprintf("endpoint input constraint %q", constraint.Kind)
	}
}

func pythonConstraintValueMatches(t ast.TypeExpr, value ast.Expr) bool {
	switch primitive := t.(type) {
	case *ast.PrimitiveType:
		switch primitive.Name {
		case "string", "text", "int":
			_, ok := value.(*ast.IntLit)
			return ok
		case "float", "money":
			switch value.(type) {
			case *ast.IntLit, *ast.FloatLit:
				return true
			}
		}
	case *ast.ListType, *ast.MapType:
		_, ok := value.(*ast.IntLit)
		return ok
	}
	return false
}

func pythonDefaultMatchesType(t ast.TypeExpr, value ast.Expr) bool {
	if _, ok := value.(*ast.NullLit); ok {
		return true
	}
	switch primitive := t.(type) {
	case *ast.PrimitiveType:
		switch primitive.Name {
		case "string", "text":
			_, ok := value.(*ast.StringLit)
			return ok
		case "int", "money":
			_, ok := value.(*ast.IntLit)
			return ok
		case "float":
			switch value.(type) {
			case *ast.IntLit, *ast.FloatLit:
				return true
			}
		case "bool":
			_, ok := value.(*ast.BoolLit)
			return ok
		case "json":
			switch value.(type) {
			case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.ListExpr, *ast.BlockExpr:
				return true
			}
		}
	case *ast.ListType:
		_, ok := value.(*ast.ListExpr)
		return ok
	case *ast.MapType:
		_, ok := value.(*ast.BlockExpr)
		return ok
	}
	return false
}

func pythonQueryInputTypeSupported(t ast.TypeExpr) bool {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		return v.Name != "json"
	case *ast.ListType:
		_, scalar := v.Element.(*ast.PrimitiveType)
		return scalar && pythonQueryInputTypeSupported(v.Element)
	default:
		return false
	}
}

func pythonInputUsesDict(t ast.TypeExpr) bool {
	switch t.(type) {
	case *ast.TypedJSONType, *ast.MapType:
		return true
	case *ast.PrimitiveType:
		return t.(*ast.PrimitiveType).Name == "json"
	default:
		return false
	}
}

// pythonExpressionFeatures is exhaustive over the current Expr interface.
// Composite expressions recurse so a future expression node nested in a list,
// call, or object is rejected before any output files are returned.
func (g *Generator) pythonExpressionFeatures(e ast.Expr) []string {
	if e == nil {
		return nil
	}
	var out []string
	switch v := e.(type) {
	case *ast.StringLit:
		for _, root := range pythonInterpolationRoots(v.Value) {
			if root == "env" || root == "header" {
				out = append(out, fmt.Sprintf("%s references in Python string interpolation", root))
			}
		}
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit,
		*ast.NullLit, *ast.NowLit, *ast.DurationLit, *ast.SizeLit,
		*ast.RateLit, *ast.Ident, *ast.PathExpr:
		return nil
	case *ast.BinaryExpr:
		out = append(out, g.pythonExpressionFeatures(v.Left)...)
		out = append(out, g.pythonExpressionFeatures(v.Right)...)
	case *ast.UnaryExpr:
		out = append(out, g.pythonExpressionFeatures(v.Operand)...)
	case *ast.FnCall:
		if !g.userFnNames()[v.Name] {
			out = append(out, fmt.Sprintf("value-expression call %q", v.Name))
		}
		for _, arg := range v.Args {
			out = append(out, g.pythonExpressionFeatures(arg)...)
		}
	case *ast.FieldAccess:
		if base, ok := v.Base.(*ast.Ident); ok && base.Name == "env" && !g.pythonEnvNames()[v.Field] {
			out = append(out, fmt.Sprintf("undeclared Python environment field env.%s", v.Field))
		}
		out = append(out, g.pythonExpressionFeatures(v.Base)...)
	case *ast.IndexAccess:
		out = append(out, g.pythonExpressionFeatures(v.Base)...)
		out = append(out, g.pythonExpressionFeatures(v.Index)...)
	case *ast.ParenExpr:
		out = append(out, g.pythonExpressionFeatures(v.Expr)...)
	case *ast.ListExpr:
		for _, element := range v.Elements {
			out = append(out, g.pythonExpressionFeatures(element)...)
		}
	case *ast.BlockExpr:
		for _, entry := range v.Entries {
			out = append(out, g.pythonExpressionFeatures(entry.Value)...)
		}
	default:
		out = append(out, fmt.Sprintf("expression %T", e))
	}
	return out
}

func pythonInterpolationRoots(value string) []string {
	var roots []string
	for i := 0; i < len(value); {
		start := strings.IndexByte(value[i:], '{')
		if start < 0 {
			break
		}
		start += i + 1
		end := strings.IndexByte(value[start:], '}')
		if end < 0 {
			break
		}
		body := strings.TrimSpace(value[start : start+end])
		rootEnd := 0
		for rootEnd < len(body) {
			ch := body[rootEnd]
			if ch != '_' && !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') && !(rootEnd > 0 && ch >= '0' && ch <= '9') {
				break
			}
			rootEnd++
		}
		if rootEnd > 0 {
			roots = append(roots, body[:rootEnd])
		}
		i = start + end + 1
	}
	return roots
}

// pythonStepExpressionFeatures validates values embedded in a supported step
// without mistaking query/log/map syntax markers for ordinary Python calls.
// Ordinary value expressions remain deliberately strict: only declared user
// functions have generated imports and callable runtime implementations.
func (g *Generator) pythonStepExpressionFeatures(e ast.Expr) []string {
	fn, ok := e.(*ast.FnCall)
	if !ok {
		return g.pythonExpressionFeatures(e)
	}
	var out []string
	for i, arg := range fn.Args {
		marker, isMarker := arg.(*ast.FnCall)
		allowedMarker := false
		if isMarker {
			switch fn.Name {
			case "query":
				// `with` is still rejected by complexStepFeatures, but it is a
				// query marker rather than a nested runtime value call. Treating it
				// structurally here keeps the fail-closed diagnostic focused.
				allowedMarker = marker.Name == "where" || marker.Name == "order" || marker.Name == "paginate" || marker.Name == "with"
			case "log":
				allowedMarker = marker.Name == "level"
			case "map":
				allowedMarker = i == 1 && (marker.Name == "save" || marker.Name == "update")
			}
		}
		if allowedMarker {
			if fn.Name == "map" {
				out = append(out, g.pythonStepExpressionFeatures(marker)...)
				continue
			}
			for _, markerArg := range marker.Args {
				out = append(out, g.pythonExpressionFeatures(markerArg)...)
			}
			continue
		}
		out = append(out, g.pythonExpressionFeatures(arg)...)
	}
	return out
}

func (g *Generator) pythonEnvNames() map[string]bool {
	names := map[string]bool{}
	if g.file == nil {
		return names
	}
	for _, block := range g.file.Blocks {
		if secret, ok := block.(*ast.Secret); ok {
			names[secret.Name] = true
		}
	}
	if g.file.Blueprint != nil {
		if blueprintEntry(g.file.Blueprint, "database") != "" || len(g.models()) > 0 {
			names["DATABASE_URL"] = true
		}
		if blueprintEntry(g.file.Blueprint, "cache") != "" {
			names["REDIS_URL"] = true
		}
	}
	return names
}

// complexStepFeatures inspects a |> step and reports any sub-feature that
// Python codegen doesn't translate yet. Phase 3c expanded the supported set
// to include `order`, `map` (save/update bodies), `log`, and FK access; the
// remaining rejections (non-`==` predicates, `or`/`in` predicates, step calls
// to user-defined fn/pipe names) are tracked under Phase 3d in BACKLOG.md.
func (g *Generator) complexStepFeatures(s *ast.StepStmt) []string {
	// A bare BlockExpr step (`|> filters = { status: "active" }`) is a dict
	// literal binding — emitStep handles it as a Python dict assignment.
	if _, ok := s.Expr.(*ast.BlockExpr); ok && s.Binding != "" {
		return nil
	}
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
			case "with":
				out = append(out, "`query ... with(...)` relationships")
			case "where":
				// where(col op val, ...) — comparison ops (==, !=, <, >,
				// <=, >=), `in`, `or`/`and` (recursive), and text-search
				// shorthand (bare ident) are supported. `like` and
				// function-call predicates are still rejected.
				for _, pred := range marker.Args {
					if !isSupportedWherePredicate(pred) {
						out = append(out, "`query ... where(...)` with unsupported predicates (expected comparison ops, `in`, `or`/`and`, or text-search ident)")
						break
					}
				}
			default:
				out = append(out, fmt.Sprintf("query modifier %q", marker.Name))
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
	case "log":
		return nil
	case "sum":
		if len(fn.Args) != 1 {
			return []string{"`sum` requires exactly one collection expression"}
		}
		if _, ok := extractSumCollection(fn.Args[0]); !ok {
			return []string{"`sum` body must reference exactly one collection"}
		}
		return nil
	default:
		return []string{fmt.Sprintf("step calls to %q", fn.Name)}
	}
}

// isSupportedWherePredicate reports whether a single `where(...)` predicate is
// in the subset Python codegen can translate: identifier <comparison-op>
// anything (==, !=, <, >, <=, >=), `col in collection.field` / `col in list`,
// `or`/`and` of supported predicates (recursive), or a bare ident (text-search
// shorthand → conditional ILIKE on text columns).
func isSupportedWherePredicate(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BinaryExpr:
		switch v.Op {
		case "==", "!=", "<", ">", "<=", ">=", "in":
			_, ok := v.Left.(*ast.Ident)
			return ok
		case "or", "and":
			return isSupportedWherePredicate(v.Left) && isSupportedWherePredicate(v.Right)
		default:
			return false
		}
	case *ast.Ident:
		return true // text-search shorthand
	default:
		return false
	}
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
