// Package js generates JavaScript/TypeScript code from Blueprint AST.
package js

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

// Generator produces a Node.js project from a Blueprint AST.
type Generator struct {
	sourceFile    string
	file          *ast.File
	models        []*ast.Model
	middlewares   map[string]*ast.Middleware // name -> middleware definition
	reactQuery    bool
	frontendOnly  bool
	genTests      bool
	propertyTests bool
	// Lookup maps for declared names (built once in generateAll).
	declaredFns       map[string]bool
	declaredPipes     map[string]bool
	declaredModels    map[string]bool
	declaredEnums     map[string]bool
	declaredSaves     map[string]bool
	structEnums       map[string]bool     // enums with struct-body variants (e.g., Plan)
	declaredExternals map[string]bool     // normalized camelCase external service names
	enumVariants      map[string][]string // enum name -> variant names (for test value synthesis)
	hasStorage        bool
}

// New creates a new JS generator.
func New() *Generator {
	return &Generator{}
}

// WithReactQuery enables optional React Query hook generation.
func (g *Generator) WithReactQuery(enabled bool) *Generator {
	g.reactQuery = enabled
	return g
}

// WithFrontendOnly enables outputting only the standalone frontend package.
func (g *Generator) WithFrontendOnly(enabled bool) *Generator {
	g.frontendOnly = enabled
	return g
}

// WithGenTests enables auto-generated contract tests with an in-memory (PGlite) harness.
func (g *Generator) WithGenTests(enabled bool) *Generator {
	g.genTests = enabled
	return g
}

// WithPropertyTests enables deterministic fast-check properties for REST
// endpoints. Property tests are additive: enabling them also emits the normal
// contract-test harness, while WithGenTests by itself keeps its existing
// contract-only behavior.
func (g *Generator) WithPropertyTests(enabled bool) *Generator {
	g.propertyTests = enabled
	return g
}

// emitCtx carries context for arrow statement code generation.
type emitCtx struct {
	kind              string              // "endpoint", "function", "middleware", "ws", "worker"
	method            string              // HTTP method for endpoints (e.g., "GET", "POST")
	path              string              // URL path for endpoints (e.g., "/api/todos/:id")
	ctxVars           map[string]bool     // identifiers injected via middleware (e.g., "auth" -> c.get('auth'))
	boundVars         map[string]string   // data-op binding: model name -> bound variable (e.g., "job" -> "job")
	declared          map[string]bool     // variables already declared in current scope
	varModels         map[string]string   // reverse of boundVars: variable name -> model name (e.g., "old" -> "job")
	singleVars        map[string]bool     // variables bound from fetch (single record, not a collection)
	asyncFns          map[string]bool     // function/pipe names that should be awaited
	structEnums       map[string]bool     // enum names that have struct-body variants (bracket access → <Name>Config)
	paginatedVars     map[string]bool     // variables bound from paginated queries (have .items/.total)
	eagerRelations    map[string][]string // query binding -> relationships requested by with(...)
	fkAliases         map[string]string   // FK relation aliases: "varName.refField" -> "_refField" (pre-fetched sub-queries)
	generator         *Generator          // back-reference for FK model lookups
	preserveBlockKeys bool                // when true, BlockExpr keys are not camelCased (for JSON response output)
}

// Files implements codegen.Generator: it returns the generated TypeScript/Node
// project as in-memory OutputFiles without touching disk.
func (g *Generator) Files(file *ast.File) ([]codegen.OutputFile, error) {
	if err := codegen.RejectUnresolvedGenerateSteps(file); err != nil {
		return nil, err
	}
	if err := rejectUnsupportedEndpointFileInputs(file); err != nil {
		return nil, err
	}
	if err := rejectExternalEmits(file); err != nil {
		return nil, err
	}
	if err := rejectExternalSubscriptionSources(file); err != nil {
		return nil, err
	}
	if err := validateExternalConfigurations(file); err != nil {
		return nil, err
	}
	if err := rejectWithLegacyQueryArgs(file); err != nil {
		return nil, err
	}
	g.file = file
	resolveTranslationKeyTypes(file)
	g.sourceFile = file.Loc.File
	if g.sourceFile == "" {
		g.sourceFile = "main.bp"
	}

	files, err := g.generateAll()
	if err != nil {
		return nil, fmt.Errorf("codegen: %w", err)
	}
	return files, nil
}

// rejectUnsupportedEndpointFileInputs prevents the Node target from claiming
// that a JSON request can carry a browser File or MIME-typed payload. The
// parser and checker retain these types for target-neutral tooling, but Node
// must fail closed until route parsing and the generated SDK both support
// multipart/form-data.
func rejectUnsupportedEndpointFileInputs(file *ast.File) error {
	if file == nil {
		return nil
	}

	aliases := make(map[string]ast.TypeExpr)
	namedFields := make(map[string][]*ast.Field)
	for _, block := range file.Blocks {
		switch decl := block.(type) {
		case *ast.Alias:
			aliases[decl.Name] = decl.Type
		case *ast.TypeDecl:
			namedFields[decl.Name] = decl.Fields
		case *ast.Model:
			namedFields[decl.Name] = decl.Fields
		case *ast.Content:
			model := decl.AsModel()
			namedFields[decl.Name] = model.Fields
		}
	}

	check := func(method, path string, stmts []ast.ArrowStmt) error {
		for _, stmt := range stmts {
			input, ok := stmt.(*ast.InputStmt)
			if !ok || !endpointTypeNeedsMultipart(input.Type, aliases, namedFields, make(map[string]bool)) {
				continue
			}
			return fmt.Errorf(
				"node target does not yet support file inputs, including MIME-typed input %q on %s %s at %s: multipart/form-data route parsing and generated SDK FormData encoding are not implemented",
				input.Name, method, path, input.Location(),
			)
		}
		return nil
	}

	for _, block := range file.Blocks {
		switch endpoint := block.(type) {
		case *ast.Endpoint:
			if err := check(endpoint.Method, endpoint.Path, endpoint.Stmts); err != nil {
				return err
			}
		case *ast.StreamEndpoint:
			if err := check("STREAM", endpoint.Path, endpoint.Stmts); err != nil {
				return err
			}
		case *ast.WsEndpoint:
			if err := check("WS", endpoint.Path, endpoint.OnConnect); err != nil {
				return err
			}
			if err := check("WS", endpoint.Path, endpoint.OnMessage); err != nil {
				return err
			}
			if err := check("WS", endpoint.Path, endpoint.OnDisconnect); err != nil {
				return err
			}
		}
	}
	return nil
}

func endpointTypeNeedsMultipart(t ast.TypeExpr, aliases map[string]ast.TypeExpr, namedFields map[string][]*ast.Field, visiting map[string]bool) bool {
	switch value := t.(type) {
	case *ast.PrimitiveType:
		return value.Name == "file"
	case *ast.MimeTypeExpr:
		return true
	case *ast.TypedJSONType:
		return endpointTypeNeedsMultipart(value.Inner, aliases, namedFields, visiting)
	case *ast.ListType:
		return endpointTypeNeedsMultipart(value.Element, aliases, namedFields, visiting)
	case *ast.MapType:
		return endpointTypeNeedsMultipart(value.Key, aliases, namedFields, visiting) ||
			endpointTypeNeedsMultipart(value.Value, aliases, namedFields, visiting)
	case *ast.NamedType:
		if visiting[value.Name] {
			return false
		}
		visiting[value.Name] = true
		defer delete(visiting, value.Name)
		if target, ok := aliases[value.Name]; ok {
			return endpointTypeNeedsMultipart(target, aliases, namedFields, visiting)
		}
		if fields, ok := namedFields[value.Name]; ok {
			for _, field := range fields {
				if endpointTypeNeedsMultipart(field.Type, aliases, namedFields, visiting) {
					return true
				}
			}
		}
	}
	return false
}

// rejectExternalEmits keeps `emit event to(service) { ... }` from being
// silently lowered to an in-process emit with the service identifier as its
// payload. External event delivery needs an explicit transport adapter.
func rejectExternalEmits(file *ast.File) error {
	if file == nil {
		return nil
	}
	validator := &externalEmitValidator{}
	ast.Walk(file, validator)
	return validator.err
}

type externalEmitValidator struct {
	ast.BaseVisitor
	err error
}

func (v *externalEmitValidator) VisitFnCall(call *ast.FnCall) bool {
	if v.err != nil || call.Name != "emit" || len(call.Args) < 2 {
		return v.err == nil
	}
	service, external := call.Args[1].(*ast.Ident)
	if !external {
		return true
	}
	v.err = fmt.Errorf(
		"node target does not yet support external emit to(%s) at %s: no external event transport adapter is generated; use an in-process emit or call an external HTTP service explicitly",
		service.Name, call.Location(),
	)
	return false
}

func fileHasCall(file *ast.File, name string) bool {
	if file == nil {
		return false
	}
	visitor := &callPresenceVisitor{name: name}
	ast.Walk(file, visitor)
	return visitor.found
}

type callPresenceVisitor struct {
	ast.BaseVisitor
	name  string
	found bool
}

func (v *callPresenceVisitor) VisitFnCall(call *ast.FnCall) bool {
	if call.Name == v.name {
		v.found = true
		return false
	}
	return !v.found
}

func rejectWithLegacyQueryArgs(file *ast.File) error {
	if file == nil {
		return nil
	}
	validator := &withLegacyQueryValidator{}
	ast.Walk(file, validator)
	return validator.err
}

type withLegacyQueryValidator struct {
	ast.BaseVisitor
	err error
}

func (v *withLegacyQueryValidator) VisitFnCall(call *ast.FnCall) bool {
	if v.err != nil || call.Name != "query" {
		return v.err == nil
	}
	hasWith := false
	hasLegacy := false
	for _, arg := range call.Args[1:] {
		if marker, ok := arg.(*ast.FnCall); ok {
			switch marker.Name {
			case "with":
				hasWith = true
				continue
			case "where", "order", "paginate":
				continue
			}
		}
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == "first" {
			continue
		}
		hasLegacy = true
	}
	if hasWith && hasLegacy {
		v.err = fmt.Errorf("node target rejects query with(...) at %s combined with legacy positional or block arguments; use structured query modifiers", call.Location())
		return false
	}
	return true
}

// rejectExternalSubscriptionSources keeps `subscribe ... from(service)` from
// masquerading as an external consumer. The generated event registry is an
// in-process bus; it can faithfully handle a source-less subscribe block, but
// it has no transport adapter that consumes events from an external service.
func rejectExternalSubscriptionSources(file *ast.File) error {
	if file == nil {
		return nil
	}
	for _, block := range file.Blocks {
		sub, ok := block.(*ast.Subscribe)
		if !ok || sub.From == "" {
			continue
		}
		return fmt.Errorf(
			"node target does not yet support subscribe %q from(%s) at %s: no external event transport is generated; remove from(%s) for an in-process subscription or provide a transport adapter",
			sub.Event, sub.From, sub.Location(), sub.From,
		)
	}
	return nil
}

// Generate implements codegen.Generator by building Files and persisting them.
func (g *Generator) Generate(file *ast.File, outDir string) error {
	files, err := g.Files(file)
	if err != nil {
		return err
	}
	return codegen.WriteOutputFiles(outDir, files)
}

func resolveTranslationKeyTypes(file *ast.File) {
	if file == nil {
		return
	}
	translations := map[string][]string{}
	for _, block := range file.Blocks {
		if tr, ok := block.(*ast.Translation); ok {
			translations[tr.Name] = append([]string(nil), tr.Keys...)
		}
	}
	ast.Walk(file, &translationKeyResolver{translations: translations})
}

type translationKeyResolver struct {
	ast.BaseVisitor
	translations map[string][]string
}

func (r *translationKeyResolver) VisitTranslationKeyType(node *ast.TranslationKeyType) bool {
	if keys, ok := r.translations[node.Namespace]; ok {
		node.Keys = append(node.Keys[:0], keys...)
	}
	return true
}

// buildAsyncFns returns the set of camelCase function names that should be
// awaited in generated code.
//
// Composition:
//   - JS-specific built-ins (`upload`, `deleteS3Object`) — owned by this target.
//   - declared fn + pipe names — resolved by internal/resolve.AsyncFunctions
//     (raw, snake_case) and camelCased here for the JS naming convention.
func (g *Generator) buildAsyncFns() map[string]bool {
	fns := map[string]bool{
		"upload":         true,
		"deleteS3Object": true,
	}
	for name := range resolve.AsyncFunctions(g.file) {
		fns[toCamelCase(name)] = true
	}
	return fns
}

// fkAccessInfo carries the JS-specific naming (camelCase var + FK column) for
// the FK access patterns resolved by internal/resolve. Conversion from the
// target-agnostic resolve.FKAccess happens at call sites via toFKAccessInfo.
type fkAccessInfo struct {
	varName     string // camelCase variable name (e.g., "item")
	fieldName   string // FK field name without _id (e.g., "product")
	targetModel string // target model name (e.g., "product")
	fkColumn    string // camelCase FK column name (e.g., "productId")
}

// toFKAccessInfo applies JS naming conventions (camelCase) to a target-agnostic
// resolve.FKAccess so downstream emit helpers can stay JS-flavored.
func toFKAccessInfo(fk resolve.FKAccess) fkAccessInfo {
	return fkAccessInfo{
		varName:     toCamelCase(fk.VarName),
		fieldName:   fk.FieldName,
		targetModel: fk.TargetModel,
		fkColumn:    toCamelCase(fk.FKColumn()),
	}
}

// fkAccessesInExpr returns JS-flavored FK access infos for the expression,
// resolved via the shared internal/resolve package.
func (g *Generator) fkAccessesInExpr(e ast.Expr, ctx *emitCtx) []fkAccessInfo {
	src := resolve.FKAccessesInExpr(e, g.models, ctx.varModels)
	out := make([]fkAccessInfo, 0, len(src))
	for _, fk := range src {
		if eagerRelationRequested(ctx, fk.VarName, fk.FieldName) {
			continue
		}
		out = append(out, toFKAccessInfo(fk))
	}
	return out
}

// fkAccessesInStmt returns JS-flavored FK access infos for the statement,
// resolved via the shared internal/resolve package.
func (g *Generator) fkAccessesInStmt(stmt ast.ArrowStmt, ctx *emitCtx) []fkAccessInfo {
	src := resolve.FKAccessesInStmt(stmt, g.models, ctx.varModels)
	out := make([]fkAccessInfo, 0, len(src))
	for _, fk := range src {
		if eagerRelationRequested(ctx, fk.VarName, fk.FieldName) {
			continue
		}
		out = append(out, toFKAccessInfo(fk))
	}
	return out
}

func eagerRelationRequested(ctx *emitCtx, variable, relation string) bool {
	if ctx == nil {
		return false
	}
	requested := ctx.eagerRelations[variable]
	if len(requested) == 0 {
		requested = ctx.eagerRelations[toCamelCase(variable)]
	}
	for _, name := range requested {
		if name == relation {
			return true
		}
	}
	return false
}

// emitFKSubQuery emits a sub-query to fetch a related record for an FK access pattern.
// Returns the alias variable name (e.g., "_product").
func (g *Generator) emitFKSubQuery(b *strings.Builder, fk fkAccessInfo, indent string) string {
	alias := "_" + toCamelCase(fk.fieldName)
	schemaTable := "schema." + toCamelCase(fk.targetModel)
	row := fmt.Sprintf("(await db.select().from(%s).where(eq(%s.id, %s.%s)))[0]",
		schemaTable, schemaTable, fk.varName, fk.fkColumn)
	if target := g.findModel(fk.targetModel); target != nil && len(target.ComputedFields) > 0 {
		row = fmt.Sprintf("schema.compute%s(%s as typeof %s.$inferSelect)",
			toPascalCase(fk.targetModel), row, schemaTable)
	}
	fmt.Fprintf(b, "%sconst %s = %s;\n", indent, alias, row)
	return alias
}

// inferMapResultBinding checks if a map body produces records of a model,
// and if subsequent statements reference the pluralized model name.
// Returns the inferred binding name if found, or empty string.
// Example: map items: save order_item { ... } -> checks if later stmts use "orderItems"
func (g *Generator) inferMapResultBinding(bodyExpr ast.Expr, laterStmts []ast.ArrowStmt) string {
	// Extract the model name from the map body (must be a save/data op)
	bodyFn, ok := bodyExpr.(*ast.FnCall)
	if !ok || !isDataOp(bodyFn.Name) || len(bodyFn.Args) == 0 {
		return ""
	}
	modelIdent, ok := bodyFn.Args[0].(*ast.Ident)
	if !ok {
		return ""
	}
	// The inferred name is the pluralized camelCase model name
	inferredName := toCamelCase(pluralize(modelIdent.Name))

	// Check if any later statement references this name
	laterRefs := collectReferencedIdents(laterStmts)
	if laterRefs[inferredName] {
		return inferredName
	}
	return ""
}

// generateAll produces all output files from the AST.
func (g *Generator) generateAll() ([]codegen.OutputFile, error) {
	var files []codegen.OutputFile

	// Classify blocks
	var (
		secrets      []*ast.Secret
		envs         []*ast.Env
		locales      []*ast.Locale
		translations []*ast.Translation
		states       []*ast.StateMachine
		analytics    []*ast.Analytics
		saves        []*ast.SaveSchema
		types        []*ast.TypeDecl
		aliases      []*ast.Alias
		enums        []*ast.Enum
		models       []*ast.Model
		contents     []*ast.Content
		fns          []*ast.Fn
		pipes        []*ast.Pipe
		middlewares  []*ast.Middleware
		endpoints    []*ast.Endpoint
		streams      []*ast.StreamEndpoint
		ws           []*ast.WsEndpoint
		workers      []*ast.Worker
		schedules    []*ast.Schedule
		externals    []*ast.External
		subscribes   []*ast.Subscribe
		tests        []*ast.Test
		fixtures     []*ast.Fixture
	)

	g.middlewares = make(map[string]*ast.Middleware)

	for _, block := range g.file.Blocks {
		switch b := block.(type) {
		case *ast.Secret:
			secrets = append(secrets, b)
		case *ast.Env:
			envs = append(envs, b)
		case *ast.Locale:
			locales = append(locales, b)
		case *ast.Translation:
			translations = append(translations, b)
		case *ast.StateMachine:
			states = append(states, b)
		case *ast.Analytics:
			analytics = append(analytics, b)
		case *ast.SaveSchema:
			saves = append(saves, b)
		case *ast.TypeDecl:
			types = append(types, b)
		case *ast.Alias:
			aliases = append(aliases, b)
		case *ast.Enum:
			enums = append(enums, b)
		case *ast.Model:
			models = append(models, b)
		case *ast.Content:
			contents = append(contents, b)
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
	for _, c := range contents {
		models = append(models, c.AsModel())
	}
	g.models = models

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
	g.declaredSaves = make(map[string]bool)
	g.structEnums = make(map[string]bool)
	g.enumVariants = make(map[string][]string)
	for _, e := range enums {
		g.declaredEnums[e.Name] = true
		for _, v := range e.Variants {
			g.enumVariants[e.Name] = append(g.enumVariants[e.Name], v.Name)
			if v.Body != nil && len(v.Body.Entries) > 0 {
				g.structEnums[e.Name] = true
			}
		}
	}
	for _, s := range saves {
		g.declaredSaves[s.Name] = true
	}
	g.declaredExternals = make(map[string]bool)
	for _, ext := range externals {
		g.declaredExternals[normalizeServiceName(ext.Name)] = true
	}

	bp := g.file.Blueprint
	// Models require the generated Drizzle/Postgres layer even when the
	// optional database entry is omitted. An explicit database with no models
	// still receives an empty schema module so db.ts is always importable.
	hasDB := blueprintEntry(bp, "database") != "" || len(models) > 0
	hasCache := blueprintEntry(bp, "cache") != ""
	hasStorage := blueprintEntry(bp, "storage") != ""
	g.hasStorage = hasStorage

	if g.propertyTests {
		if g.frontendOnly {
			return nil, fmt.Errorf("property tests require the runnable Node service; frontend-only generation is not supported")
		}
		if err := g.validatePropertyTestSupport(endpoints, types, aliases, fns, pipes); err != nil {
			return nil, err
		}
	}

	// Extra infra env vars referenced directly by generated code that may not
	// be hand-declared as secrets: REDIS_URL is read by src/lib/cache.ts and
	// by every worker/schedule Redis connection in src/index.ts; DATABASE_URL
	// is read by the Postgres pool in src/lib/db.ts.
	var extraEnvVars []string
	if hasCache || len(workers)+len(schedules) > 0 || blueprintEntry(bp, "queue") != "" {
		extraEnvVars = append(extraEnvVars, "REDIS_URL")
	}
	if hasDB {
		extraEnvVars = append(extraEnvVars, "DATABASE_URL")
	}

	if g.frontendOnly {
		apiFile := g.genFrontendTypes(models, types, aliases, enums, states, endpoints, streams, ws)
		schemasFile := g.genFrontendSchemas(models, types, aliases, enums, states, endpoints, streams, ws)
		clientFile := g.genFrontendClient(endpoints, streams, ws)
		var i18nFile *codegen.OutputFile
		if len(locales) > 0 || len(translations) > 0 {
			file := g.genFrontendI18n(locales, translations)
			i18nFile = &file
		}
		var reactQueryFile *codegen.OutputFile
		if g.reactQuery && len(endpoints) > 0 {
			file := g.genFrontendReactQuery(endpoints)
			reactQueryFile = &file
		}
		files = append(files, g.genFrontendPackage("", bp, apiFile, schemasFile, clientFile, i18nFile, reactQueryFile)...)
		return files, nil
	}

	// package.json
	files = append(files, g.genPackageJSON(bp, hasDB, hasCache, hasStorage, len(workers)+len(schedules) > 0 || hasEnqueue(endpoints), len(endpoints) > 0, len(ws) > 0))

	// tsconfig.json
	files = append(files, g.genTSConfig())

	// .env.example
	files = append(files, g.genEnvExample(secrets, envs, extraEnvVars...))

	// src/index.ts — entrypoint
	files = append(files, g.genIndex(bp, endpoints, streams, ws, middlewares, subscribes, workers, schedules, hasDB))

	// src/lib/env.ts — env validation
	files = append(files, g.genEnvTS(secrets, envs, extraEnvVars...))

	// src/lib/errors.ts
	files = append(files, g.genErrors())
	if len(states) > 0 {
		files = append(files, g.genStateLib(states))
	}
	if len(locales) > 0 || len(translations) > 0 {
		files = append(files, g.genI18n(locales, translations))
	}

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
	if len(types) > 0 || len(aliases) > 0 || len(enums) > 0 || len(states) > 0 {
		files = append(files, g.genTypes(types, aliases, enums, states))
	}

	// src/models/schema.ts (also emitted empty for database-only projects).
	if hasDB {
		files = append(files, g.genSchema(models, enums))
	}

	// src/validation/schemas.ts
	if len(endpoints) > 0 {
		files = append(files, g.genValidation(endpoints))
	}

	// src/types/api.ts, src/types/schemas.ts, src/types/client.ts
	if len(models) > 0 || len(types) > 0 || len(aliases) > 0 || len(enums) > 0 || len(states) > 0 || len(locales) > 0 || len(translations) > 0 || len(endpoints) > 0 || len(streams) > 0 || len(ws) > 0 {
		apiFile := g.genFrontendTypes(models, types, aliases, enums, states, endpoints, streams, ws)
		schemasFile := g.genFrontendSchemas(models, types, aliases, enums, states, endpoints, streams, ws)
		clientFile := g.genFrontendClient(endpoints, streams, ws)
		files = append(files, apiFile, schemasFile, clientFile)

		var i18nFile *codegen.OutputFile
		if len(locales) > 0 || len(translations) > 0 {
			file := g.genFrontendI18n(locales, translations)
			i18nFile = &file
			files = append(files, file)
		}

		var reactQueryFile *codegen.OutputFile
		if g.reactQuery && len(endpoints) > 0 {
			file := g.genFrontendReactQuery(endpoints)
			reactQueryFile = &file
			files = append(files, file)
		}

		files = append(files, g.genFrontendPackage("frontend", bp, apiFile, schemasFile, clientFile, i18nFile, reactQueryFile)...)
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
	// Collect all impl module stubs that need merging (same module, multiple
	// functions). We track the resolved exported name alongside the source
	// *ast.Fn so the merge step can emit typed signatures from fn.Inputs /
	// fn.Outputs — matching the per-fn path's typing (see genFunction) so
	// users opening the scaffold file see the real signatures, not
	// `...args: any[]`.
	type implStub struct {
		Name string  // exported function name (after `func:` override)
		Fn   *ast.Fn // source AST node, for input/output types
	}
	implModuleFuncs := make(map[string][]implStub) // stub path -> stubs
	for _, fn := range fns {
		if fn.Impl != nil {
			mod := ""
			funcName := toCamelCase(fn.Name)
			for _, kv := range fn.Impl.Entries {
				if kv.Key == "module" {
					mod = exprToString(kv.Value)
					if !strings.HasSuffix(mod, ".js") {
						mod += ".js"
					}
				}
				if kv.Key == "func" {
					funcName = exprToString(kv.Value)
				}
			}
			_, stubPath, localModule := nativeImplModulePaths(mod)
			if localModule {
				implModuleFuncs[stubPath] = append(implModuleFuncs[stubPath], implStub{Name: funcName, Fn: fn})
			}
		}
	}
	for _, fn := range fns {
		files = append(files, g.genFunction(fn)...)
	}
	// Merge stubs: when multiple functions share a module, combine their exports
	for stubPath, stubs := range implModuleFuncs {
		if len(stubs) <= 1 {
			continue
		}
		var sb strings.Builder
		sb.WriteString("// Blueprint implementation scaffold. This file is user-owned; bp build will not overwrite it.\n")
		for _, stub := range stubs {
			paramsStr, retType := g.fnSignatureTS(stub.Fn)
			fmt.Fprintf(&sb, "export async function %s(%s): Promise<%s> {\n", stub.Name, paramsStr, retType)
			fmt.Fprintf(&sb, "  throw new Error('Not implemented: %s');\n", stub.Name)
			sb.WriteString("}\n\n")
		}
		merged := codegen.OutputFile{Path: stubPath, Content: []byte(sb.String()), UserOwned: true}
		// Remove all existing stubs for this path, then append merged one
		filtered := files[:0]
		for _, f := range files {
			if f.Path != stubPath {
				filtered = append(filtered, f)
			}
		}
		files = append(filtered, merged)
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
	// Generate the shared event registry for subscribers, STREAM event handlers,
	// or an in-process emit from any generated context (routes, functions,
	// middleware, workers, schedules, STREAM, or WS).
	needsEventsLib := len(subscribes) > 0 || fileHasCall(g.file, "emit")
	if !needsEventsLib {
		for _, se := range streams {
			for _, h := range se.Handlers {
				if h.EventName != "" && h.Timeout == "" {
					needsEventsLib = true
					break
				}
			}
			if needsEventsLib {
				break
			}
		}
	}
	if needsEventsLib {
		files = append(files, g.genEventsLib())
	}
	for _, sub := range subscribes {
		files = append(files, g.genSubscribe(sub))
	}

	// src/lib/external.ts
	if len(externals) > 0 {
		files = append(files, g.genExternal(externals))
	}
	needsAnalyticsLib := len(analytics) > 0
	if !needsAnalyticsLib {
		for _, ep := range endpoints {
			if stmtsHaveCall(ep.Stmts, "track") {
				needsAnalyticsLib = true
				break
			}
		}
	}
	if !needsAnalyticsLib {
		for _, fn := range fns {
			if fn.Logic != nil && stmtsHaveCall(fn.Logic.Stmts, "track") {
				needsAnalyticsLib = true
				break
			}
		}
	}
	if needsAnalyticsLib {
		files = append(files, g.genAnalyticsLib(analytics))
	}
	if len(saves) > 0 {
		files = append(files, g.genSaveMigrations(saves)...)
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

	// Auto-generated contract tests + in-memory (PGlite) harness (opt-in).
	// Property tests build on the same harness and deliberately imply contract
	// tests; the long-standing --gen-tests mode remains contract-only.
	if (g.genTests || g.propertyTests) && len(endpoints) > 0 {
		files = append(files, g.genAutoTests(endpoints, secrets, hasDB)...)
	}
	if g.propertyTests {
		propertyFiles, err := g.genPropertyTests(endpoints, hasDB, types, aliases)
		if err != nil {
			return nil, err
		}
		files = append(files, propertyFiles...)
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

// The three pre-scans below delegate to internal/resolve (which returns raw
// .bp names) and camelCase the keys for the existing JS-side lookup sites.
// Keeping these thin wrappers means a single line changed at each call site
// instead of touching every consumer.

func collectStepBindingsReassigned(stmts []ast.ArrowStmt) map[string]bool {
	return camelKeys(resolve.InputsReassignedInSteps(stmts))
}

func collectUpdateReassignments(stmts []ast.ArrowStmt) map[string]bool {
	return camelKeys(resolve.FetchVarsReassignedByUnboundUpdate(stmts))
}

func collectPropertyMutations(stmts []ast.ArrowStmt) map[string]bool {
	return camelKeys(resolve.VarsWithPropertyMutation(stmts))
}

// camelKeys returns a copy of the input set with every key converted to
// camelCase — the form JS lookup sites use today.
func camelKeys(raw map[string]bool) map[string]bool {
	out := make(map[string]bool, len(raw))
	for name := range raw {
		out[toCamelCase(name)] = true
	}
	return out
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
	if ctx.paginatedVars == nil {
		ctx.paginatedVars = make(map[string]bool)
	}
	if ctx.eagerRelations == nil {
		ctx.eagerRelations = make(map[string][]string)
	}
	if ctx.fkAliases == nil {
		ctx.fkAliases = make(map[string]string)
	}
	ctx.generator = g

	// Resolve block facts up front (model + cardinality for every data-op binding,
	// plus map outer bindings). Seeding the ctx maps in document order mirrors the
	// old incremental "populate while emitting" behavior — last write wins.
	// External seeds done by the caller (function inputs in functions.go,
	// middleware-injected models in routes.go, etc.) are left in place; the
	// resolver only adds the data-op step facts on top.
	facts := resolve.ResolveBlock(stmts)
	for _, f := range facts.DataOps {
		ctx.varModels[f.Name] = f.Model
		ctx.boundVars[f.Model] = toCamelCase(f.Name)
		switch f.Cardinality {
		case resolve.SingleCard:
			ctx.singleVars[f.Name] = true
		case resolve.PaginatedCard:
			ctx.paginatedVars[f.Name] = true
		}
		if len(f.Relationships) > 0 {
			ctx.eagerRelations[f.Name] = append([]string(nil), f.Relationships...)
			ctx.eagerRelations[toCamelCase(f.Name)] = append([]string(nil), f.Relationships...)
		}
	}
	for _, f := range facts.MapResults {
		ctx.varModels[toCamelCase(f.Name)] = f.Model
	}

	// Pre-scan: find input variables that are later reassigned (for let vs const)
	reassigned := collectStepBindingsReassigned(stmts)

	// Pre-scan: find fetch-bound variables implicitly reassigned by unbound updates
	updateReassigned := collectUpdateReassignments(stmts)

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

	// Emit hoisted variable declarations before the main body. Sort the names
	// so the emitted order is stable across builds (random map iteration would
	// break `bp diff --exit-code` idempotency).
	hoistedNames := make([]string, 0, len(hoisted))
	for name := range hoisted {
		hoistedNames = append(hoistedNames, name)
	}
	sort.Strings(hoistedNames)
	for _, name := range hoistedNames {
		fmt.Fprintf(b, "%slet %s: any;\n", indent, name)
		ctx.declared[name] = true
	}

	for stmtIdx, stmt := range stmts {
		_ = stmtIdx // used for look-ahead in map result capture
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
						fmt.Fprintf(b, "%s%s %s = z.string().uuid().parse(c.req.param('%s'));\n",
							indent, decl, name, s.Name)
					case "int":
						fmt.Fprintf(b, "%s%s %s = z.coerce.number().int().parse(c.req.param('%s'));\n",
							indent, decl, name, s.Name)
					default:
						fmt.Fprintf(b, "%s%s %s = c.req.param('%s') || '';\n",
							indent, decl, name, s.Name)
					}
				} else if ctx.method == "GET" || ctx.method == "DELETE" {
					fmt.Fprintf(b, "%s%s %s = c.req.valid('query').%s;\n",
						indent, decl, name, s.Name)
				} else {
					fmt.Fprintf(b, "%s%s %s = c.req.valid('json').%s;\n",
						indent, decl, name, s.Name)
				}
				ctx.declared[name] = true
			}
			// In function/pipe context, inputs are function params — skip

		case *ast.StepStmt:
			// Variable facts (model + cardinality) for this binding are pre-resolved
			// above via resolve.ResolveBlock — no per-step bookkeeping needed here.

			// Special-case: delete <variable> where variable holds tracked model records
			if fn, ok := s.Expr.(*ast.FnCall); ok && fn.Name == "delete" && len(fn.Args) > 0 {
				if ident, ok := fn.Args[0].(*ast.Ident); ok {
					if modelName, tracked := ctx.varModels[ident.Name]; tracked {
						varName := toCamelCase(ident.Name)
						schemaTable := "schema." + toCamelCase(modelName)
						if ctx.singleVars[ident.Name] {
							// Single record from fetch — use eq
							fmt.Fprintf(b, "%sawait db.delete(%s).where(eq(%s.id, %s.id));\n",
								indent, schemaTable, schemaTable, varName)
						} else {
							// Collection from query — bulk delete
							fmt.Fprintf(b, "%sawait db.delete(%s).where(inArray(%s.id, %s.map((r: any) => r.id)));\n",
								indent, schemaTable, schemaTable, varName)
						}
						continue
					}
				}
			}

			// Special-case: map <variable>: <expr> — use "item" as loop param (bp convention)
			if fn, ok := s.Expr.(*ast.FnCall); ok && fn.Name == "map" && len(fn.Args) >= 2 {
				if ident, ok := fn.Args[0].(*ast.Ident); ok {
					if modelName, tracked := ctx.varModels[ident.Name]; tracked {
						collectionVar := toCamelCase(ident.Name)
						itemVar := "item"
						// Create inner ctx where boundVars maps modelName → "item"
						// so update/delete in the body use item.id (the loop param) not collection.id
						innerCtx := ctx
						innerCtx.boundVars = make(map[string]string)
						for k, v := range ctx.boundVars {
							innerCtx.boundVars[k] = v
						}
						innerCtx.boundVars[modelName] = itemVar
						// Also set varModels for "item" → modelName so FK lookups work on the loop var
						innerCtx.varModels = make(map[string]string)
						for k, v := range ctx.varModels {
							innerCtx.varModels[k] = v
						}
						innerCtx.varModels[itemVar] = modelName
						innerCtx.eagerRelations = make(map[string][]string, len(ctx.eagerRelations)+1)
						for k, relations := range ctx.eagerRelations {
							innerCtx.eagerRelations[k] = append([]string(nil), relations...)
						}
						if relations := ctx.eagerRelations[ident.Name]; len(relations) > 0 {
							innerCtx.eagerRelations[itemVar] = append([]string(nil), relations...)
						} else if relations := ctx.eagerRelations[collectionVar]; len(relations) > 0 {
							innerCtx.eagerRelations[itemVar] = append([]string(nil), relations...)
						}
						innerCtx.fkAliases = make(map[string]string)
						for k, v := range ctx.fkAliases {
							innerCtx.fkAliases[k] = v
						}

						// Check if the body expression has FK access patterns (e.g., item.product.price_cents)
						bodyFKAccesses := g.fkAccessesInExpr(fn.Args[1], &innerCtx)
						if len(bodyFKAccesses) > 0 {
							// Need a block-body lambda to inject FK sub-queries
							// Build FK sub-queries for inside the lambda
							var fkLines strings.Builder
							for _, fk := range bodyFKAccesses {
								key := fk.varName + "." + fk.fieldName
								if _, already := innerCtx.fkAliases[key]; !already {
									alias := "_" + toCamelCase(fk.fieldName)
									schemaTable := "schema." + toCamelCase(fk.targetModel)
									fmt.Fprintf(&fkLines, "%s  const %s = (await db.select().from(%s).where(eq(%s.id, %s.%s)))[0];\n",
										indent, alias, schemaTable, schemaTable, fk.varName, fk.fkColumn)
									innerCtx.fkAliases[key] = alias
								}
							}
							body := exprToJSWithCtx(fn.Args[1], &innerCtx)

							// Determine if we need to capture the map result
							mapResultVar := s.Binding
							if mapResultVar == "" {
								mapResultVar = g.inferMapResultBinding(fn.Args[1], stmts[stmtIdx+1:])
							}

							if mapResultVar != "" {
								name := toCamelCase(mapResultVar)
								fmt.Fprintf(b, "%sconst %s = await Promise.all(%s.map(async (%s: any) => {\n",
									indent, name, collectionVar, itemVar)
								b.WriteString(fkLines.String())
								fmt.Fprintf(b, "%s  return %s;\n", indent, body)
								fmt.Fprintf(b, "%s}));\n", indent)
								ctx.declared[name] = true
								// Track the variable model for the result (pluralized model)
								if bodyFn, ok := fn.Args[1].(*ast.FnCall); ok && isDataOp(bodyFn.Name) && len(bodyFn.Args) > 0 {
									if modelIdent, ok := bodyFn.Args[0].(*ast.Ident); ok {
										ctx.varModels[name] = modelIdent.Name
									}
								}
							} else {
								fmt.Fprintf(b, "%sawait Promise.all(%s.map(async (%s: any) => {\n",
									indent, collectionVar, itemVar)
								b.WriteString(fkLines.String())
								fmt.Fprintf(b, "%s  return %s;\n", indent, body)
								fmt.Fprintf(b, "%s}));\n", indent)
							}
						} else {
							body := exprToJSWithCtx(fn.Args[1], &innerCtx)

							// Determine if we need to capture the map result
							mapResultVar := s.Binding
							if mapResultVar == "" {
								mapResultVar = g.inferMapResultBinding(fn.Args[1], stmts[stmtIdx+1:])
							}

							if mapResultVar != "" {
								name := toCamelCase(mapResultVar)
								fmt.Fprintf(b, "%sconst %s = await Promise.all(%s.map(async (%s: any) => %s));\n",
									indent, name, collectionVar, itemVar, body)
								ctx.declared[name] = true
								if bodyFn, ok := fn.Args[1].(*ast.FnCall); ok && isDataOp(bodyFn.Name) && len(bodyFn.Args) > 0 {
									if modelIdent, ok := bodyFn.Args[0].(*ast.Ident); ok {
										ctx.varModels[name] = modelIdent.Name
									}
								}
							} else {
								fmt.Fprintf(b, "%sawait Promise.all(%s.map(async (%s: any) => %s));\n",
									indent, collectionVar, itemVar, body)
							}
						}
						continue
					}
				}
			}

			// Pre-scan: emit FK sub-queries for any FK access patterns in this step
			for _, fk := range g.fkAccessesInStmt(stmt, &ctx) {
				key := fk.varName + "." + fk.fieldName
				if _, already := ctx.fkAliases[key]; !already {
					alias := g.emitFKSubQuery(b, fk, indent)
					ctx.fkAliases[key] = alias
				}
			}

			expr := exprToJSWithCtx(s.Expr, &ctx)
			if s.Binding != "" {
				name := toCamelCase(s.Binding)

				if ctx.declared[name] || hoisted[name] {
					// Already declared (input reassignment or hoisted) — plain assignment
					fmt.Fprintf(b, "%s%s = %s;\n", indent, name, expr)
				} else {
					// Use `let` for reassigned vars; annotate filter-accumulator objects as Record<string, any>
					decl := "const"
					if reassigned[name] || updateReassigned[name] {
						decl = "let"
					}
					if propMutated[name] {
						fmt.Fprintf(b, "%s%s %s: Record<string, any> = %s;\n", indent, decl, name, expr)
					} else {
						fmt.Fprintf(b, "%s%s %s = %s;\n", indent, decl, name, expr)
					}
					ctx.declared[name] = true
				}
			} else {
				// Check if this is an unbound update that should reassign a fetch variable
				reassignVar := ""
				if fn, ok := s.Expr.(*ast.FnCall); ok && fn.Name == "update" && len(fn.Args) > 0 {
					if ident, ok := fn.Args[0].(*ast.Ident); ok {
						v := toCamelCase(ident.Name)
						if updateReassigned[v] {
							reassignVar = v
						}
					}
				}
				if reassignVar != "" {
					fmt.Fprintf(b, "%s%s = %s;\n", indent, reassignVar, expr)
				} else {
					fmt.Fprintf(b, "%s%s;\n", indent, expr)
				}
			}

		case *ast.GuardStmt:
			// Pre-scan: emit FK sub-queries for any FK access patterns in the guard condition
			for _, fk := range g.fkAccessesInStmt(stmt, &ctx) {
				key := fk.varName + "." + fk.fieldName
				if _, already := ctx.fkAliases[key]; !already {
					alias := g.emitFKSubQuery(b, fk, indent)
					ctx.fkAliases[key] = alias
				}
			}
			cond := exprToJSWithCtx(s.Condition, &ctx)
			switch ctx.kind {
			case "endpoint":
				fmt.Fprintf(b, "%sif (!(%s)) return c.json({ error: %q }, %s as const);\n",
					indent, cond, s.Message, s.Status)
			case "worker":
				// Workers have no HTTP response to send — stop the job with a
				// console.error and an early return instead of throwing (there's
				// no BpError import in worker files, and a thrown BpError would
				// just look like an unhandled job failure to BullMQ anyway).
				fmt.Fprintf(b, "%sif (!(%s)) { console.error(%q); return; }\n",
					indent, cond, s.Message)
			default:
				fmt.Fprintf(b, "%sif (!(%s)) throw new BpError(%s, %q);\n",
					indent, cond, s.Status, s.Message)
			}

		case *ast.WhenStmt:
			cond := exprToJSWithCtx(s.Condition, &ctx)
			if s.Inline != nil {
				fmt.Fprintf(b, "%sif (%s) %s;\n", indent, cond, exprToJSWithCtx(s.Inline, &ctx))
			} else if len(s.Body) > 0 {
				fmt.Fprintf(b, "%sif (%s) {\n", indent, cond)
				// Use a block-scoped copy of ctx so declarations inside this when-block
				// don't leak into sibling when-blocks (they're in separate JS if-scopes).
				innerCtx := ctx
				innerCtx.declared = make(map[string]bool)
				for k, v := range ctx.declared {
					innerCtx.declared[k] = v
				}
				g.emitArrowStmts(b, s.Body, indent+"  ", innerCtx)
				fmt.Fprintf(b, "%s}\n", indent)
			}

		case *ast.OutputStmt:
			// Output expressions can traverse a ref-backed relationship just
			// like step and guard expressions. Fetch those rows before rendering
			// the response so exprToJSWithCtx can replace the traversal with the
			// materialized alias.
			for _, fk := range g.fkAccessesInStmt(stmt, &ctx) {
				key := fk.varName + "." + fk.fieldName
				if _, already := ctx.fkAliases[key]; !already {
					alias := g.emitFKSubQuery(b, fk, indent)
					ctx.fkAliases[key] = alias
				}
			}
			status := s.Status
			if status == "" {
				status = "200"
			}
			// Preserve original key names in response JSON (don't camelCase BlockExpr keys)
			outputCtx := ctx
			outputCtx.preserveBlockKeys = true
			val := exprToJSWithCtx(s.Value, &outputCtx)

			switch ctx.kind {
			case "endpoint":
				// For 204 No Content, use c.body(null, 204)
				if status == "204" {
					fmt.Fprintf(b, "%sreturn c.body(null, 204 as const);\n", indent)
				} else {
					fmt.Fprintf(b, "%sreturn c.json(%s, %s as const);\n", indent, val, status)
				}
			case "ws":
				// WS context — send message via WebSocket
				fmt.Fprintf(b, "%sws.send(JSON.stringify(%s));\n", indent, val)
			default:
				// function/pipe/middleware context — plain return
				fmt.Fprintf(b, "%sreturn %s;\n", indent, val)
			}

		case *ast.TryRecover:
			fmt.Fprintf(b, "%stry {\n", indent)
			if tryBodyNeedsTransaction(s.Try) {
				// Atomic: wrap the multi-write body in a DB transaction so a
				// later failure rolls back the partial commits. `const db = tx`
				// shadows the module-level db so the body's data ops, emitted
				// unchanged, run inside the transaction.
				fmt.Fprintf(b, "%s  await db.transaction(async (tx) => {\n", indent)
				fmt.Fprintf(b, "%s    const db = tx;\n", indent)
				g.emitArrowStmts(b, s.Try, indent+"    ", ctx)
				fmt.Fprintf(b, "%s  });\n", indent)
			} else {
				g.emitArrowStmts(b, s.Try, indent+"  ", ctx)
			}
			fmt.Fprintf(b, "%s} catch (error: any) {\n", indent)
			g.emitArrowStmts(b, s.Recover, indent+"  ", ctx)
			fmt.Fprintf(b, "%s}\n", indent)
		}
	}
}

// tryBodyNeedsTransaction reports whether a `try` body should be wrapped in a
// db.transaction so that partial writes roll back if a later step fails.
//
// We wrap only when the body performs MULTIPLE mutations (a single write is
// already atomic) and contains no statement that returns from the handler — an
// output (`->`) or a `guard`. A return inside the transaction callback would
// complete the callback normally and COMMIT a partial transaction rather than
// abort the route, so those bodies are left as a bare try/catch. External side
// effects inside the body (e.g. an HTTP charge) are not rolled back by the DB
// transaction; the `recover` block remains the place to compensate for those.
func tryBodyNeedsTransaction(stmts []ast.ArrowStmt) bool {
	return countMutations(stmts) >= 2 && !stmtsReturn(stmts)
}

// countMutations counts save/update/delete data ops (including those inside a
// `map` body or a `when` block) within stmts.
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

// stmtsReturn reports whether stmts contains a statement that returns from the
// handler (an output `->` or a `guard`), recursing into `when` blocks.
func stmtsReturn(stmts []ast.ArrowStmt) bool {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.OutputStmt:
			return true
		case *ast.GuardStmt:
			return true
		case *ast.WhenStmt:
			if stmtsReturn(v.Body) {
				return true
			}
		case *ast.TryRecover:
			if stmtsReturn(v.Try) || stmtsReturn(v.Recover) {
				return true
			}
		}
	}
	return false
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

// queryIsPaginated checks if a query FnCall has a paginate() marker in its args.
func queryIsPaginated(fn *ast.FnCall) bool {
	for _, arg := range fn.Args[1:] {
		if marker, ok := arg.(*ast.FnCall); ok && marker.Name == "paginate" {
			return true
		}
	}
	return false
}

// hasEnqueue reports whether any endpoint in the file uses `enqueue` —
// used to decide whether bullmq needs to be in package.json.
func hasEnqueue(endpoints []*ast.Endpoint) bool {
	for _, ep := range endpoints {
		var found bool
		walkStmtsForEnqueue(ep.Stmts, &found)
		if found {
			return true
		}
	}
	return false
}

func walkStmtsForEnqueue(stmts []ast.ArrowStmt, found *bool) {
	for _, s := range stmts {
		if step, ok := s.(*ast.StepStmt); ok {
			if fc, ok := step.Expr.(*ast.FnCall); ok && fc.Name == "enqueue" {
				*found = true
				return
			}
		}
		if w, ok := s.(*ast.WhenStmt); ok {
			walkStmtsForEnqueue(w.Body, found)
		}
		if tr, ok := s.(*ast.TryRecover); ok {
			walkStmtsForEnqueue(tr.Try, found)
			walkStmtsForEnqueue(tr.Recover, found)
		}
	}
}

// Ensure Generator implements codegen.Generator.
var _ codegen.Generator = (*Generator)(nil)
