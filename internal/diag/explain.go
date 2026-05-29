package diag

import (
	_ "embed"
	"strings"
)

// errorCodesMD is the embedded reference doc for `bp explain`. The same content
// lives at docs/error-codes.md for the VitePress site; a Go test verifies the
// two stay byte-identical so drift becomes a CI failure rather than a stale doc.
//
//go:embed error-codes.md
var errorCodesMD string

// Lookup returns the documentation section for the given error code (e.g.
// "C001"). Codes are matched case-insensitively. If the code isn't documented
// yet, Lookup returns ("", false) and callers should print a clear "no docs"
// message rather than silently succeeding.
//
// The returned string is the raw Markdown block from the matching `### <code>`
// heading up to (but not including) the next `### `, `## `, or `---`. Callers
// render it; this package does not pretty-print Markdown.
func Lookup(code string) (string, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", false
	}

	// Header lines look like: `### C001 — missing blueprint block`. Match by
	// prefix + a non-alphanumeric boundary so that "C1" doesn't match "C10".
	prefix := "### " + code
	lines := strings.Split(errorCodesMD, "\n")

	start := -1
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		// Reject e.g. "### C0010" when searching "C001": next byte must be a
		// boundary (space, hyphen, or end-of-line).
		if len(line) > len(prefix) {
			next := line[len(prefix)]
			if next != ' ' && next != '-' && next != '\t' {
				continue
			}
		}
		start = i
		break
	}
	if start < 0 {
		return "", false
	}

	end := len(lines)
	for j := start + 1; j < len(lines); j++ {
		l := lines[j]
		if strings.HasPrefix(l, "### ") || strings.HasPrefix(l, "## ") || strings.HasPrefix(l, "---") {
			end = j
			break
		}
	}
	return strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n"), true
}
