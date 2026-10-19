package lsp

import (
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// Each test pins one scope rule the occurrence index mirrors from the checker.

func TestRename_InterpolationRootIsAReferenceButLiteralTextIsNot(t *testing.T) {
	src := `blueprint "greet" {
  version  "1.0.0"
  port     3000
  runtime  node
}

POST /api/greet {
  <- name string required
  |> message = "Hello {name}, name stays text"
  -> 200 { message: message }
}
`
	doc := parseTestDoc(t, src)
	uri := "file:///greet.bp"
	p := posAt(t, src, "name string required", 0)
	edit, fail := computeRename(uri, doc, p.Line, p.Character, "who")
	if fail != nil {
		t.Fatalf("rename refused: %s", fail.Message)
	}
	out := applyRenameEdits(t, src, workspaceEdits(t, edit, uri))
	for _, want := range []string{
		"<- who string required",
		`"Hello {who}, name stays text"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renamed text missing %q\n%s", want, out)
		}
	}
	parseTestDoc(t, out)
}

func TestRename_TryBindingsAreVisibleAfterTheBlock(t *testing.T) {
	src := `blueprint "try" {
  version  "1.0.0"
  port     3000
  runtime  node
}

model todo {
  id uuid primary
}

POST /api/t {
  <- id uuid required
  |> try {
    |> found = fetch todo(id)
  } recover {
    |> log "lookup failed"
  }
  -> 200 { found: found.id }
}
`
	doc := parseTestDoc(t, src)
	uri := "file:///try.bp"
	p := posAt(t, src, "found = fetch", 0)
	edit, fail := computeRename(uri, doc, p.Line, p.Character, "row")
	if fail != nil {
		t.Fatalf("rename refused: %s", fail.Message)
	}
	out := applyRenameEdits(t, src, workspaceEdits(t, edit, uri))
	for _, want := range []string{"|> row = fetch todo(id)", "-> 200 { found: row.id }"} {
		if !strings.Contains(out, want) {
			t.Errorf("renamed text missing %q\n%s", want, out)
		}
	}
	parseTestDoc(t, out)
}

func TestRename_ModelUsedAsMapBodyIsRefused(t *testing.T) {
	src := `blueprint "map" {
  version  "1.0.0"
  port     3000
  runtime  node
}

model todo {
  id    uuid primary
  title string
}

POST /api/t {
  |> items = query todo
  |> map items: update todo { title: "done" }
  -> 200 { count: items.count }
}
`
	doc := parseTestDoc(t, src)
	uri := "file:///map.bp"
	p := posAt(t, src, "query todo", 6)
	if _, _, ok := prepareRenameAt(doc, p.Line, p.Character); ok {
		t.Fatal("prepareRename must not offer a model that a map step derives a binding from")
	}
	if _, fail := computeRename(uri, doc, p.Line, p.Character, "task"); fail == nil || !strings.Contains(fail.Message, "derives") {
		t.Fatalf("rename of a map-derived model should be refused, got %+v", fail)
	}
}

func TestRename_MiddlewareInjectIsNotRenameable(t *testing.T) {
	data, err := os.ReadFile("../../examples/auth-service.bp")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	doc := parseTestDoc(t, src)
	// `inject user as current_user` sits in a middleware and is read by endpoints.
	p := posAt(t, src, "inject user as current_user", 15)
	if _, _, ok := prepareRenameAt(doc, p.Line, p.Character); ok {
		t.Fatal("a middleware-injected alias must not be renameable")
	}
	use := posAt(t, src, "current_user.id,", 0)
	if _, _, ok := prepareRenameAt(doc, use.Line, use.Character); ok {
		t.Fatal("an endpoint use of a middleware-injected alias must not be renameable")
	}
}

func TestRename_WebSocketHandlersReadConnectBindings(t *testing.T) {
	data, err := os.ReadFile("../../examples/realtime-chat.bp")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	doc := parseTestDoc(t, src)
	uri := "file:///realtime.bp"
	p := posAt(t, src, "inject claims.username as sender", len("inject claims.username as "))
	edit, fail := computeRename(uri, doc, p.Line, p.Character, "user_name")
	if fail != nil {
		t.Fatalf("rename refused: %s", fail.Message)
	}
	out := applyRenameEdits(t, src, workspaceEdits(t, edit, uri))
	for _, want := range []string{
		"inject claims.username as user_name",
		`|> log "User {user_name} left room {room.name}" level(info)`,
		"sender: user_name }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renamed text missing %q", want)
		}
	}
	parseTestDoc(t, out)
}

// The checker binds some names implicitly or injects them from outside the
// block. Renaming onto one of those must be refused even though the checker
// reports nothing, because the rename would silently rebind the reads.

func TestRename_CannotCaptureWebSocketMessageOrRecoverError(t *testing.T) {
	ws := `blueprint "ws" {
  version  "1.0.0"
  port     3000
  runtime  node
}

WS /ws/chat {
  on_connect {
    <- token string
    |> x = "joined"
  }
  on_message {
    |> guard message.body -> 400 "Empty"
    -> { note: x }
  }
}
`
	doc := parseTestDoc(t, ws)
	p := posAt(t, ws, "x = \"joined\"", 0)
	if _, fail := computeRename("file:///ws.bp", doc, p.Line, p.Character, "message"); fail == nil {
		t.Fatal("renaming onto the implicit WebSocket message name must be refused")
	}
	if _, fail := computeRename("file:///ws.bp", doc, p.Line, p.Character, "label"); fail != nil {
		t.Fatalf("a fresh name should be accepted: %s", fail.Message)
	}

	src := `blueprint "try" {
  version  "1.0.0"
  port     3000
  runtime  node
}

model todo {
  id uuid primary
}

POST /api/t {
  <- id uuid required
  |> try {
    |> found = fetch todo(id)
  } recover {
    |> log "lookup failed"
  }
  -> 200 { found: found.id }
}
`
	tryDoc := parseTestDoc(t, src)
	q := posAt(t, src, "found = fetch", 0)
	if _, fail := computeRename("file:///try.bp", tryDoc, q.Line, q.Character, "error"); fail == nil {
		t.Fatal("renaming onto the implicit recover name must be refused")
	}
}

func TestRename_CannotCaptureMiddlewareInjectedAlias(t *testing.T) {
	data, err := os.ReadFile("../../examples/auth-service.bp")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	doc := parseTestDoc(t, src)
	// `current_user` is read by endpoints but injected by a middleware.
	p := posAt(t, src, "valid = verify_password", 0)
	if _, fail := computeRename("file:///auth.bp", doc, p.Line, p.Character, "current_user"); fail == nil {
		t.Fatal("renaming a local onto a middleware-injected name must be refused")
	}
}

func TestRename_ExternalRenameUpdatesSubscribeAndCalls(t *testing.T) {
	src := `blueprint "sub" {
  version  "1.0.0"
  port     3000
  runtime  node
}

external "auth-service" {
  url: "http://auth:3001"
}

subscribe "user.created" from(auth_service) {
  |> log "User created"
}

GET /api/me {
  |> user = call auth_service GET /api/users/me
  -> 200 { name: user.name }
}
`
	doc := parseTestDoc(t, src)
	uri := "file:///sub.bp"
	p := posAt(t, src, "auth-service", 2)
	edit, fail := computeRename(uri, doc, p.Line, p.Character, "billing_api")
	if fail != nil {
		t.Fatalf("rename refused: %s", fail.Message)
	}
	out := applyRenameEdits(t, src, workspaceEdits(t, edit, uri))
	for _, want := range []string{`external "billing_api"`, "from(billing_api)", "call billing_api GET"} {
		if !strings.Contains(out, want) {
			t.Errorf("renamed text missing %q\n%s", want, out)
		}
	}
	parseTestDoc(t, out)
}

// TestSymbolIndex_TruncatedDocumentsNeverPanic exercises the LSP entry points
// on prefixes of real documents, which is what an editor sends mid-edit.
func TestSymbolIndex_TruncatedDocumentsNeverPanic(t *testing.T) {
	for _, src := range []string{renameFixtureSrc, rebindFixtureSrc} {
		for cut := 0; cut <= len(src); cut += 3 {
			text := src[:cut]
			file, errs := parser.ParseFile("", []byte(text))
			doc := &docIndex{text: text, file: file, errors: errs}
			lines := strings.Split(text, "\n")
			for line := 0; line < len(lines); line += 2 {
				for char := 0; char <= len(lines[line]); char += 4 {
					computeReferences("file:///t.bp", doc, line, char, true)
					prepareRenameAt(doc, line, char)
					computeRename("file:///t.bp", doc, line, char, "zz_new")
				}
			}
		}
	}
}

// A <- input that names a :param segment is the same symbol as the segment.
// Renaming only the input would make a REST endpoint stop reading the path
// parameter, and make a STREAM or WS input name no path parameter at all.
func TestRename_PathParamInputRenamesThePathSegment(t *testing.T) {
	src := `blueprint "paths" {
  version "1.0.0"
  runtime node
}

GET /api/rooms/:room_id/members/:id {
  <- room_id uuid required
  <- id uuid required
  -> 200 { room: room_id, member: id }
}

STREAM /api/rooms/:room_id/live {
  <- room_id uuid required
  stream {
    |> on timeout(5min) {
      -> { room: room_id }
    }
  }
}
`
	doc := parseTestDoc(t, src)
	uri := "file:///paths.bp"
	for _, tc := range []struct {
		at   string
		want []string
	}{
		{"<- room_id uuid required\n  <- id", []string{
			"GET /api/rooms/:space_id/members/:id {",
			"<- space_id uuid required\n  <- id",
			"{ room: space_id, member: id }",
			"STREAM /api/rooms/:room_id/live {",
		}},
		{"/live {\n  <- room_id", []string{
			"GET /api/rooms/:room_id/members/:id {",
			"STREAM /api/rooms/:space_id/live {",
			"-> { room: space_id }",
		}},
	} {
		p := posAt(t, src, tc.at, strings.Index(tc.at, "room_id"))
		edit, fail := computeRename(uri, doc, p.Line, p.Character, "space_id")
		if fail != nil {
			t.Fatalf("rename at %q refused: %s", tc.at, fail.Message)
		}
		out := applyRenameEdits(t, src, workspaceEdits(t, edit, uri))
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Errorf("rename at %q: missing %q\n%s", tc.at, want, out)
			}
		}
		parseTestDoc(t, out)
	}

	// The segment itself is a rename position and a reference.
	p := posAt(t, src, ":id {", 1)
	if _, name, ok := prepareRenameAt(doc, p.Line, p.Character); !ok || name != "id" {
		t.Fatalf("prepareRename on :id = %q, %v; want id, true", name, ok)
	}
}
