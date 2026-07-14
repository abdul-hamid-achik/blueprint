package effect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func filesByPath(t *testing.T, src string) map[string]string {
	t.Helper()
	file, errs := parser.ParseFile("x.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	files, err := New().Files(file)
	if err != nil {
		t.Fatalf("Files() error: %v", err)
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = string(f.Content)
	}
	return out
}

// The scaffold emits a runnable project + a Config module for a secrets-only
// spec, and lowercases SCREAMING_SNAKE secret names to idiomatic camelCase.
func TestEffect_ScaffoldEmitsConfigForSecrets(t *testing.T) {
	files := filesByPath(t, `blueprint "effect-min" { version "1.0.0" port 3000 runtime node }
secret DATABASE_URL required
secret STRIPE_KEY optional`)

	for _, want := range []string{
		"package.json", "tsconfig.json", "src/config.ts", "src/index.ts",
		".env.example", ".gitignore", "README.md",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("expected scaffold to emit %s", want)
		}
	}
	cfg := files["src/config.ts"]
	if !strings.Contains(cfg, `databaseUrl: Config.redacted("DATABASE_URL")`) {
		t.Errorf("required secret should map to Config.redacted with camelCase field:\n%s", cfg)
	}
	if !strings.Contains(cfg, `stripeKey: Config.option(Config.redacted("STRIPE_KEY"))`) {
		t.Errorf("optional secret should map to Config.option:\n%s", cfg)
	}
	if !strings.Contains(files["package.json"], `"effect-min"`) {
		t.Errorf("package.json should carry the app name")
	}
	packageJSON := files["package.json"]
	for _, want := range []string{
		`"effect": "` + effectVersion + `"`,
		`"typescript": "` + typescriptVersion + `"`,
		`"@types/node": "` + nodeTypesVersion + `"`,
		`node --env-file-if-exists=.env dist/index.js`,
	} {
		if !strings.Contains(packageJSON, want) {
			t.Errorf("package.json should contain %q:\n%s", want, packageJSON)
		}
	}
	for _, unwanted := range []string{"latest", "@effect/platform", "@effect/sql"} {
		if strings.Contains(packageJSON, unwanted) {
			t.Errorf("package.json should not contain unused or unpinned dependency %q:\n%s", unwanted, packageJSON)
		}
	}
	if !strings.Contains(files["src/index.ts"], `request.url === "/health"`) {
		t.Errorf("entrypoint should serve GET /health:\n%s", files["src/index.ts"])
	}
	if !strings.Contains(files["tsconfig.json"], `"noEmitOnError": true`) {
		t.Errorf("TypeScript build must not emit broken output")
	}
}

func TestEffect_ConfigPreservesSecretDefaultsAndTypedEnvDefaults(t *testing.T) {
	files := filesByPath(t, `blueprint "effect-config" { version "1.2.3" port 4310 runtime node }
secret WEBHOOK_SECRET optional default("local secret")
env MAX_FILE_SIZE 10mb
env LOG_LEVEL "info"
env FREE_MONTHLY 50
env RATIO 1.5
env ENABLE_METRICS true
env ALLOWED_TYPES ["image/png", "image with space"]`)

	cfg := files["src/config.ts"]
	for _, want := range []string{
		`webhookSecret: Config.redacted("WEBHOOK_SECRET").pipe(Config.withDefault(Redacted.make("local secret")))`,
		`maxFileSize: Config.integer("MAX_FILE_SIZE").pipe(Config.withDefault(10485760))`,
		`logLevel: Config.string("LOG_LEVEL").pipe(Config.withDefault("info"))`,
		`freeMonthly: Config.integer("FREE_MONTHLY").pipe(Config.withDefault(50))`,
		`ratio: Config.number("RATIO").pipe(Config.withDefault(1.5))`,
		`enableMetrics: Config.boolean("ENABLE_METRICS").pipe(Config.withDefault(true))`,
		`allowedTypes: Config.array(Config.string(), "ALLOWED_TYPES").pipe(Config.withDefault(["image/png", "image with space"]))`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config should contain %q:\n%s", want, cfg)
		}
	}

	envExample := files[".env.example"]
	for _, want := range []string{
		`WEBHOOK_SECRET="local secret"`,
		`MAX_FILE_SIZE=10485760`,
		`LOG_LEVEL=info`,
		`FREE_MONTHLY=50`,
		`RATIO=1.5`,
		`ENABLE_METRICS=true`,
		`ALLOWED_TYPES="image/png,image with space"`,
	} {
		if !strings.Contains(envExample, want) {
			t.Errorf(".env.example should contain %q:\n%s", want, envExample)
		}
	}

	index := files["src/index.ts"]
	for _, want := range []string{"const port = 4310", `version: "1.2.3"`, `service: "effect-config"`} {
		if !strings.Contains(index, want) {
			t.Errorf("entrypoint should preserve blueprint setting %q:\n%s", want, index)
		}
	}
}

func TestEffect_ConfigurationFailuresReturnNoFiles(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "database",
			src:  `blueprint "x" { version "1.0.0" port 3000 runtime node database postgres }`,
			want: "blueprint `database` configuration",
		},
		{
			name: "cache",
			src:  `blueprint "x" { version "1.0.0" port 3000 runtime node cache redis }`,
			want: "blueprint `cache` configuration",
		},
		{
			name: "runtime",
			src:  `blueprint "x" { version "1.0.0" port 3000 runtime bun }`,
			want: "runtime other than",
		},
		{
			name: "required secret default",
			src: `blueprint "x" { version "1.0.0" port 3000 runtime node }
secret BAD required default("unsafe")`,
			want: `required secret "BAD" with a default`,
		},
		{
			name: "duration env",
			src: `blueprint "x" { version "1.0.0" port 3000 runtime node }
env TIMEOUT 5s`,
			want: `env "TIMEOUT": unsupported default expression`,
		},
		{
			name: "empty list env",
			src: `blueprint "x" { version "1.0.0" port 3000 runtime node }
env EMPTY []`,
			want: "empty list defaults are ambiguous",
		},
		{
			name: "mixed list env",
			src: `blueprint "x" { version "1.0.0" port 3000 runtime node }
env MIXED ["a", 1]`,
			want: "mixed-type list defaults are unsupported",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, errs := parser.ParseFile("x.bp", []byte(tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			files, err := New().Files(file)
			if err == nil {
				t.Fatal("expected unsupported configuration to fail")
			}
			if files != nil {
				t.Fatalf("unsupported configuration returned %d files; want none", len(files))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should contain %q; got %v", tc.want, err)
			}
		})
	}
}

// Constructs the scaffold can't emit yet must fail with a clear, actionable
// error rather than producing broken output.
func TestEffect_UnsupportedConstructsError(t *testing.T) {
	file, errs := parser.ParseFile("x.bp", []byte(`blueprint "x" { version "1.0.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model thing { id uuid primary name string required }
@ "list"
GET /api/things {
  |> things = query thing
  -> 200 { things: things }
}`))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	_, err := New().Files(file)
	if err == nil {
		t.Fatal("expected an unsupported-features error for models + endpoints")
	}
	for _, want := range []string{"endpoints", "`model` declarations"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q; got: %v", want, err)
		}
	}
}

func TestEffect_PreviouslySilentDeclarationsFailClosed(t *testing.T) {
	loc := lexer.Loc{File: "x.bp", Line: 2, Col: 1}
	cases := []struct {
		name  string
		block ast.TopLevel
		want  string
	}{
		{name: "enum", block: &ast.Enum{Loc: loc, Name: "plan"}, want: "`enum` declarations"},
		{name: "external", block: &ast.External{Loc: loc, Name: "billing"}, want: "`external` declarations"},
		{name: "test", block: &ast.Test{Loc: loc, Name: "contract"}, want: "authored `test` declarations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := &ast.File{
				Loc:       loc,
				Blueprint: &ast.Blueprint{Loc: loc, Name: "x"},
				Blocks:    []ast.TopLevel{tc.block},
			}
			_, err := New().Files(file)
			if err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should contain %q; got %v", tc.want, err)
			}
		})
	}
}

func TestEffect_GenerateIsManifestAwareAndIdempotent(t *testing.T) {
	file, errs := parser.ParseFile("config.bp", []byte(`blueprint "effect-config" {
  version "1.0.0"
  port 3000
  runtime node
}
env LOG_LEVEL "info"`))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	outDir := t.TempDir()
	gen := New()
	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("first Generate(): %v", err)
	}
	first, err := os.ReadFile(filepath.Join(outDir, ".blueprint", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, path := range []string{"src/index.ts", "src/config.ts", ".env.example", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(path))); err != nil {
			t.Errorf("generated file %s: %v", path, err)
		}
		if !strings.Contains(string(first), `"`+path+`"`) {
			t.Errorf("manifest should own %s:\n%s", path, first)
		}
	}

	if err := gen.Generate(file, outDir); err != nil {
		t.Fatalf("second Generate(): %v", err)
	}
	second, err := os.ReadFile(filepath.Join(outDir, ".blueprint", "manifest.json"))
	if err != nil {
		t.Fatalf("read second manifest: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("Effect generator is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
