package agentctx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestTopicsReturnsKnownNames(t *testing.T) {
	want := []string{"cli", "codegen", "errors", "examples", "language", "overview", "targets", "workflow"}
	got := Topics()
	if len(got) != len(want) {
		t.Fatalf("topic count = %d, want %d; got=%v", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("topic[%d] = %q, want %q", i, got[i], n)
		}
	}
}

func TestGetParsesEachTopic(t *testing.T) {
	for _, name := range Topics() {
		t.Run(name, func(t *testing.T) {
			topic, err := Get(name)
			if err != nil {
				t.Fatalf("Get(%q) error: %v", name, err)
			}
			if topic.Title == "" {
				t.Errorf("Title empty — every topic must start with `# Heading`")
			}
			if topic.Summary == "" {
				t.Errorf("Summary empty — every topic must have a paragraph after the title")
			}
			if len(topic.Sections) == 0 {
				t.Errorf("Sections empty — every topic must have at least one `## Heading` block")
			}
			if topic.Raw == "" {
				t.Errorf("Raw empty")
			}
		})
	}
}

func TestGetUnknownTopic(t *testing.T) {
	_, err := Get("does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown topic")
	}
	if !strings.Contains(err.Error(), "available:") {
		t.Errorf("error should list available topics, got: %v", err)
	}
}

func TestRenderTopicMarkdownIsRaw(t *testing.T) {
	topic, err := Get("overview")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := RenderTopic(topic, "md", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != topic.Raw {
		t.Errorf("md render should match Raw verbatim")
	}
}

func TestRenderTopicJSONIsStructured(t *testing.T) {
	topic, err := Get("language")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := RenderTopic(topic, "json", &buf); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Schema string `json:"$schema"`
		Topic  Topic  `json:"topic"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("emitted JSON does not parse: %v", err)
	}
	if payload.Schema != SchemaURN {
		t.Errorf("schema = %q, want %q", payload.Schema, SchemaURN)
	}
	if payload.Topic.Name != "language" {
		t.Errorf("topic name = %q", payload.Topic.Name)
	}
	if payload.Topic.Title == "" {
		t.Errorf("topic title empty in JSON")
	}
	if len(payload.Topic.Sections) == 0 {
		t.Errorf("topic sections empty in JSON")
	}
}

func TestRenderTopicUnknownFormatFallsBackToMarkdown(t *testing.T) {
	topic, err := Get("overview")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := RenderTopic(topic, "weird-format-not-real", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != topic.Raw {
		t.Errorf("unknown format should fall back to md (raw), got different output")
	}
}

func TestFullSurfaceIncludesEveryTopicAndContextCommand(t *testing.T) {
	s := FullSurface("0.10.0")
	if s.Blueprint.Version != "0.10.0" {
		t.Errorf("version = %q", s.Blueprint.Version)
	}
	if s.Schema != SchemaURN {
		t.Errorf("schema = %q", s.Schema)
	}
	if len(s.Topics) != len(Topics()) {
		t.Errorf("surface topics len = %d, want %d", len(s.Topics), len(Topics()))
	}
	var foundContext bool
	for _, c := range s.Commands {
		if c.Name == "context" {
			foundContext = true
			break
		}
	}
	if !foundContext {
		t.Errorf("commandRegistry must list bp context (this very command)")
	}
	if len(s.Targets) < 2 {
		t.Errorf("expected at least node + python targets, got %d", len(s.Targets))
	}
}

func TestRenderSurfaceMarkdown(t *testing.T) {
	s := FullSurface("0.10.0")
	var buf bytes.Buffer
	if err := RenderSurface(s, "md", &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"# Blueprint 0.10.0",
		"## Commands",
		"## Targets",
		"## Topics",
		"Run `bp context <name>`",
		"**overview**",
		"**language**",
		"**node**",
		"**python**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown surface missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderSurfaceJSON(t *testing.T) {
	s := FullSurface("0.10.0")
	var buf bytes.Buffer
	if err := RenderSurface(s, "json", &buf); err != nil {
		t.Fatal(err)
	}
	var got Surface
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("surface JSON does not parse: %v", err)
	}
	if got.Schema != SchemaURN {
		t.Errorf("schema = %q", got.Schema)
	}
	if got.Blueprint.Version != "0.10.0" {
		t.Errorf("version = %q", got.Blueprint.Version)
	}
}

func TestParseTopicExtractsExamplesFromFencedBlocks(t *testing.T) {
	raw := "# Title\n\nSummary line.\n\n## Section\n\nText.\n\n```bash\nbp context overview\n```\n"
	topic := parseTopic("test", raw)
	if topic.Title != "Title" {
		t.Errorf("Title = %q", topic.Title)
	}
	if topic.Summary != "Summary line." {
		t.Errorf("Summary = %q", topic.Summary)
	}
	if len(topic.Sections) != 1 {
		t.Fatalf("section count = %d", len(topic.Sections))
	}
	sec := topic.Sections[0]
	if sec.Title != "Section" {
		t.Errorf("section title = %q", sec.Title)
	}
	if len(sec.Examples) != 1 {
		t.Fatalf("example count = %d", len(sec.Examples))
	}
	if sec.Examples[0].Language != "bash" {
		t.Errorf("example language = %q", sec.Examples[0].Language)
	}
	if sec.Examples[0].Code != "bp context overview" {
		t.Errorf("example code = %q", sec.Examples[0].Code)
	}
}

func TestParseTopicExtractsRelatedFromSeeAlso(t *testing.T) {
	raw := "# T\n\nS.\n\n## See also\n\n- `bp context cli` — the cli\n- `bp context language` — the lang\n"
	topic := parseTopic("test", raw)
	if len(topic.Related) != 2 || topic.Related[0] != "cli" || topic.Related[1] != "language" {
		t.Errorf("Related = %v, want [cli language]", topic.Related)
	}
	if len(topic.Sections) != 0 {
		t.Errorf("See also section should not appear in Sections list, got %d sections", len(topic.Sections))
	}
}

func TestEveryTopicReferencesOnlyExistingTopics(t *testing.T) {
	known := map[string]bool{}
	for _, n := range Topics() {
		known[n] = true
	}
	for _, n := range Topics() {
		topic, err := Get(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range topic.Related {
			if !known[ref] {
				t.Errorf("topic %q references unknown topic %q in See Also", n, ref)
			}
		}
	}
}

// FullGuide (behind `bp llms`) must be self-contained — the framing preamble,
// the live CLI + target surface, and EVERY topic — and current.
func TestFullGuideIsSelfContainedAndCurrent(t *testing.T) {
	g := FullGuide("0.10.0")
	for _, want := range []string{
		"# Blueprint — agent & LLM guide (v0.10.0)",
		"## CLI quick reference",
		"## Codegen targets",
		"bp llms",        // the new command advertises itself
		"--target effect", // currency: the third target is present
	} {
		if !strings.Contains(g, want) {
			t.Errorf("FullGuide missing %q", want)
		}
	}
	for _, n := range Topics() {
		tp, err := Get(n)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(g, "# "+tp.Title) {
			t.Errorf("FullGuide missing topic heading for %q (# %s)", n, tp.Title)
		}
	}
}

func TestTargetRegistryIncludesAllThreeTargets(t *testing.T) {
	got := map[string]bool{}
	for _, tg := range FullSurface("0.10.0").Targets {
		got[tg.Name] = true
	}
	for _, want := range []string{"node", "python", "effect"} {
		if !got[want] {
			t.Errorf("target registry missing %q (got %v)", want, got)
		}
	}
}

func TestCommandRegistryListsLlms(t *testing.T) {
	var found bool
	for _, c := range commandRegistry() {
		if c.Name == "llms" {
			found = true
		}
	}
	if !found {
		t.Error("commandRegistry must list the llms command")
	}
}
