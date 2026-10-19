package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/agentctx"
	"github.com/abdul-hamid-achik/blueprint/internal/diag"
	"github.com/abdul-hamid-achik/blueprint/internal/mcp"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// mcpInstructions is sent to clients during initialization and discovery.
const mcpInstructions = "Blueprint compiles declarative .bp service specs into runnable TypeScript or Python projects. " +
	"Call check after every .bp edit and fix every error before build. " +
	"Use context (topics such as language, cli, errors, targets, workflow) for the language reference, and explain for the meaning of an error code such as C052."

// mcpSubprocessTimeout bounds one bp build or bp diff run started by a tool.
const mcpSubprocessTimeout = 10 * time.Minute

// cmdMCP serves the Model Context Protocol over stdin and stdout. stdout is
// the protocol channel, so os.Stdout is redirected to stderr for the life of
// the server; any stray print from reused code lands there.
func cmdMCP() int {
	protoOut := os.Stdout
	os.Stdout = os.Stderr
	fmt.Fprintln(os.Stderr, "Blueprint MCP server (stdio) ready")
	if err := newMCPServer().Serve(os.Stdin, protoOut); err != nil {
		fmt.Fprintf(os.Stderr, "MCP error: %v\n", err)
		return 1
	}
	return 0
}

// newMCPServer registers the tools and resources in their advertised order.
func newMCPServer() *mcp.Server {
	s := mcp.NewServer("blueprint", version, mcpInstructions)
	readOnly := map[string]any{"readOnlyHint": true}

	s.AddTool(mcp.Tool{
		Name:        "check",
		Title:       "Check Blueprint source",
		Description: "Parse and semantically check one .bp file or inline source. Returns the same JSON as `bp check --json`.",
		InputSchema: sourceSchema(),
		Annotations: readOnly,
		Handler:     toolCheck,
	})
	s.AddTool(mcp.Tool{
		Name:        "lint",
		Title:       "Lint Blueprint source",
		Description: "Lint one .bp file or inline source for best practices. Returns the same JSON as `bp lint --json`.",
		InputSchema: sourceSchema(),
		Annotations: readOnly,
		Handler:     toolLint,
	})
	s.AddTool(mcp.Tool{
		Name:        "format",
		Title:       "Format Blueprint source",
		Description: "Format .bp source the way `bp fmt` does, without writing anything.",
		InputSchema: sourceSchema(),
		Annotations: readOnly,
		Handler:     toolFormat,
	})
	s.AddTool(mcp.Tool{
		Name:        "explain",
		Title:       "Explain a diagnostic code",
		Description: "Documentation for a Blueprint diagnostic code such as C052.",
		InputSchema: schemaObject(map[string]any{
			"code": propString("Diagnostic code, for example C052. Case does not matter."),
		}, "code"),
		Annotations: readOnly,
		Handler:     toolExplain,
	})
	s.AddTool(mcp.Tool{
		Name:        "context",
		Title:       "Blueprint reference context",
		Description: "Agent-facing Blueprint reference (the same text as `bp context`).",
		InputSchema: schemaObject(map[string]any{
			"topic": map[string]any{
				"type":        "string",
				"description": "Topic to print. Omit for the full surface; \"all\" prints every topic in one document.",
				"enum":        append(agentctx.Topics(), "all"),
			},
		}),
		Annotations: readOnly,
		Handler:     toolContext,
	})
	s.AddTool(mcp.Tool{
		Name:        "build",
		Title:       "Build a Blueprint project",
		Description: "Generate the project for a .bp file (same as `bp build`). Writes into `out`; refuses a non-empty directory that bp did not create.",
		InputSchema: schemaObject(map[string]any{
			"path":   propString("Path to the .bp file. Relative paths resolve against the server's working directory."),
			"out":    propStringDefault("Output directory. Relative paths resolve against the server's working directory.", "generated"),
			"target": propEnum("Codegen target.", []string{targetNode, targetPython, targetEffect}, targetNode),
		}, "path"),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true},
		Handler:     toolBuild,
	})
	s.AddTool(mcp.Tool{
		Name:        "diff",
		Title:       "Diff Blueprint build output",
		Description: "Show what `bp build` would change in `out` without writing (same as `bp diff`).",
		InputSchema: schemaObject(map[string]any{
			"path":   propString("Path to the .bp file. Relative paths resolve against the server's working directory."),
			"out":    propStringDefault("Output directory to compare against. Relative paths resolve against the server's working directory.", "generated"),
			"target": propEnum("Codegen target.", []string{targetNode, targetPython, targetEffect}, targetNode),
		}, "path"),
		Annotations: readOnly,
		Handler:     toolDiff,
	})

	s.AddResource(mcp.Resource{
		URI:         "blueprint://context",
		Name:        "context",
		Description: "Full agent-facing Blueprint surface (same as bp context).",
		MimeType:    "text/markdown",
		Read: func() (string, error) {
			var b bytes.Buffer
			err := agentctx.RenderSurface(agentctx.FullSurface(version), "md", &b)
			return b.String(), err
		},
	})
	for _, topic := range agentctx.Topics() {
		s.AddResource(mcp.Resource{
			URI:         "blueprint://context/" + topic,
			Name:        "context-" + topic,
			Description: "Blueprint context topic " + topic + " (same as bp context " + topic + ").",
			MimeType:    "text/markdown",
			Read:        topicReader(topic),
		})
	}
	s.AddResource(mcp.Resource{
		URI:         "blueprint://llms",
		Name:        "llms",
		Description: "Complete agent and LLM guide (same as bp llms).",
		MimeType:    "text/markdown",
		Read: func() (string, error) {
			return agentctx.FullGuide(version), nil
		},
	})
	return s
}

func topicReader(topic string) func() (string, error) {
	return func() (string, error) {
		t, err := agentctx.Get(topic)
		if err != nil {
			return "", err
		}
		var b bytes.Buffer
		err = agentctx.RenderTopic(t, "md", &b)
		return b.String(), err
	}
}

// sourceArgs is the input shared by check, lint and format: a path or inline
// source, plus the name to use for inline source.
type sourceArgs struct {
	Path     *string `json:"path"`
	Source   *string `json:"source"`
	Filename *string `json:"filename"`
}

func sourceSchema() map[string]any {
	return schemaObject(map[string]any{
		"path":     propString("Path to a .bp file. Relative paths resolve against the server's working directory. Pass exactly one of path or source."),
		"source":   propString("Inline .bp source text. Pass exactly one of path or source."),
		"filename": propStringDefault("Name used in diagnostics and include resolution when source is given; default input.bp in the working directory.", "input.bp"),
	})
}

// loadSource returns the absolute name and the bytes of the input. Inline
// source is named after filename, resolved against the working directory.
func loadSource(a sourceArgs) (string, []byte, error) {
	if (a.Path == nil) == (a.Source == nil) {
		return "", nil, errors.New("pass exactly one of path or source")
	}
	if a.Path != nil {
		abs, err := filepath.Abs(*a.Path)
		if err != nil {
			return "", nil, err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", nil, fmt.Errorf("cannot read %s: %v", abs, err)
		}
		return abs, data, nil
	}
	name := "input.bp"
	if a.Filename != nil && *a.Filename != "" {
		name = *a.Filename
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", nil, err
	}
	return abs, []byte(*a.Source), nil
}

func toolCheck(args json.RawMessage) mcp.ToolResult {
	var a sourceArgs
	if err := decodeToolArgs(args, &a); err != nil {
		return toolFailure(err.Error())
	}
	name, src, err := loadSource(a)
	if err != nil {
		return toolFailure(err.Error())
	}
	var buf bytes.Buffer
	checkSource(name, src, true, &buf, io.Discard)
	return jsonToolResult(buf.Bytes())
}

func toolLint(args json.RawMessage) mcp.ToolResult {
	var a sourceArgs
	if err := decodeToolArgs(args, &a); err != nil {
		return toolFailure(err.Error())
	}
	name, src, err := loadSource(a)
	if err != nil {
		return toolFailure(err.Error())
	}
	var buf bytes.Buffer
	lintSource(name, src, true, &buf, io.Discard)
	return jsonToolResult(buf.Bytes())
}

func toolFormat(args json.RawMessage) mcp.ToolResult {
	var a sourceArgs
	if err := decodeToolArgs(args, &a); err != nil {
		return toolFailure(err.Error())
	}
	name, src, err := loadSource(a)
	if err != nil {
		return toolFailure(err.Error())
	}
	file, parseErrors := parseFormatSource(name, src)
	if len(parseErrors) > 0 {
		var b strings.Builder
		for _, e := range parseErrors {
			b.WriteString(parser.FormatError(e, src))
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "\n%d syntax error(s) found", len(parseErrors))
		return toolFailure(b.String())
	}
	formatted := formatPrinter(file)
	if err := verifyFormatted(file, name, formatted); err != nil {
		return toolFailure("bp fmt refused its own output (formatter bug); the source is unchanged: " + err.Error())
	}
	return mcp.ToolResult{
		Text: formatted,
		Structured: map[string]any{
			"file":      name,
			"changed":   formatted != string(src),
			"formatted": formatted,
		},
	}
}

func toolExplain(args json.RawMessage) mcp.ToolResult {
	var a struct {
		Code *string `json:"code"`
	}
	if err := decodeToolArgs(args, &a); err != nil {
		return toolFailure(err.Error())
	}
	if a.Code == nil || strings.TrimSpace(*a.Code) == "" {
		return toolFailure("code is required")
	}
	body, ok := diag.Lookup(*a.Code)
	if !ok {
		return toolFailure(fmt.Sprintf("no documentation for code %q; see docs/error-codes.md for the full list of documented codes",
			strings.ToUpper(strings.TrimSpace(*a.Code))))
	}
	return mcp.ToolResult{Text: body}
}

func toolContext(args json.RawMessage) mcp.ToolResult {
	var a struct {
		Topic *string `json:"topic"`
	}
	if err := decodeToolArgs(args, &a); err != nil {
		return toolFailure(err.Error())
	}
	topic := ""
	if a.Topic != nil {
		topic = strings.TrimSpace(*a.Topic)
	}
	var b bytes.Buffer
	switch topic {
	case "":
		if err := agentctx.RenderSurface(agentctx.FullSurface(version), "md", &b); err != nil {
			return toolFailure(err.Error())
		}
	case "all":
		b.WriteString(agentctx.FullGuide(version))
	default:
		t, err := agentctx.Get(topic)
		if err != nil {
			return toolFailure(err.Error())
		}
		if err := agentctx.RenderTopic(t, "md", &b); err != nil {
			return toolFailure(err.Error())
		}
	}
	return mcp.ToolResult{Text: b.String()}
}

// buildArgs is the input of the build and diff tools.
type buildArgs struct {
	Path   *string `json:"path"`
	Out    *string `json:"out"`
	Target *string `json:"target"`
}

// buildOutcome is the structured result of a bp build run.
type buildOutcome struct {
	ExitCode int    `json:"exit_code"`
	OutDir   string `json:"out_dir"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// diffOutcome is the structured result of a bp diff run.
type diffOutcome struct {
	ExitCode int    `json:"exit_code"`
	Changed  bool   `json:"changed"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// parseBuildArgs validates the build and diff inputs and returns the absolute
// source path, the absolute output directory and the canonical target.
func parseBuildArgs(args json.RawMessage) (path, outDir, target string, errResult *mcp.ToolResult) {
	var a buildArgs
	if err := decodeToolArgs(args, &a); err != nil {
		r := toolFailure(err.Error())
		return "", "", "", &r
	}
	if a.Path == nil || *a.Path == "" {
		r := toolFailure("path is required")
		return "", "", "", &r
	}
	absPath, err := filepath.Abs(*a.Path)
	if err != nil {
		r := toolFailure(err.Error())
		return "", "", "", &r
	}
	if info, err := os.Stat(absPath); err != nil || info.IsDir() {
		r := toolFailure(fmt.Sprintf("cannot read %s: not a .bp file", absPath))
		return "", "", "", &r
	}
	out := "generated"
	if a.Out != nil && *a.Out != "" {
		out = *a.Out
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		r := toolFailure(err.Error())
		return "", "", "", &r
	}
	raw := ""
	if a.Target != nil {
		raw = *a.Target
	}
	t, err := resolveTarget(raw)
	if err != nil {
		r := toolFailure(err.Error())
		return "", "", "", &r
	}
	return absPath, absOut, t, nil
}

func toolBuild(args json.RawMessage) mcp.ToolResult {
	path, outDir, target, bad := parseBuildArgs(args)
	if bad != nil {
		return *bad
	}
	code, stdout, stderr := runBPSubcommand("build", path, "--out", outDir, "--target", target)

	var b strings.Builder
	if code == 0 {
		fmt.Fprintf(&b, "bp build succeeded; project written to %s\n", outDir)
	} else {
		fmt.Fprintf(&b, "bp build failed with exit code %d (1 = the spec has errors, 2 = usage, environment or unsafe out directory, 4 = the target cannot generate this spec)\n", code)
	}
	appendSection(&b, "stdout", stdout)
	appendSection(&b, "stderr", stderr)
	res := mcp.ToolResult{
		Text:       b.String(),
		Structured: buildOutcome{ExitCode: code, OutDir: outDir, Stdout: stdout, Stderr: stderr},
		IsError:    code != 0,
	}
	return res
}

func toolDiff(args json.RawMessage) mcp.ToolResult {
	path, outDir, target, bad := parseBuildArgs(args)
	if bad != nil {
		return *bad
	}
	code, stdout, stderr := runBPSubcommand("diff", path, "--out", outDir, "--target", target, "--no-color")
	outcome := diffOutcome{
		ExitCode: code,
		Changed:  code == 0 && !strings.Contains(stdout, diffUpToDateMessage),
		Stdout:   stdout,
		Stderr:   stderr,
	}
	text, _ := json.Marshal(outcome)
	return mcp.ToolResult{
		Text:       string(text),
		Structured: outcome,
		IsError:    code != 0,
	}
}

// runBPSubcommand runs this binary with args and returns its exit code and
// captured output. The child gets no stdin, NO_COLOR=1, and a timeout. A
// failure to start or a timeout reports exit code -1.
func runBPSubcommand(args ...string) (int, string, string) {
	exe, err := os.Executable()
	if err != nil {
		return -1, "", "cannot locate the bp executable: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpSubprocessTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
			fmt.Fprintf(&stderr, "%v\n", err)
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(&stderr, "timed out after %s\n", mcpSubprocessTimeout)
	}
	return code, stdout.String(), stderr.String()
}

func appendSection(b *strings.Builder, label, body string) {
	if body == "" {
		return
	}
	fmt.Fprintf(b, "\n%s:\n%s", label, body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
}

// decodeToolArgs decodes a tool's arguments object and rejects unknown
// members, matching the additionalProperties: false schemas.
func decodeToolArgs(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

// toolFailure is an isError result with a plain message.
func toolFailure(msg string) mcp.ToolResult {
	return mcp.ToolResult{Text: msg, IsError: true}
}

// jsonToolResult turns a JSON document into a result with the same compact
// JSON as text and as structuredContent.
func jsonToolResult(data []byte) mcp.ToolResult {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return toolFailure("internal error: checker produced invalid JSON: " + err.Error())
	}
	text, err := json.Marshal(v)
	if err != nil {
		return toolFailure("internal error: " + err.Error())
	}
	return mcp.ToolResult{Text: string(text), Structured: v}
}

// schemaObject builds a closed object schema. required is omitted when empty.
func schemaObject(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func propString(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func propStringDefault(desc, def string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "default": def}
}

func propEnum(desc string, values []string, def string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values, "default": def}
}
