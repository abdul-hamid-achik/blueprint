package lsp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
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
// (line, char) of a single document, or false when the position is not
// renameable.
func prepareRenameAt(doc *docIndex, line, char int) (lspRange, string, bool) {
	return prepareComponentRename([]scopeFile{{doc: doc}}, line, char)
}

// prepareComponentRename is prepareRenameAt over an include component, with
// files[0] the document holding the cursor.
func prepareComponentRename(files []scopeFile, line, char int) (lspRange, string, bool) {
	if len(files) == 0 || files[0].doc == nil || len(files[0].doc.errors) > 0 {
		return lspRange{}, "", false
	}
	idx := componentIndexes(files)
	occ, ok := cursorOccurrence(files[0].doc, idx[0], line, char)
	if !ok || renameBlocked(idx[0], occ) != nil {
		return lspRange{}, "", false
	}
	return occurrenceRange(files[0].doc.text, occ), occ.Name, true
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
	return computeComponentRename([]scopeFile{{uri: uri, doc: doc}}, line, char, newName)
}

// computeComponentRename returns the WorkspaceEdit renaming the symbol at the
// cursor in files[0] to newName. A top-level symbol is renamed in every file of
// the include component; an endpoint-local binding stays in its document.
func computeComponentRename(files []scopeFile, line, char int, newName string) (map[string]interface{}, *renameFailure) {
	if len(files) == 0 || files[0].doc == nil {
		return nil, &renameFailure{Code: lspRequestFailed, Message: "document is not open"}
	}
	origin := files[0]
	if len(origin.doc.errors) > 0 {
		return nil, &renameFailure{Code: lspRequestFailed, Message: "fix parse errors before renaming"}
	}
	idx := componentIndexes(files)
	occ, ok := cursorOccurrence(origin.doc, idx[0], line, char)
	if !ok {
		return nil, &renameFailure{Code: lspRequestFailed, Message: "no renameable Blueprint symbol at this position"}
	}
	if strings.HasPrefix(occ.Key, "local:") {
		files, idx = files[:1], idx[:1]
	} else {
		for _, f := range files[1:] {
			if len(f.doc.errors) > 0 {
				return nil, &renameFailure{Code: lspRequestFailed, Message: fmt.Sprintf("fix parse errors in %s before renaming", fileLabel(f))}
			}
		}
	}
	for i := range idx {
		if fail := renameBlocked(idx[i], occ); fail != nil {
			return nil, fail
		}
	}
	if fail := validateRenameName(newName); fail != nil {
		return nil, fail
	}
	decls := 0
	for i := range idx {
		for _, o := range idx[i].occurrencesOf(occ.Key) {
			if o.Decl {
				decls++
			}
		}
	}
	if decls > 1 {
		return nil, &renameFailure{Code: lspRequestFailed, Message: fmt.Sprintf("cannot rename %q: it is declared in more than one file of the include set", occ.Name)}
	}
	for i := range idx {
		if fail := renameCollision(idx[i], occ, newName); fail != nil {
			return nil, fail
		}
	}

	occsByFile := make([][]symOccurrence, len(idx))
	changes := map[string][]lspTextEdit{}
	for i := range idx {
		occs := idx[i].occurrencesOf(occ.Key)
		occsByFile[i] = occs
		if len(occs) == 0 {
			continue
		}
		edits := make([]lspTextEdit, 0, len(occs))
		for _, o := range occs {
			edits = append(edits, lspTextEdit{Range: occurrenceRange(files[i].doc.text, o), NewText: newName})
		}
		sort.SliceStable(edits, func(a, b int) bool {
			x, y := edits[a].Range.Start, edits[b].Range.Start
			if x.Line != y.Line {
				return x.Line < y.Line
			}
			return x.Character < y.Character
		})
		changes[files[i].uri] = edits
	}

	if fail := renameKeepsProgramValid(files, occsByFile, newName); fail != nil {
		return nil, fail
	}
	return map[string]interface{}{
		"changes": changes,
	}, nil
}

// fileLabel names a scope file in diagnostics: its path, or its URI when the
// document is not a local file.
func fileLabel(f scopeFile) string {
	if f.path != "" {
		return f.path
	}
	return f.uri
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
	if si.declares(newName) {
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

// renameKeepsProgramValid applies the rename to the text of every file that
// changes and checks that each result parses and that the merged program the
// component forms reports no more checker errors than before. This is the
// safety net for any site the index failed to resolve: a stale name left behind
// shows up as a new checker error and the rename is refused.
func renameKeepsProgramValid(files []scopeFile, occsByFile [][]symOccurrence, newName string) *renameFailure {
	before := make([]*ast.File, 0, len(files))
	after := make([]*ast.File, 0, len(files))
	for i, f := range files {
		if f.doc.file == nil {
			return &renameFailure{Code: lspRequestFailed, Message: "document did not parse"}
		}
		before = append(before, f.doc.file)
		if len(occsByFile[i]) == 0 {
			after = append(after, f.doc.file)
			continue
		}
		name := f.path
		if name == "" {
			name = uriToFilename(f.uri)
		}
		newFile, perrs := parseComponentText(name, applyOccurrenceEdits(f.doc.text, occsByFile[i], newName))
		if len(perrs) > 0 || newFile == nil {
			return &renameFailure{Code: lspRequestFailed, Message: "rename would produce a parse error"}
		}
		after = append(after, newFile)
	}
	prev := checker.Check(mergeComponentProgram(before))
	next := checker.Check(mergeComponentProgram(after))
	if len(next) <= len(prev) {
		return nil
	}
	seen := map[string]int{}
	for _, e := range prev {
		seen[e.Message]++
	}
	for _, e := range next {
		if seen[e.Message] > 0 {
			seen[e.Message]--
			continue
		}
		return &renameFailure{Code: lspRequestFailed, Message: fmt.Sprintf("rename would break the program at %d:%d: %s", e.Loc.Line, e.Loc.Col, e.Message)}
	}
	return &renameFailure{Code: lspRequestFailed, Message: "rename would introduce new checker errors"}
}

// applyOccurrenceEdits returns text with every occurrence in occs replaced by
// newName. Occurrences are applied from the end, so earlier offsets stay valid.
func applyOccurrenceEdits(text string, occs []symOccurrence, newName string) string {
	sorted := append([]symOccurrence(nil), occs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Off > sorted[j].Off })
	for _, o := range sorted {
		text = text[:o.Off] + newName + text[o.Off+o.Len:]
	}
	return text
}

// handlePrepareRename handles textDocument/prepareRename.
func (s *Server) handlePrepareRename(msg *jsonRPCMessage) error {
	var params struct {
		TextDocumentPositionParams
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, lspInvalidParams, "Invalid params", err.Error())
	}
	rng, placeholder, ok := prepareComponentRename(s.symbolScope(params.TextDocument.URI), params.Position.Line, params.Position.Character)
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
	edit, fail := computeComponentRename(s.symbolScope(params.TextDocument.URI), params.Position.Line, params.Position.Character, params.NewName)
	if fail != nil {
		return s.sendError(msg.ID, fail.Code, fail.Message, nil)
	}
	return s.sendResult(msg.ID, edit)
}
