package checker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// supportedFormats are the string format kinds documented in the language
// reference. Codegen currently lowers only email and url; the others are
// accepted by the language.
var supportedFormats = map[string]bool{"email": true, "url": true, "uuid": true, "ip": true, "date": true}

var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// checkConstraintValues rejects constraint values that cannot apply to the
// declared type (C052). Only value kinds with well-defined constraint
// semantics are checked; json, unresolved names, models, maps, lists of
// defaults, and files are left alone so the checker never guesses.
func (c *Checker) checkConstraintValues(typ ast.TypeExpr, constraints []*ast.Constraint_) {
	if typ == nil || len(constraints) == 0 {
		return
	}
	t := c.valueTypeFromTypeExpr(typ, make(map[string]bool))
	if !t.known() {
		return
	}
	var lo, hi *ast.Constraint_
	for _, con := range constraints {
		if con.Value == nil {
			continue
		}
		switch con.Kind {
		case "default":
			c.checkDefaultValue(t, con)
		case "min":
			if lo == nil {
				lo = con
			}
			c.checkBoundValue(t, con)
		case "max":
			if hi == nil {
				hi = con
			}
			c.checkBoundValue(t, con)
		case "format":
			c.checkFormatValue(t, con)
		}
	}
	if lo != nil && hi != nil {
		c.checkBoundOrder(t, lo, hi)
	}
}

func (c *Checker) checkDefaultValue(t valueType, con *ast.Constraint_) {
	switch t.kind {
	case valueList, valueMap, valueModel, valueFile, valueNull:
		return
	}
	value := con.Value
	inner, _ := unwrapNegation(value)
	ok := false
	switch t.kind {
	case valueInt:
		_, ok = inner.(*ast.IntLit)
	case valueFloat, valueMoney:
		switch inner.(type) {
		case *ast.IntLit, *ast.FloatLit:
			ok = true
		}
	case valueBool:
		_, ok = value.(*ast.BoolLit)
	case valueString:
		switch value.(type) {
		case *ast.StringLit, *ast.Ident:
			ok = true
		}
	case valueUUID:
		if lit, isStr := value.(*ast.StringLit); isStr {
			ok = uuidShape.MatchString(lit.Value)
		}
	case valueTimestamp:
		switch v := value.(type) {
		case *ast.NowLit:
			ok = true
		case *ast.StringLit:
			_, errFull := time.Parse(time.RFC3339Nano, v.Value)
			_, errDate := time.Parse("2006-01-02", v.Value)
			ok = errFull == nil || errDate == nil
		}
	case valueEnum:
		name := ""
		switch v := value.(type) {
		case *ast.Ident:
			name = v.Name
		case *ast.StringLit:
			name = v.Value
		}
		for _, variant := range t.variants {
			if name != "" && name == variant {
				ok = true
			}
		}
	}
	if ok {
		return
	}
	hint := fmt.Sprintf("Use a %s value that fits the type", t.String())
	switch t.kind {
	case valueEnum:
		hint = fmt.Sprintf("Use one of the variants: %s", joinVariants(t.variants))
	case valueTimestamp:
		hint = "Use now or an ISO-8601 timestamp string such as \"2026-01-01T00:00:00Z\""
	case valueUUID:
		hint = "Use a quoted UUID such as \"123e4567-e89b-12d3-a456-426614174000\""
	case valueBool:
		hint = "Use true or false"
	case valueInt:
		hint = "Use a whole-number literal such as 0 or -1"
	case valueFloat, valueMoney:
		hint = "Use a numeric literal such as 0 or 0.5"
	case valueString:
		hint = "Use a quoted string such as \"value\" or a bare word such as center"
	}
	c.addErrorCode(con.Loc, CodeInvalidConstraintValue,
		fmt.Sprintf("default(%s) does not fit %s", constraintValueText(value), t.String()),
		hint,
	)
}

func (c *Checker) checkBoundValue(t valueType, con *ast.Constraint_) {
	name := con.Kind
	value := con.Value
	inner, _ := unwrapNegation(value)
	switch t.kind {
	case valueInt, valueFloat, valueMoney:
		switch inner.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.SizeLit, *ast.DurationLit:
			return
		}
		c.addErrorCode(con.Loc, CodeInvalidConstraintValue,
			fmt.Sprintf("%s(%s) must be a number for %s", name, constraintValueText(value), t.String()),
			"Use a numeric literal, or a size such as 100mb or a duration such as 5s where the type allows it",
		)
	case valueString, valueUUID, valueList:
		if _, isInt := value.(*ast.IntLit); isInt {
			return
		}
		c.addErrorCode(con.Loc, CodeInvalidConstraintValue,
			fmt.Sprintf("%s(%s) must be a whole, non-negative length for %s", name, constraintValueText(value), t.String()),
			"min/max on strings and lists bound their length; use a whole number such as max(255)",
		)
	case valueBool, valueEnum, valueTimestamp:
		c.addErrorCode(con.Loc, CodeInvalidConstraintValue,
			fmt.Sprintf("%s(%s) does not apply to %s", name, constraintValueText(value), t.String()),
			"min/max apply to numbers and to string or list lengths",
		)
	}
}

// checkBoundOrder reports min(n) greater than max(m) when both are plain
// numeric or length literals. Non-literal or size/duration bounds are skipped.
func (c *Checker) checkBoundOrder(t valueType, lo, hi *ast.Constraint_) {
	switch t.kind {
	case valueInt, valueFloat, valueMoney, valueString, valueUUID, valueList:
	default:
		return
	}
	loVal, loOK := numericBound(lo.Value)
	hiVal, hiOK := numericBound(hi.Value)
	if !loOK || !hiOK || loVal <= hiVal {
		return
	}
	c.addErrorCode(hi.Loc, CodeInvalidConstraintValue,
		fmt.Sprintf("max(%s) is smaller than min(%s) for %s", constraintValueText(hi.Value), constraintValueText(lo.Value), t.String()),
		"Make max greater than or equal to min",
	)
}

func (c *Checker) checkFormatValue(t valueType, con *ast.Constraint_) {
	kind := ""
	if ident, ok := con.Value.(*ast.Ident); ok {
		kind = ident.Name
	}
	if t.kind != valueString {
		switch t.kind {
		case valueList, valueMap, valueModel, valueFile, valueNull:
			return
		}
		c.addErrorCode(con.Loc, CodeInvalidConstraintValue,
			fmt.Sprintf("format(%s) does not apply to %s", kind, t.String()),
			"format applies to string fields only",
		)
		return
	}
	if !supportedFormats[kind] {
		c.addErrorCode(con.Loc, CodeInvalidConstraintValue,
			fmt.Sprintf("format(%s) is not a supported format", kind),
			"Use one of: email, url, uuid, ip, date",
		)
	}
}

// unwrapNegation strips a unary minus so -1 is judged as the literal 1.
func unwrapNegation(e ast.Expr) (ast.Expr, bool) {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == "-" {
		return u.Operand, true
	}
	return e, false
}

// numericBound returns the value of a plain int or float literal bound.
func numericBound(e ast.Expr) (float64, bool) {
	inner, negated := unwrapNegation(e)
	var text string
	switch v := inner.(type) {
	case *ast.IntLit:
		text = v.Value
	case *ast.FloatLit:
		text = v.Value
	default:
		return 0, false
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	if negated {
		n = -n
	}
	return n, true
}

func constraintValueText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StringLit:
		return strconv.Quote(v.Value)
	case *ast.Ident:
		return v.Name
	case *ast.IntLit:
		return v.Value
	case *ast.FloatLit:
		return v.Value
	case *ast.BoolLit:
		if v.Value {
			return "true"
		}
		return "false"
	case *ast.NowLit:
		return "now"
	case *ast.SizeLit:
		return v.Value
	case *ast.DurationLit:
		return v.Value
	case *ast.UnaryExpr:
		return v.Op + constraintValueText(v.Operand)
	}
	return "expression"
}

func joinVariants(variants []string) string {
	return strings.Join(variants, ", ")
}
