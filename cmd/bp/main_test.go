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
	// Use an inline synthetic spec with constructs rejected by the Python target:
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

// TestDeployUsesBlueprintPort protects non-default-port services from a
// misleading run command (and the same value is threaded into the live smoke
// test path). Deploy reads the generated Dockerfile, the artifact Docker
// actually builds, instead of assuming port 3000.
func TestDeployUsesBlueprintPort(t *testing.T) {
	bpDir := t.TempDir()
	bp := filepath.Join(bpDir, "custom-port.bp")
	src := `blueprint "custom-port" {
  version "1.0"
  port 4242
  runtime node
}
`
	if err := os.WriteFile(bp, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	binDir := t.TempDir()
	dockerStub := filepath.Join(binDir, "docker")
	if err := os.WriteFile(dockerStub, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}
	stdout, stderr, code := runBPEnv(t, env, "deploy", bp, "--out", outDir, "--no-run", "--tag", "bp-port-test:latest")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "docker run -p 4242:4242 bp-port-test:latest") {
		t.Errorf("expected declared port in run command, got: %q", stdout)
	}
}

func TestBuildRejectsUnresolvedGenerateSlot(t *testing.T) {
	bpDir := t.TempDir()
	bp := filepath.Join(bpDir, "unresolved.bp")
	src := `blueprint "unresolved" {
  version "1.0"
  port 3000
  runtime node
}

GET /work {
  @> "add the requested behavior"
  -> 200 "ok"
}
`
	if err := os.WriteFile(bp, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "generated")
	_, stderr, code := runBP(t, "build", bp, "--out", outDir)
	if code != 4 {
		t.Fatalf("expected codegen exit 4, got %d; stderr: %s", code, stderr)
	}
	for _, want := range []string{"unresolved @> generation slot", "bp generate " + bp + " --write"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("expected stderr to contain %q; got %q", want, stderr)
		}
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("rejected build should not write output; stat err=%v", err)
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

// TestMigrateEffectTargetRejected guards against --target effect (a valid
// resolveTarget value) silently falling through to the drizzle-kit path,
// which used to build a node tree with no drizzle.config.ts and die on a
// confusing "config not found" error instead of a clear one. The rejection
// must happen before any build/tooling invocation, so no --out directory
// should even be created.
func TestMigrateEffectTargetRejected(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := filepath.Join(t.TempDir(), "effect-migrate-out")
	_, stderr, code := runBP(t, "migrate", bp, "generate", "--out", outDir, "--target", "effect")
	if code == 0 {
		t.Fatal("expected non-zero exit for --target effect migrate")
	}
	if strings.Contains(stderr, "unknown --target") {
		t.Errorf("effect is a valid target — expected a migrations-specific rejection, not 'unknown --target': %q", stderr)
	}
	if strings.Contains(stderr, "drizzle.config.ts") {
		t.Errorf("expected a clear effect-specific error, not the drizzle.config.ts fallthrough: %q", stderr)
	}
	if !strings.Contains(stderr, "does not support migrations") {
		t.Errorf("expected a clear 'does not support migrations' message, got: %q", stderr)
	}
	if _, err := os.Stat(outDir); err == nil {
		t.Errorf("expected --target effect to be rejected before any build, but %s was created", outDir)
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

// --- copyProjectEnv: refresh bp's own copy, preserve hand edits ---

// TestCopyProjectEnvRefreshesStaleCopyButPreservesHandEdits pins the "bp
// migrate silently uses a stale .env" bug: copyProjectEnv used to return
// early forever once outDir/.env existed, even though that file was
// virtually always bp's OWN earlier copy rather than a hand edit. A later
// change to the project .env (e.g. a new DATABASE_URL) was then silently
// ignored on every subsequent bp migrate/run/dev/test invocation.
//
// This test drives `bp migrate ... check --target python` (via a `uv` stub
// so no real alembic/DB is needed) three times:
//  1. first run — outDir has no .env yet, so it's copied fresh.
//  2. project .env changes, outDir/.env is untouched since bp copied it —
//     it must be refreshed to the new content.
//  3. the user then hand-edits outDir/.env; a further project .env change
//     must NOT clobber the hand edit, and bp must warn about the drift.
func TestCopyProjectEnvRefreshesStaleCopyButPreservesHandEdits(t *testing.T) {
	root := getProjectRoot()
	srcBP, err := os.ReadFile(filepath.Join(root, "examples", "todo-api.bp"))
	if err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	bpFile := filepath.Join(projectDir, "todo-api.bp")
	if err := os.WriteFile(bpFile, srcBP, 0644); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(projectDir, ".env")

	binDir := t.TempDir()
	uvStub := filepath.Join(binDir, "uv")
	if err := os.WriteFile(uvStub, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}

	outDir := t.TempDir()
	outEnvPath := filepath.Join(outDir, ".env")

	runMigrate := func() (stdout, stderr string, code int) {
		return runBPEnv(t, env, "migrate", bpFile, "check", "--out", outDir, "--target", "python")
	}

	// 1. Fresh copy.
	if err := os.WriteFile(envPath, []byte("DATABASE_URL=postgres://OLD-DB\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runMigrate(); code != 0 {
		t.Fatalf("initial migrate failed: %d\nstderr: %s", code, stderr)
	}
	got, err := os.ReadFile(outEnvPath)
	if err != nil || string(got) != "DATABASE_URL=postgres://OLD-DB\n" {
		t.Fatalf("expected fresh .env copy, got %q, err=%v", got, err)
	}

	// 2. Project .env changes; outDir/.env is still bp's untouched copy, so
	// it must be refreshed rather than left stale.
	if err := os.WriteFile(envPath, []byte("DATABASE_URL=postgres://NEW-DB\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runMigrate(); code != 0 {
		t.Fatalf("second migrate failed: %d\nstderr: %s", code, stderr)
	}
	got, err = os.ReadFile(outEnvPath)
	if err != nil || string(got) != "DATABASE_URL=postgres://NEW-DB\n" {
		t.Fatalf("stale .env bug: expected outDir/.env refreshed to NEW-DB, got %q, err=%v", got, err)
	}

	// 3. The user hand-edits outDir/.env directly.
	if err := os.WriteFile(outEnvPath, []byte("DATABASE_URL=postgres://HAND-EDITED\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("DATABASE_URL=postgres://NEWER-DB\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runMigrate()
	if code != 0 {
		t.Fatalf("third migrate failed: %d\nstderr: %s", code, stderr)
	}
	got, err = os.ReadFile(outEnvPath)
	if err != nil || string(got) != "DATABASE_URL=postgres://HAND-EDITED\n" {
		t.Errorf("hand-edited outDir/.env must be preserved, got %q, err=%v", got, err)
	}
	if !strings.Contains(stderr, envPath) {
		t.Errorf("expected a stderr notice pointing at the drifted project .env, got: %q", stderr)
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

// --- bp eject ---

// TestEjectRemovesMarkers verifies the core eject behavior: "Generated by
// Blueprint" / "Do not edit directly" header lines are stripped from .ts
// files, non-matching content is left alone.
func TestEjectRemovesMarkers(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	if _, stderr, code := runBP(t, "build", bp, "--out", outDir); code != 0 {
		t.Fatalf("build failed: %d\nstderr: %s", code, stderr)
	}

	// Confirm the fixture actually has markers before ejecting, so this test
	// can't pass vacuously.
	var markedFile string
	_ = filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || markedFile != "" || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "Generated by Blueprint") {
			markedFile = path
		}
		return nil
	})
	if markedFile == "" {
		t.Fatal("no generated .ts file with a Blueprint marker found — test fixture assumption broken")
	}

	stdout, stderr, code := runBP(t, "eject", outDir)
	if code != 0 {
		t.Fatalf("eject failed: %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Ejected") {
		t.Errorf("expected eject summary in stdout, got: %q", stdout)
	}

	got, err := os.ReadFile(markedFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "Generated by Blueprint") || strings.Contains(string(got), "Do not edit directly") {
		t.Errorf("expected markers stripped from %s, still present: %s", markedFile, got)
	}
}

// TestEjectRemovesManifest pins the eject data-loss bug: eject used to strip
// markers but leave .blueprint/manifest.json intact, so the very next `bp
// build --out <ejected-dir>` would sail past checkOutDirSafety's foreign-dir
// guard (which treats "manifest present" as "safe to overwrite, bp made
// this") and silently clobber every file just handed over to the user.
func TestEjectRemovesManifest(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	if _, stderr, code := runBP(t, "build", bp, "--out", outDir); code != 0 {
		t.Fatalf("build failed: %d\nstderr: %s", code, stderr)
	}
	manifestPath := filepath.Join(outDir, ".blueprint", "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected manifest.json after build, got err: %v", err)
	}

	if _, stderr, code := runBP(t, "eject", outDir); code != 0 {
		t.Fatalf("eject failed: %d\nstderr: %s", code, stderr)
	}

	if _, err := os.Stat(manifestPath); err == nil {
		t.Error("expected .blueprint/manifest.json to be removed by eject, but it still exists")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected error statting manifest after eject: %v", err)
	}
}

// TestEjectThenBuildRefusesWithoutForce is the end-to-end version of the
// manifest-removal fix: build, eject, then build again into the same
// directory without --force must now refuse (foreign-dir guard), instead of
// silently overwriting the user's ejected code. --force still works, for
// users who deliberately want to regenerate over ejected output.
func TestEjectThenBuildRefusesWithoutForce(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := t.TempDir()
	if _, stderr, code := runBP(t, "build", bp, "--out", outDir); code != 0 {
		t.Fatalf("build failed: %d\nstderr: %s", code, stderr)
	}
	if _, stderr, code := runBP(t, "eject", outDir); code != 0 {
		t.Fatalf("eject failed: %d\nstderr: %s", code, stderr)
	}

	_, stderr, code := runBP(t, "build", bp, "--out", outDir)
	if code == 0 {
		t.Fatal("expected rebuild into an ejected directory to refuse without --force")
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("expected stderr to mention --force, got: %q", stderr)
	}

	// --force must still be able to proceed.
	if _, stderr, code := runBP(t, "build", bp, "--out", outDir, "--force"); code != 0 {
		t.Fatalf("expected --force rebuild to succeed, got %d\nstderr: %s", code, stderr)
	}
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

func TestTestCommandPythonBuildsAndRunsPytest(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := filepath.Join(t.TempDir(), "python-tests")
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "uv.log")
	uvStub := filepath.Join(binDir, "uv")
	script := "#!/bin/sh\n" +
		"printf '%s|%s\\n' \"$PWD\" \"$*\" >> \"$BP_UV_LOG\"\n" +
		"exit 0\n"
	if err := os.WriteFile(uvStub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_UV_LOG=" + logFile,
	}

	stdout, stderr, code := runBPEnv(t, env, "test", bp, "--out", outDir, "--target", "python")
	if code != 0 {
		t.Fatalf("python test command failed: %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	wantInvocation := outDir + "|run pytest"
	if !strings.Contains(string(logBytes), wantInvocation) {
		t.Errorf("expected uv to run pytest in generated output; want %q in log %q", wantInvocation, string(logBytes))
	}
	if _, err := os.Stat(filepath.Join(outDir, "tests", "conftest.py")); err != nil {
		t.Errorf("expected generated pytest harness: %v", err)
	}
	pyproject, err := os.ReadFile(filepath.Join(outDir, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pyproject), "testcontainers[postgresql]") {
		t.Error("expected bp test --target python to enable the Postgres testcontainers dependency")
	}
}

func TestTestCommandRejectsEffectTarget(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "hello-world.bp")
	outDir := filepath.Join(t.TempDir(), "effect-tests")

	_, stderr, code := runBP(t, "test", bp, "--out", outDir, "--target", "effect")
	if code != 2 {
		t.Fatalf("expected exit 2 for unsupported effect tests, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "does not support generated tests") {
		t.Errorf("expected actionable effect-target error, got: %q", stderr)
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("effect test rejection should happen before writing output; stat err=%v", err)
	}
}

func TestNodeAuthoredTestPreflightRejectsUnfaithfulSurfaces(t *testing.T) {
	tests := []struct {
		name        string
		extra       string
		wantMessage string
	}{
		{
			name: "missing target",
			extra: `
test no_target {
  request { body { title: "x" } }
  expect { status 201 }
}
`,
			wantMessage: "no target request would be emitted",
		},
		{
			name: "file fixture request value",
			extra: `
fixture "sample" from "testdata/sample.png"
test upload_fixture {
  target POST /api/todos
  request { body { title: "x", file: fixture("sample") } }
  expect { status 201 }
}
`,
			wantMessage: `fixture("sample") is JSON-stringified`,
		},
		{
			name: "custom request headers",
			extra: `
test custom_headers {
  target POST /api/todos
  request {
    headers { Authorization: "test" }
    body { title: "x" }
  }
  expect { status 201 }
}
`,
			wantMessage: "custom request headers are ignored",
		},
		{
			name: "unsupported auth scheme",
			extra: `
test session_auth {
  target POST /api/todos
  request {
    auth webhook_sig("signature")
    body { title: "x" }
  }
  expect { status 201 }
}
`,
			wantMessage: `auth scheme "webhook_sig" is not emitted`,
		},
		{
			name: "multipart request entry",
			extra: `
test multipart_upload {
  target POST /api/todos
  request { multipart { title: "x" } }
  expect { status 201 }
}
`,
			wantMessage: "requires form/multipart encoding",
		},
		{
			name: "file typed endpoint field",
			extra: `
POST /api/upload {
  <- file file required
  -> 200 "ok"
}
test file_body {
  target POST /api/upload
  request { body { file: "contents" } }
  expect { status 200 }
}
`,
			wantMessage: `body field "file" targets a file input`,
		},
		{
			name: "dynamic target path",
			extra: `
test dynamic_path {
  target POST /api/todos/:id
  request { body { title: "x" } }
  expect { status 201 }
}
`,
			wantMessage: "still contains a :parameter placeholder",
		},
		{
			name: "cleanup body",
			extra: `
test with_cleanup {
  target POST /api/todos
  request { body { title: "x" } }
  expect { status 201 }
  cleanup { |> log "cleaning" }
}
`,
			wantMessage: "cleanup statements are parsed but not emitted",
		},
		{
			name: "test group shared setup",
			extra: `
test grouped_todo {
  target POST /api/todos
  request { body { title: "x" } }
  expect { status 201 }
}
test_group todo_suite {
  shared_setup { |> log "required group setup" }
  tests [grouped_todo]
}
`,
			wantMessage: `test_group "todo_suite" shared_setup statements are not emitted`,
		},
		{
			name: "timing assertion",
			extra: `
test timed_request {
  target POST /api/todos
  request { body { title: "x" } }
  expect {
    status 201
    duration < 2s
  }
}
`,
			wantMessage: "is emitted only as a TODO comment",
		},
		{
			name: "empty expectations",
			extra: `
test no_assertions {
  target GET /health
  expect { }
}
`,
			wantMessage: "contains no executable assertion",
		},
		{
			name: "malformed status assertion",
			extra: `
test malformed_status {
  target GET /health
  expect { status success }
}
`,
			wantMessage: "must contain exactly an HTTP status",
		},
		{
			name: "unknown body type assertion",
			extra: `
test unknown_body_type {
  target GET /health
  expect { body.status is magical }
}
`,
			wantMessage: "uses an unsupported type",
		},
		{
			name: "assertion literal needs emitter escaping",
			extra: `
test apostrophe_literal {
  target GET /health
  expect { body.status == "it's" }
}
`,
			wantMessage: "must compare with one literal value",
		},
		{
			name: "body not_exists spelling is a generator TODO",
			extra: `
test body_not_exists {
  target GET /health
  expect { body.error not_exists }
}
`,
			wantMessage: "is not a supported field assertion",
		},
		{
			name: "header name the emitter cannot reproduce",
			extra: `
test hyphenated_header {
  target GET /health
  expect { header.Content-Type == "application/json" }
}
`,
			wantMessage: "simple header name",
		},
		{
			name: "model inequality needs an unimported operator",
			extra: `
test model_inequality {
  target GET /health
  expect { model todo where(title != "x") exists }
}
`,
			wantMessage: "must use declared fields with ==",
		},
		{
			name: "model body reference without parsed response",
			extra: `
test model_body_without_body_assertion {
  target POST /api/todos
  request { body { title: "x" } }
  expect {
    status 201
    model todo where(id == body.id) exists
  }
}
`,
			wantMessage: "unsupported right-hand value",
		},
		{
			name: "multiple model assertions redeclare generated binding",
			extra: `
test multiple_model_assertions {
  target GET /health
  expect {
    model todo where(title == "first") exists
    model todo where(title == "second") exists
  }
}
`,
			wantMessage: "redeclare the generated _row binding",
		},
		{
			name: "setup input is silently dropped",
			extra: `
test setup_input {
  target GET /health
  setup { <- ignored string }
  expect { status 200 }
}
`,
			wantMessage: "not in the authored-test setup allowlist",
		},
		{
			name: "setup guard needs unavailable BpError",
			extra: `
test setup_guard {
  target GET /health
  setup { |> guard true -> 400 "stop" }
  expect { status 200 }
}
`,
			wantMessage: "not in the authored-test setup allowlist",
		},
		{
			name: "setup runtime helper is unavailable",
			extra: `
test setup_emit {
  target GET /health
  setup { |> emit changed { value: "x" } }
  expect { status 200 }
}
`,
			wantMessage: `setup call "emit" is not in`,
		},
		{
			name: "setup query is outside the import-safe subset",
			extra: `
test setup_query {
  target GET /health
  setup { |> rows = query todo where(title == "x") }
  expect { status 200 }
}
`,
			wantMessage: `setup call "query" is not in`,
		},
		{
			name: "request builtin call is not imported",
			extra: `
test request_clock {
  target POST /api/todos
  request { body { title: clock() } }
  expect { status 201 }
}
`,
			wantMessage: `calls "clock"`,
		},
		{
			name: "request string interpolation is not scoped",
			extra: `
test request_interpolation {
  target POST /api/todos
  setup { |> existing = seed todo { title: "old" } }
  request { body { title: "todo-{existing.title}" } }
  expect { status 201 }
}
`,
			wantMessage: "uses string interpolation",
		},
		{
			name: "auth builtin call is not imported",
			extra: `
test auth_clock {
  target GET /health
  request { auth bearer(clock()) }
  expect { status 200 }
}
`,
			wantMessage: `auth value calls "clock"`,
		},
		{
			name: "duplicate body entries",
			extra: `
test duplicate_body {
  target POST /api/todos
  request {
    body { title: "first" }
    body { title: "second" }
  }
  expect { status 201 }
}
`,
			wantMessage: `contains 2 "body" entries`,
		},
		{
			name: "noncanonical request entry casing",
			extra: `
test uppercase_body {
  target POST /api/todos
  request { Body { title: "ignored" } }
  expect { status 201 }
}
`,
			wantMessage: `request entry "Body" is ignored`,
		},
		{
			name: "zero repeat is silently coerced",
			extra: `
test zero_repeat {
  target POST /api/todos
  request repeat(0) { body { title: "x" } }
  expect { status 201 }
}
`,
			wantMessage: "repeat(0) would be silently emitted as one request",
		},
		{
			name: "auth whole setup row",
			extra: `
test auth_row {
  target GET /health
  setup { |> key = seed todo { title: "secret" } }
  request { auth bearer(key) }
  expect { status 200 }
}
`,
			wantMessage: "uses the whole setup row",
		},
		{
			name: "get request body",
			extra: `
test get_body {
  target GET /health
  request { body { value: "x" } }
  expect { status 200 }
}
`,
			wantMessage: "GET authored requests cannot carry",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			filename := filepath.Join(dir, "service.bp")
			if err := os.WriteFile(filename, []byte(makeTodoSource(tc.extra)), 0o644); err != nil {
				t.Fatal(err)
			}
			issues := preflightNodeAuthoredTests(filename, filepath.Join(dir, "generated"))
			if len(issues) == 0 {
				t.Fatalf("expected a preflight issue containing %q", tc.wantMessage)
			}
			var messages []string
			for _, issue := range issues {
				messages = append(messages, issue.Message)
			}
			if !strings.Contains(strings.Join(messages, "\n"), tc.wantMessage) {
				t.Fatalf("expected issue containing %q, got:\n%s", tc.wantMessage, strings.Join(messages, "\n"))
			}
		})
	}
}

func TestEndpointPathMatchesIgnoresLiteralQueryAndFragment(t *testing.T) {
	for _, target := range []string{"/api/upload?mode=test", "/api/upload#response"} {
		if !endpointPathMatches("/api/upload", target) {
			t.Errorf("expected endpoint path to match target %q after URL suffix normalization", target)
		}
	}
	if endpointPathMatches("/api/upload", "/api/other?mode=test") {
		t.Error("query normalization must not hide a different path")
	}
}

func TestNodeAuthoredTestModelsImplyGeneratedDatabase(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "service.bp")
	src := `blueprint "no-db" {
  version "1.0.0"
  port 3000
  runtime node
}
model todo {
  id uuid primary
  title string required
}
test data_without_db {
  target GET /health
  setup { |> row = seed todo { title: "x" } }
  expect { model todo where(title == "x") exists }
}
`
	if err := os.WriteFile(filename, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := preflightNodeAuthoredTests(filename, filepath.Join(dir, "generated"))
	if len(issues) != 0 {
		var messages []string
		for _, issue := range issues {
			messages = append(messages, issue.Message)
		}
		t.Fatalf("models should imply the generated database layer; got:\n%s", strings.Join(messages, "\n"))
	}
}

func TestNodeAuthoredTestPreflightDetectsZeroRepeatInInclude(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "service.bp")
	includedFile := filepath.Join(dir, "included.bp")
	if err := os.WriteFile(mainFile, []byte(makeTodoSource(`include "included.bp"`)), 0o644); err != nil {
		t.Fatal(err)
	}
	included := `test included_zero_repeat {
  target POST /api/todos
  request repeat(0) { body { title: "x" } }
  expect { status 201 }
}
`
	if err := os.WriteFile(includedFile, []byte(included), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := preflightNodeAuthoredTests(mainFile, filepath.Join(dir, "generated"))
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "repeat(0) would be silently emitted") {
		t.Fatalf("expected included repeat(0) rejection, got %#v", issues)
	}
}

func TestNodeAuthoredTestPreflightKeepsSupportedSubset(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "service.bp")
	src := makeTodoSource(`
test create_todo {
  target POST /api/todos
  setup { |> existing = seed todo { title: "Existing" } }
  request {
    auth bearer("pre-encoded-token")
    body { title: "New" }
  }
  expect {
    status 201
    body.id is uuid
    model todo where(title == "New") exists
  }
}
test requestless_health_check {
  target GET /health
  expect { status 200 }
}
`)
	if err := os.WriteFile(filename, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if issues := preflightNodeAuthoredTests(filename, filepath.Join(dir, "generated")); len(issues) > 0 {
		var messages []string
		for _, issue := range issues {
			messages = append(messages, issue.Message)
		}
		t.Fatalf("supported JSON/authored-test subset should pass preflight, got:\n%s", strings.Join(messages, "\n"))
	}
}

func TestNodeAuthoredTestPreflightRequiresReachableNativeImplementation(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "service.bp")
	outDir := filepath.Join(dir, "generated")
	nativeDecl := `
fn transform_title {
  <- value string
  -> output string
  impl node { module: "./internal/transform-title", func: "transformTitle" }
}
`
	nativeEndpointAndTest := `
POST /api/native {
  <- value string required
  |> result = transform_title(value)
  -> 200 { result: result }
}
test native_success {
  target POST /api/native
  request { body { value: "x" } }
  expect { status 200 }
}
`
	if err := os.WriteFile(filename, []byte(makeTodoSource(nativeDecl+nativeEndpointAndTest)), 0o644); err != nil {
		t.Fatal(err)
	}

	issues := preflightNodeAuthoredTests(filename, outDir)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "native implementation is missing") {
		t.Fatalf("expected one missing-native issue, got %#v", issues)
	}

	implPath := filepath.Join(outDir, "src", "impl", "functions", "internal", "transform-title.ts")
	if err := os.MkdirAll(filepath.Dir(implPath), 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "export async function transformTitle(value: string) { throw new Error('Not implemented: transformTitle'); }\n"
	if err := os.WriteFile(implPath, []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	issues = preflightNodeAuthoredTests(filename, outDir)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "still throws Not implemented") {
		t.Fatalf("expected one unimplemented-scaffold issue, got %#v", issues)
	}

	implementation := "export async function transformTitle(value: string) { return value.toUpperCase(); }\n"
	if err := os.WriteFile(implPath, []byte(implementation), 0o644); err != nil {
		t.Fatal(err)
	}
	if issues = preflightNodeAuthoredTests(filename, outDir); len(issues) > 0 {
		t.Fatalf("completed native implementation should pass preflight, got %#v", issues)
	}

	// A native declaration not reachable from this authored target does not
	// poison an otherwise supported case.
	if err := os.WriteFile(filename, []byte(makeTodoSource(nativeDecl+`
test ordinary_todo {
  target POST /api/todos
  request { body { title: "x" } }
  expect { status 201 }
}
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(outDir); err != nil {
		t.Fatal(err)
	}
	if issues = preflightNodeAuthoredTests(filename, outDir); len(issues) > 0 {
		t.Fatalf("unreachable native declaration should not block the test, got %#v", issues)
	}

	// Vitest imports the complete generated app. A sibling route therefore
	// loads its function wrapper even when this authored request targets
	// /health, so a missing native dependency there must fail preflight.
	if err := os.WriteFile(filename, []byte(makeTodoSource(nativeDecl+`
GET /api/native-sibling {
  |> result = transform_title("x")
  -> 200 { result: result }
}
test ordinary_health {
  target GET /health
  expect { status 200 }
}
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	issues = preflightNodeAuthoredTests(filename, outDir)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "native implementation is missing") {
		t.Fatalf("expected sibling app route to require its loaded native module, got %#v", issues)
	}
}

func TestNodeTestNativePreflightPathSafetyAndModuleResolution(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "generated")

	validPath, local, safe := localNativeImplementationPath("./internal/transform-title", outDir)
	if !local || !safe || validPath != filepath.Join(outDir, "src", "impl", "functions", "internal", "transform-title.ts") {
		t.Fatalf("expected safe local scaffold path, got path=%q local=%v safe=%v", validPath, local, safe)
	}
	if _, local, safe := localNativeImplementationPath("./internal/../../escape", outDir); !local || safe {
		t.Fatalf("expected ./internal traversal to remain classified as local but unsafe; local=%v safe=%v", local, safe)
	}

	if err := os.MkdirAll(filepath.Join(outDir, "node_modules", "example"), 0o755); err != nil {
		t.Fatal(err)
	}
	if available, safe := nodeModuleAvailable("example", outDir); !safe || available {
		t.Fatalf("generator emits example.js, so node_modules/example must not satisfy it; available=%v safe=%v", available, safe)
	}
	if err := os.MkdirAll(filepath.Join(outDir, "node_modules", "example.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if available, safe := nodeModuleAvailable("example", outDir); !safe || !available {
		t.Fatalf("expected emitted package example.js to be found; available=%v safe=%v", available, safe)
	}
	if available, safe := nodeModuleAvailable("node:fs", outDir); !safe || available {
		t.Fatalf("node:fs is emitted as invalid node:fs.js and must not be accepted; available=%v safe=%v", available, safe)
	}
	if available, safe := nodeModuleAvailable("../../../outside.js", outDir); safe || available {
		t.Fatalf("relative module traversal must be unsafe; available=%v safe=%v", available, safe)
	}

	relativeSource := filepath.Join(outDir, "src", "functions", "helper.ts")
	if err := os.MkdirAll(filepath.Dir(relativeSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(relativeSource, []byte("export function helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if available, safe := nodeModuleAvailable("./helper", outDir); !safe || !available {
		t.Fatalf("expected ./helper.js emitted import to resolve helper.ts; available=%v safe=%v", available, safe)
	}

	outsideExec := filepath.Join(filepath.Dir(outDir), "outside-runner")
	if err := os.WriteFile(outsideExec, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if available, safe := execCommandAvailable("../outside-runner", outDir); safe || available {
		t.Fatalf("exec path traversal must be unsafe; available=%v safe=%v", available, safe)
	}
	insideExec := filepath.Join(outDir, "bin", "runner")
	if err := os.MkdirAll(filepath.Dir(insideExec), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(insideExec, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if available, safe := execCommandAvailable("bin/runner", outDir); !safe || !available {
		t.Fatalf("generated-project executable should be available; available=%v safe=%v", available, safe)
	}
}

func TestTestCommandPreflightStopsBeforeBuildInstallAndVitest(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "service.bp")
	outDir := filepath.Join(dir, "generated")
	src := makeTodoSource(`
test cleanup_is_required {
  target POST /api/todos
  request { body { title: "x" } }
  expect { status 201 }
  cleanup { |> log "must run" }
}
`)
	if err := os.WriteFile(filename, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "bun.log")
	bunStub := filepath.Join(binDir, "bun")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$BP_BUN_LOG\"\nexit 0\n"
	if err := os.WriteFile(bunStub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_BUN_LOG=" + logFile,
	}

	_, stderr, code := runBPEnv(t, env, "test", filename, "--out", outDir)
	if code != 2 {
		t.Fatalf("expected preflight exit 2, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "cleanup statements are parsed but not emitted") || !strings.Contains(stderr, "Vitest was not started") {
		t.Fatalf("expected actionable preflight diagnostic, got: %s", stderr)
	}
	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Fatalf("bun must not be invoked after preflight rejection; stat err=%v", err)
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("preflight should happen before build output is written; stat err=%v", err)
	}
}

func TestBuildStillAllowsAuthoredTestSurfacesRejectedByTestCommand(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "service.bp")
	outDir := filepath.Join(dir, "generated")
	src := makeTodoSource(`
test cleanup_is_required {
  target POST /api/todos/:id
  request {
    headers { X_Test: "value" }
    body { title: "x" }
  }
  expect { duration < 2s }
  cleanup { |> log "must run" }
}
`)
	if err := os.WriteFile(filename, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runBP(t, "build", filename, "--out", outDir)
	if code != 0 {
		t.Fatalf("ordinary build must remain available for hand-written Vitest workflows: code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(outDir, "test", "cleanup-is-required.test.ts")); err != nil {
		t.Fatalf("expected ordinary build to emit the authored test file: %v", err)
	}
}

func TestTestCommandRunsSupportedAuthoredNodeSubset(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "service.bp")
	outDir := filepath.Join(dir, "generated")
	src := makeTodoSource(`
test create_todo {
  target POST /api/todos
  request { body { title: "x" } }
  expect {
    status 201
    body.id is uuid
  }
}
`)
	if err := os.WriteFile(filename, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "bun.log")
	bunStub := filepath.Join(binDir, "bun")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_BUN_LOG\"\n" +
		"if [ \"$1\" = \"install\" ]; then mkdir -p node_modules; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bunStub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_BUN_LOG=" + logFile,
	}

	stdout, stderr, code := runBPEnv(t, env, "test", filename, "--out", outDir)
	if code != 0 {
		t.Fatalf("supported authored test should run: code=%d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "install") || !strings.Contains(string(logBytes), "run test") {
		t.Fatalf("expected install and Vitest invocation, got log %q", string(logBytes))
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

// --- deterministic endpoint property generation ---

func TestBuildGenPropertyTestsEmitsAdditiveNodeSuite(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := filepath.Join(t.TempDir(), "property-build")

	stdout, stderr, code := runBP(t, "build", bp, "--out", outDir, "--gen-property-tests")
	if code != 0 {
		t.Fatalf("property build failed: %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, rel := range []string{
		"test/generated/todos.test.ts",
		"test/generated/todos.property.test.ts",
		"test/_harness/db.ts",
		"vitest.config.ts",
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Errorf("--gen-property-tests should emit %s: %v", rel, err)
		}
	}
	pkg, err := os.ReadFile(filepath.Join(outDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range []string{"fast-check", "@electric-sql/pglite"} {
		if !strings.Contains(string(pkg), dependency) {
			t.Errorf("property build package.json missing %q: %s", dependency, pkg)
		}
	}
}

func TestBuildGenPropertyTestsRejectsNonNodeBeforeWriting(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	for _, target := range []string{"python", "effect"} {
		t.Run(target, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), "must-not-exist")
			_, stderr, code := runBP(t, "build", bp, "--out", outDir, "--target", target, "--gen-property-tests")
			if code != 2 {
				t.Fatalf("expected usage exit 2, got %d; stderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, "supported only with --target node") {
				t.Errorf("expected focused target error, got: %q", stderr)
			}
			if _, err := os.Stat(outDir); !os.IsNotExist(err) {
				t.Errorf("target rejection must happen before output is written; stat err=%v", err)
			}
		})
	}
}

func TestDiffGenPropertyTestsUsesSameGenerationMode(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := filepath.Join(t.TempDir(), "absent-output")
	stdout, stderr, code := runBP(t, "diff", bp, "--out", outDir, "--gen-property-tests")
	if code != 0 {
		t.Fatalf("property diff failed: %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{"test/generated/todos.property.test.ts", "test/generated/todos.test.ts"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("property diff should report %q, got:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("diff must not write the requested output directory; stat err=%v", err)
	}
}

func TestTestGenPropertyTestsBuildsAndRunsVitest(t *testing.T) {
	root := getProjectRoot()
	bp := filepath.Join(root, "examples", "todo-api.bp")
	outDir := filepath.Join(t.TempDir(), "property-tests")
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "bun.log")
	bunStub := filepath.Join(binDir, "bun")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$BP_BUN_LOG\"\n" +
		"if [ \"$1\" = \"install\" ]; then mkdir -p node_modules; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bunStub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"BP_BUN_LOG=" + logFile,
	}

	stdout, stderr, code := runBPEnv(t, env, "test", bp, "--out", outDir, "--gen-property-tests")
	if code != 0 {
		t.Fatalf("property test command failed: %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, invocation := range []string{"install", "run test"} {
		if !strings.Contains(string(logBytes), invocation) {
			t.Errorf("expected bun %s invocation, log: %q", invocation, logBytes)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "test/generated/todos.property.test.ts")); err != nil {
		t.Errorf("bp test --gen-property-tests should emit property suite: %v", err)
	}
}

func TestPropertyTestFlagAppearsInHelpAndCompletions(t *testing.T) {
	for _, args := range [][]string{{"build", "--help"}, {"test", "--help"}, {"diff", "--help"}, {"completion", "bash"}, {"completion", "fish"}} {
		stdout, stderr, code := runBP(t, args...)
		if code != 0 {
			t.Fatalf("bp %s failed: %d; stderr=%s", strings.Join(args, " "), code, stderr)
		}
		if !strings.Contains(stdout+stderr, "--gen-property-tests") && !strings.Contains(stdout+stderr, "gen-property-tests") {
			t.Errorf("bp %s should expose --gen-property-tests, stdout=%q stderr=%q", strings.Join(args, " "), stdout, stderr)
		}
	}
}
