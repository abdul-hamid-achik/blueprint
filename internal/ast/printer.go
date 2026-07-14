package ast

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// blueprintQuote wraps s in double quotes, escaping only backslashes and
// double quotes. Newlines are kept as literal newlines (Blueprint allows
// multi-line strings).
func blueprintQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		default:
			sb.WriteRune(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// Print formats an AST back to canonical .bp source code.
func Print(f *File) string {
	var p printer
	formatted := p.printFile(f)
	if len(f.Comments) == 0 || len(f.SourceTokens) == 0 {
		return formatted
	}
	return restoreComments(formatted, f.SourceTokens, f.Comments)
}

type tokenIdentity struct {
	kind  lexer.TokenKind
	value string
}

type placedComment struct {
	comment lexer.Comment
	indent  int
}

// restoreComments reattaches lexer trivia to canonical output. The parser does
// not need comment productions: each comment is anchored to a neighboring
// source token, and that token is matched to its canonical counterpart by
// identity and occurrence. This keeps comments out of string contents and
// makes a second formatting pass stable.
func restoreComments(formatted string, sourceTokens []lexer.Token, comments []lexer.Comment) string {
	formattedTokens, lexErrors := lexer.Tokenize("<formatted>", []byte(formatted))
	if len(lexErrors) > 0 {
		// The ordinary printer is expected to produce valid source. If it does
		// not, returning its output unchanged is safer than guessing where
		// trivia belongs.
		return formatted
	}

	outputByIdentity := make(map[tokenIdentity][]int)
	for i, token := range formattedTokens {
		key := tokenIdentity{kind: token.Kind, value: token.Value}
		outputByIdentity[key] = append(outputByIdentity[key], i)
	}

	sourceOccurrences := make(map[tokenIdentity]int)
	tokenMap := make([]int, len(sourceTokens))
	for i := range tokenMap {
		tokenMap[i] = -1
	}
	for i, token := range sourceTokens {
		key := tokenIdentity{kind: token.Kind, value: token.Value}
		occurrence := sourceOccurrences[key]
		sourceOccurrences[key] = occurrence + 1
		if candidates := outputByIdentity[key]; occurrence < len(candidates) {
			tokenMap[i] = candidates[occurrence]
		}
	}

	leading := make(map[int][]placedComment)
	inline := make(map[int][]placedComment)
	for _, comment := range comments {
		sourceAnchor, outputAnchor, ok := mappedCommentAnchor(comment, sourceTokens, tokenMap, formattedTokens)
		if !ok {
			continue
		}

		if comment.Inline {
			line := lineAtOffset(formatted, outputAnchor.Loc.Offset+outputAnchor.Loc.Len)
			inline[line] = append(inline[line], placedComment{comment: comment})
			continue
		}

		line := outputAnchor.Loc.Line
		indent := outputAnchor.Loc.Col - 1
		// A comment immediately before a closing delimiter is indented inside
		// that delimiter in the source. Preserve that relative indentation
		// while still following any indentation changes made by the formatter.
		if delta := comment.Loc.Col - sourceAnchor.Loc.Col; delta > 0 {
			indent += delta
		}
		leading[line] = append(leading[line], placedComment{comment: comment, indent: indent})
	}

	// Canonicalization can collapse a multi-line expression onto one line. If
	// that puts multiple former inline comments on the same line, only the last
	// can remain inline; move the others above the line so no comment is hidden
	// behind an earlier #.
	for line, lineComments := range inline {
		if len(lineComments) <= 1 {
			continue
		}
		for _, item := range lineComments[:len(lineComments)-1] {
			leading[line] = append(leading[line], item)
		}
		inline[line] = lineComments[len(lineComments)-1:]
	}

	for _, items := range []map[int][]placedComment{leading, inline} {
		for line := range items {
			sort.SliceStable(items[line], func(i, j int) bool {
				return items[line][i].comment.Loc.Offset < items[line][j].comment.Loc.Offset
			})
		}
	}

	body := strings.TrimSuffix(formatted, "\n")
	var lines []string
	if body != "" {
		lines = strings.Split(body, "\n")
	}

	var sb strings.Builder
	writeLeading := func(line int) {
		for _, item := range leading[line] {
			if item.indent > 0 {
				sb.WriteString(strings.Repeat(" ", item.indent))
			}
			sb.WriteString(item.comment.Text)
			sb.WriteByte('\n')
		}
	}
	for i, lineText := range lines {
		line := i + 1
		writeLeading(line)
		sb.WriteString(lineText)
		for _, item := range inline[line] {
			sb.WriteString("  ")
			sb.WriteString(item.comment.Text)
		}
		sb.WriteByte('\n')
	}

	// Comments anchored to EOF live after the printer's final newline. Compute
	// the actual final trivia line so even a comment whose closest surviving
	// anchor is EOF cannot be dropped.
	lastTriviaLine := len(lines)
	for line := range leading {
		if line > lastTriviaLine {
			lastTriviaLine = line
		}
	}
	for line := range inline {
		if line > lastTriviaLine {
			lastTriviaLine = line
		}
	}
	for line := len(lines) + 1; line <= lastTriviaLine; line++ {
		writeLeading(line)
		for _, item := range inline[line] {
			sb.WriteString(item.comment.Text)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func mappedCommentAnchor(comment lexer.Comment, sourceTokens []lexer.Token, tokenMap []int, formattedTokens []lexer.Token) (lexer.Token, lexer.Token, bool) {
	anchor := comment.AnchorToken
	if anchor < 0 || anchor >= len(sourceTokens) {
		anchor = len(sourceTokens) - 1
	}
	if anchor < 0 {
		return lexer.Token{}, lexer.Token{}, false
	}

	if mapped := tokenMap[anchor]; mapped >= 0 && mapped < len(formattedTokens) {
		return sourceTokens[anchor], formattedTokens[mapped], true
	}

	// A canonical spelling may omit punctuation accepted by the parser. Fall
	// back to the closest mapped token in the direction the trivia belongs.
	if comment.Inline {
		for i := anchor - 1; i >= 0; i-- {
			if mapped := tokenMap[i]; mapped >= 0 && mapped < len(formattedTokens) {
				return sourceTokens[i], formattedTokens[mapped], true
			}
		}
		for i := anchor + 1; i < len(sourceTokens); i++ {
			if mapped := tokenMap[i]; mapped >= 0 && mapped < len(formattedTokens) {
				return sourceTokens[i], formattedTokens[mapped], true
			}
		}
	} else {
		for i := anchor + 1; i < len(sourceTokens); i++ {
			if mapped := tokenMap[i]; mapped >= 0 && mapped < len(formattedTokens) {
				return sourceTokens[i], formattedTokens[mapped], true
			}
		}
		for i := anchor - 1; i >= 0; i-- {
			if mapped := tokenMap[i]; mapped >= 0 && mapped < len(formattedTokens) {
				return sourceTokens[i], formattedTokens[mapped], true
			}
		}
	}
	return lexer.Token{}, lexer.Token{}, false
}

func lineAtOffset(src string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}
	return strings.Count(src[:offset], "\n") + 1
}

type printer struct {
	// fieldAlign holds column widths for the current group of model fields
	// or endpoint inputs. Set before printing a group, cleared after.
	nameWidth int
	typeWidth int
}

func (p *printer) printFile(f *File) string {
	var sb strings.Builder
	first := true

	writeBlock := func(s string) {
		if !first {
			sb.WriteString("\n")
		}
		sb.WriteString(s)
		first = false
	}

	if f.Blueprint != nil {
		writeBlock(p.printBlueprint(f.Blueprint))
	}

	for _, block := range f.Blocks {
		writeBlock(p.printTopLevel(block))
	}

	result := sb.String()
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}

func (p *printer) printTopLevel(node TopLevel) string {
	switch n := node.(type) {
	case *Blueprint:
		return p.printBlueprint(n)
	case *Secret:
		return p.printSecret(n)
	case *Env:
		return p.printEnv(n)
	case *Locale:
		return p.printLocale(n)
	case *Translation:
		return p.printTranslation(n)
	case *StateMachine:
		return p.printStateMachine(n)
	case *Analytics:
		return p.printAnalytics(n)
	case *SaveSchema:
		return p.printSaveSchema(n)
	case *Include:
		return p.printInclude(n)
	case *TypeDecl:
		return p.printTypeDecl(n)
	case *Alias:
		return p.printAlias(n)
	case *Enum:
		return p.printEnum(n)
	case *Model:
		return p.printModel(n)
	case *Content:
		return p.printContent(n)
	case *Fn:
		return p.printFn(n)
	case *Pipe:
		return p.printPipe(n)
	case *Middleware:
		return p.printMiddleware(n)
	case *Endpoint:
		return p.printEndpoint(n)
	case *StreamEndpoint:
		return p.printStreamEndpoint(n)
	case *WsEndpoint:
		return p.printWsEndpoint(n)
	case *Worker:
		return p.printWorker(n)
	case *Schedule:
		return p.printSchedule(n)
	case *External:
		return p.printExternal(n)
	case *Subscribe:
		return p.printSubscribe(n)
	case *Fixture:
		return p.printFixture(n)
	case *Test:
		return p.printTest(n)
	case *TestGroup:
		return p.printTestGroup(n)
	default:
		return fmt.Sprintf("/* unknown block: %T */\n", node)
	}
}

// --- Intent ---

func (p *printer) printIntent(intent *Intent) string {
	if intent == nil {
		return ""
	}
	return "@ " + blueprintQuote(intent.Text) + "\n"
}

// --- Blueprint ---

func (p *printer) printBlueprint(n *Blueprint) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	sb.WriteString("blueprint " + blueprintQuote(n.Name) + " {\n")
	// Align key columns (version, port, runtime, etc.)
	maxKey := 0
	for _, kv := range n.Entries {
		if len(kv.Key) > maxKey {
			maxKey = len(kv.Key)
		}
	}
	for _, kv := range n.Entries {
		fmt.Fprintf(&sb, "  %-*s %s\n", maxKey, kv.Key, p.printExpr(kv.Value))
	}
	for _, use := range n.Uses {
		sb.WriteString("  ")
		sb.WriteString(p.printUseStmt(use))
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// --- Secret ---

func (p *printer) printSecret(n *Secret) string {
	if n.Required {
		return fmt.Sprintf("secret %s required\n", n.Name)
	}
	if n.Default != nil {
		return fmt.Sprintf("secret %s optional default(%s)\n", n.Name, p.printExpr(n.Default))
	}
	return fmt.Sprintf("secret %s optional\n", n.Name)
}

// --- Env ---

func (p *printer) printEnv(n *Env) string {
	return fmt.Sprintf("env %s %s\n", n.Name, p.printExpr(n.Value))
}

func (p *printer) printLocale(n *Locale) string {
	var parts []string
	parts = append(parts, "locale", p.printLocaleCode(n.Code))
	if n.Default {
		parts = append(parts, "default")
	}
	if n.Fallback != "" {
		parts = append(parts, fmt.Sprintf("fallback(%s)", p.printLocaleCode(n.Fallback)))
	}
	return strings.Join(parts, " ") + "\n"
}

func (p *printer) printTranslation(n *Translation) string {
	var sb strings.Builder
	sb.WriteString("translation " + n.Name + " {\n")
	for _, key := range n.Keys {
		sb.WriteString("  key " + blueprintQuote(key) + "\n")
	}
	for _, bundle := range n.Bundles {
		sb.WriteString("  locale " + p.printLocaleCode(bundle.Locale) + " {\n")
		for _, kv := range bundle.Values {
			sb.WriteString("    " + blueprintQuote(kv.Key) + ": " + p.printExpr(kv.Value) + "\n")
		}
		sb.WriteString("  }\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printStateMachine(n *StateMachine) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	sb.WriteString("state " + n.Name + " {\n")
	for _, tr := range n.Transitions {
		sb.WriteString("  " + tr.From + " -> " + tr.To + "\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printAnalytics(n *Analytics) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	sb.WriteString("analytics " + n.Name + " {\n")
	for _, event := range n.Events {
		sb.WriteString("  event " + event + "\n")
	}
	for _, sink := range n.Sinks {
		sb.WriteString("  sink " + sink.Kind)
		if sink.Target != nil {
			sb.WriteString("(" + p.printExpr(sink.Target) + ")")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printSaveSchema(n *SaveSchema) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	sb.WriteString("save " + n.Name + " {\n")
	if n.Model != "" {
		sb.WriteString("  model " + n.Model + "\n")
	}
	if n.VersionField != "" {
		sb.WriteString("  version_field " + n.VersionField + "\n")
	}
	if n.Latest > 0 {
		fmt.Fprintf(&sb, "  latest %d\n", n.Latest)
	}
	for _, mig := range n.Migrations {
		fmt.Fprintf(&sb, "  migrate %d -> %d", mig.From, mig.To)
		if mig.Module != "" {
			sb.WriteString(" using " + blueprintQuote(mig.Module))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printLocaleCode(code string) string {
	if strings.Contains(code, "-") {
		return blueprintQuote(code)
	}
	return code
}

// --- Include ---

func (p *printer) printInclude(n *Include) string {
	return fmt.Sprintf("include %q\n", n.Path)
}

// --- TypeDecl ---

func (p *printer) printTypeDecl(n *TypeDecl) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "type %s {\n", n.Name)
	for _, f := range n.Fields {
		sb.WriteString("  ")
		sb.WriteString(p.printField(f))
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// --- Alias ---

func (p *printer) printAlias(n *Alias) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "alias %s = %s", n.Name, p.printType(n.Type))
	for _, c := range n.Constraints {
		sb.WriteString(" ")
		sb.WriteString(p.printConstraint(c))
	}
	sb.WriteString("\n")
	return sb.String()
}

// --- Enum ---

func (p *printer) printEnum(n *Enum) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "enum %s {\n", n.Name)
	for _, v := range n.Variants {
		fmt.Fprintf(&sb, "  %s", v.Name)
		if v.Body != nil && len(v.Body.Entries) > 0 {
			sb.WriteString(" { ")
			for i, kv := range v.Body.Entries {
				if i > 0 {
					sb.WriteString(",  ")
				}
				fmt.Fprintf(&sb, "%s: %s", kv.Key, p.printExpr(kv.Value))
			}
			sb.WriteString(" }")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}
func (p *printer) printModel(n *Model) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "model %s {\n", n.Name)
	// Compute column widths for aligned field output.
	p.nameWidth, p.typeWidth = 0, 0
	for _, f := range n.Fields {
		if w := len(f.Name); w > p.nameWidth {
			p.nameWidth = w
		}
		tw := len(p.printType(f.Type))
		if tw > p.typeWidth {
			p.typeWidth = tw
		}
	}
	for _, f := range n.Fields {
		sb.WriteString("  ")
		sb.WriteString(p.printField(f))
		sb.WriteString("\n")
	}
	for _, f := range n.ComputedFields {
		fmt.Fprintf(&sb, "  computed %s %s = %s\n", f.Name, p.printType(f.Type), p.printExpr(f.Expr))
	}
	p.nameWidth, p.typeWidth = 0, 0
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printContent(n *Content) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	sb.WriteString("content " + n.Name + " {\n")
	p.nameWidth, p.typeWidth = 0, 0
	for _, f := range n.Fields {
		if w := len(f.Name); w > p.nameWidth {
			p.nameWidth = w
		}
		tw := len(p.printType(f.Type))
		if tw > p.typeWidth {
			p.typeWidth = tw
		}
	}
	for _, f := range n.Fields {
		sb.WriteString("  " + p.printField(f) + "\n")
	}
	p.nameWidth, p.typeWidth = 0, 0
	sb.WriteString("}\n")
	return sb.String()
}

// --- Field ---

func (p *printer) printField(f *Field) string {
	var sb strings.Builder
	// When alignment widths are set (inside printModel/printContent),
	// pad the name and type columns so fields line up.
	nameFmt := f.Name
	typeStr := p.printType(f.Type)
	if p.nameWidth > 0 {
		nameFmt = fmt.Sprintf("%-*s", p.nameWidth, f.Name)
	}
	if p.typeWidth > 0 {
		typeStr = fmt.Sprintf("%-*s", p.typeWidth, typeStr)
	}
	fmt.Fprintf(&sb, "%s %s", nameFmt, typeStr)
	for _, c := range f.Constraints {
		sb.WriteString(" ")
		sb.WriteString(p.printConstraint(c))
	}
	return sb.String()
}

// --- Fn ---

func (p *printer) printFn(n *Fn) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "fn %s {\n", n.Name)
	for _, inp := range n.Inputs {
		sb.WriteString("  ")
		sb.WriteString(p.printArrowStmt(inp, "  "))
		sb.WriteString("\n")
	}
	if len(n.Inputs) > 0 {
		sb.WriteString("\n")
	}
	for _, out := range n.Outputs {
		sb.WriteString("  ")
		sb.WriteString(p.printArrowStmt(out, "  "))
		sb.WriteString("\n")
	}
	if n.Impl != nil {
		if len(n.Outputs) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(p.printImplBlock(n.Impl, "  "))
	}
	if n.Logic != nil {
		if len(n.Outputs) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(p.printLogicBlock(n.Logic, "  "))
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printImplBlock(impl *ImplBlock, indent string) string {
	// Small impl blocks (≤2 entries, none of which is itself a multi-line
	// block expression) reflow onto a single line — that's how they appear
	// in hand-written sources like examples/auth-service.bp and otherwise
	// bp fmt would inflate them unnecessarily on every save.
	if len(impl.Entries) > 0 && len(impl.Entries) <= 2 && implEntriesInlineable(impl.Entries) {
		var sb strings.Builder
		fmt.Fprintf(&sb, "%simpl %s { ", indent, impl.Strategy)
		for i, kv := range impl.Entries {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%s: %s", kv.Key, p.printExprAt(kv.Value, indent))
		}
		sb.WriteString(" }\n")
		return sb.String()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%simpl %s {\n", indent, impl.Strategy)
	for _, kv := range impl.Entries {
		fmt.Fprintf(&sb, "%s  %s: %s\n", indent, kv.Key, p.printExprAt(kv.Value, indent+"  "))
	}
	fmt.Fprintf(&sb, "%s}\n", indent)
	return sb.String()
}

// implEntriesInlineable returns true when none of the entries would force
// the line to wrap (e.g. a BlockExpr with > 3 entries, or a string with a
// newline character in it). The threshold mirrors printBlockExpr's own
// inline shortcut.
func implEntriesInlineable(entries []KVPair) bool {
	for _, kv := range entries {
		switch v := kv.Value.(type) {
		case *BlockExpr:
			if len(v.Entries) > 3 {
				return false
			}
		case *StringLit:
			if strings.ContainsRune(v.Value, '\n') {
				return false
			}
		}
	}
	return true
}

func (p *printer) printLogicBlock(logic *LogicBlock, indent string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%slogic {\n", indent)
	for _, stmt := range logic.Stmts {
		sb.WriteString(indent + "  ")
		sb.WriteString(p.printArrowStmt(stmt, indent+"  "))
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "%s}\n", indent)
	return sb.String()
}

// --- Pipe ---

func (p *printer) printPipe(n *Pipe) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "pipe %s {\n", n.Name)
	sb.WriteString(p.printArrowStmtsBlock(n.Stmts, "  "))
	sb.WriteString("}\n")
	return sb.String()
}

// --- Middleware ---

func (p *printer) printMiddleware(n *Middleware) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "middleware %s {\n", n.Name)
	for _, kv := range n.Entries {
		fmt.Fprintf(&sb, "  %s  %s\n", kv.Key, p.printExpr(kv.Value))
	}
	if len(n.Before) > 0 {
		sb.WriteString("  before {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.Before, "    "))
		sb.WriteString("  }\n")
	}
	if len(n.After) > 0 {
		sb.WriteString("  after {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.After, "    "))
		sb.WriteString("  }\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// --- Endpoint ---

func (p *printer) printEndpoint(n *Endpoint) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "%s %s {\n", n.Method, n.Path)
	for _, meta := range n.Meta {
		sb.WriteString("  ")
		sb.WriteString(p.printEndpointMeta(meta))
		sb.WriteString("\n")
	}
	if len(n.Meta) > 0 {
		sb.WriteString("\n")
	}
	// Compute column widths for aligned input statements.
	p.nameWidth, p.typeWidth = 0, 0
	for _, s := range n.Stmts {
		if inp, ok := s.(*InputStmt); ok {
			if w := len(inp.Name); w > p.nameWidth {
				p.nameWidth = w
			}
			tw := len(p.printType(inp.Type))
			if tw > p.typeWidth {
				p.typeWidth = tw
			}
		}
	}
	sb.WriteString(p.printArrowStmtsBlock(n.Stmts, "  "))
	p.nameWidth, p.typeWidth = 0, 0
	if n.OnError != nil {
		fmt.Fprintf(&sb, "  on_error -> %s %q\n", n.OnError.Status, n.OnError.Message)
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printEndpointMeta(m *EndpointMeta) string {
	switch m.Kind {
	case "use":
		if m.Use != nil {
			return p.printUseStmt(m.Use)
		}
		return fmt.Sprintf("use %s", p.printExpr(m.Value))
	case "auth":
		return fmt.Sprintf("auth %s", p.printExpr(m.Value))
	case "limit":
		return fmt.Sprintf("limit %s", p.printExpr(m.Value))
	case "cache":
		return fmt.Sprintf("cache %s", p.printExpr(m.Value))
	case "tags":
		return fmt.Sprintf("tags  %s", p.printExpr(m.Value))
	case "timeout":
		return fmt.Sprintf("timeout %s", p.printExpr(m.Value))
	default:
		if m.Value != nil {
			return fmt.Sprintf("%s %s", m.Kind, p.printExpr(m.Value))
		}
		return m.Kind
	}
}

func (p *printer) printUseStmt(u *UseStmt) string {
	var sb strings.Builder
	sb.WriteString("use ")
	sb.WriteString(u.Name)
	if len(u.Args) > 0 {
		args := make([]string, len(u.Args))
		for i, a := range u.Args {
			args[i] = p.printExpr(a)
		}
		fmt.Fprintf(&sb, "(%s)", strings.Join(args, ", "))
	}
	if u.Body != nil && len(u.Body.Entries) > 0 {
		sb.WriteString(" { ")
		for i, kv := range u.Body.Entries {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%s: %s", kv.Key, p.printExpr(kv.Value))
		}
		sb.WriteString(" }")
	}
	return sb.String()
}

// --- StreamEndpoint ---

func (p *printer) printStreamEndpoint(n *StreamEndpoint) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "STREAM %s {\n", n.Path)
	for _, meta := range n.Meta {
		sb.WriteString("  ")
		sb.WriteString(p.printEndpointMeta(meta))
		sb.WriteString("\n")
	}
	if len(n.Meta) > 0 && (len(n.Stmts) > 0 || len(n.Handlers) > 0) {
		sb.WriteString("\n")
	}
	sb.WriteString(p.printArrowStmtsBlock(n.Stmts, "  "))
	// Stream handlers must be wrapped in a `stream { ... }` block — the
	// parser only recognises `|> on event(NAME) where(...) { body }` inside
	// that wrapper. Emitting them bare round-trips into a parse error.
	if len(n.Handlers) > 0 {
		if len(n.Stmts) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("  stream {\n")
		for _, h := range n.Handlers {
			sb.WriteString(p.printStreamHandler(h, "    "))
		}
		sb.WriteString("  }\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printStreamHandler(h *StreamHandler, indent string) string {
	var sb strings.Builder
	if h.Timeout != "" {
		fmt.Fprintf(&sb, "%s|> on timeout(%s)", indent, h.Timeout)
	} else {
		fmt.Fprintf(&sb, "%s|> on event(%s)", indent, h.EventName)
		if h.Condition != nil {
			fmt.Fprintf(&sb, " where(%s)", p.printExprAt(h.Condition, indent))
		}
	}
	sb.WriteString(" {\n")
	sb.WriteString(p.printArrowStmtsBlock(h.Body, indent+"  "))
	fmt.Fprintf(&sb, "%s}\n", indent)
	return sb.String()
}

// --- WsEndpoint ---

func (p *printer) printWsEndpoint(n *WsEndpoint) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "WS %s {\n", n.Path)
	for _, meta := range n.Meta {
		sb.WriteString("  ")
		sb.WriteString(p.printEndpointMeta(meta))
		sb.WriteString("\n")
	}
	if len(n.OnConnect) > 0 {
		sb.WriteString("  on_connect {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.OnConnect, "    "))
		sb.WriteString("  }\n")
	}
	if len(n.OnMessage) > 0 {
		sb.WriteString("  on_message {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.OnMessage, "    "))
		sb.WriteString("  }\n")
	}
	if len(n.OnDisconnect) > 0 {
		sb.WriteString("  on_disconnect {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.OnDisconnect, "    "))
		sb.WriteString("  }\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// --- Worker ---

func (p *printer) printWorker(n *Worker) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "worker %s {\n", n.Name)
	for _, meta := range n.Meta {
		sb.WriteString("  ")
		sb.WriteString(p.printWorkerMeta(meta))
		sb.WriteString("\n")
	}
	if len(n.Meta) > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString(p.printArrowStmtsBlock(n.Stmts, "  "))
	if len(n.OnFail) > 0 {
		sb.WriteString("  on_fail {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.OnFail, "    "))
		sb.WriteString("  }\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printWorkerMeta(m *WorkerMeta) string {
	var sb strings.Builder
	sb.WriteString(m.Kind)
	if m.Value != nil {
		fmt.Fprintf(&sb, "  %s", p.printExpr(m.Value))
	}
	if len(m.Extra) > 0 {
		sb.WriteString(" {")
		for i, kv := range m.Extra {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, " %s: %s", kv.Key, p.printExpr(kv.Value))
		}
		sb.WriteString(" }")
	}
	return sb.String()
}

// --- Schedule ---

func (p *printer) printSchedule(n *Schedule) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "schedule %s {\n", n.Name)
	fmt.Fprintf(&sb, "  cron %q\n", n.Cron)
	sb.WriteString("\n")
	sb.WriteString(p.printArrowStmtsBlock(n.Stmts, "  "))
	sb.WriteString("}\n")
	return sb.String()
}

// --- External ---

func (p *printer) printExternal(n *External) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "external %q {\n", n.Name)
	for _, kv := range n.Entries {
		fmt.Fprintf(&sb, "  %s: %s\n", kv.Key, p.printExpr(kv.Value))
	}
	sb.WriteString("}\n")
	return sb.String()
}

// --- Subscribe ---

func (p *printer) printSubscribe(n *Subscribe) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "subscribe %q", n.Event)
	if n.From != "" {
		fmt.Fprintf(&sb, " from(%s)", n.From)
	}
	sb.WriteString(" {\n")
	sb.WriteString(p.printArrowStmtsBlock(n.Stmts, "  "))
	sb.WriteString("}\n")
	return sb.String()
}

// --- Fixture ---

func (p *printer) printFixture(n *Fixture) string {
	if n.FromPath != "" {
		return fmt.Sprintf("fixture %q from %q\n", n.Name, n.FromPath)
	}
	if n.SeedModel != "" {
		var sb strings.Builder
		fmt.Fprintf(&sb, "fixture %q seed %s", n.Name, n.SeedModel)
		if n.SeedBody != nil && len(n.SeedBody.Entries) > 0 {
			sb.WriteString(" { ")
			for i, kv := range n.SeedBody.Entries {
				if i > 0 {
					sb.WriteString(", ")
				}
				fmt.Fprintf(&sb, "%s: %s", kv.Key, p.printExpr(kv.Value))
			}
			sb.WriteString(" }")
		}
		sb.WriteString("\n")
		return sb.String()
	}
	if n.Generated != nil {
		var sb strings.Builder
		fmt.Fprintf(&sb, "fixture %q generated", n.Name)
		if len(n.Generated.Entries) > 0 {
			sb.WriteString(" { ")
			for i, kv := range n.Generated.Entries {
				if i > 0 {
					sb.WriteString(", ")
				}
				fmt.Fprintf(&sb, "%s: %s", kv.Key, p.printExpr(kv.Value))
			}
			sb.WriteString(" }")
		}
		sb.WriteString("\n")
		return sb.String()
	}
	return fmt.Sprintf("fixture %q\n", n.Name)
}

// --- Test ---

func (p *printer) printTest(n *Test) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "test %s {\n", n.Name)
	if n.Target != nil {
		fmt.Fprintf(&sb, "  target %s %s\n", n.Target.Method, n.Target.Path)
		sb.WriteString("\n")
	}
	if len(n.Setup) > 0 {
		sb.WriteString("  setup {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.Setup, "    "))
		sb.WriteString("  }\n\n")
	}
	if n.Request != nil {
		sb.WriteString(p.printTestRequest(n.Request, "  "))
		sb.WriteString("\n")
	}
	if len(n.Expect) > 0 {
		sb.WriteString("  expect {\n")
		for _, a := range n.Expect {
			fmt.Fprintf(&sb, "    %s\n", a.Raw)
		}
		sb.WriteString("  }\n")
	}
	if len(n.Cleanup) > 0 {
		sb.WriteString("\n")
		sb.WriteString("  cleanup {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.Cleanup, "    "))
		sb.WriteString("  }\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printTestRequest(r *TestRequest, indent string) string {
	var sb strings.Builder
	if r.Repeat > 0 {
		fmt.Fprintf(&sb, "%srequest repeat(%d) {\n", indent, r.Repeat)
	} else {
		fmt.Fprintf(&sb, "%srequest {\n", indent)
	}
	for _, kv := range r.Entries {
		if kv.Value != nil {
			fmt.Fprintf(&sb, "%s  %s %s\n", indent, kv.Key, p.printExpr(kv.Value))
		} else {
			// nested block like "body { ... }" is stored with nil Value sometimes
			fmt.Fprintf(&sb, "%s  %s\n", indent, kv.Key)
		}
	}
	fmt.Fprintf(&sb, "%s}\n", indent)
	return sb.String()
}

// --- TestGroup ---

func (p *printer) printTestGroup(n *TestGroup) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "test_group %s {\n", n.Name)
	if len(n.SharedSetup) > 0 {
		sb.WriteString("  shared_setup {\n")
		sb.WriteString(p.printArrowStmtsBlock(n.SharedSetup, "    "))
		sb.WriteString("  }\n\n")
	}
	if len(n.Tests) > 0 {
		sb.WriteString("  tests [\n")
		for _, t := range n.Tests {
			fmt.Fprintf(&sb, "    %s\n", t)
		}
		sb.WriteString("  ]\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// --- Arrow Statements ---

func (p *printer) printArrowStmtsBlock(stmts []ArrowStmt, indent string) string {
	var sb strings.Builder
	for _, s := range stmts {
		sb.WriteString(indent)
		sb.WriteString(p.printArrowStmt(s, indent))
		sb.WriteString("\n")
	}
	return sb.String()
}

func (p *printer) printArrowStmt(stmt ArrowStmt, indent string) string {
	switch s := stmt.(type) {
	case *InputStmt:
		return p.printInputStmt(s)
	case *StepStmt:
		return p.printStepStmt(s, indent)
	case *GuardStmt:
		return p.printGuardStmt(s, indent)
	case *WhenStmt:
		return p.printWhenStmt(s, indent)
	case *OutputStmt:
		return p.printOutputStmt(s, indent)
	case *TryRecover:
		return p.printTryRecover(s, indent)
	case *IntentStep:
		return fmt.Sprintf("|> @ %q", s.Text)
	case *GenerateStep:
		return p.printGenerateStep(s)
	default:
		return fmt.Sprintf("/* unknown stmt: %T */", stmt)
	}
}

func (p *printer) printInputStmt(s *InputStmt) string {
	var sb strings.Builder
	nameFmt := s.Name
	typeStr := p.printType(s.Type)
	if p.nameWidth > 0 {
		nameFmt = fmt.Sprintf("%-*s", p.nameWidth, s.Name)
	}
	if p.typeWidth > 0 {
		typeStr = fmt.Sprintf("%-*s", p.typeWidth, typeStr)
	}
	fmt.Fprintf(&sb, "<- %s  %s", nameFmt, typeStr)
	for _, c := range s.Constraints {
		sb.WriteString("  ")
		sb.WriteString(p.printConstraint(c))
	}
	return sb.String()
}

func (p *printer) printStepStmt(s *StepStmt, indent string) string {
	if s.Binding != "" {
		return fmt.Sprintf("|> %s = %s", s.Binding, p.printExprAt(s.Expr, indent))
	}
	return fmt.Sprintf("|> %s", p.printExprAt(s.Expr, indent))
}

func (p *printer) printGuardStmt(s *GuardStmt, indent string) string {
	if s.Message != "" {
		return fmt.Sprintf("|> guard %s -> %s %q", p.printExprAt(s.Condition, indent), s.Status, s.Message)
	}
	return fmt.Sprintf("|> guard %s -> %s", p.printExprAt(s.Condition, indent), s.Status)
}

func (p *printer) printWhenStmt(s *WhenStmt, indent string) string {
	if s.Inline != nil {
		return fmt.Sprintf("|> when %s: %s", p.printExprAt(s.Condition, indent), p.printExprAt(s.Inline, indent))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "|> when %s {\n", p.printExprAt(s.Condition, indent))
	inner := indent + "  "
	for _, st := range s.Body {
		sb.WriteString(inner)
		sb.WriteString(p.printArrowStmt(st, inner))
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "%s}", indent)
	return sb.String()
}

func (p *printer) printOutputStmt(s *OutputStmt, indent string) string {
	if s.Status != "" && s.Value != nil {
		return fmt.Sprintf("-> %s %s", s.Status, p.printExprAt(s.Value, indent))
	}
	if s.Status != "" {
		return fmt.Sprintf("-> %s", s.Status)
	}
	if s.Value != nil {
		return fmt.Sprintf("-> %s", p.printExprAt(s.Value, indent))
	}
	return "->"
}

func (p *printer) printTryRecover(s *TryRecover, indent string) string {
	var sb strings.Builder
	sb.WriteString("|> try {\n")
	inner := indent + "  "
	for _, st := range s.Try {
		sb.WriteString(inner)
		sb.WriteString(p.printArrowStmt(st, inner))
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "%s} recover {\n", indent)
	for _, st := range s.Recover {
		sb.WriteString(inner)
		sb.WriteString(p.printArrowStmt(st, inner))
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "%s}", indent)
	return sb.String()
}

func (p *printer) printGenerateStep(s *GenerateStep) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "@> %q", s.Text)
	for _, h := range s.Hints {
		if h.Value != nil {
			fmt.Fprintf(&sb, " %s(%s)", h.Name, p.printExpr(h.Value))
		} else {
			fmt.Fprintf(&sb, " %s", h.Name)
		}
	}
	return sb.String()
}

// --- Expressions ---

// isWordOp reports whether op is a word-form (alphabetic) prefix unary
// operator that must be separated from its operand by whitespace.
func isWordOp(op string) bool {
	if op == "" {
		return false
	}
	r := op[0]
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

// printExpr formats an expression without an enclosing indentation context.
// Multi-line BlockExpr values are emitted as if their lines started at
// column 0 — callers that print expressions at a known indent level should
// use printExprAt instead.
func (p *printer) printExpr(e Expr) string {
	return p.printExprAt(e, "")
}

// printExprAt formats an expression knowing that subsequent lines (e.g. of
// a multi-line BlockExpr) should be indented to align with the line on which
// the expression starts. indent is the prefix that already precedes the
// caller's line. The expression's first line is NOT prefixed by indent
// (the caller handles that); only continuation lines pick it up.
func (p *printer) printExprAt(e Expr, indent string) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *Ident:
		return n.Name
	case *StringLit:
		return blueprintQuote(n.Value)
	case *IntLit:
		return n.Value
	case *FloatLit:
		return n.Value
	case *BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *NullLit:
		return "null"
	case *NowLit:
		return "now"
	case *DurationLit:
		return n.Value
	case *SizeLit:
		return n.Value
	case *RateLit:
		return n.Value
	case *BinaryExpr:
		return fmt.Sprintf("%s %s %s", p.printExprAt(n.Left, indent), n.Op, p.printExprAt(n.Right, indent))
	case *UnaryExpr:
		// Word-form prefix operators (e.g. `not`) must be separated from the
		// operand by a space — otherwise `not existing` round-trips into the
		// single identifier `notexisting`, silently corrupting the program.
		// Symbolic operators (`!`, `-`) keep the tight form.
		if isWordOp(n.Op) {
			return fmt.Sprintf("%s %s", n.Op, p.printExprAt(n.Operand, indent))
		}
		return fmt.Sprintf("%s%s", n.Op, p.printExprAt(n.Operand, indent))
	case *FieldAccess:
		return fmt.Sprintf("%s.%s", p.printExprAt(n.Base, indent), n.Field)
	case *IndexAccess:
		return fmt.Sprintf("%s[%s]", p.printExprAt(n.Base, indent), p.printExprAt(n.Index, indent))
	case *FnCall:
		if s, ok := p.printStreamShorthand(n, indent); ok {
			return s
		}
		if s, ok := p.printDataOpShorthand(n, indent); ok {
			return s
		}
		args := make([]string, len(n.Args))
		for i, a := range n.Args {
			args[i] = p.printExprAt(a, indent)
		}
		return fmt.Sprintf("%s(%s)", n.Name, strings.Join(args, ", "))
	case *ParenExpr:
		return fmt.Sprintf("(%s)", p.printExprAt(n.Expr, indent))
	case *ListExpr:
		elems := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			elems[i] = p.printExprAt(el, indent)
		}
		return fmt.Sprintf("[%s]", strings.Join(elems, ", "))
	case *BlockExpr:
		return p.printBlockExprAt(n, indent)
	case *PathExpr:
		return n.Value
	default:
		return fmt.Sprintf("/* unknown expr: %T */", e)
	}
}

// isDataOpName reports whether name is a Blueprint data-operation keyword
// that participates in the |> step shorthand (e.g. `query todo paginate(...)`).
// Note: import_bundle / export_bundle look like regular function calls in
// source, so they keep the standard call form.
func isDataOpName(name string) bool {
	switch name {
	case "query", "save", "fetch", "update", "delete", "count", "seed":
		return true
	}
	return false
}

// isDataOpMarker reports whether e is a marker FnCall attached to a data
// operation.
func isDataOpMarker(e Expr) bool {
	fn, ok := e.(*FnCall)
	if !ok {
		return false
	}
	switch fn.Name {
	case "where", "with", "order", "paginate":
		return true
	}
	return false
}

// isFirstIdent reports whether e is the bare identifier `first`, used as a
// modifier on a data operation (e.g. `query todo where(...) first`).
func isFirstIdent(e Expr) bool {
	id, ok := e.(*Ident)
	return ok && id.Name == "first"
}

// printDataOpShorthand renders a data-operation FnCall in its canonical
// shorthand form (e.g. `query todo paginate(page, per_page)`), preserving
// the README idiom and column alignment found in hand-written sources.
// Returns ("", false) when n does not match the shorthand shape, in which
// case callers should fall back to the standard `name(args, ...)` form.
func (p *printer) printDataOpShorthand(n *FnCall, indent string) (string, bool) {
	if !isDataOpName(n.Name) {
		return "", false
	}
	if len(n.Args) == 0 {
		return "", false
	}
	model, ok := n.Args[0].(*Ident)
	if !ok {
		return "", false
	}

	rest := n.Args[1:]

	// For `fetch`, the optional second positional argument is the id
	// expression rendered inside parens: `fetch todo(id)`. Detect it as
	// "first arg that isn't a marker / block / `first`".
	var idArg Expr
	if n.Name == "fetch" && len(rest) > 0 {
		first := rest[0]
		if _, isBlock := first.(*BlockExpr); !isBlock && !isDataOpMarker(first) && !isFirstIdent(first) {
			idArg = first
			rest = rest[1:]
		}
	}

	// All remaining args must be one of: BlockExpr (body), marker FnCall
	// (where/order/paginate), or the ident `first`. Anything else (e.g. a
	// stray scalar from a hand-built AST) bails out to the standard form.
	for _, a := range rest {
		if _, ok := a.(*BlockExpr); ok {
			continue
		}
		if isDataOpMarker(a) || isFirstIdent(a) {
			continue
		}
		return "", false
	}

	var sb strings.Builder
	sb.WriteString(n.Name)
	sb.WriteByte(' ')
	sb.WriteString(model.Name)
	if idArg != nil {
		sb.WriteByte('(')
		sb.WriteString(p.printExprAt(idArg, indent))
		sb.WriteByte(')')
	}
	for _, a := range rest {
		sb.WriteByte(' ')
		sb.WriteString(p.printExprAt(a, indent))
	}
	return sb.String(), true
}

// printStreamShorthand renders stream / WS / log operations in their
// hand-written shorthand form so that bp fmt round-trips them through the
// parser cleanly. Without this, an `|> inject payload.user as sender` step
// gets reprinted as `|> inject(payload.user, sender)` — which is not valid
// Blueprint syntax and fails on re-parse.
//
// Covered shapes:
//
//	inject X            -> "inject X"
//	inject X as Y       -> "inject X as Y"
//	join   T(args)      -> "join T(args)"     (also leave)
//	broadcast T(args)   -> "broadcast T(args)"        (also whisper)
//	broadcast T(args) { -> "broadcast T(args) { ... }"
//	  body }
//	log "msg"           -> `log "msg"`
//	log "msg" level(L)  -> `log "msg" level(L)`
func (p *printer) printStreamShorthand(n *FnCall, indent string) (string, bool) {
	switch n.Name {
	case "inject":
		if len(n.Args) == 0 || len(n.Args) > 2 {
			return "", false
		}
		var sb strings.Builder
		sb.WriteString("inject ")
		sb.WriteString(p.printExprAt(n.Args[0], indent))
		if len(n.Args) == 2 {
			alias, ok := n.Args[1].(*Ident)
			if !ok {
				return "", false
			}
			sb.WriteString(" as ")
			sb.WriteString(alias.Name)
		}
		return sb.String(), true
	case "join", "leave":
		// Parser shape: { Name: op, Args: [ FnCall{Name: target, Args: [id?]} ] }
		if len(n.Args) != 1 {
			return "", false
		}
		target, ok := n.Args[0].(*FnCall)
		if !ok {
			return "", false
		}
		var sb strings.Builder
		sb.WriteString(n.Name)
		sb.WriteByte(' ')
		sb.WriteString(target.Name)
		if len(target.Args) > 0 {
			parts := make([]string, len(target.Args))
			for i, a := range target.Args {
				parts[i] = p.printExprAt(a, indent)
			}
			sb.WriteByte('(')
			sb.WriteString(strings.Join(parts, ", "))
			sb.WriteByte(')')
		}
		return sb.String(), true
	case "broadcast", "whisper":
		// Parser shape: { Name: op, Args: [ FnCall{Name: target, Args: [id?]}, BlockExpr? ] }
		if len(n.Args) < 1 || len(n.Args) > 2 {
			return "", false
		}
		target, ok := n.Args[0].(*FnCall)
		if !ok {
			return "", false
		}
		var sb strings.Builder
		sb.WriteString(n.Name)
		sb.WriteByte(' ')
		sb.WriteString(target.Name)
		if len(target.Args) > 0 {
			parts := make([]string, len(target.Args))
			for i, a := range target.Args {
				parts[i] = p.printExprAt(a, indent)
			}
			sb.WriteByte('(')
			sb.WriteString(strings.Join(parts, ", "))
			sb.WriteByte(')')
		}
		if len(n.Args) == 2 {
			block, ok := n.Args[1].(*BlockExpr)
			if !ok {
				return "", false
			}
			sb.WriteByte(' ')
			sb.WriteString(p.printBlockExprAt(block, indent))
		}
		return sb.String(), true
	case "log":
		// Parser shape: { Name: "log", Args: [ msg, levelIdent? ] }
		if len(n.Args) == 0 || len(n.Args) > 2 {
			return "", false
		}
		var sb strings.Builder
		sb.WriteString("log ")
		sb.WriteString(p.printExprAt(n.Args[0], indent))
		if len(n.Args) == 2 {
			lvl, ok := n.Args[1].(*Ident)
			if !ok {
				return "", false
			}
			sb.WriteString(" level(")
			sb.WriteString(lvl.Name)
			sb.WriteString(")")
		}
		return sb.String(), true
	case "map":
		// Parser shape: { Name: "map", Args: [ collection, body? ] }
		// Sources write `map collection: body` (body optional). The default
		// call-form `map(coll, body)` is not valid Blueprint syntax.
		if len(n.Args) == 0 || len(n.Args) > 2 {
			return "", false
		}
		var sb strings.Builder
		sb.WriteString("map ")
		sb.WriteString(p.printExprAt(n.Args[0], indent))
		if len(n.Args) == 2 {
			sb.WriteString(": ")
			sb.WriteString(p.printExprAt(n.Args[1], indent))
		}
		return sb.String(), true
	case "emit":
		// Parser shape: { Name: "emit", Args: [ eventNameStringOrIdent, toIdent?, BlockExpr? ] }
		// The optional service-target slot is identified by looking for the
		// trailing BlockExpr (always the body when present) and treating an
		// Ident between event and body as `to(svc)`.
		if len(n.Args) == 0 {
			return "", false
		}
		var sb strings.Builder
		sb.WriteString("emit ")
		// Event name: either a string literal or an identifier.
		switch ev := n.Args[0].(type) {
		case *StringLit:
			sb.WriteString(blueprintQuote(ev.Value))
		case *Ident:
			sb.WriteString(ev.Name)
		default:
			return "", false
		}
		rest := n.Args[1:]
		// Optional `to(svc)` lives between event and body and is an Ident.
		var body *BlockExpr
		if len(rest) > 0 {
			if b, ok := rest[len(rest)-1].(*BlockExpr); ok {
				body = b
				rest = rest[:len(rest)-1]
			}
		}
		if len(rest) > 1 {
			return "", false
		}
		if len(rest) == 1 {
			to, ok := rest[0].(*Ident)
			if !ok {
				return "", false
			}
			sb.WriteString(" to(")
			sb.WriteString(to.Name)
			sb.WriteString(")")
		}
		if body != nil {
			sb.WriteByte(' ')
			sb.WriteString(p.printBlockExprAt(body, indent))
		}
		return sb.String(), true
	}
	return "", false
}

// printBlockExpr renders a BlockExpr without an enclosing indent context.
// Kept for backwards-compatibility with callers that genuinely don't have
// indentation state (e.g. test scaffolding); production code paths should
// prefer printBlockExprAt so multi-line blocks line up.
func (p *printer) printBlockExpr(n *BlockExpr) string {
	return p.printBlockExprAt(n, "")
}

// printBlockExprAt renders a BlockExpr knowing that the opening brace
// appears on a line already prefixed by indent. The closing brace and each
// entry line are emitted with matching indentation so the printer round-
// trips correctly under bp check.
func (p *printer) printBlockExprAt(n *BlockExpr, indent string) string {
	if len(n.Entries) == 0 {
		return "{}"
	}
	if len(n.Entries) <= 3 {
		parts := make([]string, len(n.Entries))
		for i, kv := range n.Entries {
			parts[i] = fmt.Sprintf("%s: %s", kv.Key, p.printExprAt(kv.Value, indent))
		}
		return fmt.Sprintf("{ %s }", strings.Join(parts, ", "))
	}
	inner := indent + "  "
	var sb strings.Builder
	sb.WriteString("{\n")
	for _, kv := range n.Entries {
		fmt.Fprintf(&sb, "%s%s: %s,\n", inner, kv.Key, p.printExprAt(kv.Value, inner))
	}
	sb.WriteString(indent)
	sb.WriteString("}")
	return sb.String()
}

// --- Type Expressions ---

func (p *printer) printType(t TypeExpr) string {
	if t == nil {
		return ""
	}
	switch n := t.(type) {
	case *PrimitiveType:
		return n.Name
	case *TypedJSONType:
		return fmt.Sprintf("json<%s>", p.printType(n.Inner))
	case *TranslationKeyType:
		return fmt.Sprintf("tkey(%s)", n.Namespace)
	case *NamedType:
		return n.Name
	case *EnumInline:
		return fmt.Sprintf("enum(%s)", strings.Join(n.Variants, ", "))
	case *ListType:
		return fmt.Sprintf("list(%s)", p.printType(n.Element))
	case *MapType:
		return fmt.Sprintf("map(%s, %s)", p.printType(n.Key), p.printType(n.Value))
	case *MimeTypeExpr:
		return fmt.Sprintf("%s/%s", n.Type, n.Subtype)
	default:
		return fmt.Sprintf("/* unknown type: %T */", t)
	}
}

// --- Constraints ---

func (p *printer) printConstraint(c *Constraint_) string {
	switch c.Kind {
	case "primary", "unique", "index", "required", "optional", "auto":
		return c.Kind
	default:
		if c.Value != nil {
			return fmt.Sprintf("%s(%s)", c.Kind, p.printExpr(c.Value))
		}
		return c.Kind
	}
}
