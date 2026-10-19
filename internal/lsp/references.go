package lsp

import (
	"encoding/json"
	"strings"
)

// positionToOffset converts an LSP (line, UTF-16 character) position into a
// byte offset in text. It reports false when the line does not exist. Lines end
// at "\n"; a "\r" directly before it is the line terminator's first half, not
// content, so a character past the end of a CRLF line clamps to the offset
// before "\r" and an edit there never splits the pair.
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
	lineText = strings.TrimSuffix(lineText, "\r")
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

// computeReferences returns every occurrence of the symbol at (line, char) in
// the document. The result is never nil.
func computeReferences(uri string, doc *docIndex, line, char int, includeDecl bool) []lspLocation {
	return computeComponentReferences([]scopeFile{{uri: uri, doc: doc}}, line, char, includeDecl)
}

// computeComponentReferences returns every occurrence of the symbol at the
// cursor in files[0], across the include component. A top-level symbol is
// searched in every file of the component; an endpoint-local binding only in
// its own document. The result is never nil.
func computeComponentReferences(files []scopeFile, line, char int, includeDecl bool) []lspLocation {
	out := []lspLocation{}
	if len(files) == 0 || files[0].doc == nil {
		return out
	}
	idx := componentIndexes(files)
	occ, ok := cursorOccurrence(files[0].doc, idx[0], line, char)
	if !ok {
		return out
	}
	if strings.HasPrefix(occ.Key, "local:") {
		files, idx = files[:1], idx[:1]
	}
	for i, f := range files {
		for _, o := range idx[i].occurrencesOf(occ.Key) {
			if o.Decl && !includeDecl {
				continue
			}
			out = append(out, lspLocation{URI: f.uri, Range: occurrenceRange(f.doc.text, o)})
		}
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
	locs := computeComponentReferences(s.symbolScope(uri), params.Position.Line, params.Position.Character, params.Context.IncludeDeclaration)
	return s.sendResult(msg.ID, locs)
}
