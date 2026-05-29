package lsp

import (
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// lspDiagnostic mirrors the LSP `Diagnostic` JSON shape we publish.
type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Code     string   `json:"code,omitempty"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

const (
	severityError = 1
	severityWarn  = 2
	severityInfo  = 3
	severityHint  = 4
)

// computeDiagnostics parses + checks the document text and returns a slice of
// LSP-shaped diagnostics. It never returns nil; an empty doc returns an empty
// slice so clients see "no problems" rather than a JSON null.
func computeDiagnostics(uri, text string) []lspDiagnostic {
	diags := make([]lspDiagnostic, 0)
	idx := buildIndex(uri, text)

	for _, e := range idx.errors {
		diags = append(diags, lspDiagnostic{
			Range:    locToRange(e.Loc),
			Severity: severityError,
			Code:     e.Code,
			Source:   "blueprint",
			Message:  composeMessage(e.Message, e.Hint),
		})
	}

	// Run the checker on whatever AST we got. The checker tolerates partial
	// files; if the parser produced nothing useful (no blueprint block etc.)
	// the checker will simply emit its own diagnostics that we surface alongside
	// the parse errors.
	if idx.file != nil {
		for _, e := range checker.Check(idx.file) {
			diags = append(diags, lspDiagnostic{
				Range:    locToRange(e.Loc),
				Severity: severityError,
				Code:     e.Code,
				Source:   "blueprint",
				Message:  composeMessage(e.Message, e.Hint),
			})
		}
	}
	return diags
}

// locToRange converts a 1-indexed lexer.Loc to a 0-indexed LSP range. When
// Len is unset (0) we fall back to a 1-character span so the editor still has
// something to highlight.
func locToRange(loc lexer.Loc) lspRange {
	line := loc.Line - 1
	col := loc.Col - 1
	if line < 0 {
		line = 0
	}
	if col < 0 {
		col = 0
	}
	length := loc.Len
	if length <= 0 {
		length = 1
	}
	return lspRange{
		Start: lspPosition{Line: line, Character: col},
		End:   lspPosition{Line: line, Character: col + length},
	}
}

func composeMessage(msg, hint string) string {
	if hint == "" {
		return msg
	}
	return msg + "\n\nHint: " + hint
}
