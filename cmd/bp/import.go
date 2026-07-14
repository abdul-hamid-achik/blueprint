package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/importer"
)

const (
	maxImportFileBytes  = 5 << 20
	maxImportTotalBytes = 25 << 20
)

// cmdImport creates an honest structural scaffold. It intentionally delegates
// no handler-body lifting to heuristics: every imported route ends in a loud
// TODO and 501 response, and stderr contains a per-handler loss report.
func cmdImport(inputPath, from, outPath, name string, force bool) int {
	switch strings.ToLower(strings.TrimSpace(from)) {
	case "ts", "typescript":
	case "":
		fmt.Fprintln(os.Stderr, "Error: --from is required (currently supported: ts)")
		return 1
	default:
		fmt.Fprintf(os.Stderr, "Error: unsupported import source %q (currently supported: ts)\n", from)
		return 1
	}

	sources, defaultName, err := collectTypeScriptSources(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Import error: %s\n", err)
		return 2
	}
	if name == "" {
		name = defaultName
	}
	result, err := importer.ImportTypeScript(sources, importer.Options{Name: name})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Import error: %s\n", err)
		return 1
	}

	if outPath == "" {
		fmt.Print(result.Source)
	} else {
		if info, statErr := os.Stat(outPath); statErr == nil {
			if info.IsDir() {
				fmt.Fprintf(os.Stderr, "Import error: --out %s is a directory\n", outPath)
				return 2
			}
			if !force {
				fmt.Fprintf(os.Stderr, "Import error: %s already exists (use --force to replace it)\n", outPath)
				return 1
			}
		} else if !os.IsNotExist(statErr) {
			fmt.Fprintf(os.Stderr, "Import error: cannot inspect %s: %s\n", outPath, statErr)
			return 2
		}
		parent := filepath.Dir(outPath)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Import error: cannot create %s: %s\n", parent, err)
			return 2
		}
		if err := writeImportOutput(outPath, []byte(result.Source)); err != nil {
			fmt.Fprintf(os.Stderr, "Import error: cannot write %s: %s\n", outPath, err)
			return 2
		}
		fmt.Printf("Created structural scaffold: %s\n", outPath)
	}

	printImportReport(result.Report)
	return 0
}

func writeImportOutput(path string, data []byte) error {
	parent := filepath.Dir(path)
	tmp, err := os.CreateTemp(parent, ".bp-import-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err == nil {
		return nil
	} else if _, statErr := os.Stat(path); statErr != nil {
		return err
	}
	// Windows does not replace an existing destination with os.Rename. Move the
	// prior file aside only after the complete temp file is durable, and restore
	// it if the final rename fails.
	backup := tmpPath + ".previous"
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Rename(backup, path)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func collectTypeScriptSources(inputPath string) ([]importer.Source, string, error) {
	info, err := os.Stat(inputPath)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read %s: %w", inputPath, err)
	}
	defaultName := filepath.Base(filepath.Clean(inputPath))
	if absolute, absErr := filepath.Abs(inputPath); absErr == nil {
		defaultName = filepath.Base(filepath.Clean(absolute))
	}
	if !info.IsDir() {
		if !isTypeScriptPath(inputPath) {
			return nil, "", fmt.Errorf("%s is not a TypeScript file (.ts, .tsx, .mts, or .cts)", inputPath)
		}
		defaultName = strings.TrimSuffix(defaultName, filepath.Ext(defaultName))
		data, err := readImportSource(inputPath)
		if err != nil {
			return nil, "", err
		}
		return []importer.Source{{Path: filepath.Clean(inputPath), Data: data}}, defaultName, nil
	}

	var paths []string
	err = filepath.WalkDir(inputPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != inputPath && ignoredImportDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !isTypeScriptPath(path) || strings.HasSuffix(strings.ToLower(path), ".d.ts") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("cannot scan %s: %w", inputPath, err)
	}
	if len(paths) == 0 {
		return nil, "", fmt.Errorf("no TypeScript source files found under %s", inputPath)
	}
	sort.Strings(paths)
	sources := make([]importer.Source, 0, len(paths))
	total := int64(0)
	for _, path := range paths {
		data, err := readImportSource(path)
		if err != nil {
			return nil, "", err
		}
		total += int64(len(data))
		if total > maxImportTotalBytes {
			return nil, "", fmt.Errorf("TypeScript input exceeds the %d MiB safety limit", maxImportTotalBytes>>20)
		}
		rel, relErr := filepath.Rel(inputPath, path)
		if relErr != nil {
			rel = path
		}
		sources = append(sources, importer.Source{Path: filepath.ToSlash(rel), Data: data})
	}
	return sources, defaultName, nil
}

func readImportSource(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot inspect %s: %w", path, err)
	}
	if info.Size() > maxImportFileBytes {
		return nil, fmt.Errorf("%s exceeds the %d MiB per-file safety limit", path, maxImportFileBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	return data, nil
}

func isTypeScriptPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	default:
		return false
	}
}

func ignoredImportDirectory(name string) bool {
	switch name {
	case ".git", ".next", ".turbo", "node_modules", "dist", "build", "coverage":
		return true
	default:
		return false
	}
}

func printImportReport(report importer.Report) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "IMPORTANT: this is a structural rewrite scaffold, not a behavior-preserving import.")
	fmt.Fprintln(os.Stderr, "Every handler body was dropped intentionally. Review each TODO and restore behavior manually.")
	fmt.Fprintln(os.Stderr)
	for _, handler := range report.Handlers {
		location := handler.Source
		if handler.Line > 0 {
			location = fmt.Sprintf("%s:%d", handler.Source, handler.Line)
		}
		if handler.SkippedReason != "" {
			fmt.Fprintf(os.Stderr, "  SKIPPED %-7s %-24s %s — %s\n", handler.Method, handler.Path, location, handler.SkippedReason)
			continue
		}
		if handler.DuplicateKey {
			fmt.Fprintf(os.Stderr, "  SKIPPED %-7s %-24s %s — duplicate route; handler dropped\n", handler.Method, handler.Path, location)
			continue
		}
		fmt.Fprintf(os.Stderr, "  TODO    %-7s %-24s %s\n", handler.Method, handler.Path, location)
		fmt.Fprintf(os.Stderr, "          mapped: %s; inputs: %d\n", strings.Join(handler.Mapped, ", "), handler.Inputs)
		fmt.Fprintf(os.Stderr, "          dropped: %s\n", strings.Join(handler.Dropped, ", "))
	}
	if len(report.Warnings) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Warnings:")
		for _, warning := range report.Warnings {
			fmt.Fprintf(os.Stderr, "  - %s\n", warning)
		}
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Scaffold summary: %d model(s), %d emitted route(s), %d skipped route(s), %d input(s), %d handler body/bodies requiring manual rewrite.\n",
		report.Models, report.Routes, report.SkippedRoutes, report.Inputs, len(report.Handlers))
}
