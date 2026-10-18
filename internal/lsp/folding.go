package lsp

import (
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// lspFoldingRange is the LSP FoldingRange shape (line-only form).
type lspFoldingRange struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine"`
}

// handleFoldingRange handles textDocument/foldingRange.
func (s *Server) handleFoldingRange(msg *jsonRPCMessage) error {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := jsonUnmarshalParams(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}

	ranges := []lspFoldingRange{}
	if doc, ok := s.docs[params.TextDocument.URI]; ok {
		tokens, _ := lexer.Tokenize(uriToFilename(params.TextDocument.URI), []byte(doc.Text))
		ranges = foldingRanges(tokens)
	}
	return s.sendResult(msg.ID, ranges)
}

// foldingRanges returns one range per brace block that spans several lines.
// Braces are matched on the token stream, so braces inside strings, intents,
// and comments are ignored. The range stops one line before the closing brace
// so that the `}` stays visible when the block is collapsed; a try block
// therefore folds up to its `} recover {` line, and the recover block folds
// from that same line.
func foldingRanges(tokens []lexer.Token) []lspFoldingRange {
	match := matchBraces(tokens)
	ranges := make([]lspFoldingRange, 0, len(match))
	for open, close := range match {
		startLine := tokens[open].Loc.Line - 1
		endLine := tokens[close].Loc.Line - 2
		if endLine > startLine {
			ranges = append(ranges, lspFoldingRange{StartLine: startLine, EndLine: endLine})
		}
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].StartLine != ranges[j].StartLine {
			return ranges[i].StartLine < ranges[j].StartLine
		}
		return ranges[i].EndLine < ranges[j].EndLine
	})
	return ranges
}

// matchBraces maps the index of each `{` token to the index of its matching
// `}`. Unbalanced braces are left out of the map.
func matchBraces(tokens []lexer.Token) map[int]int {
	match := make(map[int]int)
	var open []int
	for i, tok := range tokens {
		switch tok.Kind {
		case lexer.TokenLBrace:
			open = append(open, i)
		case lexer.TokenRBrace:
			if n := len(open); n > 0 {
				match[open[n-1]] = i
				open = open[:n-1]
			}
		}
	}
	return match
}

// lineIndex converts byte offsets into LSP positions (UTF-16 characters).
type lineIndex struct {
	text   string
	starts []int
}

func newLineIndex(text string) lineIndex {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return lineIndex{text: text, starts: starts}
}

func (l lineIndex) position(offset int) lspPosition {
	if offset < 0 {
		offset = 0
	}
	if offset > len(l.text) {
		offset = len(l.text)
	}
	line := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > offset }) - 1
	return lspPosition{Line: line, Character: utf16Length(l.text[l.starts[line]:offset])}
}

// lineRestAt returns the text from offset to the end of its line, without a
// trailing carriage return.
func (l lineIndex) lineRestAt(offset int) string {
	rest := l.text[offset:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSuffix(rest, "\r")
}

// tokenIndexAt returns the index of the first token starting at or after
// offset, clamped to the last token.
func tokenIndexAt(tokens []lexer.Token, offset int) int {
	i := sort.Search(len(tokens), func(i int) bool { return tokens[i].Loc.Offset >= offset })
	if i >= len(tokens) {
		i = len(tokens) - 1
	}
	return i
}
