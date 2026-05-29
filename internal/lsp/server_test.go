package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// --- Pure-function unit tests --------------------------------------------------

func TestComputeDiagnostics_MalformedDocReportsErrors(t *testing.T) {
	// Unclosed model block — parser should report at least one ParseError.
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model user {
  id   uuid   primary
  name string required
`
	diags := computeDiagnostics("file:///tmp/test.bp", src)
	if len(diags) == 0 {
		t.Fatal("expected at least 1 diagnostic for malformed document, got 0")
	}
	for _, d := range diags {
		if d.Severity != severityError {
			t.Errorf("expected severity %d, got %d", severityError, d.Severity)
		}
		if d.Source != "blueprint" {
			t.Errorf("expected source 'blueprint', got %q", d.Source)
		}
		if d.Range.Start.Line < 0 || d.Range.End.Character < d.Range.Start.Character {
			t.Errorf("bad range: %+v", d.Range)
		}
	}
}

func TestComputeDiagnostics_DuplicateNameCheckerError(t *testing.T) {
	// Two models with the same name should trigger checker code C004.
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model user {
  id uuid primary
}
model user {
  id uuid primary
}
`
	diags := computeDiagnostics("file:///tmp/dup.bp", src)
	found := false
	for _, d := range diags {
		if d.Code == "C004" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected at least one C004 (duplicate name) diagnostic, got %d diagnostics: %+v", len(diags), diags)
	}
}

func TestComputeDiagnostics_CleanDocHasNoDiagnostics(t *testing.T) {
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model user {
  id   uuid    primary
  name string  required
}
`
	diags := computeDiagnostics("file:///tmp/ok.bp", src)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d: %+v", len(diags), diags)
	}
}

func TestComputeHover_Model(t *testing.T) {
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
@ "the canonical account holder"
model user {
  id   uuid   primary
  name string required
}
`
	idx := buildIndex("file:///tmp/h.bp", src)
	// Position the cursor in "user" of the model declaration (line index 7, col index 6).
	line, col := indexOf(src, "model user")
	out := computeHover(idx, line, col+len("model "))
	if !strings.Contains(out, "Model `user`") {
		t.Fatalf("expected Model hover, got %q", out)
	}
	if !strings.Contains(out, "the canonical account holder") {
		t.Errorf("expected intent text in hover, got %q", out)
	}
	if !strings.Contains(out, "`name`") {
		t.Errorf("expected field list to include 'name', got %q", out)
	}
}

func TestComputeHover_Intent(t *testing.T) {
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
@ "the canonical account holder"
model user {
  id uuid primary
}
`
	idx := buildIndex("file:///tmp/i.bp", src)
	line, col := indexOf(src, `@ "the canonical`)
	out := computeHover(idx, line, col)
	if !strings.Contains(out, "Intent") {
		t.Fatalf("expected Intent hover, got %q", out)
	}
	if !strings.Contains(out, "the canonical account holder") {
		t.Errorf("expected intent text, got %q", out)
	}
}

func TestComputeHover_Keyword(t *testing.T) {
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
`
	idx := buildIndex("file:///tmp/k.bp", src)
	line, col := indexOf(src, "blueprint")
	out := computeHover(idx, line, col)
	if !strings.Contains(out, "Declares the service name") {
		t.Fatalf("expected blueprint keyword doc, got %q", out)
	}
}

func TestComputeHover_DataOp(t *testing.T) {
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
pipe demo {
  |> rows = query user
  -> rows
}
`
	idx := buildIndex("file:///tmp/d.bp", src)
	line, col := indexOf(src, "query user")
	out := computeHover(idx, line, col)
	if !strings.Contains(out, "**query**") {
		t.Fatalf("expected query data-op doc, got %q", out)
	}
}

func TestComputeDefinition_Model(t *testing.T) {
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model user {
  id uuid primary
}
pipe demo {
  |> rows = query user
  -> rows
}
`
	idx := buildIndex("file:///tmp/def.bp", src)
	// Jump from `query user` to `model user`.
	line, col := indexOf(src, "query user")
	loc := computeDefinition("file:///tmp/def.bp", idx, line, col+len("query "))
	if loc == nil {
		t.Fatal("expected a definition location, got nil")
	}
	got := loc["uri"].(string)
	if got != "file:///tmp/def.bp" {
		t.Errorf("expected uri %q, got %q", "file:///tmp/def.bp", got)
	}
	rng, ok := loc["range"].(lspRange)
	if !ok {
		t.Fatalf("expected range to be lspRange, got %T", loc["range"])
	}
	// Model decl is on line 5 (0-indexed).
	if rng.Start.Line != 5 {
		t.Errorf("expected model decl on line 5, got %d", rng.Start.Line)
	}
}

func TestComputeDefinition_Field(t *testing.T) {
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
model user {
  id uuid primary
  name string required
}
GET /users/:id {
  |> user = fetch user(id: id)
  -> 200 user.name
}
`
	idx := buildIndex("file:///tmp/field.bp", src)
	line, col := indexOf(src, "user.name")
	// Position the cursor on "name" (after the dot).
	loc := computeDefinition("file:///tmp/field.bp", idx, line, col+len("user.")+1)
	if loc == nil {
		t.Fatal("expected a field definition location, got nil")
	}
	rng := loc["range"].(lspRange)
	// `name` is declared on line 7 (0-indexed), col 2.
	if rng.Start.Line != 7 {
		t.Errorf("expected field decl on line 7, got %d (range=%+v)", rng.Start.Line, rng)
	}
}

func TestComputeDefinition_UnknownReturnsNil(t *testing.T) {
	src := `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}
`
	idx := buildIndex("file:///tmp/u.bp", src)
	if loc := computeDefinition("file:///tmp/u.bp", idx, 0, 0); loc != nil {
		t.Fatalf("expected nil for unknown symbol, got %+v", loc)
	}
}

// --- Synthetic JSON-RPC roundtrip ---------------------------------------------

// rwc wires an in-memory pipe pair so Server.Run() can be driven by tests.
type rwc struct {
	in  io.Reader
	out io.Writer
}

func (r *rwc) Read(p []byte) (int, error)  { return r.in.Read(p) }
func (r *rwc) Write(p []byte) (int, error) { return r.out.Write(p) }

// framed wraps a JSON-RPC body in `Content-Length` framing.
func framed(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func TestServer_RoundtripPublishesDiagnosticsForBrokenDoc(t *testing.T) {
	id1 := 1
	initParams := map[string]interface{}{"processId": 1, "rootUri": ""}
	initMsg := jsonRPCMessage{JSONRPC: "2.0", ID: &id1, Method: "initialize", Params: mustMarshal(initParams)}
	initBytes, _ := json.Marshal(initMsg)

	// Open a deliberately malformed .bp document.
	openParams := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        "file:///tmp/r.bp",
			"version":    1,
			"languageId": "blueprint",
			"text":       "blueprint \"demo\" {\n  version \"0.1.0\"\n  port 3000\n  runtime node\n}\nmodel user {\n  id uuid primary\n",
		},
	}
	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(openParams)}
	openBytes, _ := json.Marshal(openMsg)

	id2 := 2
	shutdownMsg := jsonRPCMessage{JSONRPC: "2.0", ID: &id2, Method: "shutdown"}
	shutdownBytes, _ := json.Marshal(shutdownMsg)

	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}
	exitBytes, _ := json.Marshal(exitMsg)

	input := framed(string(initBytes)) + framed(string(openBytes)) + framed(string(shutdownBytes)) + framed(string(exitBytes))
	var out bytes.Buffer

	srv := NewServer(strings.NewReader(input), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Find the publishDiagnostics notification.
	msgs := decodeFramed(t, out.Bytes())
	var diagMsg *jsonRPCMessage
	for i := range msgs {
		if msgs[i].Method == "textDocument/publishDiagnostics" {
			diagMsg = &msgs[i]
			break
		}
	}
	if diagMsg == nil {
		t.Fatalf("expected a publishDiagnostics notification in output:\n%s", out.String())
	}

	var diags struct {
		URI         string          `json:"uri"`
		Diagnostics []lspDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(diagMsg.Params, &diags); err != nil {
		t.Fatalf("decode diagnostics params: %v", err)
	}
	if len(diags.Diagnostics) == 0 {
		t.Fatalf("expected at least 1 diagnostic, got 0 (raw=%s)", string(diagMsg.Params))
	}
	if diags.URI != "file:///tmp/r.bp" {
		t.Errorf("expected uri %q, got %q", "file:///tmp/r.bp", diags.URI)
	}
}

func TestServer_InitializeAdvertisesDefinitionProvider(t *testing.T) {
	id1 := 1
	initMsg := jsonRPCMessage{JSONRPC: "2.0", ID: &id1, Method: "initialize", Params: mustMarshal(map[string]interface{}{"processId": 1})}
	initBytes, _ := json.Marshal(initMsg)

	id2 := 2
	shutdownMsg := jsonRPCMessage{JSONRPC: "2.0", ID: &id2, Method: "shutdown"}
	shutdownBytes, _ := json.Marshal(shutdownMsg)
	exitBytes, _ := json.Marshal(jsonRPCMessage{JSONRPC: "2.0", Method: "exit"})

	input := framed(string(initBytes)) + framed(string(shutdownBytes)) + framed(string(exitBytes))

	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned: %v", err)
	}

	msgs := decodeFramed(t, out.Bytes())
	if len(msgs) == 0 {
		t.Fatal("expected at least one response")
	}
	// First response is the initialize result.
	var resp struct {
		Capabilities map[string]interface{} `json:"capabilities"`
	}
	if err := json.Unmarshal(msgs[0].Result, &resp); err != nil {
		t.Fatalf("decode initialize result: %v (raw=%s)", err, string(msgs[0].Result))
	}
	if v, _ := resp.Capabilities["definitionProvider"].(bool); !v {
		t.Errorf("expected definitionProvider=true, got %+v", resp.Capabilities)
	}
	if v, _ := resp.Capabilities["hoverProvider"].(bool); !v {
		t.Errorf("expected hoverProvider=true, got %+v", resp.Capabilities)
	}
}

// decodeFramed splits a Content-Length-framed stream into JSON-RPC messages.
func decodeFramed(t *testing.T, raw []byte) []jsonRPCMessage {
	t.Helper()
	var msgs []jsonRPCMessage
	rest := string(raw)
	for {
		idx := strings.Index(rest, "\r\n\r\n")
		if idx < 0 {
			break
		}
		header := rest[:idx]
		var length int
		for _, line := range strings.Split(header, "\r\n") {
			if strings.HasPrefix(line, "Content-Length: ") {
				fmt.Sscanf(line, "Content-Length: %d", &length)
			}
		}
		if length == 0 || len(rest) < idx+4+length {
			break
		}
		body := rest[idx+4 : idx+4+length]
		var m jsonRPCMessage
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("decode body: %v\nbody=%s", err, body)
		}
		msgs = append(msgs, m)
		rest = rest[idx+4+length:]
	}
	return msgs
}

// indexOf returns the (line, col) of the first occurrence of needle in src.
// Used by tests to locate cursor positions without hand-counting offsets.
func indexOf(src, needle string) (line, col int) {
	off := strings.Index(src, needle)
	if off < 0 {
		return 0, 0
	}
	prefix := src[:off]
	line = strings.Count(prefix, "\n")
	if line == 0 {
		return 0, off
	}
	lastNL := strings.LastIndex(prefix, "\n")
	return line, len(prefix) - lastNL - 1
}
