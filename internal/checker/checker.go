package checker

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/diag"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// CheckError represents a semantic error.
//
// Code is an optional structured error code (e.g. "C001") that, once populated
// across error sites, lets `bp explain <code>` link to documentation.
type CheckError struct {
	Loc     lexer.Loc
	Message string
	Hint    string
	Code    string
}

func (e CheckError) Error() string {
	s := fmt.Sprintf("%s: %s", e.Loc, e.Message)
	if e.Hint != "" {
		s += "\n  Hint: " + e.Hint
	}
	return s
}

// Checker performs semantic analysis on a parsed AST.
type Checker struct {
	file   *ast.File
	global *Scope
	errors []CheckError
}

// Check performs semantic checking on the given AST file.
// Returns a list of semantic errors found.
func Check(file *ast.File) []CheckError {
	c := &Checker{
		file:   file,
		global: NewScope(nil),
	}

	// Pass 1: collect all top-level declarations
	c.collectDeclarations()

	// Pass 2: validate each block
	c.validateBlueprint()
	c.validateBlocks()

	return c.errors
}

func (c *Checker) addError(loc lexer.Loc, msg, hint string) {
	c.errors = append(c.errors, CheckError{Loc: loc, Message: msg, Hint: hint})
}

// addErrorCode is addError with a structured error code (e.g. "C001").
// Use this for error sites documented in docs/error-codes.md so users can
// `bp explain <code>` to see the long-form explanation.
func (c *Checker) addErrorCode(loc lexer.Loc, code, msg, hint string) {
	c.errors = append(c.errors, CheckError{Loc: loc, Message: msg, Hint: hint, Code: code})
}

// Checker error codes — keep in sync with docs/error-codes.md and
// internal/diag/error-codes.md (the drift test enforces match).
const (
	// CodeMissingBlueprint = no blueprint block at file top level.
	CodeMissingBlueprint = "C001"
	// CodeBlueprintNameEmpty = `blueprint "" { ... }` with an empty name.
	CodeBlueprintNameEmpty = "C002"
	// CodeBlueprintMissingField = required blueprint entry (version, runtime) absent.
	CodeBlueprintMissingField = "C003"
	// CodeDuplicateName = two top-level declarations share a name.
	CodeDuplicateName = "C004"
	// CodeDuplicateEndpoint = two endpoints share METHOD + PATH.
	CodeDuplicateEndpoint = "C005"
	// CodeUnknownFunction = call to an undeclared fn/pipe/builtin.
	CodeUnknownFunction = "C006"
	// CodeUnknownMiddleware = `use <name>` where <name> isn't declared.
	CodeUnknownMiddleware = "C007"
	// CodeIdentifierNotSnakeCase = a name where Blueprint requires snake_case.
	CodeIdentifierNotSnakeCase = "C008"
	// CodePathParamNotSnakeCase = a :param segment in a path that isn't snake_case.
	CodePathParamNotSnakeCase = "C009"
	// CodeDuplicateField = two fields share a name within a single model.
	CodeDuplicateField = "C010"
	// CodeArrowStmtOrder = inputs/steps/outputs out of canonical order.
	CodeArrowStmtOrder = "C011"
	// CodeNestedTryRecover = try/recover blocks cannot be nested.
	CodeNestedTryRecover = "C012"
	// CodeUnknownType = a NamedType references a type that isn't a primitive
	// and isn't defined via `type`, `alias`, or `enum`.
	CodeUnknownType = "C013"
	// CodeUnknownRefTarget = `ref <model>` points at an identifier that isn't
	// a known model (or isn't a model symbol at all).
	CodeUnknownRefTarget = "C014"
	// CodeUnknownExternal = `call <service> ...` references a service that
	// wasn't declared via an `external "..." { ... }` block.
	CodeUnknownExternal = "C015"
)

// --- Pass 1: Collect top-level declarations ---

func (c *Checker) collectDeclarations() {
	for _, block := range c.file.Blocks {
		switch n := block.(type) {
		case *ast.Model:
			c.define(n.Name, SymModel, n.Loc, n)
		case *ast.Content:
			c.define(n.Name, SymModel, n.Loc, n)
		case *ast.Fn:
			c.define(n.Name, SymFn, n.Loc, n)
		case *ast.Pipe:
			c.define(n.Name, SymPipe, n.Loc, n)
		case *ast.Middleware:
			c.define(n.Name, SymMiddleware, n.Loc, n)
		case *ast.Enum:
			c.define(n.Name, SymEnum, n.Loc, n)
		case *ast.TypeDecl:
			c.define(n.Name, SymType, n.Loc, n)
		case *ast.Alias:
			c.define(n.Name, SymAlias, n.Loc, n)
		case *ast.External:
			c.define(n.Name, SymExternal, n.Loc, n)
		case *ast.Secret:
			c.define(n.Name, SymSecret, n.Loc, n)
		case *ast.Env:
			c.define(n.Name, SymEnv, n.Loc, n)
		case *ast.Translation:
			c.define(n.Name, SymType, n.Loc, n)
		case *ast.StateMachine:
			c.define(n.Name, SymType, n.Loc, n)
		case *ast.Analytics:
			c.define(n.Name, SymAnalytics, n.Loc, n)
		case *ast.SaveSchema:
			c.define(n.Name, SymSave, n.Loc, n)
		}
	}
}

func (c *Checker) define(name string, kind SymbolKind, loc lexer.Loc, node ast.Node) {
	sym := &Symbol{Name: name, Kind: kind, Loc: loc, Node: node}
	if existing := c.global.Define(sym); existing != nil {
		c.addErrorCode(loc, CodeDuplicateName,
			fmt.Sprintf("duplicate %s name %q (previously defined at %s)", kind, name, existing.Loc),
			fmt.Sprintf("Rename one of the %s declarations", kind),
		)
	}
}

// --- Pass 2: Validation ---

func (c *Checker) validateBlueprint() {
	if c.file.Blueprint == nil {
		c.addErrorCode(c.file.Loc, CodeMissingBlueprint,
			"missing blueprint block",
			"Every .bp file must start with: blueprint \"name\" { ... }",
		)
		return
	}

	bp := c.file.Blueprint
	if bp.Name == "" {
		c.addErrorCode(bp.Loc, CodeBlueprintNameEmpty, "blueprint name is empty", "Provide a name: blueprint \"my-app\" { ... }")
	}

	// Check required fields
	found := map[string]bool{}
	for _, e := range bp.Entries {
		found[e.Key] = true
	}
	if !found["version"] {
		c.addErrorCode(bp.Loc, CodeBlueprintMissingField, "blueprint block missing required field 'version'", `Add: version "0.1.0"`)
	}
	if !found["runtime"] {
		c.addErrorCode(bp.Loc, CodeBlueprintMissingField, "blueprint block missing required field 'runtime'", "Add: runtime node")
	}
}

func (c *Checker) validateBlocks() {
	// Track endpoints for duplicate detection (METHOD + PATH)
	endpoints := make(map[string]lexer.Loc)

	for _, block := range c.file.Blocks {
		switch n := block.(type) {
		case *ast.Model:
			c.checkModel(n)
		case *ast.Content:
			c.checkContent(n)
		case *ast.Fn:
			c.checkFn(n)
		case *ast.Pipe:
			c.checkPipe(n)
		case *ast.Middleware:
			c.checkMiddleware(n)
		case *ast.Endpoint:
			c.checkEndpoint(n)
			c.checkDuplicateEndpoint(n.Method, n.Path, n.Loc, endpoints)
		case *ast.StreamEndpoint:
			c.checkStreamEndpoint(n)
			c.checkDuplicateEndpoint("STREAM", n.Path, n.Loc, endpoints)
		case *ast.WsEndpoint:
			c.checkWsEndpoint(n)
			c.checkDuplicateEndpoint("WS", n.Path, n.Loc, endpoints)
		case *ast.Worker:
			c.checkWorker(n)
		case *ast.Schedule:
			c.checkSchedule(n)
		case *ast.Subscribe:
			c.checkSubscribe(n)
		case *ast.Enum:
			c.checkEnum(n)
		case *ast.TypeDecl:
			c.checkTypeDecl(n)
		case *ast.Alias:
			c.checkAlias(n)
		case *ast.Secret:
			c.checkSecret(n)
		case *ast.Env:
			c.checkEnv(n)
		case *ast.Locale:
			c.checkLocale(n)
		case *ast.Translation:
			c.checkTranslation(n)
		case *ast.StateMachine:
			c.checkStateMachine(n)
		case *ast.Analytics:
			c.checkAnalytics(n)
		case *ast.SaveSchema:
			c.checkSaveSchema(n)
		case *ast.Test:
			c.checkTest(n)
		case *ast.TestGroup:
			c.checkTestGroup(n)
		}
	}
}

// --- Model ---

func (c *Checker) checkModel(n *ast.Model) {
	c.checkSnakeCase(n.Name, "model", n.Loc)
	// Detect duplicate fields
	seen := map[string]lexer.Loc{}
	for _, f := range n.Fields {
		c.checkSnakeCase(f.Name, "field", f.Loc)
		if prevLoc, dup := seen[f.Name]; dup {
			c.addErrorCode(f.Loc, CodeDuplicateField,
				fmt.Sprintf("duplicate field '%s' in model '%s'", f.Name, n.Name),
				fmt.Sprintf("First defined at %s", prevLoc),
			)
		} else {
			seen[f.Name] = f.Loc
		}
		c.checkTypeRef(f.Type)
		for _, con := range f.Constraints {
			if con.Kind == "ref" && con.Value != nil {
				c.checkRefTarget(con.Value, con.Loc)
			}
		}
	}
}

func (c *Checker) checkContent(n *ast.Content) {
	c.checkSnakeCase(n.Name, "content", n.Loc)
	c.checkModel(n.AsModel())
}

// --- Fn ---

func (c *Checker) checkFn(n *ast.Fn) {
	c.checkSnakeCase(n.Name, "fn", n.Loc)
	for _, inp := range n.Inputs {
		c.checkTypeRef(inp.Type)
		c.checkConstraints(inp.Constraints)
	}
	for _, out := range n.Outputs {
		c.checkExpr(out.Value)
	}
	if n.Impl != nil {
		for _, kv := range n.Impl.Entries {
			c.checkExpr(kv.Value)
		}
	}
	if n.Logic != nil {
		c.checkTryRecoverNesting(n.Logic.Stmts, n.Loc)
		c.checkArrowStmtExprs(n.Logic.Stmts)
	}
}

// --- Pipe ---

func (c *Checker) checkPipe(n *ast.Pipe) {
	c.checkSnakeCase(n.Name, "pipe", n.Loc)
	c.checkArrowOrdering(n.Stmts, n.Loc, "pipe")
	c.checkTryRecoverNesting(n.Stmts, n.Loc)
	c.checkArrowStmtExprs(n.Stmts)
}

// --- Middleware ---

func (c *Checker) checkMiddleware(n *ast.Middleware) {
	c.checkSnakeCase(n.Name, "middleware", n.Loc)
	c.checkArrowStmtExprs(n.Before)
	c.checkArrowStmtExprs(n.After)
}

// --- Endpoint ---

func (c *Checker) checkEndpoint(n *ast.Endpoint) {
	c.checkArrowOrdering(n.Stmts, n.Loc, "endpoint")
	c.checkTryRecoverNesting(n.Stmts, n.Loc)
	c.checkPathNaming(n.Path, n.Loc)
	for _, m := range n.Meta {
		if m.Kind == "use" && m.Use != nil {
			c.checkMiddlewareRef(m.Use.Name, m.Loc)
		}
		if m.Value != nil {
			c.checkExpr(m.Value)
		}
		c.checkEndpointAuth(m)
	}
	c.checkArrowStmtExprs(n.Stmts)
}

// --- StreamEndpoint ---

func (c *Checker) checkStreamEndpoint(n *ast.StreamEndpoint) {
	c.checkArrowOrdering(n.Stmts, n.Loc, "stream endpoint")
	c.checkTryRecoverNesting(n.Stmts, n.Loc)
	c.checkPathNaming(n.Path, n.Loc)
	for _, m := range n.Meta {
		if m.Kind == "use" && m.Use != nil {
			c.checkMiddlewareRef(m.Use.Name, m.Loc)
		}
		if m.Value != nil {
			c.checkExpr(m.Value)
		}
		c.checkEndpointAuth(m)
	}
	c.checkArrowStmtExprs(n.Stmts)
	for _, h := range n.Handlers {
		c.checkExpr(h.Condition)
		c.checkArrowStmtExprs(h.Body)
	}
}

// --- WsEndpoint ---

func (c *Checker) checkWsEndpoint(n *ast.WsEndpoint) {
	c.checkPathNaming(n.Path, n.Loc)
	for _, m := range n.Meta {
		if m.Kind == "use" && m.Use != nil {
			c.checkMiddlewareRef(m.Use.Name, m.Loc)
		}
		if m.Value != nil {
			c.checkExpr(m.Value)
		}
		c.checkEndpointAuth(m)
	}
	c.checkArrowStmtExprs(n.OnConnect)
	c.checkArrowStmtExprs(n.OnMessage)
	c.checkArrowStmtExprs(n.OnDisconnect)
}

// --- Duplicate Endpoint ---

func (c *Checker) checkDuplicateEndpoint(method, path string, loc lexer.Loc, seen map[string]lexer.Loc) {
	key := method + " " + path
	if prev, ok := seen[key]; ok {
		c.addErrorCode(loc, CodeDuplicateEndpoint,
			fmt.Sprintf("duplicate endpoint %s (previously defined at %s)", key, prev),
			"Each METHOD + PATH combination must be unique",
		)
	} else {
		seen[key] = loc
	}
}

// --- Worker ---

func (c *Checker) checkWorker(n *ast.Worker) {
	c.checkSnakeCase(n.Name, "worker", n.Loc)
	c.checkArrowOrdering(n.Stmts, n.Loc, "worker")
	c.checkTryRecoverNesting(n.Stmts, n.Loc)
	c.checkArrowStmtExprs(n.Stmts)
	c.checkArrowStmtExprs(n.OnFail)
	for _, m := range n.Meta {
		c.checkExpr(m.Value)
		for _, kv := range m.Extra {
			c.checkExpr(kv.Value)
		}
	}
}

// --- Schedule ---

func (c *Checker) checkSchedule(n *ast.Schedule) {
	c.checkSnakeCase(n.Name, "schedule", n.Loc)
	c.checkArrowOrdering(n.Stmts, n.Loc, "schedule")
	c.checkArrowStmtExprs(n.Stmts)
}

// --- Subscribe ---

func (c *Checker) checkSubscribe(n *ast.Subscribe) {
	c.checkArrowOrdering(n.Stmts, n.Loc, "subscribe")
	if n.From != "" && !c.hasExternal(n.From) {
		c.addError(n.Loc, fmt.Sprintf("subscribe references unknown external %q", n.From), "Declare it with: external \"service-name\" { ... }")
	}
	c.checkArrowStmtExprs(n.Stmts)
}

// --- Enum ---

func (c *Checker) checkEnum(n *ast.Enum) {
	c.checkPascalCase(n.Name, "enum", n.Loc)
}

// --- TypeDecl ---

func (c *Checker) checkTypeDecl(n *ast.TypeDecl) {
	c.checkPascalCase(n.Name, "type", n.Loc)
	for _, f := range n.Fields {
		c.checkSnakeCase(f.Name, "field", f.Loc)
		c.checkTypeRef(f.Type)
	}
}

// --- Alias ---

func (c *Checker) checkAlias(n *ast.Alias) {
	c.checkPascalCase(n.Name, "alias", n.Loc)
	c.checkTypeRef(n.Type)
}

// --- Secret ---

func (c *Checker) checkSecret(n *ast.Secret) {
	c.checkScreamingSnakeCase(n.Name, "secret", n.Loc)
}

// --- Env ---

func (c *Checker) checkEnv(n *ast.Env) {
	c.checkScreamingSnakeCase(n.Name, "env", n.Loc)
}

func (c *Checker) checkLocale(n *ast.Locale) {
	if n.Code == "" {
		c.addError(n.Loc, "locale code cannot be empty", `Use locale en default or locale "en-US"`)
	}
	if n.Fallback == n.Code && n.Fallback != "" {
		c.addError(n.Loc, "locale fallback cannot point to itself", "Choose a different fallback locale")
	}
	defaultCount := 0
	known := map[string]bool{}
	for _, block := range c.file.Blocks {
		loc, ok := block.(*ast.Locale)
		if !ok {
			continue
		}
		known[loc.Code] = true
		if loc.Default {
			defaultCount++
		}
	}
	if defaultCount > 1 && n.Default {
		c.addError(n.Loc, "multiple default locales declared", "Keep exactly one locale marked default")
	}
	if n.Fallback != "" && !known[n.Fallback] {
		c.addError(n.Loc, fmt.Sprintf("locale %q references unknown fallback locale %q", n.Code, n.Fallback), "Declare the fallback locale before using it")
	}
}

func (c *Checker) checkTranslation(n *ast.Translation) {
	c.checkSnakeCase(n.Name, "translation", n.Loc)
	seen := map[string]bool{}
	for _, key := range n.Keys {
		if seen[key] {
			c.addError(n.Loc, fmt.Sprintf("duplicate translation key %q in translation %q", key, n.Name), "Remove the duplicate key or rename it")
			continue
		}
		seen[key] = true
	}
	knownLocales := map[string]bool{}
	for _, block := range c.file.Blocks {
		if loc, ok := block.(*ast.Locale); ok {
			knownLocales[loc.Code] = true
		}
	}
	bundleLocales := map[string]bool{}
	for _, bundle := range n.Bundles {
		if !knownLocales[bundle.Locale] {
			c.addError(bundle.Loc, fmt.Sprintf("translation %q references unknown locale %q", n.Name, bundle.Locale), "Declare the locale before using it in a translation bundle")
		}
		if bundleLocales[bundle.Locale] {
			c.addError(bundle.Loc, fmt.Sprintf("translation %q has duplicate bundle for locale %q", n.Name, bundle.Locale), "Keep at most one bundle per locale")
			continue
		}
		bundleLocales[bundle.Locale] = true
		bundleKeys := map[string]bool{}
		for _, kv := range bundle.Values {
			if !seen[kv.Key] {
				c.addError(kv.Loc, fmt.Sprintf("translation %q bundle for locale %q defines unknown key %q", n.Name, bundle.Locale, kv.Key), "Declare the key in the translation block before assigning localized values")
			}
			if bundleKeys[kv.Key] {
				c.addError(kv.Loc, fmt.Sprintf("translation %q bundle for locale %q repeats key %q", n.Name, bundle.Locale, kv.Key), "Keep only one localized value per key")
			}
			bundleKeys[kv.Key] = true
		}
	}
}

func (c *Checker) checkStateMachine(n *ast.StateMachine) {
	c.checkSnakeCase(n.Name, "state", n.Loc)
	seen := map[string]bool{}
	for _, tr := range n.Transitions {
		key := tr.From + "->" + tr.To
		if seen[key] {
			c.addError(tr.Loc, fmt.Sprintf("duplicate state transition %s in %q", key, n.Name), "Remove the duplicate transition")
			continue
		}
		seen[key] = true
	}
}

func (c *Checker) checkAnalytics(n *ast.Analytics) {
	c.checkSnakeCase(n.Name, "analytics", n.Loc)
	seen := map[string]bool{}
	for _, event := range n.Events {
		if seen[event] {
			c.addError(n.Loc, fmt.Sprintf("duplicate analytics event %q in %q", event, n.Name), "Remove the duplicate event")
		}
		seen[event] = true
	}
	for _, sink := range n.Sinks {
		switch sink.Kind {
		case "console", "http":
		default:
			c.addError(sink.Loc, fmt.Sprintf("unsupported analytics sink %q", sink.Kind), "Use sink console or sink http(\"https://...\")")
		}
	}
}

func (c *Checker) checkSaveSchema(n *ast.SaveSchema) {
	c.checkSnakeCase(n.Name, "save", n.Loc)
	if n.Model == "" {
		c.addError(n.Loc, fmt.Sprintf("save %q is missing a model", n.Name), "Add: model <model_name>")
		return
	}
	sym := c.global.Lookup(n.Model)
	if sym == nil || sym.Kind != SymModel {
		c.addError(n.Loc, fmt.Sprintf("save %q references unknown model %q", n.Name, n.Model), "Use an existing model or content block name")
		return
	}
	if n.VersionField == "" {
		c.addError(n.Loc, fmt.Sprintf("save %q is missing version_field", n.Name), "Add: version_field <field_name>")
	}
	if n.Latest < 1 {
		c.addError(n.Loc, fmt.Sprintf("save %q must declare latest >= 1", n.Name), "Add: latest 1")
	}
	model, ok := sym.Node.(*ast.Model)
	if !ok {
		if content, ok := sym.Node.(*ast.Content); ok {
			model = content.AsModel()
		}
	}
	if model == nil {
		return
	}
	found := false
	for _, f := range model.Fields {
		if f.Name == n.VersionField {
			found = true
			break
		}
	}
	if n.VersionField != "" && !found {
		c.addError(n.Loc, fmt.Sprintf("save %q version_field %q does not exist on model %q", n.Name, n.VersionField, n.Model), "Point version_field to a field on the referenced model")
	}
	seenMigrations := map[string]bool{}
	for _, mig := range n.Migrations {
		if mig.From >= mig.To {
			c.addError(mig.Loc, fmt.Sprintf("save %q has invalid migration %d -> %d", n.Name, mig.From, mig.To), "Migration targets must increase version numbers")
			continue
		}
		if n.Latest > 0 && mig.To > n.Latest {
			c.addError(mig.Loc, fmt.Sprintf("save %q migration %d -> %d exceeds latest version %d", n.Name, mig.From, mig.To, n.Latest), "Keep migration targets within the declared latest version")
		}
		key := fmt.Sprintf("%d->%d", mig.From, mig.To)
		if seenMigrations[key] {
			c.addError(mig.Loc, fmt.Sprintf("save %q repeats migration %s", n.Name, key), "Declare each migration step once")
		}
		seenMigrations[key] = true
	}
}

// --- Test ---

func (c *Checker) checkTest(n *ast.Test) {
	c.checkSnakeCase(n.Name, "test", n.Loc)
	c.checkArrowStmtExprs(n.Setup)
	c.checkArrowStmtExprs(n.Cleanup)
	if n.Request != nil {
		for _, kv := range n.Request.Entries {
			c.checkExpr(kv.Value)
		}
	}
}

// --- TestGroup ---

func (c *Checker) checkTestGroup(n *ast.TestGroup) {
	c.checkSnakeCase(n.Name, "test_group", n.Loc)
	// Check that all referenced tests exist in this file
	var testNames []string
	for _, block := range c.file.Blocks {
		if t, ok := block.(*ast.Test); ok {
			testNames = append(testNames, t.Name)
		}
	}
	for _, testName := range n.Tests {
		found := false
		for _, tn := range testNames {
			if tn == testName {
				found = true
				break
			}
		}
		if !found {
			hint := "Define the test or fix the name"
			if suggestion := suggestName(testName, testNames); suggestion != "" {
				hint += fmt.Sprintf("; did you mean %q?", suggestion)
			}
			c.addError(n.Loc,
				fmt.Sprintf("test_group references unknown test %q", testName),
				hint,
			)
		}
	}
}

// ═══════════════════════════════════════════════
// Expression Reference Checks
// ═══════════════════════════════════════════════

func (c *Checker) checkConstraints(constraints []*ast.Constraint_) {
	for _, con := range constraints {
		c.checkExpr(con.Value)
	}
}

func (c *Checker) checkArrowStmtExprs(stmts []ast.ArrowStmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.InputStmt:
			c.checkTypeRef(s.Type)
			c.checkConstraints(s.Constraints)
		case *ast.StepStmt:
			c.checkExpr(s.Expr)
		case *ast.GuardStmt:
			c.checkExpr(s.Condition)
		case *ast.WhenStmt:
			c.checkExpr(s.Condition)
			c.checkExpr(s.Inline)
			c.checkArrowStmtExprs(s.Body)
		case *ast.OutputStmt:
			c.checkExpr(s.Value)
		case *ast.TryRecover:
			c.checkArrowStmtExprs(s.Try)
			c.checkArrowStmtExprs(s.Recover)
		}
	}
}

func (c *Checker) checkExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		c.checkExpr(e.Left)
		c.checkExpr(e.Right)
	case *ast.UnaryExpr:
		c.checkExpr(e.Operand)
	case *ast.FnCall:
		c.checkFnCall(e)
		for _, arg := range e.Args {
			c.checkExpr(arg)
		}
	case *ast.FieldAccess:
		c.checkExpr(e.Base)
	case *ast.IndexAccess:
		c.checkExpr(e.Base)
		c.checkExpr(e.Index)
	case *ast.ParenExpr:
		c.checkExpr(e.Expr)
	case *ast.ListExpr:
		for _, el := range e.Elements {
			c.checkExpr(el)
		}
	case *ast.BlockExpr:
		for _, kv := range e.Entries {
			c.checkExpr(kv.Value)
		}
	}
}

func (c *Checker) checkFnCall(call *ast.FnCall) {
	if call == nil {
		return
	}
	if isCheckerBuiltinFn(call.Name) {
		if call.Name == "call" {
			c.checkExternalCall(call)
		}
		if isStorageOperation(call.Name) && !c.hasBlueprintEntry("storage") {
			c.addError(call.Loc,
				fmt.Sprintf("storage operation %q requires blueprint storage", call.Name),
				"Add `storage s3` to the blueprint block or remove the storage operation",
			)
		}
		if call.Name == "transition" && len(call.Args) > 0 {
			if id, ok := call.Args[0].(*ast.Ident); ok {
				if sym := c.global.Lookup(id.Name); sym == nil || sym.Kind != SymType {
					c.addError(call.Loc, fmt.Sprintf("transition references unknown state %q", id.Name), "Declare it with: state <name> { ... }")
				}
			}
		}
		if call.Name == "upgrade_save" && len(call.Args) > 0 {
			if id, ok := call.Args[0].(*ast.Ident); ok {
				if sym := c.global.Lookup(id.Name); sym == nil || sym.Kind != SymSave {
					c.addError(call.Loc, fmt.Sprintf("upgrade_save references unknown save %q", id.Name), "Declare it with: save <name> { ... }")
				}
			}
		}
		return
	}
	if sym := c.global.Lookup(call.Name); sym != nil {
		switch sym.Kind {
		case SymFn, SymPipe, SymModel:
			return
		default:
			c.addError(call.Loc,
				fmt.Sprintf("%q is a %s, not a function or pipe", call.Name, sym.Kind),
				"Use a declared fn or pipe name here",
			)
			return
		}
	}

	hint := "Declare it with: fn " + call.Name + " { ... }"
	candidates := append(c.global.NamesOfKind(SymFn), c.global.NamesOfKind(SymPipe)...)
	for name := range checkerBuiltinFns {
		candidates = append(candidates, name)
	}
	if suggestion := suggestName(call.Name, candidates); suggestion != "" {
		hint += fmt.Sprintf("; did you mean %q?", suggestion)
	}
	c.addErrorCode(call.Loc, CodeUnknownFunction, fmt.Sprintf("unknown function %q", call.Name), hint)
}

func (c *Checker) checkExternalCall(call *ast.FnCall) {
	if len(call.Args) == 0 {
		c.addError(call.Loc, "call requires an external service name", "Use: call service_name GET /path")
		return
	}
	service, ok := call.Args[0].(*ast.Ident)
	if !ok {
		c.addError(call.Loc, "call service must be an identifier", "Use: call service_name GET /path")
		return
	}
	if !c.hasExternal(service.Name) {
		c.addErrorCode(call.Loc, CodeUnknownExternal,
			fmt.Sprintf("call references unknown external %q", service.Name),
			"Declare it with: external \"service-name\" { ... }",
		)
	}
}

func (c *Checker) checkEndpointAuth(meta *ast.EndpointMeta) {
	if meta == nil || meta.Kind != "auth" || meta.Value == nil {
		return
	}
	fn, ok := meta.Value.(*ast.FnCall)
	if !ok {
		if id, isIdent := meta.Value.(*ast.Ident); isIdent && id.Name == "webhook_sig" {
			c.addError(meta.Loc,
				"auth webhook_sig requires using(secret.NAME)",
				"Use: auth webhook_sig using(secret.WEBHOOK_SECRET)",
			)
		}
		return
	}
	if fn.Name != "webhook_sig" {
		return
	}
	secretName, ok := webhookSignatureSecretName(fn)
	if !ok {
		c.addError(meta.Loc,
			"auth webhook_sig requires using(secret.NAME)",
			"Use: auth webhook_sig using(secret.WEBHOOK_SECRET)",
		)
		return
	}
	sym := c.global.Lookup(secretName)
	if sym == nil || sym.Kind != SymSecret {
		c.addError(meta.Loc,
			fmt.Sprintf("auth webhook_sig references unknown secret %q", secretName),
			"Declare it with: secret "+secretName+" required",
		)
	}
}

func webhookSignatureSecretName(fn *ast.FnCall) (string, bool) {
	if fn == nil || fn.Name != "webhook_sig" || len(fn.Args) == 0 {
		return "", false
	}
	using, ok := fn.Args[0].(*ast.FnCall)
	if !ok || using.Name != "using" || len(using.Args) == 0 {
		return "", false
	}
	field, ok := using.Args[0].(*ast.FieldAccess)
	if !ok {
		return "", false
	}
	base, ok := field.Base.(*ast.Ident)
	if !ok || base.Name != "secret" || field.Field == "" {
		return "", false
	}
	return field.Field, true
}

var checkerBuiltinFns = map[string]bool{
	"api_key":          true,
	"archive":          true,
	"basic":            true,
	"bearer":           true,
	"broadcast":        true,
	"call":             true,
	"clock":            true,
	"count":            true,
	"delete":           true,
	"delete_s3_object": true,
	"download":         true,
	"emit":             true,
	"enqueue":          true,
	"event":            true,
	"export_bundle":    true,
	"fetch":            true,
	"fixture":          true,
	"import_bundle":    true,
	"inject":           true,
	"join":             true,
	"jwt":              true,
	"leave":            true,
	"level":            true,
	"limit":            true,
	"log":              true,
	"map":              true,
	"offset":           true,
	"on":               true,
	"order":            true,
	"paginate":         true,
	"pipe":             true,
	"publish":          true,
	"query":            true,
	"queue":            true,
	"rollback":         true,
	"save":             true,
	"seed":             true,
	"sleep":            true,
	"sum":              true,
	"timeout":          true,
	"track":            true,
	"transition":       true,
	"upgrade_save":     true,
	"update":           true,
	"upload":           true,
	"using":            true,
	"webhook_sig":      true,
	"where":            true,
}

func isCheckerBuiltinFn(name string) bool {
	return checkerBuiltinFns[name]
}

func isStorageOperation(name string) bool {
	switch name {
	case "upload", "download", "delete_s3_object":
		return true
	}
	return false
}

func (c *Checker) hasBlueprintEntry(key string) bool {
	if c.file == nil || c.file.Blueprint == nil {
		return false
	}
	for _, entry := range c.file.Blueprint.Entries {
		if entry.Key == key {
			return true
		}
	}
	return false
}

func (c *Checker) hasExternal(name string) bool {
	want := normalizeExternalName(name)
	for _, block := range c.file.Blocks {
		if ext, ok := block.(*ast.External); ok && normalizeExternalName(ext.Name) == want {
			return true
		}
	}
	return false
}

func normalizeExternalName(name string) string {
	name = strings.Trim(name, `"`)
	name = strings.ToLower(name)
	var b strings.Builder
	prevUnderscore := false
	for _, r := range name {
		if r == '-' || r == ' ' || r == '.' {
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
			continue
		}
		b.WriteRune(r)
		prevUnderscore = r == '_'
	}
	return b.String()
}

// ═══════════════════════════════════════════════
// Arrow Ordering
// ═══════════════════════════════════════════════

type arrowPhase int

const (
	phaseInput  arrowPhase = 0
	phaseStep   arrowPhase = 1
	phaseOutput arrowPhase = 2
)

func (c *Checker) checkArrowOrdering(stmts []ast.ArrowStmt, loc lexer.Loc, blockKind string) {
	phase := phaseInput
	for _, stmt := range stmts {
		switch stmt.(type) {
		case *ast.InputStmt:
			if phase > phaseInput {
				c.addErrorCode(stmt.Location(), CodeArrowStmtOrder,
					fmt.Sprintf("input (<-) must come before steps and outputs in %s", blockKind),
					"Move all <- declarations to the top of the block",
				)
			}
		case *ast.StepStmt, *ast.GuardStmt, *ast.WhenStmt, *ast.TryRecover,
			*ast.IntentStep, *ast.GenerateStep:
			if phase < phaseStep {
				phase = phaseStep
			}
			if phase > phaseStep {
				c.addErrorCode(stmt.Location(), CodeArrowStmtOrder,
					fmt.Sprintf("steps (|>) must come before outputs (->) in %s", blockKind),
					"Move -> statements to the end of the block",
				)
			}
		case *ast.OutputStmt:
			if phase < phaseOutput {
				phase = phaseOutput
			}
		}
	}
}

// ═══════════════════════════════════════════════
// Try/Recover Nesting
// ═══════════════════════════════════════════════

func (c *Checker) checkTryRecoverNesting(stmts []ast.ArrowStmt, _ lexer.Loc) {
	for _, stmt := range stmts {
		if tr, ok := stmt.(*ast.TryRecover); ok {
			c.checkNoNestedTryRecover(tr.Try)
			c.checkNoNestedTryRecover(tr.Recover)
		}
	}
}

func (c *Checker) checkNoNestedTryRecover(stmts []ast.ArrowStmt) {
	for _, stmt := range stmts {
		if _, ok := stmt.(*ast.TryRecover); ok {
			c.addErrorCode(stmt.Location(), CodeNestedTryRecover,
				"try/recover cannot be nested",
				"Flatten the error handling or use separate pipes",
			)
		}
	}
}

// ═══════════════════════════════════════════════
// Reference Checks
// ═══════════════════════════════════════════════

func (c *Checker) checkTypeRef(te ast.TypeExpr) {
	if te == nil {
		return
	}
	switch t := te.(type) {
	case *ast.TypedJSONType:
		c.checkTypeRef(t.Inner)
	case *ast.TranslationKeyType:
		sym := c.global.Lookup(t.Namespace)
		if sym == nil {
			c.addError(t.Loc, fmt.Sprintf("unknown translation namespace %q", t.Namespace), "Define it with: translation <name> { ... }")
			return
		}
		tr, ok := sym.Node.(*ast.Translation)
		if !ok {
			c.addError(t.Loc, fmt.Sprintf("%q is not a translation namespace", t.Namespace), "Use a translation block name in tkey(...) types")
			return
		}
		t.Keys = append(t.Keys[:0], tr.Keys...)
	case *ast.NamedType:
		if !IsPrimitive(t.Name) {
			if c.global.Lookup(t.Name) == nil {
				hint := "Define it with type, alias, or enum"
				candidates := append(c.global.NamesOfKind(SymType),
					append(c.global.NamesOfKind(SymEnum),
						c.global.NamesOfKind(SymAlias)...)...)
				if suggestion := suggestName(t.Name, candidates); suggestion != "" {
					hint += fmt.Sprintf("; did you mean %q?", suggestion)
				}
				c.addErrorCode(t.Loc, CodeUnknownType,
					fmt.Sprintf("unknown type %q", t.Name),
					hint,
				)
			}
		}
	case *ast.ListType:
		c.checkTypeRef(t.Element)
	case *ast.MapType:
		c.checkTypeRef(t.Key)
		c.checkTypeRef(t.Value)
	}
}

func (c *Checker) checkRefTarget(expr ast.Expr, loc lexer.Loc) {
	if id, ok := expr.(*ast.Ident); ok {
		if sym := c.global.Lookup(id.Name); sym == nil || sym.Kind != SymModel {
			hint := "Define the model or fix the name"
			if suggestion := suggestName(id.Name, c.global.NamesOfKind(SymModel)); suggestion != "" {
				hint += fmt.Sprintf("; did you mean %q?", suggestion)
			}
			c.addErrorCode(loc, CodeUnknownRefTarget,
				fmt.Sprintf("ref references unknown model %q", id.Name),
				hint,
			)
		}
	}
}

// builtinMiddleware are middleware names available without a declaration.
var builtinMiddleware = map[string]bool{
	"cors":           true,
	"request_logger": true,
	"rate_limit":     true,
	"timeout":        true,
	"compress":       true,
	"cache":          true,
}

func (c *Checker) checkMiddlewareRef(name string, loc lexer.Loc) {
	if builtinMiddleware[name] {
		return
	}
	sym := c.global.Lookup(name)
	if sym == nil {
		hint := "Define it with: middleware " + name + " { ... }"
		// Gather candidates from both user-defined and builtin middleware
		candidates := c.global.NamesOfKind(SymMiddleware)
		for builtin := range builtinMiddleware {
			candidates = append(candidates, builtin)
		}
		if suggestion := suggestName(name, candidates); suggestion != "" {
			hint += fmt.Sprintf("; did you mean %q?", suggestion)
		}
		c.addErrorCode(loc, CodeUnknownMiddleware,
			fmt.Sprintf("unknown middleware %q", name),
			hint,
		)
		return
	}
	if sym.Kind != SymMiddleware {
		c.addError(loc,
			fmt.Sprintf("%q is a %s, not a middleware", name, sym.Kind),
			"Use a middleware name here",
		)
	}
}

// ═══════════════════════════════════════════════
// Path Naming
// ═══════════════════════════════════════════════

func (c *Checker) checkPathNaming(path string, loc lexer.Loc) {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, ":") {
			param := seg[1:]
			if !isSnakeCase(param) {
				c.addErrorCode(loc, CodePathParamNotSnakeCase,
					fmt.Sprintf("path parameter %q must be snake_case", param),
					"Use lowercase letters, digits, and underscores",
				)
			}
		} else if !isKebabCase(seg) {
			c.addError(loc,
				fmt.Sprintf("path segment %q should be kebab-case", seg),
				"Use lowercase letters, digits, and hyphens (e.g. user-profiles)",
			)
		}
	}
}

// ═══════════════════════════════════════════════
// Naming Convention Helpers
// ═══════════════════════════════════════════════

func isSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if unicode.IsUpper(r) {
			return false
		}
		if r == '_' {
			if i == 0 || i == len(s)-1 || rune(s[i-1]) == '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isScreamingSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if r == '_' {
			if i == 0 || i == len(s)-1 || rune(s[i-1]) == '_' {
				return false
			}
			continue
		}
		if !unicode.IsUpper(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isPascalCase(s string) bool {
	if s == "" || !unicode.IsUpper(rune(s[0])) {
		return false
	}
	hasLower := false
	for _, r := range s {
		if r == '_' || r == '-' {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}
	return hasLower
}

func isKebabCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if unicode.IsUpper(r) {
			return false
		}
		if r == '_' {
			return false
		}
		if r == '-' {
			if i == 0 || i == len(s)-1 || rune(s[i-1]) == '-' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func (c *Checker) checkSnakeCase(name, what string, loc lexer.Loc) {
	if !isSnakeCase(name) {
		c.addErrorCode(loc, CodeIdentifierNotSnakeCase,
			fmt.Sprintf("%s name %q must be snake_case", what, name),
			"Use lowercase letters, digits, and underscores (e.g. my_thing)",
		)
	}
}

func (c *Checker) checkScreamingSnakeCase(name, what string, loc lexer.Loc) {
	if !isScreamingSnakeCase(name) {
		c.addError(loc,
			fmt.Sprintf("%s name %q must be SCREAMING_SNAKE_CASE", what, name),
			"Use uppercase letters, digits, and underscores (e.g. MY_THING)",
		)
	}
}

func (c *Checker) checkPascalCase(name, what string, loc lexer.Loc) {
	if !isPascalCase(name) {
		c.addError(loc,
			fmt.Sprintf("%s name %q must be PascalCase", what, name),
			"Start with uppercase, use mixed case (e.g. MyThing)",
		)
	}
}

// ═══════════════════════════════════════════════
// "Did you mean?" Suggestions
// ═══════════════════════════════════════════════

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// suggestName finds the closest match from candidates within a threshold.
func suggestName(name string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	bestDist := len(name)/2 + 1 // threshold: half the name length + 1
	best := ""
	for _, c := range candidates {
		d := levenshtein(strings.ToLower(name), strings.ToLower(c))
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// ═══════════════════════════════════════════════
// Error Formatting
// ═══════════════════════════════════════════════

// FormatCheckError formats a check error with source context for display,
// delegating to the shared internal/diag formatter.
func FormatCheckError(err CheckError, src []byte) string {
	return diag.Format(&diag.Diagnostic{
		Severity: diag.SeverityError,
		Code:     err.Code,
		Loc:      err.Loc,
		Message:  err.Message,
		Hint:     err.Hint,
	}, src)
}
