// Package diag is the shared diagnostic surface used by every Blueprint pass.
//
// Each pass keeps its own narrow error type (ParseError, CheckError, ...) and
// converts to *Diagnostic when it needs a human-readable rendering. This
// centralises the source-line + caret + Hint formatting, ANSI color decisions
// (TTY-aware, NO_COLOR honoured), and the structured-error-code namespace
// (P### parser, C### checker, R### resolver, L### linter, G### codegen).
package diag

import (
	"fmt"
	"os"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// Severity controls the label and color used when rendering.
type Severity int

const (
	// SeverityError renders as a red "error" label and signals exit non-zero.
	SeverityError Severity = iota
	// SeverityWarning renders as a yellow "warning" label and does not fail.
	SeverityWarning
)

// Diagnostic is a single problem reported by a pass. Code is optional; when
// present it appears in brackets next to the label (e.g. `error[C001]:`).
type Diagnostic struct {
	Severity Severity
	Code     string
	Loc      lexer.Loc
	Message  string
	Hint     string
}

// Format renders the diagnostic for display, with source-line context and a
// caret pointing at Loc.Col. Color is on when stderr is a TTY and NO_COLOR
// is unset. Use FormatPlain for golden tests and pipes.
func Format(d *Diagnostic, src []byte) string {
	return format(d, src, useColor())
}

// FormatPlain renders the diagnostic without any ANSI escapes.
func FormatPlain(d *Diagnostic, src []byte) string {
	return format(d, src, false)
}

func format(d *Diagnostic, src []byte, color bool) string {
	var b strings.Builder

	cyan, yellow, bold, reset := "", "", "", ""
	if color {
		cyan, yellow, bold, reset = "\x1b[36m", "\x1b[33m", "\x1b[1m", "\x1b[0m"
	}

	label, labelColor := "error", ifColor(color, "\x1b[31m")
	if d.Severity == SeverityWarning {
		label, labelColor = "warning", ifColor(color, "\x1b[33m")
	}

	if d.Code != "" {
		fmt.Fprintf(&b, "%s%s%s[%s%s%s]: %s%s%s\n\n",
			labelColor, label, reset, bold, d.Code, reset, cyan, d.Loc, reset)
	} else {
		fmt.Fprintf(&b, "%s%s%s: %s%s%s\n\n",
			labelColor, label, reset, cyan, d.Loc, reset)
	}

	if line := getSourceLine(src, d.Loc.Line); line != "" {
		fmt.Fprintf(&b, "  %s\n", line)
		if d.Loc.Col > 0 {
			fmt.Fprintf(&b, "%s\n", strings.Repeat(" ", d.Loc.Col-1+2)+"^")
		}
	}

	fmt.Fprintf(&b, "\n  %s\n", d.Message)
	if d.Hint != "" {
		fmt.Fprintf(&b, "  %sHint: %s%s\n", yellow, d.Hint, reset)
	}
	return b.String()
}

func ifColor(on bool, s string) string {
	if on {
		return s
	}
	return ""
}

// useColor decides whether to emit ANSI escapes. Diagnostics typically go to
// stderr, so we probe stderr.
func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func getSourceLine(src []byte, lineNum int) string {
	lines := strings.Split(string(src), "\n")
	if lineNum >= 1 && lineNum <= len(lines) {
		return lines[lineNum-1]
	}
	return ""
}
