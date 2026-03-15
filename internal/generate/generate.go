package generate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// Slot is a resolved @> generate directive with surrounding context.
type Slot struct {
	Line    int      // source line (1-based) of the @> directive
	Text    string   // the prompt text (e.g. "add rate limiting")
	Hints   []string // hint strings like "using(redis)", "max_lines(3)"
	Context string   // a description of the parent block for the LLM prompt
}

// hintValueStr converts an ast.Expr hint value to a string representation.
func hintValueStr(v ast.Expr) string {
	switch e := v.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.IntLit:
		return e.Value
	case *ast.FloatLit:
		return e.Value
	case *ast.StringLit:
		return e.Value
	default:
		return "..."
	}
}

// collectHints converts a slice of ast.Hint into hint strings.
func collectHints(hints []ast.Hint) []string {
	result := make([]string, 0, len(hints))
	for _, h := range hints {
		if h.Value == nil {
			result = append(result, h.Name)
		} else {
			result = append(result, fmt.Sprintf("%s(%s)", h.Name, hintValueStr(h.Value)))
		}
	}
	return result
}

// collectFromStmts walks a slice of ArrowStmt, recursively entering
// TryRecover and WhenStmt bodies, and appends any GenerateStep slots
// found to *out using ctx as the parent context string.
func collectFromStmts(stmts []ast.ArrowStmt, ctx string, out *[]Slot) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.GenerateStep:
			*out = append(*out, Slot{
				Line:    s.Loc.Line,
				Text:    s.Text,
				Hints:   collectHints(s.Hints),
				Context: ctx,
			})
		case *ast.TryRecover:
			collectFromStmts(s.Try, ctx, out)
			collectFromStmts(s.Recover, ctx, out)
		case *ast.WhenStmt:
			collectFromStmts(s.Body, ctx, out)
		}
	}
}

// FindSlots walks f and returns all GenerateStep nodes as Slots.
func FindSlots(f *ast.File) []Slot {
	var slots []Slot

	for _, block := range f.Blocks {
		switch b := block.(type) {
		case *ast.Endpoint:
			ctx := fmt.Sprintf("%s %s endpoint", b.Method, b.Path)
			collectFromStmts(b.Stmts, ctx, &slots)

		case *ast.StreamEndpoint:
			ctx := fmt.Sprintf("stream %s endpoint", b.Path)
			collectFromStmts(b.Stmts, ctx, &slots)
			for _, h := range b.Handlers {
				collectFromStmts(h.Body, ctx, &slots)
			}

		case *ast.WsEndpoint:
			ctx := fmt.Sprintf("ws %s endpoint", b.Path)
			collectFromStmts(b.OnConnect, ctx, &slots)
			collectFromStmts(b.OnMessage, ctx, &slots)
			collectFromStmts(b.OnDisconnect, ctx, &slots)

		case *ast.Pipe:
			ctx := fmt.Sprintf("pipe %s", b.Name)
			collectFromStmts(b.Stmts, ctx, &slots)

		case *ast.Fn:
			ctx := fmt.Sprintf("fn %s", b.Name)
			if b.Logic != nil {
				collectFromStmts(b.Logic.Stmts, ctx, &slots)
			}

		case *ast.Middleware:
			ctx := fmt.Sprintf("middleware %s", b.Name)
			collectFromStmts(b.Before, ctx, &slots)
			collectFromStmts(b.After, ctx, &slots)

		case *ast.Schedule:
			ctx := fmt.Sprintf("schedule %s", b.Name)
			collectFromStmts(b.Stmts, ctx, &slots)

		case *ast.Worker:
			ctx := fmt.Sprintf("worker %s", b.Name)
			collectFromStmts(b.Stmts, ctx, &slots)
			collectFromStmts(b.OnFail, ctx, &slots)

		case *ast.Subscribe:
			ctx := fmt.Sprintf("subscribe %s", b.Event)
			collectFromStmts(b.Stmts, ctx, &slots)

		case *ast.Test:
			ctx := fmt.Sprintf("test %s", b.Name)
			collectFromStmts(b.Setup, ctx, &slots)
			collectFromStmts(b.Cleanup, ctx, &slots)
		}
	}

	return slots
}

// --- Anthropic API types ---

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
}

// buildPrompt constructs the LLM prompt for a given slot.
func buildPrompt(slot Slot) string {
	var sb strings.Builder

	sb.WriteString("You are a Blueprint (.bp) language assistant. Blueprint is a declarative language for web services.\n")
	sb.WriteString("Generate Blueprint arrow statements to implement the following step.\n\n")
	fmt.Fprintf(&sb, "Context: %s\n", slot.Context)
	fmt.Fprintf(&sb, "Step to implement: @> %q", slot.Text)

	if len(slot.Hints) > 0 {
		fmt.Fprintf(&sb, "\nHints: %s", strings.Join(slot.Hints, ", "))
	}

	sb.WriteString("\n\nRules:\n")
	sb.WriteString("- Output ONLY valid Blueprint arrow statements (|> step, guard, when, -> output)\n")
	sb.WriteString("- No explanation, no markdown, no code fences\n")
	sb.WriteString("- Use existing Blueprint syntax: |> var = operation, guard condition -> status \"message\"\n")
	sb.WriteString("- Keep it concise (max 5 lines)\n")

	return sb.String()
}

// callAnthropicAPI sends one prompt to the Anthropic Messages API and returns
// the generated text.
func callAnthropicAPI(prompt, apiKey string, client *http.Client) (string, error) {
	reqBody := anthropicRequest{
		Model:     "claude-haiku-4-5-20251001",
		MaxTokens: 256,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("generate: marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("generate: create request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("generate: HTTP request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return "", fmt.Errorf("generate: Anthropic API returned status %d: %s", resp.StatusCode, buf.String())
	}

	var apiResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", fmt.Errorf("generate: decode response: %w", err)
	}

	for _, block := range apiResp.Content {
		if block.Type == "text" {
			return strings.TrimSpace(block.Text), nil
		}
	}

	return "", fmt.Errorf("generate: no text content in Anthropic API response")
}

// GenerateAll calls the Anthropic Messages API for each slot and returns
// a map from source line to generated Blueprint arrow-statement text.
// apiKey is the Anthropic API key (from env ANTHROPIC_API_KEY).
func GenerateAll(slots []Slot, apiKey string) (map[int]string, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("generate: ANTHROPIC_API_KEY is not set")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	result := make(map[int]string, len(slots))

	for _, slot := range slots {
		prompt := buildPrompt(slot)
		text, err := callAnthropicAPI(prompt, apiKey, client)
		if err != nil {
			return nil, fmt.Errorf("generate: slot at line %d (%q): %w", slot.Line, slot.Text, err)
		}
		result[slot.Line] = text
	}

	return result, nil
}

// Apply replaces each @> line in src with the corresponding generated text.
// replacements maps 1-based source line to replacement text.
func Apply(src []byte, replacements map[int]string) []byte {
	lines := strings.Split(string(src), "\n")
	for i, line := range lines {
		lineNum := i + 1 // 1-based
		if replacement, ok := replacements[lineNum]; ok {
			// Preserve leading whitespace from the original line.
			trimmed := strings.TrimLeft(line, " \t")
			indent := line[:len(line)-len(trimmed)]
			lines[i] = indent + replacement
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
