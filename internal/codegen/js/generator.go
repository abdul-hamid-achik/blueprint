// Package js generates JavaScript/TypeScript code from Blueprint AST.
package js

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// Generator produces a Node.js project from a Blueprint AST.
type Generator struct {
	sourceFile  string
	file        *ast.File
	middlewares map[string]*ast.Middleware // name -> middleware definition
	// Lookup maps for declared names (built once in generateAll).
	declaredFns       map[string]bool
	declaredPipes     map[string]bool
	declaredModels    map[string]bool
	declaredEnums     map[string]bool
	structEnums       map[string]bool // enums with struct-body variants (e.g., Plan)
	declaredExternals map[string]bool // normalized camelCase external service names
	hasStorage        bool
}

// New creates a new JS generator.
func New() *Generator {
	return &Generator{}
}

// emitCtx carries context for arrow statement code generation.
type emitCtx struct {
	kind        string            // "endpoint", "function", "middleware"
	method      string            // HTTP method for endpoints (e.g., "GET", "POST")
	path        string            // URL path for endpoints (e.g., "/api/todos/:id")
	ctxVars     map[string]bool   // identifiers injected via middleware (e.g., "auth" -> c.get('auth'))
	boundVars   map[string]string // data-op binding: model name -> bound variable (e.g., "job" -> "job")
	declared    map[string]bool   // variables already declared in current scope
	varModels   map[string]string // reverse of boundVars: variable name -> model name (e.g., "old" -> "job")
	singleVars  map[string]bool   // variables bound from fetch (single record, not a collection)
	asyncFns    map[string]bool   // function/pipe names that should be awaited
	structEnums map[string]bool   // enum names that have struct-body variants (bracket access → <Name>Config)
}

// Generate implements codegen.Generator.
func (g *Generator) Generate(file *ast.File, outDir string) error {
	g.file = file
	g.sourceFile = file.Loc.File
	if g.sourceFile == "" {
		g.sourceFile = "main.bp"
	}

	files, err := g.generateAll()
	if err != nil {
		return fmt.Errorf("codegen: %w", err)
	}

	for _, f := range files {
		path := filepath.Join(outDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, f.Content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// buildAsyncFns returns the set of camelCase function names that should be awaited in generated code.
func (g *Generator) buildAsyncFns() map[string]bool {
	fns := map[string]bool{
		"upload":        true,
		"deleteS3Object": true,
	}
	for name := range g.declaredFns {
		fns[toCamelCase(name)] = true
	}
	for name := range g.declaredPipes {
		fns[toCamelCase(name)] = true
	}
	return fns
}

// generateAll produces all output files from the AST.
func (g *Generator) generateAll() ([]codegen.OutputFile, error) {
	var files []codegen.OutputFile

	// Classify blocks
	var (
		secrets     []*ast.Secret
		envs        []*ast.Env
		types       []*ast.TypeDecl
		aliases     []*ast.Alias
		enums       []*ast.Enum
		models      []*ast.Model
		fns         []*ast.Fn
		pipes       []*ast.Pipe
		middlewares []*ast.Middleware
		endpoints   []*ast.Endpoint
		streams     []*ast.StreamEndpoint
		ws          []*ast.WsEndpoint
		workers     []*ast.Worker
		schedules   []*ast.Schedule
		externals   []*ast.External
		subscribes  []*ast.Subscribe
		tests       []*ast.Test
		fixtures    []*ast.Fixture
	)

	g.middlewares = make(map[string]*ast.Middleware)

	for _, block := range g.file.Blocks {
		switch b := block.(type) {
		case *ast.Secret:
			secrets = append(secrets, b)
		case *ast.Env:
			envs = append(envs, b)
		case *ast.TypeDecl:
			types = append(types, b)
		case *ast.Alias:
			aliases = append(aliases, b)
		case *ast.Enum:
			enums = append(enums, b)
		case *ast.Model:
			models = append(models, b)
		case *ast.Fn:
			fns = append(fns, b)
		case *ast.Pipe:
			pipes = append(pipes, b)
		case *ast.Middleware:
			middlewares = append(middlewares, b)
			g.middlewares[b.Name] = b
		case *ast.Endpoint:
			endpoints = append(endpoints, b)
		case *ast.StreamEndpoint:
			streams = append(streams, b)
		case *ast.WsEndpoint:
			ws = append(ws, b)
		case *ast.Worker:
			workers = append(workers, b)
		case *ast.Schedule:
			schedules = append(schedules, b)
		case *ast.External:
			externals = append(externals, b)
		case *ast.Subscribe:
			subscribes = append(subscribes, b)
		case *ast.Test:
			tests = append(tests, b)
		case *ast.Fixture:
			fixtures = append(fixtures, b)
		}
	}

	// Build lookup maps for import resolution
	g.declaredFns = make(map[string]bool)
	for _, fn := range fns {
		g.declaredFns[fn.Name] = true
	}
	g.declaredPipes = make(map[string]bool)
	for _, p := range pipes {
		g.declaredPipes[p.Name] = true
	}
	g.declaredModels = make(map[string]bool)
	for _, m := range models {
		g.declaredModels[m.Name] = true
	}
	g.declaredEnums = make(map[string]bool)
	g.structEnums = make(map[string]bool)
	for _, e := range enums {
		g.declaredEnums[e.Name] = true
		for _, v := range e.Variants {
			if v.Body != nil && len(v.Body.Entries) > 0 {
				g.structEnums[e.Name] = true
				break
			}
		}
	}
	g.declaredExternals = make(map[string]bool)
	for _, ext := range externals {
		g.declaredExternals[normalizeServiceName(ext.Name)] = true
	}

	bp := g.file.Blueprint
	hasDB := blueprintEntry(bp, "database") != ""
	hasCache := blueprintEntry(bp, "cache") != ""
	hasStorage := blueprintEntry(bp, "storage") != ""
	g.hasStorage = hasStorage

	// package.json
	files = append(files, g.genPackageJSON(bp, hasDB, hasCache, hasStorage, len(workers)+len(schedules) > 0, len(endpoints) > 0))

	// tsconfig.json
	files = append(files, g.genTSConfig())

	// .env.example
	files = append(files, g.genEnvExample(secrets, envs))

	// src/index.ts — entrypoint
	files = append(files, g.genIndex(bp, endpoints, streams, ws, middlewares, subscribes, workers, schedules, hasDB))

	// src/lib/env.ts — env validation
	files = append(files, g.genEnvTS(secrets, envs))

	// src/lib/errors.ts
	files = append(files, g.genErrors())

	// src/lib/db.ts (if database)
	if hasDB {
		files = append(files, g.genDB(bp))
	}

	// src/lib/storage.ts (if storage)
	if hasStorage {
		files = append(files, g.genStorage())
	}

	// src/lib/cache.ts (if cache)
	if hasCache {
		files = append(files, g.genCache())
	}

	// src/types.ts
	if len(types) > 0 || len(aliases) > 0 || len(enums) > 0 {
		files = append(files, g.genTypes(types, aliases, enums))
	}

	// src/models/schema.ts
	if len(models) > 0 {
		files = append(files, g.genSchema(models, enums))
	}

	// src/validation/schemas.ts
	if len(endpoints) > 0 {
		files = append(files, g.genValidation(endpoints))
	}

	// src/routes/<resource>.ts — group endpoints by resource
	routeGroups := make(map[string][]*ast.Endpoint)
	for _, ep := range endpoints {
		res := extractResource(ep.Path)
		routeGroups[res] = append(routeGroups[res], ep)
	}
	for res, eps := range routeGroups {
		files = append(files, g.genRoute(res, eps, hasDB))
	}

	// src/functions/<name>.ts
	for _, fn := range fns {
		files = append(files, g.genFunction(fn)...)

	}

	// src/pipes/<name>.ts
	for _, p := range pipes {
		files = append(files, g.genPipe(p))
	}

	// src/middleware/<name>.ts
	for _, mw := range middlewares {
		files = append(files, g.genMiddleware(mw))
	}

	// src/workers/<name>.ts
	for _, w := range workers {
		files = append(files, g.genWorker(w))
	}

	// src/schedules/<name>.ts
	for _, s := range schedules {
		files = append(files, g.genSchedule(s))
	}

	// src/routes/<resource>[-stream].ts — stream endpoints grouped by resource
	// Use "-stream" suffix when the resource name collides with a REST route file.
	streamGroups := make(map[string][]*ast.StreamEndpoint)
	for _, se := range streams {
		res := extractResource(se.Path)
		streamGroups[res] = append(streamGroups[res], se)
	}
	for res, ses := range streamGroups {
		fileKey := res
		if _, conflict := routeGroups[res]; conflict {
			fileKey = res + "-stream"
		}
		files = append(files, g.genStreamRoute(res, fileKey, ses))
	}

	// src/routes/<resource>[-ws].ts — ws endpoints grouped by resource
	// Use "-ws" suffix when the resource name collides with a REST or stream route file.
	wsGroups := make(map[string][]*ast.WsEndpoint)
	for _, we := range ws {
		res := extractResource(we.Path)
		wsGroups[res] = append(wsGroups[res], we)
	}
	for res, wes := range wsGroups {
		fileKey := res
		_, restConflict := routeGroups[res]
		_, streamConflict := streamGroups[res]
		if restConflict || streamConflict {
			fileKey = res + "-ws"
		}
		files = append(files, g.genWsRoute(res, fileKey, wes))
	}

	// src/subscriptions/<name>.ts + src/lib/events.ts
	if len(subscribes) > 0 {
		files = append(files, g.genEventsLib())
		for _, sub := range subscribes {
			files = append(files, g.genSubscribe(sub))
		}
	}

	// src/lib/external.ts
	if len(externals) > 0 {
		files = append(files, g.genExternal(externals))
	}

	// Dockerfile
	files = append(files, g.genDockerfile())

	// .gitignore
	files = append(files, g.genGitignore())

	// drizzle.config.ts (if database)
	if hasDB {
		files = append(files, g.genDrizzleConfig())
	}

	// test/<name>.test.ts
	for _, t := range tests {
		files = append(files, g.genTest(t, fixtures))
	}

	return files, nil
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedKeys2 returns sorted keys from a map[string]bool.
func sortedKeys2(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectBindings returns the set of variable names bound in a list of arrow statements.
func collectBindings(stmts []ast.ArrowStmt) map[string]bool {
	bindings := make(map[string]bool)
	for _, s := range stmts {
		if step, ok := s.(*ast.StepStmt); ok && step.Binding != "" {
			bindings[toCamelCase(step.Binding)] = true
		}
	}
	return bindings
}

// collectReferencedIdents returns all identifier names referenced in a list of arrow statements.
func collectReferencedIdents(stmts []ast.ArrowStmt) map[string]bool {
	refs := make(map[string]bool)
	for _, s := range stmts {
		collectIdentsFromStmt(s, refs)
	}
	return refs
}

func collectIdentsFromStmt(s ast.ArrowStmt, refs map[string]bool) {
	switch v := s.(type) {
	case *ast.StepStmt:
		collectIdentsFromExpr(v.Expr, refs)
	case *ast.OutputStmt:
		if v.Value != nil {
			collectIdentsFromExpr(v.Value, refs)
		}
	case *ast.GuardStmt:
		collectIdentsFromExpr(v.Condition, refs)
	case *ast.WhenStmt:
		collectIdentsFromExpr(v.Condition, refs)
		if v.Inline != nil {
			collectIdentsFromExpr(v.Inline, refs)
		}
		for _, stmt := range v.Body {
			collectIdentsFromStmt(stmt, refs)
		}
	case *ast.TryRecover:
		for _, stmt := range v.Try {
			collectIdentsFromStmt(stmt, refs)
		}
		for _, stmt := range v.Recover {
			collectIdentsFromStmt(stmt, refs)
		}
	}
}

func collectIdentsFromExpr(e ast.Expr, refs map[string]bool) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.Ident:
		refs[toCamelCase(v.Name)] = true
	case *ast.FieldAccess:
		collectIdentsFromExpr(v.Base, refs)
	case *ast.IndexAccess:
		collectIdentsFromExpr(v.Base, refs)
		collectIdentsFromExpr(v.Index, refs)
	case *ast.BinaryExpr:
		collectIdentsFromExpr(v.Left, refs)
		collectIdentsFromExpr(v.Right, refs)
	case *ast.UnaryExpr:
		collectIdentsFromExpr(v.Operand, refs)
	case *ast.ParenExpr:
		collectIdentsFromExpr(v.Expr, refs)
	case *ast.FnCall:
		for _, a := range v.Args {
			collectIdentsFromExpr(a, refs)
		}
	case *ast.ListExpr:
		for _, el := range v.Elements {
			collectIdentsFromExpr(el, refs)
		}
	case *ast.BlockExpr:
		for _, kv := range v.Entries {
			collectIdentsFromExpr(kv.Value, refs)
		}
	}
}

// collectStepBindingsReassigned checks which input variable names are later reassigned in steps.
func collectStepBindingsReassigned(stmts []ast.ArrowStmt) map[string]bool {
	inputNames := make(map[string]bool)
	reassigned := make(map[string]bool)

	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.InputStmt:
			inputNames[toCamelCase(v.Name)] = true
		case *ast.StepStmt:
			if v.Binding != "" {
				name := toCamelCase(v.Binding)
				if inputNames[name] {
					reassigned[name] = true
				}
			}
		}
	}
	return reassigned
}

// collectPropertyMutations returns the set of variable names that have property mutations
// via `when cond: var.field = value` inline assignment patterns.
// These need `Record<string, any>` type annotation.
func collectPropertyMutations(stmts []ast.ArrowStmt) map[string]bool {
	mutated := make(map[string]bool)
	for _, stmt := range stmts {
		if when, ok := stmt.(*ast.WhenStmt); ok && when.Inline != nil {
			if bin, ok := when.Inline.(*ast.BinaryExpr); ok && bin.Op == "=" {
				if fa, ok := bin.Left.(*ast.FieldAccess); ok {
					if ident, ok := fa.Base.(*ast.Ident); ok {
						mutated[toCamelCase(ident.Name)] = true
					}
				}
			}
		}
	}
	return mutated
}

// emitArrowStmts generates JavaScript for a sequence of arrow statements.
func (g *Generator) emitArrowStmts(b *strings.Builder, stmts []ast.ArrowStmt, indent string, ctx emitCtx) {
	// Initialize context maps if nil
	if ctx.boundVars == nil {
		ctx.boundVars = make(map[string]string)
	}
	if ctx.declared == nil {
		ctx.declared = make(map[string]bool)
	}
	if ctx.varModels == nil {
		ctx.varModels = make(map[string]string)
	}
	if ctx.singleVars == nil {
		ctx.singleVars = make(map[string]bool)
	}

	// Pre-scan: find input variables that are later reassigned (for let vs const)
	reassigned := collectStepBindingsReassigned(stmts)

	// Pre-scan: find variables that have property mutations (e.g., `when x: filters.status = x`)
	// These need `Record<string, any>` type annotation
	propMutated := collectPropertyMutations(stmts)

	// Pre-scan: for try blocks, find which bindings need hoisting
	// (bindings in try that are referenced by stmts after the try)
	hoisted := make(map[string]bool)
	for i, stmt := range stmts {
		if tr, ok := stmt.(*ast.TryRecover); ok {
			tryBindings := collectBindings(tr.Try)
			// Collect references in all stmts after this try/recover
			afterStmts := stmts[i+1:]
			afterRefs := collectReferencedIdents(afterStmts)
			for name := range tryBindings {
				if afterRefs[name] {
					hoisted[name] = true
				}
			}
		}
	}

	// Emit hoisted variable declarations before the main body
	for name := range hoisted {
		b.WriteString(fmt.Sprintf("%slet %s: any;\n", indent, name))
		ctx.declared[name] = true
	}

	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.InputStmt:
			name := toCamelCase(s.Name)
			if ctx.kind == "endpoint" {
				decl := "const"
				if reassigned[name] {
					decl = "let"
				}
				if isPathParam(s.Name, ctx.path) {
					typeName := pathParamTypeName(s.Type)
					switch typeName {
					case "uuid":
						b.WriteString(fmt.Sprintf("%s%s %s = z.string().uuid().parse(c.req.param('%s'));\n",
							indent, decl, name, s.Name))
					case "int":
						b.WriteString(fmt.Sprintf("%s%s %s = z.coerce.number().int().parse(c.req.param('%s'));\n",
							indent, decl, name, s.Name))
					default:
						b.WriteString(fmt.Sprintf("%s%s %s = c.req.param('%s');\n",
							indent, decl, name, s.Name))
					}
				} else if ctx.method == "GET" || ctx.method == "DELETE" {
					b.WriteString(fmt.Sprintf("%s%s %s = c.req.valid('query').%s;\n",
						indent, decl, name, name))
				} else {
					b.WriteString(fmt.Sprintf("%s%s %s = c.req.valid('json').%s;\n",
						indent, decl, name, name))
				}
				ctx.declared[name] = true
			}
			// In function/pipe context, inputs are function params — skip

		case *ast.StepStmt:
			// Track data-op bindings BEFORE codegen so context is available
			if s.Binding != "" {
				if fn, ok := s.Expr.(*ast.FnCall); ok && isDataOp(fn.Name) {
					if len(fn.Args) > 0 {
						if ident, ok := fn.Args[0].(*ast.Ident); ok {
							name := toCamelCase(s.Binding)
							ctx.boundVars[ident.Name] = name
							ctx.varModels[s.Binding] = ident.Name
							// fetch returns a single record; query returns a collection
							if fn.Name == "fetch" {
								ctx.singleVars[s.Binding] = true
							}
						}
					}
				}
			}

			// Special-case: delete <variable> where variable holds tracked model records
			if fn, ok := s.Expr.(*ast.FnCall); ok && fn.Name == "delete" && len(fn.Args) > 0 {
				if ident, ok := fn.Args[0].(*ast.Ident); ok {
					if modelName, tracked := ctx.varModels[ident.Name]; tracked {
						varName := toCamelCase(ident.Name)
						schemaTable := "schema." + toCamelCase(modelName)
						if ctx.singleVars[ident.Name] {
							// Single record from fetch — use eq
							b.WriteString(fmt.Sprintf("%sawait db.delete(%s).where(eq(%s.id, %s.id));\n",
								indent, schemaTable, schemaTable, varName))
						} else {
							// Collection from query — bulk delete
							b.WriteString(fmt.Sprintf("%sawait db.delete(%s).where(inArray(%s.id, %s.map((r: any) => r.id)));\n",
								indent, schemaTable, schemaTable, varName))
						}
						continue
					}
				}
			}

			// Special-case: map <variable>: <expr> — use model name as loop param and fix inner boundVars
			if fn, ok := s.Expr.(*ast.FnCall); ok && fn.Name == "map" && len(fn.Args) >= 2 {
				if ident, ok := fn.Args[0].(*ast.Ident); ok {
					if modelName, tracked := ctx.varModels[ident.Name]; tracked {
						collectionVar := toCamelCase(ident.Name)
						itemVar := toCamelCase(modelName)
						// Create inner ctx where boundVars maps modelName → itemVar
						// so update/delete in the body use itemVar.id (the loop param) not collection.id
						innerCtx := ctx
						innerCtx.boundVars = make(map[string]string)
						for k, v := range ctx.boundVars {
							innerCtx.boundVars[k] = v
						}
						innerCtx.boundVars[modelName] = itemVar
						body := exprToJSWithCtx(fn.Args[1], &innerCtx)
						b.WriteString(fmt.Sprintf("%sawait Promise.all(%s.map(async (%s: any) => %s));\n",
							indent, collectionVar, itemVar, body))
						continue
					}
				}
			}

			expr := exprToJSWithCtx(s.Expr, &ctx)
			if s.Binding != "" {
				name := toCamelCase(s.Binding)

				if ctx.declared[name] || hoisted[name] {
					// Already declared (input reassignment or hoisted) — plain assignment
					b.WriteString(fmt.Sprintf("%s%s = %s;\n", indent, name, expr))
				} else {
					// Use `let` for reassigned vars; annotate filter-accumulator objects as Record<string, any>
					decl := "const"
					if reassigned[name] {
						decl = "let"
					}
					if propMutated[name] {
						b.WriteString(fmt.Sprintf("%s%s %s: Record<string, any> = %s;\n", indent, decl, name, expr))
					} else {
						b.WriteString(fmt.Sprintf("%s%s %s = %s;\n", indent, decl, name, expr))
					}
					ctx.declared[name] = true
				}
			} else {
				b.WriteString(fmt.Sprintf("%s%s;\n", indent, expr))
			}

		case *ast.GuardStmt:
			cond := exprToJSWithCtx(s.Condition, &ctx)
			if ctx.kind == "endpoint" {
				b.WriteString(fmt.Sprintf("%sif (!(%s)) return c.json({ error: %q }, %s);\n",
					indent, cond, s.Message, s.Status))
			} else {
				b.WriteString(fmt.Sprintf("%sif (!(%s)) throw new BpError(%s, %q);\n",
					indent, cond, s.Status, s.Message))
			}

		case *ast.WhenStmt:
			cond := exprToJSWithCtx(s.Condition, &ctx)
			if s.Inline != nil {
				b.WriteString(fmt.Sprintf("%sif (%s) %s;\n", indent, cond, exprToJSWithCtx(s.Inline, &ctx)))
			} else if len(s.Body) > 0 {
				b.WriteString(fmt.Sprintf("%sif (%s) {\n", indent, cond))
				// Use a block-scoped copy of ctx so declarations inside this when-block
				// don't leak into sibling when-blocks (they're in separate JS if-scopes).
				innerCtx := ctx
				innerCtx.declared = make(map[string]bool)
				for k, v := range ctx.declared {
					innerCtx.declared[k] = v
				}
				g.emitArrowStmts(b, s.Body, indent+"  ", innerCtx)
				b.WriteString(fmt.Sprintf("%s}\n", indent))
			}

		case *ast.OutputStmt:
			status := s.Status
			if status == "" {
				status = "200"
			}
			val := exprToJSWithCtx(s.Value, &ctx)

			if ctx.kind == "endpoint" {
				// For 204 No Content, use c.body(null, 204)
				if status == "204" {
					b.WriteString(fmt.Sprintf("%sreturn c.body(null, 204);\n", indent))
				} else {
					b.WriteString(fmt.Sprintf("%sreturn c.json(%s, %s);\n", indent, val, status))
				}
			} else {
				// function/pipe/middleware context — plain return
				b.WriteString(fmt.Sprintf("%sreturn %s;\n", indent, val))
			}

		case *ast.TryRecover:
			b.WriteString(fmt.Sprintf("%stry {\n", indent))
			g.emitArrowStmts(b, s.Try, indent+"  ", ctx)
			b.WriteString(fmt.Sprintf("%s} catch (error: any) {\n", indent))
			g.emitArrowStmts(b, s.Recover, indent+"  ", ctx)
			b.WriteString(fmt.Sprintf("%s}\n", indent))
		}
	}
}

// webhookAuthSecretKey extracts the env variable name from a webhook_sig auth expression.
// Input AST: FnCall{Name:"webhook_sig", Args:[FnCall{Name:"using", Args:[FieldAccess{Base:"secret",Field:"KEY"}]}]}
// Returns the field name (e.g. "STRIPE_KEY"), or "" if not a webhook_sig auth.
func webhookAuthSecretKey(expr ast.Expr) string {
	fn, ok := expr.(*ast.FnCall)
	if !ok || fn.Name != "webhook_sig" || len(fn.Args) == 0 {
		return ""
	}
	inner, ok := fn.Args[0].(*ast.FnCall)
	if !ok || inner.Name != "using" || len(inner.Args) == 0 {
		return ""
	}
	fa, ok := inner.Args[0].(*ast.FieldAccess)
	if !ok {
		return ""
	}
	return fa.Field
}

// Ensure Generator implements codegen.Generator.
var _ codegen.Generator = (*Generator)(nil)
