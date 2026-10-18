package lsp

import (
	"encoding/json"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// positionToOffset converts an LSP (line, UTF-16 character) position into a
// byte offset in text. It reports false when the line does not exist.
func positionToOffset(text string, line, char int) (int, bool) {
	if line < 0 {
		return 0, false
	}
	off := 0
	for l := 0; l < line; l++ {
		nl := strings.IndexByte(text[off:], '\n')
		if nl < 0 {
			return 0, false
		}
		off += nl + 1
	}
	lineText := text[off:]
	if nl := strings.IndexByte(lineText, '\n'); nl >= 0 {
		lineText = lineText[:nl]
	}
	return off + byteOffsetForUTF16(lineText, char), true
}

// offsetToPosition converts a byte offset in text into an LSP position.
func offsetToPosition(text string, off int) lspPosition {
	if off > len(text) {
		off = len(text)
	}
	before := text[:off]
	lineStart := strings.LastIndexByte(before, '\n') + 1
	return lspPosition{
		Line:      strings.Count(before, "\n"),
		Character: utf16Length(before[lineStart:]),
	}
}

// occurrenceRange returns the LSP range of an occurrence's name token.
func occurrenceRange(text string, o symOccurrence) lspRange {
	return lspRange{
		Start: offsetToPosition(text, o.Off),
		End:   offsetToPosition(text, o.Off+o.Len),
	}
}

// symbolDoc parses an open document for symbol analysis. It uses the strict
// parser, which accepts an intent before the blueprint block; the partial
// parser behind buildIndex does not, so it would drop most of such a file.
// Definition and diagnostics keep using buildIndex.
func (s *Server) symbolDoc(uri string) *docIndex {
	doc, ok := s.docs[uri]
	if !ok {
		return nil
	}
	file, errs := parser.ParseFile(uriToFilename(uri), []byte(doc.Text))
	return &docIndex{text: doc.Text, file: file, errors: errs}
}

// symbolAtCursor resolves the symbol under an LSP position using the document's
// occurrence index. It reports false when the position is not on a symbol the
// index tracks (keywords, literals, strings, fields, builtins, unknown names).
func symbolAtCursor(doc *docIndex, line, char int) (*symbolIndex, symOccurrence, bool) {
	if doc == nil {
		return nil, symOccurrence{}, false
	}
	off, ok := positionToOffset(doc.text, line, char)
	if !ok {
		return nil, symOccurrence{}, false
	}
	si := buildSymbolIndex(doc)
	occ, ok := si.at(off)
	return si, occ, ok
}

// computeReferences returns every occurrence of the symbol at (line, char) in
// the document. The result is never nil.
func computeReferences(uri string, doc *docIndex, line, char int, includeDecl bool) []lspLocation {
	out := []lspLocation{}
	si, occ, ok := symbolAtCursor(doc, line, char)
	if !ok {
		return out
	}
	for _, o := range si.occurrencesOf(occ.Key) {
		if o.Decl && !includeDecl {
			continue
		}
		out = append(out, lspLocation{URI: uri, Range: occurrenceRange(doc.text, o)})
	}
	return out
}

// handleReferences handles textDocument/references.
func (s *Server) handleReferences(msg *jsonRPCMessage) error {
	var params struct {
		TextDocumentPositionParams
		Context struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}
	uri := params.TextDocument.URI
	locs := computeReferences(uri, s.symbolDoc(uri), params.Position.Line, params.Position.Character, params.Context.IncludeDeclaration)
	return s.sendResult(msg.ID, locs)
}
