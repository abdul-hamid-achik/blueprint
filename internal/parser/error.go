package parser

import (
	"fmt"

	"github.com/abdul-hamid-achik/blueprint/internal/diag"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// ParseError represents a syntax error found during parsing.
//
// Code is an optional structured error code (e.g. "P001") that, once populated
// across error sites, lets `bp explain <code>` link to documentation.
type ParseError struct {
	Loc     lexer.Loc
	Message string
	Hint    string
	Code    string
}

func (e ParseError) Error() string {
	s := fmt.Sprintf("%s: %s", e.Loc, e.Message)
	if e.Hint != "" {
		s += "\n  Hint: " + e.Hint
	}
	return s
}

// FormatError renders a ParseError using the shared diagnostic formatter
// (source-line context, caret, hint, optional code).
func FormatError(err ParseError, src []byte) string {
	return diag.Format(&diag.Diagnostic{
		Severity: diag.SeverityError,
		Code:     err.Code,
		Loc:      err.Loc,
		Message:  err.Message,
		Hint:     err.Hint,
	}, src)
}
