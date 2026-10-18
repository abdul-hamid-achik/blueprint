package lsp

import (
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// symbolKindFile is the LSP SymbolKind for File. Used by include declarations.
const symbolKindFile = 1

// documentSymbol is the hierarchical LSP DocumentSymbol shape.
type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          lspRange         `json:"range"`
	SelectionRange lspRange         `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

// handleDocumentSymbol handles textDocument/documentSymbol. It parses the way
// `bp fmt` does, which accepts a file-level intent before the blueprint block
// (the partial index used for hover and diagnostics does not). Parse errors
// still yield the recovered outline.
func (s *Server) handleDocumentSymbol(msg *jsonRPCMessage) error {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := jsonUnmarshalParams(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}

	symbols := []documentSymbol{}
	if doc, ok := s.docs[params.TextDocument.URI]; ok {
		file, _ := parseForFormat(uriToFilename(params.TextDocument.URI), []byte(doc.Text))
		symbols = documentSymbols(doc.Text, file)
	}
	return s.sendResult(msg.ID, symbols)
}

// symbolNode is an intermediate declaration. Only the start offset is known
// from the AST; the end of each declaration is recovered from the token
// stream in symbolBuilder.build.
type symbolNode struct {
	offset   int
	name     string
	kind     int
	children []*symbolNode
}

// documentSymbols builds the outline for every top-level declaration kind in
// the AST, with fields, computed fields, and enum variants as children.
func documentSymbols(text string, file *ast.File) []documentSymbol {
	var nodes []*symbolNode
	if file.Blueprint != nil {
		name := file.Blueprint.Name
		if name == "" {
			name = "blueprint"
		}
		nodes = append(nodes, &symbolNode{offset: file.Blueprint.Loc.Offset, name: name, kind: symbolKindModule})
	}
	for _, block := range file.Blocks {
		if node := topLevelSymbolNode(block); node != nil {
			nodes = append(nodes, node)
		}
	}
	b := &symbolBuilder{
		lines:  newLineIndex(text),
		tokens: file.SourceTokens,
		match:  matchBraces(file.SourceTokens),
	}
	return b.build(nodes, len(file.SourceTokens))
}

// topLevelSymbolNode maps a top-level block to its outline node. Kinds follow
// the workspace symbol index so both views agree. Returns nil for blocks that
// have no name to show.
func topLevelSymbolNode(block ast.TopLevel) *symbolNode {
	switch b := block.(type) {
	case *ast.Secret:
		return leafSymbol(b.Loc, b.Name, symbolKindVariable)
	case *ast.Env:
		return leafSymbol(b.Loc, b.Name, symbolKindVariable)
	case *ast.Locale:
		return leafSymbol(b.Loc, b.Code, symbolKindString)
	case *ast.Translation:
		return leafSymbol(b.Loc, b.Name, symbolKindNamespace)
	case *ast.StateMachine:
		return leafSymbol(b.Loc, b.Name, symbolKindEnum)
	case *ast.Analytics:
		return leafSymbol(b.Loc, b.Name, symbolKindNamespace)
	case *ast.SaveSchema:
		return leafSymbol(b.Loc, b.Name, symbolKindStruct)
	case *ast.Include:
		return leafSymbol(b.Loc, b.Path, symbolKindFile)
	case *ast.TypeDecl:
		return containerSymbol(b.Loc, b.Name, symbolKindStruct, fieldSymbols(b.Fields, nil))
	case *ast.Alias:
		return leafSymbol(b.Loc, b.Name, symbolKindInterface)
	case *ast.Enum:
		variants := make([]*symbolNode, 0, len(b.Variants))
		for _, variant := range b.Variants {
			if variant != nil {
				if node := leafSymbol(variant.Loc, variant.Name, symbolKindEnumMember); node != nil {
					variants = append(variants, node)
				}
			}
		}
		return containerSymbol(b.Loc, b.Name, symbolKindEnum, variants)
	case *ast.Model:
		return containerSymbol(b.Loc, b.Name, symbolKindClass, fieldSymbols(b.Fields, b.ComputedFields))
	case *ast.Content:
		return containerSymbol(b.Loc, b.Name, symbolKindClass, fieldSymbols(b.Fields, nil))
	case *ast.Fn:
		return leafSymbol(b.Loc, b.Name, symbolKindFunction)
	case *ast.Pipe:
		return leafSymbol(b.Loc, b.Name, symbolKindFunction)
	case *ast.Middleware:
		return leafSymbol(b.Loc, b.Name, symbolKindFunction)
	case *ast.Endpoint:
		return leafSymbol(b.Loc, b.Method+" "+b.Path, symbolKindMethod)
	case *ast.StreamEndpoint:
		return leafSymbol(b.Loc, "STREAM "+b.Path, symbolKindMethod)
	case *ast.WsEndpoint:
		return leafSymbol(b.Loc, "WS "+b.Path, symbolKindMethod)
	case *ast.Worker:
		return leafSymbol(b.Loc, b.Name, symbolKindFunction)
	case *ast.Schedule:
		return leafSymbol(b.Loc, b.Name, symbolKindEvent)
	case *ast.External:
		return leafSymbol(b.Loc, b.Name, symbolKindObject)
	case *ast.Subscribe:
		return leafSymbol(b.Loc, b.Event, symbolKindEvent)
	case *ast.Test:
		return leafSymbol(b.Loc, b.Name, symbolKindMethod)
	case *ast.TestGroup:
		return leafSymbol(b.Loc, b.Name, symbolKindNamespace)
	case *ast.Fixture:
		return leafSymbol(b.Loc, b.Name, symbolKindObject)
	}
	return nil
}

func leafSymbol(loc lexer.Loc, name string, kind int) *symbolNode {
	return containerSymbol(loc, name, kind, nil)
}

func containerSymbol(loc lexer.Loc, name string, kind int, children []*symbolNode) *symbolNode {
	if name == "" {
		return nil
	}
	return &symbolNode{offset: loc.Offset, name: name, kind: kind, children: children}
}

// fieldSymbols lists fields and computed fields in source order. Computed
// fields are read-only properties but still shown as fields in the outline.
func fieldSymbols(fields []*ast.Field, computed []*ast.ComputedField) []*symbolNode {
	nodes := make([]*symbolNode, 0, len(fields)+len(computed))
	for _, field := range fields {
		if field != nil {
			if node := leafSymbol(field.Loc, field.Name, symbolKindField); node != nil {
				nodes = append(nodes, node)
			}
		}
	}
	for _, field := range computed {
		if field != nil {
			if node := leafSymbol(field.Loc, field.Name, symbolKindField); node != nil {
				nodes = append(nodes, node)
			}
		}
	}
	return nodes
}

// symbolBuilder turns symbolNodes into DocumentSymbols. A declaration runs
// from its first token up to the token before the next sibling starts (or the
// enclosing body's closing brace). That is exact for every declaration because
// each one is followed by the next declaration or by its own closing brace.
type symbolBuilder struct {
	lines  lineIndex
	tokens []lexer.Token
	match  map[int]int
}

// build returns symbols for nodes, which all live before token index limit.
func (b *symbolBuilder) build(nodes []*symbolNode, limit int) []documentSymbol {
	type placed struct {
		node     *symbolNode
		tok      int // keyword token; where the range starts
		boundary int // first token of the declaration's intent, else tok
	}
	items := make([]placed, 0, len(nodes))
	for _, node := range nodes {
		tok := tokenIndexAt(b.tokens, node.offset)
		items = append(items, placed{node: node, tok: tok, boundary: b.intentStart(tok)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].tok < items[j].tok })

	out := make([]documentSymbol, 0, len(items))
	for i, item := range items {
		// The next declaration's intent belongs to that declaration, so it
		// must not be swallowed into this one's range.
		next := limit
		if i+1 < len(items) {
			next = items[i+1].boundary
		}
		end := b.lastTokenBefore(item.tok, next)

		var children []documentSymbol
		if len(item.node.children) > 0 {
			bodyEnd := b.bodyClose(item.tok, end+1)
			children = b.build(item.node.children, bodyEnd)
		}

		startOffset := b.tokens[item.tok].Loc.Offset
		endToken := b.tokens[end]
		declRange := lspRange{
			Start: b.lines.position(startOffset),
			End:   b.lines.position(endToken.Loc.Offset + endToken.Loc.Len),
		}
		out = append(out, documentSymbol{
			Name:           item.node.name,
			Kind:           item.node.kind,
			Range:          declRange,
			SelectionRange: b.selectionRange(startOffset, item.node.name, declRange),
			Children:       children,
		})
	}
	return out
}

// lastTokenBefore returns the index of the last non-EOF token in [start, next).
func (b *symbolBuilder) lastTokenBefore(start, next int) int {
	j := next - 1
	if j >= len(b.tokens) {
		j = len(b.tokens) - 1
	}
	for j > start && b.tokens[j].Kind == lexer.TokenEOF {
		j--
	}
	if j < start {
		return start
	}
	return j
}

// intentStart returns the index of the `@` opening an intent (`@ "..."`) that
// directly precedes the declaration keyword at tok, or tok when there is none.
// The intent is documentation, so ranges start at the keyword; this index only
// marks where the previous declaration must stop.
func (b *symbolBuilder) intentStart(tok int) int {
	if tok >= 2 && b.tokens[tok-2].Kind == lexer.TokenIntent && b.tokens[tok-1].Kind == lexer.TokenString {
		return tok - 2
	}
	return tok
}

// bodyClose returns the index of the `}` closing the first brace block that
// opens in [from, to). Falls back to to when there is no matched block.
func (b *symbolBuilder) bodyClose(from, to int) int {
	for j := from; j < to && j < len(b.tokens); j++ {
		if b.tokens[j].Kind == lexer.TokenLBrace {
			if close, ok := b.match[j]; ok {
				return close
			}
			return to
		}
	}
	return to
}

// selectionRange narrows a declaration to its name. The AST location points at
// the keyword (or the HTTP method for endpoints), so the name is looked for on
// the same line: first at the start, then after the leading keyword. If the
// name cannot be found, the leading word is selected instead.
func (b *symbolBuilder) selectionRange(startOffset int, name string, declRange lspRange) lspRange {
	rest := b.lines.lineRestAt(startOffset)
	at := -1
	if strings.HasPrefix(rest, name) {
		at = 0
	} else {
		i := strings.IndexAny(rest, " \t")
		if i >= 0 {
			rest2 := strings.TrimLeft(rest[i:], " \t")
			if j := strings.Index(rest2, name); j >= 0 {
				at = len(rest) - len(rest2) + j
			}
		}
	}
	if at < 0 {
		word := rest
		if i := strings.IndexAny(rest, " \t"); i >= 0 {
			word = rest[:i]
		}
		return lspRange{Start: declRange.Start, End: b.lines.position(startOffset + len(word))}
	}
	return lspRange{
		Start: b.lines.position(startOffset + at),
		End:   b.lines.position(startOffset + at + len(name)),
	}
}
