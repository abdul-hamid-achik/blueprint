// Package agentctx provides the agent-facing context surface for Blueprint —
// the implementation behind `bp context [topic]`. It embeds a curated set of
// Markdown topics that explain the language, CLI, codegen, targets, errors,
// and recommended agent workflow. Output can be raw Markdown (default) or
// structured JSON for tooling that prefers to parse.
//
// Topics are intentionally hand-written rather than auto-extracted from
// docs/*.md so the agent-facing surface stays scannable and tight; the
// VitePress site remains the authoritative reference for humans.
package agentctx

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
)

//go:embed topics/*.md
var topicsFS embed.FS

// SchemaURN is the stable identifier consumers can key off in JSON output.
const SchemaURN = "urn:blueprint.dev:context:v1"

// Topic is the parsed shape of one topic file.
type Topic struct {
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	Summary  string    `json:"summary"`
	Sections []Section `json:"sections"`
	Related  []string  `json:"relatedTopics"`
	// Raw is the verbatim Markdown source — used by the `--format md` path.
	// Excluded from JSON because Sections + Title carry the same information
	// in a structured form.
	Raw string `json:"-"`
}

// Section is one `##` block within a topic, with any fenced code blocks
// extracted into Examples for structured consumers.
type Section struct {
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	Examples []Example `json:"examples,omitempty"`
}

// Example is one fenced code block (```lang ... ```) found inside a section.
type Example struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

// Surface is the full agent-facing surface returned when no topic is given.
// Mirrors cairntrace's `cairn explain` shape so agents that handle one can
// handle both.
type Surface struct {
	Schema     string         `json:"$schema"`
	Version    string         `json:"version"`
	Blueprint  BlueprintMeta  `json:"blueprint"`
	Commands   []CommandInfo  `json:"commands"`
	Targets    []TargetInfo   `json:"targets"`
	Topics     []TopicSummary `json:"topics"`
	OutputForm []string       `json:"outputFormats"`
}

// BlueprintMeta carries the version and a one-paragraph framing.
type BlueprintMeta struct {
	Version string `json:"version"`
	Summary string `json:"summary"`
}

// CommandInfo is one line of the CLI surface.
type CommandInfo struct {
	Name     string `json:"name"`
	Synopsis string `json:"synopsis"`
	Summary  string `json:"summary"`
}

// TargetInfo describes one codegen backend.
type TargetInfo struct {
	Name    string `json:"name"`
	Stack   string `json:"stack"`
	Summary string `json:"summary"`
}

// TopicSummary is the entry shown when an agent asks for the topic list.
type TopicSummary struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// Topics returns the sorted list of available topic names.
func Topics() []string {
	entries, err := fs.ReadDir(topicsFS, "topics")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(names)
	return names
}

// Get parses and returns the named topic. Returns an error if the name does
// not match an embedded topic file.
func Get(name string) (Topic, error) {
	if name == "" {
		return Topic{}, fmt.Errorf("topic name is empty")
	}
	data, err := topicsFS.ReadFile("topics/" + name + ".md")
	if err != nil {
		return Topic{}, fmt.Errorf("unknown topic %q (available: %s)", name, strings.Join(Topics(), ", "))
	}
	return parseTopic(name, string(data)), nil
}

// FullSurface returns the synthesized "no-topic" view: version, command list,
// target list, and topic index. Built fresh on every call so it always reflects
// the current binary's command registry.
func FullSurface(version string) Surface {
	var topicSummaries []TopicSummary
	for _, n := range Topics() {
		t, err := Get(n)
		if err != nil {
			continue
		}
		topicSummaries = append(topicSummaries, TopicSummary{
			Name:    t.Name,
			Title:   t.Title,
			Summary: t.Summary,
		})
	}
	return Surface{
		Schema:     SchemaURN,
		Version:    "1",
		Blueprint:  blueprintMeta(version),
		Commands:   commandRegistry(),
		Targets:    targetRegistry(),
		Topics:     topicSummaries,
		OutputForm: []string{"md", "json"},
	}
}

// guideOrder is the reading order for the one-shot guide. Any topic not listed
// here is appended afterward (in sorted order) so new topics still surface.
var guideOrder = []string{"overview", "language", "cli", "workflow", "targets", "codegen", "examples", "errors"}

// FullGuide assembles the complete agent/LLM onboarding document behind
// `bp llms`: a framing preamble, the live CLI + target surface (always reflects
// this binary), and every topic concatenated in reading order. The result is a
// single self-contained Markdown document an agent can read once to learn the
// language, the CLI, and the recommended workflow — the `llms.txt` for `bp`.
func FullGuide(version string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Blueprint — agent & LLM guide (v%s)\n\n", version)
	b.WriteString(blueprintMeta(version).Summary)
	b.WriteString("\n\n")
	b.WriteString("Generated by `bp llms` — the one-shot onboarding for AI agents and LLMs. ")
	b.WriteString("Read top to bottom to learn the language, the CLI, and the workflow. ")
	b.WriteString("For a single topic run `bp context <topic>`; for an error code run ")
	b.WriteString("`bp explain <code>`; for a machine-readable surface run `bp context --format json`.\n\n")

	b.WriteString("## CLI quick reference\n\n")
	for _, c := range commandRegistry() {
		fmt.Fprintf(&b, "- `%s` — %s\n", c.Synopsis, c.Summary)
	}
	b.WriteString("\n## Codegen targets\n\n")
	for _, t := range targetRegistry() {
		fmt.Fprintf(&b, "- **%s** (`%s`) — %s\n", t.Name, t.Stack, t.Summary)
	}

	seen := map[string]bool{}
	emit := func(name string) {
		t, err := Get(name)
		if err != nil {
			return
		}
		b.WriteString("\n\n---\n\n")
		b.WriteString(strings.TrimRight(t.Raw, "\n"))
		b.WriteString("\n")
		seen[name] = true
	}
	for _, name := range guideOrder {
		emit(name)
	}
	for _, name := range Topics() {
		if !seen[name] {
			emit(name)
		}
	}
	return b.String()
}

// RenderTopic writes the topic in the requested format to w.
// format is "md" (default, the raw markdown) or "json" (the parsed Topic).
func RenderTopic(t Topic, format string, w io.Writer) error {
	switch normalizeFormat(format) {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Schema string `json:"$schema"`
			Topic  Topic  `json:"topic"`
		}{Schema: SchemaURN, Topic: t})
	default:
		_, err := io.WriteString(w, t.Raw)
		return err
	}
}

// RenderSurface writes the full surface in the requested format to w.
func RenderSurface(s Surface, format string, w io.Writer) error {
	switch normalizeFormat(format) {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	default:
		return renderSurfaceMarkdown(s, w)
	}
}

func normalizeFormat(f string) string {
	switch strings.ToLower(f) {
	case "json":
		return "json"
	case "md", "markdown", "":
		return "md"
	default:
		// Unrecognized formats fall back to markdown rather than erroring —
		// agents that fat-finger a flag still get useful output.
		return "md"
	}
}

func renderSurfaceMarkdown(s Surface, w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Blueprint %s\n\n", s.Blueprint.Version)
	fmt.Fprintf(&b, "%s\n\n", s.Blueprint.Summary)

	b.WriteString("## Commands\n\n")
	for _, c := range s.Commands {
		fmt.Fprintf(&b, "- **%s** — %s\n  `%s`\n", c.Name, c.Summary, c.Synopsis)
	}
	b.WriteString("\n")

	b.WriteString("## Targets\n\n")
	for _, t := range s.Targets {
		fmt.Fprintf(&b, "- **%s** (`%s`) — %s\n", t.Name, t.Stack, t.Summary)
	}
	b.WriteString("\n")

	b.WriteString("## Topics\n\n")
	b.WriteString("Run `bp context <name>` for any of:\n\n")
	for _, t := range s.Topics {
		fmt.Fprintf(&b, "- **%s** — %s\n", t.Name, t.Summary)
	}
	b.WriteString("\n")

	b.WriteString("## Output formats\n\n")
	b.WriteString("`bp context [topic] --format {md,json}` (default `md`).\n")

	_, err := io.WriteString(w, b.String())
	return err
}

func blueprintMeta(version string) BlueprintMeta {
	return BlueprintMeta{
		Version: version,
		Summary: "Blueprint is a Go-based compiler. `.bp` DSL sources describe data models, business pipelines, HTTP endpoints, middleware, and streaming handlers; `bp build` emits a runnable project — TypeScript+Hono+Drizzle (default), Python+FastAPI+SQLAlchemy (`--target python`), or an experimental Effect-TS health/config scaffold (`--target effect`).",
	}
}

// commandRegistry is the single source of truth for the agent-facing CLI
// surface. Keep in sync with cmd/bp/main.go's printUsage so agents and humans
// see the same list. New commands added in main.go should append here.
func commandRegistry() []CommandInfo {
	return []CommandInfo{
		{"check", "bp check <file.bp> [--json]", "Parse + semantic check."},
		{"build", "bp build <file.bp> [--out <dir>] [--target node|python|effect] [--gen-tests] [--gen-property-tests]", "Compile to a target project; tests are Node/Python, properties are Node-only, and unsupported target flags fail closed (idempotent)."},
		{"diff", "bp diff <file.bp> [--out <dir>] [--gen-property-tests] [--exit-code] [--apply]", "Show pending changes; --exit-code is the CI idempotency gate."},
		{"fmt", "bp fmt <file.bp> [--write] [--check]", "Format a .bp file (round-trip safe)."},
		{"lint", "bp lint <file.bp>", "Stylistic lint."},
		{"docs", "bp docs <file.bp> [--out <file.json>]", "Emit OpenAPI 3.1 from declared inputs/outputs."},
		{"run", "bp run <file.bp> [--out <dir>]", "Build + start the server."},
		{"dev", "bp dev <file.bp> [--out <dir>]", "Watch + rebuild + restart on change."},
		{"test", "bp test <file.bp> [--out <dir>] [--target node|python] [--gen-property-tests]", "Build with contract tests and run Vitest (Node, optionally fast-check) or pytest (Python)."},
		{"migrate", "bp migrate <file.bp> generate|push|check [--target node|python]", "Drizzle (node) or Alembic (python) migrations."},
		{"deploy", "bp deploy <file.bp> [--target docker|fly] [--tag <image>] [--no-run]", "Build + smoke-run Docker image; fly currently exits as 'not implemented'."},
		{"generate", "bp generate <file.bp> [--write]", "Resolve @> slots via LLM (requires ANTHROPIC_API_KEY)."},
		{"init", "bp init [name]", "Scaffold a new project."},
		{"import", "bp import [path] --from ts [--out <file.bp>]", "Recover static TypeScript structure as a TODO/501 review scaffold; handler behavior is never imported."},
		{"eject", "bp eject <dir>", "Strip Blueprint markers from a generated project."},
		{"explain", "bp explain <code>", "Print docs for a structured error code (Cxxx/Lxxx/Pxxx)."},
		{"context", "bp context [topic] [--format md|json]", "Agent-facing language + CLI surface, by topic."},
		{"llms", "bp llms [--out <file>]", "Print the complete one-shot agent/LLM guide (every topic in one document); --out writes an llms.txt."},
		{"doctor", "bp doctor", "Check toolchain dependencies."},
		{"lsp", "bp lsp", "Start stdio LSP diagnostics, hover, definition, completion, and local-workspace symbols."},
		{"stats", "bp stats <file.bp> [--json]", "Code statistics."},
		{"completion", "bp completion <bash|zsh|fish>", "Generate a shell completion script."},
		{"version", "bp version", "Print version."},
	}
}

func targetRegistry() []TargetInfo {
	return []TargetInfo{
		{"node", "Hono + Drizzle + Zod", "Default. Vitest + PGlite, opt-in deterministic fast-check properties, pure computed fields, and one-level ref-backed relationship loading."},
		{"python", "FastAPI + SQLAlchemy 2.0 + Pydantic v2 + Alembic", "`--target python`. Advanced supported subset with an exhaustive fail-closed gate. Generated contracts run via pytest + testcontainers[postgresql] (Docker)."},
		{"effect", "Effect core Config + Node HTTP (application stack planned)", "`--target effect`. Early runnable scaffold — emits GET /health plus typed secret/env Config with pinned dependencies; endpoint/model/test emit fails closed. Opt-in/experimental, not the default."},
	}
}

// parseTopic extracts Title, Summary, Sections, Examples, and Related links
// from a topic Markdown file. The parser is intentionally minimal — it
// understands what our hand-written topics use, not full CommonMark.
func parseTopic(name, raw string) Topic {
	t := Topic{Name: name, Raw: raw}

	lines := strings.Split(raw, "\n")
	i := 0

	// Title: first `# ` heading.
	for ; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "# ") {
			t.Title = strings.TrimSpace(strings.TrimPrefix(lines[i], "# "))
			i++
			break
		}
	}

	// Summary: first non-empty paragraph after the title, until the first `## `.
	var summary []string
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "## ") {
			break
		}
		if strings.TrimSpace(line) == "" {
			if len(summary) > 0 {
				break
			}
			continue
		}
		summary = append(summary, line)
	}
	t.Summary = strings.TrimSpace(strings.Join(summary, " "))

	// Sections + per-section examples.
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "## ") {
			i++
			continue
		}
		sec := Section{Title: strings.TrimSpace(strings.TrimPrefix(lines[i], "## "))}
		i++
		var body strings.Builder
		for i < len(lines) && !strings.HasPrefix(lines[i], "## ") {
			line := lines[i]
			if strings.HasPrefix(line, "```") {
				lang := strings.TrimPrefix(line, "```")
				lang = strings.TrimSpace(lang)
				i++
				var code strings.Builder
				for i < len(lines) && !strings.HasPrefix(lines[i], "```") {
					code.WriteString(lines[i])
					code.WriteString("\n")
					i++
				}
				if i < len(lines) {
					// consume the closing ```
					i++
				}
				sec.Examples = append(sec.Examples, Example{Language: lang, Code: strings.TrimRight(code.String(), "\n")})
				continue
			}
			body.WriteString(line)
			body.WriteString("\n")
			i++
		}
		sec.Body = strings.TrimSpace(body.String())
		if sec.Title == "See also" {
			t.Related = extractRelated(sec.Body)
			continue
		}
		t.Sections = append(t.Sections, sec)
	}

	return t
}

// extractRelated pulls topic names out of a "See also" body. Lines look like
// `- ` + "`bp context <name>`" — we just take the <name>.
func extractRelated(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-") {
			continue
		}
		// Find the first backtick and parse `bp context <name>`.
		const prefix = "`bp context "
		idx := strings.Index(line, prefix)
		if idx < 0 {
			continue
		}
		rest := line[idx+len(prefix):]
		end := strings.IndexAny(rest, "` ")
		if end < 0 {
			continue
		}
		name := strings.TrimSpace(rest[:end])
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}
