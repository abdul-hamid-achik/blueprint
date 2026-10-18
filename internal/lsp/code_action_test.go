package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const caHeader = `blueprint "test" {
  version  "1.0"
  port     3000
  runtime  node
  database postgres
}
`

// quickFixCases covers every "did you mean" site in internal/checker that the
// LSP can reach from a document. Each source contains exactly one misspelling.
var quickFixCases = []struct {
	name       string
	src        string
	misspelled string
	suggestion string
}{
	{
		name: "unknown middleware",
		src: caHeader + `
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Missing auth"
  }
}
POST /api/test {
  use reqire_auth
  -> 200 "ok"
}
`,
		misspelled: "reqire_auth",
		suggestion: "require_auth",
	},
	{
		name: "unknown type",
		src: caHeader + `
enum Status {
  active
  inactive
}
model item {
  id     uuid    primary
  status Statsu  required
}
`,
		misspelled: "Statsu",
		suggestion: "Status",
	},
	{
		name: "unknown ref model",
		src: caHeader + `
model user {
  id uuid primary
}
model post {
  id      uuid primary
  user_id uuid ref(usr)
}
`,
		misspelled: "usr",
		suggestion: "user",
	},
	{
		name: "unknown function",
		src: caHeader + `
fn process_data {
  <- input string
  -> output string
  impl node { module: "./internal/process-data" }
}

POST /api/test {
  <- input string required
  |> result = proces_data(input)
  -> 200 result
}
`,
		misspelled: "proces_data",
		suggestion: "process_data",
	},
	{
		name: "unknown model field",
		src: caHeader + `
model user {
  id    uuid   primary
  email string required
}

GET /api/users/:id {
  <- id uuid required
  |> row = fetch user(id)
  -> 200 row.emial
}
`,
		misspelled: "emial",
		suggestion: "email",
	},
	{
		name: "unknown test in test_group",
		src: caHeader + `
model user {
  id uuid primary
}
test create_user {
  target POST /api/users
  expect {
    status 200
  }
}
test_group all_tests {
  tests [create_usr]
}
`,
		misspelled: "create_usr",
		suggestion: "create_user",
	},
}

// applyTextEdits applies single-line LSP edits to text, the way a client would.
func applyTextEdits(t *testing.T, text string, edits []textEdit) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	for _, e := range edits {
		if e.Range.Start.Line != e.Range.End.Line {
			t.Fatalf("multi-line edit not expected: %+v", e)
		}
		line := lines[e.Range.Start.Line]
		start := byteOffsetForUTF16(line, e.Range.Start.Character)
		end := byteOffsetForUTF16(line, e.Range.End.Character)
		lines[e.Range.Start.Line] = line[:start] + e.NewText + line[end:]
	}
	return strings.Join(lines, "\n")
}

// rangeText returns the source text covered by a single-line LSP range.
func rangeText(text string, r lspRange) string {
	line := strings.Split(text, "\n")[r.Start.Line]
	return line[byteOffsetForUTF16(line, r.Start.Character):byteOffsetForUTF16(line, r.End.Character)]
}

// fullDocRange covers the whole document, so every diagnostic intersects it.
func fullDocRange(text string) lspRange {
	lines := strings.Split(text, "\n")
	return lspRange{End: lspPosition{Line: len(lines) - 1, Character: len(lines[len(lines)-1])}}
}

func findDiagnostic(t *testing.T, diags []lspDiagnostic, msgPart string) lspDiagnostic {
	t.Helper()
	for _, d := range diags {
		if strings.Contains(d.Message, msgPart) {
			return d
		}
	}
	t.Fatalf("no diagnostic containing %q in %+v", msgPart, diags)
	return lspDiagnostic{}
}

func TestCodeAction_DidYouMeanSitesProduceCorrectEdit(t *testing.T) {
	for _, tc := range quickFixCases {
		t.Run(tc.name, func(t *testing.T) {
			const uri = "file:///tmp/ca.bp"
			diags := computeDiagnostics(uri, tc.src)
			d := findDiagnostic(t, diags, tc.misspelled)
			if len(d.Data) == 0 {
				t.Fatalf("diagnostic has no quick-fix data: %+v", d)
			}

			actions := quickFixActions(uri, tc.src, fullDocRange(tc.src), []lspDiagnostic{d})
			if len(actions) != 1 {
				t.Fatalf("got %d actions, want 1: %+v", len(actions), actions)
			}
			a := actions[0]
			if a.Kind != "quickfix" || !a.IsPreferred {
				t.Errorf("kind=%q preferred=%v, want quickfix/true", a.Kind, a.IsPreferred)
			}
			wantTitle := `Change to "` + tc.suggestion + `"`
			if a.Title != wantTitle {
				t.Errorf("title = %q, want %q", a.Title, wantTitle)
			}
			edits := a.Edit.Changes[uri]
			if len(edits) != 1 {
				t.Fatalf("edits = %+v, want exactly one", edits)
			}
			if got := rangeText(tc.src, edits[0].Range); got != tc.misspelled {
				t.Errorf("edit range covers %q, want %q", got, tc.misspelled)
			}
			if edits[0].NewText != tc.suggestion {
				t.Errorf("newText = %q, want %q", edits[0].NewText, tc.suggestion)
			}

			// Applying the edit must remove that diagnostic.
			fixed := applyTextEdits(t, tc.src, edits)
			for _, after := range computeDiagnostics(uri, fixed) {
				if strings.Contains(after.Message, `"`+tc.misspelled+`"`) {
					t.Errorf("diagnostic still present after fix: %q", after.Message)
				}
			}
		})
	}
}

func TestCodeAction_NoActionWithoutSuggestion(t *testing.T) {
	src := caHeader + `
POST /api/test {
  use zzzzqqqq
  -> 200 "ok"
}
`
	const uri = "file:///tmp/nosug.bp"
	diags := computeDiagnostics(uri, src)
	d := findDiagnostic(t, diags, "zzzzqqqq")
	if strings.Contains(d.Message, "did you mean") || len(d.Data) != 0 {
		t.Fatalf("expected no suggestion for far-off name, got %+v", d)
	}
	if actions := quickFixActions(uri, src, fullDocRange(src), []lspDiagnostic{d}); len(actions) != 0 {
		t.Errorf("got actions without a suggestion: %+v", actions)
	}
}

func TestCodeAction_NoActionWhenTokenAlreadyMatchesSuggestion(t *testing.T) {
	// A payload whose identifier already equals the suggestion must never be
	// offered as a change.
	src := caHeader + `
model user {
  id uuid primary
}
model post {
  id      uuid primary
  user_id uuid ref(user)
}
`
	const uri = "file:///tmp/same.bp"
	fix := quickFixData{Name: "user", Suggestion: "user", Range: lspRange{
		Start: lspPosition{Line: 10, Character: 15}, End: lspPosition{Line: 10, Character: 19},
	}}
	d := lspDiagnostic{Range: fix.Range, Message: "x", Data: mustMarshal(fix)}
	if actions := quickFixActions(uri, src, fullDocRange(src), []lspDiagnostic{d}); len(actions) != 0 {
		t.Errorf("got action for a no-op replacement: %+v", actions)
	}
}

func TestCodeAction_RangeFiltering(t *testing.T) {
	src := quickFixCases[0].src
	const uri = "file:///tmp/range.bp"
	diags := computeDiagnostics(uri, src)
	d := findDiagnostic(t, diags, "reqire_auth")

	// Request range on an unrelated line: no action.
	far := lspRange{Start: lspPosition{Line: 0, Character: 0}, End: lspPosition{Line: 0, Character: 5}}
	if actions := quickFixActions(uri, src, far, []lspDiagnostic{d}); len(actions) != 0 {
		t.Errorf("unrelated range returned %d actions", len(actions))
	}

	// Empty cursor range placed inside the misspelled identifier: one action.
	fix, ok := decodeQuickFix(d.Data)
	if !ok {
		t.Fatal("no quick-fix data")
	}
	cursor := lspRange{Start: fix.Range.Start, End: fix.Range.Start}
	cursor.Start.Character++
	cursor.End.Character++
	if actions := quickFixActions(uri, src, cursor, []lspDiagnostic{d}); len(actions) != 1 {
		t.Errorf("cursor inside identifier returned %d actions, want 1", len(actions))
	}
}

func TestCodeAction_DataLessFallbackRecomputes(t *testing.T) {
	src := quickFixCases[4].src // unknown model field
	const uri = "file:///tmp/nodata.bp"
	diags := computeDiagnostics(uri, src)
	d := findDiagnostic(t, diags, "emial")

	// Client echoes the diagnostic without the data field.
	stripped := d
	stripped.Data = nil
	actions := quickFixActions(uri, src, fullDocRange(src), []lspDiagnostic{stripped})
	if len(actions) != 1 {
		t.Fatalf("data-less diagnostic: got %d actions, want 1", len(actions))
	}
	if actions[0].Title != `Change to "email"` {
		t.Errorf("title = %q", actions[0].Title)
	}
	if got := rangeText(src, actions[0].Edit.Changes[uri][0].Range); got != "emial" {
		t.Errorf("edit covers %q, want emial", got)
	}
}

func TestCodeAction_StaleDataIsNotApplied(t *testing.T) {
	src := quickFixCases[0].src
	const uri = "file:///tmp/stale.bp"
	diags := computeDiagnostics(uri, src)
	d := findDiagnostic(t, diags, "reqire_auth")

	// Payload points at a range that no longer holds the misspelled name.
	bad := quickFixData{Name: "reqire_auth", Suggestion: "require_auth", Range: lspRange{
		Start: lspPosition{Line: 0, Character: 0}, End: lspPosition{Line: 0, Character: 10},
	}}
	stale := d
	stale.Data = mustMarshal(bad)
	actions := quickFixActions(uri, src, fullDocRange(src), []lspDiagnostic{stale})
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1 recomputed from current text", len(actions))
	}
	if got := rangeText(src, actions[0].Edit.Changes[uri][0].Range); got != "reqire_auth" {
		t.Errorf("edit covers %q, want reqire_auth", got)
	}
}

func TestCodeAction_EmptyContextUsesCurrentDiagnostics(t *testing.T) {
	src := quickFixCases[2].src
	const uri = "file:///tmp/emptyctx.bp"
	actions := quickFixActions(uri, src, fullDocRange(src), nil)
	if len(actions) != 1 || actions[0].Title != `Change to "user"` {
		t.Fatalf("empty context: got %+v", actions)
	}
}

func TestCodeAction_OnlyFilterExcludesQuickfix(t *testing.T) {
	if quickfixRequested([]string{"refactor", "source.organizeImports"}) {
		t.Error("quickfix should not be admitted by refactor-only filter")
	}
	if !quickfixRequested(nil) || !quickfixRequested([]string{"quickfix"}) {
		t.Error("quickfix should be admitted by empty or quickfix filters")
	}
}

func TestCodeAction_UnicodeLineUsesUTF16Columns(t *testing.T) {
	// Multi-byte text before the misspelled identifier on the same line: the
	// edit range must be in UTF-16 units, not bytes.
	src := caHeader + `
fn process_data {
  <- input string
  -> output string
  impl node { module: "./internal/process-data" }
}

POST /api/test {
  <- input string required
  |> result = "café ünï" + proces_data(input)
  -> 200 result
}
`
	const uri = "file:///tmp/utf16.bp"
	diags := computeDiagnostics(uri, src)
	d := findDiagnostic(t, diags, "proces_data")
	actions := quickFixActions(uri, src, fullDocRange(src), []lspDiagnostic{d})
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	edit := actions[0].Edit.Changes[uri][0]
	if got := rangeText(src, edit.Range); got != "proces_data" {
		t.Errorf("edit covers %q, want proces_data", got)
	}
	if fixed := applyTextEdits(t, src, actions[0].Edit.Changes[uri]); !strings.Contains(fixed, "+ process_data(input)") {
		t.Errorf("applied text wrong:\n%s", fixed)
	}
}

func TestServer_CodeActionRoundtrip(t *testing.T) {
	const uri = "file:///tmp/roundtrip.bp"
	src := quickFixCases[3].src

	id1, id2, id3 := 1, 2, 3
	initMsg := jsonRPCMessage{JSONRPC: "2.0", ID: &id1, Method: "initialize", Params: mustMarshal(map[string]interface{}{"processId": 1})}
	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 1, "languageId": "blueprint", "text": src},
	})}
	// Pass the published diagnostics back, as a client would.
	var input strings.Builder
	for _, m := range []jsonRPCMessage{initMsg, openMsg} {
		b, _ := json.Marshal(m)
		input.WriteString(framed(string(b)))
	}
	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input.String()), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	pub := diagnosticsNotifications(t, decodeFramed(t, out.Bytes()), uri)
	if len(pub) == 0 || len(pub[len(pub)-1].Diagnostics) == 0 {
		t.Fatal("expected published diagnostics")
	}
	diags := pub[len(pub)-1].Diagnostics

	// Second server run: codeAction request with the diagnostics attached.
	var input2 strings.Builder
	initB, _ := json.Marshal(initMsg)
	openB, _ := json.Marshal(openMsg)
	reqB, _ := json.Marshal(jsonRPCMessage{JSONRPC: "2.0", ID: &id2, Method: "textDocument/codeAction", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"range":        fullDocRange(src),
		"context":      map[string]interface{}{"diagnostics": diags},
	})})
	shutB, _ := json.Marshal(jsonRPCMessage{JSONRPC: "2.0", ID: &id3, Method: "shutdown"})
	exitB, _ := json.Marshal(jsonRPCMessage{JSONRPC: "2.0", Method: "exit"})
	input2.WriteString(framed(string(initB)) + framed(string(openB)) + framed(string(reqB)) + framed(string(shutB)) + framed(string(exitB)))

	var out2 bytes.Buffer
	if err := NewServer(strings.NewReader(input2.String()), &out2).Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	resp := findResponse(decodeFramed(t, out2.Bytes()), 2)
	if resp == nil {
		t.Fatal("no codeAction response")
	}
	var actions []codeAction
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		t.Fatalf("decode codeAction result: %v (raw=%s)", err, resp.Result)
	}
	if len(actions) != 1 || actions[0].Title != `Change to "process_data"` {
		t.Fatalf("actions = %+v", actions)
	}
}

func TestServer_InitializeAdvertisesCodeActionProvider(t *testing.T) {
	id1 := 1
	initB, _ := json.Marshal(jsonRPCMessage{JSONRPC: "2.0", ID: &id1, Method: "initialize", Params: mustMarshal(map[string]interface{}{"processId": 1})})
	exitB, _ := json.Marshal(jsonRPCMessage{JSONRPC: "2.0", Method: "exit"})
	var out bytes.Buffer
	if err := NewServer(strings.NewReader(framed(string(initB))+framed(string(exitB))), &out).Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp struct {
		Capabilities struct {
			CodeActionProvider struct {
				CodeActionKinds []string `json:"codeActionKinds"`
			} `json:"codeActionProvider"`
		} `json:"capabilities"`
	}
	msgs := decodeFramed(t, out.Bytes())
	if len(msgs) == 0 {
		t.Fatal("no initialize response")
	}
	if err := json.Unmarshal(msgs[0].Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kinds := resp.Capabilities.CodeActionProvider.CodeActionKinds
	if len(kinds) != 1 || kinds[0] != "quickfix" {
		t.Errorf("codeActionKinds = %v, want [quickfix]", kinds)
	}
}
