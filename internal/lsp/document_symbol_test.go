package lsp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// everyKindBlueprint declares one example of every top-level kind the AST has,
// plus fields, computed fields, and enum variants as children.
const everyKindBlueprint = `blueprint "symbols" {
  version "0.1.0"
  port 3000
  runtime node
}
secret API_TOKEN required
env API_ORIGIN "https://example.com"
locale "en" default
translation greetings {
  key "hello"
  locale "en" {
    "hello": "Hello"
  }
}
state order_status {
  pending -> paid
  paid -> shipped
}
analytics product_events {
  event product_viewed
  sink http("https://example.com/events")
}
save game_save {
  model player
  version_field version
  latest 1
}
include "includes/models.bp"
alias Email = string
enum Role {
  admin
  member { level: 2 }
}
type Profile {
  bio string optional
}
content article {
  title string required
}
model user {
  id uuid primary
  email Email required
  computed full_name string = first + last
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
GET /users/:id {
  -> 200 []
}
STREAM /api/rooms/:id/live {
  <- id uuid required
  stream {
    |> on event(new_message) where(room_id == id) {
      -> { body: event.body }
    }
  }
}
WS /ws/rooms/:id {
  on_message {
    -> { ok: true }
  }
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
test create_user {
  target POST /api/users
  request { body { name: "x" } }
  expect { status 200 }
}
test_group users_suite {
  shared_setup { |> log "s" }
  tests [create_user]
}
fixture "users" from "testdata/users.json"
`

// findDocumentSymbol searches the outline depth-first for name.
func findDocumentSymbol(symbols []documentSymbol, name string) (documentSymbol, bool) {
	for _, s := range symbols {
		if s.Name == name {
			return s, true
		}
		if child, ok := findDocumentSymbol(s.Children, name); ok {
			return child, true
		}
	}
	return documentSymbol{}, false
}

func TestDocumentSymbols_CoversEveryDeclarationKind(t *testing.T) {
	idx := buildIndex("file:///tmp/every.bp", everyKindBlueprint)
	if len(idx.errors) != 0 {
		t.Fatalf("fixture must parse cleanly: %+v", idx.errors)
	}
	symbols := documentSymbols(everyKindBlueprint, idx.file)

	want := map[string]int{
		"symbols":                    symbolKindModule,
		"API_TOKEN":                  symbolKindVariable,
		"API_ORIGIN":                 symbolKindVariable,
		"en":                         symbolKindString,
		"greetings":                  symbolKindNamespace,
		"order_status":               symbolKindEnum,
		"product_events":             symbolKindNamespace,
		"game_save":                  symbolKindStruct,
		"includes/models.bp":         symbolKindFile,
		"Email":                      symbolKindInterface,
		"Role":                       symbolKindEnum,
		"admin":                      symbolKindEnumMember,
		"member":                     symbolKindEnumMember,
		"Profile":                    symbolKindStruct,
		"bio":                        symbolKindField,
		"article":                    symbolKindClass,
		"title":                      symbolKindField,
		"user":                       symbolKindClass,
		"id":                         symbolKindField,
		"email":                      symbolKindField,
		"full_name":                  symbolKindField,
		"validate_email":             symbolKindFunction,
		"normalize":                  symbolKindFunction,
		"auth":                       symbolKindFunction,
		"GET /users/:id":             symbolKindMethod,
		"STREAM /api/rooms/:id/live": symbolKindMethod,
		"WS /ws/rooms/:id":           symbolKindMethod,
		"sync_users":                 symbolKindFunction,
		"cleanup":                    symbolKindEvent,
		"billing":                    symbolKindObject,
		"user.created":               symbolKindEvent,
		"create_user":                symbolKindMethod,
		"users_suite":                symbolKindNamespace,
		"users":                      symbolKindObject,
	}
	for name, kind := range want {
		got, ok := findDocumentSymbol(symbols, name)
		if !ok {
			t.Errorf("missing document symbol %q", name)
			continue
		}
		if got.Kind != kind {
			t.Errorf("symbol %q kind = %d, want %d", name, got.Kind, kind)
		}
	}

	// Children nest under their declaration rather than appearing at top level.
	for _, top := range symbols {
		if top.Name == "email" || top.Name == "admin" {
			t.Errorf("child %q must not appear at top level", top.Name)
		}
	}
	user, _ := findDocumentSymbol(symbols, "user")
	if len(user.Children) != 3 {
		t.Errorf("model user children = %d, want 3 (id, email, full_name)", len(user.Children))
	}
	role, _ := findDocumentSymbol(symbols, "Role")
	if len(role.Children) != 2 {
		t.Errorf("enum Role children = %d, want 2 variants", len(role.Children))
	}
}

func TestDocumentSymbols_RangesCoverDeclarationAndSelectNames(t *testing.T) {
	lines := strings.Split(everyKindBlueprint, "\n")
	idx := buildIndex("file:///tmp/every.bp", everyKindBlueprint)
	symbols := documentSymbols(everyKindBlueprint, idx.file)

	var check func(list []documentSymbol)
	check = func(list []documentSymbol) {
		for _, s := range list {
			sel := s.SelectionRange
			if sel.Start.Line != sel.End.Line {
				t.Errorf("selection for %q spans lines: %+v", s.Name, sel)
				continue
			}
			if got := lines[sel.Start.Line][sel.Start.Character:sel.End.Character]; got != s.Name {
				t.Errorf("selection for %q selected %q", s.Name, got)
			}
			if !positionLE(s.Range.Start, sel.Start) || !positionLE(sel.End, s.Range.End) {
				t.Errorf("selection %+v for %q lies outside range %+v", sel, s.Name, s.Range)
			}
			check(s.Children)
		}
	}
	check(symbols)

	// A multi-line model spans from its keyword to its closing brace.
	model, ok := findDocumentSymbol(symbols, "user")
	if !ok {
		t.Fatal("missing model user")
	}
	start := lineIndexOf(lines, "model user {")
	if model.Range.Start.Line != start || model.Range.End.Line != start+4 || model.Range.End.Character != 1 {
		t.Fatalf("model range = %+v, want lines %d..%d ending after the brace", model.Range, start, start+4)
	}
	email, _ := findDocumentSymbol(model.Children, "email")
	if email.Range.Start.Line != start+2 {
		t.Fatalf("field email range = %+v, want line %d", email.Range, start+2)
	}
}

func TestDocumentSymbols_BrokenDocumentStillListsValidDeclarations(t *testing.T) {
	text := "model good {\n  id uuid primary\n}\nmodel broken {\n  id uuid primary\n"
	idx := buildIndex("file:///tmp/broken-symbols.bp", text)
	if len(idx.errors) == 0 {
		t.Fatal("fixture must contain a parse error")
	}
	symbols := documentSymbols(text, idx.file)
	if _, ok := findDocumentSymbol(symbols, "good"); !ok {
		t.Fatalf("valid declaration missing from outline: %+v", symbols)
	}
}

func TestServer_DocumentSymbolRoundTrip(t *testing.T) {
	const uri = "file:///tmp/every-symbols.bp"
	resp := requestResponse(t, uri, everyKindBlueprint, "textDocument/documentSymbol", nil)
	var symbols []documentSymbol
	if err := json.Unmarshal(resp.Result, &symbols); err != nil {
		t.Fatalf("decode symbols: %v", err)
	}
	if _, ok := findDocumentSymbol(symbols, "GET /users/:id"); !ok {
		t.Fatalf("endpoint missing from response: %s", resp.Result)
	}
	if _, ok := findDocumentSymbol(symbols, "email"); !ok {
		t.Fatal("model field missing from response")
	}
}

func TestServer_DocumentSymbolUnknownDocumentReturnsEmptyArray(t *testing.T) {
	resp := requestUnopened(t, "file:///tmp/never-opened-symbols.bp", "textDocument/documentSymbol")
	if got := string(resp.Result); got != "[]" {
		t.Fatalf("unknown document must return [], got %s", got)
	}
}

// intentBlueprint puts an intent (`@ "..."`) before a declaration that follows
// a declaration with no closing brace.
const intentBlueprint = `env LIMIT 10
@ "The user model"
model user {
  id uuid primary
}
@ "Create a user"
GET /users {
  -> 200 []
}
`

func TestDocumentSymbols_IntentBelongsToNextDeclaration(t *testing.T) {
	idx := buildIndex("file:///tmp/intents.bp", intentBlueprint)
	if len(idx.errors) != 0 {
		t.Fatalf("fixture must parse cleanly: %+v", idx.errors)
	}
	symbols := documentSymbols(intentBlueprint, idx.file)
	if len(symbols) != 3 {
		t.Fatalf("want 3 top-level symbols, got %+v", symbols)
	}
	env := symbols[0]
	if env.Range.End.Line != 0 {
		t.Fatalf("env range %+v swallowed the next intent", env.Range)
	}
	for i := 1; i < len(symbols); i++ {
		prev, next := symbols[i-1], symbols[i]
		if !positionLE(prev.Range.End, next.Range.Start) {
			t.Fatalf("ranges of %q and %q overlap: %+v / %+v", prev.Name, next.Name, prev.Range, next.Range)
		}
	}
}

func TestDocumentSymbols_RangesDoNotOverlapOnFixtureWithIntents(t *testing.T) {
	src, err := os.ReadFile("../../testdata/valid/intent_everywhere.bp")
	if err != nil {
		t.Fatal(err)
	}
	file, errs := parseForFormat("intent_everywhere.bp", src)
	if len(errs) != 0 {
		t.Fatalf("fixture must parse cleanly: %+v", errs)
	}
	symbols := documentSymbols(string(src), file)
	if _, ok := findDocumentSymbol(symbols, "test-intents"); !ok {
		t.Fatalf("blueprint block behind a file-level intent missing from outline: %+v", symbols)
	}
	if len(symbols) != 4 {
		t.Fatalf("want blueprint, user, validate_name, and POST /api/users; got %+v", symbols)
	}
	for i := 1; i < len(symbols); i++ {
		prev, next := symbols[i-1], symbols[i]
		if !positionLE(prev.Range.End, next.Range.Start) {
			t.Fatalf("ranges of %q and %q overlap: %+v / %+v", prev.Name, next.Name, prev.Range, next.Range)
		}
	}
}

// positionLE reports whether a is at or before b.
func positionLE(a, b lspPosition) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Character <= b.Character
}

// lineIndexOf returns the index of the first line containing needle.
func lineIndexOf(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}
