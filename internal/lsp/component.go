package lsp

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// scopeFile is one file of an include component, the set of files that a
// rename or reference search over one document must cover together.
type scopeFile struct {
	uri  string    // the URI the edits are keyed by
	path string    // absolute local path; "" when the document is not a local file
	doc  *docIndex // text, parsed file, and parse errors
	// includes are the absolute paths named by this file's include statements.
	includes []string
}

// parseComponentText parses one .bp file. A file with a blueprint block is
// parsed strictly. An include fragment has no blueprint block, so the strict
// parser rejects it; for those the partial parser is used instead. The strict
// errors are returned when neither parser accepts the text.
func parseComponentText(filename, text string) (*ast.File, []parser.ParseError) {
	file, errs := parser.ParseFile(filename, []byte(text))
	if len(errs) == 0 {
		return file, nil
	}
	if partial, perrs := parser.ParsePartialFile(filename, []byte(text)); len(perrs) == 0 {
		return partial, nil
	}
	return file, errs
}

// newScopeFile parses text as the document at uri (local path, or "" when uri
// is not a local file) and records its include targets.
func newScopeFile(uri, path, text string) scopeFile {
	filename := path
	if filename == "" {
		filename = uriToFilename(uri)
	}
	file, errs := parseComponentText(filename, text)
	return scopeFile{
		uri:      uri,
		path:     path,
		doc:      &docIndex{text: text, file: file, errors: errs},
		includes: includeTargets(path, file),
	}
}

// includeTargets returns the absolute paths named by the include statements of
// file, resolved against the directory of path as resolveIncludesRecursive does.
func includeTargets(path string, file *ast.File) []string {
	if path == "" || file == nil {
		return nil
	}
	dir := filepath.Dir(path)
	var out []string
	for _, block := range file.Blocks {
		inc, ok := block.(*ast.Include)
		if !ok {
			continue
		}
		if abs, err := filepath.Abs(filepath.Join(dir, inc.Path)); err == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	return out
}

// symbolScope returns the include component of the open document uri, with
// that document first. The component holds the files that the document reaches
// through include statements (recursively), plus every workspace .bp file whose
// include graph reaches a file already in the set. Open documents contribute
// their in-memory text; other files are read from disk, and unreadable files
// are left out. It returns nil when uri is not open.
//
// Include cycles are safe: each path enters the set once. The workspace scan
// reuses workspaceDocuments, so it honors the same skip rules as workspace
// symbols.
func (s *Server) symbolScope(uri string) []scopeFile {
	doc, ok := s.docs[uri]
	if !ok {
		return nil
	}
	if uriToFilename(uri) == uri {
		// Not a local file: nothing on disk can include it or be included by it.
		return []scopeFile{newScopeFile(uri, "", doc.Text)}
	}
	originPath := documentIdentity(uri)

	openByPath := make(map[string]*Document, len(s.docs))
	for u, d := range s.docs {
		if uriToFilename(u) == u {
			continue // not a local file, so it cannot match a path
		}
		openByPath[documentIdentity(u)] = d
	}

	loaded := map[string]*scopeFile{}
	load := func(path string) *scopeFile {
		if f, ok := loaded[path]; ok {
			return f
		}
		var f *scopeFile
		if d, open := openByPath[path]; open {
			sf := newScopeFile(d.URI, path, d.Text)
			f = &sf
		} else if data, err := os.ReadFile(path); err == nil {
			sf := newScopeFile(filenameToURI(path), path, string(data))
			f = &sf
		}
		loaded[path] = f
		return f
	}

	origin := load(originPath)
	if origin == nil {
		sf := newScopeFile(uri, originPath, doc.Text)
		origin = &sf
	}
	origin.uri = uri
	comp := map[string]*scopeFile{originPath: origin}

	var candidates []string
	for _, wd := range s.workspaceDocuments() {
		if p := documentIdentity(wd.uri); uriToFilename(wd.uri) != wd.uri {
			candidates = append(candidates, p)
		}
	}
	sort.Strings(candidates)

	inComp := func(includes []string) bool {
		for _, p := range includes {
			if _, ok := comp[p]; ok {
				return true
			}
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		// Forward: everything an included file pulls in.
		for _, f := range snapshotComponent(comp) {
			for _, p := range f.includes {
				if _, ok := comp[p]; ok {
					continue
				}
				if g := load(p); g != nil {
					comp[p] = g
					changed = true
				}
			}
		}
		// Reverse: workspace files that include something already in the set.
		for _, p := range candidates {
			if _, ok := comp[p]; ok {
				continue
			}
			if g := load(p); g != nil && inComp(g.includes) {
				comp[p] = g
				changed = true
			}
		}
	}

	paths := make([]string, 0, len(comp))
	for p := range comp {
		if p != originPath {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	out := []scopeFile{*origin}
	for _, p := range paths {
		out = append(out, *comp[p])
	}
	return out
}

// snapshotComponent returns the component's files in path order, so the
// fixpoint loop can add to the set while it walks a stable list.
func snapshotComponent(comp map[string]*scopeFile) []*scopeFile {
	paths := make([]string, 0, len(comp))
	for p := range comp {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]*scopeFile, 0, len(paths))
	for _, p := range paths {
		out = append(out, comp[p])
	}
	return out
}

// componentIndexes builds one symbol index per file. Each index also knows the
// top-level declarations of the other files in the scope, so a reference in
// main.bp to a model declared in models.bp resolves to the same symbol key
// ("model:<name>") as the declaration does.
func componentIndexes(files []scopeFile) []*symbolIndex {
	out := make([]*symbolIndex, len(files))
	if len(files) == 1 {
		out[0] = buildSymbolIndex(files[0].doc)
		return out
	}
	own := make([]map[string]globalSym, len(files))
	for i := range files {
		own[i] = buildSymbolIndex(files[i].doc).globals
	}
	for i := range files {
		imported := map[string]globalSym{}
		for j := range files {
			if j == i {
				continue
			}
			for name, g := range own[j] {
				if _, seen := imported[name]; !seen {
					imported[name] = g
				}
			}
		}
		out[i] = buildSymbolIndexWith(files[i].doc, imported)
	}
	return out
}

// cursorOccurrence returns the occurrence under (line, char) of doc, using the
// index idx built for that document in its component.
func cursorOccurrence(doc *docIndex, idx *symbolIndex, line, char int) (symOccurrence, bool) {
	if doc == nil || idx == nil {
		return symOccurrence{}, false
	}
	off, ok := positionToOffset(doc.text, line, char)
	if !ok {
		return symOccurrence{}, false
	}
	return idx.at(off)
}

// mergeComponentProgram joins the files of a component into one program the
// checker can judge, the way cmd/bp resolveIncludes splices included blocks
// into the including file. Include statements and duplicate blueprint blocks
// are dropped. A single file is returned as it is.
func mergeComponentProgram(files []*ast.File) *ast.File {
	if len(files) == 1 {
		return files[0]
	}
	merged := &ast.File{}
	for _, f := range files {
		if f == nil {
			continue
		}
		if merged.Blueprint == nil && f.Blueprint != nil {
			merged.Blueprint = f.Blueprint
		}
		for _, b := range f.Blocks {
			if _, ok := b.(*ast.Include); ok {
				continue
			}
			merged.Blocks = append(merged.Blocks, b)
		}
	}
	return merged
}
