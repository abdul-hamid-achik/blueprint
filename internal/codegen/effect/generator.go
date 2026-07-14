// Package effect is a code-generation target that emits TypeScript built on
// Effect. The current target is intentionally small: it emits a runnable
// health service and Effect Config for declared secrets and environment
// defaults, while rejecting every application construct it cannot preserve.
//
// Status: SCAFFOLD. This generator emits a runnable health-only Node service,
// typed Effect Config for secrets and env declarations, and the static project
// shell. It reports constructs it cannot translate via unsupportedFeatures —
// the same staged approach the Python target started with. Authored endpoint
// and model emission remain deliberately unsupported.
//
// It exists now to (a) validate that the codegen.Generator contract accepts a
// third target cleanly and (b) make `bp build --target effect` a real, wired
// path rather than vaporware.
package effect

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

const (
	effectVersion     = "3.22.0"
	typescriptVersion = "5.9.3"
	nodeTypesVersion  = "22.20.1"
)

// Generator emits an Effect-based TypeScript project.
type Generator struct {
	file       *ast.File
	sourceFile string
}

// New returns an Effect target generator.
func New() *Generator { return &Generator{} }

// Files implements codegen.Generator: it returns the generated Effect project
// as in-memory OutputFiles without touching disk. It returns an error naming
// the constructs this scaffold does not yet translate.
func (g *Generator) Files(file *ast.File) ([]codegen.OutputFile, error) {
	if err := codegen.RejectUnresolvedGenerateSteps(file); err != nil {
		return nil, err
	}
	g.file = file
	g.sourceFile = file.Loc.File
	if g.sourceFile == "" {
		g.sourceFile = "main.bp"
	}
	if file.Blueprint == nil {
		return nil, fmt.Errorf("effect target: missing blueprint block (this should have been caught by the checker)")
	}
	if missing := g.unsupportedFeatures(); len(missing) > 0 {
		return nil, fmt.Errorf(
			"effect target is an early scaffold and does not yet emit: %s.\n"+
				"  It currently generates a runnable health service + Effect Config for secrets/env defaults.\n"+
				"  Design + roadmap: docs/multi-target-codegen.md.\n"+
				"  Use --target node for the full feature set today.",
			strings.Join(missing, ", "))
	}

	var secrets []*ast.Secret
	var envs []*ast.Env
	for _, b := range g.file.Blocks {
		switch n := b.(type) {
		case *ast.Secret:
			secrets = append(secrets, n)
		case *ast.Env:
			envs = append(envs, n)
		}
	}

	return []codegen.OutputFile{
		g.genPackageJSON(),
		g.genTSConfig(),
		g.genConfig(secrets, envs),
		g.genIndex(),
		g.genEnvExample(secrets, envs),
		g.genGitignore(),
		g.genReadme(),
	}, nil
}

// Generate writes the Effect project to outDir.
func (g *Generator) Generate(file *ast.File, outDir string) error {
	files, err := g.Files(file)
	if err != nil {
		return err
	}
	return codegen.WriteOutputFiles(outDir, files)
}

// unsupportedFeatures audits declaration contents as well as top-level kinds.
// Admitting an Env node but dropping its default, or accepting a database entry
// without a data layer, would be just as incorrect as silently dropping an
// endpoint.
func (g *Generator) unsupportedFeatures() []string {
	seen := map[string]bool{}
	add := func(feature string) { seen[feature] = true }

	if g.file.Blueprint != nil {
		entryNames := map[string]bool{}
		for _, entry := range g.file.Blueprint.Entries {
			if entryNames[entry.Key] {
				add(fmt.Sprintf("duplicate blueprint entry %q", entry.Key))
			}
			entryNames[entry.Key] = true
			switch entry.Key {
			case "version":
				if _, ok := entry.Value.(*ast.StringLit); !ok {
					add("non-string blueprint version")
				}
			case "port":
				value, ok := entry.Value.(*ast.IntLit)
				if !ok {
					add("non-integer blueprint port")
					continue
				}
				port, err := strconv.Atoi(value.Value)
				if err != nil || port < 1 || port > 65535 {
					add(fmt.Sprintf("blueprint port %q outside 1..65535", value.Value))
				}
			case "runtime":
				value, ok := entry.Value.(*ast.Ident)
				if !ok || value.Name != "node" {
					add("blueprint runtime other than `node`")
				}
			case "database", "cache", "storage":
				add(fmt.Sprintf("blueprint `%s` configuration", entry.Key))
			default:
				add(fmt.Sprintf("blueprint entry %q", entry.Key))
			}
		}
		if len(g.file.Blueprint.Uses) > 0 {
			add("blueprint-level `use` middleware")
		}
	}

	configFields := map[string]string{}
	registerConfigField := func(sourceName string) {
		field := effectConfigField(sourceName)
		if previous, exists := configFields[field]; exists && previous != sourceName {
			add(fmt.Sprintf("configuration names %q and %q both map to %q", previous, sourceName, field))
			return
		}
		configFields[field] = sourceName
	}
	for _, b := range g.file.Blocks {
		switch n := b.(type) {
		case *ast.Secret:
			registerConfigField(n.Name)
			if n.Required && n.Default != nil {
				add(fmt.Sprintf("required secret %q with a default", n.Name))
			}
			if n.Default != nil {
				if _, ok := n.Default.(*ast.StringLit); !ok {
					add(fmt.Sprintf("non-string default for secret %q", n.Name))
				}
			}
		case *ast.Env:
			registerConfigField(n.Name)
			if _, _, err := effectEnvConfig(n.Name, n.Value); err != nil {
				add(fmt.Sprintf("env %q: %s", n.Name, err))
			}
		case *ast.Locale:
			add("`locale` declarations")
		case *ast.Translation:
			add("`translation` declarations")
		case *ast.StateMachine:
			add("`state` machines")
		case *ast.Analytics:
			add("`analytics` declarations")
		case *ast.SaveSchema:
			add("`save` declarations")
		case *ast.Include:
			add("unresolved `include` declarations")
		case *ast.TypeDecl:
			add("`type` declarations")
		case *ast.Alias:
			add("`alias` declarations")
		case *ast.Enum:
			add("`enum` declarations")
		case *ast.Model:
			add("`model` declarations")
		case *ast.Content:
			add("`content` declarations")
		case *ast.Endpoint:
			add("endpoints")
		case *ast.StreamEndpoint:
			add("`STREAM` endpoints")
		case *ast.WsEndpoint:
			add("`WS` endpoints")
		case *ast.Worker:
			add("`worker` declarations")
		case *ast.Schedule:
			add("`schedule` declarations")
		case *ast.Fn:
			add("`fn` declarations")
		case *ast.Pipe:
			add("`pipe` declarations")
		case *ast.Middleware:
			add("`middleware` declarations")
		case *ast.External:
			add("`external` declarations")
		case *ast.Subscribe:
			add("`subscribe` declarations")
		case *ast.Test:
			add("authored `test` declarations")
		case *ast.TestGroup:
			add("`test_group` declarations")
		case *ast.Fixture:
			add("`fixture` declarations")
		default:
			// Future AST declarations must fail closed until the Effect target
			// explicitly decides how to preserve their semantics.
			add(fmt.Sprintf("unsupported declaration %T", b))
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (g *Generator) appName() string {
	if g.file.Blueprint != nil && g.file.Blueprint.Name != "" {
		return common.KebabCase(g.file.Blueprint.Name)
	}
	return "blueprint-effect-app"
}

func (g *Generator) genPackageJSON() codegen.OutputFile {
	content := fmt.Sprintf(`{
  "name": %s,
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "engines": {
    "node": ">=22.9.0"
  },
  "scripts": {
    "build": "tsc -p tsconfig.json",
    "start": "node --env-file-if-exists=.env dist/index.js"
  },
  "dependencies": {
    "effect": %s
  },
  "devDependencies": {
    "@types/node": %s,
    "typescript": %s
  }
}
`, jsonString(g.appName()), jsonString(effectVersion), jsonString(nodeTypesVersion), jsonString(typescriptVersion))
	return codegen.OutputFile{Path: "package.json", Content: []byte(content)}
}

func (g *Generator) genTSConfig() codegen.OutputFile {
	content := `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "strict": true,
    "exactOptionalPropertyTypes": true,
    "noEmitOnError": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true,
    "outDir": "dist",
    "rootDir": "src"
  },
  "include": ["src/**/*.ts"],
  "exclude": ["node_modules", "dist"]
}
`
	return codegen.OutputFile{Path: "tsconfig.json", Content: []byte(content)}
}

// genConfig emits a single typed Effect Config for every accepted
// configuration declaration. Loading AppConfig at startup validates required
// secrets and coerces environment overrides to the type of their Blueprint
// default.
func (g *Generator) genConfig(secrets []*ast.Secret, envs []*ast.Env) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	needsRedacted := false
	for _, secret := range secrets {
		if secret.Default != nil {
			needsRedacted = true
			break
		}
	}
	if needsRedacted {
		b.WriteString(`import { Config, Redacted } from "effect"` + "\n\n")
	} else {
		b.WriteString(`import { Config } from "effect"` + "\n\n")
	}

	b.WriteString("export const AppConfig = Config.all({\n")
	for _, s := range secrets {
		field := effectConfigField(s.Name)
		switch {
		case s.Required:
			fmt.Fprintf(&b, "  %s: Config.redacted(%q),\n", field, s.Name)
		case s.Default != nil:
			value := s.Default.(*ast.StringLit).Value
			fmt.Fprintf(&b, "  %s: Config.redacted(%q).pipe(Config.withDefault(Redacted.make(%s))),\n",
				field, s.Name, jsonString(value))
		default:
			fmt.Fprintf(&b, "  %s: Config.option(Config.redacted(%q)),\n", field, s.Name)
		}
	}
	for _, env := range envs {
		config, _, err := effectEnvConfig(env.Name, env.Value)
		if err != nil {
			// Files validates first. Keep this guard local so future call sites
			// cannot accidentally render a malformed declaration.
			continue
		}
		fmt.Fprintf(&b, "  %s: %s,\n", effectConfigField(env.Name), config)
	}
	b.WriteString("})\n")
	return codegen.OutputFile{Path: "src/config.ts", Content: []byte(b.String())}
}

func (g *Generator) genIndex() codegen.OutputFile {
	port := g.blueprintPort()
	content := fmt.Sprintf(`%simport { createServer, type Server } from "node:http"
import { Effect } from "effect"

import { AppConfig } from "./config.js"

const port = %d
const health = JSON.stringify({
  status: "ok",
  service: %s,
  version: %s,
  target: "effect"
})

const startServer = Effect.tryPromise({
  try: () =>
    new Promise<Server>((resolve, reject) => {
      const server = createServer((request, response) => {
        response.setHeader("content-type", "application/json; charset=utf-8")
        response.setHeader("cache-control", "no-store")

        if (request.method === "GET" && request.url === "/health") {
          response.writeHead(200)
          response.end(health)
          return
        }

        response.writeHead(404)
        response.end(JSON.stringify({ error: "not_found" }))
      })

      server.once("error", reject)
      server.listen(port, "0.0.0.0", () => resolve(server))
    }),
  catch: (error) => error instanceof Error ? error : new Error(String(error))
})

const main = Effect.gen(function* () {
  // Loading the config before binding the socket makes required secrets fail
  // fast and validates every environment override.
  yield* AppConfig
  yield* startServer
  yield* Effect.log(%s)
})

Effect.runPromise(main).catch((error: unknown) => {
  console.error("Blueprint Effect service failed to start", error)
  process.exitCode = 1
})
`, fileHeader(g.sourceFile), port, jsonString(g.appName()), jsonString(g.blueprintVersion()),
		jsonString(fmt.Sprintf("Blueprint Effect service listening on http://0.0.0.0:%d", port)))
	return codegen.OutputFile{Path: "src/index.ts", Content: []byte(content)}
}

func (g *Generator) genEnvExample(secrets []*ast.Secret, envs []*ast.Env) codegen.OutputFile {
	var b strings.Builder
	b.WriteString("# Generated by Blueprint. Copy to .env and adjust for local use.\n")
	b.WriteString("# Required secrets have no value; size defaults are rendered as bytes.\n\n")

	for _, secret := range secrets {
		value := ""
		if defaultValue, ok := secret.Default.(*ast.StringLit); ok {
			value = dotenvValue(defaultValue.Value)
		}
		fmt.Fprintf(&b, "%s=%s\n", secret.Name, value)
	}
	if len(secrets) > 0 && len(envs) > 0 {
		b.WriteString("\n")
	}
	for _, env := range envs {
		_, example, err := effectEnvConfig(env.Name, env.Value)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", env.Name, example)
	}
	return codegen.OutputFile{Path: ".env.example", Content: []byte(b.String())}
}

func (g *Generator) genGitignore() codegen.OutputFile {
	return codegen.OutputFile{
		Path:    ".gitignore",
		Content: []byte("node_modules/\ndist/\n.env\n"),
	}
}

func (g *Generator) genReadme() codegen.OutputFile {
	content := fmt.Sprintf("# %s — Effect target (health scaffold)\n\n"+
		"Generated by Blueprint `--target effect`.\n\n"+
		"This experimental target is runnable, but intentionally health-only. It\n"+
		"loads typed Effect Config for every declared secret and env value, then\n"+
		"serves `GET /health` on port %d. Blueprint rejects authored endpoints,\n"+
		"models, database configuration, and other unsupported declarations before\n"+
		"writing any files.\n\n"+
		"```sh\n"+
		"cp .env.example .env\n"+
		"npm install\n"+
		"npm run build\n"+
		"npm start\n"+
		"```\n\n"+
		"For a complete backend today, use `bp build --target node`. Track Effect\n"+
		"target progress at https://blueprint-lang.dev/multi-target-codegen.\n",
		g.appName(), g.blueprintPort())
	return codegen.OutputFile{Path: "README.md", Content: []byte(content)}
}

func (g *Generator) blueprintPort() int {
	if g.file != nil && g.file.Blueprint != nil {
		for _, entry := range g.file.Blueprint.Entries {
			if entry.Key != "port" {
				continue
			}
			value, ok := entry.Value.(*ast.IntLit)
			if !ok {
				break
			}
			if port, err := strconv.Atoi(value.Value); err == nil {
				return port
			}
			break
		}
	}
	return 3000
}

func (g *Generator) blueprintVersion() string {
	if g.file != nil && g.file.Blueprint != nil {
		for _, entry := range g.file.Blueprint.Entries {
			if entry.Key == "version" {
				if value, ok := entry.Value.(*ast.StringLit); ok {
					return value.Value
				}
			}
		}
	}
	return "0.1.0"
}

func effectConfigField(name string) string {
	return common.CamelCase(strings.ToLower(name))
}

// effectEnvConfig returns the Effect Config expression plus the dotenv
// representation of the Blueprint default. The accepted subset is exhaustive:
// any new expression kind must be deliberately implemented or rejected.
func effectEnvConfig(name string, expr ast.Expr) (string, string, error) {
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return effectEnvConfig(name, paren.Expr)
	}

	switch value := expr.(type) {
	case *ast.StringLit:
		return fmt.Sprintf("Config.string(%q).pipe(Config.withDefault(%s))", name, jsonString(value.Value)),
			dotenvValue(value.Value), nil
	case *ast.IntLit:
		if _, err := strconv.ParseInt(value.Value, 10, 64); err != nil {
			return "", "", fmt.Errorf("invalid integer default %q", value.Value)
		}
		return fmt.Sprintf("Config.integer(%q).pipe(Config.withDefault(%s))", name, value.Value), value.Value, nil
	case *ast.FloatLit:
		if _, err := strconv.ParseFloat(value.Value, 64); err != nil {
			return "", "", fmt.Errorf("invalid number default %q", value.Value)
		}
		return fmt.Sprintf("Config.number(%q).pipe(Config.withDefault(%s))", name, value.Value), value.Value, nil
	case *ast.BoolLit:
		text := strconv.FormatBool(value.Value)
		return fmt.Sprintf("Config.boolean(%q).pipe(Config.withDefault(%s))", name, text), text, nil
	case *ast.SizeLit:
		bytes, err := sizeLiteralBytes(value.Value)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("Config.integer(%q).pipe(Config.withDefault(%s))", name, bytes), bytes, nil
	case *ast.ListExpr:
		return effectListEnvConfig(name, value)
	case nil:
		return "", "", fmt.Errorf("missing default")
	default:
		return "", "", fmt.Errorf("unsupported default expression %T", expr)
	}
}

func effectListEnvConfig(name string, list *ast.ListExpr) (string, string, error) {
	if len(list.Elements) == 0 {
		return "", "", fmt.Errorf("empty list defaults are ambiguous")
	}

	kind := ""
	defaults := make([]string, 0, len(list.Elements))
	examples := make([]string, 0, len(list.Elements))
	for _, element := range list.Elements {
		switch value := element.(type) {
		case *ast.StringLit:
			if kind != "" && kind != "string" {
				return "", "", fmt.Errorf("mixed-type list defaults are unsupported")
			}
			if strings.ContainsAny(value.Value, ",\r\n") {
				return "", "", fmt.Errorf("string list values cannot contain commas or newlines")
			}
			kind = "string"
			defaults = append(defaults, jsonString(value.Value))
			examples = append(examples, value.Value)
		case *ast.IntLit:
			if kind != "" && kind != "integer" && kind != "number" {
				return "", "", fmt.Errorf("mixed-type list defaults are unsupported")
			}
			if _, err := strconv.ParseInt(value.Value, 10, 64); err != nil {
				return "", "", fmt.Errorf("invalid integer list value %q", value.Value)
			}
			if kind == "" {
				kind = "integer"
			}
			defaults = append(defaults, value.Value)
			examples = append(examples, value.Value)
		case *ast.FloatLit:
			if kind != "" && kind != "integer" && kind != "number" {
				return "", "", fmt.Errorf("mixed-type list defaults are unsupported")
			}
			if _, err := strconv.ParseFloat(value.Value, 64); err != nil {
				return "", "", fmt.Errorf("invalid number list value %q", value.Value)
			}
			kind = "number"
			defaults = append(defaults, value.Value)
			examples = append(examples, value.Value)
		case *ast.BoolLit:
			if kind != "" && kind != "boolean" {
				return "", "", fmt.Errorf("mixed-type list defaults are unsupported")
			}
			kind = "boolean"
			text := strconv.FormatBool(value.Value)
			defaults = append(defaults, text)
			examples = append(examples, text)
		default:
			return "", "", fmt.Errorf("unsupported list value %T", element)
		}
	}

	config := fmt.Sprintf("Config.array(Config.%s(), %q).pipe(Config.withDefault([%s]))",
		kind, name, strings.Join(defaults, ", "))
	return config, dotenvValue(strings.Join(examples, ",")), nil
}

func sizeLiteralBytes(value string) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(value))
	factor := int64(1)
	number := lower
	for _, unit := range []struct {
		suffix string
		factor int64
	}{
		{suffix: "gb", factor: 1024 * 1024 * 1024},
		{suffix: "mb", factor: 1024 * 1024},
		{suffix: "kb", factor: 1024},
		{suffix: "b", factor: 1},
	} {
		if strings.HasSuffix(lower, unit.suffix) {
			number = strings.TrimSuffix(lower, unit.suffix)
			factor = unit.factor
			break
		}
	}
	n, err := strconv.ParseInt(number, 10, 64)
	if err != nil || n < 0 || (factor != 0 && n > (1<<63-1)/factor) {
		return "", fmt.Errorf("invalid size default %q", value)
	}
	return strconv.FormatInt(n*factor, 10), nil
}

func dotenvValue(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t#\"'=\r\n") {
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	return value
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func fileHeader(src string) string {
	return fmt.Sprintf("// Generated by Blueprint (effect target) from %s\n"+
		"// Do not edit directly — modify the .bp source and run `bp build`\n\n", src)
}

// Ensure Generator implements codegen.Generator.
var _ codegen.Generator = (*Generator)(nil)
