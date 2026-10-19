// Package mcp implements a stdio Model Context Protocol server. It answers the
// stateless 2026-07-28 revision and the initialize-based 2025-11-25 and older
// revisions on the same connection. The package uses only the standard
// library and knows nothing about Blueprint.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Protocol revisions. modernVersions carry a version in each request's _meta
// and have no handshake; legacyVersions use initialize.
var (
	modernVersions = []string{"2026-07-28"}
	legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
)

const (
	metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	metaServerInfo      = "io.modelcontextprotocol/serverInfo"
	ttlMillis           = 3600000
	cacheScopePublic    = "public"
)

// JSON-RPC and MCP error codes.
const (
	codeParseError         = -32700
	codeInvalidRequest     = -32600
	codeMethodNotFound     = -32601
	codeInvalidParams      = -32602
	codeInternalError      = -32603
	codeUnsupportedVersion = -32022
	codeLegacyNotFound     = -32002
)

// Tool is a callable operation. Handler receives the raw arguments object,
// never nil ("{}" when the client sent none).
type Tool struct {
	Name        string
	Title       string
	Description string
	InputSchema map[string]any
	Annotations map[string]any
	Handler     func(args json.RawMessage) ToolResult
}

// ToolResult is what a Tool handler returns. IsError marks a failure the
// model should see, such as bad input, an unreadable path or a nonzero
// subprocess exit. Structured, when set, is sent as structuredContent.
type ToolResult struct {
	Text       string
	Structured any
	IsError    bool
}

// Resource is a readable document identified by its URI.
type Resource struct {
	URI         string
	Name        string
	Description string
	MimeType    string
	Read        func() (string, error)
}

// Server holds the registered tools and resources and serves one stdio
// connection.
type Server struct {
	name         string
	version      string
	instructions string
	tools        []Tool
	resources    []Resource
	writeMu      sync.Mutex
}

// NewServer returns a server that reports name, version and instructions
// during discovery and initialization.
func NewServer(name, version, instructions string) *Server {
	return &Server{name: name, version: version, instructions: instructions}
}

// AddTool registers a tool. Tools are listed in registration order.
func (s *Server) AddTool(t Tool) {
	s.tools = append(s.tools, t)
}

// AddResource registers a resource. Resources are listed in registration order.
func (s *Server) AddResource(r Resource) {
	s.resources = append(s.resources, r)
}

// request is one decoded JSON-RPC request or notification.
type request struct {
	id     json.RawMessage
	hasID  bool
	method string
	params json.RawMessage
}

// rpcError is the error member of a JSON-RPC response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// response is one JSON-RPC response. Exactly one of Result and Error is set.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads newline-delimited JSON-RPC messages from in and writes one
// response line per request to out. It returns nil at end of input.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	br := bufio.NewReader(in)
	for {
		line, err := br.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if werr := s.handleLine(trimmed, out); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// handleLine processes one message and writes its response, if it has one.
func (s *Server) handleLine(line []byte, out io.Writer) (err error) {
	var id json.RawMessage
	defer func() {
		if r := recover(); r != nil {
			err = s.write(out, errorResponse(id, codeInternalError, fmt.Sprintf("internal error: %v", r), nil))
		}
	}()

	if !json.Valid(line) {
		return s.write(out, errorResponse(nil, codeParseError, "Parse error", nil))
	}
	if line[0] != '{' {
		return s.write(out, errorResponse(nil, codeInvalidRequest, "Invalid Request: message must be a JSON object", nil))
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(line, &members); err != nil {
		return s.write(out, errorResponse(nil, codeInvalidRequest, "Invalid Request", nil))
	}

	req := request{}
	if raw, ok := members["id"]; ok {
		req.id = raw
		req.hasID = true
		id = raw
	}

	var method string
	hasMethod := false
	if raw, ok := members["method"]; ok && json.Unmarshal(raw, &method) == nil {
		hasMethod = true
	}
	if !hasMethod {
		// A client response (result or error, no method) is not a request.
		_, isResult := members["result"]
		_, isError := members["error"]
		if isResult || isError {
			return nil
		}
		if req.hasID {
			return s.write(out, errorResponse(req.id, codeInvalidRequest, "Invalid Request: method must be a string", nil))
		}
		return s.write(out, errorResponse(nil, codeInvalidRequest, "Invalid Request: method must be a string", nil))
	}
	req.method = method
	req.params = members["params"]

	if !req.hasID {
		// Notifications get no response, whatever their method.
		return nil
	}
	return s.write(out, s.dispatch(req))
}

// dispatch answers one request.
func (s *Server) dispatch(req request) response {
	if req.method == "server/discover" {
		return s.discover(req)
	}
	if version, modern := modernVersionOf(req.params); modern {
		if !contains(modernVersions, version) {
			return errorResponse(req.id, codeUnsupportedVersion, "Unsupported protocol version", map[string]any{
				"supported": allVersions(),
				"requested": version,
			})
		}
		return s.dispatchModern(req)
	}
	return s.dispatchLegacy(req)
}

// dispatchModern answers a request that declares a supported modern version.
func (s *Server) dispatchModern(req request) response {
	switch req.method {
	case "tools/list":
		return s.modernResult(req, map[string]any{"tools": s.toolList()}, true)
	case "tools/call":
		return s.callTool(req, true)
	case "resources/list":
		return s.modernResult(req, map[string]any{"resources": s.resourceList()}, true)
	case "resources/read":
		return s.readResource(req, true)
	default:
		// ping is removed in the modern revision, and initialize is legacy-only.
		return errorResponse(req.id, codeMethodNotFound, "Method not found: "+req.method, nil)
	}
}

// dispatchLegacy answers a request with no modern version in _meta. Legacy
// requests do not need a prior initialize.
func (s *Server) dispatchLegacy(req request) response {
	switch req.method {
	case "initialize":
		return s.initialize(req)
	case "ping":
		return okResponse(req.id, map[string]any{})
	case "tools/list":
		return okResponse(req.id, map[string]any{"tools": s.toolList()})
	case "tools/call":
		return s.callTool(req, false)
	case "resources/list":
		return okResponse(req.id, map[string]any{"resources": s.resourceList()})
	case "resources/read":
		return s.readResource(req, false)
	default:
		return errorResponse(req.id, codeMethodNotFound, "Method not found: "+req.method, nil)
	}
}

// discover answers server/discover. It is treated as modern even when _meta
// carries no protocol version.
func (s *Server) discover(req request) response {
	return s.modernResult(req, map[string]any{
		"supportedVersions": allVersions(),
		"capabilities":      s.capabilities(),
		"instructions":      s.instructions,
	}, true)
}

// initialize answers the legacy handshake. A requested version this server
// does not know gets the newest legacy version, as the spec allows.
func (s *Server) initialize(req request) response {
	var p struct {
		ProtocolVersion *string `json:"protocolVersion"`
	}
	if err := decodeParams(req.params, &p); err != nil {
		return errorResponse(req.id, codeInvalidParams, "Invalid params: "+err.Error(), nil)
	}
	version := legacyVersions[0]
	if p.ProtocolVersion != nil && contains(legacyVersions, *p.ProtocolVersion) {
		version = *p.ProtocolVersion
	}
	return okResponse(req.id, map[string]any{
		"protocolVersion": version,
		"capabilities":    s.capabilities(),
		"serverInfo":      s.serverInfo(),
		"instructions":    s.instructions,
	})
}

// callTool runs a tool. An unknown tool or malformed params is a protocol
// error; a handler failure comes back as a normal result with isError set.
func (s *Server) callTool(req request, modern bool) response {
	var p struct {
		Name      *string         `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := decodeParams(req.params, &p); err != nil {
		return errorResponse(req.id, codeInvalidParams, "Invalid params: "+err.Error(), nil)
	}
	if p.Name == nil {
		return errorResponse(req.id, codeInvalidParams, "Invalid params: name is required", nil)
	}
	tool := s.findTool(*p.Name)
	if tool == nil {
		return errorResponse(req.id, codeInvalidParams, "Unknown tool: "+*p.Name, nil)
	}

	args := p.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	if !isJSONObject(args) {
		return errorResponse(req.id, codeInvalidParams, "Invalid params: arguments must be an object", nil)
	}

	res := runTool(tool, args)
	result := map[string]any{
		"content": []map[string]any{{"type": "text", "text": res.Text}},
		"isError": res.IsError,
	}
	if res.Structured != nil {
		result["structuredContent"] = res.Structured
	}
	if modern {
		return s.modernResult(req, result, false)
	}
	return okResponse(req.id, result)
}

func (s *Server) findTool(name string) *Tool {
	for i := range s.tools {
		if s.tools[i].Name == name {
			return &s.tools[i]
		}
	}
	return nil
}

// runTool calls a handler and turns a panic into an isError result.
func runTool(t *Tool, args json.RawMessage) (res ToolResult) {
	defer func() {
		if r := recover(); r != nil {
			res = ToolResult{Text: fmt.Sprintf("tool %s failed: internal error: %v", t.Name, r), IsError: true}
		}
	}()
	return t.Handler(args)
}

// readResource answers resources/read. An unknown URI is -32602 in the modern
// revision and -32002 in the legacy one.
func (s *Server) readResource(req request, modern bool) response {
	var p struct {
		URI *string `json:"uri"`
	}
	if err := decodeParams(req.params, &p); err != nil {
		return errorResponse(req.id, codeInvalidParams, "Invalid params: "+err.Error(), nil)
	}
	if p.URI == nil {
		return errorResponse(req.id, codeInvalidParams, "Invalid params: uri is required", nil)
	}
	var res *Resource
	for i := range s.resources {
		if s.resources[i].URI == *p.URI {
			res = &s.resources[i]
			break
		}
	}
	if res == nil {
		code := codeInvalidParams
		if !modern {
			code = codeLegacyNotFound
		}
		return errorResponse(req.id, code, "Resource not found: "+*p.URI, map[string]any{"uri": *p.URI})
	}
	text, err := res.Read()
	if err != nil {
		return errorResponse(req.id, codeInternalError, "Failed to read resource: "+err.Error(), map[string]any{"uri": *p.URI})
	}
	result := map[string]any{
		"contents": []map[string]any{{"uri": res.URI, "mimeType": res.MimeType, "text": text}},
	}
	if modern {
		return s.modernResult(req, result, true)
	}
	return okResponse(req.id, result)
}

// toolList is the tools/list payload. It is never nil, so it marshals as [].
func (s *Server) toolList() []map[string]any {
	list := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		entry := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		}
		if t.Title != "" {
			entry["title"] = t.Title
		}
		if t.Annotations != nil {
			entry["annotations"] = t.Annotations
		}
		list = append(list, entry)
	}
	return list
}

// resourceList is the resources/list payload. It is never nil.
func (s *Server) resourceList() []map[string]any {
	list := make([]map[string]any, 0, len(s.resources))
	for _, r := range s.resources {
		list = append(list, map[string]any{
			"uri":         r.URI,
			"name":        r.Name,
			"description": r.Description,
			"mimeType":    r.MimeType,
		})
	}
	return list
}

func (s *Server) capabilities() map[string]any {
	return map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}
}

func (s *Server) serverInfo() map[string]any {
	return map[string]any{"name": s.name, "version": s.version}
}

// modernResult adds the modern-revision fields to a successful result. List
// results also carry the cache hints.
func (s *Server) modernResult(req request, result map[string]any, cacheable bool) response {
	result["resultType"] = "complete"
	result["_meta"] = map[string]any{metaServerInfo: s.serverInfo()}
	if cacheable {
		result["ttlMs"] = ttlMillis
		result["cacheScope"] = cacheScopePublic
	}
	return okResponse(req.id, result)
}

// write marshals one response as a single line and writes it under the write
// lock.
func (s *Server) write(out io.Writer, resp response) error {
	if len(resp.ID) == 0 {
		resp.ID = json.RawMessage("null")
	}
	resp.JSONRPC = "2.0"
	data, err := json.Marshal(resp)
	if err != nil {
		data, _ = json.Marshal(errorResponse(nil, codeInternalError, "internal error: "+err.Error(), nil))
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = out.Write(data)
	return err
}

func okResponse(id json.RawMessage, result any) response {
	return response{ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string, data any) response {
	return response{ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}}
}

// modernVersionOf reports the protocol version a request declares in
// params._meta, and whether it declares one. A non-string value counts as no
// declaration, so the request takes the legacy path.
func modernVersionOf(params json.RawMessage) (string, bool) {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil {
		return "", false
	}
	raw, ok := p.Meta[metaProtocolVersion]
	if !ok {
		return "", false
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	return v, true
}

// decodeParams unmarshals params into v. Absent or null params leave v at
// its zero value.
func decodeParams(params json.RawMessage, v any) error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	return json.Unmarshal(params, v)
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func allVersions() []string {
	return append(append([]string{}, modernVersions...), legacyVersions...)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
