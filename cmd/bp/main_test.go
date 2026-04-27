package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
