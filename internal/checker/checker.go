package checker

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/diag"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/naming"
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

	// Check required fields, duplicate configuration, and the scalar shapes
	// code generators rely on. The parser intentionally accepts generic
	// key/value expressions here; semantic validation must prevent malformed
	// values from silently falling back to generator defaults.
	found := map[string]bool{}
	locations := map[string]lexer.Loc{}
	for _, e := range bp.Entries {
		if previous, duplicate := locations[e.Key]; duplicate {
			c.addError(e.Loc,
				fmt.Sprintf("duplicate blueprint entry %q (previously defined at %s)", e.Key, previous),
				"Keep exactly one value for each blueprint setting",
			)
		} else {
			locations[e.Key] = e.Loc
		}
		found[e.Key] = true
		switch e.Key {
		case "version":
			if _, ok := e.Value.(*ast.StringLit); !ok {
				c.addError(e.Loc, "blueprint version must be a string", `Use: version "0.1.0"`)
			}
		case "port":
			value, ok := e.Value.(*ast.IntLit)
			if !ok {
				c.addError(e.Loc, "blueprint port must be an integer", "Use a TCP port from 1 through 65535")
				continue
			}
			port, err := strconv.Atoi(value.Value)
			if err != nil || port < 1 || port > 65535 {
				c.addError(e.Loc, fmt.Sprintf("blueprint port %q is outside 1..65535", value.Value), "Choose a valid TCP port")
			}
		case "runtime", "database", "cache", "storage":
			if _, ok := e.Value.(*ast.Ident); !ok {
				c.addError(e.Loc, fmt.Sprintf("blueprint %s must be an identifier", e.Key), fmt.Sprintf("Use: %s <name> without quotes", e.Key))
			}
		}
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
	// Detect duplicate persisted and computed fields in one shared namespace.
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
	computedTypes := make(map[string]computedFieldInfo, len(n.Fields)+len(n.ComputedFields))
	for _, f := range n.Fields {
		typ, supported := computedTypeFromTypeExpr(f.Type)
		total := false
		for _, constraint := range f.Constraints {
			// Match the generated Drizzle select shape: primary keys and
			// explicitly required columns are non-null. Defaults alone affect
			// inserts, but do not make a persisted column non-null.
			total = total || constraint.Kind == "required" || constraint.Kind == "primary"
		}
		computedTypes[f.Name] = computedFieldInfo{typ: typ, supported: supported, total: total, declared: computedTypeName(f.Type)}
	}
	for _, f := range n.ComputedFields {
		c.checkSnakeCase(f.Name, "computed field", f.Loc)
		if prevLoc, dup := seen[f.Name]; dup {
			c.addErrorCode(f.Loc, CodeDuplicateField,
				fmt.Sprintf("duplicate field '%s' in model '%s'", f.Name, n.Name),
				fmt.Sprintf("First defined at %s", prevLoc),
			)
		} else {
			seen[f.Name] = f.Loc
		}
		c.checkTypeRef(f.Type)
		expected, supported := computedTypeFromTypeExpr(f.Type)
		if !supported {
			c.addError(f.Loc,
				fmt.Sprintf("computed field %q uses unsupported result type", f.Name),
				"Use string, text, int, float, money, or bool for computed fields",
			)
			continue
		}
		actual, ok := c.inferComputedExprType(f.Expr, computedTypes, f.Name)
		if ok && !computedTypesAssignable(expected, actual) {
			c.addError(f.Loc,
				fmt.Sprintf("computed field %q declares %s but its expression produces %s", f.Name, expected, actual),
				"Change the declared type or expression so they agree",
			)
		}
		// Declaration order is the dependency order. This makes cycles and
		// forward references impossible while allowing computed-on-computed.
		computedTypes[f.Name] = computedFieldInfo{typ: expected, supported: true, total: true, declared: string(expected)}
	}
}

type computedFieldInfo struct {
	typ       computedValueType
	supported bool
	total     bool
	declared  string
}

type computedValueType string

const (
	computedString computedValueType = "string"
	computedInt    computedValueType = "int"
	computedFloat  computedValueType = "float"
	computedMoney  computedValueType = "money"
	computedBool   computedValueType = "bool"
)

func computedTypeFromTypeExpr(t ast.TypeExpr) (computedValueType, bool) {
	primitive, ok := t.(*ast.PrimitiveType)
	if !ok {
		return "", false
	}
	switch primitive.Name {
	case "string", "text":
		return computedString, true
	case "int":
		return computedInt, true
	case "float":
		return computedFloat, true
	case "money":
		return computedMoney, true
	case "bool":
		return computedBool, true
	default:
		return "", false
	}
}

func computedTypeName(t ast.TypeExpr) string {
	switch value := t.(type) {
	case *ast.PrimitiveType:
		return value.Name
	case *ast.NamedType:
		return value.Name
	default:
		return fmt.Sprintf("%T", t)
	}
}

func computedTypesAssignable(expected, actual computedValueType) bool {
	return expected == actual || expected == computedFloat && actual == computedInt
}

func isComputedNumeric(t computedValueType) bool {
	return t == computedInt || t == computedFloat || t == computedMoney
}

func (c *Checker) inferComputedExprType(expr ast.Expr, fields map[string]computedFieldInfo, computedName string) (computedValueType, bool) {
	switch e := expr.(type) {
	case *ast.StringLit:
		return computedString, true
	case *ast.IntLit:
		return computedInt, true
	case *ast.FloatLit:
		return computedFloat, true
	case *ast.BoolLit:
		return computedBool, true
	case *ast.Ident:
		if field, ok := fields[e.Name]; ok {
			if !field.supported {
				c.addError(e.Loc,
					fmt.Sprintf("computed field %q cannot use field %q of type %s", computedName, e.Name, field.declared),
					"Use fields with string, text, int, float, money, or bool values",
				)
				return "", false
			}
			if !field.total {
				c.addError(e.Loc,
					fmt.Sprintf("computed field %q cannot use nullable field %q", computedName, e.Name),
					"Computed fields must be total; make the source required or primary, or compute the fallback in application code",
				)
				return "", false
			}
			return field.typ, true
		}
		c.addError(e.Loc,
			fmt.Sprintf("computed field %q references unknown or later field %q", computedName, e.Name),
			"Reference a persisted field or a computed field declared earlier in the model",
		)
		return "", false
	case *ast.ParenExpr:
		return c.inferComputedExprType(e.Expr, fields, computedName)
	case *ast.UnaryExpr:
		operand, ok := c.inferComputedExprType(e.Operand, fields, computedName)
		if !ok {
			return "", false
		}
		switch e.Op {
		case "not":
			if operand == computedBool {
				return computedBool, true
			}
		case "-":
			if isComputedNumeric(operand) {
				return operand, true
			}
		}
		c.addError(e.Loc, fmt.Sprintf("operator %q is not valid for computed %s values", e.Op, operand), "Use a type-compatible pure expression")
		return "", false
	case *ast.BinaryExpr:
		left, leftOK := c.inferComputedExprType(e.Left, fields, computedName)
		right, rightOK := c.inferComputedExprType(e.Right, fields, computedName)
		if !leftOK || !rightOK {
			return "", false
		}
		switch e.Op {
		case "+":
			if left == computedString && right == computedString {
				return computedString, true
			}
			fallthrough
		case "-", "*", "/":
			if isComputedNumeric(left) && isComputedNumeric(right) {
				if left == computedFloat || right == computedFloat || e.Op == "/" {
					return computedFloat, true
				}
				if left == computedMoney && right == computedMoney && (e.Op == "+" || e.Op == "-") {
					return computedMoney, true
				}
				if left == computedInt && right == computedInt {
					return computedInt, true
				}
			}
		case "==", "!=":
			if computedTypesAssignable(left, right) || computedTypesAssignable(right, left) {
				return computedBool, true
			}
		case "<", ">", "<=", ">=":
			if isComputedNumeric(left) && isComputedNumeric(right) || left == computedString && right == computedString {
				return computedBool, true
			}
		case "and", "or":
			if left == computedBool && right == computedBool {
				return computedBool, true
			}
		}
		c.addError(e.Loc,
			fmt.Sprintf("operator %q is not valid for computed %s and %s values", e.Op, left, right),
			"Use a type-compatible pure expression",
		)
		return "", false
	default:
		c.addError(expr.Location(),
			fmt.Sprintf("computed field %q uses unsupported expression %T", computedName, expr),
			"Computed expressions may use literals, model fields, parentheses, and pure unary/binary operators",
		)
		return "", false
	}
}

func (c *Checker) checkContent(n *ast.Content) {
	c.checkSnakeCase(n.Name, "content", n.Loc)
	c.checkModel(n.AsModel())
}

// --- Fn ---

func (c *Checker) checkFn(n *ast.Fn) {
	c.checkSnakeCase(n.Name, "fn", n.Loc)
	if isCheckerBuiltinFn(n.Name) {
		c.addError(n.Loc,
			fmt.Sprintf("function name %q is reserved by a built-in", n.Name),
			"Rename the function so calls cannot be mistaken for a built-in operation",
		)
	}
	scope := c.newArrowScope()
	for _, inp := range n.Inputs {
		c.checkTypeRef(inp.Type)
		c.checkConstraints(inp.Constraints)
		c.declareArrowInput(scope, inp)
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
		c.checkArrowStmtExprsInScope(n.Logic.Stmts, scope)
	}
}

// --- Pipe ---

func (c *Checker) checkPipe(n *ast.Pipe) {
	c.checkSnakeCase(n.Name, "pipe", n.Loc)
	if isCheckerBuiltinFn(n.Name) {
		c.addError(n.Loc,
			fmt.Sprintf("pipe name %q is reserved by a built-in", n.Name),
			"Rename the pipe so calls cannot be mistaken for a built-in operation",
		)
	}
	if inputs := pipeInputs(n); len(inputs) > 1 && !hasDuplicateInputName(inputs) {
		c.addError(n.Loc,
			fmt.Sprintf("pipe %q may declare at most one input, got %d", n.Name, len(inputs)),
			"Pipes transform one value; keep a single <- input declaration",
		)
	}
	c.checkArrowOrdering(n.Stmts, n.Loc, "pipe")
	c.checkTryRecoverNesting(n.Stmts, n.Loc)
	c.checkArrowStmtExprsInScope(n.Stmts, c.newArrowScope())
}

func hasDuplicateInputName(inputs []*ast.InputStmt) bool {
	seen := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		if input == nil {
			continue
		}
		if seen[input.Name] {
			return true
		}
		seen[input.Name] = true
	}
	return false
}

// --- Middleware ---

func (c *Checker) checkMiddleware(n *ast.Middleware) {
	c.checkSnakeCase(n.Name, "middleware", n.Loc)
	scope := c.newArrowScope("header")
	c.checkArrowStmtExprsInScope(n.Before, scope)
	c.checkArrowStmtExprsInScope(n.After, scope)
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
	c.checkArrowStmtExprsInScope(n.Stmts, c.newEndpointScope(n.Meta))
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
	scope := c.newRealtimeEndpointScope(n.Path, n.Meta)
	c.checkArrowStmtExprsInScope(n.Stmts, scope)
	for _, h := range n.Handlers {
		c.checkExpr(h.Condition)
		handlerScope := cloneArrowScope(scope)
		bindArrowName(handlerScope, "event", SymVariable, h.Loc)
		c.checkStreamConditionRefs(h.Condition, handlerScope)
		c.checkArrowStmtExprsInScope(h.Body, handlerScope)
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
	scope := c.newRealtimeEndpointScope(n.Path, n.Meta)
	c.checkArrowStmtExprsInScope(n.OnConnect, scope)
	messageScope := cloneArrowScope(scope)
	bindArrowName(messageScope, "message", SymVariable, n.Loc)
	c.checkArrowStmtExprsInScope(n.OnMessage, messageScope)
	c.checkArrowStmtExprsInScope(n.OnDisconnect, cloneArrowScope(scope))
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
	c.checkArrowStmtExprsInScope(n.Stmts, c.newArrowScope())
	onFailScope := c.newArrowScope()
	for _, stmt := range n.Stmts {
		if input, ok := stmt.(*ast.InputStmt); ok {
			bindArrowName(onFailScope, input.Name, SymInput, input.Loc)
		}
	}
	bindArrowName(onFailScope, "error", SymVariable, n.Loc)
	c.checkArrowStmtExprsInScope(n.OnFail, onFailScope)
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
	c.checkArrowStmtExprsInScope(n.Stmts, c.newArrowScope())
}

// --- Subscribe ---

func (c *Checker) checkSubscribe(n *ast.Subscribe) {
	c.checkArrowOrdering(n.Stmts, n.Loc, "subscribe")
	if n.From != "" && !c.hasExternal(n.From) {
		c.addError(n.Loc, fmt.Sprintf("subscribe references unknown external %q", n.From), "Declare it with: external \"service-name\" { ... }")
	}
	scope := c.newArrowScope()
	bindArrowName(scope, "event", SymVariable, n.Loc)
	c.checkArrowStmtExprsInScope(n.Stmts, scope)
}

// --- Enum ---

func (c *Checker) checkEnum(n *ast.Enum) {
	c.checkPascalCase(n.Name, "enum", n.Loc)
	seen := make(map[string]lexer.Loc, len(n.Variants))
	for _, variant := range n.Variants {
		if previous, exists := seen[variant.Name]; exists {
			c.addError(variant.Loc,
				fmt.Sprintf("duplicate variant %q in enum %q", variant.Name, n.Name),
				fmt.Sprintf("First defined at %s; remove or rename one of the variants", previous),
			)
			continue
		}
		seen[variant.Name] = variant.Loc
	}
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
	scope := c.newArrowScope()
	c.checkArrowStmtExprsInScope(n.Setup, scope)
	if n.Request != nil {
		for _, kv := range n.Request.Entries {
			c.checkExpr(kv.Value)
			c.checkLocalExpr(kv.Value, scope)
		}
	}
	c.checkArrowStmtExprsInScope(n.Cleanup, scope)
}

// --- TestGroup ---

func (c *Checker) checkTestGroup(n *ast.TestGroup) {
	c.checkSnakeCase(n.Name, "test_group", n.Loc)
	c.checkArrowStmtExprsInScope(n.SharedSetup, c.newArrowScope())
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

// newArrowScope creates a block-local scope. Generated expressions expose
// declared configuration through env.NAME. `secret.NAME` is reserved for
// syntax that interprets it explicitly (currently webhook auth); it is not a
// general expression namespace in target code.
func (c *Checker) newArrowScope(names ...string) *Scope {
	scope := NewScope(c.global)
	for _, name := range append([]string{"env"}, names...) {
		bindArrowName(scope, name, SymVariable, lexer.Loc{})
	}
	return scope
}

func (c *Checker) newEndpointScope(meta []*ast.EndpointMeta) *Scope {
	scope := c.newArrowScope("header")
	for _, item := range meta {
		if item.Kind == "auth" {
			if call, ok := item.Value.(*ast.FnCall); ok && call.Name == "webhook_sig" {
				// Webhook auth parses the verified request body into `data`
				// before endpoint statements are emitted.
				bindArrowName(scope, "data", SymVariable, item.Loc)
			}
		}
		if item.Kind != "use" || item.Use == nil {
			continue
		}
		c.bindMiddlewareInjections(scope, item.Use.Name)
	}
	return scope
}

// STREAM and WS generators extract path parameters before their handlers.
// REST generators only declare path parameters that also have an explicit
// `<- name type` input, which the statement walk binds in source order.
func (c *Checker) newRealtimeEndpointScope(path string, meta []*ast.EndpointMeta) *Scope {
	scope := c.newEndpointScope(meta)
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, ":") && len(segment) > 1 {
			bindArrowName(scope, strings.TrimPrefix(segment, ":"), SymInput, lexer.Loc{})
		}
	}
	return scope
}

func (c *Checker) bindMiddlewareInjections(scope *Scope, name string) {
	symbol := c.global.Lookup(name)
	if symbol == nil || symbol.Kind != SymMiddleware {
		return
	}
	middleware, ok := symbol.Node.(*ast.Middleware)
	if !ok {
		return
	}
	for injected, loc := range injectedArrowNames(middleware.Before) {
		bindArrowName(scope, injected, SymVariable, loc)
	}
}

func bindArrowName(scope *Scope, name string, kind SymbolKind, loc lexer.Loc) {
	if scope == nil || name == "" {
		return
	}
	scope.Define(&Symbol{Name: name, Kind: kind, Loc: loc})
}

// declareArrowInput defines a source-level <- input while preserving the
// synthetic path parameters seeded for realtime endpoints. A real declaration
// replaces a synthetic location so a second <- with the same name is reported
// at the duplicate rather than silently folded into the scope.
func (c *Checker) declareArrowInput(scope *Scope, input *ast.InputStmt) {
	if scope == nil || input == nil || input.Name == "" {
		return
	}
	if existing, ok := scope.symbols[input.Name]; ok {
		if existing.Kind == SymInput && existing.Loc.Line == 0 {
			existing.Loc = input.Loc
			existing.Node = c.modelNodeForType(input.Type)
			return
		}
		hint := "Remove or rename one of the <- input declarations"
		if existing.Loc.Line > 0 {
			hint = fmt.Sprintf("First declared at %s; remove or rename one of the <- inputs", existing.Loc)
		}
		c.addError(input.Loc, fmt.Sprintf("duplicate input %q", input.Name), hint)
		return
	}
	scope.Define(&Symbol{
		Name: input.Name,
		Kind: SymInput,
		Loc:  input.Loc,
		Node: c.modelNodeForType(input.Type),
	})
}

func (c *Checker) modelNodeForType(typ ast.TypeExpr) ast.Node {
	named, ok := typ.(*ast.NamedType)
	if !ok {
		return nil
	}
	symbol := c.global.Lookup(named.Name)
	if symbol == nil || symbol.Kind != SymModel {
		return nil
	}
	return symbol.Node
}

func pipeInputs(pipe *ast.Pipe) []*ast.InputStmt {
	if pipe == nil {
		return nil
	}
	var inputs []*ast.InputStmt
	for _, stmt := range pipe.Stmts {
		if input, ok := stmt.(*ast.InputStmt); ok {
			inputs = append(inputs, input)
		}
	}
	return inputs
}

// pipeCallInputs mirrors the current target contract: a pipe transforms one
// value. A declared <- gives that parameter its source name; a pipe without an
// explicit <- retains the generator's implicit `input` parameter.
func pipeCallInputs(pipe *ast.Pipe) []*ast.InputStmt {
	inputs := pipeInputs(pipe)
	if len(inputs) > 0 {
		return inputs[:1]
	}
	return []*ast.InputStmt{{Name: "input"}}
}

func (c *Checker) addDuplicateLocalBinding(name string, loc, previous lexer.Loc) {
	c.addError(loc,
		fmt.Sprintf("duplicate local binding %q", name),
		fmt.Sprintf("First bound at %s; choose a new name (only <- inputs may be reassigned)", previous),
	)
}

// bindArrowModelValue records the model carried by a runtime binding. That
// lets map bodies expose the generator's singular model alias (for example,
// `map old: log job.id`) without treating every top-level model declaration as
// a runtime value in unrelated expressions.
func (c *Checker) bindArrowModelValue(scope *Scope, name, modelName string, loc lexer.Loc) {
	if scope == nil || name == "" {
		return
	}
	symbol := &Symbol{Name: name, Kind: SymVariable, Loc: loc}
	if model := c.global.Lookup(modelName); model != nil && model.Kind == SymModel {
		symbol.Node = model.Node
	}
	if existing, ok := scope.symbols[name]; ok {
		existing.Node = symbol.Node
		return
	}
	scope.Define(symbol)
}

func cloneArrowScope(scope *Scope) *Scope {
	if scope == nil {
		return nil
	}
	clone := NewScope(scope.parent)
	for name, symbol := range scope.symbols {
		copy := *symbol
		clone.symbols[name] = &copy
	}
	return clone
}

func mergeArrowScope(dst, src *Scope) {
	if dst == nil || src == nil {
		return
	}
	for name, symbol := range src.symbols {
		copy := *symbol
		dst.symbols[name] = &copy
	}
}

func injectedArrowNames(stmts []ast.ArrowStmt) map[string]lexer.Loc {
	result := make(map[string]lexer.Loc)
	for _, stmt := range stmts {
		step, ok := stmt.(*ast.StepStmt)
		if !ok {
			continue
		}
		call, ok := step.Expr.(*ast.FnCall)
		if !ok || call.Name != "inject" || len(call.Args) < 2 {
			continue
		}
		if alias, ok := call.Args[1].(*ast.Ident); ok {
			result[alias.Name] = alias.Loc
		}
	}
	return result
}

func (c *Checker) checkArrowStmtExprsInScope(stmts []ast.ArrowStmt, scope *Scope) {
	// A step may intentionally rebind a <- input (the generators make those
	// inputs mutable where needed). A name first introduced by another step is
	// a local declaration, however, and declaring it again in the same lexical
	// statement list would produce an invalid or ambiguous target assignment.
	localBindings := make(map[string]lexer.Loc)
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.InputStmt:
			c.checkTypeRef(s.Type)
			c.checkConstraints(s.Constraints)
			c.declareArrowInput(scope, s)
		case *ast.StepStmt:
			c.checkExpr(s.Expr)
			c.checkLocalExpr(s.Expr, scope)
			modelName := c.arrowExprModelName(s.Expr, scope)
			if call, ok := s.Expr.(*ast.FnCall); ok && call.Name == "inject" && len(call.Args) >= 2 {
				if alias, ok := call.Args[1].(*ast.Ident); ok {
					bindArrowName(scope, alias.Name, SymVariable, alias.Loc)
				}
			}
			if s.Binding == "" {
				if name, loc := implicitMapResultBinding(s.Expr); name != "" {
					if previous, duplicate := localBindings[name]; duplicate {
						c.addDuplicateLocalBinding(name, loc, previous)
					} else {
						localBindings[name] = loc
						c.bindArrowModelValue(scope, name, modelName, loc)
					}
				}
				continue
			}
			if input := scope.symbols[s.Binding]; input != nil && input.Kind == SymInput {
				// Rebinding an input is assignment, not a second declaration. Keep
				// its SymInput kind so subsequent reassignments remain valid.
				if modelName != "" {
					c.bindArrowModelValue(scope, s.Binding, modelName, s.Loc)
				} else {
					// The reassignment may change a model-typed input into an
					// unrelated value; do not retain stale field metadata.
					input.Node = nil
				}
				continue
			}
			if previous, duplicate := localBindings[s.Binding]; duplicate {
				c.addDuplicateLocalBinding(s.Binding, s.Loc, previous)
				continue
			}
			localBindings[s.Binding] = s.Loc
			if modelName != "" {
				c.bindArrowModelValue(scope, s.Binding, modelName, s.Loc)
			} else {
				bindArrowName(scope, s.Binding, SymVariable, s.Loc)
			}
		case *ast.GuardStmt:
			c.checkExpr(s.Condition)
			c.checkLocalExpr(s.Condition, scope)
		case *ast.WhenStmt:
			c.checkExpr(s.Condition)
			c.checkLocalExpr(s.Condition, scope)
			c.checkExpr(s.Inline)
			c.checkLocalExpr(s.Inline, scope)
			// Code generators emit block-form `when` bodies in a lexical block;
			// declarations made there are unavailable afterward or to siblings.
			c.checkArrowStmtExprsInScope(s.Body, cloneArrowScope(scope))
		case *ast.OutputStmt:
			c.checkExpr(s.Value)
			c.checkLocalExpr(s.Value, scope)
		case *ast.TryRecover:
			tryScope := cloneArrowScope(scope)
			c.checkArrowStmtExprsInScope(s.Try, tryScope)
			recoverScope := cloneArrowScope(scope)
			bindArrowName(recoverScope, "error", SymVariable, s.Loc)
			c.checkArrowStmtExprsInScope(s.Recover, recoverScope)
			// Successful try bindings are intentionally available to following
			// outputs (a common `try { result = ... } ... -> result` pattern).
			mergeArrowScope(scope, tryScope)
		case *ast.GenerateStep:
			for _, hint := range s.Hints {
				c.checkExpr(hint.Value)
				c.checkLocalExpr(hint.Value, scope)
			}
		}
	}
}

// implicitMapResultBinding mirrors the cross-target map convention: an
// unbound `map rows: save order_item { ... }` may be referenced afterward as
// `order_items`.
func implicitMapResultBinding(expr ast.Expr) (string, lexer.Loc) {
	call, ok := expr.(*ast.FnCall)
	if !ok || call.Name != "map" || len(call.Args) < 2 {
		return "", lexer.Loc{}
	}
	body, ok := call.Args[1].(*ast.FnCall)
	if !ok || !isCheckerDataOperation(body.Name) || len(body.Args) == 0 {
		return "", lexer.Loc{}
	}
	model, ok := body.Args[0].(*ast.Ident)
	if !ok {
		return "", lexer.Loc{}
	}
	return naming.Pluralize(model.Name), model.Loc
}

func isCheckerDataOperation(name string) bool {
	switch name {
	case "query", "fetch", "save", "update", "delete", "count", "seed", "import_bundle", "export_bundle":
		return true
	default:
		return false
	}
}

// arrowExprModelName returns the model represented by a step result when it is
// statically knowable. The checker only needs this small fact to mirror map's
// generated `item`/singular-model aliases; deeper type facts belong in resolve.
func (c *Checker) arrowExprModelName(expr ast.Expr, scope *Scope) string {
	call, ok := expr.(*ast.FnCall)
	if !ok {
		return ""
	}
	if call.Name == "map" && len(call.Args) > 1 {
		return c.arrowExprModelName(call.Args[1], scope)
	}
	switch call.Name {
	case "query", "fetch", "save", "update", "seed", "import_bundle":
	default:
		return ""
	}
	if len(call.Args) == 0 {
		return ""
	}
	ident, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return ""
	}
	if symbol := scope.Lookup(ident.Name); symbol != nil {
		if modelName := arrowSymbolModelName(symbol); modelName != "" {
			return modelName
		}
	}
	if symbol := c.global.Lookup(ident.Name); symbol != nil && symbol.Kind == SymModel {
		return ident.Name
	}
	return ""
}

func arrowSymbolModelName(symbol *Symbol) string {
	if symbol == nil || symbol.Node == nil {
		return ""
	}
	switch node := symbol.Node.(type) {
	case *ast.Model:
		return node.Name
	case *ast.Content:
		return node.Name
	default:
		return ""
	}
}

func (c *Checker) checkLocalExpr(expr ast.Expr, scope *Scope) {
	if expr == nil {
		return
	}
	switch item := expr.(type) {
	case *ast.Ident:
		c.checkLocalIdent(item, scope)
	case *ast.BinaryExpr:
		if item.Op == "=" {
			c.checkComputedFieldAssignment(item.Left, scope)
		}
		c.checkLocalExpr(item.Left, scope)
		c.checkLocalExpr(item.Right, scope)
	case *ast.UnaryExpr:
		c.checkLocalExpr(item.Operand, scope)
	case *ast.FnCall:
		c.checkLocalCallArgs(item, scope)
	case *ast.FieldAccess:
		c.checkLocalExpr(item.Base, scope)
		c.checkKnownModelFieldAccess(item, scope)
	case *ast.IndexAccess:
		c.checkLocalExpr(item.Base, scope)
		c.checkLocalExpr(item.Index, scope)
	case *ast.ParenExpr:
		c.checkLocalExpr(item.Expr, scope)
	case *ast.ListExpr:
		for _, element := range item.Elements {
			c.checkLocalExpr(element, scope)
		}
	case *ast.BlockExpr:
		for _, entry := range item.Entries {
			c.checkLocalExpr(entry.Value, scope)
		}
	case *ast.StringLit:
		for _, name := range interpolationRootNames(item.Value) {
			c.checkLocalIdent(&ast.Ident{Loc: item.Loc, Name: name}, scope)
		}
	}
}

func (c *Checker) checkComputedFieldAssignment(expr ast.Expr, scope *Scope) {
	access, ok := expr.(*ast.FieldAccess)
	if !ok || scope == nil {
		return
	}
	modelName := ""
	switch base := access.Base.(type) {
	case *ast.Ident:
		modelName = arrowSymbolModelName(scope.Lookup(base.Name))
	case *ast.FieldAccess:
		root, ok := base.Base.(*ast.Ident)
		if !ok {
			return
		}
		sourceModel := arrowSymbolModelName(scope.Lookup(root.Name))
		_, target, found := relationDefinition(fieldsForModel(c, sourceModel), base.Field)
		if found {
			modelName = target
		}
	}
	if modelHasComputedField(c.modelNode(modelName), access.Field) {
		c.addError(access.Loc,
			fmt.Sprintf("cannot assign to computed field %q on model %q", access.Field, modelName),
			"Change one of the persisted source fields instead",
		)
	}
}

// interpolationRootNames returns the leading identifier from every `{...}`
// segment in a string. Interpolation bodies are stored as raw StringLit text
// and emitted verbatim by codegen, so validating their root is the strongest
// sound check available without pretending they are parsed Blueprint Exprs.
func interpolationRootNames(value string) []string {
	var roots []string
	for i := 0; i < len(value); {
		if value[i] != '{' {
			i++
			continue
		}
		i++
		start := i
		depth := 1
		for i < len(value) && depth > 0 {
			switch value[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					break
				}
			}
			i++
		}
		body := strings.TrimSpace(value[start:i])
		if i < len(value) && value[i] == '}' {
			i++
		}
		if body == "" || !isInterpolationIdentStart(body[0]) {
			continue
		}
		end := 1
		for end < len(body) && isInterpolationIdentContinue(body[end]) {
			end++
		}
		roots = append(roots, body[:end])
	}
	return roots
}

func isInterpolationIdentStart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func isInterpolationIdentContinue(ch byte) bool {
	return isInterpolationIdentStart(ch) || ch >= '0' && ch <= '9'
}

func (c *Checker) checkLocalIdent(ident *ast.Ident, scope *Scope) {
	if ident == nil || ident.Name == "close" {
		return
	}
	if scope != nil {
		if symbol := scope.Lookup(ident.Name); symbol != nil {
			switch symbol.Kind {
			case SymVariable, SymInput, SymEnum:
				return
			}
		}
	}
	if strings.HasPrefix(ident.Name, "pipe_") {
		name := strings.TrimPrefix(ident.Name, "pipe_")
		if symbol := c.global.Lookup(name); symbol != nil && symbol.Kind == SymPipe {
			c.addArityError(ident.Loc, symbol, 1, 0)
			return
		}
	}
	c.addError(ident.Loc,
		fmt.Sprintf("unbound identifier %q", ident.Name),
		"Declare it with <-, bind it with |> name = ..., or fix the name",
	)
}

func (c *Checker) checkLocalCallArgs(call *ast.FnCall, scope *Scope) {
	if call == nil {
		return
	}
	switch call.Name {
	case "query", "fetch", "save", "update", "delete", "count", "seed", "import_bundle", "export_bundle":
		c.checkDataOperationRefs(call, scope)
	case "where", "with", "order":
		// These are normally handled with model-field context by their
		// enclosing data operation. Standalone uses are schema syntax.
		return
	case "call":
		// The first two identifiers are an external service and HTTP method.
		for i, arg := range call.Args {
			if i < 2 {
				continue
			}
			c.checkLocalExpr(arg, scope)
		}
	case "emit":
		// Bare event and destination names are declarations, not locals.
		for _, arg := range call.Args {
			if _, symbolic := arg.(*ast.Ident); symbolic {
				continue
			}
			c.checkLocalExpr(arg, scope)
		}
	case "enqueue":
		for i, arg := range call.Args {
			if i == 0 {
				continue
			}
			c.checkLocalExpr(arg, scope)
		}
	case "inject":
		if len(call.Args) > 0 {
			c.checkLocalExpr(call.Args[0], scope)
		}
		// The optional second identifier declares the context alias.
	case "log":
		if len(call.Args) > 0 {
			c.checkLocalExpr(call.Args[0], scope)
		}
		// Remaining bare identifiers are log levels (info, warn, error).
		for _, arg := range call.Args[1:] {
			if _, symbolic := arg.(*ast.Ident); !symbolic {
				c.checkLocalExpr(arg, scope)
			}
		}
	case "map":
		if len(call.Args) > 0 {
			c.checkLocalExpr(call.Args[0], scope)
		}
		if len(call.Args) > 1 {
			itemScope := cloneArrowScope(scope)
			modelName := ""
			if collection, ok := call.Args[0].(*ast.Ident); ok {
				modelName = arrowSymbolModelName(scope.Lookup(collection.Name))
			}
			if modelName != "" {
				c.bindArrowModelValue(itemScope, "item", modelName, call.Loc)
				c.bindArrowModelValue(itemScope, modelName, modelName, call.Loc)
			} else {
				bindArrowName(itemScope, "item", SymVariable, call.Loc)
			}
			c.checkLocalExpr(call.Args[1], itemScope)
		}
	case "join", "leave", "broadcast", "whisper":
		for i, arg := range call.Args {
			if i == 0 {
				if target, ok := arg.(*ast.FnCall); ok {
					for _, targetArg := range target.Args {
						c.checkLocalExpr(targetArg, scope)
					}
					continue
				}
			}
			c.checkLocalExpr(arg, scope)
		}
	case "level", "event", "queue":
		// Arguments to these helpers are symbolic names.
		return
	case "transition", "upgrade_save":
		for i, arg := range call.Args {
			if i == 0 {
				continue
			}
			c.checkLocalExpr(arg, scope)
		}
	default:
		for _, arg := range call.Args {
			c.checkLocalExpr(arg, scope)
		}
	}
}

func (c *Checker) checkDataOperationRefs(call *ast.FnCall, scope *Scope) {
	if len(call.Args) == 0 {
		return
	}
	model, ok := call.Args[0].(*ast.Ident)
	if !ok {
		c.addError(call.Loc,
			fmt.Sprintf("data operation %q requires a model name", call.Name),
			"Pass a declared model or content name as the first argument",
		)
		for _, arg := range call.Args[1:] {
			c.checkLocalExpr(arg, scope)
		}
		return
	}
	modelName := model.Name
	fields, knownModel := c.modelFieldNames(modelName)
	localTarget := arrowScopeHasRuntimeName(scope, modelName)
	fieldModelName := modelName
	if !knownModel && localTarget {
		if resolved := arrowSymbolModelName(scope.Lookup(modelName)); resolved != "" {
			fieldModelName = resolved
			fields, knownModel = c.modelFieldNames(resolved)
		}
	}
	if !knownModel {
		if call.Name == "update" || call.Name == "delete" {
			if !localTarget {
				c.addError(model.Loc,
					fmt.Sprintf("data operation %q references unknown model or binding %q", call.Name, modelName),
					"Use a declared model or a value bound earlier in this block",
				)
			}
		} else {
			c.addError(model.Loc,
				fmt.Sprintf("data operation %q references unknown model %q", call.Name, modelName),
				"Declare the model or fix its name",
			)
		}
	}
	if call.Name == "save" || call.Name == "seed" || call.Name == "update" {
		for _, arg := range call.Args[1:] {
			block, ok := arg.(*ast.BlockExpr)
			if !ok {
				continue
			}
			for _, entry := range block.Entries {
				if modelHasComputedField(c.modelNode(fieldModelName), entry.Key) {
					c.addError(entry.Loc,
						fmt.Sprintf("%s cannot write computed field %q on model %q", call.Name, entry.Key, fieldModelName),
						"Remove the computed field from the write body; it is derived from persisted fields",
					)
				}
			}
		}
	}
	seenModifiers := map[string]lexer.Loc{}
	seenRelations := map[string]lexer.Loc{}
	seenTargets := map[string]string{}
	for _, arg := range call.Args[1:] {
		if marker, ok := arg.(*ast.FnCall); ok {
			if previous, duplicate := seenModifiers[marker.Name]; duplicate {
				c.addError(marker.Loc,
					fmt.Sprintf("duplicate %s modifier on %s", marker.Name, call.Name),
					fmt.Sprintf("Keep one %s(...) modifier; the first is at %s", marker.Name, previous),
				)
			} else {
				seenModifiers[marker.Name] = marker.Loc
			}
			switch marker.Name {
			case "where":
				for _, condition := range marker.Args {
					if knownModel {
						c.checkQueryExpr(condition, scope, fields)
					} else {
						c.checkUnknownModelQueryExpr(condition, scope)
					}
				}
				continue
			case "order":
				for _, orderArg := range marker.Args {
					if ident, ok := orderArg.(*ast.Ident); ok {
						if !knownModel || ident.Name == "asc" || ident.Name == "desc" || fields[ident.Name] {
							continue
						}
					}
					c.checkLocalExpr(orderArg, scope)
				}
				continue
			case "with":
				if call.Name != "query" {
					c.addError(marker.Loc, fmt.Sprintf("%s does not support with(...) relationships", call.Name), "Use with(...) on a query operation")
				}
				if len(marker.Args) == 0 {
					c.addError(marker.Loc, "with(...) requires at least one relationship", "Name a ref-backed relationship, for example with(author)")
				}
				for _, relationExpr := range marker.Args {
					relation, symbolic := relationExpr.(*ast.Ident)
					if !symbolic {
						c.addError(relationExpr.Location(), "with(...) relationships must be bare identifiers", "Use with(author), not a dynamic expression")
						continue
					}
					if previous, duplicate := seenRelations[relation.Name]; duplicate {
						c.addError(relation.Loc, fmt.Sprintf("duplicate relationship %q in with(...) modifiers", relation.Name), fmt.Sprintf("Remove the duplicate; first requested at %s", previous))
						continue
					}
					seenRelations[relation.Name] = relation.Loc
					if !knownModel {
						continue
					}
					if relation.Name == "_base" || fields[relation.Name] || modelHasComputedField(c.modelNode(modelName), relation.Name) {
						c.addError(relation.Loc,
							fmt.Sprintf("relationship %q collides with a field on model %q", relation.Name, modelName),
							"Rename the relationship or the colliding model field",
						)
						continue
					}
					sourceField, target, found := relationDefinition(fieldsForModel(c, modelName), relation.Name)
					if !found {
						c.addError(relation.Loc,
							fmt.Sprintf("model %q has no ref-backed relationship %q", modelName, relation.Name),
							fmt.Sprintf("Declare %s_id with ref(<model>) or remove it from with(...)", relation.Name),
						)
						continue
					}
					targetFields, _ := c.modelFields(target)
					targetID := modelFieldNamed(targetFields, "id")
					if targetID == nil {
						c.addError(relation.Loc,
							fmt.Sprintf("relationship %q targets model %q without a persisted id field", relation.Name, target),
							"Add an id field to the target model; with(...) joins refs against target.id",
						)
						continue
					}
					if !modelFieldHasConstraint(targetID, "primary") && !modelFieldHasConstraint(targetID, "unique") {
						c.addError(relation.Loc,
							fmt.Sprintf("relationship %q targets %s.id, which is neither primary nor unique", relation.Name, target),
							"Mark the target id field primary or unique so each source row joins at most one target row",
						)
						continue
					}
					if checkerTypeKey(sourceField.Type) != checkerTypeKey(targetID.Type) {
						c.addError(relation.Loc,
							fmt.Sprintf("relationship %q has type %s but %s.id has type %s", relation.Name, checkerTypeKey(sourceField.Type), target, checkerTypeKey(targetID.Type)),
							"Use the same type for the ref field and target id",
						)
						continue
					}
					if target == modelName {
						c.addError(relation.Loc,
							fmt.Sprintf("self relationship %q on model %q requires a join alias", relation.Name, modelName),
							"Self-join aliases are not supported in this release; omit it from with(...)",
						)
						continue
					}
					if previous, collision := seenTargets[target]; collision && previous != relation.Name {
						c.addError(relation.Loc,
							fmt.Sprintf("relationships %q and %q both join model %q", previous, relation.Name, target),
							"This release does not alias repeated joins to the same target model; request one relationship",
						)
					} else {
						seenTargets[target] = relation.Name
					}
				}
				continue
			}
		}
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == "first" {
			if previous, duplicate := seenModifiers["first"]; duplicate {
				c.addError(ident.Loc, "duplicate first modifier", fmt.Sprintf("Keep one first modifier; the first is at %s", previous))
			} else {
				seenModifiers["first"] = ident.Loc
			}
			continue
		}
		c.checkLocalExpr(arg, scope)
	}
	if _, paginated := seenModifiers["paginate"]; paginated {
		if firstLoc, first := seenModifiers["first"]; first {
			c.addError(firstLoc, "query cannot combine paginate(...) and first", "Choose a paginated collection or a single first record")
		}
	}
	if withLoc, hasWith := seenModifiers["with"]; hasWith {
		for _, arg := range call.Args[1:] {
			if isStructuredDataOperationModifier(arg) {
				continue
			}
			c.addError(withLoc,
				"query with(...) cannot be combined with legacy positional or block arguments",
				"Use structured where(...), order(...), paginate(...), and first modifiers",
			)
			break
		}
	}
}

func isStructuredDataOperationModifier(expr ast.Expr) bool {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name == "first"
	}
	marker, ok := expr.(*ast.FnCall)
	if !ok {
		return false
	}
	switch marker.Name {
	case "where", "with", "order", "paginate":
		return true
	default:
		return false
	}
}

func fieldsForModel(c *Checker, name string) []*ast.Field {
	fields, _ := c.modelFields(name)
	return fields
}

func relationDefinition(fields []*ast.Field, relation string) (*ast.Field, string, bool) {
	for _, field := range fields {
		if field.Name != relation+"_id" {
			continue
		}
		for _, constraint := range field.Constraints {
			if constraint.Kind != "ref" {
				continue
			}
			if target, ok := constraint.Value.(*ast.Ident); ok {
				return field, target.Name, true
			}
		}
	}
	return nil, "", false
}

func modelFieldNamed(fields []*ast.Field, name string) *ast.Field {
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

func modelFieldHasConstraint(field *ast.Field, kind string) bool {
	for _, constraint := range field.Constraints {
		if constraint.Kind == kind {
			return true
		}
	}
	return false
}

func modelHasComputedField(model *ast.Model, name string) bool {
	if model == nil {
		return false
	}
	for _, field := range model.ComputedFields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func checkerTypeKey(t ast.TypeExpr) string {
	switch value := t.(type) {
	case *ast.PrimitiveType:
		return value.Name
	case *ast.NamedType:
		return value.Name
	case *ast.TypedJSONType:
		return "json<" + checkerTypeKey(value.Inner) + ">"
	case *ast.ListType:
		return "list(" + checkerTypeKey(value.Element) + ")"
	case *ast.MapType:
		return "map(" + checkerTypeKey(value.Key) + "," + checkerTypeKey(value.Value) + ")"
	default:
		return fmt.Sprintf("%T", t)
	}
}

func arrowScopeHasRuntimeName(scope *Scope, name string) bool {
	for current := scope; current != nil; current = current.parent {
		symbol, exists := current.symbols[name]
		if !exists {
			continue
		}
		return symbol.Kind == SymVariable || symbol.Kind == SymInput
	}
	return false
}

func (c *Checker) modelFieldNames(name string) (map[string]bool, bool) {
	fields, ok := c.modelFields(name)
	if !ok {
		return nil, false
	}
	result := make(map[string]bool, len(fields))
	for _, field := range fields {
		result[field.Name] = true
	}
	return result, true
}

func (c *Checker) modelNode(name string) *ast.Model {
	symbol := c.global.Lookup(name)
	if symbol == nil || symbol.Kind != SymModel {
		return nil
	}
	switch node := symbol.Node.(type) {
	case *ast.Model:
		return node
	case *ast.Content:
		return node.AsModel()
	default:
		return nil
	}
}

func (c *Checker) modelFields(name string) ([]*ast.Field, bool) {
	symbol := c.global.Lookup(name)
	if symbol == nil || symbol.Kind != SymModel {
		return nil, false
	}
	var fields []*ast.Field
	switch node := symbol.Node.(type) {
	case *ast.Model:
		fields = node.Fields
	case *ast.Content:
		fields = node.AsModel().Fields
	default:
		return nil, false
	}
	return fields, true
}

// checkKnownModelFieldAccess validates the narrow, high-confidence case where
// the base is a runtime identifier already known to carry a model. Nested JSON
// and relation leaves remain intentionally unchecked; their base is another
// FieldAccess rather than an Ident and needs richer type propagation.
func (c *Checker) checkKnownModelFieldAccess(access *ast.FieldAccess, scope *Scope) {
	if access == nil || scope == nil {
		return
	}
	base, ok := access.Base.(*ast.Ident)
	if !ok {
		return
	}
	symbol := scope.Lookup(base.Name)
	if symbol == nil || (symbol.Kind != SymVariable && symbol.Kind != SymInput) {
		return
	}
	modelName := arrowSymbolModelName(symbol)
	if modelName == "" {
		return
	}
	fields, ok := c.modelFields(modelName)
	if !ok {
		return
	}
	candidates := make([]string, 0, len(fields))
	for _, field := range fields {
		candidates = append(candidates, field.Name)
		if field.Name == access.Field {
			return
		}
		if strings.HasSuffix(field.Name, "_id") && field.Name == access.Field+"_id" && fieldHasRef(field) {
			// FK relation shorthand: `item.product` is backed by the
			// `product_id ref(product)` column.
			return
		}
		if strings.HasSuffix(field.Name, "_id") && fieldHasRef(field) {
			candidates = append(candidates, strings.TrimSuffix(field.Name, "_id"))
		}
	}
	if model := c.modelNode(modelName); model != nil {
		for _, field := range model.ComputedFields {
			candidates = append(candidates, field.Name)
			if field.Name == access.Field {
				return
			}
		}
	}
	// Collections and paginated results intentionally expose these generated
	// properties in target code. Without cardinality metadata, accepting them
	// is safer than misdiagnosing a valid projection as a model-field typo.
	switch access.Field {
	case "count", "length", "items", "total", "page", "per_page":
		return
	}
	hint := fmt.Sprintf("Use a field declared on model %q", modelName)
	if suggestion := suggestName(access.Field, candidates); suggestion != "" {
		hint += fmt.Sprintf("; did you mean %q?", suggestion)
	}
	c.addError(access.Loc,
		fmt.Sprintf("model %q has no field %q", modelName, access.Field),
		hint,
	)
}

func fieldHasRef(field *ast.Field) bool {
	if field == nil {
		return false
	}
	for _, constraint := range field.Constraints {
		if constraint.Kind == "ref" {
			return true
		}
	}
	return false
}

func (c *Checker) checkQueryExpr(expr ast.Expr, scope *Scope, fields map[string]bool) {
	if ident, ok := expr.(*ast.Ident); ok && fields[ident.Name] {
		return
	}
	switch item := expr.(type) {
	case *ast.BinaryExpr:
		c.checkQueryExpr(item.Left, scope, fields)
		c.checkQueryExpr(item.Right, scope, fields)
	case *ast.UnaryExpr:
		c.checkQueryExpr(item.Operand, scope, fields)
	default:
		c.checkLocalExpr(expr, scope)
	}
}

// When the model itself is unknown, its field set is unavailable. Preserve a
// bare identifier in predicate position as a possible field while still
// resolving value operands, so `query typo where(id == missing)` reports both
// the unknown model and the genuinely unbound value.
func (c *Checker) checkUnknownModelQueryExpr(expr ast.Expr, scope *Scope) {
	switch item := expr.(type) {
	case *ast.BinaryExpr:
		if item.Op == "and" || item.Op == "or" {
			c.checkUnknownModelQueryExpr(item.Left, scope)
			c.checkUnknownModelQueryExpr(item.Right, scope)
			return
		}
		if _, possibleField := item.Left.(*ast.Ident); !possibleField {
			c.checkLocalExpr(item.Left, scope)
		}
		c.checkLocalExpr(item.Right, scope)
	case *ast.UnaryExpr:
		if _, possibleField := item.Operand.(*ast.Ident); !possibleField {
			c.checkLocalExpr(item.Operand, scope)
		}
	case *ast.Ident:
		if arrowScopeHasRuntimeName(scope, item.Name) {
			c.checkLocalIdent(item, scope)
		}
	default:
		c.checkLocalExpr(expr, scope)
	}
}

// Stream filters use event payload fields on the left-hand side and locals on
// the right-hand side (for example, where(room_id == id)).
func (c *Checker) checkStreamConditionRefs(expr ast.Expr, scope *Scope) {
	if expr == nil {
		return
	}
	if binary, ok := expr.(*ast.BinaryExpr); ok {
		if binary.Op == "and" || binary.Op == "or" {
			c.checkStreamConditionRefs(binary.Left, scope)
			c.checkStreamConditionRefs(binary.Right, scope)
			return
		}
		if _, eventField := binary.Left.(*ast.Ident); !eventField {
			c.checkLocalExpr(binary.Left, scope)
		}
		c.checkLocalExpr(binary.Right, scope)
		return
	}
	c.checkLocalExpr(expr, scope)
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
		case SymFn:
			fn, ok := sym.Node.(*ast.Fn)
			if ok {
				c.checkCallableArity(call, sym, fn.Inputs)
			}
			return
		case SymPipe:
			pipe, ok := sym.Node.(*ast.Pipe)
			if ok {
				c.checkCallableArity(call, sym, pipeCallInputs(pipe))
			}
			return
		case SymModel:
			// Realtime targets such as `join room(id)` are represented by the
			// parser as a nested call whose name may also be a model. Their
			// symbolic target is validated by the enclosing builtin.
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

func (c *Checker) checkCallableArity(call *ast.FnCall, symbol *Symbol, inputs []*ast.InputStmt) {
	if call == nil || symbol == nil || len(call.Args) == len(inputs) {
		return
	}
	c.addArityError(call.Loc, symbol, len(inputs), len(call.Args))
}

func (c *Checker) addArityError(loc lexer.Loc, symbol *Symbol, expected, actual int) {
	if symbol == nil {
		return
	}
	kind := "function"
	callPrefix := ""
	var inputs []*ast.InputStmt
	switch node := symbol.Node.(type) {
	case *ast.Fn:
		inputs = node.Inputs
	case *ast.Pipe:
		kind = "pipe"
		callPrefix = "pipe "
		inputs = pipeCallInputs(node)
	default:
		return
	}
	argumentWord := "arguments"
	if expected == 1 {
		argumentWord = "argument"
	}
	params := make([]string, 0, len(inputs))
	for _, input := range inputs {
		params = append(params, input.Name)
	}
	signature := fmt.Sprintf("%s%s(%s)", callPrefix, symbol.Name, strings.Join(params, ", "))
	c.addError(loc,
		fmt.Sprintf("%s %q expects %d %s, got %d", kind, symbol.Name, expected, argumentWord, actual),
		fmt.Sprintf("Call it as %s; declared at %s", signature, symbol.Loc),
	)
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
	"with":             true,
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
