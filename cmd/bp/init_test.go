package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/js"
	"github.com/abdul-hamid-achik/blueprint/internal/linter"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// renderForTest renders one starter exactly as `bp init <service>` would.
func renderForTest(t *testing.T, templateName, display, service string) string {
	t.Helper()
	tpl, ok := findInitTemplate(templateName)
	if !ok {
		t.Fatalf("unknown template %q", templateName)
	}
	src, err := renderInitFile(tpl.Name+".bp.tmpl", initData{Name: display, Service: service, Template: tpl})
	if err != nil {
		t.Fatalf("render %s: %v", templateName, err)
	}
	return string(src)
}

// TestInitTemplatesCompileCleanly checks every starter through the compiler
// front end: it parses, passes the checker with no errors, lints clean, and
// the Node generator produces files.
func TestInitTemplatesCompileCleanly(t *testing.T) {
	for _, tpl := range initTemplates {
		t.Run(tpl.Name, func(t *testing.T) {
			src := renderForTest(t, tpl.Name, "Sample App", "sample-app")
			file, parseErrs := parser.ParseFile("sample-app.bp", []byte(src))
			if len(parseErrs) > 0 {
				t.Fatalf("parse errors: %v", parseErrs)
			}
			if errs := checker.Check(file); len(errs) > 0 {
				for _, e := range errs {
					t.Errorf("check: %s", e.Error())
				}
			}
			if issues := linter.Lint(file); len(issues) > 0 {
				for _, issue := range issues {
					t.Errorf("lint: %s", issue.String())
				}
			}
			if _, err := js.New().Files(file); err != nil {
				t.Errorf("node codegen Files() failed: %v", err)
			}
		})
	}
}

// TestInitTemplatesFormatterStable checks that bp fmt leaves each starter
// unchanged. The worker starter is skipped: bp fmt rewrites `enqueue "q" {...}`
// into `enqueue("q", {...})`, which no longer parses (a known formatter bug).
// The default starter is also excluded: it is kept byte-identical to the
// original `bp init` output, which predates the formatter's canonical spacing.
func TestInitTemplatesFormatterStable(t *testing.T) {
	for _, tpl := range initTemplates {
		t.Run(tpl.Name, func(t *testing.T) {
			switch tpl.Name {
			case "default":
				t.Skip("default template is pinned to the original bp init output, which is not formatter-canonical")
			case "worker":
				t.Skip("worker template uses enqueue, which bp fmt cannot round-trip yet")
			}
			src := renderForTest(t, tpl.Name, "Sample App", "sample-app")
			file, parseErrs := parser.ParseFile("sample-app.bp", []byte(src))
			if len(parseErrs) > 0 {
				t.Fatalf("parse errors: %v", parseErrs)
			}
			if formatted := ast.Print(file); formatted != src {
				t.Errorf("bp fmt would change the template; formatted output:\n%s", formatted)
			}
		})
	}
}

// TestInitDefaultTemplateUnchanged pins the default starter to the exact bytes
// that `bp init` produced before templates existed.
func TestInitDefaultTemplateUnchanged(t *testing.T) {
	want := `@ "test-project — describe your service here"
blueprint "test-project" {
  version  "0.1.0"
  port     8080
  runtime  node
  database postgres
}

secret DATABASE_URL required

model item {
  id      uuid      primary
  name    string    required
  created timestamp default(now)
}

@ "List all items"
GET /api/items {
  <- page     int default(1) min(1)
  <- per_page int default(20) max(100)

  |> items = query item order(created desc) paginate(page, per_page)

  -> 200 { items: items.items, total: items.total }
}

@ "Create an item"
POST /api/items {
  <- name string required

  |> item = save item { name: name }

  -> 201 { id: item.id, name: item.name }
}
`
	if got := renderForTest(t, "default", "test-project", "test-project"); got != want {
		t.Errorf("default template changed.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSanitizeInitName(t *testing.T) {
	valid := map[string]string{
		"test-project": "test-project",
		"My Service":   "my-service",
		"api_v2.core":  "api_v2.core",
		"  padded  ":   "padded",
	}
	for in, want := range valid {
		got, err := sanitizeInitName(in)
		if err != nil || got != want {
			t.Errorf("sanitizeInitName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	invalid := []string{"", "   ", "../escape", "a/b", `a\b`, "a..b", ".", "..", ".hidden", "-leading", "café", "tab\tname", `quo"te`, strings.Repeat("a", 215)}
	for _, in := range invalid {
		if got, err := sanitizeInitName(in); err == nil {
			t.Errorf("sanitizeInitName(%q) = %q; want an error", in, got)
		}
	}
}

// --- bp init CLI ---

func TestInitListTemplates(t *testing.T) {
	stdout, stderr, code := runBP(t, "init", "--list-templates")
	if code != 0 {
		t.Fatalf("init --list-templates exited %d; stderr: %s", code, stderr)
	}
	for _, name := range []string{"default", "minimal", "crud", "auth", "worker"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("--list-templates output missing %q: %s", name, stdout)
		}
	}
}

func TestInitUnknownTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	_, stderr, code := runInitIn(t, tmpDir, "init", "svc", "--template", "crd")
	if code != 2 {
		t.Fatalf("expected exit 2 for unknown template, got %d; stderr: %s", code, stderr)
	}
	for _, want := range []string{`unknown template "crd"`, `did you mean "crud"`, "default, minimal, crud, auth, worker"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q: %s", want, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "svc")); err == nil {
		t.Error("unknown template must not create the project directory")
	}
}

func TestInitRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"../escape", "a/b", "a..b", "café", "   "} {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			_, stderr, code := runInitIn(t, tmpDir, "init", name)
			if code != 2 {
				t.Fatalf("expected exit 2, got %d; stderr: %s", code, stderr)
			}
			entries, err := os.ReadDir(tmpDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("invalid name %q left files behind: %v", name, entries)
			}
		})
	}
}

func TestInitMinimalHasNoDatabaseNote(t *testing.T) {
	tmpDir := t.TempDir()
	stdout, stderr, code := runInitIn(t, tmpDir, "init", "min-svc", "--template", "minimal")
	if code != 0 {
		t.Fatalf("init failed with code %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "DATABASE_URL") || strings.Contains(stdout, "REDIS_URL") {
		t.Errorf("minimal next steps should not mention env vars: %s", stdout)
	}
	for _, want := range []string{"bp dev min-svc.bp", "bp test min-svc.bp", "bp run min-svc.bp"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("next steps missing %q: %s", want, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %s", stderr)
	}
}

func TestInitWorkerNextStepsMentionRedis(t *testing.T) {
	tmpDir := t.TempDir()
	stdout, stderr, code := runInitIn(t, tmpDir, "init", "job-svc", "--template", "worker")
	if code != 0 {
		t.Fatalf("init failed with code %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "DATABASE_URL and REDIS_URL") {
		t.Errorf("worker next steps should name both env vars: %s", stdout)
	}
	if !strings.Contains(stdout, "Redis reachable at REDIS_URL") {
		t.Errorf("worker next steps should explain the Redis requirement for bp test: %s", stdout)
	}
}

func TestInitScaffoldsGitignoreAndReadme(t *testing.T) {
	tmpDir := t.TempDir()
	if _, stderr, code := runInitIn(t, tmpDir, "init", "scaf-svc", "--template", "crud"); code != 0 {
		t.Fatalf("init failed with code %d: %s", code, stderr)
	}
	projectDir := filepath.Join(tmpDir, "scaf-svc")
	gitignore, err := os.ReadFile(filepath.Join(projectDir, ".gitignore"))
	if err != nil {
		t.Fatalf("expected .gitignore: %v", err)
	}
	for _, entry := range []string{"node_modules/", "dist/", ".env", ".env.local"} {
		if !strings.Contains(string(gitignore), "\n"+entry+"\n") {
			t.Errorf(".gitignore missing %q: %s", entry, gitignore)
		}
	}
	// User-owned implementation scaffolds live under generated/src/impl/, so
	// the starter .gitignore must never ignore generated/ wholesale.
	for _, line := range strings.Split(string(gitignore), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "generated") {
			t.Errorf(".gitignore must not ignore generated output (user-owned files live there): %q", line)
		}
	}
	readme, err := os.ReadFile(filepath.Join(projectDir, "README.md"))
	if err != nil {
		t.Fatalf("expected README.md: %v", err)
	}
	for _, want := range []string{"# scaf-svc", "bp dev scaf-svc.bp", "bp test scaf-svc.bp", "`DATABASE_URL`"} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.md missing %q: %s", want, readme)
		}
	}
}

func TestInitDoesNotOverwriteExistingScaffoldFiles(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "keep-svc")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	const customIgnore = "dist/\n"
	const customReadme = "# my own readme\n"
	if err := os.WriteFile(filepath.Join(projectDir, ".gitignore"), []byte(customIgnore), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "README.md"), []byte(customReadme), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runInitIn(t, tmpDir, "init", "keep-svc"); code != 0 {
		t.Fatalf("init into an existing directory should succeed when the .bp is absent: %s", stderr)
	}
	gotIgnore, _ := os.ReadFile(filepath.Join(projectDir, ".gitignore"))
	gotReadme, _ := os.ReadFile(filepath.Join(projectDir, "README.md"))
	if string(gotIgnore) != customIgnore {
		t.Errorf(".gitignore was overwritten: %q", gotIgnore)
	}
	if string(gotReadme) != customReadme {
		t.Errorf("README.md was overwritten: %q", gotReadme)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "keep-svc.bp")); err != nil {
		t.Errorf("expected keep-svc.bp to be created: %v", err)
	}
}

func TestInitExistingBlueprintIsNotOverwritten(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "held-svc")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	const original = "# hand-written\n"
	bpPath := filepath.Join(projectDir, "held-svc.bp")
	if err := os.WriteFile(bpPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runInitIn(t, tmpDir, "init", "held-svc", "--template", "minimal"); code != 1 {
		t.Fatalf("expected exit 1 when the .bp already exists, got %d", code)
	}
	got, _ := os.ReadFile(bpPath)
	if string(got) != original {
		t.Errorf("existing .bp was overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".gitignore")); err == nil {
		t.Error("a refused init must not write .gitignore")
	}
}

// --- completion coverage for init templates ---

func TestCompletionScriptsIncludeInitTemplates(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			stdout, stderr, code := runBP(t, "completion", shell)
			if code != 0 {
				t.Fatalf("completion %s failed: %d\nstderr: %s", shell, code, stderr)
			}
			// fish spells flags as `-l template`, so match the bare flag names.
			wants := append([]string{"template", "list-templates"}, initTemplateNames()...)
			for _, want := range wants {
				if !strings.Contains(stdout, want) {
					t.Errorf("%s completion missing %q", shell, want)
				}
			}
		})
	}
}

// runInitIn runs the test's bp binary with args inside dir and returns its
// stdout, stderr, and exit code.
func runInitIn(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bpBinary, args...)
	cmd.Dir = dir
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	code := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("failed to run bp %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return outBuf.String(), errBuf.String(), code
}
