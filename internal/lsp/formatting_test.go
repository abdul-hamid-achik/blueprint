package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// unformattedBlueprint has irregular indentation and no blank lines, so the
// canonical printer must change it.
const unformattedBlueprint = "blueprint \"fmt\" {\n  version \"0.1.0\"\n    port 3000\n  runtime node\n}\nmodel user {\nid uuid primary\n  name string required\n}\n"

// requestResponse opens text, sends one request with the given extra params
// through the real JSON-RPC loop, and returns the response with id 2.
func requestResponse(t *testing.T, uri, text, method string, extra map[string]any) *jsonRPCMessage {
	t.Helper()
	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": 1, "languageId": "blueprint", "text": text},
	})}
	return requestWithMessages(t, []jsonRPCMessage{openMsg}, uri, method, extra)
}

// requestUnopened sends one request for a document the client never opened.
func requestUnopened(t *testing.T, uri, method string) *jsonRPCMessage {
	t.Helper()
	return requestWithMessages(t, nil, uri, method, nil)
}

func requestWithMessages(t *testing.T, before []jsonRPCMessage, uri, method string, extra map[string]any) *jsonRPCMessage {
	t.Helper()
	initMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "initialize", Params: mustMarshal(map[string]any{"processId": 1})}
	params := map[string]any{"textDocument": map[string]any{"uri": uri}}
	for k, v := range extra {
		params[k] = v
	}
	reqMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(2), Method: method, Params: mustMarshal(params)}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}

	var input strings.Builder
	messages := append([]jsonRPCMessage{initMsg}, before...)
	messages = append(messages, reqMsg, exitMsg)
	for _, msg := range messages {
		body, _ := json.Marshal(msg)
		input.WriteString(framed(string(body)))
	}
	var output bytes.Buffer
	if err := NewServer(strings.NewReader(input.String()), &output).Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	resp := findResponse(decodeFramed(t, output.Bytes()), 2)
	if resp == nil {
		t.Fatalf("missing response to %s", method)
	}
	if resp.Error != nil {
		t.Fatalf("%s returned error: %+v", method, resp.Error)
	}
	return resp
}

func formatWithPrinter(t *testing.T, text string) string {
	t.Helper()
	file, errs := parseForFormat("fmt.bp", []byte(text))
	if len(errs) > 0 {
		t.Fatalf("fixture must parse cleanly: %+v", errs)
	}
	return ast.Print(file)
}

func TestFormatDocumentText_ChangesUnformattedAndIsIdempotent(t *testing.T) {
	formatted, changed := formatDocumentText("fmt.bp", unformattedBlueprint)
	if !changed {
		t.Fatal("unformatted document should produce an edit")
	}
	if formatted != formatWithPrinter(t, unformattedBlueprint) {
		t.Fatalf("formatting must use ast.Print, got:\n%s", formatted)
	}
	if _, changed := formatDocumentText("fmt.bp", formatted); changed {
		t.Fatalf("formatter output must be a fixed point, got:\n%s", formatted)
	}
}

// TestFormatDocumentText_OnlyStableOutputIsReturned checks the property that
// every edit the server offers is a fixed point of the printer, across all
// fixtures.
func TestFormatDocumentText_OnlyStableOutputIsReturned(t *testing.T) {
	var paths []string
	for _, pattern := range []string{"../../testdata/valid/*.bp", "../../examples/*.bp"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatal("no fixtures found")
	}
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		formatted, changed := formatDocumentText(path, string(src))
		if !changed {
			continue
		}
		if _, again := formatDocumentText(path, formatted); again {
			t.Errorf("%s: LSP offered an edit that is not stable on a second pass", path)
		}
	}
}

func TestFormatDocumentText_PreservesComments(t *testing.T) {
	text := "# top comment\nblueprint \"fmt\" {\n  version \"0.1.0\"\n    port 3000\n  runtime node\n}\n# model comment\nmodel user {\nid uuid primary # inline\n}\n"
	formatted, changed := formatDocumentText("fmt.bp", text)
	if !changed {
		t.Fatal("expected the irregular indentation to be formatted")
	}
	for _, comment := range []string{"# top comment", "# model comment", "# inline"} {
		if !strings.Contains(formatted, comment) {
			t.Fatalf("formatted output lost %q:\n%s", comment, formatted)
		}
	}
}

func TestFormatDocumentText_BrokenInputIsNotFormatted(t *testing.T) {
	if _, changed := formatDocumentText("fmt.bp", "model user {\n  id uuid primary\n"); changed {
		t.Fatal("document with parse errors must not be formatted")
	}
}

func TestFormattedOutputIsSafe_RejectsCommentLossAndParseErrors(t *testing.T) {
	source := "model user {\n  id uuid primary # keep me\n}\n"
	file, errs := parseForFormat("fmt.bp", []byte(source))
	if len(errs) > 0 {
		t.Fatalf("fixture must parse cleanly: %+v", errs)
	}
	if formattedOutputIsSafe("fmt.bp", file, "model user {\n  id uuid primary\n}\n") {
		t.Fatal("output that drops a comment must be rejected")
	}
	if formattedOutputIsSafe("fmt.bp", file, "model user {\n  id uuid primary # keep me\n") {
		t.Fatal("output with a parse error must be rejected")
	}
	if !formattedOutputIsSafe("fmt.bp", file, ast.Print(file)) {
		t.Fatal("printer output that keeps comments and parses must be accepted")
	}
}

func TestDocumentEndPosition(t *testing.T) {
	cases := []struct {
		text string
		want lspPosition
	}{
		{"", lspPosition{Line: 0, Character: 0}},
		{"abc", lspPosition{Line: 0, Character: 3}},
		{"a\nbc", lspPosition{Line: 1, Character: 2}},
		{"a\r\nbc", lspPosition{Line: 1, Character: 2}},
		{"a\r\nbc\r\n", lspPosition{Line: 2, Character: 0}},
		{"héllo\n", lspPosition{Line: 1, Character: 0}},
		{"x\n😀", lspPosition{Line: 1, Character: 2}},
	}
	for _, tc := range cases {
		if got := documentEndPosition(tc.text); got != tc.want {
			t.Errorf("documentEndPosition(%q) = %+v, want %+v", tc.text, got, tc.want)
		}
	}
}

func TestServer_Formatting_ReturnsOneFullDocumentEdit(t *testing.T) {
	const uri = "file:///tmp/fmt-edit.bp"
	resp := requestResponse(t, uri, unformattedBlueprint, "textDocument/formatting", map[string]any{
		"options": map[string]any{"tabSize": 2, "insertSpaces": true},
	})
	var edits []lspTextEdit
	if err := json.Unmarshal(resp.Result, &edits); err != nil {
		t.Fatalf("decode edits: %v (raw=%s)", err, resp.Result)
	}
	if len(edits) != 1 {
		t.Fatalf("want exactly one edit, got %+v", edits)
	}
	if edits[0].Range.Start != (lspPosition{}) {
		t.Fatalf("edit must start at the document origin: %+v", edits[0].Range)
	}
	if want := documentEndPosition(unformattedBlueprint); edits[0].Range.End != want {
		t.Fatalf("edit end = %+v, want %+v", edits[0].Range.End, want)
	}
	if want := formatWithPrinter(t, unformattedBlueprint); edits[0].NewText != want {
		t.Fatalf("newText does not match bp fmt output:\n%s", edits[0].NewText)
	}
}

func TestServer_Formatting_AlreadyFormattedReturnsEmptyArray(t *testing.T) {
	const uri = "file:///tmp/fmt-clean.bp"
	clean := formatWithPrinter(t, unformattedBlueprint)
	resp := requestResponse(t, uri, clean, "textDocument/formatting", nil)
	if got := string(resp.Result); got != "[]" {
		t.Fatalf("already formatted document must return [], got %s", got)
	}
}

func TestServer_Formatting_BrokenParseReturnsEmptyArray(t *testing.T) {
	const uri = "file:///tmp/fmt-broken.bp"
	resp := requestResponse(t, uri, "model user {\n  id uuid primary\n", "textDocument/formatting", nil)
	if got := string(resp.Result); got != "[]" {
		t.Fatalf("broken document must return [], got %s", got)
	}
}

func TestServer_Formatting_UnknownDocumentReturnsEmptyArray(t *testing.T) {
	resp := requestUnopened(t, "file:///tmp/never-opened.bp", "textDocument/formatting")
	if got := string(resp.Result); got != "[]" {
		t.Fatalf("unknown document must return [], got %s", got)
	}
}

func TestServer_Formatting_CRLFDocumentEditSpansAllLines(t *testing.T) {
	const uri = "file:///tmp/fmt-crlf.bp"
	crlf := strings.ReplaceAll(unformattedBlueprint, "\n", "\r\n")
	resp := requestResponse(t, uri, crlf, "textDocument/formatting", nil)
	var edits []lspTextEdit
	if err := json.Unmarshal(resp.Result, &edits); err != nil {
		t.Fatalf("decode edits: %v (raw=%s)", err, resp.Result)
	}
	if len(edits) != 1 {
		t.Fatalf("want one edit for a CRLF document, got %+v", edits)
	}
	lines := strings.Count(crlf, "\n")
	if got := edits[0].Range.End; got != (lspPosition{Line: lines, Character: 0}) {
		t.Fatalf("CRLF end = %+v, want line %d char 0", got, lines)
	}
	if strings.Contains(edits[0].NewText, "\r") {
		t.Fatal("formatter output is LF-only; newText must not contain CR")
	}
}

func TestServer_Formatting_NoTrailingNewlineEndsOnLastCharacter(t *testing.T) {
	const uri = "file:///tmp/fmt-no-eol.bp"
	text := "blueprint \"fmt\" {\r\n  version \"0.1.0\"\r\n    port 3000\r\n  runtime node\r\n}\r\nmodel user {\r\nid uuid primary\r\n}"
	resp := requestResponse(t, uri, text, "textDocument/formatting", nil)
	var edits []lspTextEdit
	if err := json.Unmarshal(resp.Result, &edits); err != nil {
		t.Fatalf("decode edits: %v (raw=%s)", err, resp.Result)
	}
	if len(edits) != 1 {
		t.Fatalf("want one edit, got %+v", edits)
	}
	if got := edits[0].Range.End; got != (lspPosition{Line: 7, Character: 1}) {
		t.Fatalf("end = %+v, want line 7 char 1 (after the final brace)", got)
	}
}

func TestServer_InitializeAdvertisesFormattingSymbolsAndFolding(t *testing.T) {
	initMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "initialize", Params: mustMarshal(map[string]any{"processId": 1})}
	body, _ := json.Marshal(initMsg)
	var out bytes.Buffer
	if err := NewServer(strings.NewReader(framed(string(body))), &out).Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	initResponse := findResponse(decodeFramed(t, out.Bytes()), 1)
	if initResponse == nil {
		t.Fatal("missing initialize response")
	}
	var initialized struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(initResponse.Result, &initialized); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	for _, key := range []string{"documentFormattingProvider", "documentSymbolProvider", "foldingRangeProvider"} {
		if v, _ := initialized.Capabilities[key].(bool); !v {
			t.Errorf("expected %s=true, got %+v", key, initialized.Capabilities[key])
		}
	}
}
