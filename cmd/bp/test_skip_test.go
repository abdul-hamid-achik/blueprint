package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHasGeneratedTests pins the detection that lets bp test skip the runner
// when a spec (for example, one with only STREAM endpoints) generates no tests.
func TestHasGeneratedTests(t *testing.T) {
	write := func(t *testing.T, dir, rel string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("// test\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		target string
		files  []string
		want   bool
	}{
		{"node no test dir", targetNode, nil, false},
		{"node harness only", targetNode, []string{"test/_harness/setup.ts"}, false},
		{"node generated endpoint test", targetNode, []string{"test/generated/todos.test.ts"}, true},
		{"node authored test block", targetNode, []string{"test/_harness/setup.ts", "test/signup.test.ts"}, true},
		{"python no tests dir", targetPython, nil, false},
		{"python conftest only", targetPython, []string{"tests/conftest.py", "tests/__init__.py"}, false},
		{"python contract test", targetPython, []string{"tests/conftest.py", "tests/test_todos.py"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, rel := range tc.files {
				write(t, dir, rel)
			}
			if got := hasGeneratedTests(dir, tc.target); got != tc.want {
				t.Errorf("hasGeneratedTests(%v, %q) = %v, want %v", tc.files, tc.target, got, tc.want)
			}
		})
	}
}

// streamOnlySpec has a STREAM endpoint and no REST endpoints, so the generated
// project has no test files to run.
const streamOnlySpec = `blueprint "stream-only" {
  version  "1.0.0"
  runtime  %s
  database postgres
}

secret JWT_SECRET required

@ "Stream pings"
STREAM /api/ping {
  auth jwt using(secret.JWT_SECRET)

  stream {
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`

// TestTestSkipsRunnerWhenNoTestsGenerated checks that bp test reports the skip
// and exits 0 for a spec that generates no tests, without invoking bun or uv.
func TestTestSkipsRunnerWhenNoTestsGenerated(t *testing.T) {
	t.Run("node", func(t *testing.T) {
		dir := t.TempDir()
		bp := filepath.Join(dir, "stream-only.bp")
		if err := os.WriteFile(bp, []byte(fmt.Sprintf(streamOnlySpec, "node")), 0644); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(dir, "out")
		stdout, stderr, code := runBP(t, "test", bp, "--out", outDir)
		if code != 0 {
			t.Fatalf("expected exit 0, got %d (stdout=%q stderr=%q)", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "No tests generated for") {
			t.Errorf("expected skip message on stdout, got: %q", stdout)
		}
		if strings.Contains(stdout, "Running tests in") {
			t.Errorf("runner should not have been invoked, stdout: %q", stdout)
		}
	})
	t.Run("python", func(t *testing.T) {
		dir := t.TempDir()
		bp := filepath.Join(dir, "stream-only.bp")
		if err := os.WriteFile(bp, []byte(fmt.Sprintf(streamOnlySpec, "python")), 0644); err != nil {
			t.Fatal(err)
		}
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
		outDir := filepath.Join(dir, "out")
		stdout, stderr, code := runBPEnv(t, env, "test", bp, "--target", "python", "--out", outDir)
		if code != 0 {
			t.Fatalf("expected exit 0, got %d (stdout=%q stderr=%q)", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "No tests generated for") {
			t.Errorf("expected skip message on stdout, got: %q", stdout)
		}
		if _, err := os.Stat(logFile); err == nil {
			t.Errorf("uv should not have been invoked")
		}
	})
}
