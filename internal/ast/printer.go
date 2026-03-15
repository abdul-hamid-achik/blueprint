package ast

import (
	"fmt"
	"strings"
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
	return p.printFile(f)
}

type printer struct{}

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
	for _, kv := range n.Entries {
		fmt.Fprintf(&sb, "  %s %s\n", kv.Key, p.printExpr(kv.Value))
	}
	for _, use := range n.Uses {
		sb.WriteString("  ")
		sb.WriteString(p.printUseStmt(use))
		sb.WriteString("\n")
	}
	sb.WriteString("}")
	sb.WriteString("\n")
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

// --- Model ---

func (p *printer) printModel(n *Model) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	fmt.Fprintf(&sb, "model %s {\n", n.Name)
	for _, f := range n.Fields {
		sb.WriteString("  ")
		sb.WriteString(p.printField(f))
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printContent(n *Content) string {
	var sb strings.Builder
	if n.Intent != nil {
		sb.WriteString(p.printIntent(n.Intent))
	}
	sb.WriteString("content " + n.Name + " {\n")
	for _, f := range n.Fields {
		sb.WriteString("  " + p.printField(f) + "\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// --- Field ---

func (p *printer) printField(f *Field) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s", f.Name, p.printType(f.Type))
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
	var sb strings.Builder
	fmt.Fprintf(&sb, "%simpl %s {\n", indent, impl.Strategy)
	for _, kv := range impl.Entries {
		fmt.Fprintf(&sb, "%s  %s: %s\n", indent, kv.Key, p.printExpr(kv.Value))
	}
	fmt.Fprintf(&sb, "%s}\n", indent)
	return sb.String()
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
	sb.WriteString(p.printArrowStmtsBlock(n.Stmts, "  "))
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
	for _, h := range n.Handlers {
		sb.WriteString(p.printStreamHandler(h, "  "))
	}
	sb.WriteString("}\n")
	return sb.String()
}

func (p *printer) printStreamHandler(h *StreamHandler, indent string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%son %s", indent, h.EventName)
	if h.Condition != nil {
		fmt.Fprintf(&sb, "(%s)", p.printExpr(h.Condition))
	}
	if h.Timeout != "" {
		fmt.Fprintf(&sb, " timeout(%s)", h.Timeout)
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
		return p.printStepStmt(s)
	case *GuardStmt:
		return p.printGuardStmt(s)
	case *WhenStmt:
		return p.printWhenStmt(s, indent)
	case *OutputStmt:
		return p.printOutputStmt(s)
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
	fmt.Fprintf(&sb, "<- %s  %s", s.Name, p.printType(s.Type))
	for _, c := range s.Constraints {
		sb.WriteString("  ")
		sb.WriteString(p.printConstraint(c))
	}
	return sb.String()
}

func (p *printer) printStepStmt(s *StepStmt) string {
	if s.Binding != "" {
		return fmt.Sprintf("|> %s = %s", s.Binding, p.printExpr(s.Expr))
	}
	return fmt.Sprintf("|> %s", p.printExpr(s.Expr))
}

func (p *printer) printGuardStmt(s *GuardStmt) string {
	if s.Message != "" {
		return fmt.Sprintf("|> guard %s -> %s %q", p.printExpr(s.Condition), s.Status, s.Message)
	}
	return fmt.Sprintf("|> guard %s -> %s", p.printExpr(s.Condition), s.Status)
}

func (p *printer) printWhenStmt(s *WhenStmt, indent string) string {
	if s.Inline != nil {
		return fmt.Sprintf("|> when %s: %s", p.printExpr(s.Condition), p.printExpr(s.Inline))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "|> when %s {\n", p.printExpr(s.Condition))
	inner := indent + "  "
	for _, st := range s.Body {
		sb.WriteString(inner)
		sb.WriteString(p.printArrowStmt(st, inner))
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "%s}", indent)
	return sb.String()
}

func (p *printer) printOutputStmt(s *OutputStmt) string {
	if s.Status != "" && s.Value != nil {
		return fmt.Sprintf("-> %s %s", s.Status, p.printExpr(s.Value))
	}
	if s.Status != "" {
		return fmt.Sprintf("-> %s", s.Status)
	}
	if s.Value != nil {
		return fmt.Sprintf("-> %s", p.printExpr(s.Value))
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

func (p *printer) printExpr(e Expr) string {
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
		return fmt.Sprintf("%s %s %s", p.printExpr(n.Left), n.Op, p.printExpr(n.Right))
	case *UnaryExpr:
		return fmt.Sprintf("%s%s", n.Op, p.printExpr(n.Operand))
	case *FieldAccess:
		return fmt.Sprintf("%s.%s", p.printExpr(n.Base), n.Field)
	case *IndexAccess:
		return fmt.Sprintf("%s[%s]", p.printExpr(n.Base), p.printExpr(n.Index))
	case *FnCall:
		args := make([]string, len(n.Args))
		for i, a := range n.Args {
			args[i] = p.printExpr(a)
		}
		return fmt.Sprintf("%s(%s)", n.Name, strings.Join(args, ", "))
	case *ParenExpr:
		return fmt.Sprintf("(%s)", p.printExpr(n.Expr))
	case *ListExpr:
		elems := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			elems[i] = p.printExpr(el)
		}
		return fmt.Sprintf("[%s]", strings.Join(elems, ", "))
	case *BlockExpr:
		return p.printBlockExpr(n)
	case *PathExpr:
		return n.Value
	default:
		return fmt.Sprintf("/* unknown expr: %T */", e)
	}
}

func (p *printer) printBlockExpr(n *BlockExpr) string {
	if len(n.Entries) == 0 {
		return "{}"
	}
	if len(n.Entries) <= 3 {
		parts := make([]string, len(n.Entries))
		for i, kv := range n.Entries {
			parts[i] = fmt.Sprintf("%s: %s", kv.Key, p.printExpr(kv.Value))
		}
		return fmt.Sprintf("{ %s }", strings.Join(parts, ", "))
	}
	var sb strings.Builder
	sb.WriteString("{\n")
	for _, kv := range n.Entries {
		fmt.Fprintf(&sb, "  %s: %s,\n", kv.Key, p.printExpr(kv.Value))
	}
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
