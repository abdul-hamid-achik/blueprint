package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// captureStderr runs fn and returns what it wrote to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	defer func() { os.Stderr = saved }()
	fn()
	w.Close()
	return <-done
}

func writeFmtSource(t *testing.T, src string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "service.bp")
	if err := os.WriteFile(filename, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	return filename
}

const formatterBugSource = "blueprint \"svc\" {\n  version \"0.1.0\"\n}\n\n# keep me\nenv LOG_LEVEL \"info\"\n"

// withBrokenPrinter makes the formatter emit output produced by render, as if
// the formatter had a defect, for the duration of the test.
func withBrokenPrinter(t *testing.T, render func(*ast.File) string) {
	t.Helper()
	saved := formatPrinter
	formatPrinter = render
	t.Cleanup(func() { formatPrinter = saved })
}

func TestFmtRefusesOutputThatChangesTheAST(t *testing.T) {
	withBrokenPrinter(t, func(*ast.File) string {
		return "blueprint \"svc\" {\n  version \"0.1.0\"\n}\n"
	})
	filename := writeFmtSource(t, formatterBugSource)

	for _, mode := range []struct {
		name         string
		write, check bool
	}{{"write", true, false}, {"check", false, true}, {"print", false, false}} {
		var code int
		stderr := captureStderr(t, func() {
			code = cmdFmt(filename, mode.write, mode.check)
		})
		if code != exitFormatterBug {
			t.Errorf("%s: exit code = %d, want %d", mode.name, code, exitFormatterBug)
		}
		if !strings.Contains(stderr, "bug in the formatter") || !strings.Contains(stderr, "issues") {
			t.Errorf("%s: stderr does not ask for a bug report:\n%s", mode.name, stderr)
		}
		if !strings.Contains(stderr, "syntax tree") {
			t.Errorf("%s: stderr does not say the syntax tree changed:\n%s", mode.name, stderr)
		}
	}

	got, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != formatterBugSource {
		t.Errorf("source file was modified:\n%s", got)
	}
}

func TestFmtRefusesOutputThatDoesNotParse(t *testing.T) {
	withBrokenPrinter(t, func(*ast.File) string {
		return "blueprint \"svc\" {\n"
	})
	filename := writeFmtSource(t, formatterBugSource)

	var code int
	stderr := captureStderr(t, func() {
		code = cmdFmt(filename, true, false)
	})
	if code != exitFormatterBug {
		t.Fatalf("exit code = %d, want %d", code, exitFormatterBug)
	}
	if !strings.Contains(stderr, "does not parse") {
		t.Errorf("stderr does not say the output failed to parse:\n%s", stderr)
	}
	got, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != formatterBugSource {
		t.Errorf("source file was modified:\n%s", got)
	}
}

func TestFmtAcceptsVerifiedOutput(t *testing.T) {
	filename := writeFmtSource(t, formatterBugSource)
	var code int
	stderr := captureStderr(t, func() {
		code = cmdFmt(filename, true, false)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "bug in the formatter") {
		t.Errorf("verified output was refused:\n%s", stderr)
	}
}
