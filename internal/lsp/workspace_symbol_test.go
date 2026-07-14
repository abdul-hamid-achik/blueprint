package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func symbolNames(symbols []workspaceSymbol) []string {
	names := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		names = append(names, symbol.Name)
	}
	return names
}

func symbolByName(t *testing.T, symbols []workspaceSymbol, name string) workspaceSymbol {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol
		}
	}
	t.Fatalf("symbol %q missing from %v", name, symbolNames(symbols))
	return workspaceSymbol{}
}

func TestDocumentWorkspaceSymbols_CoversDeclarationsAndMembers(t *testing.T) {
	const uri = "file:///tmp/symbols.bp"
	source := `blueprint "symbols" {
  version "0.1.0"
  port 3000
  runtime node
}
secret API_TOKEN required
env API_ORIGIN "https://example.com"
alias Email = string
enum Role {
  admin
  member
}
type Profile {
  bio string optional
}
model user {
  id uuid primary
  email Email required
}
fn validate_email {
  <- value Email
  -> bool
  impl node { module: "./validate-email" }
}
pipe normalize {
  <- value string
  -> value
}
middleware auth {
  before { |> log "auth" }
}
GET /users {
  -> 200 []
}
worker sync_users {
  trigger queue("sync")
  |> log "sync"
}
schedule cleanup {
  cron "0 0 * * *"
  |> log "cleanup"
}
external "billing" { url: "https://billing.example.com" }
subscribe "user.created" { |> log "created" }
fixture "users" from "testdata/users.json"
`
	idx := buildIndex(uri, source)
	if len(idx.errors) != 0 {
		t.Fatalf("workspace symbol fixture must parse cleanly: %+v", idx.errors)
	}
	symbols := documentWorkspaceSymbols(uri, source, idx.file, "")
	for _, name := range []string{
		"symbols", "API_TOKEN", "API_ORIGIN", "Email", "Role", "admin", "member",
		"Profile", "bio", "user", "id", "email", "validate_email", "normalize",
		"auth", "GET /users", "sync_users", "cleanup", "billing", "user.created", "users",
	} {
		symbolByName(t, symbols, name)
	}
	if got := symbolByName(t, symbols, "email"); got.Kind != symbolKindField || got.ContainerName != "user" {
		t.Fatalf("bad field symbol: %+v", got)
	}
	if got := symbolByName(t, symbols, "GET /users"); got.Kind != symbolKindMethod || got.ContainerName != "endpoints" {
		t.Fatalf("bad endpoint symbol: %+v", got)
	}

	// Every range should select its advertised name exactly in this ASCII
	// fixture. This catches the common mistake of highlighting the declaration
	// keyword because lexer.Loc points at the start of the declaration.
	lines := strings.Split(source, "\n")
	for _, symbol := range symbols {
		start := symbol.Location.Range.Start
		end := symbol.Location.Range.End
		if start.Line != end.Line || start.Line < 0 || start.Line >= len(lines) {
			t.Fatalf("invalid range for %q: %+v", symbol.Name, symbol.Location.Range)
		}
		line := lines[start.Line]
		if start.Character < 0 || end.Character > len(line) || start.Character >= end.Character {
			t.Fatalf("out-of-bounds range for %q: %+v in %q", symbol.Name, symbol.Location.Range, line)
		}
		if selected := line[start.Character:end.Character]; selected != symbol.Name {
			t.Fatalf("range for %q selected %q (%+v)", symbol.Name, selected, symbol.Location.Range)
		}
	}
}

func TestDocumentWorkspaceSymbols_QueryMatchesNameAndContainer(t *testing.T) {
	source := `blueprint "demo" {}
model account {
  id uuid primary
  display_name string required
}
fn validate_email { -> bool impl node { module: "./validate" } }
`
	idx := buildIndex("file:///tmp/query.bp", source)
	if len(idx.errors) != 0 {
		t.Fatalf("query fixture must parse cleanly: %+v", idx.errors)
	}
	byName := documentWorkspaceSymbols("file:///tmp/query.bp", source, idx.file, "validate")
	if got := symbolNames(byName); len(got) != 1 || got[0] != "validate_email" {
		t.Fatalf("unexpected name query result: %v", got)
	}
	byContainer := documentWorkspaceSymbols("file:///tmp/query.bp", source, idx.file, "account")
	for _, name := range []string{"account", "display_name", "id"} {
		symbolByName(t, byContainer, name)
	}
}

func TestWorkspaceSymbols_ScanDiskOpenDocumentWinsAndIgnoredDirsStayHidden(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace with spaces")
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	diskPath := filepath.Join(root, "service.bp")
	if err := os.WriteFile(diskPath, []byte("blueprint \"disk\" {}\nmodel disk_user { id uuid primary }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "other.bp"), []byte("model other_user { id uuid primary }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "ignored", "hidden.bp"), []byte("model hidden_user { id uuid primary }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-.bp files are not parsed even if their contents happen to be valid.
	if err := os.WriteFile(filepath.Join(root, "not-blueprint.txt"), []byte("model text_user { id uuid primary }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rootURI := filenameToURI(root)
	docURI := filenameToURI(diskPath)
	unsaved := "blueprint \"live\" {}\nmodel live_user { id uuid primary }\n"
	initMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "initialize", Params: mustMarshal(map[string]any{
		"workspaceFolders": []map[string]any{{"uri": rootURI, "name": "test"}},
	})}
	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]any{
		"textDocument": map[string]any{"uri": docURI, "version": 2, "languageId": "blueprint", "text": unsaved},
	})}
	symbolMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(2), Method: "workspace/symbol", Params: mustMarshal(map[string]any{"query": "user"})}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}
	var input strings.Builder
	for _, msg := range []jsonRPCMessage{initMsg, openMsg, symbolMsg, exitMsg} {
		body, _ := json.Marshal(msg)
		input.WriteString(framed(string(body)))
	}
	var output bytes.Buffer
	if err := NewServer(strings.NewReader(input.String()), &output).Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	response := findResponse(decodeFramed(t, output.Bytes()), 2)
	if response == nil || response.Error != nil {
		t.Fatalf("bad workspace symbol response: %+v", response)
	}
	var symbols []workspaceSymbol
	if err := json.Unmarshal(response.Result, &symbols); err != nil {
		t.Fatalf("decode symbols: %v", err)
	}
	names := strings.Join(symbolNames(symbols), ",")
	if !strings.Contains(names, "live_user") || !strings.Contains(names, "other_user") {
		t.Fatalf("expected open + nested workspace symbols, got %v", symbolNames(symbols))
	}
	for _, forbidden := range []string{"disk_user", "hidden_user", "text_user"} {
		if strings.Contains(names, forbidden) {
			t.Fatalf("unexpected %q in workspace symbols: %v", forbidden, symbolNames(symbols))
		}
	}
	if got := symbolByName(t, symbols, "live_user").Location.URI; got != docURI {
		t.Fatalf("expected encoded open-document URI %q, got %q", docURI, got)
	}
}

func TestWorkspaceFolders_CanBeAddedAndRemoved(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.workspaceRoots = []string{first}
	msg := jsonRPCMessage{JSONRPC: "2.0", Method: "workspace/didChangeWorkspaceFolders", Params: mustMarshal(map[string]any{
		"event": map[string]any{
			"removed": []map[string]any{{"uri": filenameToURI(first)}},
			"added":   []map[string]any{{"uri": filenameToURI(second)}},
		},
	})}
	if err := s.handleMessage(&msg); err != nil {
		t.Fatalf("change workspace folders: %v", err)
	}
	if len(s.workspaceRoots) != 1 || s.workspaceRoots[0] != second {
		t.Fatalf("unexpected roots: %v", s.workspaceRoots)
	}
}

func TestWorkspaceRootParsing_RejectsRemoteAndNormalizesFileURI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "space here")
	uri := filenameToURI(root)
	got, ok := localWorkspaceRoot(uri)
	if !ok || got != root {
		t.Fatalf("localWorkspaceRoot(%q) = %q, %v; want %q, true", uri, got, ok, root)
	}
	if _, ok := localWorkspaceRoot("https://example.com/project"); ok {
		t.Fatal("remote workspace URI must not be scanned as a local path")
	}
	if _, ok := localWorkspaceRoot("file://remote-host/project"); ok {
		t.Fatal("remote file host must not be scanned")
	}
}

func TestServer_WorkspaceSymbolMalformedParamsReturnsInvalidParams(t *testing.T) {
	var output bytes.Buffer
	s := NewServer(strings.NewReader(""), &output)
	msg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(7), Method: "workspace/symbol"}
	if err := s.handleMessage(&msg); err != nil {
		t.Fatalf("handle message: %v", err)
	}
	responses := decodeFramed(t, output.Bytes())
	if len(responses) != 1 || responses[0].Error == nil || responses[0].Error.Code != -32602 {
		t.Fatalf("expected -32602 response, got %+v", responses)
	}
}
