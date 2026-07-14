// Package lsp implements a Language Server Protocol server for Blueprint.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Server implements the LSP protocol for Blueprint.
type Server struct {
	reader         *bufio.Reader
	writer         io.Writer
	docs           map[string]*Document
	indexes        map[string]*docIndex // parsed AST cache keyed by uri
	workspaceRoots []string             // absolute local paths, in deterministic order
	running        bool
}

// Document represents an open text document.
type Document struct {
	URI        string `json:"uri"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
	LanguageID string `json:"languageId"`
}

// NewServer creates a new LSP server.
func NewServer(r io.Reader, w io.Writer) *Server {
	return &Server{
		reader:  bufio.NewReader(r),
		writer:  w,
		docs:    make(map[string]*Document),
		indexes: make(map[string]*docIndex),
	}
}

// Run starts the LSP server and processes messages.
func (s *Server) Run() error {
	s.running = true
	for s.running {
		if err := s.processMessage(); err != nil {
			if err == io.EOF {
				return nil
			}
			// Log error but continue processing
			fmt.Fprintf(os.Stderr, "LSP error: %v\n", err)
		}
	}
	return nil
}

// processMessage reads and handles a single LSP message.
func (s *Server) processMessage() error {
	// Read headers
	contentLength := 0
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break // End of headers
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			lengthStr := strings.TrimPrefix(line, "Content-Length: ")
			contentLength, _ = strconv.Atoi(lengthStr)
		}
	}

	if contentLength == 0 {
		return nil
	}

	// Read content
	content := make([]byte, contentLength)
	_, err := io.ReadFull(s.reader, content)
	if err != nil {
		return err
	}

	// Parse JSON-RPC message
	var msg jsonRPCMessage
	if err := json.Unmarshal(content, &msg); err != nil {
		return s.sendError(msg.ID, -32700, "Parse error", err.Error())
	}

	// Handle message
	return s.handleMessage(&msg)
}

// jsonRPCMessage represents a JSON-RPC 2.0 message.
type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError represents a JSON-RPC error.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// handleMessage dispatches to the appropriate handler.
func (s *Server) handleMessage(msg *jsonRPCMessage) error {
	switch msg.Method {
	case "initialize":
		return s.handleInitialize(msg)
	case "initialized":
		return nil // No response needed
	case "shutdown":
		s.running = false
		return s.sendResult(msg.ID, nil)
	case "exit":
		s.running = false
		return nil
	case "textDocument/didOpen":
		return s.handleDidOpen(msg)
	case "textDocument/didChange":
		return s.handleDidChange(msg)
	case "textDocument/didClose":
		return s.handleDidClose(msg)
	case "textDocument/hover":
		return s.handleHover(msg)
	case "textDocument/definition":
		return s.handleDefinition(msg)
	case "textDocument/completion":
		return s.handleCompletion(msg)
	case "workspace/symbol":
		return s.handleWorkspaceSymbol(msg)
	case "workspace/didChangeWorkspaceFolders":
		return s.handleDidChangeWorkspaceFolders(msg)
	default:
		// Method not found
		if msg.ID != nil {
			return s.sendError(msg.ID, -32601, "Method not found", msg.Method)
		}
		return nil
	}
}

// handleInitialize handles the initialize request.
func (s *Server) handleInitialize(msg *jsonRPCMessage) error {
	var params struct {
		ProcessID        int    `json:"processId"`
		RootURI          string `json:"rootUri"`
		RootPath         string `json:"rootPath"`
		WorkspaceFolders []struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"workspaceFolders"`
		Capabilities struct {
			TextDocument struct {
				Synchronization struct {
					DynamicRegistration bool `json:"dynamicRegistration"`
					WillSave            bool `json:"willSave"`
					WillSaveWaitUntil   bool `json:"willSaveWaitUntil"`
					DidSave             bool `json:"didSave"`
				} `json:"synchronization"`
				Hover struct {
					DynamicRegistration bool     `json:"dynamicRegistration"`
					ContentFormat       []string `json:"contentFormat"`
				} `json:"hover"`
			} `json:"textDocument"`
		} `json:"capabilities"`
	}

	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}

	roots := make([]string, 0, len(params.WorkspaceFolders)+1)
	for _, folder := range params.WorkspaceFolders {
		if root, ok := localWorkspaceRoot(folder.URI); ok {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		if root, ok := localWorkspaceRoot(params.RootURI); ok {
			roots = append(roots, root)
		} else if params.RootPath != "" {
			if root, ok := localWorkspaceRoot(params.RootPath); ok {
				roots = append(roots, root)
			}
		}
	}
	s.workspaceRoots = normalizeWorkspaceRoots(roots)

	result := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"textDocumentSync":        1, // Full document sync
			"hoverProvider":           true,
			"definitionProvider":      true,
			"workspaceSymbolProvider": true,
			"completionProvider": map[string]interface{}{
				"resolveProvider":   false,
				"triggerCharacters": []string{"."},
			},
			"workspace": map[string]interface{}{
				"workspaceFolders": map[string]interface{}{
					"supported":           true,
					"changeNotifications": true,
				},
			},
		},
		"serverInfo": map[string]string{
			"name":    "blueprint-lsp",
			"version": "0.2.0",
		},
	}

	return s.sendResult(msg.ID, result)
}

// handleDidOpen handles textDocument/didOpen notification.
func (s *Server) handleDidOpen(msg *jsonRPCMessage) error {
	var params struct {
		TextDocument Document `json:"textDocument"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return err
	}

	s.docs[params.TextDocument.URI] = &params.TextDocument

	// TODO: Validate document and publish diagnostics
	return s.publishDiagnostics(params.TextDocument.URI)
}

// handleDidChange handles textDocument/didChange notification.
func (s *Server) handleDidChange(msg *jsonRPCMessage) error {
	var params struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return err
	}

	doc, ok := s.docs[params.TextDocument.URI]
	if !ok {
		return nil
	}

	doc.Version = params.TextDocument.Version
	// The server advertises textDocumentSync = Full (1) in handleInitialize, so
	// every entry in ContentChanges carries the entire new document text rather
	// than a range-based edit. A single didChange notification may legally batch
	// multiple content changes; per the LSP spec they must be applied in order,
	// which — for full-document sync — means the last entry wins. Only applying
	// ContentChanges[0] silently dropped every edit after the first.
	for _, change := range params.ContentChanges {
		doc.Text = change.Text
	}

	// TODO: Validate document and publish diagnostics
	return s.publishDiagnostics(params.TextDocument.URI)
}

// handleDidClose handles textDocument/didClose notification.
func (s *Server) handleDidClose(msg *jsonRPCMessage) error {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return err
	}

	delete(s.docs, params.TextDocument.URI)
	delete(s.indexes, params.TextDocument.URI)

	// Clear diagnostics
	return s.sendNotification("textDocument/publishDiagnostics", map[string]interface{}{
		"uri":         params.TextDocument.URI,
		"diagnostics": []interface{}{},
	})
}

// handleHover handles textDocument/hover request.
func (s *Server) handleHover(msg *jsonRPCMessage) error {
	var params struct {
		TextDocumentPositionParams
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}

	content := s.getHoverContent(params.TextDocument.URI, params.Position.Line, params.Position.Character)
	if content == "" {
		// Per LSP spec, returning null tells the client "no hover info" so it
		// doesn't render an empty popup.
		return s.sendResult(msg.ID, nil)
	}
	result := map[string]interface{}{
		"contents": map[string]string{
			"kind":  "markdown",
			"value": content,
		},
	}

	return s.sendResult(msg.ID, result)
}

// handleDefinition handles textDocument/definition request.
func (s *Server) handleDefinition(msg *jsonRPCMessage) error {
	var params struct {
		TextDocumentPositionParams
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}
	uri := params.TextDocument.URI
	idx := s.getIndex(uri)
	loc := computeDefinition(uri, idx, params.Position.Line, params.Position.Character)
	if loc == nil {
		// LSP allows null for "no definition found".
		return s.sendResult(msg.ID, nil)
	}
	return s.sendResult(msg.ID, loc)
}

// TextDocumentPositionParams contains position information.
type TextDocumentPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

// getHoverContent returns hover markdown for (line, char). Empty string means
// no info available; the caller is responsible for translating that into a null
// LSP response.
func (s *Server) getHoverContent(uri string, line, char int) string {
	idx := s.getIndex(uri)
	if idx == nil {
		return ""
	}
	return computeHover(idx, line, char)
}

// getIndex returns the cached parse for uri, building it on demand.
func (s *Server) getIndex(uri string) *docIndex {
	doc, ok := s.docs[uri]
	if !ok {
		return nil
	}
	if idx, ok := s.indexes[uri]; ok && idx.text == doc.Text {
		return idx
	}
	idx := buildIndex(uri, doc.Text)
	s.indexes[uri] = idx
	return idx
}

// publishDiagnostics parses + checks the doc and pushes a
// textDocument/publishDiagnostics notification with one entry per parse/check
// error. An empty diagnostics list clears stale problems in the editor.
func (s *Server) publishDiagnostics(uri string) error {
	doc, ok := s.docs[uri]
	if !ok {
		return s.sendNotification("textDocument/publishDiagnostics", map[string]interface{}{
			"uri":         uri,
			"diagnostics": []interface{}{},
		})
	}
	diags := computeDiagnostics(uri, doc.Text)
	// Refresh the AST cache here so hover/definition see the same parse the
	// editor already got diagnostics for.
	s.indexes[uri] = buildIndex(uri, doc.Text)
	return s.sendNotification("textDocument/publishDiagnostics", map[string]interface{}{
		"uri":         uri,
		"diagnostics": diags,
	})
}

// sendResult sends a successful result response.
func (s *Server) sendResult(id *int, result any) error {
	msg := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  mustMarshal(result),
	}
	return s.sendMessage(&msg)
}

// sendError sends an error response.
func (s *Server) sendError(id *int, code int, message string, data any) error {
	msg := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	return s.sendMessage(&msg)
}

// sendNotification sends a notification (no response expected).
func (s *Server) sendNotification(method string, params any) error {
	msg := jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  mustMarshal(params),
	}
	return s.sendMessage(&msg)
}

// sendMessage sends a JSON-RPC message.
func (s *Server) sendMessage(msg *jsonRPCMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := s.writer.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := s.writer.Write(data); err != nil {
		return err
	}
	return nil
}

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func jsonUnmarshalParams(data json.RawMessage, dst any) error {
	if len(data) == 0 {
		return fmt.Errorf("missing params")
	}
	return json.Unmarshal(data, dst)
}
