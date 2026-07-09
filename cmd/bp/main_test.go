package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a concurrency-safe io.Writer/String() pair, for tests that
// poll a long-running subprocess's output while it's still writing to it
// (plain strings.Builder/bytes.Buffer aren't safe for that under -race).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

var bpBinary string

func TestMain(m *testing.M) {
	// Build the binary once for all tests
	tmp, err := os.MkdirTemp("", "bp-test-*")
	if err != nil {
		panic(err)
	}
	bpBinary = filepath.Join(tmp, "bp")
	cmd := exec.Command("go", "build", "-o", bpBinary, ".")
	cmd.Dir = filepath.Join(getProjectRoot(), "cmd", "bp")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

func getProjectRoot() string {
	// Walk up from cmd/bp to find go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// runBP executes the bp binary with args and returns stdout, stderr, and exit code.
func runBP(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	return runBPEnv(t, nil, args...)
}

func runBPEnv(t *testing.T, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bpBinary, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run bp: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func TestExtractDoctorVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "redis-cli", in: "redis-cli 8.8.0", want: "8.8.0"},
		{name: "docker trailing comma", in: "Docker version 29.5.2, build 79eb04c", want: "29.5.2"},
		{name: "node v prefix", in: "v22.22.0", want: "22.22.0"},
		{name: "go version line", in: "go version go1.26.3 darwin/arm64", want: "1.26.3"},
		{name: "tsc Version prefix", in: "Version 5.9.3", want: "5.9.3"},
		{name: "drizzle-kit two-component", in: "drizzle-kit: v0.31.10\nNo config path provided", want: "0.31.10"},
		{name: "psql full line", in: "psql (PostgreSQL) 16.4", want: "16.4"},
		{name: "alembic", in: "alembic 1.18.4", want: "1.18.4"},
		{name: "pytest", in: "pytest 8.3.3", want: "8.3.3"},
		{name: "no version falls back", in: "no version info", want: "no version info"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDoctorVersion(tc.in)
			if got != tc.want {
				t.Errorf("extractDoctorVersion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDoctorCommand(t *testing.T) {
	// Smoke test: doctor should run and print the expected banner. We don't
	// pin exact dependency versions because they vary per machine, but we do
	// guarantee none of the historic version-parsing bugs sneak back in.
	stdout, _, _ := runBP(t, "doctor")
	if !strings.Contains(stdout, "Blueprint Environment Check") {
		t.Errorf("expected doctor banner, got %q", stdout)
	}
	for _, name := range []string{"Go", "Node.js", "Bun", "tsc", "drizzle-kit", "Python", "uv", "alembic", "pytest", "Docker", "Redis", "Git"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("expected doctor output to mention %q, got %q", name, stdout)
		}
	}
	// Guard against the old "redis-cli" / "29.5.2," version-parsing bugs.
	if strings.Contains(stdout, "(redis-cli)") {
		t.Errorf("redis version regressed to literal 'redis-cli': %s", stdout)
	}
	for _, bad := range []string{".,)", ",)"} {
		if strings.Contains(stdout, bad) {
			t.Errorf("version output contains trailing punctuation %q: %s", bad, stdout)
		}
	}
}

func TestVersion(t *testing.T) {
	stdout, _, exitCode := runBP(t, "version")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "bp version") {
		t.Errorf("expected output to contain 'bp version', got %q", stdout)
	}
}

func TestVersionDashV(t *testing.T) {
	stdout, _, exitCode := runBP(t, "-v")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "bp version") {
		t.Errorf("expected output to contain 'bp version', got %q", stdout)
	}
}

func TestHelp(t *testing.T) {
	stdout, _, exitCode := runBP(t, "help")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Commands:") {
		t.Errorf("expected output to contain 'Commands:', got %q", stdout)
	}
}

func TestHelpDashH(t *testing.T) {
	stdout, _, exitCode := runBP(t, "--help")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Commands:") {
		t.Errorf("expected output to contain 'Commands:', got %q", stdout)
	}
}

func TestNoArgs(t *testing.T) {
	_, _, exitCode := runBP(t)
	if exitCode != 1 {
		t.Errorf("expected exit code 1 (no args), got %d", exitCode)
	}
}

func TestUnknownCommand(t *testing.T) {
	_, stderr, exitCode := runBP(t, "nonexistent")
	if exitCode != 1 {
		t.Errorf("expected exit code 1 for unknown command, got %d", exitCode)
	}
	if !strings.Contains(stderr, "Unknown command") {
		t.Errorf("expected stderr to contain 'Unknown command', got %q", stderr)
	}
}

func TestCheckValidFile(t *testing.T) {
	root := getProjectRoot()
	validFile := filepath.Join(root, "examples", "hello-world.bp")
	stdout, _, exitCode := runBP(t, "check", validFile)
	if exitCode != 0 {
		t.Errorf("expected exit code 0 for valid file, got %d", exitCode)
	}
	if !strings.Contains(stdout, "OK:") {
		t.Errorf("expected output to contain 'OK:', got %q", stdout)
	}
}

func TestCheckAllExamples(t *testing.T) {
	root := getProjectRoot()
	examples := []string{
		"hello-world.bp",
		"todo-api.bp",
		"auth-service.bp",
		"ecommerce-api.bp",
		"realtime-chat.bp",
	}
	for _, ex := range examples {
		t.Run(ex, func(t *testing.T) {
			exFile := filepath.Join(root, "examples", ex)
			stdout, stderr, exitCode := runBP(t, "check", exFile)
			if exitCode != 0 {
				t.Errorf("expected exit code 0 for %s, got %d\nstdout: %s\nstderr: %s", ex, exitCode, stdout, stderr)
			}
			if !strings.Contains(stdout, "OK:") {
				t.Errorf("expected 'OK:' for %s, got %q", ex, stdout)
			}
		})
	}
}

func TestCheckInvalidFile(t *testing.T) {
	root := getProjectRoot()
	invalidFile := filepath.Join(root, "testdata", "invalid", "missing_closing_brace.bp")
	_, stderr, exitCode := runBP(t, "check", invalidFile)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit code for invalid file, got 0")
	}
	if !strings.Contains(strings.ToLower(stderr), "error") {
		t.Errorf("expected stderr to contain 'error', got %q", stderr)
	}
}

func TestCheckNonexistentFile(t *testing.T) {
	_, stderr, exitCode := runBP(t, "check", "/nonexistent/file.bp")
	if exitCode == 0 {
		t.Errorf("expected non-zero exit code for missing file, got 0")
	}
	if !strings.Contains(strings.ToLower(stderr), "error") {
		t.Errorf("expected stderr to contain 'error', got %q", stderr)
	}
}

func TestBuildValidFile(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	validFile := filepath.Join(root, "examples", "hello-world.bp")
	stdout, _, exitCode := runBP(t, "build", validFile, "--out", outDir)
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Built") {
		t.Errorf("expected output to contain 'Built', got %q", stdout)
	}

	// Verify key files were generated
	expectedFiles := []string{
		"package.json",
		"src/index.ts",
	}
	for _, f := range expectedFiles {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected generated file %s to exist", f)
		}
	}
}

func TestBuildWithReactQuery(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	validFile := filepath.Join(root, "examples", "todo-api.bp")
	stdout, stderr, exitCode := runBP(t, "build", validFile, "--out", outDir, "--react-query")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	for _, f := range []string{
		"src/types/react-query.ts",
		"frontend/package.json",
		"frontend/src/react-query.ts",
	} {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected generated file %s to exist", f)
		}
	}

	pkgBytes, err := os.ReadFile(filepath.Join(outDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pkgBytes), `"@tanstack/react-query"`) {
		t.Error("root package.json should include react-query dependency when flag is enabled")
	}
}

func TestBuildFrontendOnly(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	validFile := filepath.Join(root, "examples", "todo-api.bp")
	stdout, stderr, exitCode := runBP(t, "build", validFile, "--out", outDir, "--frontend-only")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	for _, f := range []string{"package.json", "tsconfig.json", ".gitignore", "src/index.ts", "src/api.ts", "src/client.ts", "src/schemas.ts"} {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected frontend-only file %s to exist", f)
		}
	}
	for _, f := range []string{"Dockerfile", ".env.example", "frontend/package.json", "src/routes/todos.ts"} {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("did not expect backend artifact %s in frontend-only output", f)
		}
	}

	pkgBytes, err := os.ReadFile(filepath.Join(outDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pkgBytes), `"name": "todo-api-frontend"`) {
		t.Error("frontend-only output should use frontend package metadata at the root")
	}
}

func TestFrontendCommand(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	validFile := filepath.Join(root, "examples", "todo-api.bp")
	stdout, stderr, exitCode := runBP(t, "frontend", validFile, "--out", outDir, "--react-query")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	for _, f := range []string{"package.json", "README.md", ".gitignore", "src/react-query.ts"} {
		path := filepath.Join(outDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected frontend command to generate %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "frontend", "package.json")); err == nil {
		t.Error("frontend command should not nest output under frontend/")
	}
}

func TestFrontendPublishCommand(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	validFile := filepath.Join(root, "examples", "todo-api.bp")
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "bun.log")
	bunStub := filepath.Join(binDir, "bun")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_BUN_LOG\"\n" +
		"if [ \"$1\" = \"install\" ]; then mkdir -p node_modules; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bunStub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_BUN_LOG=" + logFile,
	}
	stdout, stderr, exitCode := runBPEnv(t, env, "frontend", "publish", validFile, "--out", outDir, "--react-query")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)
	if !strings.Contains(logStr, "install") {
		t.Error("frontend publish should run bun install")
	}
	if !strings.Contains(logStr, "run build") {
		t.Error("frontend publish should run bun run build")
	}
	if !strings.Contains(logStr, "pm pack --dry-run") {
		t.Error("frontend publish should run bun pm pack --dry-run")
	}
	if !strings.Contains(stdout, "ready for publish review") {
		t.Error("frontend publish should report successful dry-run completion")
	}
}

func TestFrontendPublishCommandSkipInstall(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	validFile := filepath.Join(root, "examples", "todo-api.bp")
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "bun.log")
	bunStub := filepath.Join(binDir, "bun")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_BUN_LOG\"\n" +
		"exit 0\n"
	if err := os.WriteFile(bunStub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_BUN_LOG=" + logFile,
	}
	stdout, stderr, exitCode := runBPEnv(t, env, "frontend", "publish", validFile, "--out", outDir)
	if exitCode != 0 {
		t.Fatalf("initial publish expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if err := os.MkdirAll(filepath.Join(outDir, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logFile, nil, 0644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, exitCode = runBPEnv(t, env, "frontend", "publish", validFile, "--out", outDir, "--skip-install")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)
	if strings.Contains(logStr, "install") {
		t.Error("frontend publish --skip-install should not run bun install")
	}
	if !strings.Contains(logStr, "run build") {
		t.Error("frontend publish --skip-install should still run bun run build")
	}
	if !strings.Contains(logStr, "pm pack --dry-run") {
		t.Error("frontend publish --skip-install should still run bun pm pack --dry-run")
	}
	if !strings.Contains(stdout, "Skipped bun install") {
		t.Error("frontend publish --skip-install should report skipped install")
	}
}

func TestDiffWithReactQuery(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	validFile := filepath.Join(root, "examples", "todo-api.bp")

	stdout, stderr, exitCode := runBP(t, "build", validFile, "--out", outDir)
	if exitCode != 0 {
		t.Fatalf("initial build failed: exit %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	stdout, stderr, exitCode = runBP(t, "diff", validFile, "--out", outDir, "--react-query")
	if exitCode != 0 {
		t.Fatalf("diff failed: exit %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "src/types/react-query.ts") {
		t.Errorf("expected diff output to mention react-query.ts, got %q", stdout)
	}
	if !strings.Contains(stdout, "frontend/package.json") {
		t.Errorf("expected diff output to mention frontend package changes, got %q", stdout)
	}
}

func TestDiffFrontendOnly(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	validFile := filepath.Join(root, "examples", "todo-api.bp")

	stdout, stderr, exitCode := runBP(t, "build", validFile, "--out", outDir, "--frontend-only")
	if exitCode != 0 {
		t.Fatalf("frontend-only build failed: exit %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	stdout, stderr, exitCode = runBP(t, "diff", validFile, "--out", outDir, "--frontend-only")
	if exitCode != 0 {
		t.Fatalf("frontend-only diff failed: exit %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "No changes") {
		t.Errorf("expected no changes for matching frontend-only output, got %q", stdout)
	}
}

func TestBuildAllExamples(t *testing.T) {
	root := getProjectRoot()
	examples := []string{
		"hello-world.bp",
		"todo-api.bp",
		"auth-service.bp",
		"ecommerce-api.bp",
		"realtime-chat.bp",
	}
	for _, ex := range examples {
		t.Run(ex, func(t *testing.T) {
			outDir := t.TempDir()
			exFile := filepath.Join(root, "examples", ex)
			stdout, stderr, exitCode := runBP(t, "build", exFile, "--out", outDir)
			if exitCode != 0 {
				t.Errorf("expected exit code 0 for %s, got %d\nstdout: %s\nstderr: %s", ex, exitCode, stdout, stderr)
			}
			// Verify at least package.json and src/index.ts exist
			for _, f := range []string{"package.json", "src/index.ts"} {
				path := filepath.Join(outDir, f)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					t.Errorf("expected %s to be generated for %s", f, ex)
				}
			}
		})
	}
}

func TestBuildInvalidFile(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	invalidFile := filepath.Join(root, "testdata", "invalid", "missing_closing_brace.bp")
	_, _, exitCode := runBP(t, "build", invalidFile, "--out", outDir)
	if exitCode == 0 {
		t.Errorf("expected non-zero exit code for invalid file build")
	}
}

func TestFmtValidFile(t *testing.T) {
	root := getProjectRoot()
	validFile := filepath.Join(root, "examples", "hello-world.bp")
	stdout, _, exitCode := runBP(t, "fmt", validFile)
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	// Should output formatted code
	if len(stdout) == 0 {
		t.Error("expected non-empty formatted output")
	}
	// Should contain blueprint keyword
	if !strings.Contains(stdout, "blueprint") {
		t.Errorf("expected formatted output to contain 'blueprint', got %q", stdout)
	}
}

func TestFmtCheckAlreadyFormatted(t *testing.T) {
	// Create a temp file with already-formatted content
	root := getProjectRoot()
	validFile := filepath.Join(root, "examples", "hello-world.bp")

	// First, get the formatted output
	formatted, _, _ := runBP(t, "fmt", validFile)

	// Write to a temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "formatted.bp")
	if err := os.WriteFile(tmpFile, []byte(formatted), 0644); err != nil {
		t.Fatal(err)
	}

	// Now check it — should exit 0 if already formatted
	_, _, exitCode := runBP(t, "fmt", tmpFile, "--check")
	if exitCode != 0 {
		t.Errorf("expected exit code 0 for already-formatted file, got %d", exitCode)
	}
}

func TestLintValidFile(t *testing.T) {
	root := getProjectRoot()
	validFile := filepath.Join(root, "examples", "hello-world.bp")
	stdout, _, exitCode := runBP(t, "lint", validFile)
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "OK:") {
		t.Errorf("expected 'OK:' in lint output, got %q", stdout)
	}
}

func TestDocsValidFile(t *testing.T) {
	root := getProjectRoot()
	validFile := filepath.Join(root, "examples", "hello-world.bp")
	stdout, _, exitCode := runBP(t, "docs", validFile)
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	// Output should be valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Errorf("expected valid JSON output, got parse error: %v\noutput: %s", err, stdout)
	}
	// Should contain OpenAPI version
	if _, ok := parsed["openapi"]; !ok {
		t.Error("expected JSON to contain 'openapi' key")
	}
}

func TestDocsToFile(t *testing.T) {
	root := getProjectRoot()
	validFile := filepath.Join(root, "examples", "hello-world.bp")
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "openapi.json")
	stdout, _, exitCode := runBP(t, "docs", validFile, "--out", outFile)
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Docs written to") {
		t.Errorf("expected 'Docs written to' in output, got %q", stdout)
	}
	// Verify file was created and is valid JSON
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Errorf("expected valid JSON in output file, got parse error: %v", err)
	}
}

func TestInit(t *testing.T) {
	tmpDir := t.TempDir()
	// Run init inside the temp dir
	cmd := exec.Command(bpBinary, "init", "test-project")
	cmd.Dir = tmpDir
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run bp init: %v", err)
		}
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d\nstderr: %s", exitCode, errBuf.String())
	}

	stdout := outBuf.String()
	if !strings.Contains(stdout, "Created") {
		t.Errorf("expected output to contain 'Created', got %q", stdout)
	}

	// Verify directory and .bp file were created
	projectDir := filepath.Join(tmpDir, "test-project")
	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		t.Error("expected test-project directory to be created")
	}
	bpFile := filepath.Join(projectDir, "test-project.bp")
	if _, err := os.Stat(bpFile); os.IsNotExist(err) {
		t.Error("expected test-project.bp file to be created")
	}

	// The generated file should be parseable
	_, _, checkCode := runBP(t, "check", bpFile)
	if checkCode != 0 {
		t.Errorf("expected generated .bp file to pass check, got exit code %d", checkCode)
	}
}

func TestInitDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	// First init
	cmd1 := exec.Command(bpBinary, "init", "dup-test")
	cmd1.Dir = tmpDir
	if err := cmd1.Run(); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Second init — should fail because file already exists
	cmd2 := exec.Command(bpBinary, "init", "dup-test")
	cmd2.Dir = tmpDir
	var errBuf strings.Builder
	cmd2.Stderr = &errBuf
	err := cmd2.Run()
	if err == nil {
		t.Error("expected second init to fail (file already exists)")
	}
}

func TestCheckCommandHelp(t *testing.T) {
	_, stderr, exitCode := runBP(t, "check", "--help")
	if exitCode != 0 {
		t.Errorf("expected exit code 0 for check --help, got %d", exitCode)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("expected stderr to contain 'Usage:', got %q", stderr)
	}
}

func TestBuildCommandHelp(t *testing.T) {
	_, stderr, exitCode := runBP(t, "build", "--help")
	if exitCode != 0 {
		t.Errorf("expected exit code 0 for build --help, got %d", exitCode)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("expected stderr to contain 'Usage:', got %q", stderr)
	}
}

func TestCheckValidTestdata(t *testing.T) {
	root := getProjectRoot()
	// Test a few valid testdata files
	validFiles := []string{
		"minimal.bp",
		"simple_endpoint.bp",
		"single_model.bp",
		"single_secret.bp",
	}
	for _, f := range validFiles {
		t.Run(f, func(t *testing.T) {
			path := filepath.Join(root, "testdata", "valid", f)
			stdout, stderr, exitCode := runBP(t, "check", path)
			if exitCode != 0 {
				t.Errorf("expected exit code 0 for %s, got %d\nstdout: %s\nstderr: %s", f, exitCode, stdout, stderr)
			}
		})
	}
}

// makeTodoSource returns a minimal valid .bp source for diff/build tests.
func makeTodoSource(extra string) string {
	return `@ "diff test"
blueprint "diff-test" {
  version "1.0.0"
  port 3000
  runtime node
  database postgres
}
secret DATABASE_URL required
model todo {
  id    uuid   primary
  title string required
}
@ "Create"
POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id }
}
` + extra
}

func TestDiffNoChanges(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "todo.bp")
	if err := os.WriteFile(src, []byte(makeTodoSource("")), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if _, _, code := runBP(t, "build", src, "--out", out); code != 0 {
		t.Fatalf("build failed")
	}
	stdout, _, code := runBP(t, "diff", src, "--out", out, "--no-color", "--exit-code")
	if code != 0 {
		t.Errorf("expected exit 0 when no changes, got %d. stdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "No changes") {
		t.Errorf("expected 'No changes' in output, got: %s", stdout)
	}
}

func TestDiffUnifiedAndExitCode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "todo.bp")
	if err := os.WriteFile(src, []byte(makeTodoSource("")), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if _, _, code := runBP(t, "build", src, "--out", out); code != 0 {
		t.Fatalf("build failed")
	}

	// Add a new endpoint — guaranteed to change codegen output.
	extra := `
@ "Count"
GET /api/todos/count {
  |> todos = query todo
  -> 200 { count: todos.count }
}
`
	if err := os.WriteFile(src, []byte(makeTodoSource(extra)), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runBP(t, "diff", src, "--out", out, "--no-color", "--exit-code")
	if code != 1 {
		t.Errorf("expected exit 1 when there is a diff, got %d", code)
	}
	for _, want := range []string{"modified:", "---", "+++", "@@"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected %q in unified diff output, got:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, ".blueprint/manifest.json") {
		t.Errorf("manifest.json should be suppressed from diff output, got:\n%s", stdout)
	}
}

func TestDiffIgnoresUntrackedFilesInOutDir(t *testing.T) {
	// CI pipelines often run `npm install` (node) or `uv sync` (python) in
	// the output directory between bp invocations. Those create files
	// (node_modules/, .venv/, ...) that bp never produced. `bp diff` must
	// not flag them as "deleted" against the next build — the manifest is
	// the source of truth for which files bp owns.
	dir := t.TempDir()
	src := filepath.Join(dir, "todo.bp")
	if err := os.WriteFile(src, []byte(makeTodoSource("")), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if _, _, code := runBP(t, "build", src, "--out", out); code != 0 {
		t.Fatalf("build failed")
	}
	// Plant the kind of files `npm install` would create.
	if err := os.MkdirAll(filepath.Join(out, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "node_modules", ".bin", "tsc"), []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runBP(t, "diff", src, "--out", out, "--no-color", "--exit-code")
	if code != 0 {
		t.Errorf("expected exit 0 despite planted node_modules, got %d (stdout=%q)", code, stdout)
	}
	if strings.Contains(stdout, "node_modules") {
		t.Errorf("node_modules should not show up in diff output, got: %s", stdout)
	}
}

func TestDiffApplyWritesChanges(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "todo.bp")
	if err := os.WriteFile(src, []byte(makeTodoSource("")), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if _, _, code := runBP(t, "build", src, "--out", out); code != 0 {
		t.Fatalf("build failed")
	}
	extra := `
@ "Count"
GET /api/todos/count {
  |> todos = query todo
  -> 200 { count: todos.count }
}
`
	if err := os.WriteFile(src, []byte(makeTodoSource(extra)), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runBP(t, "diff", src, "--out", out, "--no-color", "--apply")
	if code != 0 {
		t.Errorf("expected exit 0 from --apply, got %d. stdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "Applying changes") {
		t.Errorf("expected 'Applying changes' message, got:\n%s", stdout)
	}

	// After apply, a fresh diff should report no changes.
	stdout2, _, code2 := runBP(t, "diff", src, "--out", out, "--no-color", "--exit-code")
	if code2 != 0 {
		t.Errorf("expected exit 0 after apply, got %d. stdout=%s", code2, stdout2)
	}
	if !strings.Contains(stdout2, "No changes") {
		t.Errorf("expected 'No changes' after apply, got:\n%s", stdout2)
	}
}

func TestBuildPythonHelloWorld(t *testing.T) {
	root := getProjectRoot()
	outDir := t.TempDir()
	stdout, stderr, code := runBP(t, "build",
		filepath.Join(root, "examples", "hello-world.bp"),
		"--out", outDir, "--target", "python")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "target=python") {
		t.Errorf("expected stdout to mention python target, got: %q", stdout)
	}
	for _, rel := range []string{"pyproject.toml", "src/app.py", "src/routes/hello.py"} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Errorf("expected %s to exist after build: %v", rel, err)
		}
	}
}

func TestBuildPythonRejectsUnsupportedSpec(t *testing.T) {
	// All 5 shipped examples now compile on --target python, so the test
	// uses an inline synthetic spec with constructs still rejected (Phase 5b):
	// `pipe` declarations, `worker`, `storage`. The roadmap error message must
	// surface cleanly so users get pointed at BACKLOG.md, not half-broken code.
	dir := t.TempDir()
	src := filepath.Join(dir, "unsupported.bp")
	body := `blueprint "x" { version "1.0" port 3000 runtime node storage s3 }
pipe validate { <- v string  -> v }
worker process { trigger "job"  |> log "doing work" }
GET /api/x { -> 200 "ok" }
`
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	_, stderr, code := runBP(t, "build", src, "--out", outDir, "--target", "python")
	if code == 0 {
		t.Fatalf("expected non-zero exit on unsupported spec")
	}
	for _, want := range []string{
		"python target does not yet support",
		"BACKLOG.md",
		"--target node",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("expected stderr to contain %q, got: %q", want, stderr)
		}
	}
}

func TestBuildRejectsUnknownTarget(t *testing.T) {
	root := getProjectRoot()
	_, stderr, code := runBP(t, "build",
		filepath.Join(root, "examples", "hello-world.bp"),
		"--out", t.TempDir(), "--target", "ruby")
	if code == 0 {
		t.Fatalf("expected non-zero exit on unknown target")
	}
	if !strings.Contains(stderr, "unknown --target") {
		t.Errorf("expected 'unknown --target' error, got: %q", stderr)
	}
}

func TestExplainKnownCode(t *testing.T) {
	stdout, _, code := runBP(t, "explain", "C001")
	if code != 0 {
		t.Errorf("bp explain C001 should exit 0, got %d (stdout=%q)", code, stdout)
	}
	if !strings.Contains(stdout, "### C001") {
		t.Errorf("expected heading in explain output, got: %q", stdout)
	}
	if !strings.Contains(stdout, "missing blueprint block") {
		t.Errorf("expected title in explain output, got: %q", stdout)
	}
}

func TestExplainCaseInsensitive(t *testing.T) {
	if _, _, code := runBP(t, "explain", "c004"); code != 0 {
		t.Errorf("lowercase code should hit, exit was %d", code)
	}
}

func TestExplainUnknownCodeExitsOne(t *testing.T) {
	_, stderr, code := runBP(t, "explain", "C999")
	if code != 1 {
		t.Errorf("unknown code should exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "no documentation for code") {
		t.Errorf("expected helpful stderr, got: %q", stderr)
	}
}

func TestExplainMissingArg(t *testing.T) {
	_, stderr, code := runBP(t, "explain")
	if code == 0 {
		t.Errorf("explain without code should exit non-zero")
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("expected usage hint, got: %q", stderr)
	}
}

func TestCheckInvalidTestdata(t *testing.T) {
	root := getProjectRoot()
	// Test a few invalid testdata files
	invalidFiles := []string{
		"missing_closing_brace.bp",
		"unexpected_token.bp",
		"empty_file.bp",
	}
	for _, f := range invalidFiles {
		t.Run(f, func(t *testing.T) {
			path := filepath.Join(root, "testdata", "invalid", f)
			_, _, exitCode := runBP(t, "check", path)
			if exitCode == 0 {
				t.Errorf("expected non-zero exit code for invalid file %s", f)
			}
		})
	}
}

// TestDeployTargetFlyRejected pins the Pillar 5 contract: `bp deploy --target fly`
// must exit non-zero and point at the production-readiness doc, not silently
// fall back to docker.
func TestDeployTargetFlyRejected(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	stdout, stderr, code := runBP(t, "deploy", bp, "--out", outDir, "--target", "fly")
	if code == 0 {
		t.Fatalf("expected non-zero exit for --target fly, got 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "not yet implemented") {
		t.Errorf("expected 'not yet implemented' in stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, "production-readiness.md") {
		t.Errorf("expected pointer to production-readiness.md, got: %q", stderr)
	}
}

// TestDeployTargetUnknown ensures arbitrary --target values get a clean error
// rather than crashing or silently doing the wrong thing.
func TestDeployTargetUnknown(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	_, stderr, code := runBP(t, "deploy", bp, "--out", outDir, "--target", "kubernetes")
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown --target")
	}
	if !strings.Contains(stderr, "unknown --target") {
		t.Errorf("expected 'unknown --target' in stderr, got: %q", stderr)
	}
}

// TestDeployDockerNoRun stubs the `docker` binary on PATH and verifies that the
// --no-run flag skips the smoke-test `docker run` step. We can't actually run a
// container in unit tests, so --no-run is the only path we can exercise end to
// end here. The stub logs every invocation so we can assert what bp called.
func TestDeployDockerNoRun(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "docker.log")
	dockerStub := filepath.Join(binDir, "docker")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_DOCKER_LOG\"\n" +
		"exit 0\n"
	if err := os.WriteFile(dockerStub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_DOCKER_LOG=" + logFile,
	}
	stdout, stderr, code := runBPEnv(t, env, "deploy", bp, "--out", outDir, "--no-run", "--tag", "bp-test:latest")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)
	if !strings.Contains(logStr, "build -t bp-test:latest") {
		t.Errorf("expected docker build to be invoked, log: %q", logStr)
	}
	if strings.Contains(logStr, "run -d") {
		t.Errorf("--no-run should skip docker run, but log contained it: %q", logStr)
	}
}

// TestMigratePythonRunsAlembic stubs `uv` on PATH and verifies the migrate
// command builds the python tree (pyproject.toml exists) and invokes
// `uv run alembic ...` with the right subcommand. This is the bun-stub-on-PATH
// pattern from TestFrontendPublishCommand, adapted for uv.
func TestMigratePythonRunsAlembic(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := t.TempDir()
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "uv.log")
	uvStub := filepath.Join(binDir, "uv")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_UV_LOG\"\n" +
		"exit 0\n"
	if err := os.WriteFile(uvStub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_UV_LOG=" + logFile,
	}
	stdout, stderr, code := runBPEnv(t, env, "migrate", bp, "generate", "--out", outDir, "--target", "python")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	// Confirm we built the python tree, not the node one.
	if _, err := os.Stat(filepath.Join(outDir, "pyproject.toml")); err != nil {
		t.Errorf("expected pyproject.toml in python migrate output, got err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "package.json")); err == nil {
		t.Error("python migrate should not have produced a package.json")
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)
	if !strings.Contains(logStr, "run alembic revision --autogenerate") {
		t.Errorf("expected alembic revision --autogenerate, log: %q", logStr)
	}
}

// TestMigratePythonPush ensures the push subcommand maps to alembic upgrade head.
func TestMigratePythonPush(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := t.TempDir()
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "uv.log")
	uvStub := filepath.Join(binDir, "uv")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_UV_LOG\"\n" +
		"exit 0\n"
	if err := os.WriteFile(uvStub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_UV_LOG=" + logFile,
	}
	stdout, stderr, code := runBPEnv(t, env, "migrate", bp, "push", "--out", outDir, "--target", "python")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "run alembic upgrade head") {
		t.Errorf("expected alembic upgrade head, log: %q", string(logBytes))
	}
}

// TestMigratePythonStudioRejected covers the "studio has no python equivalent"
// branch — alembic is CLI-only, so we error rather than silently doing nothing.
func TestMigratePythonStudioRejected(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := t.TempDir()
	binDir := t.TempDir()
	// Even with uv on PATH, studio should error before invoking it.
	uvStub := filepath.Join(binDir, "uv")
	if err := os.WriteFile(uvStub, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}
	_, stderr, code := runBPEnv(t, env, "migrate", bp, "studio", "--out", outDir, "--target", "python")
	if code == 0 {
		t.Fatal("expected non-zero exit for studio + python")
	}
	if !strings.Contains(stderr, "not supported with --target python") {
		t.Errorf("expected studio rejection message, got: %q", stderr)
	}
}

// TestMigrateUnknownTargetRejected confirms the --target validation surfaces a
// clean error rather than a stack trace.
func TestMigrateUnknownTargetRejected(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := t.TempDir()
	_, stderr, code := runBP(t, "migrate", bp, "generate", "--out", outDir, "--target", "ruby")
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown migrate target")
	}
	if !strings.Contains(stderr, "unknown --target") {
		t.Errorf("expected 'unknown --target' message, got: %q", stderr)
	}
}

// TestDeployHelpMentionsTarget protects against drift between the actual flag
// parser and the `bp deploy --help` text — both must mention --target.
// printCommandHelp writes to stderr, so we read it from there.
func TestDeployHelpMentionsTarget(t *testing.T) {
	_, stderr, code := runBP(t, "deploy", "--help")
	if code != 0 {
		t.Fatalf("expected exit 0 for help, got %d", code)
	}
	if !strings.Contains(stderr, "--target") {
		t.Errorf("deploy help should mention --target, got: %q", stderr)
	}
	if !strings.Contains(stderr, "--no-run") {
		t.Errorf("deploy help should mention --no-run, got: %q", stderr)
	}
}

// TestMigrateHelpMentionsTarget mirrors TestDeployHelpMentionsTarget for migrate.
func TestMigrateHelpMentionsTarget(t *testing.T) {
	_, stderr, code := runBP(t, "migrate", "--help")
	if code != 0 {
		t.Fatalf("expected exit 0 for help, got %d", code)
	}
	if !strings.Contains(stderr, "--target") {
		t.Errorf("migrate help should mention --target, got: %q", stderr)
	}
	if !strings.Contains(stderr, "alembic") {
		t.Errorf("migrate help should mention alembic, got: %q", stderr)
	}
}

// --- flag parsing: equals-form, typos, missing values ---
//
// These pin the three empirically-observed bugs: `--target=python` silently
// built node (equals form ignored), `--gentests` (typo) exited 0 (unknown
// flags silently dropped), and `--out` with no following value exited 0
// (missing value silently kept the default).

func TestBuildEqualsFormTarget(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	stdout, stderr, code := runBP(t, "build", bp, "--out="+outDir, "--target=python")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "target=python") {
		t.Errorf("expected python target in output, got: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(outDir, "pyproject.toml")); err != nil {
		t.Errorf("expected pyproject.toml to exist (equals-form --target must be honored): %v", err)
	}
}

func TestBuildUnknownFlagTypoSuggestion(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	_, stderr, code := runBP(t, "build", bp, "--out", t.TempDir(), "--gentests")
	if code != 1 {
		t.Errorf("expected exit 1 for typo flag, got %d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("expected 'unknown flag' in stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, "--gen-tests") {
		t.Errorf("expected a 'did you mean --gen-tests' hint, got: %q", stderr)
	}
}

func TestBuildMissingFlagValue(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	_, stderr, code := runBP(t, "build", bp, "--out")
	if code != 1 {
		t.Errorf("expected exit 1 for --out with no value, got %d", code)
	}
	if !strings.Contains(stderr, "needs a value") {
		t.Errorf("expected 'needs a value' message, got: %q", stderr)
	}
}

func TestBuildMissingTargetValueBeforeAnotherFlag(t *testing.T) {
	// `--target` immediately followed by another recognized flag must not
	// silently swallow that flag's name as the target value.
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	_, stderr, code := runBP(t, "build", bp, "--out", t.TempDir(), "--target", "--gen-tests")
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "needs a value") {
		t.Errorf("expected 'needs a value' message, got: %q", stderr)
	}
}

func TestCheckUnknownFlagRejected(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	_, stderr, code := runBP(t, "check", bp, "--jsonn")
	if code != 1 {
		t.Errorf("expected exit 1 for unknown flag, got %d", code)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("expected 'unknown flag' message, got: %q", stderr)
	}
}

func TestMigrateUnknownSubcommandRejected(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	_, stderr, code := runBP(t, "migrate", bp, "genereate", "--out", t.TempDir())
	if code != 1 {
		t.Errorf("expected exit 1 for unknown migrate subcommand, got %d", code)
	}
	if !strings.Contains(stderr, "unknown migrate subcommand") {
		t.Errorf("expected 'unknown migrate subcommand' message, got: %q", stderr)
	}
}

// --- bp build --out foreign-directory safety ---

// TestBuildRefusesForeignOutDirWithoutForce pins the "silently clobbers
// foreign files" bug: building into a non-empty directory with no Blueprint
// manifest must refuse and leave the foreign content untouched.
func TestBuildRefusesForeignOutDirWithoutForce(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	foreignPath := filepath.Join(outDir, "package.json")
	foreignContent := `{"name":"someone-elses-project"}`
	if err := os.WriteFile(foreignPath, []byte(foreignContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runBP(t, "build", bp, "--out", outDir)
	if code == 0 {
		t.Fatalf("expected non-zero exit when out dir has foreign files, got 0")
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("expected stderr to mention --force, got: %q", stderr)
	}
	if !strings.Contains(stderr, "package.json") {
		t.Errorf("expected stderr to list the colliding file, got: %q", stderr)
	}

	got, err := os.ReadFile(foreignPath)
	if err != nil || string(got) != foreignContent {
		t.Errorf("foreign package.json was modified: got=%q err=%v", string(got), err)
	}
}

// TestBuildForceOverwritesForeignOutDir verifies --force proceeds anyway.
func TestBuildForceOverwritesForeignOutDir(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	foreignPath := filepath.Join(outDir, "package.json")
	foreignContent := `{"name":"someone-elses-project"}`
	if err := os.WriteFile(foreignPath, []byte(foreignContent), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runBP(t, "build", bp, "--out", outDir, "--force")
	if code != 0 {
		t.Fatalf("expected exit 0 with --force, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	got, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == foreignContent {
		t.Error("expected --force to overwrite the foreign package.json with generated content")
	}
	if _, err := os.Stat(filepath.Join(outDir, ".blueprint", "manifest.json")); err != nil {
		t.Errorf("expected manifest.json to exist after a forced build: %v", err)
	}
}

// TestBuildRebuildIntoManifestDirWithoutForce verifies commands that build
// internally (run/dev/test/migrate/deploy) keep working against their own
// prior output — a dir bp already built into (has a manifest) never needs
// --force, even though it's non-empty.
func TestBuildRebuildIntoManifestDirWithoutForce(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	if _, stderr, code := runBP(t, "build", bp, "--out", outDir); code != 0 {
		t.Fatalf("initial build failed: %s", stderr)
	}
	_, stderr, code := runBP(t, "build", bp, "--out", outDir)
	if code != 0 {
		t.Fatalf("expected rebuild into manifest-bearing dir to succeed without --force, got %d\nstderr: %s", code, stderr)
	}
}

// TestBuildIntoEmptyOrFreshOutDirNeverRefuses covers the two other safe
// cases: a directory that doesn't exist yet, and one that exists but is
// empty.
func TestBuildIntoEmptyOrFreshOutDirNeverRefuses(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")

	t.Run("missing", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "does-not-exist-yet")
		if _, stderr, code := runBP(t, "build", bp, "--out", outDir); code != 0 {
			t.Errorf("expected exit 0 for a fresh --out dir, got non-zero. stderr: %s", stderr)
		}
	})
	t.Run("empty", func(t *testing.T) {
		outDir := t.TempDir()
		if _, stderr, code := runBP(t, "build", bp, "--out", outDir); code != 0 {
			t.Errorf("expected exit 0 for an empty --out dir, got non-zero. stderr: %s", stderr)
		}
	})
}

// --- bp test / bp run install decision logic (needsInstall) ---
//
// cmdTest used to only check node_modules existence, so a node_modules
// installed before --gen-tests was requested (e.g. by an earlier `bp run`)
// would be considered "up to date" even though --gen-tests's package.json
// now needs @electric-sql/pglite that install never fetched. needsInstall
// tracks a package.json content hash to catch that case.

func TestNeedsInstallMissingNodeModules(t *testing.T) {
	dir := t.TempDir()
	if !needsInstall(dir) {
		t.Error("expected needsInstall=true when node_modules is absent")
	}
}

func TestNeedsInstallSkipsWhenHashMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkgPath := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkgPath, []byte(`{"name":"x","dependencies":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !needsInstall(dir) {
		t.Fatal("expected needsInstall=true before any hash has been recorded")
	}
	recordInstallHash(dir)
	if needsInstall(dir) {
		t.Error("expected needsInstall=false once the recorded hash matches package.json")
	}
}

func TestNeedsInstallDetectsPackageJSONChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkgPath := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkgPath, []byte(`{"name":"x","dependencies":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	recordInstallHash(dir)
	if needsInstall(dir) {
		t.Fatal("expected needsInstall=false right after recording")
	}
	// Simulate --gen-tests adding a new dependency to package.json.
	if err := os.WriteFile(pkgPath, []byte(`{"name":"x","dependencies":{"@electric-sql/pglite":"^0.2.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !needsInstall(dir) {
		t.Error("expected needsInstall=true after package.json changed since the recorded install")
	}
}

// TestTestCommandReinstallsWhenPackageJSONChanges is the end-to-end
// regression: a `bp run` (no --gen-tests) installs deps into an outDir, then
// `bp test` on the same outDir adds @electric-sql/pglite to package.json.
// node_modules already exists from the run above, so the old "install only
// if node_modules is absent" logic would skip install here — this is
// exactly the cryptic pglite failure the fix addresses.
func TestTestCommandReinstallsWhenPackageJSONChanges(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := t.TempDir()
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "bun.log")
	bunStub := filepath.Join(binDir, "bun")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_BUN_LOG\"\n" +
		"if [ \"$1\" = \"install\" ]; then mkdir -p node_modules; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bunStub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_BUN_LOG=" + logFile,
	}

	if _, stderr, code := runBPEnv(t, env, "run", bp, "--out", outDir); code != 0 {
		t.Fatalf("initial run failed: %d\nstderr: %s", code, stderr)
	}
	if err := os.WriteFile(logFile, nil, 0644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runBPEnv(t, env, "test", bp, "--out", outDir)
	if code != 0 {
		t.Fatalf("test command failed: %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "install") {
		t.Errorf("expected bp test to re-run bun install after package.json changed, log: %q", string(logBytes))
	}
}

// --- bp dev: install-if-needed + crash reporting ---

// TestDevInstallsDependencies pins the "bp dev never installs dependencies"
// bug: the initial build in a fresh outDir must trigger a bun install before
// trying to start the server.
func TestDevInstallsDependencies(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "todo.bp")
	if err := os.WriteFile(src, []byte(makeTodoSource("")), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "bun.log")
	bunStub := filepath.Join(binDir, "bun")
	// `bun run start` exits immediately (0) so cmdDev's startNodeProcess
	// returns promptly instead of hanging the test; we only assert install
	// happened before the watch loop settles, then send SIGTERM to stop it.
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_BUN_LOG\"\n" +
		"if [ \"$1\" = \"install\" ]; then mkdir -p node_modules; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bunStub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_BUN_LOG=" + logFile,
	}

	cmd := exec.Command(bpBinary, "dev", src, "--out", outDir)
	cmd.Env = append(os.Environ(), env...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start bp dev: %v", err)
	}
	// Give it a moment to build, install, and attempt to start the server.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(logFile); err == nil && strings.Contains(string(data), "install") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("bun stub was never invoked: %v (stdout=%s stderr=%s)", err, outBuf.String(), errBuf.String())
	}
	if !strings.Contains(string(logBytes), "install") {
		t.Errorf("expected bp dev to run bun install, log: %q", string(logBytes))
	}
}

// TestDevReportsCrashedServer pins the other half of the "keeps watching
// after the server fails to start, as if healthy" bug: when the started
// process exits non-zero on its own (not because bp dev stopped it for a
// rebuild), bp dev must report that explicitly instead of staying silent.
func TestDevReportsCrashedServer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "todo.bp")
	if err := os.WriteFile(src, []byte(makeTodoSource("")), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	binDir := t.TempDir()
	bunStub := filepath.Join(binDir, "bun")
	// install succeeds, but `bun run start` crashes immediately (exit 7) —
	// simulates a server that dies on its own right after cmd.Start() returns.
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"install\" ]; then mkdir -p node_modules; exit 0; fi\n" +
		"if [ \"$1\" = \"run\" ] && [ \"$2\" = \"start\" ]; then exit 7; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bunStub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}

	cmd := exec.Command(bpBinary, "dev", src, "--out", outDir)
	cmd.Env = append(os.Environ(), env...)
	var errBuf syncBuffer
	cmd.Stdout = &syncBuffer{}
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start bp dev: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(errBuf.String(), "Server exited unexpectedly") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()

	if !strings.Contains(errBuf.String(), "Server exited unexpectedly") {
		t.Errorf("expected bp dev to report the crashed server explicitly, stderr: %q", errBuf.String())
	}
}

// --- bp init next-steps output ---

func TestInitPrintsNextSteps(t *testing.T) {
	tmpDir := t.TempDir()
	cmd := exec.Command(bpBinary, "init", "steps-test")
	cmd.Dir = tmpDir
	var outBuf strings.Builder
	cmd.Stdout = &outBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("bp init failed: %v", err)
	}
	stdout := outBuf.String()
	for _, want := range []string{"cd steps-test", "bp check steps-test.bp", "bp run steps-test.bp", "DATABASE_URL"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected init output to mention %q, got: %q", want, stdout)
		}
	}
}

// --- shell completion command table coverage ---

// TestCompletionScriptsIncludeAllCommands guards against the bash/zsh/fish
// completion lists drifting out of sync with the actual command set again —
// each script must mention every command in cliCommands, including the ones
// that were previously missing (stats, doctor, explain, context, llms).
func TestCompletionScriptsIncludeAllCommands(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			stdout, stderr, code := runBP(t, "completion", shell)
			if code != 0 {
				t.Fatalf("completion %s failed: %d\nstderr: %s", shell, code, stderr)
			}
			for _, cmdName := range []string{"stats", "doctor", "explain", "context", "llms", "lsp"} {
				if !strings.Contains(stdout, cmdName) {
					t.Errorf("%s completion missing command %q", shell, cmdName)
				}
			}
		})
	}
}
