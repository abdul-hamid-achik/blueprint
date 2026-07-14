package lsp

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// LSP SymbolKind values used by SymbolInformation.
const (
	symbolKindModule     = 2
	symbolKindNamespace  = 3
	symbolKindClass      = 5
	symbolKindMethod     = 6
	symbolKindField      = 8
	symbolKindEnum       = 10
	symbolKindInterface  = 11
	symbolKindFunction   = 12
	symbolKindVariable   = 13
	symbolKindString     = 15
	symbolKindObject     = 19
	symbolKindEnumMember = 22
	symbolKindStruct     = 23
	symbolKindEvent      = 24
)

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type workspaceSymbol struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	Location      lspLocation `json:"location"`
	ContainerName string      `json:"containerName,omitempty"`
}

type indexedWorkspaceDocument struct {
	uri  string
	text string
	idx  *docIndex
}

func (s *Server) handleWorkspaceSymbol(msg *jsonRPCMessage) error {
	var params struct {
		Query string `json:"query"`
	}
	if err := jsonUnmarshalParams(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}
	docs := s.workspaceDocuments()
	items := make([]workspaceSymbol, 0, len(docs)*4)
	for _, doc := range docs {
		items = append(items, documentWorkspaceSymbols(doc.uri, doc.text, doc.idx.file, params.Query)...)
	}
	sortWorkspaceSymbols(items)
	return s.sendResult(msg.ID, items)
}

func (s *Server) handleDidChangeWorkspaceFolders(msg *jsonRPCMessage) error {
	var params struct {
		Event struct {
			Added []struct {
				URI string `json:"uri"`
			} `json:"added"`
			Removed []struct {
				URI string `json:"uri"`
			} `json:"removed"`
		} `json:"event"`
	}
	if err := jsonUnmarshalParams(msg.Params, &params); err != nil {
		// This method is normally a notification. Notifications cannot receive a
		// JSON-RPC error response, so malformed ones are safely ignored.
		if msg.ID == nil {
			return nil
		}
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}
	removed := make(map[string]bool, len(params.Event.Removed))
	for _, folder := range params.Event.Removed {
		if root, ok := localWorkspaceRoot(folder.URI); ok {
			removed[root] = true
		}
	}
	roots := make([]string, 0, len(s.workspaceRoots)+len(params.Event.Added))
	for _, root := range s.workspaceRoots {
		if !removed[root] {
			roots = append(roots, root)
		}
	}
	for _, folder := range params.Event.Added {
		if root, ok := localWorkspaceRoot(folder.URI); ok {
			roots = append(roots, root)
		}
	}
	s.workspaceRoots = normalizeWorkspaceRoots(roots)
	if msg.ID != nil {
		return s.sendResult(msg.ID, nil)
	}
	return nil
}

func localWorkspaceRoot(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	path := value
	if !filepath.IsAbs(value) {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
			return "", false
		}
		path = u.Path
	}
	abs, err := filepath.Abs(filepath.FromSlash(path))
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

func normalizeWorkspaceRoots(roots []string) []string {
	seen := make(map[string]bool, len(roots))
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = filepath.Clean(root)
		if root == "." || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

func filenameToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Clean(path))}).String()
}

func (s *Server) workspaceDocuments() []indexedWorkspaceDocument {
	byPath := make(map[string]indexedWorkspaceDocument)
	for uri, doc := range s.docs {
		key := documentIdentity(uri)
		byPath[key] = indexedWorkspaceDocument{uri: uri, text: doc.Text, idx: s.getIndex(uri)}
	}

	for _, root := range s.workspaceRoots {
		info, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if strings.EqualFold(filepath.Ext(root), ".bp") {
				s.addDiskWorkspaceDocument(byPath, root)
			}
			continue
		}
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				if path != root && ignoredWorkspaceDirectory(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".bp") {
				return nil
			}
			s.addDiskWorkspaceDocument(byPath, path)
			return nil
		})
	}

	docs := make([]indexedWorkspaceDocument, 0, len(byPath))
	for _, doc := range byPath {
		if doc.idx != nil {
			docs = append(docs, doc)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].uri < docs[j].uri })
	return docs
}

func (s *Server) addDiskWorkspaceDocument(byPath map[string]indexedWorkspaceDocument, path string) {
	key := filepath.Clean(path)
	if _, open := byPath[key]; open {
		return
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return
	}
	uri := filenameToURI(path)
	source := string(text)
	byPath[key] = indexedWorkspaceDocument{uri: uri, text: source, idx: buildIndex(uri, source)}
}

func documentIdentity(uri string) string {
	if path := uriToFilename(uri); path != uri {
		if abs, err := filepath.Abs(path); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(path)
	}
	return uri
}

func ignoredWorkspaceDirectory(name string) bool {
	switch name {
	case ".git", ".blueprint", "node_modules", "vendor", "dist", "build", "generated", ".venv", "venv", "__pycache__":
		return true
	}
	return false
}

func documentWorkspaceSymbols(uri, text string, file *ast.File, query string) []workspaceSymbol {
	if file == nil {
		return []workspaceSymbol{}
	}
	query = strings.ToLower(strings.TrimSpace(query))
	items := make([]workspaceSymbol, 0, len(file.Blocks)*2)
	add := func(name string, kind int, loc lexer.Loc, container string) {
		if name == "" || query != "" && !strings.Contains(strings.ToLower(name+" "+container), query) {
			return
		}
		items = append(items, workspaceSymbol{
			Name:          name,
			Kind:          kind,
			Location:      lspLocation{URI: uri, Range: declarationRange(text, loc, name)},
			ContainerName: container,
		})
	}
	if file.Blueprint != nil {
		add(file.Blueprint.Name, symbolKindModule, file.Blueprint.Loc, "")
	}
	for _, block := range file.Blocks {
		switch b := block.(type) {
		case *ast.Secret:
			add(b.Name, symbolKindVariable, b.Loc, "secrets")
		case *ast.Env:
			add(b.Name, symbolKindVariable, b.Loc, "environment")
		case *ast.Locale:
			add(b.Code, symbolKindString, b.Loc, "locales")
		case *ast.Translation:
			add(b.Name, symbolKindNamespace, b.Loc, "translations")
		case *ast.StateMachine:
			add(b.Name, symbolKindEnum, b.Loc, "state")
		case *ast.Analytics:
			add(b.Name, symbolKindNamespace, b.Loc, "analytics")
		case *ast.SaveSchema:
			add(b.Name, symbolKindStruct, b.Loc, "save schemas")
		case *ast.TypeDecl:
			add(b.Name, symbolKindStruct, b.Loc, "types")
			for _, field := range b.Fields {
				if field != nil {
					add(field.Name, symbolKindField, field.Loc, b.Name)
				}
			}
		case *ast.Alias:
			add(b.Name, symbolKindInterface, b.Loc, "aliases")
		case *ast.Enum:
			add(b.Name, symbolKindEnum, b.Loc, "enums")
			for _, variant := range b.Variants {
				if variant != nil {
					add(variant.Name, symbolKindEnumMember, variant.Loc, b.Name)
				}
			}
		case *ast.Model:
			add(b.Name, symbolKindClass, b.Loc, "models")
			for _, field := range b.Fields {
				if field != nil {
					add(field.Name, symbolKindField, field.Loc, b.Name)
				}
			}
			for _, field := range b.ComputedFields {
				if field != nil {
					add(field.Name, symbolKindField, field.Loc, b.Name)
				}
			}
		case *ast.Content:
			add(b.Name, symbolKindClass, b.Loc, "content")
			for _, field := range b.Fields {
				if field != nil {
					add(field.Name, symbolKindField, field.Loc, b.Name)
				}
			}
		case *ast.Fn:
			add(b.Name, symbolKindFunction, b.Loc, "functions")
		case *ast.Pipe:
			add(b.Name, symbolKindFunction, b.Loc, "pipes")
		case *ast.Middleware:
			add(b.Name, symbolKindFunction, b.Loc, "middleware")
		case *ast.Endpoint:
			add(b.Method+" "+b.Path, symbolKindMethod, b.Loc, "endpoints")
		case *ast.StreamEndpoint:
			add("STREAM "+b.Path, symbolKindMethod, b.Loc, "endpoints")
		case *ast.WsEndpoint:
			add("WS "+b.Path, symbolKindMethod, b.Loc, "endpoints")
		case *ast.Worker:
			add(b.Name, symbolKindFunction, b.Loc, "workers")
		case *ast.Schedule:
			add(b.Name, symbolKindEvent, b.Loc, "schedules")
		case *ast.External:
			add(b.Name, symbolKindObject, b.Loc, "external services")
		case *ast.Subscribe:
			add(b.Event, symbolKindEvent, b.Loc, "subscriptions")
		case *ast.Test:
			add(b.Name, symbolKindMethod, b.Loc, "tests")
		case *ast.TestGroup:
			add(b.Name, symbolKindNamespace, b.Loc, "test groups")
		case *ast.Fixture:
			add(b.Name, symbolKindObject, b.Loc, "fixtures")
		}
	}
	sortWorkspaceSymbols(items)
	return items
}

func declarationRange(text string, loc lexer.Loc, name string) lspRange {
	lines := strings.Split(text, "\n")
	lineNo := loc.Line - 1
	if lineNo < 0 || lineNo >= len(lines) {
		return locToRange(loc)
	}
	line := lines[lineNo]
	start := loc.Col - 1
	if start < 0 || start > len(line) {
		start = 0
	}
	if at := strings.Index(line[start:], name); at >= 0 {
		start += at
		return lspRange{
			Start: lspPosition{Line: lineNo, Character: utf16Length(line[:start])},
			End:   lspPosition{Line: lineNo, Character: utf16Length(line[:start+len(name)])},
		}
	}
	return locToRange(loc)
}

func sortWorkspaceSymbols(items []workspaceSymbol) {
	sort.SliceStable(items, func(i, j int) bool {
		left := strings.ToLower(items[i].Name)
		right := strings.ToLower(items[j].Name)
		if left != right {
			return left < right
		}
		if items[i].Location.URI != items[j].Location.URI {
			return items[i].Location.URI < items[j].Location.URI
		}
		if items[i].Location.Range.Start.Line != items[j].Location.Range.Start.Line {
			return items[i].Location.Range.Start.Line < items[j].Location.Range.Start.Line
		}
		if items[i].Location.Range.Start.Character != items[j].Location.Range.Start.Character {
			return items[i].Location.Range.Start.Character < items[j].Location.Range.Start.Character
		}
		return items[i].Kind < items[j].Kind
	})
}
