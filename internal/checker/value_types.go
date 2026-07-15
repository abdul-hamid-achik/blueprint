package checker

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// valueKind is the small, checker-local type vocabulary used for checks that
// are provably sound from the current AST. It is intentionally not a complete
// language type system: JSON, structurally declared types, and unsupported
// expressions remain unknown and therefore do not produce speculative errors.
type valueKind uint8

const (
	valueUnknown valueKind = iota
	valueNull
	valueString
	valueInt
	valueFloat
	valueMoney
	valueBool
	valueUUID
	valueTimestamp
	valueFile
	valueEnum
	valueModel
	valueList
	valueMap
)

type valueType struct {
	kind     valueKind
	name     string
	optional bool
	element  *valueType
	key      *valueType
	value    *valueType
	variants []string
}

func (t valueType) known() bool { return t.kind != valueUnknown }

func (t valueType) String() string {
	var base string
	switch t.kind {
	case valueNull:
		base = "null"
	case valueString:
		base = "string"
	case valueInt:
		base = "int"
	case valueFloat:
		base = "float"
	case valueMoney:
		base = "money"
	case valueBool:
		base = "bool"
	case valueUUID:
		base = "uuid"
	case valueTimestamp:
		base = "timestamp"
	case valueFile:
		base = "file"
	case valueEnum:
		if t.name != "" {
			base = "enum " + t.name
		} else {
			base = "enum(" + strings.Join(t.variants, ", ") + ")"
		}
	case valueModel:
		base = "model " + t.name
	case valueList:
		base = "list(unknown)"
		if t.element != nil {
			base = "list(" + t.element.String() + ")"
		}
	case valueMap:
		key, value := "unknown", "unknown"
		if t.key != nil {
			key = t.key.String()
		}
		if t.value != nil {
			value = t.value.String()
		}
		base = fmt.Sprintf("map(%s, %s)", key, value)
	default:
		base = "unknown"
	}
	if t.optional && t.kind != valueUnknown && t.kind != valueNull {
		return "optional " + base
	}
	return base
}

func copyValueType(t valueType) *valueType {
	copy := t
	return &copy
}

func hasOptionalConstraint(constraints []*ast.Constraint_) bool {
	for _, constraint := range constraints {
		if constraint.Kind == "optional" {
			return true
		}
	}
	return false
}

func (c *Checker) declaredValueType(typ ast.TypeExpr, constraints []*ast.Constraint_) valueType {
	result := c.valueTypeFromTypeExpr(typ, make(map[string]bool))
	if result.known() && hasOptionalConstraint(constraints) {
		result.optional = true
	}
	return result
}

func (c *Checker) valueTypeFromTypeExpr(typ ast.TypeExpr, visiting map[string]bool) valueType {
	switch value := typ.(type) {
	case *ast.PrimitiveType:
		switch value.Name {
		case "string", "text", "secret":
			return valueType{kind: valueString}
		case "int":
			return valueType{kind: valueInt}
		case "float":
			return valueType{kind: valueFloat}
		case "money":
			return valueType{kind: valueMoney}
		case "bool":
			return valueType{kind: valueBool}
		case "uuid":
			return valueType{kind: valueUUID}
		case "timestamp":
			return valueType{kind: valueTimestamp}
		case "file":
			return valueType{kind: valueFile}
		default:
			// json and future primitives are deliberately conservative.
			return valueType{}
		}
	case *ast.TypedJSONType:
		return valueType{}
	case *ast.TranslationKeyType:
		return valueType{kind: valueString}
	case *ast.MimeTypeExpr:
		return valueType{kind: valueFile}
	case *ast.EnumInline:
		return valueType{kind: valueEnum, variants: append([]string(nil), value.Variants...)}
	case *ast.ListType:
		element := c.valueTypeFromTypeExpr(value.Element, visiting)
		return valueType{kind: valueList, element: copyValueType(element)}
	case *ast.MapType:
		key := c.valueTypeFromTypeExpr(value.Key, visiting)
		item := c.valueTypeFromTypeExpr(value.Value, visiting)
		return valueType{kind: valueMap, key: copyValueType(key), value: copyValueType(item)}
	case *ast.NamedType:
		if visiting[value.Name] {
			return valueType{}
		}
		symbol := c.global.Lookup(value.Name)
		if symbol == nil {
			return valueType{}
		}
		switch node := symbol.Node.(type) {
		case *ast.Model, *ast.Content:
			return valueType{kind: valueModel, name: value.Name}
		case *ast.Enum:
			variants := make([]string, 0, len(node.Variants))
			for _, variant := range node.Variants {
				variants = append(variants, variant.Name)
			}
			return valueType{kind: valueEnum, name: value.Name, variants: variants}
		case *ast.Alias:
			visiting[value.Name] = true
			result := c.valueTypeFromTypeExpr(node.Type, visiting)
			delete(visiting, value.Name)
			if result.known() && hasOptionalConstraint(node.Constraints) {
				result.optional = true
			}
			return result
		default:
			return valueType{}
		}
	default:
		return valueType{}
	}
}

func valueTypesAssignable(expected, actual valueType, expr ast.Expr) bool {
	if !expected.known() || !actual.known() {
		return true
	}
	if actual.kind == valueNull {
		return expected.optional
	}
	if actual.optional && !expected.optional {
		return false
	}
	expected.optional = false
	actual.optional = false
	if expected.kind == valueEnum {
		if actual.kind == valueEnum {
			return expected.name != "" && expected.name == actual.name ||
				expected.name == "" && actual.name == "" && strings.Join(expected.variants, "\x00") == strings.Join(actual.variants, "\x00")
		}
		if literal, ok := expr.(*ast.StringLit); ok {
			for _, variant := range expected.variants {
				if literal.Value == variant {
					return true
				}
			}
		}
		return false
	}
	if expected.kind == valueString && (actual.kind == valueUUID || actual.kind == valueEnum) {
		return true
	}
	if expected.kind == valueUUID && actual.kind == valueString {
		// UUID values cross the source/runtime boundary as strings. Runtime
		// validation remains responsible for checking the literal's shape.
		return true
	}
	if expected.kind == valueTimestamp && actual.kind == valueString {
		// Timestamps, like UUIDs, enter generated services as validated
		// strings. Their format remains a runtime constraint.
		return true
	}
	if expected.kind == valueFloat && actual.kind == valueInt {
		return true
	}
	if expected.kind == valueMoney && (actual.kind == valueInt || actual.kind == valueFloat) {
		return true
	}
	if expected.kind != actual.kind {
		return false
	}
	switch expected.kind {
	case valueModel:
		return expected.name == actual.name
	case valueList:
		if expected.element == nil || actual.element == nil {
			return true
		}
		if literal, ok := expr.(*ast.ListExpr); ok {
			for _, element := range literal.Elements {
				if !valueTypesAssignable(*expected.element, *actual.element, element) {
					return false
				}
			}
			return true
		}
		return valueTypesAssignable(*expected.element, *actual.element, nil)
	case valueMap:
		if expected.key != nil && actual.key != nil && !valueTypesAssignable(*expected.key, *actual.key, nil) {
			return false
		}
		if expected.value != nil && actual.value != nil {
			if literal, ok := expr.(*ast.BlockExpr); ok {
				for _, entry := range literal.Entries {
					if !valueTypesAssignable(*expected.value, *actual.value, entry.Value) {
						return false
					}
				}
			} else if !valueTypesAssignable(*expected.value, *actual.value, nil) {
				return false
			}
		}
	}
	return true
}

func (c *Checker) inferValueType(expr ast.Expr, scope *Scope) valueType {
	if expr == nil {
		return valueType{}
	}
	switch value := expr.(type) {
	case *ast.StringLit, *ast.PathExpr:
		return valueType{kind: valueString}
	case *ast.IntLit:
		return valueType{kind: valueInt}
	case *ast.FloatLit:
		return valueType{kind: valueFloat}
	case *ast.BoolLit:
		return valueType{kind: valueBool}
	case *ast.NullLit:
		return valueType{kind: valueNull}
	case *ast.NowLit:
		return valueType{kind: valueTimestamp}
	case *ast.Ident:
		if scope != nil {
			if symbol := scope.Lookup(value.Name); symbol != nil {
				return symbol.value
			}
		}
		return valueType{}
	case *ast.ParenExpr:
		return c.inferValueType(value.Expr, scope)
	case *ast.UnaryExpr:
		if value.Op == "not" {
			return valueType{kind: valueBool}
		}
		return valueType{}
	case *ast.BinaryExpr:
		switch value.Op {
		case "==", "!=", "<", ">", "<=", ">=", "in", "and", "or":
			return valueType{kind: valueBool}
		default:
			return valueType{}
		}
	case *ast.ListExpr:
		if len(value.Elements) == 0 {
			unknown := valueType{}
			return valueType{kind: valueList, element: &unknown}
		}
		element := c.inferValueType(value.Elements[0], scope)
		for _, item := range value.Elements[1:] {
			candidate := c.inferValueType(item, scope)
			if !valueTypesAssignable(element, candidate, item) || !valueTypesAssignable(candidate, element, value.Elements[0]) {
				return valueType{}
			}
		}
		return valueType{kind: valueList, element: copyValueType(element)}
	case *ast.BlockExpr:
		key := valueType{kind: valueString}
		if len(value.Entries) == 0 {
			unknown := valueType{}
			return valueType{kind: valueMap, key: &key, value: &unknown}
		}
		item := c.inferValueType(value.Entries[0].Value, scope)
		for _, entry := range value.Entries[1:] {
			candidate := c.inferValueType(entry.Value, scope)
			if !valueTypesAssignable(item, candidate, entry.Value) || !valueTypesAssignable(candidate, item, value.Entries[0].Value) {
				return valueType{}
			}
		}
		return valueType{kind: valueMap, key: &key, value: copyValueType(item)}
	case *ast.IndexAccess:
		base := c.inferValueType(value.Base, scope)
		switch base.kind {
		case valueList:
			if base.element != nil {
				return *base.element
			}
		case valueMap:
			if base.value != nil {
				return *base.value
			}
		}
		return valueType{}
	case *ast.FieldAccess:
		return c.inferDirectFieldType(value, scope)
	case *ast.FnCall:
		return c.inferCallValueType(value, scope)
	default:
		return valueType{}
	}
}

func (c *Checker) inferDirectFieldType(access *ast.FieldAccess, scope *Scope) valueType {
	base, ok := access.Base.(*ast.Ident)
	if !ok {
		// Nested JSON and FK leaves require richer propagation and remain
		// deliberately unknown in this conservative slice.
		return valueType{}
	}
	if symbol := c.global.Lookup(base.Name); symbol != nil && symbol.Kind == SymEnum {
		decl, ok := symbol.Node.(*ast.Enum)
		if !ok {
			return valueType{}
		}
		for _, variant := range decl.Variants {
			if variant.Name == access.Field {
				return c.valueTypeFromTypeExpr(&ast.NamedType{Name: decl.Name}, make(map[string]bool))
			}
		}
		return valueType{}
	}
	if scope == nil {
		return valueType{}
	}
	baseType := c.inferValueType(base, scope)
	collection := false
	collectionOptional := false
	if baseType.kind == valueList && baseType.element != nil {
		collectionOptional = baseType.optional
		baseType = *baseType.element
		collection = true
	}
	if baseType.kind != valueModel {
		return valueType{}
	}
	fields, ok := c.modelFields(baseType.name)
	if !ok {
		return valueType{}
	}
	for _, field := range fields {
		if field.Name != access.Field {
			continue
		}
		fieldType := c.declaredValueType(field.Type, field.Constraints)
		// A field read through a nullable model is itself nullable until a
		// positive truthy guard narrows the model binding.
		fieldType.optional = fieldType.optional || baseType.optional
		if collection {
			return valueType{kind: valueList, optional: collectionOptional, element: copyValueType(fieldType)}
		}
		return fieldType
	}
	return valueType{}
}

func (c *Checker) inferCallValueType(call *ast.FnCall, scope *Scope) valueType {
	if call == nil {
		return valueType{}
	}
	if symbol := c.global.Lookup(call.Name); symbol != nil {
		switch node := symbol.Node.(type) {
		case *ast.Fn:
			return c.functionOutputValueType(node)
		case *ast.Pipe:
			// Pipes do not declare an output type. Inferring it from the input
			// would be unsound because a pipe may transform the value.
			return valueType{}
		}
	}
	modelName := c.callModelName(call, scope)
	if modelName != "" {
		model := valueType{kind: valueModel, name: modelName}
		switch call.Name {
		case "fetch":
			model.optional = true
			return model
		case "save", "seed", "update":
			return model
		case "query":
			for _, arg := range call.Args[1:] {
				if ident, ok := arg.(*ast.Ident); ok && ident.Name == "first" {
					model.optional = true
					return model
				}
				if marker, ok := arg.(*ast.FnCall); ok && marker.Name == "paginate" {
					return valueType{}
				}
			}
			return valueType{kind: valueList, element: &model}
		case "import_bundle", "export_bundle":
			return valueType{kind: valueList, element: &model}
		}
	}
	if call.Name == "count" {
		return valueType{kind: valueInt}
	}
	if call.Name == "map" && len(call.Args) > 1 {
		item := c.inferValueType(call.Args[1], scope)
		return valueType{kind: valueList, element: copyValueType(item)}
	}
	return valueType{}
}

func (c *Checker) callModelName(call *ast.FnCall, scope *Scope) string {
	if call == nil || len(call.Args) == 0 {
		return ""
	}
	ident, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return ""
	}
	if scope != nil {
		if symbol := scope.Lookup(ident.Name); symbol != nil {
			if modelName := arrowSymbolModelName(symbol); modelName != "" {
				return modelName
			}
		}
	}
	if symbol := c.global.Lookup(ident.Name); symbol != nil && symbol.Kind == SymModel {
		return ident.Name
	}
	return ""
}

func (c *Checker) functionOutputValueType(fn *ast.Fn) valueType {
	if fn == nil || len(fn.Outputs) != 1 || fn.Outputs[0].Value == nil {
		return valueType{}
	}
	ident, ok := fn.Outputs[0].Value.(*ast.Ident)
	if !ok {
		return valueType{}
	}
	if strings.Contains(ident.Name, "/") {
		return valueType{kind: valueFile}
	}
	if IsPrimitive(ident.Name) {
		return c.valueTypeFromTypeExpr(&ast.PrimitiveType{Name: ident.Name}, make(map[string]bool))
	}
	return c.valueTypeFromTypeExpr(&ast.NamedType{Name: ident.Name}, make(map[string]bool))
}

func (c *Checker) checkCallableArgumentTypes(call *ast.FnCall, scope *Scope) {
	if call == nil || isCheckerBuiltinFn(call.Name) {
		return
	}
	symbol := c.global.Lookup(call.Name)
	if symbol == nil {
		return
	}
	var inputs []*ast.InputStmt
	callable := ""
	switch node := symbol.Node.(type) {
	case *ast.Fn:
		inputs = node.Inputs
		callable = "function"
	case *ast.Pipe:
		inputs = pipeCallInputs(node)
		callable = "pipe"
	default:
		return
	}
	// Arity has its own diagnostic. Avoid adding type noise to an already
	// malformed call whose argument-to-parameter mapping is incomplete.
	if len(call.Args) != len(inputs) {
		return
	}
	for i, input := range inputs {
		if input == nil || input.Type == nil {
			continue
		}
		expected := c.declaredValueType(input.Type, input.Constraints)
		actual := c.inferValueType(call.Args[i], scope)
		if c.exprAssignableTo(expected, call.Args[i], scope) {
			continue
		}
		c.addError(call.Args[i].Location(),
			fmt.Sprintf("argument %d to %s %q expects %s, got %s", i+1, callable, call.Name, expected, describeActualType(actual, call.Args[i])),
			fmt.Sprintf("Parameter %q is declared at %s", input.Name, input.Loc),
		)
	}
}

func (c *Checker) addAssignmentTypeError(loc lexer.Loc, name string, input *Symbol, expected, actual valueType, expr ast.Expr) {
	hint := fmt.Sprintf("Input %q requires %s", name, expected)
	if input != nil && input.Loc.Line > 0 {
		hint = fmt.Sprintf("Input %q is declared as %s at %s", name, expected, input.Loc)
	}
	c.addError(loc,
		fmt.Sprintf("cannot assign %s to input %q of type %s", describeActualType(actual, expr), name, expected),
		hint,
	)
}

func (c *Checker) checkModelWriteValueType(operation, modelName string, entry ast.KVPair, scope *Scope) {
	fields, ok := c.modelFields(modelName)
	if !ok {
		return
	}
	field := modelFieldNamed(fields, entry.Key)
	if field == nil {
		return
	}
	expected := c.declaredValueType(field.Type, field.Constraints)
	actual := c.inferValueType(entry.Value, scope)
	if c.exprAssignableTo(expected, entry.Value, scope) {
		return
	}
	c.addError(entry.Loc,
		fmt.Sprintf("%s field %q on model %q expects %s, got %s", operation, entry.Key, modelName, expected, describeActualType(actual, entry.Value)),
		fmt.Sprintf("Field %q is declared at %s", entry.Key, field.Loc),
	)
}

// exprAssignableTo uses a known declared composite type to validate literal
// members independently. A heterogeneous literal deliberately infers as
// unknown in isolation, but that must not let a clearly incompatible member
// bypass an assignment or call contract.
func (c *Checker) exprAssignableTo(expected valueType, expr ast.Expr, scope *Scope) bool {
	if !expected.known() || expr == nil {
		return true
	}
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.Expr
	}
	if literal, ok := expr.(*ast.ListExpr); ok {
		if expected.kind != valueList {
			unknown := valueType{}
			return valueTypesAssignable(expected, valueType{kind: valueList, element: &unknown}, literal)
		}
		if expected.element == nil {
			return true
		}
		for _, element := range literal.Elements {
			if !c.exprAssignableTo(*expected.element, element, scope) {
				return false
			}
		}
		return true
	}
	if literal, ok := expr.(*ast.BlockExpr); ok {
		if expected.kind != valueMap {
			key, unknown := valueType{kind: valueString}, valueType{}
			return valueTypesAssignable(expected, valueType{kind: valueMap, key: &key, value: &unknown}, literal)
		}
		for _, entry := range literal.Entries {
			if expected.key != nil {
				key := &ast.StringLit{Loc: entry.Loc, Value: entry.Key}
				if !c.exprAssignableTo(*expected.key, key, scope) {
					return false
				}
			}
			if expected.value != nil && !c.exprAssignableTo(*expected.value, entry.Value, scope) {
				return false
			}
		}
		return true
	}
	actual := c.inferValueType(expr, scope)
	return valueTypesAssignable(expected, actual, expr)
}

func (c *Checker) checkInlineWhenInputAssignment(expr ast.Expr, scope *Scope) {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.Expr
	}
	assignment, ok := expr.(*ast.BinaryExpr)
	if !ok || assignment.Op != "=" {
		return
	}
	left, ok := assignment.Left.(*ast.Ident)
	if !ok || scope == nil {
		// Field assignments are property mutations, not typed input
		// reassignments, and retain their existing behavior.
		return
	}
	input := scope.Lookup(left.Name)
	if input == nil || input.Kind != SymInput {
		return
	}
	expected := input.declared
	actual := c.inferValueType(assignment.Right, scope)
	if c.exprAssignableTo(expected, assignment.Right, scope) {
		return
	}
	c.addAssignmentTypeError(assignment.Loc, left.Name, input, expected, actual, assignment.Right)
}

func describeActualType(actual valueType, expr ast.Expr) string {
	if literal, ok := expr.(*ast.StringLit); ok {
		return fmt.Sprintf("string %q", literal.Value)
	}
	return actual.String()
}

func (c *Checker) narrowTruthyValue(scope *Scope, expr ast.Expr) {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.Expr
	}
	if scope == nil {
		return
	}
	if _, negated := expr.(*ast.UnaryExpr); negated {
		// `guard not existing` proves the opposite of non-null presence.
		return
	}
	if ident, ok := expr.(*ast.Ident); ok {
		c.narrowOptionalSymbol(scope, ident.Name)
		return
	}
	// Existing Blueprint programs commonly guard a field predicate immediately
	// after fetch. Reading that field requires the model base to exist, so retain
	// that established narrowing without treating arbitrary identifiers inside
	// comparisons as non-null.
	c.narrowDirectFieldBases(scope, expr)
}

func (c *Checker) narrowOptionalSymbol(scope *Scope, name string) {
	symbol := scope.Lookup(name)
	if symbol != nil && symbol.value.optional {
		symbol.value.optional = false
	}
}

func (c *Checker) narrowDirectFieldBases(scope *Scope, expr ast.Expr) {
	if expr == nil {
		return
	}
	switch value := expr.(type) {
	case *ast.FieldAccess:
		if base, ok := value.Base.(*ast.Ident); ok {
			c.narrowOptionalSymbol(scope, base.Name)
		}
		c.narrowDirectFieldBases(scope, value.Base)
	case *ast.BinaryExpr:
		c.narrowDirectFieldBases(scope, value.Left)
		c.narrowDirectFieldBases(scope, value.Right)
	case *ast.ParenExpr:
		c.narrowDirectFieldBases(scope, value.Expr)
	case *ast.FnCall:
		for _, arg := range value.Args {
			c.narrowDirectFieldBases(scope, arg)
		}
	case *ast.IndexAccess:
		c.narrowDirectFieldBases(scope, value.Base)
		c.narrowDirectFieldBases(scope, value.Index)
	case *ast.ListExpr:
		for _, element := range value.Elements {
			c.narrowDirectFieldBases(scope, element)
		}
	case *ast.BlockExpr:
		for _, entry := range value.Entries {
			c.narrowDirectFieldBases(scope, entry.Value)
		}
	}
}
