package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// bpSkipDirs are directory names never descended into when a directory
// argument is expanded. Hidden directories (name starting with ".") are
// skipped too; see bpSkipDir.
var bpSkipDirs = map[string]bool{
	"node_modules": true,
	"generated":    true,
	"dist":         true,
	".blueprint":   true,
}

// bpSkipDir reports whether a directory encountered below an expanded root
// must be skipped.
func bpSkipDir(name string) bool {
	return bpSkipDirs[name] || strings.HasPrefix(name, ".")
}

// walkBPDir returns the *.bp files under root, recursively, in sorted order.
// root itself is never skipped, even when its name would match bpSkipDir
// (`bp check .` or `bp check .hidden/` must still work).
func walkBPDir(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && bpSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".bp") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// bpIncludeTargets returns the absolute paths of the files that path pulls in
// with `include` statements. Include paths resolve relative to the including
// file's directory, as in resolveIncludesRecursive. Unreadable files and
// files with parse errors contribute nothing: their own check reports the
// problem.
func bpIncludeTargets(path string) []string {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	file, _ := parser.ParsePartialFile(path, src)
	if file == nil {
		return nil
	}
	dir := filepath.Dir(path)
	var targets []string
	for _, block := range file.Blocks {
		inc, ok := block.(*ast.Include)
		if !ok {
			continue
		}
		if abs, err := filepath.Abs(filepath.Join(dir, inc.Path)); err == nil {
			targets = append(targets, abs)
		}
	}
	return targets
}

// resolveBPArgs turns the positional arguments of `bp check`, `bp lint` and
// `bp fmt` into the files to process.
//
// A single argument that is not a directory is returned unchanged with
// multi=false, so single-file invocations keep their existing output and
// read-error handling byte for byte. Otherwise multi is true and:
//   - a directory expands recursively to its *.bp files (walkBPDir), skipping
//     node_modules, generated, dist, .blueprint and dot-directories;
//   - a directory file that is included, directly or through other includes,
//     from a candidate that no other candidate includes is skipped, since
//     include fragments are not standalone. Explicitly named files are always
//     kept, even when included elsewhere. Files only reachable through an
//     include cycle are kept, so the cycle's error is reported;
//   - a missing path, or a directory with no .bp files, is an error;
//   - duplicates are dropped, keeping the first occurrence.
func resolveBPArgs(args []string) (files []string, multi bool, err error) {
	if len(args) == 1 {
		if info, statErr := os.Stat(args[0]); statErr != nil || !info.IsDir() {
			return args, false, nil
		}
	}

	type expansion struct {
		arg      string
		explicit bool
		files    []string // directory-discovered files, when !explicit
	}
	var parts []expansion
	for _, arg := range args {
		info, statErr := os.Stat(arg)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return nil, true, fmt.Errorf("no such file or directory: %s", arg)
			}
			return nil, true, statErr
		}
		if !info.IsDir() {
			parts = append(parts, expansion{arg: arg, explicit: true})
			continue
		}
		discovered, walkErr := walkBPDir(arg)
		if walkErr != nil {
			return nil, true, walkErr
		}
		if len(discovered) == 0 {
			return nil, true, fmt.Errorf("no .bp files found in directory: %s", arg)
		}
		parts = append(parts, expansion{arg: arg, files: discovered})
	}

	// Includes are edges between candidate files (explicit or discovered). A
	// discovered file is a fragment when it is reachable through includes from
	// a candidate that no other candidate includes. A file only reachable
	// through an include cycle is not a fragment, so the cycle is still checked
	// and its circular-include error is reported instead of passing silently.
	var candidates []string
	explicitAbs := map[string]bool{}
	for _, p := range parts {
		if p.explicit {
			candidates = append(candidates, p.arg)
			if abs, absErr := filepath.Abs(p.arg); absErr == nil {
				explicitAbs[abs] = true
			}
		} else {
			candidates = append(candidates, p.files...)
		}
	}
	absOf := func(path string) string {
		if abs, absErr := filepath.Abs(path); absErr == nil {
			return abs
		}
		return filepath.Clean(path)
	}
	edges := map[string][]string{}
	includedBy := map[string]bool{}
	for _, c := range candidates {
		targets := bpIncludeTargets(c)
		edges[absOf(c)] = targets
		for _, t := range targets {
			includedBy[t] = true
		}
	}
	reached := map[string]bool{}
	var queue []string
	for _, c := range candidates {
		if abs := absOf(c); !includedBy[abs] && !reached[abs] {
			reached[abs] = true
			queue = append(queue, abs)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, t := range edges[cur] {
			if !reached[t] {
				reached[t] = true
				queue = append(queue, t)
			}
		}
	}
	skipFragment := func(path string) bool {
		abs := absOf(path)
		return !explicitAbs[abs] && includedBy[abs] && reached[abs]
	}

	seen := map[string]bool{}
	add := func(path string) {
		key, absErr := filepath.Abs(path)
		if absErr != nil {
			key = filepath.Clean(path)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		files = append(files, path)
	}
	for _, p := range parts {
		if p.explicit {
			add(p.arg)
			continue
		}
		for _, f := range p.files {
			if skipFragment(f) {
				continue
			}
			add(f)
		}
	}
	return files, true, nil
}

// bpFileRunner processes one file for a multi-file command. In JSON mode the
// per-file object goes to stdout (as in the single-file form) and nothing else
// is written. Otherwise the runner writes its normal text output. The return
// value is the file's exit code.
type bpFileRunner func(file string, jsonOutput bool, stdout, stderr io.Writer) int

// lintJSONIssue is one entry of the `bp lint --json` issues array. Parse,
// include and semantic failures are reported as issues too, with rule set to
// "parse", "include" or "semantic". line/column are 0 when unknown.
type lintJSONIssue struct {
	File     string `json:"file"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
	Hint     string `json:"hint"`
}

// lintJSONResult is the single-file `bp lint --json` object.
type lintJSONResult struct {
	Success      bool            `json:"success"`
	Filename     string          `json:"filename"`
	IssueCount   int             `json:"issue_count"`
	ErrorCount   int             `json:"error_count"`
	WarningCount int             `json:"warning_count"`
	Issues       []lintJSONIssue `json:"issues"`
}

// printLintJSON fills in the counts, writes result as JSON, and returns the
// exit code for the file: 1 when any issue has error severity, else 0.
func printLintJSON(w io.Writer, result lintJSONResult) int {
	result.IssueCount = len(result.Issues)
	for _, issue := range result.Issues {
		switch issue.Severity {
		case "error":
			result.ErrorCount++
		case "warning":
			result.WarningCount++
		}
	}
	result.Success = result.ErrorCount == 0
	printJSONTo(w, result)
	if result.ErrorCount > 0 {
		return 1
	}
	return 0
}

// runBPFiles runs run over every file, never stopping at the first failure. It
// returns the most severe exit code (the numerically largest, per the exit
// code table in docs/cli-reference.md). A one-line summary goes to stderr.
//
// In JSON mode it prints one document:
//
//	{"success": bool, "file_count": n, "failed_count": n, "files": [per-file objects]}
//
// Each per-file object is the single-file --json object. It gets a "filename"
// key when it does not already have one, so read errors can be attributed.
func runBPFiles(files []string, jsonOutput bool, verb string, run bpFileRunner) int {
	worst := 0
	failed := 0
	results := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		var code int
		if jsonOutput {
			var buf bytes.Buffer
			code = run(f, true, &buf, io.Discard)
			obj := map[string]interface{}{}
			if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
				obj = map[string]interface{}{
					"success": false,
					"error":   map[string]string{"type": "internal_error", "message": err.Error()},
				}
			}
			if _, ok := obj["filename"]; !ok {
				obj["filename"] = f
			}
			results = append(results, obj)
		} else {
			code = run(f, false, os.Stdout, os.Stderr)
		}
		if code != 0 {
			failed++
		}
		if code > worst {
			worst = code
		}
	}

	if jsonOutput {
		printJSON(map[string]interface{}{
			"success":      failed == 0,
			"file_count":   len(files),
			"failed_count": failed,
			"files":        results,
		})
	}
	fmt.Fprintf(os.Stderr, "%d files %s, %d failed\n", len(files), verb, failed)
	return worst
}
