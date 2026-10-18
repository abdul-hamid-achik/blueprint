package main

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

// initTemplateFS holds the starter .bp sources and the scaffold files
// (.gitignore, README.md) written next to them by `bp init`.
//
//go:embed templates/init/*.bp.tmpl templates/scaffold/*.tmpl
var initTemplateFS embed.FS

// initTemplateSet parses every embedded template once. Base names are unique
// across the two directories, so ExecuteTemplate looks them up by file name.
var initTemplateSet = template.Must(template.New("").
	Funcs(template.FuncMap{"quote": strconv.Quote}).
	ParseFS(initTemplateFS, "templates/*/*.tmpl"))

// initTemplate describes one `bp init --template` starter.
type initTemplate struct {
	Name    string
	Summary string   // one line for --list-templates and the generated README
	Secrets []string // secrets the generated project reads at run time
	Note    string   // extra next step printed after the file is written
}

// initTemplates lists the starters in display order; the first is the default.
var initTemplates = []initTemplate{
	{
		Name:    "default",
		Summary: "One item model with list and create endpoints (Postgres)",
		Secrets: []string{"DATABASE_URL"},
	},
	{
		Name:    "minimal",
		Summary: "A single health endpoint, no database",
	},
	{
		Name:    "crud",
		Summary: "One model with paginated list, get, create, update and delete (Postgres)",
		Secrets: []string{"DATABASE_URL"},
	},
	{
		Name:    "auth",
		Summary: "Users with register and login, and a Bearer-protected profile (Postgres)",
		Secrets: []string{"DATABASE_URL", "JWT_SECRET"},
		Note: "The auth functions are stubs until you implement them: " +
			"generated/src/impl/functions/internal/auth.ts (hashPassword, verifyPassword, signJwt, verifyJwt).",
	},
	{
		Name:    "worker",
		Summary: "An endpoint that enqueues a job, plus a Redis-backed worker (Postgres, Redis)",
		Secrets: []string{"DATABASE_URL", "REDIS_URL"},
		Note: "bp test needs Redis reachable at REDIS_URL: the generated tests enqueue a real job. " +
			"Start one locally with `redis-server`.",
	},
}

// initNamePattern is the accepted shape of a sanitized project name. It keeps
// the name usable as a directory, a .bp file name, a blueprint name, and an
// npm package name (lowercase, starts with a letter or digit).
var initNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// initNameMaxLen is npm's package name limit, which the generated package.json
// inherits from the blueprint name.
const initNameMaxLen = 214

// initData is the context every init template is rendered with.
type initData struct {
	Name     string // display name, used in the @ description line
	Service  string // sanitized name: directory, .bp file, and blueprint name
	Template initTemplate
}

func initTemplateNames() []string {
	names := make([]string, len(initTemplates))
	for i, t := range initTemplates {
		names[i] = t.Name
	}
	return names
}

func findInitTemplate(name string) (initTemplate, bool) {
	for _, t := range initTemplates {
		if t.Name == name {
			return t, true
		}
	}
	return initTemplate{}, false
}

// suggestInitTemplate returns the closest template name within 2 edits, or "".
func suggestInitTemplate(input string) string {
	best := ""
	bestDist := 3
	for _, t := range initTemplates {
		if d := levenshteinDistance(input, t.Name); d < bestDist {
			bestDist = d
			best = t.Name
		}
	}
	return best
}

// runInit parses the arguments after `bp init` and returns the exit code.
func runInit(args []string) int {
	parsed, err := parseArgs(args, flagSpec{"--template": true, "--list-templates": false})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	if parsed.set["--list-templates"] {
		printInitTemplates()
		return 0
	}
	if len(parsed.positional) > 1 {
		fmt.Fprintf(os.Stderr, "Error: unexpected argument %q\n", parsed.positional[1])
		return 2
	}
	name := ""
	if len(parsed.positional) == 1 {
		name = parsed.positional[0]
	}
	templateName := "default"
	if parsed.set["--template"] {
		templateName = parsed.values["--template"]
	}
	return cmdInit(name, templateName)
}

func printInitTemplates() {
	fmt.Println("Available templates:")
	for _, t := range initTemplates {
		fmt.Printf("  %-8s  %s\n", t.Name, t.Summary)
	}
	fmt.Println()
	fmt.Println("Use: bp init <name> --template <template>")
}

// sanitizeInitName trims the name, lowercases it, and turns spaces into dashes,
// then rejects anything that cannot safely be a project directory and
// blueprint name.
func sanitizeInitName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("project name %q must not contain path separators", name)
	}
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("project name %q must not contain \"..\"", name)
	}
	safeName := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	if safeName == "" {
		return "", fmt.Errorf("project name is empty; pass one, e.g. bp init my-service")
	}
	if len(safeName) > initNameMaxLen || !initNamePattern.MatchString(safeName) {
		return "", fmt.Errorf("project name %q is not a valid blueprint name: use letters, digits, '-', '_' or '.', starting with a letter or digit", name)
	}
	return safeName, nil
}

func renderInitFile(templateName string, data initData) ([]byte, error) {
	var buf bytes.Buffer
	if err := initTemplateSet.ExecuteTemplate(&buf, templateName, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// cmdInit scaffolds <name>/<name>.bp from the chosen template, plus .gitignore
// and README.md when they are absent. Existing files are never overwritten.
func cmdInit(name, templateName string) int {
	tpl, ok := findInitTemplate(templateName)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown template %q", templateName)
		if s := suggestInitTemplate(templateName); s != "" {
			fmt.Fprintf(os.Stderr, " (did you mean %q?)", s)
		}
		fmt.Fprintf(os.Stderr, "\nAvailable templates: %s\n", strings.Join(initTemplateNames(), ", "))
		return 2
	}

	// Default name to current directory name
	if name == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting current directory: %s\n", err)
			return 2
		}
		name = filepath.Base(cwd)
	}

	safeName, err := sanitizeInitName(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	data := initData{Name: strings.TrimSpace(name), Service: safeName, Template: tpl}

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

	source, err := renderInitFile(tpl.Name+".bp.tmpl", data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error rendering template %s: %s\n", tpl.Name, err)
		return 2
	}
	if err := os.WriteFile(bpFile, source, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
		return 2
	}
	fmt.Printf("Created %s/%s.bp\n", safeName, safeName)

	for _, extra := range []struct{ file, tmpl string }{
		{".gitignore", "gitignore.tmpl"},
		{"README.md", "README.md.tmpl"},
	} {
		path := filepath.Join(safeName, extra.file)
		if _, err := os.Lstat(path); err == nil {
			continue
		}
		content, err := renderInitFile(extra.tmpl, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error rendering %s: %s\n", extra.file, err)
			return 2
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
			return 2
		}
		fmt.Printf("Created %s\n", path)
	}

	printInitNextSteps(safeName, tpl)
	return 0
}

func printInitNextSteps(service string, tpl initTemplate) {
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  cd %s\n", service)
	fmt.Printf("  bp check %s.bp\n", service)
	fmt.Printf("  bp run %s.bp\n", service)
	fmt.Printf("  bp dev %s.bp     # rebuild and restart on every change\n", service)
	fmt.Printf("  bp test %s.bp    # build and run the generated tests\n", service)
	if len(tpl.Secrets) > 0 {
		fmt.Println()
		fmt.Printf("  bp run builds to generated/ and needs %s set. After your first\n", strings.Join(tpl.Secrets, " and "))
		fmt.Println("  build/run, copy generated/.env.example to a .env file (project root or")
		fmt.Println("  generated/.env) and fill it in.")
	}
	if tpl.Note != "" {
		fmt.Println()
		fmt.Printf("  %s\n", tpl.Note)
	}
}
