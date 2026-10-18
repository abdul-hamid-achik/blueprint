package lsp

import (
	"fmt"
	"os"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// lspTextEdit is the LSP TextEdit shape.
type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// handleFormatting handles textDocument/formatting. The reply is either an
// empty list or one edit that replaces the whole document with the output of
// the same printer `bp fmt` uses. FormattingOptions are ignored: the printer
// has a single canonical style.
func (s *Server) handleFormatting(msg *jsonRPCMessage) error {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := jsonUnmarshalParams(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}

	edits := []lspTextEdit{}
	if doc, ok := s.docs[params.TextDocument.URI]; ok {
		filename := uriToFilename(params.TextDocument.URI)
		if formatted, changed := formatDocumentText(filename, doc.Text); changed {
			edits = append(edits, lspTextEdit{
				Range:   lspRange{Start: lspPosition{}, End: documentEndPosition(doc.Text)},
				NewText: formatted,
			})
		}
	}
	return s.sendResult(msg.ID, edits)
}

// formatDocumentText returns the canonical formatting of text and whether it
// differs from text. It refuses (changed=false) when the input has parse
// errors, and also when the printer output fails a safety check: it must
// re-parse cleanly and keep the same number of `#` comments. A refusal is
// logged to stderr so the editor never rewrites a file into something worse.
func formatDocumentText(filename, text string) (string, bool) {
	file, errs := parseForFormat(filename, []byte(text))
	if len(errs) > 0 {
		return "", false
	}
	formatted := ast.Print(file)
	if formatted == text {
		return "", false
	}
	if !formattedOutputIsSafe(filename, file, formatted) {
		return "", false
	}
	return formatted, true
}

// formattedOutputIsSafe reports whether formatted may replace the document
// that original was parsed from. It must re-parse without errors into the
// same syntax tree (the check `bp fmt` makes before writing), keep the same
// number of `#` comments and `@` intents, and be a fixed point of the
// printer. The last rule stops format-on-save from rewriting a file again on
// every save when the printer keeps changing its output.
func formattedOutputIsSafe(filename string, original *ast.File, formatted string) bool {
	reparsed, errs := parseForFormat(filename, []byte(formatted))
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "blueprint-lsp: formatting %s skipped: formatter output has %d parse error(s)\n", filename, len(errs))
		return false
	}
	if same, where := ast.SameStructure(original, reparsed); !same {
		fmt.Fprintf(os.Stderr, "blueprint-lsp: formatting %s skipped: formatter output changes the syntax tree (%s)\n", filename, where)
		return false
	}
	if len(reparsed.Comments) != len(original.Comments) {
		fmt.Fprintf(os.Stderr, "blueprint-lsp: formatting %s skipped: comment count changed from %d to %d\n", filename, len(original.Comments), len(reparsed.Comments))
		return false
	}
	if got, want := ast.IntentCount(reparsed), ast.IntentCount(original); got != want {
		fmt.Fprintf(os.Stderr, "blueprint-lsp: formatting %s skipped: intent count changed from %d to %d\n", filename, want, got)
		return false
	}
	if ast.Print(reparsed) != formatted {
		fmt.Fprintf(os.Stderr, "blueprint-lsp: formatting %s skipped: formatter output is not stable on a second pass\n", filename)
		return false
	}
	return true
}

// parseForFormat parses src the way `bp fmt` (cmdFmt in cmd/bp/main.go) does:
// a full-file parse, retried with the partial parser only when the sole
// complaint is the missing blueprint block of an include fragment. Keep the
// two in step if either changes.
func parseForFormat(filename string, src []byte) (*ast.File, []parser.ParseError) {
	file, errs := parser.ParseFile(filename, src)
	for _, e := range errs {
		if e.Code == parser.CodeMissingBlueprint {
			return parser.ParsePartialFile(filename, src)
		}
	}
	return file, errs
}

// documentEndPosition returns the LSP position just after the last character
// of text. Lines are split on "\n" only, so a CRLF document ends on the line
// after its final "\r\n" and the last line's UTF-16 length is exact whether or
// not the text ends with a newline.
func documentEndPosition(text string) lspPosition {
	lastNewline := strings.LastIndexByte(text, '\n')
	return lspPosition{
		Line:      strings.Count(text, "\n"),
		Character: utf16Length(text[lastNewline+1:]),
	}
}
