package lsp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// LSP CompletionItemKind values. Keeping these local avoids pulling a protocol
// dependency into the compiler just for a handful of wire constants.
const (
	completionKindFunction = 3
	completionKindField    = 5
	completionKindVariable = 6
	completionKindClass    = 7
	completionKindKeyword  = 14
	completionKindSnippet  = 15
	completionKindStruct   = 22
	completionKindEnum     = 13
)

type completionItem struct {
	Label            string `json:"label"`
	Kind             int    `json:"kind"`
	Detail           string `json:"detail,omitempty"`
	Documentation    string `json:"documentation,omitempty"`
	InsertText       string `json:"insertText,omitempty"`
	InsertTextFormat int    `json:"insertTextFormat,omitempty"`
	SortText         string `json:"sortText,omitempty"`
}

type completionCandidate struct {
	completionItem
	priority int
}

type sourceFrame struct {
	kind      string
	startLine int
}

var (
	fieldAccessSuffix = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z0-9_]*)$`)
	modelArgSuffix    = regexp.MustCompile(`\b(?:query|fetch|save|update|delete|count|seed)\s+([A-Za-z0-9_]*)$`)
	useArgSuffix      = regexp.MustCompile(`\buse\s+([A-Za-z0-9_]*)$`)
	pipeArgSuffix     = regexp.MustCompile(`\bpipe\s+([A-Za-z0-9_]*)$`)
	callArgSuffix     = regexp.MustCompile(`\bcall\s+([A-Za-z0-9_-]*)$`)
	refArgSuffix      = regexp.MustCompile(`\bref\(\s*([A-Za-z0-9_]*)$`)
	blueprintValue    = regexp.MustCompile(`\b(runtime|database|cache|storage)\s+([A-Za-z0-9_-]*)$`)
	queryModel        = regexp.MustCompile(`\bquery\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	withOpen          = regexp.MustCompile(`\bwith\s*\(`)
	identifierPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	inputLine         = regexp.MustCompile(`^\s*<-\s+([A-Za-z_][A-Za-z0-9_]*)\s+([^\s]+)`)
	bindingLine       = regexp.MustCompile(`^\s*\|>\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)
	rowBinding        = regexp.MustCompile(`^(?:fetch|save)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	firstQueryBinding = regexp.MustCompile(`^query\s+([A-Za-z_][A-Za-z0-9_]*)\b.*\bfirst\b`)
)

// handleCompletion returns a deterministic CompletionItem array. An empty
// array (rather than null) is intentional: it tells clients that completion
// succeeded but this source position has no Blueprint suggestions.
func (s *Server) handleCompletion(msg *jsonRPCMessage) error {
	var params struct {
		TextDocumentPositionParams
	}
	if err := jsonUnmarshalParams(msg.Params, &params); err != nil {
		return s.sendError(msg.ID, -32602, "Invalid params", err.Error())
	}
	idx := s.getIndex(params.TextDocument.URI)
	if idx == nil {
		return s.sendResult(msg.ID, []completionItem{})
	}
	items := computeCompletions(idx, params.Position.Line, params.Position.Character)
	return s.sendResult(msg.ID, items)
}

// computeCompletions uses lightweight source context in addition to the
// partial AST. The source scan matters while a declaration is incomplete and
// therefore intentionally absent from the parser's recovery AST.
func computeCompletions(idx *docIndex, line, character int) []completionItem {
	if idx == nil || line < 0 {
		return []completionItem{}
	}
	linePrefix, ok := sourcePrefixAt(idx.text, line, character)
	if !ok || positionInCommentOrString(idx.text, line, character) {
		return []completionItem{}
	}
	prefix := wordPrefix(linePrefix)
	trimmed := strings.TrimSpace(linePrefix)
	frame := sourceFrameAt(idx.text, line, character)

	// A dot is the strongest completion context. Never fall back to unrelated
	// keywords after it: returning no entries is safer than suggesting an
	// invalid field on an unknown value.
	if match := fieldAccessSuffix.FindStringSubmatch(linePrefix); match != nil {
		return finishCompletions(fieldCompletions(idx, frame, line, match[1]), match[2])
	}
	if model, existing, relationPrefix, ok := relationshipContext(linePrefix); ok {
		return finishCompletions(relationshipCompletions(idx.file, model, existing), relationPrefix)
	}

	if match := useArgSuffix.FindStringSubmatch(linePrefix); match != nil {
		return finishCompletions(middlewareCompletions(idx.file), match[1])
	}
	if match := modelArgSuffix.FindStringSubmatch(linePrefix); match != nil {
		return finishCompletions(modelCompletions(idx.file), match[1])
	}
	if match := refArgSuffix.FindStringSubmatch(linePrefix); match != nil {
		return finishCompletions(modelCompletions(idx.file), match[1])
	}
	if match := pipeArgSuffix.FindStringSubmatch(linePrefix); match != nil && strings.Contains(linePrefix, "|>") {
		return finishCompletions(pipeCompletions(idx.file, true), match[1])
	}
	if match := callArgSuffix.FindStringSubmatch(linePrefix); match != nil && strings.Contains(linePrefix, "|>") {
		return finishCompletions(externalCompletions(idx.file), match[1])
	}
	if frame.kind == "blueprint" {
		if match := blueprintValue.FindStringSubmatch(linePrefix); match != nil {
			return finishCompletions(blueprintValueCompletions(match[1]), match[2])
		}
	}

	if isFieldBlock(frame.kind) {
		if frame.kind == "model" {
			if position, computed := computedFieldPosition(linePrefix); computed {
				switch position {
				case "keyword":
					return finishCompletions([]completionCandidate{computedFieldSnippet()}, prefix)
				case "type":
					return finishCompletions(typeCompletions(idx.file), prefix)
				case "equals":
					return finishCompletions([]completionCandidate{candidate("=", completionKindKeyword, "computed expression", "= ${1:expression}", 5)}, "")
				case "expression":
					items := computedExpressionCompletions(idx, frame, line)
					items = append(items, expressionCompletions(idx, frame, line)...)
					return finishCompletions(items, prefix)
				default:
					return []completionItem{}
				}
			}
			if trimmed == "" {
				return finishCompletions([]completionCandidate{computedFieldSnippet()}, "")
			}
		}
		if fieldPosition(linePrefix) == "type" {
			return finishCompletions(typeCompletions(idx.file), prefix)
		}
		if fieldPosition(linePrefix) == "constraint" {
			items := constraintCompletions()
			items = append(items, refModelCompletions(idx.file)...)
			return finishCompletions(items, prefix)
		}
		return []completionItem{}
	}

	if strings.HasPrefix(trimmed, "<-") || strings.HasPrefix(trimmed, "->") {
		if strings.HasPrefix(trimmed, "->") && frame.kind == "fn" {
			return finishCompletions(typeCompletions(idx.file), prefix)
		}
		switch arrowDeclarationPosition(linePrefix) {
		case "type":
			return finishCompletions(typeCompletions(idx.file), prefix)
		case "constraint":
			return finishCompletions(constraintCompletions(), prefix)
		case "expression":
			return finishCompletions(expressionCompletions(idx, frame, line), prefix)
		}
	}

	if frame.kind == "" {
		return finishCompletions(topLevelCompletions(idx.file), prefix)
	}
	if frame.kind == "blueprint" {
		return finishCompletions(blueprintCompletions(idx.file), prefix)
	}

	if strings.HasPrefix(trimmed, "|>") {
		items := make([]completionCandidate, 0, 24)
		if shouldOfferStepOps(trimmed) {
			items = append(items, stepCompletions()...)
		}
		items = append(items, expressionCompletions(idx, frame, line)...)
		return finishCompletions(items, prefix)
	}

	items := make([]completionCandidate, 0, 24)
	switch frame.kind {
	case "fn":
		items = append(items, fnBodyCompletions()...)
	case "middleware":
		items = append(items, keywordCandidate("before", "before {\n  |> ${1:step}\n}", "middleware before phase", 10))
		items = append(items, keywordCandidate("after", "after {\n  |> ${1:step}\n}", "middleware after phase", 11))
	case "endpoint", "stream", "ws":
		items = append(items, endpointMetaCompletions()...)
		items = append(items, arrowCompletions()...)
	case "worker":
		items = append(items, workerMetaCompletions()...)
		items = append(items, arrowCompletions()...)
	case "arrow", "pipe", "schedule", "subscribe":
		items = append(items, arrowCompletions()...)
	case "external":
		items = append(items, externalEntryCompletions()...)
	}
	return finishCompletions(items, prefix)
}

func shouldOfferStepOps(trimmed string) bool {
	expr := strings.TrimSpace(strings.TrimPrefix(trimmed, "|>"))
	if at := strings.Index(expr, "="); at >= 0 {
		expr = strings.TrimSpace(expr[at+1:])
	}
	return len(strings.Fields(expr)) <= 1
}

func sourcePrefixAt(text string, line, character int) (string, bool) {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return "", false
	}
	src := lines[line]
	offset := byteOffsetForUTF16(src, character)
	return src[:offset], true
}

func byteOffsetForUTF16(s string, character int) int {
	if character <= 0 {
		return 0
	}
	units := 0
	for offset, r := range s {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > character {
			return offset
		}
		units += width
	}
	return len(s)
}

func utf16Length(s string) int {
	if utf8.RuneCountInString(s) == len(s) {
		return len(s)
	}
	return len(utf16.Encode([]rune(s)))
}

func positionInCommentOrString(text string, targetLine, character int) bool {
	lines := strings.Split(text, "\n")
	if targetLine < 0 || targetLine >= len(lines) {
		return false
	}
	inString := false
	escaped := false
	for lineNo := 0; lineNo <= targetLine; lineNo++ {
		line := lines[lineNo]
		limit := len(line)
		if lineNo == targetLine {
			limit = byteOffsetForUTF16(line, character)
		}
		for i := 0; i < limit; i++ {
			ch := line[i]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == '"' {
					inString = false
				}
				continue
			}
			if ch == '#' {
				if lineNo == targetLine {
					return true
				}
				break
			}
			if ch == '"' {
				inString = true
			}
		}
		// The newline itself consumes an escape in the lexer. The string remains
		// open, but the first byte on the next line is not escaped.
		escaped = false
	}
	return inString
}

func wordPrefix(prefix string) string {
	i := len(prefix)
	for i > 0 && isWordByte(prefix[i-1]) {
		i--
	}
	return prefix[i:]
}

// sourceFrameAt tracks braces while ignoring comments and strings. Unknown
// object-literal braces remain on the stack but are skipped when choosing the
// enclosing language block.
func sourceFrameAt(text string, targetLine, character int) sourceFrame {
	lines := strings.Split(text, "\n")
	if targetLine < 0 || targetLine >= len(lines) {
		return sourceFrame{}
	}
	stack := make([]sourceFrame, 0, 4)
	inString := false
	escaped := false
	for lineNo := 0; lineNo <= targetLine; lineNo++ {
		line := lines[lineNo]
		limit := len(line)
		if lineNo == targetLine {
			limit = byteOffsetForUTF16(line, character)
		}
		for i := 0; i < limit; i++ {
			ch := line[i]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
				} else if ch == '"' {
					inString = false
				}
				continue
			}
			if ch == '#' {
				break
			}
			if ch == '"' {
				inString = true
				continue
			}
			switch ch {
			case '{':
				header := strings.TrimSpace(line[:i])
				stack = append(stack, sourceFrame{kind: classifyBlockHeader(header), startLine: lineNo})
			case '}':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
		escaped = false
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].kind != "" {
			return stack[i]
		}
	}
	return sourceFrame{}
}

func classifyBlockHeader(header string) string {
	fields := strings.Fields(header)
	if len(fields) == 0 {
		return ""
	}
	first := fields[0]
	if first == "|>" && len(fields) > 1 {
		first = fields[1]
	}
	switch first {
	case "blueprint":
		return "blueprint"
	case "model", "content", "type":
		return first
	case "fn":
		return "fn"
	case "enum":
		return "enum"
	case "pipe":
		return "pipe"
	case "middleware":
		return "middleware"
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return "endpoint"
	case "STREAM":
		return "stream"
	case "WS":
		return "ws"
	case "worker":
		return "worker"
	case "schedule":
		return "schedule"
	case "subscribe":
		return "subscribe"
	case "external":
		return "external"
	case "state", "analytics", "save", "translation", "test", "test_group", "fixture":
		return first
	case "before", "after", "logic", "on_fail", "on_connect", "on_message", "on_disconnect", "when", "try", "recover", "setup", "cleanup":
		return "arrow"
	case "request":
		return "request"
	case "expect":
		return "expect"
	}
	return ""
}

func isFieldBlock(kind string) bool {
	return kind == "model" || kind == "content" || kind == "type"
}

func computedFieldPosition(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 || !strings.HasPrefix("computed", fields[0]) {
		return "", false
	}
	if fields[0] != "computed" {
		return "keyword", true
	}
	if strings.Contains(trimmed, "=") {
		return "expression", true
	}
	trailing := len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t')
	switch {
	case len(fields) == 1:
		return "name", true
	case len(fields) == 2 && trailing:
		return "type", true
	case len(fields) == 2:
		return "name", true
	case len(fields) == 3 && trailing:
		return "equals", true
	case len(fields) == 3:
		return "type", true
	default:
		return "equals", true
	}
}

func computedFieldSnippet() completionCandidate {
	return candidate("computed", completionKindSnippet, "read-only derived model field", "computed ${1:name} ${2:string} = ${3:expression}", 5)
}

func fieldPosition(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "name"
	}
	fields := strings.Fields(trimmed)
	trailing := len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t')
	if len(fields) == 1 && trailing || len(fields) == 2 && !trailing {
		return "type"
	}
	if len(fields) >= 2 {
		return "constraint"
	}
	return "name"
}

func arrowDeclarationPosition(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "->") {
		fields := strings.Fields(trimmed)
		if len(fields) == 1 {
			return "expression"
		}
		secondIsStatus := len(fields[1]) == 3 && fields[1][0] >= '1' && fields[1][0] <= '5'
		if secondIsStatus {
			return "expression"
		}
		trailing := len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t')
		if len(fields) >= 3 || trailing {
			return "type"
		}
		return "expression"
	}
	fields := strings.Fields(trimmed)
	trailing := len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t')
	if len(fields) == 2 && trailing || len(fields) == 3 && !trailing {
		return "type"
	}
	if len(fields) >= 3 {
		return "constraint"
	}
	return "name"
}

func candidate(label string, kind int, detail, insert string, priority int) completionCandidate {
	format := 1
	if strings.Contains(insert, "${") {
		format = 2
	}
	return completionCandidate{
		completionItem: completionItem{Label: label, Kind: kind, Detail: detail, InsertText: insert, InsertTextFormat: format},
		priority:       priority,
	}
}

func keywordCandidate(label, insert, detail string, priority int) completionCandidate {
	return candidate(label, completionKindKeyword, detail, insert, priority)
}

func finishCompletions(candidates []completionCandidate, prefix string) []completionItem {
	prefix = strings.ToLower(prefix)
	seen := make(map[string]bool, len(candidates))
	filtered := make([]completionCandidate, 0, len(candidates))
	for _, item := range candidates {
		if item.Label == "" || seen[item.Label] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(item.Label), prefix) {
			continue
		}
		seen[item.Label] = true
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].priority != filtered[j].priority {
			return filtered[i].priority < filtered[j].priority
		}
		return strings.ToLower(filtered[i].Label) < strings.ToLower(filtered[j].Label)
	})
	out := make([]completionItem, 0, len(filtered))
	for i, item := range filtered {
		item.SortText = fmt.Sprintf("%04d:%04d:%s", item.priority, i, strings.ToLower(item.Label))
		out = append(out, item.completionItem)
	}
	return out
}

func topLevelCompletions(file *ast.File) []completionCandidate {
	items := []completionCandidate{
		candidate("blueprint", completionKindSnippet, "service configuration", "blueprint \"${1:service}\" {\n  version \"${2:0.1.0}\"\n  port ${3:3000}\n  runtime ${4:node}\n}", 10),
		candidate("model", completionKindSnippet, "database model", "model ${1:name} {\n  id uuid primary\n  ${2:field} ${3:string} required\n}", 20),
		candidate("content", completionKindSnippet, "managed content model", "content ${1:name} {\n  ${2:title} ${3:string} required\n}", 21),
		candidate("fn", completionKindSnippet, "function", "fn ${1:name} {\n  <- ${2:input} ${3:string}\n  -> ${4:output} ${5:string}\n\n  impl node {\n    module: \"./internal/${1:name}\"\n  }\n}", 22),
		candidate("pipe", completionKindSnippet, "reusable pipeline", "pipe ${1:name} {\n  <- ${2:input} ${3:string}\n  |> ${4:step}\n  -> ${2:input}\n}", 23),
		candidate("middleware", completionKindSnippet, "request middleware", "middleware ${1:name} {\n  before {\n    |> ${2:step}\n  }\n}", 24),
		candidate("GET endpoint", completionKindSnippet, "HTTP endpoint", "GET ${1:/path} {\n  ${2:|> step}\n  -> 200 ${3:{ ok: true }}\n}", 30),
		candidate("POST endpoint", completionKindSnippet, "HTTP endpoint", "POST ${1:/path} {\n  <- ${2:input} ${3:string} required\n  ${4:|> step}\n  -> 201 ${5:{ ok: true }}\n}", 31),
		candidate("PUT endpoint", completionKindSnippet, "HTTP endpoint", "PUT ${1:/path} {\n  ${2:|> step}\n  -> 200 ${3:{ ok: true }}\n}", 32),
		candidate("PATCH endpoint", completionKindSnippet, "HTTP endpoint", "PATCH ${1:/path} {\n  ${2:|> step}\n  -> 200 ${3:{ ok: true }}\n}", 33),
		candidate("DELETE endpoint", completionKindSnippet, "HTTP endpoint", "DELETE ${1:/path} {\n  ${2:|> step}\n  -> 204 null\n}", 34),
		candidate("STREAM endpoint", completionKindSnippet, "server-sent event endpoint", "STREAM ${1:/path} {\n  ${2:|> step}\n}", 35),
		candidate("WS endpoint", completionKindSnippet, "WebSocket endpoint", "WS ${1:/path} {\n  on_message {\n    |> ${2:step}\n  }\n}", 36),
		candidate("type", completionKindSnippet, "structured type", "type ${1:Name} {\n  ${2:field} ${3:string} required\n}", 40),
		candidate("alias", completionKindSnippet, "type alias", "alias ${1:Name} = ${2:string}", 41),
		candidate("enum", completionKindSnippet, "named enum", "enum ${1:Name} {\n  ${2:value}\n}", 42),
		keywordCandidate("secret", "secret ${1:NAME} required", "environment secret", 43),
		keywordCandidate("env", "env ${1:NAME} ${2:value}", "environment value", 44),
		keywordCandidate("include", "include \"${1:path.bp}\"", "include another Blueprint file", 45),
		candidate("worker", completionKindSnippet, "background worker", "worker ${1:name} {\n  trigger queue(\"${2:jobs}\")\n  |> ${3:step}\n}", 50),
		candidate("schedule", completionKindSnippet, "scheduled job", "schedule ${1:name} {\n  cron \"${2:0 * * * *}\"\n  |> ${3:step}\n}", 51),
		candidate("external", completionKindSnippet, "external HTTP service", "external \"${1:name}\" {\n  url: \"${2:https://example.com}\"\n}", 52),
		candidate("subscribe", completionKindSnippet, "event subscription", "subscribe \"${1:event}\" {\n  |> ${2:step}\n}", 53),
		candidate("state", completionKindSnippet, "state machine", "state ${1:name} {\n  ${2:pending} -> ${3:done}\n}", 54),
		candidate("analytics", completionKindSnippet, "analytics declaration", "analytics ${1:name} {\n  event ${2:event_name}\n  sink console\n}", 55),
		candidate("save", completionKindSnippet, "versioned save schema", "save ${1:name} {\n  model ${2:model}\n  version_field ${3:version}\n  latest ${4:1}\n}", 56),
		keywordCandidate("locale", "locale ${1:en} default", "supported locale", 57),
		candidate("translation", completionKindSnippet, "translation namespace", "translation ${1:name} {\n  key \"${2:key}\"\n}", 58),
		candidate("fixture", completionKindSnippet, "test fixture", "fixture \"${1:name}\" from \"${2:path}\"", 60),
		candidate("test", completionKindSnippet, "authored contract test", "test ${1:name} {\n  target ${2:GET} ${3:/path}\n  expect {\n    status == ${4:200}\n  }\n}", 61),
		candidate("test_group", completionKindSnippet, "test group", "test_group ${1:name} {\n  tests [${2:test_name}]\n}", 62),
		keywordCandidate("@ intent", "@ \"${1:intent}\"", "attach intent to the next declaration", 5),
	}
	if file != nil && file.Blueprint != nil {
		// A second blueprint declaration is never valid, so remove that snippet.
		items = items[1:]
	}
	return items
}

func blueprintCompletions(file *ast.File) []completionCandidate {
	items := []completionCandidate{
		keywordCandidate("version", "version \"${1:0.1.0}\"", "Blueprint version", 10),
		keywordCandidate("port", "port ${1:3000}", "HTTP port", 11),
		keywordCandidate("runtime", "runtime ${1:node}", "runtime target", 12),
		keywordCandidate("database", "database ${1:postgres}", "database backend", 13),
		keywordCandidate("cache", "cache ${1:redis}", "cache backend", 14),
		keywordCandidate("storage", "storage ${1|s3,local|}", "object storage backend", 15),
		keywordCandidate("use", "use ${1:middleware}", "global middleware", 20),
	}
	items = append(items, middlewareCompletions(file)...)
	return items
}

func blueprintValueCompletions(key string) []completionCandidate {
	values := map[string][]string{
		"runtime":  {"node"},
		"database": {"postgres"},
		"cache":    {"redis"},
		"storage":  {"local", "s3"},
	}[key]
	items := make([]completionCandidate, 0, len(values))
	for _, value := range values {
		items = append(items, candidate(value, completionKindKeyword, key+" value", value, 5))
	}
	return items
}

func typeCompletions(file *ast.File) []completionCandidate {
	names := []string{"string", "int", "float", "bool", "uuid", "timestamp", "json", "file", "money"}
	items := make([]completionCandidate, 0, len(names)+8)
	for _, name := range names {
		items = append(items, candidate(name, completionKindKeyword, "built-in type", name, 10))
	}
	items = append(items,
		candidate("enum", completionKindSnippet, "inline enum", "enum(${1:value}, ${2:value})", 20),
		candidate("json<type>", completionKindSnippet, "typed JSON", "json<${1:string}>", 21),
		candidate("list(type)", completionKindSnippet, "list type", "list(${1:string})", 22),
		candidate("map(key, value)", completionKindSnippet, "map type", "map(${1:string}, ${2:string})", 23),
	)
	if file != nil {
		for _, block := range file.Blocks {
			switch b := block.(type) {
			case *ast.TypeDecl:
				items = append(items, candidate(b.Name, completionKindStruct, "declared type", b.Name, 30))
			case *ast.Alias:
				items = append(items, candidate(b.Name, completionKindClass, "declared alias", b.Name, 30))
			case *ast.Enum:
				items = append(items, candidate(b.Name, completionKindEnum, "declared enum", b.Name, 30))
			case *ast.Model:
				items = append(items, candidate(b.Name, completionKindClass, "model type", b.Name, 31))
			case *ast.Content:
				items = append(items, candidate(b.Name, completionKindClass, "content type", b.Name, 31))
			}
		}
	}
	return items
}

func constraintCompletions() []completionCandidate {
	return []completionCandidate{
		keywordCandidate("required", "required", "required value", 10),
		keywordCandidate("optional", "optional", "optional value", 11),
		keywordCandidate("primary", "primary", "primary key", 12),
		keywordCandidate("unique", "unique", "unique index", 13),
		keywordCandidate("index", "index", "database index", 14),
		keywordCandidate("auto", "auto", "automatically updated", 15),
		candidate("default", completionKindSnippet, "default value", "default(${1:value})", 20),
		candidate("ref", completionKindSnippet, "model reference", "ref(${1:model})", 21),
		candidate("format", completionKindSnippet, "format constraint", "format(${1:email})", 22),
		candidate("min", completionKindSnippet, "minimum constraint", "min(${1:0})", 23),
		candidate("max", completionKindSnippet, "maximum constraint", "max(${1:100})", 24),
	}
}

func modelCompletions(file *ast.File) []completionCandidate {
	var items []completionCandidate
	if file == nil {
		return items
	}
	for _, block := range file.Blocks {
		switch b := block.(type) {
		case *ast.Model:
			items = append(items, candidate(b.Name, completionKindClass, "model", b.Name, 10))
		case *ast.Content:
			items = append(items, candidate(b.Name, completionKindClass, "content model", b.Name, 11))
		}
	}
	return items
}

func relationshipContext(line string) (model string, existing map[string]bool, prefix string, ok bool) {
	opens := withOpen.FindAllStringIndex(line, -1)
	if len(opens) == 0 {
		return "", nil, "", false
	}
	open := opens[len(opens)-1]
	tail := line[open[1]:]
	if strings.Contains(tail, ")") {
		return "", nil, "", false
	}
	queries := queryModel.FindAllStringSubmatch(line[:open[0]], -1)
	if len(queries) == 0 {
		return "", nil, "", false
	}
	model = queries[len(queries)-1][1]
	prefix = wordPrefix(tail)
	completed := tail[:len(tail)-len(prefix)]
	existing = make(map[string]bool)
	for _, name := range identifierPattern.FindAllString(completed, -1) {
		existing[name] = true
	}
	return model, existing, prefix, true
}

func relationshipCompletions(file *ast.File, modelName string, existing map[string]bool) []completionCandidate {
	model, ok := findModel(file, modelName)
	if !ok {
		return nil
	}
	collides := make(map[string]bool, len(model.Fields)+len(model.ComputedFields))
	for _, field := range model.Fields {
		if field != nil {
			collides[field.Name] = true
		}
	}
	for _, field := range model.ComputedFields {
		if field != nil {
			collides[field.Name] = true
		}
	}
	items := make([]completionCandidate, 0, len(model.Fields))
	for _, field := range model.Fields {
		if field == nil || !strings.HasSuffix(field.Name, "_id") {
			continue
		}
		relation := strings.TrimSuffix(field.Name, "_id")
		if relation == "" || relation == "_base" || existing[relation] || collides[relation] {
			continue
		}
		target := ""
		for _, constraint := range field.Constraints {
			if constraint == nil || constraint.Kind != "ref" {
				continue
			}
			if ident, symbolic := constraint.Value.(*ast.Ident); symbolic {
				target = ident.Name
			}
		}
		if target == "" {
			continue
		}
		items = append(items, candidate(relation, completionKindField, "relationship to "+target, relation, 5))
	}
	return items
}

func modelForSourceFrame(idx *docIndex, frame sourceFrame) *ast.Model {
	if idx == nil || idx.file == nil {
		return nil
	}
	for _, block := range idx.file.Blocks {
		if model, ok := block.(*ast.Model); ok && model.Loc.Line-1 == frame.startLine {
			return model
		}
	}
	lines := strings.Split(idx.text, "\n")
	if frame.startLine < 0 || frame.startLine >= len(lines) {
		return nil
	}
	header := strings.Fields(strings.TrimSpace(lines[frame.startLine]))
	if len(header) >= 2 && header[0] == "model" {
		model, _ := findModel(idx.file, header[1])
		return model
	}
	return nil
}

func computedExpressionCompletions(idx *docIndex, frame sourceFrame, line int) []completionCandidate {
	model := modelForSourceFrame(idx, frame)
	if model == nil {
		return nil
	}
	items := make([]completionCandidate, 0, len(model.Fields)+len(model.ComputedFields))
	for _, field := range model.Fields {
		if field != nil {
			items = append(items, candidate(field.Name, completionKindField, model.Name+" field: "+typeString(field.Type), field.Name, 5))
		}
	}
	for _, field := range model.ComputedFields {
		if field != nil && field.Loc.Line-1 < line {
			items = append(items, candidate(field.Name, completionKindField, model.Name+" computed field: "+typeString(field.Type), field.Name, 6))
		}
	}
	return items
}

func refModelCompletions(file *ast.File) []completionCandidate {
	items := modelCompletions(file)
	for i := range items {
		name := items[i].Label
		items[i].Label = "ref(" + name + ")"
		items[i].InsertText = "ref(" + name + ")"
		items[i].Detail = "reference " + name
		items[i].priority = 30
	}
	return items
}

func middlewareCompletions(file *ast.File) []completionCandidate {
	var items []completionCandidate
	if file == nil {
		return items
	}
	for _, block := range file.Blocks {
		if m, ok := block.(*ast.Middleware); ok {
			items = append(items, candidate(m.Name, completionKindFunction, "middleware", m.Name, 5))
		}
	}
	return items
}

func pipeCompletions(file *ast.File, includeCall bool) []completionCandidate {
	var items []completionCandidate
	if file == nil {
		return items
	}
	for _, block := range file.Blocks {
		if p, ok := block.(*ast.Pipe); ok {
			insert := p.Name
			if includeCall {
				insert = p.Name + "(${1:value})"
			}
			items = append(items, candidate(p.Name, completionKindFunction, "pipe", insert, 15))
		}
	}
	return items
}

func externalCompletions(file *ast.File) []completionCandidate {
	var items []completionCandidate
	if file == nil {
		return items
	}
	for _, block := range file.Blocks {
		if ext, ok := block.(*ast.External); ok {
			name := strings.NewReplacer("-", "_", ".", "_").Replace(ext.Name)
			detail := "external service"
			if name != ext.Name {
				detail = "external service \"" + ext.Name + "\""
			}
			items = append(items, candidate(name, completionKindClass, detail, name, 10))
		}
	}
	return items
}

func expressionCompletions(idx *docIndex, frame sourceFrame, line int) []completionCandidate {
	items := scopeCompletions(idx, frame, line)
	if idx == nil || idx.file == nil {
		return items
	}
	for _, block := range idx.file.Blocks {
		switch b := block.(type) {
		case *ast.Fn:
			args := make([]string, 0, len(b.Inputs))
			for i, input := range b.Inputs {
				args = append(args, fmt.Sprintf("${%d:%s}", i+1, input.Name))
			}
			items = append(items, candidate(b.Name, completionKindFunction, "function", b.Name+"("+strings.Join(args, ", ")+")", 20))
		case *ast.Pipe:
			items = append(items, candidate(b.Name, completionKindFunction, "pipe", "pipe "+b.Name+"(${1:value})", 21))
		}
	}
	for _, name := range []string{"true", "false", "null", "now"} {
		items = append(items, candidate(name, completionKindKeyword, "literal", name, 30))
	}
	return items
}

type scopedValue struct {
	name  string
	model string
}

func scopeValues(idx *docIndex, frame sourceFrame, line int) []scopedValue {
	if idx == nil {
		return nil
	}
	lines := strings.Split(idx.text, "\n")
	start := frame.startLine + 1
	if start < 0 {
		start = 0
	}
	if line > len(lines) {
		line = len(lines)
	}
	values := make([]scopedValue, 0, 8)
	byName := make(map[string]int)
	add := func(value scopedValue) {
		if at, exists := byName[value.name]; exists {
			if value.model != "" {
				values[at].model = value.model
			}
			return
		}
		byName[value.name] = len(values)
		values = append(values, value)
	}
	for i := start; i < line && i < len(lines); i++ {
		if match := inputLine.FindStringSubmatch(lines[i]); match != nil {
			model := ""
			if _, ok := findModel(idx.file, match[2]); ok {
				model = match[2]
			}
			add(scopedValue{name: match[1], model: model})
			continue
		}
		match := bindingLine.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		value := scopedValue{name: match[1]}
		expr := strings.TrimSpace(match[2])
		if row := rowBinding.FindStringSubmatch(expr); row != nil {
			if _, ok := findModel(idx.file, row[1]); ok {
				value.model = row[1]
			}
		} else if row := firstQueryBinding.FindStringSubmatch(expr); row != nil {
			if _, ok := findModel(idx.file, row[1]); ok {
				value.model = row[1]
			}
		} else if prior, exists := byName[expr]; exists {
			value.model = values[prior].model
		}
		add(value)
	}
	return values
}

func scopeCompletions(idx *docIndex, frame sourceFrame, line int) []completionCandidate {
	values := scopeValues(idx, frame, line)
	items := make([]completionCandidate, 0, len(values))
	for _, value := range values {
		detail := "local value"
		if value.model != "" {
			detail = value.model + " value"
		}
		items = append(items, candidate(value.name, completionKindVariable, detail, value.name, 5))
	}
	return items
}

func fieldCompletions(idx *docIndex, frame sourceFrame, line int, base string) []completionCandidate {
	if idx == nil || idx.file == nil {
		return nil
	}
	if base == "env" || base == "secret" {
		var items []completionCandidate
		for _, block := range idx.file.Blocks {
			switch b := block.(type) {
			case *ast.Env:
				if base == "env" {
					items = append(items, candidate(b.Name, completionKindVariable, "declared environment value", b.Name, 5))
				}
			case *ast.Secret:
				items = append(items, candidate(b.Name, completionKindVariable, "declared secret", b.Name, 6))
			}
		}
		return items
	}
	modelName := ""
	if _, ok := findModel(idx.file, base); ok {
		modelName = base
	} else {
		for _, value := range scopeValues(idx, frame, line) {
			if value.name == base {
				modelName = value.model
				break
			}
		}
	}
	model, ok := findModel(idx.file, modelName)
	if !ok {
		return nil
	}
	items := make([]completionCandidate, 0, len(model.Fields))
	for _, field := range model.Fields {
		if field == nil {
			continue
		}
		items = append(items, candidate(field.Name, completionKindField, modelName+" field: "+typeString(field.Type), field.Name, 5))
	}
	for _, field := range model.ComputedFields {
		if field == nil {
			continue
		}
		items = append(items, candidate(field.Name, completionKindField, modelName+" computed field: "+typeString(field.Type), field.Name, 6))
	}
	return items
}

func stepCompletions() []completionCandidate {
	return []completionCandidate{
		candidate("query", completionKindFunction, "query model rows", "query ${1:model} where(${2:condition})", 10),
		candidate("fetch", completionKindFunction, "fetch one model row", "fetch ${1:model}(${2:id})", 11),
		candidate("save", completionKindFunction, "persist a model row", "save ${1:model} { ${2:field}: ${3:value} }", 12),
		candidate("update", completionKindFunction, "update model rows", "update ${1:model} { ${2:field}: ${3:value} }", 13),
		candidate("delete", completionKindFunction, "delete model rows", "delete ${1:model} where(${2:condition})", 14),
		candidate("guard", completionKindSnippet, "early return unless condition holds", "guard ${1:condition} -> ${2:400} \"${3:message}\"", 15),
		candidate("when", completionKindSnippet, "conditional step", "when ${1:condition}: ${2:expression}", 16),
		candidate("try", completionKindSnippet, "recoverable step block", "try {\n  |> ${1:step}\n} recover {\n  |> ${2:recovery}\n}", 17),
		candidate("log", completionKindFunction, "structured log", "log \"${1:message}\"", 20),
		candidate("emit", completionKindFunction, "emit in-process event", "emit ${1:event} { ${2:data}: ${3:value} }", 21),
		candidate("enqueue", completionKindFunction, "enqueue worker job", "enqueue \"${1:queue}\" { ${2:data}: ${3:value} }", 22),
		candidate("call", completionKindFunction, "call external service", "call ${1:service} ${2:GET} ${3:/path}", 23),
		candidate("map", completionKindFunction, "map a collection", "map ${1:items}: ${2:expression}", 24),
		candidate("upload", completionKindFunction, "upload a file", "upload(${1:file}, ${2:bucket})", 25),
		candidate("download", completionKindFunction, "download a file", "download(${1:url})", 26),
		candidate("inject", completionKindFunction, "inject middleware context", "inject ${1:value} as ${2:name}", 27),
		candidate("sleep", completionKindFunction, "pause execution", "sleep ${1:1s}", 28),
	}
}

func arrowCompletions() []completionCandidate {
	return []completionCandidate{
		candidate("<- input", completionKindSnippet, "declare an input", "<- ${1:name} ${2:string} ${3:required}", 10),
		candidate("|> step", completionKindSnippet, "execute a step", "|> ${1:result = }${2:expression}", 11),
		candidate("-> output", completionKindSnippet, "return output", "-> ${1:200} ${2:value}", 12),
		candidate("@> generate", completionKindSnippet, "LLM generation slot", "@> \"${1:prompt}\"", 13),
	}
}

func fnBodyCompletions() []completionCandidate {
	items := arrowCompletions()
	items = append(items,
		candidate("impl node", completionKindSnippet, "native Node implementation", "impl node {\n  module: \"${1:./internal/function}\"\n  func: \"${2:default}\"\n}", 20),
		candidate("impl exec", completionKindSnippet, "executable implementation", "impl exec {\n  command: \"${1:command}\"\n}", 21),
		candidate("logic", completionKindSnippet, "inline Blueprint function", "logic {\n  |> ${1:step}\n  |> -> ${2:value}\n}", 22),
	)
	return items
}

func endpointMetaCompletions() []completionCandidate {
	return []completionCandidate{
		keywordCandidate("use", "use ${1:middleware}", "endpoint middleware", 5),
		keywordCandidate("auth", "auth ${1:scheme}", "endpoint authentication metadata", 6),
		keywordCandidate("limit", "limit ${1:60/min}", "rate limit", 7),
		keywordCandidate("cache", "cache ${1:5s}", "cache metadata", 8),
		keywordCandidate("tags", "tags [\"${1:tag}\"]", "OpenAPI tags", 9),
		keywordCandidate("timeout", "timeout ${1:30s}", "request timeout", 10),
	}
}

func workerMetaCompletions() []completionCandidate {
	return []completionCandidate{
		keywordCandidate("trigger", "trigger ${1:queue}", "worker trigger", 5),
		keywordCandidate("retry", "retry ${1:3}", "retry count", 6),
		keywordCandidate("timeout", "timeout ${1:30s}", "job timeout", 7),
		candidate("on_fail", completionKindSnippet, "failure handler", "on_fail {\n  |> ${1:step}\n}", 8),
	}
}

func externalEntryCompletions() []completionCandidate {
	return []completionCandidate{
		keywordCandidate("url", "url: \"${1:https://example.com}\"", "service base URL", 5),
		keywordCandidate("auth", "auth: ${1:bearer}(secret.${2:TOKEN})", "outbound authentication", 6),
		keywordCandidate("timeout", "timeout: ${1:30s}", "request timeout", 7),
		keywordCandidate("retry", "retry: ${1:3}", "additional retry attempts", 8),
	}
}
