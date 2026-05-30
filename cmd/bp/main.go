package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/agentctx"
	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/js"
	pythongen "github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/diag"
	"github.com/abdul-hamid-achik/blueprint/internal/docs"
	"github.com/abdul-hamid-achik/blueprint/internal/generate"
	"github.com/abdul-hamid-achik/blueprint/internal/linter"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// version is set by goreleaser ldflags at build time. Between releases this
// stays at the next planned version with a `-dev` suffix so `bp version`
// makes it obvious the binary was built from source rather than a release.
var version = "0.10.0"

// Supported codegen targets. New targets register here and add a case to
// dispatchTarget below. The flag default is targetNode, so existing usage is
// unchanged.
const (
	targetNode   = "node"
	targetPython = "python"
)

// resolveTarget validates a --target flag value and returns the canonical name.
// An empty string defaults to targetNode so callers that don't set the flag
// keep their current behavior.
func resolveTarget(t string) (string, error) {
	switch t {
	case "", targetNode:
		return targetNode, nil
	case targetPython:
		return targetPython, nil
	default:
		return "", fmt.Errorf("unknown --target %q (supported: %s, %s)", t, targetNode, targetPython)
	}
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "check":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("check", "check <file.bp> [--json]",
				"Validate a .bp file for syntax and semantic errors.",
				[][2]string{
					{"--json", "Output in JSON format (for CI)"},
				})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp check <file.bp> [--json]")
			os.Exit(1)
		}
		jsonOutput := false
		for _, arg := range os.Args[3:] {
			if arg == "--json" {
				jsonOutput = true
			}
		}
		os.Exit(cmdCheck(os.Args[2], jsonOutput))
	case "build":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("build", "build <file.bp> [--out <dir>] [--target <name>] [--react-query] [--frontend-only] [--gen-tests]",
				"Compile a .bp file to a runnable project.",
				[][2]string{
					{"--out <dir>", "Output directory (default: generated/)"},
					{"--target <name>", "Codegen target: node (default) or python (FastAPI + SQLAlchemy + Alembic)"},
					{"--react-query", "Generate React Query hooks and add frontend deps (node target)"},
					{"--frontend-only", "Emit only the standalone frontend package (node target)"},
					{"--gen-tests", "Generate contract tests. node: PGlite-backed Vitest. python: testcontainers-backed pytest (Docker required)"},
				})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp build <file.bp> [--out <dir>] [--target <name>] [--react-query] [--frontend-only] [--gen-tests]")
			os.Exit(1)
		}
		outDir := "generated"
		target := targetNode
		reactQuery := false
		frontendOnly := false
		genTests := false
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--out":
				if i+1 < len(os.Args) {
					outDir = os.Args[i+1]
					i++
				}
			case "--target":
				if i+1 < len(os.Args) {
					target = os.Args[i+1]
					i++
				}
			case "--react-query":
				reactQuery = true
			case "--frontend-only":
				frontendOnly = true
			case "--gen-tests":
				genTests = true
			}
		}
		os.Exit(cmdBuild(os.Args[2], outDir, target, reactQuery, frontendOnly, genTests))
	case "frontend":
		if len(os.Args) >= 3 && os.Args[2] == "publish" {
			if hasHelpFlag(os.Args[3:]) {
				printCommandHelp("frontend publish", "frontend publish <file.bp> [--out <dir>] [--react-query] [--skip-install]",
					"Generate the standalone frontend SDK package, install dependencies, build it, and run `bun pm pack --dry-run`.",
					[][2]string{{"--out <dir>", "Output directory (default: generated/)"}, {"--react-query", "Generate React Query hooks and add frontend deps"}, {"--skip-install", "Skip `bun install` before build and pack dry-run"}})
				os.Exit(0)
			}
			if len(os.Args) < 4 {
				fmt.Fprintln(os.Stderr, "Usage: bp frontend publish <file.bp> [--out <dir>] [--react-query] [--skip-install]")
				os.Exit(1)
			}
			outDir := "generated"
			reactQuery := false
			skipInstall := false
			for i := 4; i < len(os.Args); i++ {
				if os.Args[i] == "--out" && i+1 < len(os.Args) {
					outDir = os.Args[i+1]
					i++
				} else if os.Args[i] == "--react-query" {
					reactQuery = true
				} else if os.Args[i] == "--skip-install" {
					skipInstall = true
				}
			}
			os.Exit(cmdFrontendPublish(os.Args[3], outDir, reactQuery, skipInstall))
		}
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("frontend", "frontend <file.bp> [--out <dir>] [--react-query]",
				"Generate only the standalone frontend SDK package.\nUse `bp frontend publish` to build and dry-run the package flow.",
				[][2]string{{"--out <dir>", "Output directory (default: generated/)"}, {"--react-query", "Generate React Query hooks and add frontend deps"}})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp frontend <file.bp> [--out <dir>] [--react-query]")
			os.Exit(1)
		}
		outDir := "generated"
		reactQuery := false
		for i := 3; i < len(os.Args); i++ {
			if os.Args[i] == "--out" && i+1 < len(os.Args) {
				outDir = os.Args[i+1]
				i++
			} else if os.Args[i] == "--react-query" {
				reactQuery = true
			}
		}
		os.Exit(cmdBuild(os.Args[2], outDir, targetNode, reactQuery, true, false))
	case "fmt":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("fmt", "fmt <file.bp> [--write] [--check]",
				"Format a .bp file. Prints formatted output to stdout by default.",
				[][2]string{
					{"--write", "Write formatted output back to the file"},
					{"--check", "Check if file is formatted; exit 1 if not (for CI)"},
				})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp fmt <file.bp> [--write] [--check]")
			os.Exit(1)
		}
		write := false
		check := false
		for _, arg := range os.Args[3:] {
			if arg == "--write" {
				write = true
			}
			if arg == "--check" {
				check = true
			}
		}
		os.Exit(cmdFmt(os.Args[2], write, check))
	case "lint":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("lint", "lint <file.bp>",
				"Lint a .bp file for best practice violations.",
				nil)
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp lint <file.bp>")
			os.Exit(1)
		}
		os.Exit(cmdLint(os.Args[2]))
	case "docs":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("docs", "docs <file.bp> [--out <file.json>]",
				"Generate an OpenAPI 3.1 JSON specification from a .bp file.",
				[][2]string{{"--out <file>", "Write to file instead of stdout"}})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp docs <file.bp> [--out file.json]")
			os.Exit(1)
		}
		outFile := ""
		for i := 3; i < len(os.Args); i++ {
			if os.Args[i] == "--out" && i+1 < len(os.Args) {
				outFile = os.Args[i+1]
				i++
			}
		}
		os.Exit(cmdDocs(os.Args[2], outFile))
	case "test":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("test", "test <file.bp> [--out <dir>]",
				"Build and run the Vitest test suite.",
				[][2]string{{"--out <dir>", "Output directory (default: generated/)"}})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp test <file.bp> [--out <dir>]")
			os.Exit(1)
		}
		outDir := "generated"
		for i := 3; i < len(os.Args); i++ {
			if os.Args[i] == "--out" && i+1 < len(os.Args) {
				outDir = os.Args[i+1]
				i++
			}
		}
		os.Exit(cmdTest(os.Args[2], outDir))
	case "migrate":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("migrate", "migrate <file.bp> [generate|push|studio] [--out <dir>] [--target <name>]",
				"Build and run database migrations.\nnode (default): Drizzle Kit. python: Alembic via uv.",
				[][2]string{
					{"--out <dir>", "Output directory (default: generated/)"},
					{"--target <name>", "Codegen target: node (default, drizzle-kit) or python (alembic)"},
				})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp migrate <file.bp> [generate|push|studio] [--out <dir>] [--target <name>]")
			os.Exit(1)
		}
		outDir := "generated"
		subCmd := "generate"
		target := targetNode
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "generate", "push", "studio", "check":
				subCmd = os.Args[i]
			case "--out":
				if i+1 < len(os.Args) {
					outDir = os.Args[i+1]
					i++
				}
			case "--target":
				if i+1 < len(os.Args) {
					target = os.Args[i+1]
					i++
				}
			}
		}
		os.Exit(cmdMigrate(os.Args[2], outDir, subCmd, target))
	case "generate":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("generate", "generate <file.bp> [--write]",
				"Resolve @> LLM generation slots using the Anthropic API.\nRequires ANTHROPIC_API_KEY environment variable.",
				[][2]string{{"--write", "Write resolved code back to the file"}})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp generate <file.bp> [--write]")
			os.Exit(1)
		}
		write := false
		for _, arg := range os.Args[3:] {
			if arg == "--write" {
				write = true
			}
		}
		os.Exit(cmdGenerate(os.Args[2], write))
	case "init":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("init", "init [name]",
				"Scaffold a new Blueprint project with a starter .bp file.\nIf name is omitted, uses the current directory name.",
				nil)
			os.Exit(0)
		}
		name := ""
		if len(os.Args) >= 3 {
			name = os.Args[2]
		}
		os.Exit(cmdInit(name))
	case "run":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("run", "run <file.bp> [--out <dir>]",
				"Build and start the server. Runs bun install if needed.",
				[][2]string{{"--out <dir>", "Output directory (default: generated/)"}})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp run <file.bp> [--out <dir>]")
			os.Exit(1)
		}
		outDir := "generated"
		for i := 3; i < len(os.Args); i++ {
			if os.Args[i] == "--out" && i+1 < len(os.Args) {
				outDir = os.Args[i+1]
				i++
			}
		}
		os.Exit(cmdRun(os.Args[2], outDir))
	case "dev":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("dev", "dev <file.bp> [--out <dir>]",
				"Watch mode -- rebuild and restart the server on file changes.",
				[][2]string{{"--out <dir>", "Output directory (default: generated/)"}})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp dev <file.bp> [--out <dir>]")
			os.Exit(1)
		}
		outDir := "generated"
		for i := 3; i < len(os.Args); i++ {
			if os.Args[i] == "--out" && i+1 < len(os.Args) {
				outDir = os.Args[i+1]
				i++
			}
		}
		os.Exit(cmdDev(os.Args[2], outDir))
	case "eject":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("eject", "eject <dir>",
				"Remove Blueprint markers from generated code, making it fully yours.\nThis removes 'Generated by' headers and source references.",
				nil)
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp eject <dir>")
			os.Exit(1)
		}
		os.Exit(cmdEject(os.Args[2]))
	case "diff":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("diff", "diff <file.bp> [--out <dir>] [--react-query] [--frontend-only] [--gen-tests] [--apply] [--exit-code] [--no-color]",
				"Show what changes bp build would make, without overwriting.",
				[][2]string{
					{"--out <dir>", "Output directory (default: generated/)"},
					{"--react-query", "Compare output as if build ran with React Query hooks enabled"},
					{"--frontend-only", "Compare output as if build emitted only the standalone frontend package"},
					{"--gen-tests", "Compare output as if build emitted the auto-generated test harness"},
					{"--apply", "Write the changes (equivalent to bp build) after showing the diff"},
					{"--exit-code", "Exit 1 if there are any changes (for CI)"},
					{"--no-color", "Disable ANSI color in diff output"},
				})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp diff <file.bp> [--out <dir>] [--react-query] [--frontend-only] [--gen-tests] [--apply] [--exit-code] [--no-color]")
			os.Exit(1)
		}
		outDir := "generated"
		target := targetNode
		reactQuery := false
		frontendOnly := false
		genTests := false
		apply := false
		exitOnDiff := false
		noColor := false
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--out":
				if i+1 < len(os.Args) {
					outDir = os.Args[i+1]
					i++
				}
			case "--target":
				if i+1 < len(os.Args) {
					target = os.Args[i+1]
					i++
				}
			case "--react-query":
				reactQuery = true
			case "--frontend-only":
				frontendOnly = true
			case "--gen-tests":
				genTests = true
			case "--apply":
				apply = true
			case "--exit-code":
				exitOnDiff = true
			case "--no-color":
				noColor = true
			}
		}
		os.Exit(cmdDiff(os.Args[2], outDir, target, reactQuery, frontendOnly, genTests, apply, exitOnDiff, noColor))
	case "deploy":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("deploy", "deploy <file.bp> [--out <dir>] [--tag <tag>] [--target <name>] [--no-run]",
				"Build and run a Docker container from your Blueprint.",
				[][2]string{
					{"--out <dir>", "Output directory (default: generated/)"},
					{"--tag <tag>", "Docker image tag (default: blueprint-app:latest)"},
					{"--target <name>", "Deploy target: docker (default). fly is reserved for v0.11."},
					{"--no-run", "Skip the smoke-test docker run after build"},
				})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp deploy <file.bp> [--out <dir>] [--tag <tag>] [--target <name>] [--no-run]")
			os.Exit(1)
		}
		outDir := "generated"
		tag := "blueprint-app:latest"
		deployTarget := "docker"
		noRun := false
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "--out":
				if i+1 < len(os.Args) {
					outDir = os.Args[i+1]
					i++
				}
			case "--tag":
				if i+1 < len(os.Args) {
					tag = os.Args[i+1]
					i++
				}
			case "--target":
				if i+1 < len(os.Args) {
					deployTarget = os.Args[i+1]
					i++
				}
			case "--no-run":
				noRun = true
			}
		}
		os.Exit(cmdDeploy(os.Args[2], outDir, tag, deployTarget, noRun))
	case "version", "--version", "-v":
		fmt.Printf("bp version %s\n", version)
	case "completion":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("completion", "completion <shell>",
				"Generate shell completion script for bash, zsh, or fish.",
				nil)
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp completion <bash|zsh|fish>")
			os.Exit(1)
		}
		os.Exit(cmdCompletion(os.Args[2]))
	case "stats":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("stats", "stats <file.bp> [--json]",
				"Show code statistics for a Blueprint file.",
				[][2]string{{"--json", "Output in JSON format"}})
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp stats <file.bp> [--json]")
			os.Exit(1)
		}
		jsonOutput := false
		for _, arg := range os.Args[3:] {
			if arg == "--json" {
				jsonOutput = true
			}
		}
		os.Exit(cmdStats(os.Args[2], jsonOutput))
	case "doctor":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("doctor", "doctor",
				"Check your environment for Blueprint dependencies.",
				nil)
			os.Exit(0)
		}
		os.Exit(cmdDoctor())
	case "lsp":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("lsp", "lsp",
				"Start the Language Server Protocol server.",
				nil)
			os.Exit(0)
		}
		os.Exit(cmdLSP())
	case "explain":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("explain", "explain <code>",
				"Print the documentation for a Blueprint error code (e.g. C001).",
				nil)
			os.Exit(0)
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp explain <code>")
			os.Exit(1)
		}
		os.Exit(cmdExplain(os.Args[2]))
	case "context":
		if hasHelpFlag(os.Args[2:]) {
			printCommandHelp("context", "context [topic] [--format md|json]",
				"Print the agent-facing language + CLI surface. With no topic, prints the full surface (version, command list, target list, topic index). With a topic, prints the focused docs for that topic. Available topics: "+strings.Join(agentctx.Topics(), ", ")+".",
				nil)
			os.Exit(0)
		}
		topic := ""
		format := "md"
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--format":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "Error: --format needs a value (md or json)")
					os.Exit(1)
				}
				format = args[i+1]
				i++
			default:
				if strings.HasPrefix(args[i], "-") {
					fmt.Fprintf(os.Stderr, "Error: unknown flag %s\n", args[i])
					os.Exit(1)
				}
				if topic != "" {
					fmt.Fprintln(os.Stderr, "Error: at most one topic argument is supported")
					os.Exit(1)
				}
				topic = args[i]
			}
		}
		os.Exit(cmdContext(topic, format))
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		// Check for typo and suggest correction
		if suggestion := suggestCommand(os.Args[1]); suggestion != "" {
			fmt.Fprintf(os.Stderr, "\nDid you mean: %s?\n", suggestion)
		}
		printUsage()
		os.Exit(1)
	}
}

func cmdCheck(filename string, jsonOutput bool) int {
	src, err := os.ReadFile(filename)
	if err != nil {
		if jsonOutput {
			printJSONError("read_error", err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)

	if len(parseErrors) > 0 {
		if jsonOutput {
			printJSONCheckResult(filename, false, parseErrors, nil)
		} else {
			for _, e := range parseErrors {
				fmt.Fprintln(os.Stderr, parser.FormatError(e, src))
			}
			fmt.Fprintf(os.Stderr, "\n%d syntax error(s) found\n", len(parseErrors))
		}
		return 1
	}

	// Resolve include statements — merge blocks from included files
	if errs := resolveIncludes(file, filename); len(errs) > 0 {
		if jsonOutput {
			printJSONError("include_error", errs[0])
		} else {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, e)
			}
		}
		return 1
	}

	// Semantic checking
	checkErrors := checker.Check(file)

	if len(checkErrors) > 0 {
		if jsonOutput {
			printJSONCheckResult(filename, false, nil, checkErrors)
		} else {
			for _, e := range checkErrors {
				fmt.Fprintln(os.Stderr, checker.FormatCheckError(e, src))
			}
			fmt.Fprintf(os.Stderr, "\n%d semantic error(s) found\n", len(checkErrors))
		}
		return 1
	}

	if jsonOutput {
		printJSONCheckResult(filename, true, nil, nil)
	} else {
		fmt.Printf("OK: %s\n", filename)
	}
	return 0
}

func cmdBuild(filename, outDir, target string, reactQuery, frontendOnly, genTests bool) int {
	return cmdBuildWithOptions(filename, outDir, target, reactQuery, frontendOnly, false, genTests)
}

func cmdBuildWithOptions(filename, outDir, target string, reactQuery, frontendOnly, preserveNodeModules, genTests bool) int {
	// Validate target before any work — keeps errors visible and short.
	canonical, err := resolveTarget(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	target = canonical
	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, parser.FormatError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d syntax error(s) found\n", len(parseErrors))
		return 1
	}

	// Resolve include statements — merge blocks from included files
	if errs := resolveIncludes(file, filename); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		return 1
	}

	checkErrors := checker.Check(file)
	if len(checkErrors) > 0 {
		for _, e := range checkErrors {
			fmt.Fprintln(os.Stderr, checker.FormatCheckError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d semantic error(s) found\n", len(checkErrors))
		return 1
	}

	_ = preserveNodeModules // Builds are manifest-based and no longer remove node_modules.

	switch target {
	case targetNode:
		gen := js.New().WithReactQuery(reactQuery).WithFrontendOnly(frontendOnly).WithGenTests(genTests)
		if err := gen.Generate(file, outDir); err != nil {
			fmt.Fprintf(os.Stderr, "Codegen error: %s\n", err)
			return 4
		}
	case targetPython:
		// Frontend-specific flags are still Node-only. --gen-tests is wired
		// through in Phase 4 (testcontainers-backed pytest harness).
		if reactQuery || frontendOnly {
			fmt.Fprintf(os.Stderr, "Error: --react-query and --frontend-only are not supported with --target python yet\n")
			return 2
		}
		if err := pythongen.New().WithGenTests(genTests).Generate(file, outDir); err != nil {
			fmt.Fprintf(os.Stderr, "Codegen error: %s\n", err)
			return 4
		}
	default:
		// resolveTarget already filtered unknowns; this is unreachable.
		fmt.Fprintf(os.Stderr, "Error: target %q has no generator\n", target)
		return 2
	}

	fmt.Printf("Built %s -> %s/ (target=%s)\n", filename, outDir, target)
	return 0
}

func cmdFrontendPublish(filename, outDir string, reactQuery, skipInstall bool) int {
	if code := cmdBuildWithOptions(filename, outDir, targetNode, reactQuery, true, skipInstall, false); code != 0 {
		return code
	}

	if skipInstall {
		nodeModulesPath := filepath.Join(outDir, "node_modules")
		if _, err := os.Stat(nodeModulesPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: --skip-install requires existing dependencies in %s. Run without --skip-install once first.\n", outDir)
			return 2
		}
	}

	steps := []struct {
		label string
		args  []string
	}{}
	if !skipInstall {
		steps = append(steps, struct {
			label string
			args  []string
		}{label: "Installing frontend package dependencies", args: []string{"install"}})
	}
	steps = append(steps,
		struct {
			label string
			args  []string
		}{label: "Building frontend package", args: []string{"run", "build"}},
		struct {
			label string
			args  []string
		}{label: "Running bun pack dry run", args: []string{"pm", "pack", "--dry-run"}},
	)

	for _, step := range steps {
		fmt.Printf("%s in %s...\n", step.label, outDir)
		cmd := exec.Command("bun", step.args...)
		cmd.Dir = outDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "%s failed: %s\n", step.label, err)
			return 2
		}
	}
	if skipInstall {
		fmt.Printf("Skipped bun install in %s.\n", outDir)
	}

	fmt.Printf("Frontend package in %s is ready for publish review.\n", outDir)
	return 0
}

// resolveIncludes processes include statements in the parsed file, reading and
// parsing included files and merging their blocks into the main file's AST.
// It detects circular includes and returns errors for any issues found.
func resolveIncludes(file *ast.File, mainFilename string) []string {
	baseDir := filepath.Dir(mainFilename)
	seen := map[string]bool{mainFilename: true}
	return resolveIncludesRecursive(file, baseDir, seen)
}

func resolveIncludesRecursive(file *ast.File, baseDir string, seen map[string]bool) []string {
	var errors []string
	var newBlocks []ast.TopLevel

	for _, block := range file.Blocks {
		inc, ok := block.(*ast.Include)
		if !ok {
			newBlocks = append(newBlocks, block)
			continue
		}

		// Resolve include path relative to the current file's directory
		incPath := filepath.Join(baseDir, inc.Path)

		// Check for circular includes
		absPath, err := filepath.Abs(incPath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: cannot resolve include path %q: %s", inc.Location(), inc.Path, err))
			continue
		}
		if seen[absPath] {
			errors = append(errors, fmt.Sprintf("%s: circular include detected: %q", inc.Location(), inc.Path))
			continue
		}
		seen[absPath] = true

		// Read and parse the included file
		src, err := os.ReadFile(incPath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: cannot read include %q: %s", inc.Location(), inc.Path, err))
			continue
		}

		incFile, parseErrors := parser.ParsePartialFile(incPath, src)
		if len(parseErrors) > 0 {
			for _, e := range parseErrors {
				errors = append(errors, parser.FormatError(e, src))
			}
			errors = append(errors, fmt.Sprintf("%d error(s) in included file %q", len(parseErrors), inc.Path))
			continue
		}

		// Recursively resolve includes in the included file
		incBaseDir := filepath.Dir(incPath)
		if errs := resolveIncludesRecursive(incFile, incBaseDir, seen); len(errs) > 0 {
			errors = append(errors, errs...)
			continue
		}

		// Merge blocks from included file (skip its blueprint block — only one allowed)
		for _, b := range incFile.Blocks {
			if _, isBP := b.(*ast.Blueprint); isBP {
				continue // skip duplicate blueprint blocks from includes
			}
			newBlocks = append(newBlocks, b)
		}
	}

	file.Blocks = newBlocks
	return errors
}

func cmdFmt(filename string, write, check bool) int {
	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, parser.FormatError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d syntax error(s) found\n", len(parseErrors))
		return 1
	}

	formatted := ast.Print(file)

	if check {
		if formatted != string(src) {
			fmt.Println(filename)
			return 1
		}
		return 0
	}

	if write {
		if err := os.WriteFile(filename, []byte(formatted), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
			return 2
		}
		fmt.Printf("Formatted: %s\n", filename)
	} else {
		fmt.Print(formatted)
	}
	return 0
}

func cmdLint(filename string) int {
	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, parser.FormatError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d syntax error(s) found\n", len(parseErrors))
		return 1
	}

	if errs := resolveIncludes(file, filename); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		return 1
	}

	checkErrors := checker.Check(file)
	if len(checkErrors) > 0 {
		for _, e := range checkErrors {
			fmt.Fprintln(os.Stderr, checker.FormatCheckError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d semantic error(s) found\n", len(checkErrors))
		return 1
	}

	issues := linter.Lint(file)

	if len(issues) == 0 {
		fmt.Printf("OK: %s — no lint issues\n", filename)
		return 0
	}

	errorCount := 0
	warningCount := 0
	for _, issue := range issues {
		fmt.Println(issue.String())
		if issue.Hint != "" {
			fmt.Printf("  hint: %s\n", issue.Hint)
		}
		switch issue.Level {
		case "error":
			errorCount++
		case "warning":
			warningCount++
		}
	}

	fmt.Printf("\n%d issue(s): %d error(s), %d warning(s)\n", len(issues), errorCount, warningCount)

	if errorCount > 0 {
		return 1
	}
	return 0
}

func cmdDocs(filename, outFile string) int {
	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, parser.FormatError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d syntax error(s) found\n", len(parseErrors))
		return 1
	}

	if errs := resolveIncludes(file, filename); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		return 1
	}

	checkErrors := checker.Check(file)
	if len(checkErrors) > 0 {
		for _, e := range checkErrors {
			fmt.Fprintln(os.Stderr, checker.FormatCheckError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d semantic error(s) found\n", len(checkErrors))
		return 1
	}

	jsonData, err := docs.GenerateOpenAPI(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Docs error: %s\n", err)
		return 4
	}

	if outFile != "" {
		if err := os.WriteFile(outFile, jsonData, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
			return 2
		}
		fmt.Printf("Docs written to %s\n", outFile)
	} else {
		fmt.Println(string(jsonData))
	}
	return 0
}

func cmdDev(filename, outDir string) int {
	// Initial build
	fmt.Printf("Building %s...\n", filename)
	if code := cmdBuild(filename, outDir, targetNode, false, false, false); code != 0 {
		return code
	}

	// Start the node process
	proc := startNodeProcess(outDir)

	// Track file modification time for change detection
	info, err := os.Stat(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	lastMod := info.ModTime()

	// Handle SIGINT/SIGTERM for clean shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("Watching %s for changes (Ctrl+C to stop)...\n", filename)

	for {
		select {
		case <-sigCh:
			fmt.Println("\nShutting down...")
			if proc != nil {
				_ = proc.Process.Signal(syscall.SIGTERM)
				_ = proc.Wait()
			}
			return 0
		case <-ticker.C:
			info, err := os.Stat(filename)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastMod) {
				lastMod = info.ModTime()
				fmt.Printf("\nFile changed — rebuilding...\n")

				if proc != nil {
					_ = proc.Process.Signal(syscall.SIGTERM)
					_ = proc.Wait()
					proc = nil
				}

				if code := cmdBuild(filename, outDir, targetNode, false, false, false); code != 0 {
					fmt.Fprintln(os.Stderr, "Build failed — waiting for next change...")
				} else {
					proc = startNodeProcess(outDir)
				}
			}
		}
	}
}

// startNodeProcess starts `bun run start` in the given output directory.
func startNodeProcess(outDir string) *exec.Cmd {
	cmd := exec.Command("bun", "run", "start")
	cmd.Dir = outDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not start server: %s\n", err)
		return nil
	}
	fmt.Printf("Server started (pid %d)\n", cmd.Process.Pid)
	return cmd
}

func cmdRun(filename, outDir string) int {
	if code := cmdBuild(filename, outDir, targetNode, false, false, false); code != 0 {
		return code
	}

	// Install dependencies if node_modules is absent.
	if _, err := os.Stat(filepath.Join(outDir, "node_modules")); os.IsNotExist(err) {
		fmt.Printf("Installing dependencies in %s...\n", outDir)
		install := exec.Command("bun", "install")
		install.Dir = outDir
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "bun install failed: %s\n", err)
			return 2
		}
	}

	fmt.Printf("Starting server in %s...\n", outDir)
	start := exec.Command("bun", "run", "start")
	start.Dir = outDir
	start.Stdout = os.Stdout
	start.Stderr = os.Stderr
	if err := start.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Server exited: %s\n", err)
		return 1
	}
	return 0
}

func cmdTest(filename, outDir string) int {
	// Enable auto-generated contract tests with the in-memory (PGlite) harness so
	// `bp test` runs end-to-end without a live database.
	if code := cmdBuild(filename, outDir, targetNode, false, false, true); code != 0 {
		return code
	}

	if _, err := os.Stat(filepath.Join(outDir, "node_modules")); os.IsNotExist(err) {
		fmt.Printf("Installing dependencies in %s...\n", outDir)
		install := exec.Command("bun", "install")
		install.Dir = outDir
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "bun install failed: %s\n", err)
			return 2
		}
	}

	fmt.Printf("Running tests in %s...\n", outDir)
	cmd := exec.Command("bun", "run", "test")
	cmd.Dir = outDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

func cmdMigrate(filename, outDir, subCmd, target string) int {
	// Validate target up front so we don't half-build a node tree before
	// realising the user asked for python or vice versa.
	canonical, err := resolveTarget(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	target = canonical

	if code := cmdBuild(filename, outDir, target, false, false, false); code != 0 {
		return code
	}

	// Copy .env from the project directory (parent of outDir) to outDir if it exists.
	// Both drizzle-kit and alembic read DATABASE_URL from .env, so keep this
	// behavior identical across targets.
	projectDir := filepath.Dir(filename)
	envSrc := filepath.Join(projectDir, ".env")
	envDst := filepath.Join(outDir, ".env")
	if _, err := os.Stat(envSrc); err == nil {
		if envContent, err := os.ReadFile(envSrc); err == nil {
			if err := os.WriteFile(envDst, envContent, 0644); err == nil {
				fmt.Printf("Copied .env from %s to %s\n", projectDir, outDir)
			}
		}
	}

	switch target {
	case targetPython:
		return runAlembic(outDir, subCmd)
	default:
		return runDrizzleKit(outDir, subCmd)
	}
}

// runDrizzleKit installs node deps if needed, then shells to `bunx drizzle-kit
// <subCmd>` in outDir.
func runDrizzleKit(outDir, subCmd string) int {
	if _, err := os.Stat(filepath.Join(outDir, "node_modules")); os.IsNotExist(err) {
		fmt.Printf("Installing dependencies in %s...\n", outDir)
		install := exec.Command("bun", "install")
		install.Dir = outDir
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "bun install failed: %s\n", err)
			return 2
		}
	}

	fmt.Printf("Running drizzle-kit %s in %s...\n", subCmd, outDir)
	cmd := exec.Command("bunx", "drizzle-kit", subCmd)
	cmd.Dir = outDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

// runAlembic translates Blueprint's migrate subcommands into alembic invocations
// and shells to `uv run alembic ...` so users don't have to activate a venv.
// We check for `uv` on PATH first to give a clean error rather than the cryptic
// "executable file not found" message exec.Command produces.
func runAlembic(outDir, subCmd string) int {
	if _, err := exec.LookPath("uv"); err != nil {
		fmt.Fprintln(os.Stderr, "Error: uv not found on PATH. Install uv to run python migrations.")
		fmt.Fprintln(os.Stderr, "See https://docs.astral.sh/uv/ for installation instructions.")
		return 2
	}

	var alembicArgs []string
	switch subCmd {
	case "generate":
		alembicArgs = []string{"run", "alembic", "revision", "--autogenerate", "-m", "auto"}
	case "push":
		alembicArgs = []string{"run", "alembic", "upgrade", "head"}
	case "check":
		alembicArgs = []string{"run", "alembic", "check"}
	case "studio":
		fmt.Fprintln(os.Stderr, "Error: 'studio' is not supported with --target python (alembic has no GUI).")
		fmt.Fprintln(os.Stderr, "Use 'bp migrate <file> generate --target python' or 'bp migrate <file> push --target python'.")
		return 2
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown migrate subcommand %q for --target python\n", subCmd)
		return 2
	}

	fmt.Printf("Running uv %s in %s...\n", strings.Join(alembicArgs, " "), outDir)
	cmd := exec.Command("uv", alembicArgs...)
	cmd.Dir = outDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// uv exits non-zero when alembic itself is missing; surface a clearer
		// hint instead of letting users hunt through uv's output.
		fmt.Fprintf(os.Stderr, "alembic invocation failed: %s\n", err)
		fmt.Fprintln(os.Stderr, "If alembic is not installed, run `uv sync` inside the generated project first.")
		return 1
	}
	return 0
}

func cmdGenerate(filename string, write bool) int {
	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, parser.FormatError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d syntax error(s) found\n", len(parseErrors))
		return 1
	}

	slots := generate.FindSlots(file)
	if len(slots) == 0 {
		fmt.Printf("No @> generate slots found in %s\n", filename)
		return 0
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: ANTHROPIC_API_KEY environment variable not set")
		return 2
	}

	fmt.Printf("Found %d @> slot(s) — calling Anthropic API...\n", len(slots))

	replacements, err := generate.GenerateAll(slots, apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Generate error: %s\n", err)
		return 4
	}

	updated := generate.Apply(src, replacements)

	if write {
		if err := os.WriteFile(filename, updated, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
			return 2
		}
		fmt.Printf("Updated: %s (%d slot(s) resolved)\n", filename, len(replacements))
	} else {
		fmt.Print(string(updated))
	}
	return 0
}

func cmdInit(name string) int {
	// Default name to current directory name
	if name == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting current directory: %s\n", err)
			return 2
		}
		name = filepath.Base(cwd)
	}

	// Sanitize name for use as a directory/file name
	safeName := strings.ToLower(strings.ReplaceAll(name, " ", "-"))

	// Create the service directory if it doesn't exist
	if err := os.MkdirAll(safeName, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %s\n", err)
		return 2
	}

	bpFile := filepath.Join(safeName, safeName+".bp")

	// Don't overwrite existing file
	if _, err := os.Stat(bpFile); err == nil {
		fmt.Fprintf(os.Stderr, "Error: %s already exists\n", bpFile)
		return 1
	}

	template := buildInitTemplate(name, safeName)

	if err := os.WriteFile(bpFile, []byte(template), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
		return 2
	}

	fmt.Printf("Created %s/%s.bp\n", safeName, safeName)
	return 0
}

func cmdEject(dir string) int {
	count := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)

		// Remove Blueprint header lines
		lines := strings.Split(content, "\n")
		var cleaned []string
		for _, line := range lines {
			if strings.HasPrefix(line, "// Generated by Blueprint") ||
				strings.HasPrefix(line, "// Do not edit directly") {
				continue
			}
			cleaned = append(cleaned, line)
		}

		newContent := strings.Join(cleaned, "\n")
		if newContent != content {
			if err := os.WriteFile(path, []byte(newContent), info.Mode()); err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			fmt.Printf("  ejected: %s\n", rel)
			count++
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	fmt.Printf("\nEjected %d file(s). This code is now fully yours.\n", count)
	fmt.Println("You can safely delete your .bp source file and stop using Blueprint.")
	return 0
}

func cmdDiff(filename, outDir, target string, reactQuery, frontendOnly, genTests, apply, exitOnDiff, noColor bool) int {
	canonical, err := resolveTarget(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	target = canonical

	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, parser.FormatError(e, src))
		}
		return 1
	}

	if errs := resolveIncludes(file, filename); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		return 1
	}

	checkErrors := checker.Check(file)
	if len(checkErrors) > 0 {
		for _, e := range checkErrors {
			fmt.Fprintln(os.Stderr, e)
		}
		return 1
	}

	tmpDir, err := os.MkdirTemp("", "bp-diff-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	switch target {
	case targetNode:
		gen := js.New().WithReactQuery(reactQuery).WithFrontendOnly(frontendOnly).WithGenTests(genTests)
		if err := gen.Generate(file, tmpDir); err != nil {
			fmt.Fprintf(os.Stderr, "Codegen error: %s\n", err)
			return 4
		}
	case targetPython:
		if err := pythongen.New().WithGenTests(genTests).Generate(file, tmpDir); err != nil {
			fmt.Fprintf(os.Stderr, "Codegen error: %s\n", err)
			return 4
		}
	default:
		fmt.Fprintf(os.Stderr, "Error: target %q has no generator\n", target)
		return 2
	}

	useColor := !noColor && isTerminal(os.Stdout)
	report := diffReport{useColor: useColor}

	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		// outDir absent — every emitted file is new.
		_ = filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(tmpDir, path)
			if skipDiffPath(rel) {
				return nil
			}
			report.added = append(report.added, rel)
			return nil
		})
	} else {
		err := filepath.Walk(tmpDir, func(newPath string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(tmpDir, newPath)
			if skipDiffPath(rel) {
				return nil
			}
			oldPath := filepath.Join(outDir, rel)

			oldData, err := os.ReadFile(oldPath)
			if os.IsNotExist(err) {
				report.added = append(report.added, rel)
				return nil
			}
			if err != nil {
				return err
			}
			newData, _ := os.ReadFile(newPath)
			if !bytes.Equal(oldData, newData) {
				report.modified = append(report.modified, fileDiff{
					rel:     rel,
					oldPath: oldPath,
					newPath: newPath,
				})
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error comparing files: %s\n", err)
			return 2
		}

		// Deleted files: only consider entries the previous build's manifest
		// claimed it generated. Otherwise unrelated tree state (node_modules,
		// .venv, user-installed deps, drizzle-kit output) gets flagged as
		// "deleted" simply because `bp` never produced it. This is what makes
		// `bp diff --exit-code` survive a CI pipeline that runs `npm install`
		// (or `uv sync`) in the build directory between bp invocations.
		prev := readPrevManifest(outDir)
		for rel := range prev {
			if skipDiffPath(rel) {
				continue
			}
			if _, err := os.Stat(filepath.Join(tmpDir, rel)); !os.IsNotExist(err) {
				continue
			}
			// Was generated last time and the new build doesn't produce it.
			// Only report if it still exists on disk — a file already removed
			// by the writer's stale-cleanup step isn't a diff.
			if _, err := os.Stat(filepath.Join(outDir, rel)); err == nil {
				report.deleted = append(report.deleted, rel)
			}
		}
	}

	sort.Strings(report.added)
	sort.Strings(report.deleted)
	sort.Slice(report.modified, func(i, j int) bool { return report.modified[i].rel < report.modified[j].rel })

	report.print(os.Stdout)

	if !report.any() {
		return 0
	}

	if apply {
		fmt.Println()
		fmt.Println("Applying changes...")
		if code := cmdBuild(filename, outDir, target, reactQuery, frontendOnly, genTests); code != 0 {
			return code
		}
		return 0
	}

	if exitOnDiff {
		return 1
	}
	return 0
}

// readPrevManifest reads outDir/.blueprint/manifest.json (if present) and
// returns the set of paths it claims as generated. Used by cmdDiff to
// constrain "deleted" detection to files bp actually owns.
func readPrevManifest(outDir string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(outDir, ".blueprint", "manifest.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Generated map[string]string `json:"generated"`
	}
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	out := make(map[string]bool, len(m.Generated))
	for k := range m.Generated {
		out[k] = true
	}
	return out
}

// --- diff reporting helpers ---

// skipDiffPath hides files whose content is purely derived from the rest of the
// output, so the diff stays focused on what the user wrote in the .bp source.
func skipDiffPath(rel string) bool {
	// The codegen manifest is a hash-of-everything-else; its diff is opaque noise.
	return rel == filepath.FromSlash(".blueprint/manifest.json")
}

type fileDiff struct {
	rel     string
	oldPath string
	newPath string
}

type diffReport struct {
	added    []string
	modified []fileDiff
	deleted  []string
	useColor bool
}

func (r *diffReport) any() bool {
	return len(r.added)+len(r.modified)+len(r.deleted) > 0
}

func (r *diffReport) print(w io.Writer) {
	for _, rel := range r.added {
		fmt.Fprintf(w, "%snew file: %s%s\n", r.colorAdded(), rel, r.colorReset())
	}
	for _, rel := range r.deleted {
		fmt.Fprintf(w, "%sdeleted:  %s%s\n", r.colorRemoved(), rel, r.colorReset())
	}
	for _, fd := range r.modified {
		fmt.Fprintf(w, "%smodified: %s%s\n", r.colorHeader(), fd.rel, r.colorReset())
		patch, err := unifiedDiff(fd.oldPath, fd.newPath, fd.rel)
		if err != nil {
			fmt.Fprintf(w, "  (could not produce unified diff: %v)\n", err)
			continue
		}
		r.writeColored(w, patch)
	}
	if !r.any() {
		fmt.Fprintln(w, "No changes — output is up to date.")
		return
	}
	fmt.Fprintf(w, "\n%d added, %d modified, %d deleted.\n",
		len(r.added), len(r.modified), len(r.deleted))
}

// unifiedDiff shells out to `diff -u`, which returns exit 1 for "files differ" —
// that is normal and not an error.
func unifiedDiff(oldPath, newPath, rel string) (string, error) {
	cmd := exec.Command("diff", "-u",
		"--label", "a/"+rel,
		"--label", "b/"+rel,
		oldPath, newPath)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return string(out), nil
		}
		return "", err
	}
	return string(out), nil
}

// writeColored line-paints a unified-diff patch using ANSI escapes when enabled.
func (r *diffReport) writeColored(w io.Writer, patch string) {
	if !r.useColor {
		fmt.Fprint(w, patch)
		return
	}
	for _, line := range strings.SplitAfter(patch, "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			fmt.Fprintf(w, "%s%s%s", r.colorHeader(), line, r.colorReset())
		case strings.HasPrefix(line, "@@"):
			fmt.Fprintf(w, "%s%s%s", r.colorHunk(), line, r.colorReset())
		case strings.HasPrefix(line, "+"):
			fmt.Fprintf(w, "%s%s%s", r.colorAdded(), line, r.colorReset())
		case strings.HasPrefix(line, "-"):
			fmt.Fprintf(w, "%s%s%s", r.colorRemoved(), line, r.colorReset())
		default:
			fmt.Fprint(w, line)
		}
	}
}

func (r *diffReport) colorAdded() string   { return ifColor(r.useColor, "\x1b[32m") }
func (r *diffReport) colorRemoved() string { return ifColor(r.useColor, "\x1b[31m") }
func (r *diffReport) colorHeader() string  { return ifColor(r.useColor, "\x1b[1m") }
func (r *diffReport) colorHunk() string    { return ifColor(r.useColor, "\x1b[36m") }
func (r *diffReport) colorReset() string   { return ifColor(r.useColor, "\x1b[0m") }

func ifColor(on bool, s string) string {
	if on {
		return s
	}
	return ""
}

// isTerminal reports whether f is a TTY without pulling in a dependency.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func cmdDeploy(filename, outDir, tag, deployTarget string, noRun bool) int {
	// Resolve and validate --target before doing any work so unsupported
	// targets fail fast with a clear pointer to the production-readiness gate.
	switch deployTarget {
	case "", "docker":
		deployTarget = "docker"
	case "fly":
		fmt.Fprintln(os.Stderr, "Error: --target fly is not implemented yet; tracked for v0.11.")
		fmt.Fprintln(os.Stderr, "See docs/production-readiness.md (Pillar 5: deployable artifacts) for status.")
		return 2
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown --target %q (supported: docker; fly is reserved for v0.11)\n", deployTarget)
		return 2
	}

	// First, build the project
	fmt.Println("Building...")
	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, parser.FormatError(e, src))
		}
		return 1
	}

	if errs := resolveIncludes(file, filename); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		return 1
	}

	checkErrors := checker.Check(file)
	if len(checkErrors) > 0 {
		for _, e := range checkErrors {
			fmt.Fprintln(os.Stderr, e)
		}
		return 1
	}

	// Build to outDir
	gen := js.New()
	if err := gen.Generate(file, outDir); err != nil {
		fmt.Fprintf(os.Stderr, "Codegen error: %s\n", err)
		return 4
	}

	// Check if Dockerfile exists
	dockerfilePath := filepath.Join(outDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: No Dockerfile found in %s\n", outDir)
		return 2
	}

	// Build Docker image
	fmt.Printf("Building Docker image: %s\n", tag)
	cmd := exec.Command("docker", "build", "-t", tag, ".")
	cmd.Dir = outDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Docker build failed: %s\n", err)
		return 2
	}

	// Smoke-test the built image to honor Pillar 5 row 1 ("build and run").
	// Skipped via --no-run for CI flows that only want to validate the build.
	if !noRun {
		if code := dockerSmokeTest(tag); code != 0 {
			return code
		}
	}

	fmt.Printf("\nDeploy complete! Image: %s\n", tag)
	fmt.Println("Run with: docker run -p 3000:3000", tag)
	return 0
}

// dockerSmokeTest runs the freshly-built image, hits /health, then tears it down.
// Returns a non-zero exit code if the container fails to start or /health does
// not respond. The container is named bp-smoke so a stale instance from a
// previous run cannot wedge the host.
func dockerSmokeTest(tag string) int {
	const containerName = "bp-smoke"

	// Best-effort: remove any leftover container from a previous interrupted run.
	_ = exec.Command("docker", "rm", "-f", containerName).Run()

	fmt.Printf("Smoke-testing image %s (docker run -d -p 3000:3000 --name %s)...\n", tag, containerName)
	runCmd := exec.Command("docker", "run", "-d", "-p", "3000:3000", "--name", containerName, tag)
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	if err := runCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "docker run failed: %s\n", err)
		return 2
	}
	// Make sure we tear the container down even if /health fails.
	defer func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	}()

	time.Sleep(2 * time.Second)

	// Probe /health from inside the container so we don't depend on host curl.
	// The generated server exposes /health on port 3000 by default.
	healthCmd := exec.Command("docker", "exec", containerName,
		"sh", "-c", "wget -qO- http://127.0.0.1:3000/health || curl -fsS http://127.0.0.1:3000/health")
	healthCmd.Stdout = os.Stdout
	healthCmd.Stderr = os.Stderr
	if err := healthCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Smoke test failed: /health did not respond (%s)\n", err)
		return 1
	}
	fmt.Println("Smoke test passed: /health responded.")
	return 0
}

func cmdCompletion(shell string) int {
	switch shell {
	case "bash":
		fmt.Println(`_bp_completion() {
    local cur prev opts
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    
    commands="check build frontend diff run dev test migrate generate docs fmt lint init eject deploy completion version help"
    
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "${commands}" -- ${cur}) )
        return 0
    fi
    
    case "${prev}" in
        check|build|frontend|diff|run|dev|test|migrate|generate|docs|fmt|lint|deploy)
            _filedir "@(.bp)"
            return 0
            ;;
        publish)
            _filedir "@(.bp)"
            return 0
            ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh fish" -- ${cur}) )
            return 0
            ;;
        --out)
            _filedir -d
            return 0
            ;;
    esac

    if [[ ${COMP_CWORD} -eq 2 && ${COMP_WORDS[1]} == "frontend" ]]; then
        COMPREPLY=( $(compgen -W "publish" -- ${cur}) $(compgen -f -X '!*.bp' -- ${cur}) )
        return 0
    fi
    
    if [[ ${cur} == -* ]]; then
        COMPREPLY=( $(compgen -W "--out --react-query --frontend-only --skip-install --write --check --tag --help -h" -- ${cur}) )
        return 0
    fi
}

complete -F _bp_completion bp`)
	case "zsh":
		fmt.Println(`#compdef bp

_bp() {
    local curcontext="$curcontext" state line
    typeset -A opt_args

    _arguments -C \
        '1: :->command' \
        '*: :->args'

    case "$state" in
        command)
            _values 'commands' \
                'check[Validate syntax and semantics]' \
                'build[Compile .bp to JavaScript/TypeScript]' \
                'frontend[Generate standalone frontend SDK package]' \
                'diff[Show changes without overwriting]' \
                'run[Build and start the server]' \
                'dev[Watch mode - rebuild and restart]' \
                'test[Build and run vitest]' \
                'migrate[Run drizzle-kit migration]' \
                'generate[Resolve @> slots via LLM]' \
                'docs[Generate OpenAPI 3.1 JSON]' \
                'fmt[Format a .bp file]' \
                'lint[Lint for best practices]' \
                'init[Scaffold a new project]' \
                'eject[Remove Blueprint markers]' \
                'deploy[Build and run Docker container]' \
                'completion[Generate shell completion]' \
                'version[Print version]' \
                'help[Show help]'
            ;;
        args)
            case "$line[1]" in
                check|build|frontend|diff|run|dev|test|migrate|generate|docs|fmt|lint|deploy)
                    _files -g "*.bp"
                    ;;
                completion)
                    _values 'shell' 'bash' 'zsh' 'fish'
                    ;;
                init|eject|version|help)
                    ;;
            esac
            ;;
    esac
}

compdef _bp bp`)
	case "fish":
		fmt.Println(`complete -c bp -f

complete -c bp -n "__fish_use_subcommand" -a "check" -d "Validate syntax and semantics"
complete -c bp -n "__fish_use_subcommand" -a "build" -d "Compile .bp to JavaScript/TypeScript"
complete -c bp -n "__fish_use_subcommand" -a "frontend" -d "Generate standalone frontend SDK package"
complete -c bp -n "__fish_use_subcommand" -a "diff" -d "Show changes without overwriting"
complete -c bp -n "__fish_use_subcommand" -a "run" -d "Build and start the server"
complete -c bp -n "__fish_use_subcommand" -a "dev" -d "Watch mode - rebuild and restart"
complete -c bp -n "__fish_use_subcommand" -a "test" -d "Build and run vitest"
complete -c bp -n "__fish_use_subcommand" -a "migrate" -d "Run drizzle-kit migration"
complete -c bp -n "__fish_use_subcommand" -a "generate" -d "Resolve @> slots via LLM"
complete -c bp -n "__fish_use_subcommand" -a "docs" -d "Generate OpenAPI 3.1 JSON"
complete -c bp -n "__fish_use_subcommand" -a "fmt" -d "Format a .bp file"
complete -c bp -n "__fish_use_subcommand" -a "lint" -d "Lint for best practices"
complete -c bp -n "__fish_use_subcommand" -a "init" -d "Scaffold a new project"
complete -c bp -n "__fish_use_subcommand" -a "eject" -d "Remove Blueprint markers"
complete -c bp -n "__fish_use_subcommand" -a "deploy" -d "Build and run Docker container"
complete -c bp -n "__fish_use_subcommand" -a "completion" -d "Generate shell completion"
complete -c bp -n "__fish_use_subcommand" -a "version" -d "Print version"
complete -c bp -n "__fish_use_subcommand" -a "help" -d "Show help"

complete -c bp -n "__fish_seen_subcommand_from check build frontend diff run dev test migrate generate docs fmt lint deploy" -a "(__fish_complete_suffix .bp)"
complete -c bp -n "__fish_seen_subcommand_from frontend; and not __fish_seen_subcommand_from publish" -a "publish" -d "Build and dry-run frontend package publish flow"
complete -c bp -n "__fish_seen_subcommand_from publish" -a "(__fish_complete_suffix .bp)"
complete -c bp -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"

complete -c bp -l out -d "Output directory"
complete -c bp -l react-query -d "Generate React Query hooks"
complete -c bp -l frontend-only -d "Emit only the standalone frontend package"
complete -c bp -l skip-install -d "Skip bun install before frontend publish dry-run"
complete -c bp -l write -d "Write output back to file"
complete -c bp -l check -d "Check if formatted (CI mode)"
complete -c bp -l tag -d "Docker image tag"
complete -c bp -l help -s h -d "Show help"`)
	default:
		fmt.Fprintf(os.Stderr, "Unknown shell: %s. Supported: bash, zsh, fish\n", shell)
		return 1
	}
	return 0
}

func buildInitTemplate(displayName, safeName string) string {
	return fmt.Sprintf(`@ %q
blueprint %q {
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
`, displayName+" — describe your service here", safeName)
}

func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

func printCommandHelp(cmd, usage, desc string, flags [][2]string) {
	fmt.Fprintf(os.Stderr, "Usage: bp %s\n\n%s\n", usage, desc)
	if len(flags) > 0 {
		fmt.Fprintf(os.Stderr, "\nFlags:\n")
		for _, f := range flags {
			fmt.Fprintf(os.Stderr, "  %-16s %s\n", f[0], f[1])
		}
	}
}

func printUsage() {
	fmt.Println("Blueprint Language Toolchain")
	fmt.Println()
	fmt.Println("Usage: bp <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  check      <file.bp>                       Validate syntax and semantics")
	fmt.Println("  build      <file.bp> [--out dir]           Compile .bp to JavaScript/TypeScript")
	fmt.Println("  frontend   <file.bp> [--out dir]           Generate the standalone frontend SDK package")
	fmt.Println("  frontend   publish <file.bp>               Build and dry-run the frontend SDK publish flow")
	fmt.Println("  diff       <file.bp> [--out dir]           Show changes without overwriting")
	fmt.Println("  run        <file.bp> [--out dir]           Build and start the server")
	fmt.Println("  dev        <file.bp> [--out dir]           Watch mode — rebuild and restart on changes")
	fmt.Println("  test       <file.bp> [--out dir]           Build and run vitest")
	fmt.Println("  migrate    <file.bp> [generate|push|…]     Build and run drizzle-kit (node) or alembic (--target python)")
	fmt.Println("  generate   <file.bp> [--write]             Resolve @> slots via LLM (needs ANTHROPIC_API_KEY)")
	fmt.Println("  docs       <file.bp> [--out file.json]     Generate OpenAPI 3.1 JSON spec")
	fmt.Println("  fmt        <file.bp> [--write]             Format a .bp file")
	fmt.Println("  lint       <file.bp>                       Lint a .bp file for best practices")
	fmt.Println("  init       [name]                          Scaffold a new Blueprint project")
	fmt.Println("  eject      <dir>                           Remove Blueprint markers from generated code")
	fmt.Println("  deploy     <file.bp> [--tag <image>]       Build and smoke-run Docker image (--target docker default; fly reserved)")
	fmt.Println("  completion <bash|zsh|fish>                 Generate shell completion script")
	fmt.Println("  stats      <file.bp> [--json]              Show code statistics")
	fmt.Println("  explain    <code>                          Print docs for a structured error code (Cxxx/Lxxx/Pxxx)")
	fmt.Println("  context    [topic] [--format md|json]      Agent-facing language + CLI surface")
	fmt.Println("  doctor                                     Check environment dependencies")
	fmt.Println("  lsp                                        Start LSP server")
	fmt.Println("  version                                    Print version")
	fmt.Println("  help                                       Show this help")
}

// suggestCommand returns the closest matching command for typos
func suggestCommand(input string) string {
	commands := []string{"check", "build", "frontend", "diff", "run", "dev", "test", "migrate",
		"generate", "docs", "fmt", "lint", "init", "eject", "deploy",
		"completion", "explain", "context", "doctor", "lsp", "stats",
		"version", "help"}

	// Common typos mapping
	typos := map[string]string{
		"chekc":   "check",
		"chek":    "check",
		"biuld":   "build",
		"buid":    "build",
		"buld":    "build",
		"fronend": "frontend",
		"fronted": "frontend",
		"front":   "frontend",
		"diff":    "diff",
		"ru":      "run",
		"rn":      "run",
		"de":      "dev",
		"tev":     "dev",
		"tets":    "test",
		"tst":     "test",
		"migarte": "migrate",
		"migrat":  "migrate",
		"generat": "generate",
		"gen":     "generate",
		"dcos":    "docs",
		"fnt":     "fmt",
		"fromat":  "fmt",
		"int":     "init",
		"ejet":    "eject",
		"deply":   "deploy",
		"complet": "completion",
		"compl":   "completion",
		"verison": "version",
		"versin":  "version",
		"ver":     "version",
		"hlep":    "help",
		"hel":     "help",
	}

	// Direct typo match
	if suggestion, ok := typos[input]; ok {
		return suggestion
	}

	// Find closest match by Levenshtein distance
	bestMatch := ""
	bestDist := 3 // Only suggest if within 2 edits

	for _, cmd := range commands {
		dist := levenshteinDistance(input, cmd)
		if dist < bestDist {
			bestDist = dist
			bestMatch = cmd
		}
	}

	return bestMatch
}

// levenshteinDistance calculates the edit distance between two strings
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Use a simple iterative approach with O(min(m,n)) space
	if len(a) < len(b) {
		a, b = b, a
	}

	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)

	for j := 0; j <= len(b); j++ {
		previous[j] = j
	}

	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = min3(
				current[j-1]+1,     // insertion
				previous[j]+1,      // deletion
				previous[j-1]+cost, // substitution
			)
		}
		previous, current = current, previous
	}

	return previous[len(b)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// JSON output helpers

func printJSONError(errorType, message string) {
	output := map[string]interface{}{
		"success": false,
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	}
	printJSON(output)
}

func printJSONCheckResult(filename string, valid bool, parseErrors []parser.ParseError, checkErrors []checker.CheckError) {
	output := map[string]interface{}{
		"success":  valid,
		"filename": filename,
	}

	if !valid {
		var errors []map[string]interface{}

		for _, e := range parseErrors {
			errors = append(errors, map[string]interface{}{
				"type":    "parse",
				"message": e.Message,
				"line":    e.Loc.Line,
				"column":  e.Loc.Col,
				"file":    e.Loc.File,
			})
		}

		for _, e := range checkErrors {
			errors = append(errors, map[string]interface{}{
				"type":    "semantic",
				"message": e.Message,
				"line":    e.Loc.Line,
				"column":  e.Loc.Col,
				"file":    e.Loc.File,
			})
		}

		output["errors"] = errors
		output["error_count"] = len(errors)
	}

	printJSON(output)
}

func printJSON(v interface{}) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to encode JSON output: %v\n", err)
	}
}

// cmdStats shows code statistics for a Blueprint file
func cmdStats(filename string, jsonOutput bool) int {
	src, err := os.ReadFile(filename)
	if err != nil {
		if jsonOutput {
			printJSONError("read_error", err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		return 2
	}

	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		if jsonOutput {
			printJSON(map[string]interface{}{
				"success": false,
				"error":   "parse errors in file",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: file has %d parse error(s)\n", len(parseErrors))
		}
		return 1
	}

	// Count different block types
	stats := struct {
		TotalLines      int `json:"total_lines"`
		Models          int `json:"models"`
		Endpoints       int `json:"endpoints"`
		Functions       int `json:"functions"`
		Pipes           int `json:"pipes"`
		Middleware      int `json:"middleware"`
		Workers         int `json:"workers"`
		Schedules       int `json:"schedules"`
		Tests           int `json:"tests"`
		Secrets         int `json:"secrets"`
		EnvVars         int `json:"env_vars"`
		Enums           int `json:"enums"`
		Types           int `json:"types"`
		ComplexityScore int `json:"complexity_score"`
	}{}

	stats.TotalLines = len(strings.Split(string(src), "\n"))

	for _, block := range file.Blocks {
		switch b := block.(type) {
		case *ast.Model:
			stats.Models++
			stats.ComplexityScore += len(b.Fields)
		case *ast.Endpoint:
			stats.Endpoints++
			stats.ComplexityScore += len(b.Stmts)
		case *ast.Fn:
			stats.Functions++
			if b.Logic != nil {
				stats.ComplexityScore += len(b.Logic.Stmts)
			}
		case *ast.Pipe:
			stats.Pipes++
			stats.ComplexityScore += len(b.Stmts)
		case *ast.Middleware:
			stats.Middleware++
			stats.ComplexityScore += len(b.Before) + len(b.After)
		case *ast.Worker:
			stats.Workers++
			stats.ComplexityScore += len(b.Stmts)
		case *ast.Schedule:
			stats.Schedules++
			stats.ComplexityScore += len(b.Stmts)
		case *ast.Test:
			stats.Tests++
		case *ast.Secret:
			stats.Secrets++
		case *ast.Env:
			stats.EnvVars++
		case *ast.Enum:
			stats.Enums++
			stats.ComplexityScore += len(b.Variants)
		case *ast.TypeDecl:
			stats.Types++
		}
	}

	if jsonOutput {
		printJSON(map[string]interface{}{
			"success":  true,
			"filename": filename,
			"stats":    stats,
		})
	} else {
		fmt.Printf("Statistics for %s:\n\n", filename)
		fmt.Printf("  Lines of code:     %d\n", stats.TotalLines)
		fmt.Printf("  Complexity score:  %d\n\n", stats.ComplexityScore)
		fmt.Println("  Blocks:")
		fmt.Printf("    Models:      %d\n", stats.Models)
		fmt.Printf("    Endpoints:   %d\n", stats.Endpoints)
		fmt.Printf("    Functions:   %d\n", stats.Functions)
		fmt.Printf("    Pipes:       %d\n", stats.Pipes)
		fmt.Printf("    Middleware:  %d\n", stats.Middleware)
		fmt.Printf("    Workers:     %d\n", stats.Workers)
		fmt.Printf("    Schedules:   %d\n", stats.Schedules)
		fmt.Printf("    Tests:       %d\n", stats.Tests)
		fmt.Printf("    Enums:       %d\n", stats.Enums)
		fmt.Printf("    Types:       %d\n", stats.Types)
		fmt.Println()
		fmt.Println("  Configuration:")
		fmt.Printf("    Secrets:     %d\n", stats.Secrets)
		fmt.Printf("    Env vars:    %d\n", stats.EnvVars)
	}

	return 0
}

// cmdExplain prints the embedded documentation section for an error code.
// Returns 0 on hit, 1 when the code isn't documented (yet) so scripts can
// distinguish "no such code" from "lookup failed".
func cmdExplain(code string) int {
	body, ok := diag.Lookup(code)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: no documentation for code %q\n", strings.ToUpper(strings.TrimSpace(code)))
		fmt.Fprintln(os.Stderr, "  Hint: see docs/error-codes.md for the full list of documented codes.")
		return 1
	}
	fmt.Println(body)
	return 0
}

// cmdContext prints the agent-facing language + CLI surface. When topic is
// empty, the full surface is rendered (version + commands + targets + topic
// index). When topic is set, the focused topic doc is rendered. Format is
// "md" (default) or "json".
func cmdContext(topic, format string) int {
	if topic == "" {
		if err := agentctx.RenderSurface(agentctx.FullSurface(version), format, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}
	t, err := agentctx.Get(topic)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if err := agentctx.RenderTopic(t, format, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

// doctorVersionRegex extracts a semver-ish version string from arbitrary
// command output. Many tools embed extra prefix/suffix tokens (e.g.
// `redis-cli 8.8.0`, `Docker version 29.5.2, build 79eb04c`) which made the
// previous "Nth whitespace-separated token" heuristic produce garbage like
// "redis-cli" or "29.5.2,". A regex pinned to the first M.m.p triple sidesteps
// the per-tool special casing and is robust to trailing punctuation.
var doctorVersionRegex = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)

// extractDoctorVersion pulls the first dotted version number from output, or
// falls back to the trimmed line when no match is found so we still surface
// *something* useful (e.g. for tools that just print a date).
func extractDoctorVersion(output string) string {
	trimmed := strings.TrimSpace(output)
	if m := doctorVersionRegex.FindString(trimmed); m != "" {
		return m
	}
	return trimmed
}

// runDoctorProbe executes a shell command and returns the trimmed stdout (or
// combined output on failure) plus the success flag. Using sh -c keeps parity
// with the older behaviour so probes like `bunx drizzle-kit --version` work.
func runDoctorProbe(command string) (string, bool) {
	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(output)), false
	}
	return strings.TrimSpace(string(output)), true
}

// cmdDoctor checks the environment for Blueprint dependencies.
//
// We probe a wide set of tools that the generated TypeScript and Python
// projects actually shell out to (drizzle-kit, tsc, uv, alembic, pytest, ...).
// Each check carries its own list of fallback probes so we can prefer e.g.
// `bunx drizzle-kit --version` but fall back to `npx drizzle-kit --version`
// when bun is missing — whichever first succeeds wins.
func cmdDoctor() int {
	type probe struct {
		Name     string   `json:"name"`
		Commands []string `json:"-"`
		Required bool     `json:"required"`
		Found    bool     `json:"found"`
		Version  string   `json:"version,omitempty"`
		Message  string   `json:"message,omitempty"`
		// HintIfMissing is appended to the output when the probe fails;
		// useful for "install via uv add" style nudges.
		HintIfMissing string `json:"-"`
	}

	checks := []probe{
		{Name: "Go", Commands: []string{"go version"}, Required: false},
		{Name: "Node.js", Commands: []string{"node --version"}, Required: false},
		{Name: "npm", Commands: []string{"npm --version"}, Required: false},
		{Name: "Bun", Commands: []string{"bun --version"}, Required: false},
		{Name: "tsc", Commands: []string{"tsc --version", "npx --no-install tsc --version", "bunx tsc --version", "npx tsc --version"}, Required: false},
		{Name: "drizzle-kit", Commands: []string{"bunx drizzle-kit --version", "npx --no-install drizzle-kit --version", "npx drizzle-kit --version"}, Required: false},
		{Name: "Python", Commands: []string{"python3 --version"}, Required: false},
		{Name: "uv", Commands: []string{"uv --version"}, Required: false},
		{Name: "alembic", Commands: []string{"alembic --version"}, Required: false, HintIfMissing: "(install via uv add)"},
		{Name: "pytest", Commands: []string{"pytest --version"}, Required: false, HintIfMissing: "(install via uv add)"},
		{Name: "Docker", Commands: []string{"docker --version"}, Required: false},
		{Name: "PostgreSQL", Commands: []string{"psql --version"}, Required: false},
		{Name: "Redis", Commands: []string{"redis-cli --version"}, Required: false},
		{Name: "Git", Commands: []string{"git --version"}, Required: false},
	}

	for i := range checks {
		for _, cmd := range checks[i].Commands {
			out, ok := runDoctorProbe(cmd)
			if ok {
				checks[i].Found = true
				checks[i].Version = extractDoctorVersion(out)
				break
			}
		}
		if !checks[i].Found {
			if checks[i].Required {
				checks[i].Message = "Required for running generated projects"
			} else if checks[i].HintIfMissing != "" {
				checks[i].Message = checks[i].HintIfMissing
			} else {
				checks[i].Message = "Optional"
			}
		}
	}

	// Toolchain gating: we need at least ONE TypeScript toolchain (bun OR
	// node+npm) for the default target. python3 is only strictly required for
	// --target python, which we can't detect from `bp doctor` alone, so we
	// surface a soft warning rather than failing the check.
	tsToolchainOK := false
	nodeFound, npmFound := false, false
	for _, c := range checks {
		switch c.Name {
		case "Bun":
			if c.Found {
				tsToolchainOK = true
			}
		case "Node.js":
			nodeFound = c.Found
		case "npm":
			npmFound = c.Found
		}
	}
	if nodeFound && npmFound {
		tsToolchainOK = true
	}

	// Check environment variables
	envVars := []struct {
		Name    string `json:"name"`
		Set     bool   `json:"set"`
		Message string `json:"message,omitempty"`
	}{
		{Name: "ANTHROPIC_API_KEY", Message: "Required for bp generate"},
		{Name: "DATABASE_URL", Message: "Default database connection"},
		{Name: "REDIS_URL", Message: "For caching and job queues"},
	}

	for i := range envVars {
		envVars[i].Set = os.Getenv(envVars[i].Name) != ""
	}

	fmt.Println("Blueprint Environment Check")
	fmt.Println()
	fmt.Println("Dependencies:")
	for _, c := range checks {
		status := "✅"
		if !c.Found {
			if c.Required {
				status = "❌"
			} else {
				status = "⚠️"
			}
		}
		fmt.Printf("  %s %-12s", status, c.Name)
		if c.Found {
			fmt.Printf("(%s)", c.Version)
		}
		if c.Message != "" {
			fmt.Printf(" - %s", c.Message)
		}
		fmt.Println()
	}

	fmt.Println()
	fmt.Println("Environment Variables:")
	for _, e := range envVars {
		status := "❌"
		if e.Set {
			status = "✅"
		}
		fmt.Printf("  %s %-20s", status, e.Name)
		if !e.Set && e.Message != "" {
			fmt.Printf("- %s", e.Message)
		}
		fmt.Println()
	}

	fmt.Println()
	if tsToolchainOK {
		fmt.Println("✅ TypeScript toolchain found (need Bun OR Node.js+npm).")
	} else {
		fmt.Println("❌ No TypeScript toolchain detected.")
		fmt.Println("   Install Bun (https://bun.sh) or Node.js+npm to run generated projects.")
	}

	fmt.Println("ℹ️  Python target (--target python) requires python3 + uv; alembic/pytest are installed via uv add inside generated projects.")

	if tsToolchainOK {
		return 0
	}
	return 1
}
