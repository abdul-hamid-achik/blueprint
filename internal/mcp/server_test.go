package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// testServer returns a server with two tools and one resource. The "boom"
// tool panics, the "echo" tool echoes its arguments as structured content.
func testServer() *Server {
	s := NewServer("test", "1.2.3", "test instructions")
	s.AddTool(Tool{
		Name:        "alpha",
		Description: "first",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(args json.RawMessage) ToolResult {
			return ToolResult{Text: "alpha ok"}
		},
	})
	s.AddTool(Tool{
		Name:        "echo",
		Title:       "Echo",
		Description: "echoes its arguments",
		InputSchema: map[string]any{"type": "object"},
		Annotations: map[string]any{"readOnlyHint": true},
		Handler: func(args json.RawMessage) ToolResult {
			var v any
			_ = json.Unmarshal(args, &v)
			return ToolResult{Text: string(args), Structured: v}
		},
	})
	s.AddTool(Tool{
		Name:        "boom",
		Description: "panics",
		Handler: func(args json.RawMessage) ToolResult {
			panic("kaboom")
		},
	})
	s.AddResource(Resource{
		URI:      "test://doc",
		Name:     "doc",
		MimeType: "text/markdown",
		Read:     func() (string, error) { return "# doc", nil },
	})
	s.AddResource(Resource{
		URI:      "test://broken",
		Name:     "broken",
		MimeType: "text/markdown",
		Read:     func() (string, error) { return "", errors.New("disk gone") },
	})
	return s
}

// exchange serves the given lines and returns the decoded response objects,
// in order.
func exchange(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var responses []map[string]any
	for _, raw := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if raw == "" {
			continue
		}
		if strings.ContainsAny(raw, "\r") {
			t.Fatalf("response contains a carriage return: %q", raw)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("response is not one JSON object: %v\n%s", err, raw)
		}
		responses = append(responses, m)
	}
	return responses
}

func one(t *testing.T, s *Server, line string) map[string]any {
	t.Helper()
	got := exchange(t, s, line)
	if len(got) != 1 {
		t.Fatalf("expected 1 response, got %d: %v", len(got), got)
	}
	return got[0]
}

func errorOf(t *testing.T, resp map[string]any) (int, map[string]any) {
	t.Helper()
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error response, got %v", resp)
	}
	code, _ := e["code"].(float64)
	data, _ := e["data"].(map[string]any)
	return int(code), data
}

func resultOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	r, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result response, got %v", resp)
	}
	return r
}

const modernMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}`

func TestLegacyInitializeEchoesSupportedVersion(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	r := resultOf(t, resp)
	if r["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want 2025-06-18", r["protocolVersion"])
	}
	if _, ok := r["resultType"]; ok {
		t.Errorf("legacy result must not carry resultType: %v", r)
	}
	if r["instructions"] != "test instructions" {
		t.Errorf("instructions = %v", r["instructions"])
	}
	info := r["serverInfo"].(map[string]any)
	if info["name"] != "test" || info["version"] != "1.2.3" {
		t.Errorf("serverInfo = %v", info)
	}
}

func TestLegacyInitializeUnknownVersionGetsNewestLegacy(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if v := resultOf(t, resp)["protocolVersion"]; v != "2025-11-25" {
		t.Errorf("protocolVersion = %v, want 2025-11-25", v)
	}
}

func TestNotificationGetsNoResponse(t *testing.T) {
	got := exchange(t, testServer(),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/anything-at-all","params":{}}`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"alpha"}}`,
	)
	if len(got) != 0 {
		t.Fatalf("notifications must not get responses, got %v", got)
	}
}

func TestPingLegacyAndModern(t *testing.T) {
	s := testServer()
	legacy := one(t, s, `{"jsonrpc":"2.0","id":7,"method":"ping"}`)
	if r := resultOf(t, legacy); len(r) != 0 {
		t.Errorf("legacy ping result = %v, want {}", r)
	}
	if legacy["id"] != float64(7) {
		t.Errorf("id not echoed: %v", legacy["id"])
	}

	modern := one(t, s, `{"jsonrpc":"2.0","id":8,"method":"ping","params":{`+modernMeta+`}}`)
	if code, _ := errorOf(t, modern); code != -32601 {
		t.Errorf("modern ping code = %d, want -32601", code)
	}
}

func TestModernRequestGetsResultTypeAndMeta(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":"abc","method":"tools/list","params":{`+modernMeta+`}}`)
	if resp["id"] != "abc" {
		t.Errorf("string id not echoed: %v", resp["id"])
	}
	r := resultOf(t, resp)
	if r["resultType"] != "complete" {
		t.Errorf("resultType = %v", r["resultType"])
	}
	if r["ttlMs"] != float64(3600000) || r["cacheScope"] != "public" {
		t.Errorf("cache hints missing: ttlMs=%v cacheScope=%v", r["ttlMs"], r["cacheScope"])
	}
	meta := r["_meta"].(map[string]any)
	if _, ok := meta["io.modelcontextprotocol/serverInfo"]; !ok {
		t.Errorf("_meta missing serverInfo: %v", meta)
	}
}

func TestModernUnsupportedVersion(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}}`)
	code, data := errorOf(t, resp)
	if code != -32022 {
		t.Fatalf("code = %d, want -32022", code)
	}
	if data["requested"] != "2099-01-01" {
		t.Errorf("requested = %v", data["requested"])
	}
	supported, _ := data["supported"].([]any)
	want := []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
	if len(supported) != len(want) {
		t.Fatalf("supported = %v", supported)
	}
	for i, v := range want {
		if supported[i] != v {
			t.Errorf("supported[%d] = %v, want %s", i, supported[i], v)
		}
	}
}

func TestServerDiscoverWithoutMeta(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":4,"method":"server/discover"}`)
	r := resultOf(t, resp)
	if r["resultType"] != "complete" || r["ttlMs"] != float64(3600000) || r["cacheScope"] != "public" {
		t.Errorf("discover result = %v", r)
	}
	if r["instructions"] != "test instructions" {
		t.Errorf("instructions = %v", r["instructions"])
	}
	versions := r["supportedVersions"].([]any)
	if len(versions) != 5 || versions[0] != "2026-07-28" {
		t.Errorf("supportedVersions = %v", versions)
	}
	caps := r["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities = %v", caps)
	}
}

func TestToolsListOrderAndNoNulls(t *testing.T) {
	s := testServer()
	resp := one(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`)
	raw := mustRaw(t, resp)
	if strings.Contains(raw, "null") {
		t.Errorf("tools/list contains null: %s", raw)
	}
	tools := resultOf(t, resp)["tools"].([]any)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "alpha,echo,boom" {
		t.Errorf("tool order = %v", names)
	}
	// A tool without a schema still gets a usable object.
	schema := tools[2].(map[string]any)["inputSchema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("default schema = %v", schema)
	}
}

func TestEmptyToolListIsArray(t *testing.T) {
	resp := one(t, NewServer("x", "0", ""), `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`)
	if !strings.Contains(mustRaw(t, resp), `"tools":[]`) {
		t.Errorf("empty tool list must marshal as []: %s", mustRaw(t, resp))
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope"}}`)
	if code, _ := errorOf(t, resp); code != -32602 {
		t.Errorf("code = %d, want -32602", code)
	}
	if msg := resp["error"].(map[string]any)["message"]; msg != "Unknown tool: nope" {
		t.Errorf("message = %v", msg)
	}
}

func TestToolsCallMissingArgumentsTreatedAsEmpty(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo"}}`)
	r := resultOf(t, resp)
	if r["isError"] != false {
		t.Errorf("isError = %v, want false", r["isError"])
	}
	structured, ok := r["structuredContent"].(map[string]any)
	if !ok || len(structured) != 0 {
		t.Errorf("structuredContent = %v, want empty object", r["structuredContent"])
	}
	text := r["content"].([]any)[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "{}" {
		t.Errorf("content = %v", text)
	}
}

func TestToolsCallStructuredAndModern(t *testing.T) {
	line := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{` + modernMeta + `,"name":"echo","arguments":{"a":1}}}`
	r := resultOf(t, one(t, testServer(), line))
	if r["resultType"] != "complete" {
		t.Errorf("modern tools/call resultType = %v", r["resultType"])
	}
	if _, ok := r["ttlMs"]; ok {
		t.Errorf("tools/call must not carry ttlMs")
	}
	if got := r["structuredContent"].(map[string]any)["a"]; got != float64(1) {
		t.Errorf("structuredContent = %v", r["structuredContent"])
	}
}

func TestToolHandlerPanicBecomesIsError(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"boom"}}`)
	r := resultOf(t, resp)
	if r["isError"] != true {
		t.Fatalf("panic must become isError result, got %v", r)
	}
	if !strings.Contains(mustRaw(t, resp), "kaboom") {
		t.Errorf("panic text missing: %s", mustRaw(t, resp))
	}
}

func TestToolsCallBadParams(t *testing.T) {
	s := testServer()
	cases := []string{
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":3}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"echo","arguments":"x"}}`,
	}
	for _, c := range cases {
		if code, _ := errorOf(t, one(t, s, c)); code != -32602 {
			t.Errorf("%s: code = %d, want -32602", c, code)
		}
	}
}

func TestMalformedLineIsParseError(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":`)
	if code, _ := errorOf(t, resp); code != -32700 {
		t.Errorf("code = %d, want -32700", code)
	}
	if resp["id"] != nil {
		t.Errorf("id = %v, want null", resp["id"])
	}
}

func TestBatchArrayIsInvalidRequest(t *testing.T) {
	resp := one(t, testServer(), `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`)
	if code, _ := errorOf(t, resp); code != -32600 {
		t.Errorf("code = %d, want -32600", code)
	}
	if resp["id"] != nil {
		t.Errorf("id = %v, want null", resp["id"])
	}
}

func TestMissingMethodWithIDIsInvalidRequest(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":21}`)
	if code, _ := errorOf(t, resp); code != -32600 {
		t.Errorf("code = %d, want -32600", code)
	}
	if resp["id"] != float64(21) {
		t.Errorf("id = %v, want 21", resp["id"])
	}
}

func TestUnknownMethodIsNotFound(t *testing.T) {
	s := testServer()
	if code, _ := errorOf(t, one(t, s, `{"jsonrpc":"2.0","id":13,"method":"no/such"}`)); code != -32601 {
		t.Errorf("legacy code = %d, want -32601", code)
	}
	if code, _ := errorOf(t, one(t, s, `{"jsonrpc":"2.0","id":13,"method":"no/such","params":{`+modernMeta+`}}`)); code != -32601 {
		t.Errorf("modern code = %d, want -32601", code)
	}
}

func TestResourcesListAndRead(t *testing.T) {
	s := testServer()
	list := one(t, s, `{"jsonrpc":"2.0","id":14,"method":"resources/list"}`)
	resources := resultOf(t, list)["resources"].([]any)
	if len(resources) != 2 || resources[0].(map[string]any)["uri"] != "test://doc" {
		t.Errorf("resources = %v", resources)
	}

	read := resultOf(t, one(t, s, `{"jsonrpc":"2.0","id":15,"method":"resources/read","params":{"uri":"test://doc"}}`))
	contents := read["contents"].([]any)[0].(map[string]any)
	if contents["text"] != "# doc" || contents["mimeType"] != "text/markdown" || contents["uri"] != "test://doc" {
		t.Errorf("contents = %v", contents)
	}

	modernRead := resultOf(t, one(t, s, `{"jsonrpc":"2.0","id":18,"method":"resources/read","params":{"uri":"test://doc",`+modernMeta+`}}`))
	if modernRead["resultType"] != "complete" || modernRead["ttlMs"] != float64(3600000) || modernRead["cacheScope"] != "public" {
		t.Errorf("modern resources/read missing modern fields: %v", modernRead)
	}
}

func TestResourceNotFoundCodes(t *testing.T) {
	s := testServer()
	legacy := one(t, s, `{"jsonrpc":"2.0","id":16,"method":"resources/read","params":{"uri":"test://missing"}}`)
	if code, _ := errorOf(t, legacy); code != -32002 {
		t.Errorf("legacy code = %d, want -32002", code)
	}
	modern := one(t, s, `{"jsonrpc":"2.0","id":16,"method":"resources/read","params":{"uri":"test://missing","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if code, _ := errorOf(t, modern); code != -32602 {
		t.Errorf("modern code = %d, want -32602", code)
	}
}

func TestResourceReadFailureIsInternalError(t *testing.T) {
	resp := one(t, testServer(), `{"jsonrpc":"2.0","id":17,"method":"resources/read","params":{"uri":"test://broken"}}`)
	if code, _ := errorOf(t, resp); code != -32603 {
		t.Errorf("code = %d, want -32603", code)
	}
}

func TestEachRequestGetsOneResponseInOrder(t *testing.T) {
	got := exchange(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		``,
		`{"jsonrpc":"2.0","id":3,"method":"no/such"}`,
	)
	if len(got) != 3 {
		t.Fatalf("got %d responses, want 3", len(got))
	}
	for i, want := range []float64{1, 2, 3} {
		if got[i]["id"] != want {
			t.Errorf("response %d id = %v, want %v", i, got[i]["id"], want)
		}
	}
}

func TestServeReturnsNilOnEOFWithoutInput(t *testing.T) {
	var out bytes.Buffer
	if err := testServer().Serve(strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestLargeArgumentLine(t *testing.T) {
	big := strings.Repeat("x", 2<<20)
	line := `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"echo","arguments":{"source":"` + big + `"}}}`
	r := resultOf(t, one(t, testServer(), line))
	if r["isError"] != false {
		t.Errorf("large argument failed: %v", r["isError"])
	}
}

func mustRaw(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
