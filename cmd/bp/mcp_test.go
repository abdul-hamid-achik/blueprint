package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mcpStep is one line sent to `bp mcp`. Notifications get no reply.
type mcpStep struct {
	name   string
	line   string
	notify bool
	id     any // expected id in the reply; nil expects null
}

// mcpRPC builds one request line.
func mcpRPC(t *testing.T, id any, method string, params any) string {
	t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	return mcpMarshal(t, msg)
}

// mcpCall builds a tools/call request line.
func mcpCall(t *testing.T, id int, tool string, args map[string]any) string {
	t.Helper()
	return mcpRPC(t, id, "tools/call", map[string]any{"name": tool, "arguments": args})
}

func mcpMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.ContainsRune(b, '\n') {
		t.Fatalf("request line contains a newline: %s", b)
	}
	return string(b)
}

// runMCPSession starts `bp mcp`, writes the lines to stdin, closes it, and
// returns the decoded reply lines and the exit code. Every stdout line must be
// a JSON-RPC 2.0 object.
func runMCPSession(t *testing.T, lines []string) ([]map[string]any, string, int) {
	t.Helper()
	cmd := exec.Command(bpBinary, "mcp")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("failed to run bp mcp: %v", err)
		}
		code = exitErr.ExitCode()
	}

	var replies []map[string]any
	for _, raw := range strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("stdout line is not JSON (stray output?): %v\n%s", err, raw)
		}
		if m["jsonrpc"] != "2.0" {
			t.Fatalf("stdout line is not JSON-RPC 2.0: %s", raw)
		}
		if _, hasResult := m["result"]; !hasResult {
			if _, hasError := m["error"]; !hasError {
				t.Fatalf("stdout line is neither result nor error: %s", raw)
			}
		}
		replies = append(replies, m)
	}
	return replies, stderr.String(), code
}

// toolResultOf returns the result object of a tools/call reply.
func toolResultOf(t *testing.T, reply map[string]any) map[string]any {
	t.Helper()
	r, ok := reply["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected a result, got %v", reply)
	}
	return r
}

func TestMCPStdioSession(t *testing.T) {
	root := getProjectRoot()
	hello := filepath.Join(root, "examples", "hello-world.bp")
	todo := filepath.Join(root, "examples", "todo-api.bp")
	realtime := filepath.Join(root, "examples", "realtime-chat.bp")
	outDir := filepath.Join(t.TempDir(), "out")
	pyDir := filepath.Join(t.TempDir(), "py")

	const c052 = "blueprint \"c\" {\n  version \"1.0.0\"\n  runtime node\n}\n\n" +
		"POST /api/x {\n  <- flag bool default(1)\n  -> 200 { ok: true }\n}\n"
	const unformatted = "blueprint \"c\" {\n    version \"1.0.0\"\n  runtime node\n}\n"
	modernMeta := map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28"}}

	steps := []mcpStep{
		{name: "initialize", id: 1, line: mcpRPC(t, 1, "initialize", map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		})},
		{name: "initialized", notify: true, line: mcpMarshal(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})},
		{name: "tools/list", id: 2, line: mcpRPC(t, 2, "tools/list", nil)},
		{name: "check C052", id: 3, line: mcpCall(t, 3, "check", map[string]any{"source": c052})},
		{name: "check path", id: 4, line: mcpCall(t, 4, "check", map[string]any{"path": hello})},
		{name: "lint path", id: 5, line: mcpCall(t, 5, "lint", map[string]any{"path": todo})},
		{name: "format", id: 6, line: mcpCall(t, 6, "format", map[string]any{"source": unformatted})},
		{name: "explain", id: 7, line: mcpCall(t, 7, "explain", map[string]any{"code": "C052"})},
		{name: "context", id: 8, line: mcpCall(t, 8, "context", map[string]any{"topic": "language"})},
		{name: "context unknown", id: 9, line: mcpCall(t, 9, "context", map[string]any{"topic": "nope"})},
		{name: "build", id: 10, line: mcpCall(t, 10, "build", map[string]any{"path": hello, "out": outDir})},
		{name: "diff", id: 11, line: mcpCall(t, 11, "diff", map[string]any{"path": hello, "out": outDir})},
		{name: "build python", id: 12, line: mcpCall(t, 12, "build", map[string]any{"path": realtime, "out": pyDir, "target": "python"})},
		{name: "malformed", id: nil, line: `{"jsonrpc":"2.0","id":`},
		{name: "unknown method", id: 13, line: mcpRPC(t, 13, "no/such", nil)},
		{name: "modern tools/list", id: 14, line: mcpRPC(t, 14, "tools/list", modernMeta)},
		{name: "server/discover", id: 15, line: mcpRPC(t, 15, "server/discover", nil)},
	}

	var lines []string
	responses := 0
	for _, s := range steps {
		lines = append(lines, s.line)
		if !s.notify {
			responses++
		}
	}

	replies, stderr, code := runMCPSession(t, lines)
	if code != 0 {
		t.Fatalf("bp mcp exited %d after stdin closed; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "Blueprint MCP server (stdio) ready") {
		t.Errorf("missing startup line on stderr: %q", stderr)
	}
	if len(replies) != responses {
		t.Fatalf("got %d reply lines for %d requests", len(replies), responses)
	}

	// Replies come back in request order; match each one to its step.
	byName := map[string]map[string]any{}
	next := 0
	for _, s := range steps {
		if s.notify {
			continue
		}
		reply := replies[next]
		next++
		if mcpMarshal(t, reply["id"]) != mcpMarshal(t, s.id) {
			t.Errorf("%s: reply id = %v, want %v", s.name, reply["id"], s.id)
		}
		byName[s.name] = reply
	}

	// Discovery and protocol errors.
	if r := resultOf(t, byName["initialize"]); r["protocolVersion"] != "2025-06-18" {
		t.Errorf("initialize protocolVersion = %v", r["protocolVersion"])
	}
	if code := errCode(t, byName["malformed"]); code != -32700 {
		t.Errorf("malformed: code %d, want -32700", code)
	}
	if code := errCode(t, byName["unknown method"]); code != -32601 {
		t.Errorf("unknown method: code %d, want -32601", code)
	}
	modernList := resultOf(t, byName["modern tools/list"])
	if modernList["resultType"] != "complete" || modernList["ttlMs"] != float64(3600000) {
		t.Errorf("modern tools/list missing modern fields: %v", modernList)
	}
	if _, ok := modernList["_meta"]; !ok {
		t.Errorf("modern tools/list missing _meta")
	}
	discover := resultOf(t, byName["server/discover"])
	if discover["resultType"] != "complete" || discover["instructions"] == nil {
		t.Errorf("server/discover result = %v", discover)
	}

	// tools/list names every tool in order.
	var names []string
	for _, tool := range resultOf(t, byName["tools/list"])["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "check,lint,format,explain,context,build,diff" {
		t.Errorf("tools/list names = %v", names)
	}

	// Every tool runs through the real binary without stray stdout bytes.
	check := toolResultOf(t, byName["check C052"])
	if check["isError"] != false {
		t.Errorf("check C052 is a diagnostic, not a failure: %v", check)
	}
	checkJSON := check["structuredContent"].(map[string]any)
	if checkJSON["success"] != false || !strings.Contains(mcpMarshal(t, checkJSON), `"C052"`) {
		t.Errorf("check C052 JSON = %v, want success false with C052", checkJSON)
	}

	if r := toolResultOf(t, byName["check path"]); r["isError"] != false ||
		r["structuredContent"].(map[string]any)["success"] != true {
		t.Errorf("check hello-world: %v", r)
	}
	if r := toolResultOf(t, byName["lint path"]); r["isError"] != false {
		t.Errorf("lint todo-api: %v", r)
	}

	format := toolResultOf(t, byName["format"])
	if format["isError"] != false || format["structuredContent"].(map[string]any)["changed"] != true {
		t.Errorf("format of unformatted source: %v", format)
	}

	explain := toolResultOf(t, byName["explain"])
	if explain["isError"] != false || !strings.Contains(textOf(explain), "C052") {
		t.Errorf("explain C052: %v", explain)
	}

	if r := toolResultOf(t, byName["context"]); r["isError"] != false || textOf(r) == "" {
		t.Errorf("context language: %v", r)
	}
	if r := toolResultOf(t, byName["context unknown"]); r["isError"] != true {
		t.Errorf("context nope should be isError: %v", r)
	}

	build := toolResultOf(t, byName["build"])
	if build["isError"] != false {
		t.Fatalf("build hello-world: %v", build)
	}
	if _, err := os.Stat(filepath.Join(outDir, ".blueprint", "manifest.json")); err != nil {
		t.Errorf("build did not write the manifest: %v", err)
	}

	diff := toolResultOf(t, byName["diff"])
	if diff["isError"] != false || diff["structuredContent"].(map[string]any)["changed"] != false {
		t.Errorf("diff right after build should report no changes: %v", diff)
	}

	pyBuild := toolResultOf(t, byName["build python"])
	if pyBuild["isError"] != true {
		t.Errorf("python build of realtime-chat should be isError: %v", pyBuild)
	}
	if got := pyBuild["structuredContent"].(map[string]any)["exit_code"]; got != float64(4) {
		t.Errorf("python build exit_code = %v, want 4", got)
	}
}

// TestMCPModernRequestsOnly checks the stateless era end to end: discovery,
// a modern tools/list, and an unsupported modern version.
func TestMCPModernRequestsOnly(t *testing.T) {
	bad := map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2099-01-01"}}
	lines := []string{
		mcpRPC(t, 1, "server/discover", nil),
		mcpRPC(t, 2, "tools/list", map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28"}}),
		mcpRPC(t, 3, "tools/list", bad),
	}
	replies, _, code := runMCPSession(t, lines)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(replies) != 3 {
		t.Fatalf("got %d replies for 3 requests", len(replies))
	}
	if r := resultOf(t, replies[0]); r["cacheScope"] != "public" {
		t.Errorf("discover: %v", r)
	}
	if r := resultOf(t, replies[1]); r["resultType"] != "complete" {
		t.Errorf("modern tools/list: %v", r)
	}
	if code := errCode(t, replies[2]); code != -32022 {
		t.Errorf("unsupported version: code %d, want -32022", code)
	}
}

func resultOf(t *testing.T, reply map[string]any) map[string]any {
	t.Helper()
	r, ok := reply["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %v", reply)
	}
	return r
}

func errCode(t *testing.T, reply map[string]any) int {
	t.Helper()
	e, ok := reply["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got %v", reply)
	}
	return int(e["code"].(float64))
}

// textOf joins the text blocks of a tool result.
func textOf(result map[string]any) string {
	var b strings.Builder
	content, _ := result["content"].([]any)
	for _, c := range content {
		if block, ok := c.(map[string]any); ok {
			if s, ok := block["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}
