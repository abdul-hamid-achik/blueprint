package checker

import (
	"fmt"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// operandFamily groups the scalar value kinds that operators distinguish.
// Operands outside these families (models, lists, maps, files, json, unknown,
// and null) are never reported: the checker only fires when both sides are
// known with certainty to be incompatible.
type operandFamily uint8

const (
	familyNone operandFamily = iota
	familyNumeric
	familyText
	familyTimestamp
	familyBool
	familyEnum
)

// scalarOperand returns the operand's family after dropping optionality. A
// nullable value is still the same kind when it is not null, and null is
// compared with any type, so optionality never changes which operator applies.
func scalarOperand(t valueType) (valueType, operandFamily) {
	t.optional = false
	switch t.kind {
	case valueInt, valueFloat, valueMoney:
		return t, familyNumeric
	case valueString, valueUUID:
		return t, familyText
	case valueTimestamp:
		return t, familyTimestamp
	case valueBool:
		return t, familyBool
	case valueEnum:
		return t, familyEnum
	default:
		return t, familyNone
	}
}

// numericAgainstText reports a number paired with a string-like value. Text
// and timestamps are runtime strings, so mixing either with a number has no
// meaning in any target.
func numericAgainstText(lf, rf operandFamily) bool {
	return lf == familyNumeric && (rf == familyText || rf == familyTimestamp) ||
		rf == familyNumeric && (lf == familyText || lf == familyTimestamp)
}

// operatorMismatch reports whether a binary operator is certainly misapplied
// to two operands of known types. It is deliberately an explicit list of bad
// combinations: anything not listed is accepted, so an unmodelled type can
// never produce a diagnostic.
func operatorMismatch(op string, left, right valueType, leftExpr, rightExpr ast.Expr) bool {
	l, lf := scalarOperand(left)
	r, rf := scalarOperand(right)
	if lf == familyNone || rf == familyNone {
		return false
	}
	switch op {
	case "==", "!=":
		return equalityMismatch(l, r, lf, rf, leftExpr, rightExpr)
	case "<", ">", "<=", ">=":
		if lf == familyBool || rf == familyBool {
			return true
		}
		return numericAgainstText(lf, rf)
	case "+", "-", "*", "/":
		if lf == familyBool || rf == familyBool {
			return true
		}
		// Datetime and enum arithmetic is not part of the documented contract,
		// so those operands are left to the runtime rather than guessed at here.
		if lf == familyEnum || rf == familyEnum || lf == familyTimestamp || rf == familyTimestamp {
			return false
		}
		if numericAgainstText(lf, rf) {
			return true
		}
		// `+` concatenates two strings; every other arithmetic operator needs numbers.
		return op != "+" && (lf == familyText || rf == familyText)
	}
	return false
}

// equalityMismatch accepts any pair the assignability rules accept in either
// direction. That covers numeric widening, uuid and timestamp values that
// cross the boundary as strings, and enum values compared with strings.
// Two named enums are only compatible when they are the same declaration;
// inline enums are not compared by name, so they are never reported.
func equalityMismatch(l, r valueType, lf, rf operandFamily, leftExpr, rightExpr ast.Expr) bool {
	if lf == familyEnum && rf == familyEnum {
		return l.name != "" && r.name != "" && l.name != r.name
	}
	if valueTypesAssignable(l, r, rightExpr) || valueTypesAssignable(r, l, leftExpr) {
		return false
	}
	return true
}

// unaryOperatorMismatch reports unary minus applied to a value that cannot be
// negated. `not` is intentionally not checked: truthiness of optional models
// and fields is an established guard idiom.
func unaryOperatorMismatch(op string, operand valueType) bool {
	if op != "-" {
		return false
	}
	_, family := scalarOperand(operand)
	return family == familyBool || family == familyText
}

// checkBinaryOperatorTypes validates one binary expression. typeOf supplies
// the operand types in the caller's name-resolution context: arrow scopes for
// steps, guards, when conditions and outputs, and model fields for where(...).
func (c *Checker) checkBinaryOperatorTypes(expr *ast.BinaryExpr, typeOf func(ast.Expr) valueType) {
	if expr == nil {
		return
	}
	switch expr.Op {
	case "==", "!=", "<", ">", "<=", ">=", "+", "-", "*", "/":
	case "in":
		left := typeOf(expr.Left)
		right := typeOf(expr.Right)
		if right.kind != valueList || right.element == nil {
			return
		}
		// Membership is equality against each element of the list.
		if !operatorMismatch("==", left, *right.element, expr.Left, nil) {
			return
		}
		c.addOperatorError(expr.Loc,
			fmt.Sprintf("operator %q cannot test %s against a list of %s", expr.Op, describeActualType(left, expr.Left), right.element.String()),
			"Test a value against elements of the same type",
		)
		return
	default:
		// Assignment, and/or (truthiness idiom), and unknown operators are not checked here.
		return
	}
	left := typeOf(expr.Left)
	right := typeOf(expr.Right)
	if !operatorMismatch(expr.Op, left, right, expr.Left, expr.Right) {
		return
	}
	leftDesc := describeActualType(left, expr.Left)
	rightDesc := describeActualType(right, expr.Right)
	switch expr.Op {
	case "==", "!=", "<", ">", "<=", ">=":
		c.addOperatorError(expr.Loc,
			fmt.Sprintf("operator %q cannot compare %s with %s", expr.Op, leftDesc, rightDesc),
			"Compare values of the same kind: numbers with numbers, strings with strings, bools with bools",
		)
	default:
		c.addOperatorError(expr.Loc,
			fmt.Sprintf("operator %q cannot combine %s with %s", expr.Op, leftDesc, rightDesc),
			"Arithmetic needs numbers; `+` also joins two strings",
		)
	}
}

// checkUnaryOperatorTypes validates unary minus on a value that cannot be negated.
func (c *Checker) checkUnaryOperatorTypes(expr *ast.UnaryExpr, operand valueType) {
	if expr == nil || !unaryOperatorMismatch(expr.Op, operand) {
		return
	}
	c.addOperatorError(expr.Loc,
		fmt.Sprintf("operator %q cannot negate %s", expr.Op, describeActualType(operand, expr.Operand)),
		"Negation applies to int, float, and money values",
	)
}

// addOperatorError records a C050 diagnostic once per location and message,
// because a statement can be visited through more than one enclosing walk.
func (c *Checker) addOperatorError(loc lexer.Loc, msg, hint string) {
	for _, existing := range c.errors {
		if existing.Code == CodeOperatorTypeMismatch && existing.Loc == loc && existing.Message == msg {
			return
		}
	}
	c.addErrorCode(loc, CodeOperatorTypeMismatch, msg, hint)
}

// predicateOperandTypes types the operands of a where(...) predicate on a
// known model. A bare identifier naming a model field is that column, which
// checkQueryExpr also prefers over a same-named binding, so it takes the
// field's declared type instead of the scope's.
func (c *Checker) predicateOperandTypes(modelName string, scope *Scope, fields map[string]bool) func(ast.Expr) valueType {
	return func(expr ast.Expr) valueType {
		for {
			paren, ok := expr.(*ast.ParenExpr)
			if !ok {
				break
			}
			expr = paren.Expr
		}
		if ident, ok := expr.(*ast.Ident); ok && fields[ident.Name] {
			modelFields, _ := c.modelFields(modelName)
			if field := modelFieldNamed(modelFields, ident.Name); field != nil {
				return c.declaredValueType(field.Type, field.Constraints)
			}
			return valueType{}
		}
		return c.inferValueType(expr, scope)
	}
}
