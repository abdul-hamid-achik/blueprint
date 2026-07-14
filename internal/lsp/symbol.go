package lsp

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// SymbolKind classifies what kind of symbol is at a cursor position.
type SymbolKind int

const (
	SymbolNone SymbolKind = iota
	SymbolModel
	SymbolFn
	SymbolPipe
	SymbolMiddleware
	SymbolField   // <model>.<field>
	SymbolIntent  // hovered over the @ intent prefix
	SymbolKeyword // bare keyword like "model", "fn"
	SymbolDataOp  // built-in step like "query", "save", "fetch"
	SymbolUnknown // an identifier we couldn't classify; fall back to keyword docs
)

// SymbolInfo describes a symbol found at a cursor.
type SymbolInfo struct {
	Kind   SymbolKind
	Name   string
	Parent string // for SymbolField, holds the model name
	// Range that the symbol occupies in the source (1-indexed line/col).
	Loc lexer.Loc
}

// builtinDataOps are the data-op step names recognized for hover. Mirrors
// internal/resolve/resolve.go's isDataOp() list. Kept in sync manually; if it
// drifts the worst case is missing hover text, not incorrect behavior.
var builtinDataOps = map[string]string{
	"query":      "**query** *(data op)*\n\nFetch rows from a model.\n\n```bp\n|> rows = query user where active = true\n```",
	"fetch":      "**fetch** *(data op)*\n\nFetch a single row by id/key from a model.\n\n```bp\n|> u = fetch user(id: input.id)\n```",
	"save":       "**save** *(data op)*\n\nPersist a row to a model (insert or update).\n\n```bp\n|> save user(name: input.name)\n```",
	"update":     "**update** *(data op)*\n\nUpdate fields on an existing row.\n\n```bp\n|> update user set name = input.name where id = input.id\n```",
	"delete":     "**delete** *(data op)*\n\nDelete one or more rows by predicate.\n\n```bp\n|> delete user where id = input.id\n```",
	"emit":       "**emit** *(data op)*\n\nEmit an event onto the queue/event bus.\n\n```bp\n|> emit user_created(user_id: u.id)\n```",
	"publish":    "**publish** *(data op)*\n\nPublish a message to a topic.",
	"send":       "**send** *(data op)*\n\nSend a message to an external channel/queue.",
	"log":        "**log** *(data op)*\n\nEmit a structured log line.",
	"track":      "**track** *(data op)*\n\nRecord an analytics event.",
	"transition": "**transition** *(data op)*\n\nMove a state machine to a new state.",
	"call":       "**call** *(data op)*\n\nInvoke another fn/pipe.",
}

// builtinKeywordDocs are the small set of static keyword docs the old hover
// returned. Preserved as a fallback when no AST symbol is found.
var builtinKeywordDocs = map[string]string{
	"blueprint":  "**blueprint** - Declares the service name and configuration\n\n```bp\nblueprint \"my-api\" {\n  version \"1.0.0\"\n  port 3000\n}\n```",
	"model":      "**model** - Defines a database table\n\n```bp\nmodel user {\n  id   uuid  primary\n  name string required\n}\n```",
	"fn":         "**fn** - Declares a function\n\n```bp\nfn process {\n  <- input string\n  -> output string\n}\n```",
	"pipe":       "**pipe** - Declares a reusable pipeline\n\n```bp\npipe validate {\n  <- input string\n  |> guard input != \"\" -> 400 \"Required\"\n  -> input\n}\n```",
	"middleware": "**middleware** - Declares reusable middleware\n\n```bp\nmiddleware auth {\n  before { |> inject user }\n}\n```",
	"guard":      "**guard** - Early return if condition fails\n\n```bp\n|> guard user.active -> 403 \"Forbidden\"\n```",
	"when":       "**when** - Conditional execution\n\n```bp\n|> when plan == \"pro\": limit = 1000\n```",
	"try":        "**try** - Error handling block\n\n```bp\n|> try {\n  |> risky_operation()\n} recover {\n  |> log error\n}\n```",
}

// docIndex caches a parsed AST for an open document text. The Server keeps a
// per-uri cache to avoid reparsing for every hover/definition request.
type docIndex struct {
	text   string
	file   *ast.File
	errors []parser.ParseError
}

// buildIndex parses the given source. Always returns a non-nil index; the
// caller is responsible for checking len(errors).
func buildIndex(uri, text string) *docIndex {
	f, errs := parser.ParsePartialFile(uriToFilename(uri), []byte(text))
	return &docIndex{text: text, file: f, errors: errs}
}

func uriToFilename(uri string) string {
	u, err := url.Parse(uri)
	if err == nil && u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") {
		return filepath.FromSlash(u.Path)
	}
	return uri
}

// extractWordAtPos returns the word at an LSP (line, UTF-16 character)
// position. The returned start column is a byte offset because lexer.Loc and
// the source slicing performed by findSymbolAt are byte-based.
// Returns ("", 0, 0) when no word is found.
func extractWordAtPos(text string, line, char int) (word string, startLine, startChar int) {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return "", 0, 0
	}
	src := lines[line]
	if char < 0 {
		char = 0
	}
	char = byteOffsetForUTF16(src, char)
	start := char
	for start > 0 && isWordByte(src[start-1]) {
		start--
	}
	end := char
	for end < len(src) && isWordByte(src[end]) {
		end++
	}
	if start == end {
		return "", line, start
	}
	return src[start:end], line, start
}

// findSymbolAt classifies what's at (line, char) using the parsed AST.
// Coordinates are LSP 0-indexed. Returns SymbolNone if the position is empty.
func findSymbolAt(idx *docIndex, line, char int) SymbolInfo {
	if idx == nil {
		return SymbolInfo{}
	}
	word, sline, scol := extractWordAtPos(idx.text, line, char)
	if word == "" {
		// Check whether the user is hovering over the `@` of an intent.
		if isAtIntentPrefix(idx.text, line, char) {
			return SymbolInfo{Kind: SymbolIntent}
		}
		return SymbolInfo{}
	}

	// Detect "<model>.<field>" by inspecting the preceding character.
	parent := ""
	{
		lines := strings.Split(idx.text, "\n")
		if sline < len(lines) {
			src := lines[sline]
			if scol > 0 && src[scol-1] == '.' {
				// Walk back through the parent identifier.
				pe := scol - 1
				ps := pe
				for ps > 0 && isWordByte(src[ps-1]) {
					ps--
				}
				if ps < pe {
					parent = src[ps:pe]
				}
			}
		}
	}

	loc := lexer.Loc{
		File: uriToFilename(""),
		Line: sline + 1,
		Col:  scol + 1,
		Len:  len(word),
	}

	if parent != "" {
		// Confirm parent resolves to a model before classifying as field.
		if _, ok := findModel(idx.file, parent); ok {
			return SymbolInfo{Kind: SymbolField, Name: word, Parent: parent, Loc: loc}
		}
	}

	// Classify by global declarations.
	for _, block := range idx.file.Blocks {
		switch b := block.(type) {
		case *ast.Model:
			if b.Name == word {
				return SymbolInfo{Kind: SymbolModel, Name: word, Loc: loc}
			}
		case *ast.Content:
			if b.Name == word {
				return SymbolInfo{Kind: SymbolModel, Name: word, Loc: loc}
			}
		case *ast.Fn:
			if b.Name == word {
				return SymbolInfo{Kind: SymbolFn, Name: word, Loc: loc}
			}
		case *ast.Pipe:
			if b.Name == word {
				return SymbolInfo{Kind: SymbolPipe, Name: word, Loc: loc}
			}
		case *ast.Middleware:
			if b.Name == word {
				return SymbolInfo{Kind: SymbolMiddleware, Name: word, Loc: loc}
			}
		}
	}

	if _, ok := builtinDataOps[word]; ok {
		return SymbolInfo{Kind: SymbolDataOp, Name: word, Loc: loc}
	}
	if _, ok := builtinKeywordDocs[word]; ok {
		return SymbolInfo{Kind: SymbolKeyword, Name: word, Loc: loc}
	}
	return SymbolInfo{Kind: SymbolUnknown, Name: word, Loc: loc}
}

// isAtIntentPrefix reports whether (line, char) is on the `@` character of an
// intent (`@ "..."`). The lexer emits a TokenIntent for `@` so we just look at
// the byte directly.
func isAtIntentPrefix(text string, line, char int) bool {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return false
	}
	src := lines[line]
	if char < 0 {
		return false
	}
	char = byteOffsetForUTF16(src, char)
	if char >= len(src) {
		return false
	}
	return src[char] == '@'
}

// findModel locates a model by name in the file. Falls back to scanning
// Content blocks (which behave like models for codegen).
func findModel(file *ast.File, name string) (*ast.Model, bool) {
	if file == nil {
		return nil, false
	}
	for _, block := range file.Blocks {
		switch b := block.(type) {
		case *ast.Model:
			if b.Name == name {
				return b, true
			}
		case *ast.Content:
			if b.Name == name {
				return b.AsModel(), true
			}
		}
	}
	return nil, false
}

// findField returns the named field on a model.
func findField(m *ast.Model, fieldName string) (*ast.Field, bool) {
	if m == nil {
		return nil, false
	}
	for _, f := range m.Fields {
		if f != nil && f.Name == fieldName {
			return f, true
		}
	}
	return nil, false
}

func findComputedField(m *ast.Model, fieldName string) (*ast.ComputedField, bool) {
	if m == nil {
		return nil, false
	}
	for _, field := range m.ComputedFields {
		if field != nil && field.Name == fieldName {
			return field, true
		}
	}
	return nil, false
}

// findIntentAt returns the *ast.Intent whose `@` is at (line, char).
// Currently only used to classify intent hover; other handlers don't need it.
func findIntentAt(file *ast.File, text string, line, char int) *ast.Intent {
	if file == nil {
		return nil
	}
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) || char < 0 {
		return nil
	}
	char = byteOffsetForUTF16(lines[line], char)
	var found *ast.Intent
	check := func(it *ast.Intent) {
		if it == nil {
			return
		}
		if it.Loc.Line-1 == line && it.Loc.Col-1 == char {
			found = it
		}
	}
	if file.Blueprint != nil {
		check(file.Blueprint.Intent)
	}
	for _, block := range file.Blocks {
		switch b := block.(type) {
		case *ast.Model:
			check(b.Intent)
		case *ast.Content:
			check(b.Intent)
		case *ast.Fn:
			check(b.Intent)
		case *ast.Pipe:
			check(b.Intent)
		case *ast.Middleware:
			check(b.Intent)
		case *ast.Endpoint:
			check(b.Intent)
		case *ast.StreamEndpoint:
			check(b.Intent)
		case *ast.WsEndpoint:
			check(b.Intent)
		case *ast.Worker:
			check(b.Intent)
		case *ast.Schedule:
			check(b.Intent)
		case *ast.Enum:
			check(b.Intent)
		case *ast.StateMachine:
			check(b.Intent)
		case *ast.Analytics:
			check(b.Intent)
		case *ast.SaveSchema:
			check(b.Intent)
		case *ast.Subscribe:
			check(b.Intent)
		case *ast.Test:
			check(b.Intent)
		case *ast.TestGroup:
			check(b.Intent)
		}
	}
	return found
}

func isWordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
