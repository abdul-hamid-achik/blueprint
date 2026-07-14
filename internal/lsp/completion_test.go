package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const completionCursor = "<|cursor|>"

func completionsAt(t *testing.T, marked string) []completionItem {
	t.Helper()
	at := strings.Index(marked, completionCursor)
	if at < 0 || strings.LastIndex(marked, completionCursor) != at {
		t.Fatalf("source must contain exactly one %q marker", completionCursor)
	}
	before := marked[:at]
	line := strings.Count(before, "\n")
	lineStart := strings.LastIndex(before, "\n") + 1
	character := utf16Length(before[lineStart:])
	source := strings.Replace(marked, completionCursor, "", 1)
	return computeCompletions(buildIndex("file:///tmp/completion.bp", source), line, character)
}

func completionLabels(items []completionItem) []string {
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	return labels
}

func hasCompletion(items []completionItem, label string) bool {
	for _, item := range items {
		if item.Label == label {
			return true
		}
	}
	return false
}

func completionByLabel(t *testing.T, items []completionItem, label string) completionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("completion %q missing from %v", label, completionLabels(items))
	return completionItem{}
}

func TestComputeCompletions_TopLevelUsesSnippetsAndPrefix(t *testing.T) {
	items := completionsAt(t, `blueprint "demo" {
  version "0.1.0"
  port 3000
  runtime node
}

mo<|cursor|>`)
	if got := completionLabels(items); len(got) != 1 || got[0] != "model" {
		t.Fatalf("expected prefix-filtered model completion, got %v", got)
	}
	model := items[0]
	if model.InsertTextFormat != 2 || !strings.Contains(model.InsertText, "model ${1:name}") {
		t.Fatalf("expected a model snippet, got %+v", model)
	}
	if hasCompletion(items, "blueprint") {
		t.Fatal("must not offer a duplicate blueprint block")
	}
}

func TestComputeCompletions_CommentsAndStringsAreSilent(t *testing.T) {
	comment := completionsAt(t, `blueprint "demo" {}
# model <|cursor|>`)
	if len(comment) != 0 {
		t.Fatalf("expected no completion in comment, got %v", completionLabels(comment))
	}
	stringValue := completionsAt(t, `blueprint "demo" {}
env MESSAGE "model <|cursor|>"
`)
	if len(stringValue) != 0 {
		t.Fatalf("expected no completion in string, got %v", completionLabels(stringValue))
	}
}

func TestComputeCompletions_FieldTypeAndConstraintContexts(t *testing.T) {
	typeItems := completionsAt(t, `blueprint "demo" {}
alias Email = string
model user {
  email Em<|cursor|>
}
`)
	if got := completionLabels(typeItems); len(got) != 1 || got[0] != "Email" {
		t.Fatalf("expected declared type completion, got %v", got)
	}

	constraintItems := completionsAt(t, `blueprint "demo" {}
model user {
  email string req<|cursor|>
}
`)
	if got := completionLabels(constraintItems); len(got) != 1 || got[0] != "required" {
		t.Fatalf("expected required constraint completion, got %v", got)
	}
}

func TestComputeCompletions_ContentAndTypeKeepOrdinaryFieldContexts(t *testing.T) {
	for _, block := range []string{"content", "type"} {
		t.Run(block, func(t *testing.T) {
			blank := completionsAt(t, `blueprint "demo" {}
`+block+` profile {
  <|cursor|>
}
`)
			if hasCompletion(blank, "computed") {
				t.Fatalf("computed must be model-only, got %v", completionLabels(blank))
			}

			types := completionsAt(t, `blueprint "demo" {}
alias Email = string
`+block+` profile {
  email Em<|cursor|>
}
`)
			if got := completionLabels(types); len(got) != 1 || got[0] != "Email" {
				t.Fatalf("expected ordinary field type completion, got %v", got)
			}

			constraints := completionsAt(t, `blueprint "demo" {}
`+block+` profile {
  email string req<|cursor|>
}
`)
			if got := completionLabels(constraints); len(got) != 1 || got[0] != "required" {
				t.Fatalf("expected ordinary field constraint completion, got %v", got)
			}

			computedSyntax := completionsAt(t, `blueprint "demo" {}
`+block+` profile {
  computed display_name string = fir<|cursor|>
}
`)
			if len(computedSyntax) != 0 {
				t.Fatalf("computed expressions must be model-only, got %v", completionLabels(computedSyntax))
			}
		})
	}
}

func TestComputeCompletions_BlueprintSettingValues(t *testing.T) {
	items := completionsAt(t, `blueprint "demo" {
  storage s<|cursor|>
}
`)
	if got := completionLabels(items); len(got) != 1 || got[0] != "s3" {
		t.Fatalf("expected storage backend value, got %v", got)
	}
}

func TestComputeCompletions_UsesDeclarationKindContext(t *testing.T) {
	items := completionsAt(t, `blueprint "demo" {}
middleware require_auth {
  before {
    |> log "auth"
  }
}
GET /users {
  use req<|cursor|>
}
`)
	if got := completionLabels(items); len(got) != 1 || got[0] != "require_auth" {
		t.Fatalf("expected only matching middleware, got %v", got)
	}
	if completionByLabel(t, items, "require_auth").Detail != "middleware" {
		t.Fatalf("expected middleware detail, got %+v", items[0])
	}
}

func TestComputeCompletions_ModelOperationAndReferenceContexts(t *testing.T) {
	source := `blueprint "demo" {}
model user {
  id uuid primary
}
model team {
  id uuid primary
}
GET /users {
  |> row = fetch us<|cursor|>
}
`
	items := completionsAt(t, source)
	if got := completionLabels(items); len(got) != 1 || got[0] != "user" {
		t.Fatalf("expected model argument completion, got %v", got)
	}

	refItems := completionsAt(t, `blueprint "demo" {}
model user { id uuid primary }
model post {
  user_id uuid ref(us<|cursor|>)
}
`)
	if got := completionLabels(refItems); len(got) != 1 || got[0] != "user" {
		t.Fatalf("expected model completion inside ref(), got %v", got)
	}
}

func TestComputeCompletions_ExternalCallUsesIdentifierForm(t *testing.T) {
	items := completionsAt(t, `blueprint "demo" {}
external "auth-service" { url: "https://auth.example.com" }
GET /me {
  |> user = call auth_<|cursor|>
}
`)
	if got := completionLabels(items); len(got) != 1 || got[0] != "auth_service" {
		t.Fatalf("expected parser-safe external reference, got %v", got)
	}
	item := completionByLabel(t, items, "auth_service")
	if item.InsertText != "auth_service" || !strings.Contains(item.Detail, "auth-service") {
		t.Fatalf("unexpected external completion: %+v", item)
	}
}

func TestComputeCompletions_InfersFieldsForInputsAndRowBindings(t *testing.T) {
	source := `blueprint "demo" {}
model user {
  id uuid primary
  name string required
  nickname string optional
}
GET /users/:id {
  <- id uuid required
  <- account user required
  |> fetched = fetch user(id)
  |> log fetched.na<|cursor|>
}
`
	items := completionsAt(t, source)
	if got := completionLabels(items); len(got) != 1 || got[0] != "name" {
		t.Fatalf("expected inferred user.name field, got %v", got)
	}
	if detail := items[0].Detail; !strings.Contains(detail, "user field: string") {
		t.Fatalf("expected field type detail, got %q", detail)
	}

	inputItems := completionsAt(t, strings.Replace(source, "fetched.na"+completionCursor, "account.nick"+completionCursor, 1))
	if got := completionLabels(inputItems); len(got) != 1 || got[0] != "nickname" {
		t.Fatalf("expected named-model input field completion, got %v", got)
	}
}

func TestComputeCompletions_EnvironmentAndSecretMembers(t *testing.T) {
	items := completionsAt(t, `blueprint "demo" {}
secret API_TOKEN required
env API_ORIGIN "https://example.com"
GET /health {
  |> log env.API_<|cursor|>
}
`)
	if got := completionLabels(items); strings.Join(got, ",") != "API_ORIGIN,API_TOKEN" {
		t.Fatalf("expected deterministic env + secret members, got %v", got)
	}
}

func TestComputeCompletions_ScopedValuesAndUnicodePosition(t *testing.T) {
	items := completionsAt(t, `blueprint "demo" {}
model user { id uuid primary }
GET /users {
  <- user_id uuid required
  |> fetched = fetch user(user_id)
  |> label = "😀" + fet<|cursor|>
}
`)
	if got := completionLabels(items); len(got) != 1 || got[0] != "fetched" {
		t.Fatalf("expected scoped binding after UTF-16 surrogate pair, got %v", got)
	}
}

func TestComputeCompletions_IsDeterministicAndUnique(t *testing.T) {
	marked := `blueprint "demo" {}
fn log { -> string impl node { module: "./log" } }
GET /health {
  |> <|cursor|>
}
`
	first := completionsAt(t, marked)
	second := completionsAt(t, marked)
	one, _ := json.Marshal(first)
	two, _ := json.Marshal(second)
	if !bytes.Equal(one, two) {
		t.Fatalf("completion order is not deterministic:\n%s\n%s", one, two)
	}
	seen := map[string]bool{}
	for _, item := range first {
		if seen[item.Label] {
			t.Fatalf("duplicate completion label %q in %v", item.Label, completionLabels(first))
		}
		seen[item.Label] = true
	}
}

func TestServer_CompletionRoundTripAndCapabilities(t *testing.T) {
	const uri = "file:///tmp/completion-roundtrip.bp"
	text := `blueprint "demo" {}
model user { id uuid primary }
GET /users {
  |> row = fetch us
}
`
	initMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(1), Method: "initialize", Params: mustMarshal(map[string]any{"processId": 1})}
	openMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": 1, "languageId": "blueprint", "text": text},
	})}
	line, col := indexOf(text, "fetch us")
	completionMsg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(2), Method: "textDocument/completion", Params: mustMarshal(map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": col + len("fetch us")},
	})}
	exitMsg := jsonRPCMessage{JSONRPC: "2.0", Method: "exit"}

	var input strings.Builder
	for _, msg := range []jsonRPCMessage{initMsg, openMsg, completionMsg, exitMsg} {
		body, _ := json.Marshal(msg)
		input.WriteString(framed(string(body)))
	}
	var output bytes.Buffer
	if err := NewServer(strings.NewReader(input.String()), &output).Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := decodeFramed(t, output.Bytes())
	initResponse := findResponse(msgs, 1)
	if initResponse == nil {
		t.Fatal("missing initialize response")
	}
	var initialized struct {
		Capabilities struct {
			CompletionProvider      map[string]any `json:"completionProvider"`
			WorkspaceSymbolProvider bool           `json:"workspaceSymbolProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(initResponse.Result, &initialized); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if initialized.Capabilities.CompletionProvider == nil || !initialized.Capabilities.WorkspaceSymbolProvider {
		t.Fatalf("missing completion/workspace symbol capabilities: %+v", initialized.Capabilities)
	}
	response := findResponse(msgs, 2)
	if response == nil || response.Error != nil {
		t.Fatalf("bad completion response: %+v", response)
	}
	var items []completionItem
	if err := json.Unmarshal(response.Result, &items); err != nil {
		t.Fatalf("decode completion result: %v", err)
	}
	if got := completionLabels(items); len(got) != 1 || got[0] != "user" {
		t.Fatalf("unexpected completion response %v", got)
	}
}

func TestServer_CompletionForClosedDocumentReturnsEmptyArray(t *testing.T) {
	var out bytes.Buffer
	s := NewServer(strings.NewReader(""), &out)
	msg := jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(9), Method: "textDocument/completion", Params: mustMarshal(map[string]any{
		"textDocument": map[string]any{"uri": "file:///tmp/not-open.bp"},
		"position":     map[string]any{"line": 0, "character": 0},
	})}
	if err := s.handleMessage(&msg); err != nil {
		t.Fatalf("handle completion: %v", err)
	}
	responses := decodeFramed(t, out.Bytes())
	if len(responses) != 1 || string(responses[0].Result) != "[]" {
		t.Fatalf("expected empty array, got %+v", responses)
	}
}

func TestComputeCompletions_ComputedFieldSyntax(t *testing.T) {
	blank := completionsAt(t, `blueprint "demo" {}
model user {
  <|cursor|>
}
`)
	computed := completionByLabel(t, blank, "computed")
	if computed.InsertText != "computed ${1:name} ${2:string} = ${3:expression}" {
		t.Fatalf("unexpected computed snippet: %+v", computed)
	}
	afterName := completionsAt(t, `blueprint "demo" {}
model user {
  computed display_name <|cursor|>
}
`)
	if !hasCompletion(afterName, "string") || hasCompletion(afterName, "required") {
		t.Fatalf("computed name must enter a type context, got %v", completionLabels(afterName))
	}

	types := completionsAt(t, `blueprint "demo" {}
model user {
  name string required
  computed display_name str<|cursor|>
}
`)
	if got := completionLabels(types); len(got) != 1 || got[0] != "string" {
		t.Fatalf("expected computed result type, got %v", got)
	}

	equals := completionsAt(t, `blueprint "demo" {}
model user {
  name string required
  computed display_name string <|cursor|>
}
`)
	if got := completionLabels(equals); len(got) != 1 || got[0] != "=" {
		t.Fatalf("expected computed expression introducer, got %v", got)
	}

	expression := completionsAt(t, `blueprint "demo" {}
model user {
  first_name string required
  computed shout string = first_name
  computed display_name string = fir<|cursor|>
}
`)
	if got := completionLabels(expression); len(got) != 1 || got[0] != "first_name" {
		t.Fatalf("expected persisted field in computed expression, got %v", got)
	}
}

func TestComputeCompletions_QueryWithRefBackedRelationships(t *testing.T) {
	base := `blueprint "demo" {}
model person { id uuid primary }
model post {
  id uuid primary
  author_id uuid ref(person)
  editor_id uuid ref(person)
  title string required
}
GET /posts {
  |> posts = query post %s
}
`
	first := completionsAt(t, strings.Replace(base, "%s", "with(au"+completionCursor, 1))
	if got := completionLabels(first); len(got) != 1 || got[0] != "author" {
		t.Fatalf("expected author relationship, got %v", got)
	}
	if detail := first[0].Detail; detail != "relationship to person" {
		t.Fatalf("unexpected relationship detail %q", detail)
	}

	second := completionsAt(t, strings.Replace(base, "%s", "with(author, ed"+completionCursor, 1))
	if got := completionLabels(second); len(got) != 1 || got[0] != "editor" {
		t.Fatalf("expected remaining editor relationship, got %v", got)
	}

	closed := completionsAt(t, strings.Replace(base, "%s", "with(author) ed"+completionCursor, 1))
	if hasCompletion(closed, "editor") {
		t.Fatalf("closed with(...) must not remain in relationship context: %v", completionLabels(closed))
	}
}

func TestComputeCompletions_MultilineStringsSuppressAndDoNotCorruptContext(t *testing.T) {
	inside := completionsAt(t, `blueprint "demo" {}
env MESSAGE "first line
model fake { comp<|cursor|>
still a string"
`)
	if len(inside) != 0 {
		t.Fatalf("expected no completion inside multiline string, got %v", completionLabels(inside))
	}

	after := completionsAt(t, `blueprint "demo" {}
env MESSAGE "first line
# braces and comments are string data: model fake {"
mo<|cursor|>
`)
	if got := completionLabels(after); len(got) != 1 || got[0] != "model" {
		t.Fatalf("brace in multiline string corrupted top-level context: %v", got)
	}
}
