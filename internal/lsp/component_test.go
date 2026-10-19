package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const componentModels = `model user {
  id   uuid primary
  name string required
}
`

const componentMain = `blueprint "app" {
  version "0.1.0"
  port    3000
  runtime node
}

include "models.bp"

@ "List users"
GET /users {
  |> rows = query user
  -> 200 { count: rows.count }
}
`

// componentOther declares a model with the same name but is not part of the
// include graph of main.bp, so a rename from main.bp must leave it alone.
const componentOther = `blueprint "other" {
  version "0.1.0"
  port    3001
  runtime node
}

model user {
  id uuid primary
}
`

// newWorkspaceServer writes files into a fresh temp directory, makes it the
// server's workspace root, and returns the server with that directory.
func newWorkspaceServer(t *testing.T, files map[string]string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	srv := NewServer(strings.NewReader(""), &out)
	srv.workspaceRoots = normalizeWorkspaceRoots([]string{dir})
	return srv, dir
}

// openWorkspaceDoc opens name (with in-memory text) in srv and returns its URI.
func openWorkspaceDoc(t *testing.T, srv *Server, dir, name, text string) string {
	t.Helper()
	uri := filenameToURI(filepath.Join(dir, name))
	sendRequest(t, srv, nil, "textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 1, "languageId": "blueprint", "text": text},
	})
	return uri
}

func changesOf(t *testing.T, edit map[string]interface{}) map[string][]lspTextEdit {
	t.Helper()
	changes, ok := edit["changes"].(map[string][]lspTextEdit)
	if !ok {
		t.Fatalf("edit has no changes map: %#v", edit)
	}
	return changes
}

func TestRename_TopLevelModelAcrossIncludes(t *testing.T) {
	srv, dir := newWorkspaceServer(t, map[string]string{
		"main.bp":   componentMain,
		"models.bp": componentModels,
		"other.bp":  componentOther,
	})
	mainURI := openWorkspaceDoc(t, srv, dir, "main.bp", componentMain)
	modelsURI := filenameToURI(filepath.Join(dir, "models.bp"))
	otherURI := filenameToURI(filepath.Join(dir, "other.bp"))

	// Through the real request path: the cursor sits on the use in main.bp.
	line, col := indexOf(componentMain, "query user")
	col += len("query ")
	id := 7
	sendRequest(t, srv, &id, "textDocument/rename", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": mainURI},
		"position":     map[string]interface{}{"line": line, "character": col},
		"newName":      "person",
	})
	msgs := decodeFramed(t, srvOutput(t, srv))
	resp := findResponse(msgs, id)
	if resp == nil || resp.Error != nil {
		t.Fatalf("rename response = %+v", resp)
	}
	var edit struct {
		Changes map[string][]lspTextEdit `json:"changes"`
	}
	if err := json.Unmarshal(resp.Result, &edit); err != nil {
		t.Fatalf("decode rename result: %v (raw=%s)", err, resp.Result)
	}
	if len(edit.Changes[mainURI]) != 1 || len(edit.Changes[modelsURI]) != 1 {
		t.Fatalf("changes = %+v, want one edit in main.bp and one in models.bp", edit.Changes)
	}
	if _, ok := edit.Changes[otherURI]; ok {
		t.Fatalf("unrelated other.bp was renamed: %+v", edit.Changes)
	}
	decl := edit.Changes[modelsURI][0].Range
	if decl.Start != (lspPosition{Line: 0, Character: 6}) || decl.End != (lspPosition{Line: 0, Character: 10}) {
		t.Fatalf("models.bp declaration edit = %+v, want line 0 cols 6-10", decl)
	}
}

// srvOutput returns everything the server has written so far.
func srvOutput(t *testing.T, srv *Server) []byte {
	t.Helper()
	w, ok := srv.writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("server writer is not a buffer")
	}
	return w.Bytes()
}

func TestRename_FromIncludedFragmentReachesIncluder(t *testing.T) {
	srv, dir := newWorkspaceServer(t, map[string]string{
		"main.bp":   componentMain,
		"models.bp": componentModels,
		"other.bp":  componentOther,
	})
	modelsURI := openWorkspaceDoc(t, srv, dir, "models.bp", componentModels)
	mainURI := filenameToURI(filepath.Join(dir, "main.bp"))
	otherURI := filenameToURI(filepath.Join(dir, "other.bp"))

	scope := srv.symbolScope(modelsURI)
	if len(scope) != 2 || scope[0].uri != modelsURI {
		t.Fatalf("scope = %+v, want models.bp first and main.bp", scopeURIs(scope))
	}

	line, col := indexOf(componentModels, "model user")
	col += len("model ")
	rng, name, ok := prepareComponentRename(scope, line, col)
	if !ok || name != "user" || rng.Start.Line != 0 {
		t.Fatalf("prepareRename on models.bp declaration = %+v %q %v", rng, name, ok)
	}

	edit, fail := computeComponentRename(scope, line, col, "person")
	if fail != nil {
		t.Fatalf("rename from models.bp failed: %s", fail.Message)
	}
	changes := changesOf(t, edit)
	if len(changes[modelsURI]) != 1 || len(changes[mainURI]) != 1 {
		t.Fatalf("changes = %+v, want edits in models.bp and main.bp", changes)
	}
	if _, ok := changes[otherURI]; ok {
		t.Fatal("unrelated other.bp was renamed")
	}
}

func TestRename_UsesUnsavedInMemoryText(t *testing.T) {
	srv, dir := newWorkspaceServer(t, map[string]string{
		"main.bp":   componentMain,
		"models.bp": componentModels,
	})
	mainURI := openWorkspaceDoc(t, srv, dir, "main.bp", componentMain)
	// The client has an unsaved line above the declaration; the disk copy
	// still starts with the model. Edits must follow the in-memory text.
	edited := "# unsaved edit\n" + componentModels
	modelsURI := openWorkspaceDoc(t, srv, dir, "models.bp", edited)

	line, col := indexOf(componentMain, "query user")
	col += len("query ")
	edit, fail := computeComponentRename(srv.symbolScope(mainURI), line, col, "person")
	if fail != nil {
		t.Fatalf("rename failed: %s", fail.Message)
	}
	changes := changesOf(t, edit)
	if len(changes[modelsURI]) != 1 {
		t.Fatalf("changes = %+v, want one edit in the open models.bp", changes)
	}
	got := changes[modelsURI][0].Range
	if got.Start != (lspPosition{Line: 1, Character: 6}) || got.End != (lspPosition{Line: 1, Character: 10}) {
		t.Fatalf("models.bp edit = %+v, want line 1 cols 6-10 of the unsaved text", got)
	}
}

func TestRename_IncludeCycleTerminates(t *testing.T) {
	const a = `blueprint "cycle" {
  version "0.1.0"
  port    3000
  runtime node
}

include "b.bp"

model widget {
  id uuid primary
}
`
	const b = `include "a.bp"

@ "Count widgets"
GET /widgets {
  |> rows = query widget
  -> 200 { count: rows.count }
}
`
	srv, dir := newWorkspaceServer(t, map[string]string{"a.bp": a, "b.bp": b})
	aURI := openWorkspaceDoc(t, srv, dir, "a.bp", a)
	bURI := filenameToURI(filepath.Join(dir, "b.bp"))

	type result struct {
		changes map[string][]lspTextEdit
		fail    *renameFailure
	}
	done := make(chan result, 1)
	line, col := indexOf(a, "model widget")
	col += len("model ")
	go func() {
		edit, fail := computeComponentRename(srv.symbolScope(aURI), line, col, "sprocket")
		if fail != nil {
			done <- result{fail: fail}
			return
		}
		done <- result{changes: edit["changes"].(map[string][]lspTextEdit)}
	}()
	select {
	case r := <-done:
		if r.fail != nil {
			t.Fatalf("rename in a cyclic component failed: %s", r.fail.Message)
		}
		if len(r.changes[aURI]) != 1 || len(r.changes[bURI]) != 1 {
			t.Fatalf("changes = %+v, want edits in a.bp and b.bp", r.changes)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("rename did not terminate on an include cycle")
	}
}

func TestRename_LocalBindingStaysInDocument(t *testing.T) {
	srv, dir := newWorkspaceServer(t, map[string]string{
		"main.bp":   componentMain,
		"models.bp": componentModels,
	})
	mainURI := openWorkspaceDoc(t, srv, dir, "main.bp", componentMain)
	line, col := indexOf(componentMain, "rows = query user")

	edit, fail := computeComponentRename(srv.symbolScope(mainURI), line, col, "found")
	if fail != nil {
		t.Fatalf("rename of endpoint-local binding failed: %s", fail.Message)
	}
	changes := changesOf(t, edit)
	if len(changes) != 1 || len(changes[mainURI]) < 2 {
		t.Fatalf("changes = %+v, want only main.bp edits for a local binding", changes)
	}
}

func TestReferences_SpanIncludeComponent(t *testing.T) {
	srv, dir := newWorkspaceServer(t, map[string]string{
		"main.bp":   componentMain,
		"models.bp": componentModels,
		"other.bp":  componentOther,
	})
	mainURI := openWorkspaceDoc(t, srv, dir, "main.bp", componentMain)
	modelsURI := filenameToURI(filepath.Join(dir, "models.bp"))
	otherURI := filenameToURI(filepath.Join(dir, "other.bp"))

	line, col := indexOf(componentMain, "query user")
	col += len("query ")
	locs := computeComponentReferences(srv.symbolScope(mainURI), line, col, true)
	byURI := map[string]int{}
	for _, l := range locs {
		byURI[l.URI]++
	}
	if byURI[mainURI] != 1 || byURI[modelsURI] != 1 || byURI[otherURI] != 0 || len(locs) != 2 {
		t.Fatalf("references by file = %v (locs %+v), want one in main.bp and one in models.bp", byURI, locs)
	}

	noDecl := computeComponentReferences(srv.symbolScope(mainURI), line, col, false)
	if len(noDecl) != 1 || noDecl[0].URI != mainURI {
		t.Fatalf("references without declaration = %+v, want only the use in main.bp", noDecl)
	}
}

func TestSymbolScope_FollowsReverseIncludesTransitively(t *testing.T) {
	const top = `blueprint "top" {
  version "0.1.0"
  port    3000
  runtime node
}

include "mid.bp"
`
	const mid = `include "models.bp"
`
	srv, dir := newWorkspaceServer(t, map[string]string{
		"top.bp":    top,
		"mid.bp":    mid,
		"models.bp": componentModels,
		"other.bp":  componentOther,
	})
	modelsURI := openWorkspaceDoc(t, srv, dir, "models.bp", componentModels)

	scope := srv.symbolScope(modelsURI)
	got := map[string]bool{}
	for _, f := range scope {
		got[filepath.Base(f.path)] = true
	}
	for _, want := range []string{"models.bp", "mid.bp", "top.bp"} {
		if !got[want] {
			t.Fatalf("scope %v is missing %s", scopeURIs(scope), want)
		}
	}
	if got["other.bp"] {
		t.Fatalf("scope %v includes unrelated other.bp", scopeURIs(scope))
	}
}

func TestSymbolScope_SkipsUnreadableInclude(t *testing.T) {
	const main = `blueprint "app" {
  version "0.1.0"
  port    3000
  runtime node
}

include "gone.bp"
include "models.bp"
`
	srv, dir := newWorkspaceServer(t, map[string]string{"main.bp": main, "models.bp": componentModels})
	mainURI := openWorkspaceDoc(t, srv, dir, "main.bp", main)
	scope := srv.symbolScope(mainURI)
	if len(scope) != 2 {
		t.Fatalf("scope = %v, want main.bp and models.bp only", scopeURIs(scope))
	}
	if srv.symbolScope("file:///not/open.bp") != nil {
		t.Fatal("symbolScope of an unopened document should be nil")
	}
}

func TestRename_RefusesNameDeclaredInIncludedFile(t *testing.T) {
	const models = componentModels + `
model account {
  id uuid primary
}
`
	srv, dir := newWorkspaceServer(t, map[string]string{"main.bp": componentMain, "models.bp": models})
	mainURI := openWorkspaceDoc(t, srv, dir, "main.bp", componentMain)
	line, col := indexOf(componentMain, "query user")
	col += len("query ")
	_, fail := computeComponentRename(srv.symbolScope(mainURI), line, col, "account")
	if fail == nil || !strings.Contains(fail.Message, "already exists") {
		t.Fatalf("rename onto a model in models.bp = %+v, want a collision refusal", fail)
	}
}

func scopeURIs(files []scopeFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.uri)
	}
	return out
}
