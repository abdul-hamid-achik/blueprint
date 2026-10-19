package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// rng builds an lspRange from (line, char) pairs.
func rng(sl, sc, el, ec int) *lspRange {
	return &lspRange{
		Start: lspPosition{Line: sl, Character: sc},
		End:   lspPosition{Line: el, Character: ec},
	}
}

func TestApplyContentChange(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		rng     *lspRange
		newText string
		want    string
	}{
		{name: "insert at start", text: "abc", rng: rng(0, 0, 0, 0), newText: "X", want: "Xabc"},
		{name: "insert at end of document", text: "abc", rng: rng(0, 3, 0, 3), newText: "!", want: "abc!"},
		{name: "insert a newline", text: "ab\ncd", rng: rng(0, 1, 0, 1), newText: "\n", want: "a\nb\ncd"},
		{name: "delete across lines", text: "a\nb\nc", rng: rng(0, 1, 2, 0), newText: "", want: "ac"},
		{name: "replace across lines", text: "one\ntwo\nthree", rng: rng(0, 2, 1, 1), newText: "X", want: "onXwo\nthree"},
		{name: "character after e-acute counts one unit", text: "café!", rng: rng(0, 4, 0, 4), newText: "X", want: "caféX!"},
		{name: "character after emoji counts two units", text: "x😀y", rng: rng(0, 3, 0, 3), newText: "Z", want: "x😀Zy"},
		{name: "delete an emoji by UTF-16 range", text: "a😀b", rng: rng(0, 1, 0, 3), newText: "", want: "ab"},
		{name: "CRLF insert before CR", text: "a\r\nb", rng: rng(0, 1, 0, 1), newText: "X", want: "aX\r\nb"},
		{name: "CRLF past line end clamps before CR", text: "a\r\nb", rng: rng(0, 9, 0, 9), newText: "X", want: "aX\r\nb"},
		{name: "CRLF second line", text: "a\r\nbc\r\n", rng: rng(1, 2, 1, 2), newText: "X", want: "a\r\nbcX\r\n"},
		{name: "line past end clamps to len(text)", text: "ab\ncd", rng: rng(7, 0, 7, 0), newText: "X", want: "ab\ncdX"},
		{name: "character past line end clamps to line end", text: "ab\ncd", rng: rng(0, 50, 0, 50), newText: "X", want: "abX\ncd"},
		{name: "negative line clamps to start", text: "ab", rng: rng(-1, 0, -1, 0), newText: "X", want: "Xab"},
		{name: "end before start collapses to insertion", text: "abcdef", rng: rng(0, 4, 0, 2), newText: "X", want: "abcdXef"},
		{name: "nil range replaces whole document", text: "old text", rng: nil, newText: "new", want: "new"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applyContentChange(tt.text, tt.rng, tt.newText); got != tt.want {
				t.Fatalf("applyContentChange(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// TestApplyContentChanges_BatchAppliesInOrder checks that a batch is applied
// one change at a time, so later ranges are in the coordinates left by the
// earlier ones.
func TestApplyContentChanges_BatchAppliesInOrder(t *testing.T) {
	text := "abc\n"
	changes := []struct {
		rng     *lspRange
		newText string
	}{
		{rng(0, 3, 0, 3), "X"}, // "abcX\n"
		{rng(0, 4, 0, 4), "Y"}, // needs the X from the first change: "abcXY\n"
		{rng(1, 0, 1, 0), "Z"}, // the empty line after the final newline: "abcXY\nZ"
	}
	for _, c := range changes {
		text = applyContentChange(text, c.rng, c.newText)
	}
	if want := "abcXY\nZ"; text != want {
		t.Fatalf("batch result = %q, want %q", text, want)
	}
}

// sessionServer returns a server whose responses land in out, so tests can
// call handleMessage directly and inspect the stored document text.
func sessionServer(out *bytes.Buffer) *Server {
	return NewServer(strings.NewReader(""), out)
}

func sendRequest(t *testing.T, srv *Server, id *int, method string, params any) {
	t.Helper()
	msg := &jsonRPCMessage{JSONRPC: "2.0", ID: id, Method: method, Params: mustMarshal(params)}
	if err := srv.handleMessage(msg); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func TestServer_IncrementalDidChange_StoredTextHoverAndDefinition(t *testing.T) {
	const uri = "file:///tmp/incremental.bp"
	openText := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model widget {
  id uuid primary
}
pipe demo {
  |> rows = query widget
  -> rows
}
`
	var out bytes.Buffer
	srv := sessionServer(&out)
	sendRequest(t, srv, nil, "textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 1, "languageId": "blueprint", "text": openText},
	})

	// Rename the model declaration and its use, as two incremental edits in
	// one notification. Both are single-line, so the line numbers hold.
	declLine, declCol := indexOf(openText, "model widget")
	useLine, useCol := indexOf(openText, "query widget")
	sendRequest(t, srv, nil, "textDocument/didChange", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 2},
		"contentChanges": []map[string]interface{}{
			{"range": rng(declLine, declCol+6, declLine, declCol+12), "text": "gizmo"},
			{"range": rng(useLine, useCol+6, useLine, useCol+12), "text": "gizmo"},
		},
	})

	wantText := strings.NewReplacer("model widget", "model gizmo", "query widget", "query gizmo").Replace(openText)
	if got := srv.docs[uri].Text; got != wantText {
		t.Fatalf("stored text after incremental change:\n%s\nwant:\n%s", got, wantText)
	}
	if srv.docs[uri].Version != 2 {
		t.Fatalf("version = %d, want 2", srv.docs[uri].Version)
	}

	id := 10
	sendRequest(t, srv, &id, "textDocument/hover", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": declLine, "character": declCol + 8},
	})
	id = 11
	sendRequest(t, srv, &id, "textDocument/definition", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": useLine, "character": useCol + 8},
	})

	msgs := decodeFramed(t, out.Bytes())
	hover := findResponse(msgs, 10)
	if hover == nil {
		t.Fatal("no hover response")
	}
	if !strings.Contains(string(hover.Result), "gizmo") {
		t.Fatalf("hover on renamed model = %s, want it to mention gizmo", hover.Result)
	}

	def := findResponse(msgs, 11)
	if def == nil {
		t.Fatal("no definition response")
	}
	var loc struct {
		URI   string `json:"uri"`
		Range lspRange
	}
	if err := json.Unmarshal(def.Result, &loc); err != nil {
		t.Fatalf("decode definition: %v (raw=%s)", err, def.Result)
	}
	if loc.URI != uri || loc.Range.Start.Line != declLine {
		t.Fatalf("definition = %+v, want declaration on line %d", loc, declLine)
	}
}
