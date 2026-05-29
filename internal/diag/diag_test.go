package diag_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/diag"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

func TestFormatPlainShowsCaret(t *testing.T) {
	src := []byte("line one\nline two has a bad token here\nline three\n")
	d := &diag.Diagnostic{
		Severity: diag.SeverityError,
		Loc:      lexer.Loc{File: "x.bp", Line: 2, Col: 15},
		Message:  "unexpected token",
		Hint:     "did you mean ':' ?",
	}
	out := diag.FormatPlain(d, src)

	if !strings.Contains(out, "error:") {
		t.Errorf("missing error label: %q", out)
	}
	if !strings.Contains(out, "line two has a bad token here") {
		t.Errorf("missing source-line context: %q", out)
	}
	// The caret should appear under col 15 — the line is indented two spaces
	// in the output, so the caret column = col-1+2 = 16.
	caretLine := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(l, "^") && strings.TrimRight(l, "^ ") == "" {
			caretLine = l
			break
		}
	}
	if caretLine == "" {
		t.Errorf("missing caret line: %q", out)
	} else if len(caretLine)-1 != 15-1+2 {
		t.Errorf("caret at column %d, want %d (line=%q)", len(caretLine)-1, 15-1+2, caretLine)
	}
	if !strings.Contains(out, "Hint: did you mean ':' ?") {
		t.Errorf("missing hint: %q", out)
	}
}

func TestFormatPlainShowsCode(t *testing.T) {
	d := &diag.Diagnostic{
		Severity: diag.SeverityError,
		Code:     "C001",
		Loc:      lexer.Loc{File: "x.bp", Line: 1, Col: 1},
		Message:  "missing blueprint block",
	}
	out := diag.FormatPlain(d, []byte("foo\n"))
	if !strings.Contains(out, "error[C001]:") {
		t.Errorf("expected error[C001] label, got: %q", out)
	}
}

func TestFormatPlainNoCodeOmitsBrackets(t *testing.T) {
	d := &diag.Diagnostic{
		Severity: diag.SeverityError,
		Loc:      lexer.Loc{File: "x.bp", Line: 1, Col: 1},
		Message:  "boom",
	}
	out := diag.FormatPlain(d, []byte("foo\n"))
	if strings.Contains(out, "[]:") {
		t.Errorf("unexpected empty brackets in: %q", out)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "error:") {
		t.Errorf("expected prefix 'error:', got: %q", out)
	}
}

func TestFormatPlainWarningLabel(t *testing.T) {
	d := &diag.Diagnostic{
		Severity: diag.SeverityWarning,
		Loc:      lexer.Loc{File: "x.bp", Line: 1, Col: 1},
		Message:  "deprecated keyword",
	}
	out := diag.FormatPlain(d, []byte("foo\n"))
	if !strings.HasPrefix(strings.TrimSpace(out), "warning:") {
		t.Errorf("expected warning label, got: %q", out)
	}
}

func TestFormatPlainHandlesMissingLine(t *testing.T) {
	// Loc points at line 999 which doesn't exist in src — should not panic
	// and should still render message/hint.
	d := &diag.Diagnostic{
		Severity: diag.SeverityError,
		Loc:      lexer.Loc{File: "x.bp", Line: 999, Col: 1},
		Message:  "something",
		Hint:     "fix it",
	}
	out := diag.FormatPlain(d, []byte("only one line\n"))
	if !strings.Contains(out, "something") || !strings.Contains(out, "Hint: fix it") {
		t.Errorf("expected message+hint even without source line: %q", out)
	}
}
