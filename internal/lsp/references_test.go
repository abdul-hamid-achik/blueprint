package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// renameFixtureSrc exercises every symbol kind the index tracks: a model with
// FK and column-like names, a ref() constraint, a pipe, a fn-free endpoint pair
// with same-spelled bindings, strings, an intent and a comment that repeat
// symbol names.
const renameFixtureSrc = `@ "Todo fixture for rename tests"
blueprint "rename-fixture" {
  version  "1.0.0"
  port     3000
  runtime  node
}

# todo comment that must never be renamed
model todo {
  id       uuid primary
  todo_id  uuid
  owner_id uuid required
  title    string required
}

model comment {
  id       uuid primary
  todo_ref uuid ref(todo)
  body     string
}

pipe validate_title {
  <- title string
  |> guard title != "" -> 400 "empty todo title"
  -> title
}

@ "Fetch one todo for todo owner"
GET /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> guard todo -> 404 "todo not found"
  |> heading = pipe validate_title(todo.title)
  -> 200 { title: heading, note: "todo is here" }
}

@ "Create a todo"
POST /api/todos {
  <- title string required
  <- owner_id uuid required
  |> todo = save todo { title: title, owner_id: owner_id }
  |> rows = query todo where(owner_id == owner_id)
  -> 201 { id: todo.id, count: rows.count }
}
`

// rebindFixtureSrc has an endpoint input rebound by a later step.
const rebindFixtureSrc = `blueprint "rebind-fixture" {
  version  "1.0.0"
  port     3000
  runtime  node
}

pipe validate_name {
  <- value string
  |> guard value != "" -> 400 "empty"
  -> value
}

POST /api/names {
  <- name string required
  |> name = pipe validate_name(name)
  -> 200 { name: name }
}
`

// parseTestDoc parses src with the strict parser and fails the test if it does
// not parse or does not pass the checker, so every rename test starts from a
// valid program.
func parseTestDoc(t *testing.T, src string) *docIndex {
	t.Helper()
	file, errs := parser.ParseFile("", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("fixture does not parse: %v", errs[0].Message)
	}
	if cerrs := checker.Check(file); len(cerrs) > 0 {
		t.Fatalf("fixture does not pass the checker: %s", cerrs[0].Message)
	}
	return &docIndex{text: src, file: file, errors: errs}
}

// posAt returns the LSP position delta bytes after the first occurrence of needle.
func posAt(t *testing.T, text, needle string, delta int) lspPosition {
	t.Helper()
	off := strings.Index(text, needle)
	if off < 0 {
		t.Fatalf("needle %q not found", needle)
	}
	return offsetToPosition(text, off+delta)
}

func posKey(p lspPosition) string { return fmt.Sprintf("%d:%d", p.Line, p.Character) }

func locKeys(locs []lspLocation) []string {
	out := make([]string, 0, len(locs))
	for _, l := range locs {
		out = append(out, posKey(l.Range.Start))
	}
	sort.Strings(out)
	return out
}

func wantKeys(ps ...lspPosition) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, posKey(p))
	}
	sort.Strings(out)
	return out
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applyRenameEdits applies LSP text edits to text.
func applyRenameEdits(t *testing.T, text string, edits []lspTextEdit) string {
	t.Helper()
	type span struct {
		start, end int
		newText    string
	}
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		s, ok1 := positionToOffset(text, e.Range.Start.Line, e.Range.Start.Character)
		en, ok2 := positionToOffset(text, e.Range.End.Line, e.Range.End.Character)
		if !ok1 || !ok2 {
			t.Fatalf("edit out of range: %+v", e)
		}
		spans = append(spans, span{s, en, e.NewText})
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	out := text
	for _, s := range spans {
		out = out[:s.start] + s.newText + out[s.end:]
	}
	return out
}

// workspaceEdits extracts the edits for uri from a WorkspaceEdit result.
func workspaceEdits(t *testing.T, edit map[string]interface{}, uri string) []lspTextEdit {
	t.Helper()
	changes, ok := edit["changes"].(map[string][]lspTextEdit)
	if !ok {
		t.Fatalf("WorkspaceEdit has no changes map: %#v", edit)
	}
	return changes[uri]
}

func TestSymbolIndex_ExamplesAndFixturesResolveCompletely(t *testing.T) {
	files, _ := filepath.Glob("../../examples/*.bp")
	more, _ := filepath.Glob("../../testdata/valid/*.bp")
	files = append(files, more...)
	if len(files) == 0 {
		t.Fatal("no example or fixture files found")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, errs := parser.ParseFile(f, data)
		if len(errs) > 0 {
			continue // invalid fixtures are covered by the parser tests
		}
		si := buildSymbolIndex(&docIndex{text: string(data), file: file})
		if si.mismatches != 0 {
			t.Errorf("%s: %d AST sites did not match their tokens", filepath.Base(f), si.mismatches)
		}
	}
}

func TestReferences_ModelSitesExcludeStringsIntentsCommentsAndFKs(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	uri := "file:///fixture.bp"
	// Cursor on the model declaration name.
	line := posAt(t, renameFixtureSrc, "model todo", 6)
	got := locKeys(computeReferences(uri, doc, line.Line, line.Character, true))
	want := wantKeys(
		posAt(t, renameFixtureSrc, "model todo", 6),
		posAt(t, renameFixtureSrc, "ref(todo)", 4),
		posAt(t, renameFixtureSrc, "fetch todo", 6),
		posAt(t, renameFixtureSrc, "save todo", 5),
		posAt(t, renameFixtureSrc, "query todo", 6),
	)
	if !equalKeys(got, want) {
		t.Fatalf("includeDeclaration=true: got %v, want %v", got, want)
	}

	got = locKeys(computeReferences(uri, doc, line.Line, line.Character, false))
	want = wantKeys(
		posAt(t, renameFixtureSrc, "ref(todo)", 4),
		posAt(t, renameFixtureSrc, "fetch todo", 6),
		posAt(t, renameFixtureSrc, "save todo", 5),
		posAt(t, renameFixtureSrc, "query todo", 6),
	)
	if !equalKeys(got, want) {
		t.Fatalf("includeDeclaration=false: got %v, want %v", got, want)
	}
}

func TestReferences_PipeCallSites(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	uri := "file:///fixture.bp"
	p := posAt(t, renameFixtureSrc, "pipe validate_title {", 5)
	got := locKeys(computeReferences(uri, doc, p.Line, p.Character, true))
	want := wantKeys(
		posAt(t, renameFixtureSrc, "pipe validate_title {", 5),
		posAt(t, renameFixtureSrc, "pipe validate_title(todo", 5),
	)
	if !equalKeys(got, want) {
		t.Fatalf("pipe references: got %v, want %v", got, want)
	}
}

func TestReferences_BindingScopeIsolatedToItsEndpoint(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	uri := "file:///fixture.bp"
	// The `todo` binding in GET; POST declares its own `todo` binding.
	p := posAt(t, renameFixtureSrc, "todo = fetch", 0)
	got := locKeys(computeReferences(uri, doc, p.Line, p.Character, true))
	want := wantKeys(
		posAt(t, renameFixtureSrc, "todo = fetch", 0),
		posAt(t, renameFixtureSrc, "guard todo ->", 6),
		posAt(t, renameFixtureSrc, "todo.title)", 0),
	)
	if !equalKeys(got, want) {
		t.Fatalf("binding references: got %v, want %v", got, want)
	}
}

func TestRename_BindingKeepsKVKeysAndPipeNames(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	uri := "file:///fixture.bp"
	p := posAt(t, renameFixtureSrc, "heading = pipe", 0)
	edit, fail := computeRename(uri, doc, p.Line, p.Character, "headline")
	if fail != nil {
		t.Fatalf("rename refused: %s", fail.Message)
	}
	out := applyRenameEdits(t, renameFixtureSrc, workspaceEdits(t, edit, uri))
	for _, want := range []string{
		"|> headline = pipe validate_title(todo.title)",
		"-> 200 { title: headline, note: \"todo is here\" }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renamed text missing %q\n%s", want, out)
		}
	}
	parseTestDoc(t, out) // still a valid program
}

func TestRename_ModelKeepsFKFieldsStringsAndBindings(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	uri := "file:///fixture.bp"
	p := posAt(t, renameFixtureSrc, "model todo", 6)
	edit, fail := computeRename(uri, doc, p.Line, p.Character, "task")
	if fail != nil {
		t.Fatalf("rename refused: %s", fail.Message)
	}
	out := applyRenameEdits(t, renameFixtureSrc, workspaceEdits(t, edit, uri))
	for _, want := range []string{
		"model task {",
		"ref(task)",
		"|> todo = fetch task(id)",
		"|> todo = save task {",
		"|> rows = query task where",
		"todo_id  uuid",
		"todo_ref uuid",
		"# todo comment that must never be renamed",
		"@ \"Fetch one todo for todo owner\"",
		"\"todo is here\"",
		"\"todo not found\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renamed text missing %q\n%s", want, out)
		}
	}
	parseTestDoc(t, out)
}

func TestRename_InputRebindIsOneSymbolAndKVKeysStay(t *testing.T) {
	doc := parseTestDoc(t, rebindFixtureSrc)
	uri := "file:///rebind.bp"
	p := posAt(t, rebindFixtureSrc, "name string required", 0)
	edit, fail := computeRename(uri, doc, p.Line, p.Character, "label")
	if fail != nil {
		t.Fatalf("rename refused: %s", fail.Message)
	}
	out := applyRenameEdits(t, rebindFixtureSrc, workspaceEdits(t, edit, uri))
	for _, want := range []string{
		"<- label string required",
		"|> label = pipe validate_name(label)",
		"-> 200 { name: label }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renamed text missing %q\n%s", want, out)
		}
	}
	parseTestDoc(t, out)
}

func TestRename_WherePredicateColumnIsNotABinding(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	uri := "file:///fixture.bp"
	inp := posAt(t, renameFixtureSrc, "<- owner_id uuid", 3)
	edit, fail := computeRename(uri, doc, inp.Line, inp.Character, "owner")
	if fail != nil {
		t.Fatalf("rename refused: %s", fail.Message)
	}
	out := applyRenameEdits(t, renameFixtureSrc, workspaceEdits(t, edit, uri))
	for _, want := range []string{
		"<- owner uuid required",
		"owner_id: owner }",
		"where(owner_id == owner)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renamed text missing %q\n%s", want, out)
		}
	}
	parseTestDoc(t, out)
}

func TestRename_RejectsInvalidAndCollidingNames(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	uri := "file:///fixture.bp"
	pipeAt := posAt(t, renameFixtureSrc, "pipe validate_title {", 5)
	bindAt := posAt(t, renameFixtureSrc, "heading = pipe", 0)
	cases := []struct {
		name string
		line lspPosition
		new  string
	}{
		{"empty name", pipeAt, ""},
		{"leading digit", pipeAt, "1abc"},
		{"space inside", pipeAt, "two words"},
		{"hyphen", pipeAt, "todo-id"},
		{"keyword fetch", pipeAt, "fetch"},
		{"keyword query", pipeAt, "query"},
		{"reserved pipe prefix", pipeAt, "pipe_x"},
		{"reserved env", bindAt, "env"},
		{"reserved header", bindAt, "header"},
		{"collides with model", pipeAt, "todo"},
		{"collides with other model", pipeAt, "comment"},
		{"binding collides with model", bindAt, "todo"},
		{"binding collides with input", bindAt, "id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			edit, fail := computeRename(uri, doc, c.line.Line, c.line.Character, c.new)
			if fail == nil {
				t.Fatalf("rename to %q was accepted: %v", c.new, edit)
			}
			if fail.Code == 0 || fail.Message == "" {
				t.Fatalf("refusal lacks code or message: %+v", fail)
			}
		})
	}
}

func TestPrepareRename_NullOutsideRenameableSymbols(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	cases := map[string]lspPosition{
		"keyword fetch":         posAt(t, renameFixtureSrc, "fetch todo(id)", 2),
		"keyword guard":         posAt(t, renameFixtureSrc, "guard todo ->", 2),
		"keyword primary":       posAt(t, renameFixtureSrc, "primary", 1),
		"string literal":        posAt(t, renameFixtureSrc, "\"todo is here\"", 2),
		"error string":          posAt(t, renameFixtureSrc, "\"todo not found\"", 3),
		"intent word":           posAt(t, renameFixtureSrc, "@ \"Fetch one todo", 5),
		"comment word":          posAt(t, renameFixtureSrc, "# todo comment", 3),
		"field name todo_id":    posAt(t, renameFixtureSrc, "todo_id  uuid", 2),
		"integer literal":       posAt(t, renameFixtureSrc, "200 {", 1),
		"field access property": posAt(t, renameFixtureSrc, "todo.id", 5),
		"whitespace":            posAt(t, renameFixtureSrc, "  id       uuid", 0),
	}
	for name, p := range cases {
		if _, _, ok := prepareRenameAt(doc, p.Line, p.Character); ok {
			t.Errorf("%s: prepareRename returned a range; want null", name)
		}
	}
}

func TestPrepareRename_ReturnsRangeAndPlaceholder(t *testing.T) {
	doc := parseTestDoc(t, renameFixtureSrc)
	p := posAt(t, renameFixtureSrc, "fetch todo(id)", 6)
	rng, placeholder, ok := prepareRenameAt(doc, p.Line, p.Character)
	if !ok {
		t.Fatal("expected a renameable model reference")
	}
	if placeholder != "todo" {
		t.Errorf("placeholder = %q, want todo", placeholder)
	}
	if rng.Start != p || rng.End.Character != p.Character+len("todo") {
		t.Errorf("range = %+v, want start %+v spanning 4 characters", rng, p)
	}
}

func TestPrepareRename_BuiltinMiddlewareIsNotRenameable(t *testing.T) {
	src := `blueprint "mw" {
  version  "1.0.0"
  port     3000
  runtime  node
}

GET /api/ping {
  use request_logger
  -> 200 { ok: true }
}
`
	doc := parseTestDoc(t, src)
	p := posAt(t, src, "request_logger", 3)
	if _, _, ok := prepareRenameAt(doc, p.Line, p.Character); ok {
		t.Fatal("builtin middleware must not be renameable")
	}
	if refs := computeReferences("file:///mw.bp", doc, p.Line, p.Character, true); len(refs) != 0 {
		t.Fatalf("builtin middleware references = %v, want none", refs)
	}
}

// TestDefinitionAgreesWithReferences checks that go-to-definition from each
// reference site lands on the declaration the index reports, for the symbol
// kinds definition supports.
func TestDefinitionAgreesWithReferences(t *testing.T) {
	sources := []string{renameFixtureSrc, rebindFixtureSrc}
	for _, f := range validSourceFiles(t) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, string(data))
	}
	for _, src := range sources {
		file, errs := parser.ParseFile("", []byte(src))
		if len(errs) > 0 {
			continue
		}
		doc := &docIndex{text: src, file: file, errors: errs}
		uri := "file:///agree.bp"
		si := buildSymbolIndex(doc)
		for _, o := range si.occs {
			// computeDefinition supports models, fn, pipe and middleware only.
			if !strings.HasPrefix(o.Key, "model:") && !strings.HasPrefix(o.Key, "fn:") &&
				!strings.HasPrefix(o.Key, "pipe:") && !strings.HasPrefix(o.Key, "middleware:") {
				continue
			}
			var declPos lspPosition
			found := false
			for _, d := range si.occurrencesOf(o.Key) {
				if d.Decl {
					declPos = offsetToPosition(src, d.Off)
					found = true
				}
			}
			if !found {
				t.Fatalf("no declaration for key %s", o.Key)
			}
			pos := offsetToPosition(src, o.Off)
			loc := computeDefinition(uri, buildIndex(uri, src), pos.Line, pos.Character)
			if loc == nil {
				t.Errorf("definition from %s at %s returned nothing", o.Key, posKey(pos))
				continue
			}
			rng := loc["range"].(lspRange)
			if rng.Start != declPos {
				t.Errorf("definition from %s at %s = %s, index declaration = %s", o.Key, posKey(pos), posKey(rng.Start), posKey(declPos))
			}
		}
	}
}

// validSourceFiles lists the example and fixture .bp files.
func validSourceFiles(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob("../../examples/*.bp")
	more, _ := filepath.Glob("../../testdata/valid/*.bp")
	files = append(files, more...)
	if len(files) == 0 {
		t.Fatal("no example or fixture files found")
	}
	return files
}

// TestRename_CheckerInvariantOverExamples renames every symbol in each
// example and fixture to a fresh name. Each accepted rename must touch only
// identifier tokens and must not add checker errors. A refusal that reports
// new checker errors means the index missed a site and is a test failure.
func TestRename_CheckerInvariantOverExamples(t *testing.T) {
	files, _ := filepath.Glob("../../examples/*.bp")
	more, _ := filepath.Glob("../../testdata/valid/*.bp")
	files = append(files, more...)
	totalAccepted := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		file, errs := parser.ParseFile(f, data)
		if len(errs) > 0 || len(checker.Check(file)) > 0 {
			continue
		}
		base := filepath.Base(f)
		doc := &docIndex{text: src, file: file}
		si := buildSymbolIndex(doc)
		uri := "file:///" + base
		baseErrs := len(checker.Check(file))

		seen := map[string]bool{}
		for _, o := range si.occs {
			if seen[o.Key] {
				continue
			}
			seen[o.Key] = true
			newName := freshRenameName(o.Name, len(seen))
			pos := offsetToPosition(src, o.Off)
			edit, fail := computeRename(uri, doc, pos.Line, pos.Character, newName)
			if fail != nil {
				// A refusal the index explains is fine; a refusal that reports
				// stale names means a site was missed.
				if strings.Contains(fail.Message, "would break") || strings.Contains(fail.Message, "new checker errors") {
					t.Errorf("%s: rename of %s (%s) left stale names: %s", base, o.Name, o.Key, fail.Message)
				}
				continue
			}
			totalAccepted++
			edits := workspaceEdits(t, edit, uri)
			for _, e := range edits {
				start, _ := positionToOffset(src, e.Range.Start.Line, e.Range.Start.Character)
				if strings.HasPrefix(o.Key, "external:") {
					continue // the declared string and the call identifier differ by normalization
				}
				if got := src[start : start+len(o.Name)]; got != o.Name {
					t.Errorf("%s: edit at %s covers %q, want %q", base, posKey(e.Range.Start), got, o.Name)
				}
				if !editOnCode(si, start) {
					t.Errorf("%s: edit at %s is not an identifier token or an interpolation root", base, posKey(e.Range.Start))
				}
			}
			out := applyRenameEdits(t, src, edits)
			renamed, perrs := parser.ParseFile(base, []byte(out))
			if len(perrs) > 0 {
				t.Errorf("%s: rename of %s produced a parse error: %s", base, o.Name, perrs[0].Message)
				continue
			}
			if got := len(checker.Check(renamed)); got > baseErrs {
				t.Errorf("%s: rename of %s added checker errors (%d -> %d)", base, o.Name, baseErrs, got)
			}
		}
	}
	if totalAccepted == 0 {
		t.Fatal("no rename was accepted; the invariant test is vacuous")
	}
}

// editOnCode reports whether an edit at off is an identifier-like token or an
// interpolation root inside an ordinary string. Comments are not tokens, and
// intent strings (`@ "..."`) never hold roots, so both are rejected.
func editOnCode(si *symbolIndex, off int) bool {
	for i, tok := range si.toks {
		switch {
		case tok.Kind == lexer.TokenString:
			if off > tok.Loc.Offset && off < tok.Loc.Offset+len(tok.Value)+2 && (i == 0 || si.toks[i-1].Kind != lexer.TokenIntent) {
				return true
			}
		case tok.Kind == lexer.TokenPath:
			// A :param segment linked to its <- input.
			if rel := off - tok.Loc.Offset; rel > 0 && rel < len(tok.Value) && tok.Value[rel-1] == ':' {
				return true
			}
		case tok.Loc.Offset == off:
			return true
		}
	}
	return false
}

// freshRenameName returns a valid identifier that keeps the casing style of
// the original name, so the checker's naming rules still apply.
func freshRenameName(old string, n int) string {
	if old != "" && old[0] >= 'A' && old[0] <= 'Z' {
		return fmt.Sprintf("Zz%d", n)
	}
	return fmt.Sprintf("zz_sym_%d", n)
}

// sessionMessages runs the server over framed requests and returns every
// message it wrote, in order.
func sessionMessages(t *testing.T, reqs ...jsonRPCMessage) []jsonRPCMessage {
	t.Helper()
	var input strings.Builder
	for _, r := range reqs {
		body, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		input.WriteString(framed(string(body)))
	}
	var out bytes.Buffer
	srv := NewServer(strings.NewReader(input.String()), &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return decodeFramed(t, out.Bytes())
}

func reqMsg(id int, method string, params any) jsonRPCMessage {
	return jsonRPCMessage{JSONRPC: "2.0", ID: idPtr(id), Method: method, Params: mustMarshal(params)}
}

func openMsg(uri, text string) jsonRPCMessage {
	return jsonRPCMessage{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: mustMarshal(map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri, "version": 1, "languageId": "blueprint", "text": text},
	})}
}

func TestServer_InitializeAdvertisesReferencesAndRename(t *testing.T) {
	msgs := sessionMessages(t,
		reqMsg(1, "initialize", map[string]interface{}{"processId": 1, "rootUri": ""}),
		reqMsg(2, "shutdown", nil),
		jsonRPCMessage{JSONRPC: "2.0", Method: "exit"},
	)
	init := findResponse(msgs, 1)
	if init == nil {
		t.Fatal("no initialize response")
	}
	var resp struct {
		Capabilities struct {
			ReferencesProvider bool `json:"referencesProvider"`
			RenameProvider     struct {
				PrepareProvider bool `json:"prepareProvider"`
			} `json:"renameProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(init.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Capabilities.ReferencesProvider {
		t.Error("referencesProvider not advertised")
	}
	if !resp.Capabilities.RenameProvider.PrepareProvider {
		t.Error("renameProvider.prepareProvider not advertised")
	}
}

func TestServer_RenameRoundtripAndResponseErrors(t *testing.T) {
	uri := "file:///fixture.bp"
	pipeLine := posAt(t, renameFixtureSrc, "pipe validate_title {", 5)
	at := func(p lspPosition) map[string]interface{} {
		return map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": uri},
			"position":     map[string]interface{}{"line": p.Line, "character": p.Character},
		}
	}
	withName := func(p lspPosition, name string) map[string]interface{} {
		m := at(p)
		m["newName"] = name
		return m
	}
	msgs := sessionMessages(t,
		reqMsg(1, "initialize", map[string]interface{}{"processId": 1, "rootUri": ""}),
		openMsg(uri, renameFixtureSrc),
		reqMsg(2, "textDocument/prepareRename", at(pipeLine)),
		reqMsg(3, "textDocument/references", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": uri},
			"position":     map[string]interface{}{"line": pipeLine.Line, "character": pipeLine.Character},
			"context":      map[string]interface{}{"includeDeclaration": true},
		}),
		reqMsg(4, "textDocument/rename", withName(pipeLine, "fetch")),
		reqMsg(5, "textDocument/rename", withName(pipeLine, "check_title")),
		reqMsg(6, "shutdown", nil),
		jsonRPCMessage{JSONRPC: "2.0", Method: "exit"},
	)

	prep := findResponse(msgs, 2)
	if prep == nil || prep.Error != nil {
		t.Fatalf("prepareRename failed: %+v", prep)
	}
	var prepResult struct {
		Placeholder string `json:"placeholder"`
	}
	if err := json.Unmarshal(prep.Result, &prepResult); err != nil || prepResult.Placeholder != "validate_title" {
		t.Errorf("prepareRename placeholder = %+v (%v)", prepResult, err)
	}

	refs := findResponse(msgs, 3)
	if refs == nil || refs.Error != nil {
		t.Fatalf("references failed: %+v", refs)
	}
	var locs []lspLocation
	if err := json.Unmarshal(refs.Result, &locs); err != nil || len(locs) != 2 {
		t.Errorf("references = %d locations (%v), want 2", len(locs), err)
	}

	bad := findResponse(msgs, 4)
	if bad == nil || bad.Error == nil || bad.Error.Code != lspInvalidParams {
		t.Errorf("keyword rename should return a ResponseError with code %d, got %+v", lspInvalidParams, bad)
	}

	good := findResponse(msgs, 5)
	if good == nil || good.Error != nil {
		t.Fatalf("valid rename failed: %+v", good)
	}
	var edit struct {
		Changes map[string][]lspTextEdit `json:"changes"`
	}
	if err := json.Unmarshal(good.Result, &edit); err != nil {
		t.Fatal(err)
	}
	edits := edit.Changes[uri]
	if len(edits) != 2 {
		t.Fatalf("rename edits = %d, want 2", len(edits))
	}
	for i := 1; i < len(edits); i++ {
		a, b := edits[i-1].Range.Start, edits[i].Range.Start
		if a.Line > b.Line || (a.Line == b.Line && a.Character >= b.Character) {
			t.Errorf("edits not sorted by position: %v", edits)
		}
	}
	out := applyRenameEdits(t, renameFixtureSrc, edits)
	if !strings.Contains(out, "pipe check_title(todo.title)") || !strings.Contains(out, "pipe check_title {") {
		t.Errorf("roundtrip rename did not apply:\n%s", out)
	}
}

func TestServer_PrepareRenameNullForUnknownDocument(t *testing.T) {
	msgs := sessionMessages(t,
		reqMsg(1, "textDocument/prepareRename", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file:///missing.bp"},
			"position":     map[string]interface{}{"line": 0, "character": 0},
		}),
		jsonRPCMessage{JSONRPC: "2.0", Method: "exit"},
	)
	resp := findResponse(msgs, 1)
	if resp == nil || resp.Error != nil || string(resp.Result) != "null" {
		t.Fatalf("prepareRename on unknown document = %+v, want null result", resp)
	}
}

// Ensure the lexer keyword table agrees with the rename keyword check.
func TestValidateRenameName_KeywordsUseLexerTable(t *testing.T) {
	for _, kw := range []string{"model", "fetch", "query", "order", "count"} {
		if lexer.LookupKeyword(kw) == lexer.TokenIdent {
			t.Fatalf("%s should be a keyword", kw)
		}
		if validateRenameName(kw) == nil {
			t.Errorf("%s accepted as a rename target", kw)
		}
	}
	if validateRenameName("order_total") != nil {
		t.Error("order_total should be a valid rename target")
	}
}
