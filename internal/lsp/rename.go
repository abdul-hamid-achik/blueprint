package lsp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// renameFailure is a rename request the server refuses. Code is a JSON-RPC
// error code and is sent as a ResponseError.
type renameFailure struct {
	Code    int
	Message string
}

const (
	lspInvalidParams = -32602
	lspRequestFailed = -32803 // LSP RequestFailed: well-formed request, refused
)

// renameReservedNames are names the checker or runtime binds implicitly inside
// a block (env, header, data, message, error, item) or gives a built-in
// meaning (close, first), beyond the lexer keywords.
var renameReservedNames = map[string]bool{
	"env":     true,
	"header":  true,
	"data":    true,
	"message": true,
	"error":   true,
	"item":    true,
	"close":   true,
	"first":   true,
}

// validateRenameName reports why name cannot be a symbol name, or nil.
func validateRenameName(name string) *renameFailure {
	invalid := func(format string, args ...any) *renameFailure {
		return &renameFailure{Code: lspInvalidParams, Message: fmt.Sprintf(format, args...)}
	}
	if name == "" {
		return invalid("new name must not be empty")
	}
	if strings.HasPrefix(name, "pipe_") {
		return invalid("%q is reserved for pipe call lowering", name)
	}
	toks, errs := lexer.Tokenize("", []byte(name))
	if len(errs) > 0 || len(toks) != 2 || toks[1].Kind != lexer.TokenEOF || toks[0].Value != name {
		return invalid("%q is not a valid Blueprint identifier", name)
	}
	if toks[0].Kind != lexer.TokenIdent {
		if lexer.LookupKeyword(name) != lexer.TokenIdent {
			return invalid("%q is a Blueprint keyword and cannot be used as a name", name)
		}
		return invalid("%q is not a valid Blueprint identifier", name)
	}
	if renameReservedNames[name] {
		return invalid("%q is a reserved built-in name", name)
	}
	return nil
}

// prepareRenameAt reports the range and current name of the symbol at
// (line, char), or false when the position is not renameable.
func prepareRenameAt(doc *docIndex, line, char int) (lspRange, string, bool) {
	if doc == nil || len(doc.errors) > 0 {
		return lspRange{}, "", false
	}
	si, occ, ok := symbolAtCursor(doc, line, char)
	if !ok || renameBlocked(si, occ) != nil {
		return lspRange{}, "", false
	}
	return occurrenceRange(doc.text, occ), occ.Name, true
}

// renameBlocked reports why a symbol that the index tracks still cannot be
// renamed safely, or nil. Model names are used by the checker to derive
// implicit bindings, so a model used as a map body is refused.
func renameBlocked(si *symbolIndex, occ symOccurrence) *renameFailure {
	if strings.HasPrefix(occ.Key, "model:") && si.mapDerived[occ.Name] {
		return &renameFailure{Code: lspRequestFailed, Message: fmt.Sprintf("cannot rename %q: a map step derives a result binding from this model's name", occ.Name)}
	}
	return nil
}

// computeRename returns the WorkspaceEdit renaming the symbol at (line, char)
// to newName, limited to this document.
func computeRename(uri string, doc *docIndex, line, char int, newName string) (map[string]interface{}, *renameFailure) {
	if doc == nil {
		return nil, &renameFailure{Code: lspRequestFailed, Message: "document is not open"}
	}
	if len(doc.errors) > 0 {
		return nil, &renameFailure{Code: lspRequestFailed, Message: "fix parse errors before renaming"}
	}
	si, occ, ok := symbolAtCursor(doc, line, char)
	if !ok {
		return nil, &renameFailure{Code: lspRequestFailed, Message: "no renameable Blueprint symbol at this position"}
	}
	if fail := renameBlocked(si, occ); fail != nil {
		return nil, fail
	}
	if fail := validateRenameName(newName); fail != nil {
		return nil, fail
	}
	if fail := renameCollision(si, occ, newName); fail != nil {
		return nil, fail
	}

	occs := si.occurrencesOf(occ.Key)
	edits := make([]lspTextEdit, 0, len(occs))
	for _, o := range occs {
		edits = append(edits, lspTextEdit{Range: occurrenceRange(doc.text, o), NewText: newName})
	}
	sort.SliceStable(edits, func(i, j int) bool {
		a, b := edits[i].Range.Start, edits[j].Range.Start
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Character < b.Character
	})

	if fail := renameKeepsProgramValid(doc, occs, newName); fail != nil {
		return nil, fail
	}
	return map[string]interface{}{
		"changes": map[string][]lspTextEdit{uri: edits},
	}, nil
}

// renameCollision rejects names that would clash with, or be captured by, an
// existing symbol. The check is deliberately conservative: a top-level name
// conflicts with any top-level name or any endpoint-local binding of the same
// spelling, and a local name conflicts with any top-level name or any other
// binding in its own top-level block.
func renameCollision(si *symbolIndex, occ symOccurrence, newName string) *renameFailure {
	if newName == occ.Name {
		return nil
	}
	clash := func(format string, args ...any) *renameFailure {
		return &renameFailure{Code: lspRequestFailed, Message: fmt.Sprintf(format, args...)}
	}
	if si.unresolved[newName] {
		// The name is read here but bound outside the index (middleware
		// injection, implicit WebSocket or webhook names); renaming onto it
		// would silently rebind those reads.
		return clash("%q is already read in this document without a local binding, so renaming onto it would capture that read", newName)
	}
	if _, exists := si.globals[newName]; exists {
		return clash("a top-level declaration named %q already exists", newName)
	}
	if ext, exists := si.externals[normalizeExternal(newName)]; exists && "external:"+normalizeExternal(newName) != occ.Key {
		return clash("an external service named %q already exists", ext.Raw)
	}
	if strings.HasPrefix(occ.Key, "local:") {
		block := si.localBlock[occ.Key]
		for key, name := range si.localName {
			if key != occ.Key && name == newName && si.localBlock[key] == block {
				return clash("%q is already bound in this block", newName)
			}
		}
		return nil
	}
	for _, name := range si.localName {
		if name == newName {
			return clash("renaming to %q would be captured by an endpoint-local binding of the same name", newName)
		}
	}
	return nil
}

// renameKeepsProgramValid applies the rename to the text and checks that the
// result parses and the checker reports no more errors than before. This is
// the safety net for any site the index failed to resolve: a stale name left
// behind shows up as a new checker error and the rename is refused.
func renameKeepsProgramValid(doc *docIndex, occs []symOccurrence, newName string) *renameFailure {
	if doc.file == nil {
		return &renameFailure{Code: lspRequestFailed, Message: "document did not parse"}
	}
	text := doc.text
	sorted := append([]symOccurrence(nil), occs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Off > sorted[j].Off })
	for _, o := range sorted {
		text = text[:o.Off] + newName + text[o.Off+o.Len:]
	}

	newFile, perrs := parser.ParseFile("", []byte(text))
	if len(perrs) > 0 || newFile == nil {
		return &renameFailure{Code: lspRequestFailed, Message: "rename would produce a parse error"}
	}
	before := checker.Check(doc.file)
	after := checker.Check(newFile)
	if len(after) <= len(before) {
		return nil
	}
	seen := map[string]int{}
	for _, e := range before {
		seen[e.Message]++
	}
	for _, e := range after {
		if seen[e.Message] > 0 {
			seen[e.Message]--
			continue
		}
		return &renameFailure{Code: lspRequestFailed, Message: fmt.Sprintf("rename would break the program at %d:%d: %s", e.Loc.Line, e.Loc.Col, e.Message)}
	}
	return &renameFailure{Code: lspRequestFailed, Message: "rename would introduce new checker errors"}
}

// handlePrepareRename handles textDocument/prepareRename.
func (s *Server) handlePrepareRename(msg *jsonRPCMessage) error {
	var params struct {
		TextDocumentPositionParams
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, lspInvalidParams, "Invalid params", err.Error())
	}
	rng, placeholder, ok := prepareRenameAt(s.symbolDoc(params.TextDocument.URI), params.Position.Line, params.Position.Character)
	if !ok {
		return s.sendResult(msg.ID, nil)
	}
	return s.sendResult(msg.ID, map[string]interface{}{
		"range":       rng,
		"placeholder": placeholder,
	})
}

// handleRename handles textDocument/rename.
func (s *Server) handleRename(msg *jsonRPCMessage) error {
	var params struct {
		TextDocumentPositionParams
		NewName string `json:"newName"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, lspInvalidParams, "Invalid params", err.Error())
	}
	uri := params.TextDocument.URI
	edit, fail := computeRename(uri, s.symbolDoc(uri), params.Position.Line, params.Position.Character, params.NewName)
	if fail != nil {
		return s.sendError(msg.ID, fail.Code, fail.Message, nil)
	}
	return s.sendResult(msg.ID, edit)
}
