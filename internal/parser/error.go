package parser

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// ParseError represents a syntax error found during parsing.
type ParseError struct {
	Loc     lexer.Loc
	Message string
	Hint    string
}

func (e ParseError) Error() string {
	s := fmt.Sprintf("%s: %s", e.Loc, e.Message)
	if e.Hint != "" {
		s += "\n  Hint: " + e.Hint
	}
	return s
}

// FormatError formats a parse error with source context for display.
func FormatError(err ParseError, src []byte) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Error: %s\n\n", err.Loc)

	// Extract the source line
	line := getSourceLine(src, err.Loc.Line)
	if line != "" {
		fmt.Fprintf(&b, "  %s\n", line)
		// Build pointer
		if err.Loc.Col > 0 {
			pointer := strings.Repeat(" ", err.Loc.Col-1+2) + "^"
			fmt.Fprintf(&b, "%s\n", pointer)
		}
	}

	fmt.Fprintf(&b, "\n  %s\n", err.Message)
	if err.Hint != "" {
		fmt.Fprintf(&b, "  %s\n", err.Hint)
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
