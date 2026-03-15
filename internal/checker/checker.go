package checker

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// CheckError represents a semantic error.
type CheckError struct {
	Loc     lexer.Loc
	Message string
	Hint    string
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
		c.addError(loc,
			fmt.Sprintf("duplicate %s name %q (previously defined at %s)", kind, name, existing.Loc),
			fmt.Sprintf("Rename one of the %s declarations", kind),
		)
	}
}

// --- Pass 2: Validation ---

func (c *Checker) validateBlueprint() {
	if c.file.Blueprint == nil {
		c.addError(c.file.Loc,
			"missing blueprint block",
			"Every .bp file must start with: blueprint \"name\" { ... }",
		)
		return
	}

	bp := c.file.Blueprint
	if bp.Name == "" {
		c.addError(bp.Loc, "blueprint name is empty", "Provide a name: blueprint \"my-app\" { ... }")
	}

	// Check required fields
	found := map[string]bool{}
	for _, e := range bp.Entries {
		found[e.Key] = true
	}
	if !found["version"] {
		c.addError(bp.Loc, "blueprint block missing required field 'version'", `Add: version "0.1.0"`)
	}
	if !found["runtime"] {
		c.addError(bp.Loc, "blueprint block missing required field 'runtime'", "Add: runtime node")
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
			c.addError(f.Loc,
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
	if n.Logic != nil {
		c.checkTryRecoverNesting(n.Logic.Stmts, n.Loc)
	}
}

// --- Pipe ---

func (c *Checker) checkPipe(n *ast.Pipe) {
	c.checkSnakeCase(n.Name, "pipe", n.Loc)
	c.checkArrowOrdering(n.Stmts, n.Loc, "pipe")
	c.checkTryRecoverNesting(n.Stmts, n.Loc)
}

// --- Middleware ---

func (c *Checker) checkMiddleware(n *ast.Middleware) {
	c.checkSnakeCase(n.Name, "middleware", n.Loc)
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
	}
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
	}
}

// --- WsEndpoint ---

func (c *Checker) checkWsEndpoint(n *ast.WsEndpoint) {
	c.checkPathNaming(n.Path, n.Loc)
	for _, m := range n.Meta {
		if m.Kind == "use" && m.Use != nil {
			c.checkMiddlewareRef(m.Use.Name, m.Loc)
		}
	}
}

// --- Duplicate Endpoint ---

func (c *Checker) checkDuplicateEndpoint(method, path string, loc lexer.Loc, seen map[string]lexer.Loc) {
	key := method + " " + path
	if prev, ok := seen[key]; ok {
		c.addError(loc,
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
}

// --- Schedule ---

func (c *Checker) checkSchedule(n *ast.Schedule) {
	c.checkSnakeCase(n.Name, "schedule", n.Loc)
	c.checkArrowOrdering(n.Stmts, n.Loc, "schedule")
}

// --- Subscribe ---

func (c *Checker) checkSubscribe(n *ast.Subscribe) {
	c.checkArrowOrdering(n.Stmts, n.Loc, "subscribe")
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
				c.addError(stmt.Location(),
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
				c.addError(stmt.Location(),
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
			c.addError(stmt.Location(),
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
				c.addError(t.Loc,
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
			c.addError(loc,
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
		c.addError(loc,
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
				c.addError(loc,
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
		c.addError(loc,
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

// FormatCheckError formats a check error with source context for display.
func FormatCheckError(err CheckError, src []byte) string {
	var b strings.Builder

	color := os.Getenv("NO_COLOR") == ""
	red, cyan, yellow, reset := "\033[31m", "\033[36m", "\033[33m", "\033[0m"
	if !color {
		red, cyan, yellow, reset = "", "", "", ""
	}

	fmt.Fprintf(&b, "%serror:%s %s%s%s\n\n", red, reset, cyan, err.Loc, reset)

	line := getSourceLine(src, err.Loc.Line)
	if line != "" {
		fmt.Fprintf(&b, "  %s\n", line)
		if err.Loc.Col > 0 {
			fmt.Fprintf(&b, "%s\n", strings.Repeat(" ", err.Loc.Col-1+2)+"^")
		}
	}

	fmt.Fprintf(&b, "\n  %s\n", err.Message)
	if err.Hint != "" {
		fmt.Fprintf(&b, "  %s%s%s\n", yellow, err.Hint, reset)
	}
	return b.String()
}

func getSourceLine(src []byte, lineNum int) string {
	lines := strings.Split(string(src), "\n")
	if lineNum >= 1 && lineNum <= len(lines) {
		return lines[lineNum-1]
	}
	return ""
}
