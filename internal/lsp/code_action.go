package lsp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// This file implements textDocument/codeAction for "did you mean" quick fixes.
//
// Checker and parser hints that carry `did you mean "X"?` are turned into a
// quickfix at diagnostic-publish time: the suggestion and the range of the
// misspelled identifier are stored in the diagnostic's `data` field. When a
// client echoes that diagnostic back in codeAction context, the stored data is
// re-validated against the current document text. Clients that drop `data`
// (or send stale ranges) are served by recomputing the diagnostics for the
// current text and matching on range and message.

// quickFixData is the payload carried in a diagnostic's `data` field.
type quickFixData struct {
	// Name is the misspelled identifier as it appears in the source.
	Name string `json:"name"`
	// Suggestion is the replacement text taken from the hint.
	Suggestion string `json:"suggestion"`
	// Range is the LSP range of the misspelled identifier.
	Range lspRange `json:"range"`
}

// didYouMeanRe extracts the suggestion from a hint such as
// `...; did you mean "require_auth"?`.
var didYouMeanRe = regexp.MustCompile(`did you mean "([^"]+)"\?`)

// quotedRe pulls the %q-quoted names out of a checker message.
var quotedRe = regexp.MustCompile(`"([^"]*)"`)

// fixContext lazily tokenizes one document so identifier ranges can be
// resolved from diagnostic locations without re-lexing per diagnostic.
type fixContext struct {
	uri       string
	text      string
	lines     []string
	tokens    []lexer.Token
	tokenized bool
}

func newFixContext(uri, text string) *fixContext {
	return &fixContext{uri: uri, text: text, lines: strings.Split(text, "\n")}
}

func (fc *fixContext) ensureTokens() {
	if fc.tokenized {
		return
	}
	fc.tokenized = true
	fc.tokens, _ = lexer.Tokenize(uriToFilename(fc.uri), []byte(fc.text))
}

// fixScanTokens bounds how many tokens past a diagnostic location are
// examined when looking for the misspelled identifier.
const fixScanTokens = 64

// tokenStart is the 0-based LSP position of a token's first byte column.
func tokenStart(tok lexer.Token) lspPosition {
	return lspPosition{Line: tok.Loc.Line - 1, Character: tok.Loc.Col - 1}
}

// quickFix builds the fix payload for a diagnostic at range r. ok is false when
// the hint has no suggestion or no identifier token at or after r matches the
// names quoted in the message.
func (fc *fixContext) quickFix(r lspRange, message, hint string) (quickFixData, bool) {
	m := didYouMeanRe.FindStringSubmatch(hint)
	if m == nil {
		return quickFixData{}, false
	}
	suggestion := m[1]
	fc.ensureTokens()

	// The misspelled name is one of the names quoted in the message. The
	// diagnostic location may be a keyword before it (`use`, `test_group`) or
	// the enclosing expression (`row` in `row.emial`), so scan forward from the
	// location for the first quoted-name token that is a plausible typo of the
	// suggestion. The scan is capped so a far-away token cannot be picked.
	quoted := make(map[string]bool)
	firstLine := strings.SplitN(message, "\n", 2)[0]
	for _, q := range quotedRe.FindAllStringSubmatch(firstLine, -1) {
		quoted[q[1]] = true
	}

	start := 0
	for start < len(fc.tokens) && positionLess(tokenStart(fc.tokens[start]), r.Start) {
		start++
	}
	found := -1
	for i := start; i < len(fc.tokens) && i < start+fixScanTokens; i++ {
		tok := fc.tokens[i]
		if tok.Kind == lexer.TokenString || tok.Value == "" || tok.Value == suggestion ||
			!quoted[tok.Value] || !isWordString(tok.Value) {
			continue
		}
		d := editDistance(strings.ToLower(tok.Value), strings.ToLower(suggestion))
		if d < len(tok.Value)/2+1 {
			found = i
			break
		}
	}
	if found < 0 {
		return quickFixData{}, false
	}

	tok := fc.tokens[found]
	line := ""
	if tok.Loc.Line-1 < len(fc.lines) {
		line = fc.lines[tok.Loc.Line-1]
	}
	byteCol := tok.Loc.Col - 1
	if byteCol > len(line) {
		return quickFixData{}, false
	}
	startChar := utf16Length(line[:byteCol])
	return quickFixData{
		Name:       tok.Value,
		Suggestion: suggestion,
		Range: lspRange{
			Start: lspPosition{Line: tok.Loc.Line - 1, Character: startChar},
			End:   lspPosition{Line: tok.Loc.Line - 1, Character: startChar + utf16Length(tok.Value)},
		},
	}, true
}

// decodeQuickFix parses a diagnostic's data payload. An absent or foreign
// payload yields ok=false.
func decodeQuickFix(raw json.RawMessage) (quickFixData, bool) {
	if len(raw) == 0 {
		return quickFixData{}, false
	}
	var fix quickFixData
	if err := json.Unmarshal(raw, &fix); err != nil {
		return quickFixData{}, false
	}
	if fix.Name == "" || fix.Suggestion == "" || fix.Name == fix.Suggestion {
		return quickFixData{}, false
	}
	return fix, true
}

// validQuickFix checks that the identifier range still holds the misspelled
// name in the current text, so a stale payload is never applied.
func validQuickFix(text string, fix quickFixData) bool {
	if fix.Range.Start.Line != fix.Range.End.Line {
		return false
	}
	lines := strings.Split(text, "\n")
	if fix.Range.Start.Line < 0 || fix.Range.Start.Line >= len(lines) {
		return false
	}
	line := lines[fix.Range.Start.Line]
	start := byteOffsetForUTF16(line, fix.Range.Start.Character)
	end := byteOffsetForUTF16(line, fix.Range.End.Character)
	if start > end {
		return false
	}
	return line[start:end] == fix.Name
}

// codeAction is the LSP CodeAction shape we return.
type codeAction struct {
	Title       string          `json:"title"`
	Kind        string          `json:"kind"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
	IsPreferred bool            `json:"isPreferred"`
	Edit        workspaceEdit   `json:"edit"`
}

type workspaceEdit struct {
	Changes map[string][]textEdit `json:"changes"`
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// handleCodeAction handles textDocument/codeAction.
func (s *Server) handleCodeAction(msg *jsonRPCMessage) error {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Range   lspRange `json:"range"`
		Context struct {
			Diagnostics []lspDiagnostic `json:"diagnostics"`
			Only        []string        `json:"only"`
		} `json:"context"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}

	actions := make([]codeAction, 0)
	doc, ok := s.docs[params.TextDocument.URI]
	if !ok || !quickfixRequested(params.Context.Only) {
		return s.sendResult(msg.ID, actions)
	}
	actions = quickFixActions(params.TextDocument.URI, doc.Text, params.Range, params.Context.Diagnostics)
	return s.sendResult(msg.ID, actions)
}

// quickfixRequested reports whether a context.only filter admits quickfix
// actions. An empty filter admits everything.
func quickfixRequested(only []string) bool {
	if len(only) == 0 {
		return true
	}
	for _, kind := range only {
		if strings.HasPrefix("quickfix", kind) {
			return true
		}
	}
	return false
}

// quickFixActions builds one quickfix per suggestion whose identifier range
// intersects requested. client holds the diagnostics the client sent in the
// request context; when it is empty, the current diagnostics overlapping
// requested are used instead.
func quickFixActions(uri, text string, requested lspRange, client []lspDiagnostic) []codeAction {
	var current []lspDiagnostic
	currentLoaded := false
	loadCurrent := func() []lspDiagnostic {
		if !currentLoaded {
			current = computeDiagnostics(uri, text)
			currentLoaded = true
		}
		return current
	}

	src := client
	if len(src) == 0 {
		for _, d := range loadCurrent() {
			if rangesIntersect(d.Range, requested) {
				src = append(src, d)
			}
		}
	}

	type candidate struct {
		fix  quickFixData
		diag lspDiagnostic
	}
	var found []candidate
	seen := make(map[string]bool)
	for _, d := range src {
		fix, ok := decodeQuickFix(d.Data)
		if !ok || !validQuickFix(text, fix) {
			// Data was dropped or is stale: match the same diagnostic in the
			// current text by range and message.
			fix, ok = matchCurrentFix(loadCurrent(), d)
		}
		if !ok || !rangesIntersect(fix.Range, requested) {
			continue
		}
		key := fmt.Sprintf("%d:%d:%d:%d:%s", fix.Range.Start.Line, fix.Range.Start.Character,
			fix.Range.End.Line, fix.Range.End.Character, fix.Suggestion)
		if seen[key] {
			continue
		}
		seen[key] = true
		found = append(found, candidate{fix: fix, diag: d})
	}

	sort.SliceStable(found, func(i, j int) bool {
		return positionLess(found[i].fix.Range.Start, found[j].fix.Range.Start)
	})

	actions := make([]codeAction, 0, len(found))
	for _, c := range found {
		actions = append(actions, codeAction{
			Title:       fmt.Sprintf("Change to %q", c.fix.Suggestion),
			Kind:        "quickfix",
			Diagnostics: []lspDiagnostic{c.diag},
			IsPreferred: true,
			Edit: workspaceEdit{Changes: map[string][]textEdit{
				uri: {{Range: c.fix.Range, NewText: c.fix.Suggestion}},
			}},
		})
	}
	return actions
}

// matchCurrentFix finds the current diagnostic identical to d (same range and
// message) and returns its validated fix payload.
func matchCurrentFix(current []lspDiagnostic, d lspDiagnostic) (quickFixData, bool) {
	for _, c := range current {
		if c.Range != d.Range || c.Message != d.Message {
			continue
		}
		fix, ok := decodeQuickFix(c.Data)
		if ok {
			return fix, true
		}
	}
	return quickFixData{}, false
}

func positionLess(a, b lspPosition) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Character < b.Character
}

// rangesIntersect reports whether two LSP ranges share at least one position.
// Ends are inclusive so an empty cursor range at the end of an identifier
// still matches it.
func rangesIntersect(a, b lspRange) bool {
	return !positionLess(a.End, b.Start) && !positionLess(b.End, a.Start)
}

// isWordString reports whether s is a plain identifier-like word.
func isWordString(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isWordByte(s[i]) {
			return false
		}
	}
	return s != ""
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
