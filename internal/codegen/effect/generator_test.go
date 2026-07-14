package effect

import (
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

// The scaffold emits the project shell + a Config module for a secrets-only
// spec, and lowercases SCREAMING_SNAKE secret names to idiomatic camelCase.
func TestEffect_ScaffoldEmitsConfigForSecrets(t *testing.T) {
	files := filesByPath(t, `blueprint "effect-min" { version "1.0.0" port 3000 runtime node }
secret DATABASE_URL required
secret STRIPE_KEY optional`)

	for _, want := range []string{"package.json", "tsconfig.json", "src/config.ts", "README.md"} {
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
