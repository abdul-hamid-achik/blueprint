package js

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// projectRoot walks up from the package directory to find go.mod.
func projectRoot() string {
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

// copyDir recursively copies all files from src to dst, creating directories as needed.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	}); err != nil {
		t.Fatalf("copy dir %s -> %s: %v", src, dst, err)
	}
}

func TestGoldenHelloWorld(t *testing.T) {
	root := projectRoot()
	src, err := os.ReadFile(filepath.Join(root, "examples", "hello-world.bp"))
	if err != nil {
		t.Fatal(err)
	}

	file, errs := parser.ParseFile("hello-world.bp", src)
	if len(errs) > 0 {
		t.Fatal(errs)
	}

	checkErrs := checker.Check(file)
	if len(checkErrs) > 0 {
		t.Fatal(checkErrs)
	}

	gen := New()
	outDir := t.TempDir()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatal(err)
	}

	goldenDir := filepath.Join("testdata", "golden", "hello-world")

	// If UPDATE_GOLDEN is set, overwrite golden files and return
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		t.Log("Updating golden files...")
		copyDir(t, outDir, goldenDir)
		return
	}

	// Compare each generated file against golden files
	generatedCount := 0
	if err := filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(outDir, path)
		goldenPath := filepath.Join(goldenDir, rel)

		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("failed to read generated file: %s: %v", rel, readErr)
			return nil
		}

		want, readErr := os.ReadFile(goldenPath)
		if readErr != nil {
			t.Errorf("missing golden file: %s (run UPDATE_GOLDEN=1 go test ./internal/codegen/js/ -run TestGoldenHelloWorld to create)", rel)
			return nil
		}

		// Normalize line endings for cross-platform comparison
		gotStr := strings.ReplaceAll(string(got), "\r\n", "\n")
		wantStr := strings.ReplaceAll(string(want), "\r\n", "\n")

		if gotStr != wantStr {
			t.Errorf("golden file mismatch: %s\nRun UPDATE_GOLDEN=1 go test ./internal/codegen/js/ -run TestGoldenHelloWorld to update\n\n--- want ---\n%s\n--- got ---\n%s", rel, wantStr, gotStr)
		}
		generatedCount++
		return nil
	}); err != nil {
		t.Fatalf("walk generated hello-world output: %v", err)
	}

	// Also check that there are no extra golden files that are no longer generated
	if err := filepath.Walk(goldenDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(goldenDir, path)
		generatedPath := filepath.Join(outDir, rel)
		if _, err := os.Stat(generatedPath); os.IsNotExist(err) {
			t.Errorf("golden file %s exists but was not generated (stale golden file)", rel)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk hello-world golden dir: %v", err)
	}

	if generatedCount == 0 {
		t.Error("no generated files were compared — something is wrong")
	}
}

func TestGoldenTodoAPI(t *testing.T) {
	root := projectRoot()
	src, err := os.ReadFile(filepath.Join(root, "examples", "todo-api.bp"))
	if err != nil {
		t.Fatal(err)
	}

	file, errs := parser.ParseFile("todo-api.bp", src)
	if len(errs) > 0 {
		t.Fatal(errs)
	}

	checkErrs := checker.Check(file)
	if len(checkErrs) > 0 {
		t.Fatal(checkErrs)
	}

	gen := New()
	outDir := t.TempDir()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatal(err)
	}

	goldenDir := filepath.Join("testdata", "golden", "todo-api")

	// If UPDATE_GOLDEN is set, overwrite golden files and return
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		t.Log("Updating golden files...")
		copyDir(t, outDir, goldenDir)
		return
	}

	// If golden dir does not exist, skip (only hello-world is mandatory)
	if _, err := os.Stat(goldenDir); os.IsNotExist(err) {
		t.Skip("golden files for todo-api not yet generated; run UPDATE_GOLDEN=1 go test ./internal/codegen/js/ -run TestGoldenTodoAPI")
	}

	if err := filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(outDir, path)
		goldenPath := filepath.Join(goldenDir, rel)

		got, _ := os.ReadFile(path)
		want, readErr := os.ReadFile(goldenPath)
		if readErr != nil {
			t.Errorf("missing golden file: %s", rel)
			return nil
		}

		gotStr := strings.ReplaceAll(string(got), "\r\n", "\n")
		wantStr := strings.ReplaceAll(string(want), "\r\n", "\n")

		if gotStr != wantStr {
			t.Errorf("golden file mismatch: %s\nRun UPDATE_GOLDEN=1 to update", rel)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk generated todo-api output: %v", err)
	}
}
