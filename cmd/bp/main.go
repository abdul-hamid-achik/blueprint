package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/js"
	"github.com/abdul-hamid-achik/blueprint/internal/docs"
	"github.com/abdul-hamid-achik/blueprint/internal/generate"
	"github.com/abdul-hamid-achik/blueprint/internal/linter"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "check":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp check <file.bp>")
			os.Exit(1)
		}
		os.Exit(cmdCheck(os.Args[2]))
	case "build":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp build <file.bp> [--out <dir>]")
			os.Exit(1)
		}
		outDir := "generated"
		for i := 3; i < len(os.Args); i++ {
			if os.Args[i] == "--out" && i+1 < len(os.Args) {
				outDir = os.Args[i+1]
				i++
			}
		}
		os.Exit(cmdBuild(os.Args[2], outDir))
	case "fmt":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp fmt <file.bp> [--write]")
			os.Exit(1)
		}
		write := false
		for _, arg := range os.Args[3:] {
			if arg == "--write" {
				write = true
			}
		}
		os.Exit(cmdFmt(os.Args[2], write))
	case "lint":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp lint <file.bp>")
			os.Exit(1)
		}
		os.Exit(cmdLint(os.Args[2]))
	case "docs":
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
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: bp migrate <file.bp> [generate|push|studio] [--out <dir>]")
			os.Exit(1)
		}
		outDir := "generated"
		subCmd := "generate"
		for i := 3; i < len(os.Args); i++ {
			switch os.Args[i] {
			case "generate", "push", "studio", "check":
				subCmd = os.Args[i]
			case "--out":
				if i+1 < len(os.Args) {
					outDir = os.Args[i+1]
					i++
				}
			}
		}
		os.Exit(cmdMigrate(os.Args[2], outDir, subCmd))
	case "generate":
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
		name := ""
		if len(os.Args) >= 3 {
			name = os.Args[2]
		}
		os.Exit(cmdInit(name))
	case "version":
		fmt.Printf("bp version %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func cmdCheck(filename string) int {
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

	// Semantic checking
	checkErrors := checker.Check(file)

	if len(checkErrors) > 0 {
		for _, e := range checkErrors {
			fmt.Fprintln(os.Stderr, checker.FormatCheckError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d semantic error(s) found\n", len(checkErrors))
		return 1
	}

	fmt.Printf("OK: %s\n", filename)
	return 0
}

func cmdBuild(filename, outDir string) int {
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

	checkErrors := checker.Check(file)
	if len(checkErrors) > 0 {
		for _, e := range checkErrors {
			fmt.Fprintln(os.Stderr, checker.FormatCheckError(e, src))
		}
		fmt.Fprintf(os.Stderr, "\n%d semantic error(s) found\n", len(checkErrors))
		return 1
	}

	gen := js.New()
	if err := gen.Generate(file, outDir); err != nil {
		fmt.Fprintf(os.Stderr, "Codegen error: %s\n", err)
		return 4
	}

	fmt.Printf("Built %s -> %s/\n", filename, outDir)
	return 0
}

func cmdFmt(filename string, write bool) int {
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
	if code := cmdBuild(filename, outDir); code != 0 {
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

				if code := cmdBuild(filename, outDir); code != 0 {
					fmt.Fprintln(os.Stderr, "Build failed — waiting for next change...")
				} else {
					proc = startNodeProcess(outDir)
				}
			}
		}
	}
}

// startNodeProcess starts `npm start` in the given output directory.
func startNodeProcess(outDir string) *exec.Cmd {
	cmd := exec.Command("npm", "start")
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
	if code := cmdBuild(filename, outDir); code != 0 {
		return code
	}

	// npm install if node_modules is absent
	if _, err := os.Stat(filepath.Join(outDir, "node_modules")); os.IsNotExist(err) {
		fmt.Printf("Installing dependencies in %s...\n", outDir)
		install := exec.Command("npm", "install")
		install.Dir = outDir
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "npm install failed: %s\n", err)
			return 2
		}
	}

	fmt.Printf("Starting server in %s...\n", outDir)
	start := exec.Command("npm", "start")
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
	if code := cmdBuild(filename, outDir); code != 0 {
		return code
	}

	if _, err := os.Stat(filepath.Join(outDir, "node_modules")); os.IsNotExist(err) {
		fmt.Printf("Installing dependencies in %s...\n", outDir)
		install := exec.Command("npm", "install")
		install.Dir = outDir
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "npm install failed: %s\n", err)
			return 2
		}
	}

	fmt.Printf("Running tests in %s...\n", outDir)
	cmd := exec.Command("npx", "vitest", "run")
	cmd.Dir = outDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

func cmdMigrate(filename, outDir, subCmd string) int {
	if code := cmdBuild(filename, outDir); code != 0 {
		return code
	}

	if _, err := os.Stat(filepath.Join(outDir, "node_modules")); os.IsNotExist(err) {
		fmt.Printf("Installing dependencies in %s...\n", outDir)
		install := exec.Command("npm", "install")
		install.Dir = outDir
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "npm install failed: %s\n", err)
			return 2
		}
	}

	fmt.Printf("Running drizzle-kit %s in %s...\n", subCmd, outDir)
	cmd := exec.Command("npx", "drizzle-kit", subCmd)
	cmd.Dir = outDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
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

func printUsage() {
	fmt.Println("Blueprint Language Toolchain")
	fmt.Println()
	fmt.Println("Usage: bp <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  check    <file.bp>                       Validate syntax and semantics")
	fmt.Println("  build    <file.bp> [--out dir]           Compile .bp to JavaScript/TypeScript")
	fmt.Println("  run      <file.bp> [--out dir]           Build and start the server")
	fmt.Println("  dev      <file.bp> [--out dir]           Watch mode — rebuild and restart on changes")
	fmt.Println("  test     <file.bp> [--out dir]           Build and run vitest")
	fmt.Println("  migrate  <file.bp> [generate|push|…]    Build and run drizzle-kit migration")
	fmt.Println("  generate <file.bp> [--write]             Resolve @> slots via LLM (needs ANTHROPIC_API_KEY)")
	fmt.Println("  docs     <file.bp> [--out file.json]     Generate OpenAPI 3.1 JSON spec")
	fmt.Println("  fmt      <file.bp> [--write]             Format a .bp file")
	fmt.Println("  lint     <file.bp>                       Lint a .bp file for best practices")
	fmt.Println("  init     [name]                          Scaffold a new Blueprint project")
	fmt.Println("  version                                  Print version")
	fmt.Println("  help                                     Show this help")
}
