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
	reader  *bufio.Reader
	writer  io.Writer
	docs    map[string]*Document
	running bool
}

// Document represents an open text document.
type Document struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
	Text    string `json:"text"`
	LanguageID string `json:"languageId"`
}

// NewServer creates a new LSP server.
func NewServer(r io.Reader, w io.Writer) *Server {
	return &Server{
		reader: bufio.NewReader(r),
		writer: w,
		docs:   make(map[string]*Document),
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
		ProcessID int `json:"processId"`
		RootURI   string `json:"rootUri"`
		Capabilities struct {
			TextDocument struct {
				Synchronization struct {
					DynamicRegistration bool `json:"dynamicRegistration"`
					WillSave            bool `json:"willSave"`
					WillSaveWaitUntil   bool `json:"willSaveWaitUntil"`
					DidSave             bool `json:"didSave"`
				} `json:"synchronization"`
				Hover struct {
					DynamicRegistration bool `json:"dynamicRegistration"`
					ContentFormat       []string `json:"contentFormat"`
				} `json:"hover"`
			} `json:"textDocument"`
		} `json:"capabilities"`
	}
	
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}

	result := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"textDocumentSync": 1, // Full document sync
			"hoverProvider":    true,
		},
		"serverInfo": map[string]string{
			"name":    "blueprint-lsp",
			"version": "0.1.0",
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
		TextDocument   struct {
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
	if len(params.ContentChanges) > 0 {
		doc.Text = params.ContentChanges[0].Text
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
	
	result := map[string]interface{}{
		"contents": map[string]string{
			"kind":  "markdown",
			"value": content,
		},
	}

	return s.sendResult(msg.ID, result)
}

// TextDocumentPositionParams contains position information.
type TextDocumentPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position     struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

// getHoverContent returns hover information for a position.
func (s *Server) getHoverContent(uri string, line, char int) string {
	doc, ok := s.docs[uri]
	if !ok {
		return ""
	}

	lines := strings.Split(doc.Text, "\n")
	if line >= len(lines) {
		return ""
	}

	textLine := lines[line]
	if char >= len(textLine) {
		return ""
	}

	// Simple keyword detection
	word := extractWordAt(textLine, char)
	return getKeywordDocumentation(word)
}

// extractWordAt extracts the word at the given position.
func extractWordAt(line string, pos int) string {
	start := pos
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	end := pos
	for end < len(line) && isWordChar(line[end]) {
		end++
	}
	return line[start:end]
}

func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// getKeywordDocumentation returns documentation for Blueprint keywords.
func getKeywordDocumentation(word string) string {
	docs := map[string]string{
		"blueprint": "**blueprint** - Declares the service name and configuration\n\n```bp\nblueprint \"my-api\" {\n  version \"1.0.0\"\n  port 3000\n}\n```",
		"model": "**model** - Defines a database table\n\n```bp\nmodel user {\n  id   uuid  primary\n  name string required\n}\n```",
		"fn": "**fn** - Declares a function\n\n```bp\nfn process {\n  <- input string\n  -> output string\n}\n```",
		"pipe": "**pipe** - Declares a reusable pipeline\n\n```bp\npipe validate {\n  <- input string\n  |> guard input != \"\" -> 400 \"Required\"\n  -> input\n}\n```",
		"middleware": "**middleware** - Declares reusable middleware\n\n```bp\nmiddleware auth {\n  before { |> inject user }\n}\n```",
		"guard": "**guard** - Early return if condition fails\n\n```bp\n|> guard user.active -> 403 \"Forbidden\"\n```",
		"when": "**when** - Conditional execution\n\n```bp\n|> when plan == \"pro\": limit = 1000\n```",
		"try": "**try** - Error handling block\n\n```bp\n|> try {\n  |> risky_operation()\n} recover {\n  |> log error\n}\n```",
	}

	if doc, ok := docs[word]; ok {
		return doc
	}
	return ""
}

// publishDiagnostics sends diagnostics for a document.
func (s *Server) publishDiagnostics(uri string) error {
	// TODO: Actually parse and validate the document
	// For now, send empty diagnostics
	return s.sendNotification("textDocument/publishDiagnostics", map[string]interface{}{
		"uri":         uri,
		"diagnostics": []interface{}{},
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
