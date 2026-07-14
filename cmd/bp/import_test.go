package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestImportCommandPrintsScaffoldAndHonestReport(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "schema.ts"), `
export const widgets = pgTable("widgets", {
  id: uuid("id").primaryKey(),
  name: varchar("name", { length: 100 }).notNull(),
});
`)
	writeTestFile(t, filepath.Join(dir, "app.ts"), `
const CreateWidget = z.object({ name: z.string().min(1) });
app.post("/widgets", zValidator("json", CreateWidget), async (c) => {
  return c.json(await create(c.req.valid("json")), 201);
});
`)
	// A tempting route under node_modules must not leak into the scaffold.
	writeTestFile(t, filepath.Join(dir, "node_modules", "bad.ts"), `app.get("/dependency", handler)`)

	stdout, stderr, code := runBP(t, "import", "--from", "ts", "--name", "Widget Service", dir)
	if code != 0 {
		t.Fatalf("bp import failed with %d:\n%s", code, stderr)
	}
	for _, want := range []string{`blueprint "widget_service"`, "model widget {", "POST /widgets {", "-> 501"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout scaffold missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "/dependency") {
		t.Fatalf("ignored dependency route was imported:\n%s", stdout)
	}
	for _, want := range []string{
		"structural rewrite scaffold, not a behavior-preserving import",
		"Every handler body was dropped intentionally",
		"TODO    POST",
		"dropped: entire imperative handler body",
		"1 handler body/bodies requiring manual rewrite",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr fidelity report missing %q:\n%s", want, stderr)
		}
	}
	parsed, parseErrs := parser.ParseFile("imported.bp", []byte(stdout))
	if len(parseErrs) != 0 {
		t.Fatalf("stdout scaffold did not parse: %+v", parseErrs)
	}
	if errs := checker.Check(parsed); len(errs) != 0 {
		t.Fatalf("stdout scaffold did not check: %+v", errs)
	}
}

func TestImportCommandOutRefusesOverwriteUnlessForced(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "app.ts")
	out := filepath.Join(dir, "service.bp")
	writeTestFile(t, source, `app.get("/health", (c) => c.text("ok"))`)
	writeTestFile(t, out, "keep me")

	_, stderr, code := runBP(t, "import", source, "--from=typescript", "--out", out)
	if code != 1 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("expected safe overwrite rejection, code=%d stderr=%q", code, stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil || string(data) != "keep me" {
		t.Fatalf("rejected import changed output: data=%q err=%v", data, err)
	}

	stdout, stderr, code := runBP(t, "import", source, "--from", "ts", "--out", out, "--force")
	if code != 0 {
		t.Fatalf("forced import failed, code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "Created structural scaffold:") {
		t.Fatalf("missing output confirmation: %q", stdout)
	}
	data, err = os.ReadFile(out)
	if err != nil || !strings.Contains(string(data), "GET /health") || !strings.Contains(string(data), "-> 501") {
		t.Fatalf("forced import did not write scaffold: data=%q err=%v", data, err)
	}
}

func TestImportCommandRejectsMissingOrUnknownSourceKind(t *testing.T) {
	source := filepath.Join(t.TempDir(), "app.ts")
	writeTestFile(t, source, `app.get("/health", handler)`)
	for _, args := range [][]string{
		{"import", source},
		{"import", source, "--from", "python"},
	} {
		_, stderr, code := runBP(t, args...)
		if code != 1 || !strings.Contains(stderr, "currently supported: ts") {
			t.Errorf("args=%v expected source-kind rejection, code=%d stderr=%q", args, code, stderr)
		}
	}
}

func TestImportHelpIsExplicitAboutLoss(t *testing.T) {
	_, stderr, code := runBP(t, "import", "--help")
	if code != 0 {
		t.Fatalf("bp import --help failed: %d, %s", code, stderr)
	}
	for _, want := range []string{"--from <kind>", "Handler behavior is never imported", "501"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("help missing %q:\n%s", want, stderr)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
