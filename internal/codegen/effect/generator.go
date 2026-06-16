// Package effect is a code-generation target that emits TypeScript built on
// Effect (effect-ts): @effect/platform HttpApi for routing, Effect Schema for
// validation, @effect/sql for the database, Config for secrets, and Layers for
// dependency injection.
//
// Status: SCAFFOLD. This generator currently emits the static project shell
// (package.json, tsconfig, a Config module for secrets, README) and reports the
// constructs it does not yet translate via unsupportedFeatures — the same
// staged approach the Python target started with. The endpoint/model emit is
// being designed against a hand-written Effect reference (kept in the
// maintainer's review notes, outside this repo);
// once the idioms are locked, the body emit grows here.
//
// It exists now to (a) validate that the codegen.Generator contract accepts a
// third target cleanly and (b) make `bp build --target effect` a real, wired
// path rather than vaporware.
package effect

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
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
				"  It currently generates the project shell + Config-based secrets.\n"+
				"  Design + roadmap: docs/multi-target-codegen.md.\n"+
				"  Use --target node for the full feature set today.",
			strings.Join(missing, ", "))
	}

	var secrets []*ast.Secret
	for _, b := range g.file.Blocks {
		if s, ok := b.(*ast.Secret); ok {
			secrets = append(secrets, s)
		}
	}

	return []codegen.OutputFile{
		g.genPackageJSON(),
		g.genTSConfig(),
		g.genConfig(secrets),
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

// unsupportedFeatures lists constructs this scaffold cannot emit yet. Secrets
// and env are handled; everything that needs the (in-design) endpoint/model
// emit is reported so the build fails with a clear, actionable message instead
// of producing broken output.
func (g *Generator) unsupportedFeatures() []string {
	seen := map[string]bool{}
	for _, b := range g.file.Blocks {
		switch b.(type) {
		case *ast.Secret, *ast.Env:
			// handled
		case *ast.Model:
			seen["`model` declarations"] = true
		case *ast.Endpoint:
			seen["endpoints"] = true
		case *ast.StreamEndpoint:
			seen["`STREAM` endpoints"] = true
		case *ast.WsEndpoint:
			seen["`WS` endpoints"] = true
		case *ast.Worker:
			seen["`worker` declarations"] = true
		case *ast.Schedule:
			seen["`schedule` declarations"] = true
		case *ast.Fn:
			seen["`fn` declarations"] = true
		case *ast.Pipe:
			seen["`pipe` declarations"] = true
		case *ast.Middleware:
			seen["`middleware` declarations"] = true
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
	// Indicative versions — Effect's @effect/platform HttpApi is still
	// stabilizing; pin deliberately when the generator emits real handlers.
	content := fmt.Sprintf(`{
  "name": %q,
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "tsc",
    "start": "node dist/index.js"
  },
  "dependencies": {
    "effect": "latest",
    "@effect/platform": "latest",
    "@effect/platform-node": "latest",
    "@effect/sql": "latest",
    "@effect/sql-pg": "latest"
  },
  "devDependencies": {
    "typescript": "^5.5.0"
  }
}
`, g.appName())
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
    "skipLibCheck": true,
    "outDir": "dist",
    "rootDir": "src"
  },
  "include": ["src"]
}
`
	return codegen.OutputFile{Path: "tsconfig.json", Content: []byte(content)}
}

// genConfig emits an Effect Config module for the spec's secrets. Config is part
// of the stable Effect core (not the experimental HttpApi surface), so this is
// safe to emit today: each secret reads from the environment and fails fast at
// startup with a typed ConfigError if a required value is missing.
func (g *Generator) genConfig(secrets []*ast.Secret) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`import { Config } from "effect"` + "\n\n")
	if len(secrets) == 0 {
		b.WriteString("// No secrets declared.\n")
		b.WriteString("export const AppConfig = {} as const\n")
		return codegen.OutputFile{Path: "src/config.ts", Content: []byte(b.String())}
	}
	b.WriteString("export const AppConfig = {\n")
	for _, s := range secrets {
		// Secrets are SCREAMING_SNAKE; lowercase before camelCasing so
		// DATABASE_URL → databaseUrl rather than DATABASEURL.
		field := common.CamelCase(strings.ToLower(s.Name))
		if s.Required {
			fmt.Fprintf(&b, "  %s: Config.redacted(%q),\n", field, s.Name)
		} else {
			fmt.Fprintf(&b, "  %s: Config.option(Config.redacted(%q)),\n", field, s.Name)
		}
	}
	b.WriteString("} as const\n")
	return codegen.OutputFile{Path: "src/config.ts", Content: []byte(b.String())}
}

func (g *Generator) genReadme() codegen.OutputFile {
	content := fmt.Sprintf("# %s — Effect target (scaffold)\n\n"+
		"Generated by Blueprint `--target effect`.\n\n"+
		"**This is an early scaffold.** It emits the project shell and a `Config`\n"+
		"module for secrets. Endpoint/model emit (HttpApi + Schema + @effect/sql)\n"+
		"is in design — see the generator contract in `docs/multi-target-codegen.md`\n"+
		"in the Blueprint repo.\n\n"+
		"For a complete backend today, build with `--target node`.\n",
		g.appName())
	return codegen.OutputFile{Path: "README.md", Content: []byte(content)}
}

func fileHeader(src string) string {
	return fmt.Sprintf("// Generated by Blueprint (effect target) from %s\n"+
		"// Do not edit directly — modify the .bp source and run `bp build`\n\n", src)
}

// Ensure Generator implements codegen.Generator.
var _ codegen.Generator = (*Generator)(nil)
