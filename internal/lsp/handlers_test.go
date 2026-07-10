package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// --- textDocument/didChange correctness ---------------------------------------
//
// The server advertises textDocumentSync = Full (see handleInitialize), which
// means every entry in a didChange notification's contentChanges carries the
// ENTIRE new document text (not a range-based patch). Per the LSP spec,
// multiple entries in one notification must be applied in order; for full
// sync that means "last entry wins". handleDidChange used to always apply
// ContentChanges[0], silently discarding any later entries. The tests below
// drive the server through its real JSON-RPC entry point (Server.Run reading
// framed messages off an in-memory pipe) to prove all changes are merged in
// order, not just the first.

func idPtr(v int) *int { return &v }

// findResponse returns the response message with the given id, if any.
func findResponse(msgs []jsonRPCMessage, id int) *jsonRPCMessage {
	for i := range msgs {
		if msgs[i].ID != nil && *msgs[i].ID == id {
			return &msgs[i]
		}
	}
	return nil
}

// diagnosticsNotifications returns every textDocument/publishDiagnostics
// notification for uri, in the order they were emitted.
func diagnosticsNotifications(t *testing.T, msgs []jsonRPCMessage, uri string) []struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
} {
	t.Helper()
	var out []struct {
		URI         string          `json:"uri"`
		Diagnostics []lspDiagnostic `json:"diagnostics"`
	}
	for _, m := range msgs {
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var d struct {
			URI         string          `json:"uri"`
			Diagnostics []lspDiagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(m.Params, &d); err != nil {
			t.Fatalf("decode publishDiagnostics params: %v", err)
		}
		if d.URI == uri {
			out = append(out, d)
		}
	}
	return out
}

func TestServer_DidChange_MultiChangeAppliesLastEntry(t *testing.T) {
	const uri = "file:///tmp/multi.bp"

	// Open text has a duplicate-model-name checker error (C004).
	openText := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model widget {
  id uuid primary
}
model widget {
  id uuid primary
}
`

	// First content change in the batch: deliberately garbage. If the server
	// (incorrectly) only applied ContentChanges[0], the document would end up
	// as this unparseable garbage instead of the valid final text below.
	garbageText := "!!! this is not a blueprint file ???"

	// Final content change: a valid, error-free document. Under full-document
	// sync semantics this is the text that should win.
	finalText := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model widget {
  id    uuid   primary
  count int    required
}
pipe demo {
  |> rows = query widget
  -> rows
}
`

	initMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "initialize", Params: mustMarshal(map[string]interface{}{"processId": 1})}
	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        uri,
			"version":    1,
			"languageId": "blueprint",
			"text":       openText,
		},
	})}
	changeMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 2},
		"contentChanges": []map[string]interface{}{
			{"text": garbageText},
			{"text": finalText},
		},
	})}

	hoverLine, hoverCol := indexOf(finalText, "model widget")
	hoverCol += len("model ")
	hoverMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(2), Method: "textDocument/hover", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": hoverLine, "character": hoverCol},
	})}

	defLine, defCol := indexOf(finalText, "query widget")
	defCol += len("query ")
	defMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(3), Method: "textDocument/definition", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": defLine, "character": defCol},
	})}

	closeMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didClose", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
	})}
	shutdownMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(4), Method: "shutdown"}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}

	var input strings.Builder
	for _, m := range []jsonRPCMessage{initMsg, openMsg, changeMsg, hoverMsg, defMsg, closeMsg, shutdownMsg, exitMsg} {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal %s: %v", m.Method, err)
		}
		input.WriteString(framed(string(b)))
	}

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input.String()), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	msgs := decodeFramed(t, out.Bytes())

	// --- diagnostics: open (errors) -> didChange (merged, clean) -> didClose (cleared)
	diags := diagnosticsNotifications(t, msgs, uri)
	if len(diags) < 3 {
		t.Fatalf("expected at least 3 publishDiagnostics notifications (open, change, close), got %d", len(diags))
	}
	if len(diags[0].Diagnostics) == 0 {
		t.Errorf("expected diagnostics after didOpen (duplicate model name), got none")
	}
	// This is the crux of the regression test: if handleDidChange only applied
	// ContentChanges[0] (the garbage text), this would report parse errors
	// instead of zero diagnostics for the clean, merged final text.
	if len(diags[1].Diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics after didChange merged to the clean final text, got %d: %+v", len(diags[1].Diagnostics), diags[1].Diagnostics)
	}
	last := diags[len(diags)-1]
	if len(last.Diagnostics) != 0 {
		t.Errorf("expected diagnostics cleared after didClose, got %d", len(last.Diagnostics))
	}

	// --- hover: should reflect the merged (final) text, not the garbage change[0]
	hoverResp := findResponse(msgs, 2)
	if hoverResp == nil {
		t.Fatal("expected a hover response with id=2")
	}
	if hoverResp.Error != nil {
		t.Fatalf("hover returned error: %+v", hoverResp.Error)
	}
	var hoverResult struct {
		Contents struct {
			Value string `json:"value"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(hoverResp.Result, &hoverResult); err != nil {
		t.Fatalf("decode hover result: %v (raw=%s)", err, string(hoverResp.Result))
	}
	if !strings.Contains(hoverResult.Contents.Value, "Model `widget`") {
		t.Errorf("expected hover to describe Model `widget` (proves merged text won), got %q", hoverResult.Contents.Value)
	}
	if !strings.Contains(hoverResult.Contents.Value, "`count`") {
		t.Errorf("expected hover fields list to include `count` field from final text, got %q", hoverResult.Contents.Value)
	}

	// --- definition: "query widget" should resolve back to "model widget" on line 5
	defResp := findResponse(msgs, 3)
	if defResp == nil {
		t.Fatal("expected a definition response with id=3")
	}
	if defResp.Error != nil {
		t.Fatalf("definition returned error: %+v", defResp.Error)
	}
	var defResult struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(defResp.Result, &defResult); err != nil {
		t.Fatalf("decode definition result: %v (raw=%s)", err, string(defResp.Result))
	}
	if defResult.URI != uri {
		t.Errorf("expected definition uri %q, got %q", uri, defResult.URI)
	}
	if defResult.Range.Start.Line != 5 {
		t.Errorf("expected model widget decl on line 5, got %d", defResult.Range.Start.Line)
	}

	// --- shutdown should still succeed cleanly after all of the above.
	shutdownResp := findResponse(msgs, 4)
	if shutdownResp == nil {
		t.Fatal("expected a shutdown response with id=4")
	}
	if shutdownResp.Error != nil {
		t.Errorf("shutdown returned error: %+v", shutdownResp.Error)
	}
}

// TestServer_DidChange_UnknownDocumentIsANoop guards the early-return path
// when a didChange arrives for a uri that was never opened (or already
// closed) — the server should neither panic nor publish diagnostics for it.
func TestServer_DidChange_UnknownDocumentIsANoop(t *testing.T) {
	const uri = "file:///tmp/never-opened.bp"

	changeMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: mustMarshal(map[string]interface{}{
		"textDocument":   map[string]interface{}{"uri": uri, "version": 2},
		"contentChanges": []map[string]interface{}{{"text": "irrelevant"}},
	})}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}

	var input strings.Builder
	for _, m := range []jsonRPCMessage{changeMsg, exitMsg} {
		b, _ := json.Marshal(m)
		input.WriteString(framed(string(b)))
	}

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input.String()), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	msgs := decodeFramed(t, out.Bytes())
	for _, m := range msgs {
		if m.Method == "textDocument/publishDiagnostics" {
			t.Errorf("did not expect diagnostics for a document that was never opened, got %+v", m)
		}
	}
}

// --- hover / definition: null results and unknown methods ---------------------

func TestServer_Hover_NoInfoReturnsNullResult(t *testing.T) {
	const uri = "file:///tmp/hovernull.bp"
	openText := "blueprint \"demo\" {\n  version \"0.1.0\"\n  port 3000\n  runtime node\n}\n\n\n"

	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 1, "languageId": "blueprint", "text": openText},
	})}
	// Position on a blank line: no symbol, no intent prefix -> empty hover.
	hoverMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "textDocument/hover", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": 5, "character": 0},
	})}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}

	var input strings.Builder
	for _, m := range []jsonRPCMessage{openMsg, hoverMsg, exitMsg} {
		b, _ := json.Marshal(m)
		input.WriteString(framed(string(b)))
	}

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input.String()), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	msgs := decodeFramed(t, out.Bytes())
	resp := findResponse(msgs, 1)
	if resp == nil {
		t.Fatal("expected a hover response with id=1")
	}
	if string(resp.Result) != "null" {
		t.Errorf("expected null hover result, got %s", string(resp.Result))
	}
}

func TestServer_Definition_UnresolvedReturnsNullResult(t *testing.T) {
	const uri = "file:///tmp/defnull.bp"
	openText := "blueprint \"demo\" {\n  version \"0.1.0\"\n  port 3000\n  runtime node\n}\n"

	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 1, "languageId": "blueprint", "text": openText},
	})}
	defMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "textDocument/definition", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": 0, "character": 0},
	})}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}

	var input strings.Builder
	for _, m := range []jsonRPCMessage{openMsg, defMsg, exitMsg} {
		b, _ := json.Marshal(m)
		input.WriteString(framed(string(b)))
	}

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input.String()), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	msgs := decodeFramed(t, out.Bytes())
	resp := findResponse(msgs, 1)
	if resp == nil {
		t.Fatal("expected a definition response with id=1")
	}
	if string(resp.Result) != "null" {
		t.Errorf("expected null definition result, got %s", string(resp.Result))
	}
}

// TestServer_DidClose_ClearsDiagnosticsAndIndex closes a doc and verifies an
// empty-diagnostics notification is published and that a subsequent hover on
// the (now-closed) uri behaves as "no document" rather than reusing stale state.
func TestServer_DidClose_ClearsDiagnosticsAndIndex(t *testing.T) {
	const uri = "file:///tmp/close.bp"
	openText := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model widget {
  id uuid primary
}
`
	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 1, "languageId": "blueprint", "text": openText},
	})}
	closeMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didClose", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
	})}
	hoverMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "textDocument/hover", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": 5, "character": 8},
	})}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}

	var input strings.Builder
	for _, m := range []jsonRPCMessage{openMsg, closeMsg, hoverMsg, exitMsg} {
		b, _ := json.Marshal(m)
		input.WriteString(framed(string(b)))
	}

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input.String()), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	msgs := decodeFramed(t, out.Bytes())
	diags := diagnosticsNotifications(t, msgs, uri)
	if len(diags) < 2 {
		t.Fatalf("expected diagnostics notifications for open + close, got %d", len(diags))
	}
	if len(diags[len(diags)-1].Diagnostics) != 0 {
		t.Errorf("expected empty diagnostics after didClose, got %+v", diags[len(diags)-1].Diagnostics)
	}

	// Hovering over a closed document has no backing Document, so it must
	// resolve to a null result rather than serving a stale AST.
	resp := findResponse(msgs, 1)
	if resp == nil {
		t.Fatal("expected a hover response with id=1")
	}
	if string(resp.Result) != "null" {
		t.Errorf("expected null hover result for a closed document, got %s", string(resp.Result))
	}
}

// TestServer_UnknownMethod_SendsMethodNotFoundError exercises the
// "method not found" default branch of handleMessage/sendError.
func TestServer_UnknownMethod_SendsMethodNotFoundError(t *testing.T) {
	unknownMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "textDocument/completion"}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}

	var input strings.Builder
	for _, m := range []jsonRPCMessage{unknownMsg, exitMsg} {
		b, _ := json.Marshal(m)
		input.WriteString(framed(string(b)))
	}

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input.String()), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	msgs := decodeFramed(t, out.Bytes())
	resp := findResponse(msgs, 1)
	if resp == nil {
		t.Fatal("expected a response with id=1")
	}
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error for an unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601 (method not found), got %d", resp.Error.Code)
	}
}
