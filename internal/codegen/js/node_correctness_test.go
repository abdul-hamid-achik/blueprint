package js

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func parseNodeCorrectnessSource(t *testing.T, src string) []codegen.OutputFile {
	t.Helper()
	file, parseErrs := parser.ParseFile("node-correctness.bp", []byte(src))
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	files, err := New().Files(file)
	if err != nil {
		t.Fatalf("Files() error: %v", err)
	}
	return files
}

func nodeCorrectnessOutput(t *testing.T, files []codegen.OutputFile, path string) string {
	t.Helper()
	for _, file := range files {
		if file.Path == path {
			return string(file.Content)
		}
	}
	t.Fatalf("output %s was not generated", path)
	return ""
}

func TestNodeQueryCoercionIsLexicalAndAliasAware(t *testing.T) {
	files := parseNodeCorrectnessSource(t, `blueprint "query-coercion" {
  version "1.0.0"
  runtime node
}

alias Score = int min(0) max(10)
alias Price = money min(0)

GET /api/search {
  <- active bool required
  <- page int required
  <- price money required
  <- score Score required
  <- minimum_price Price required
  -> 200 { active: active, page: page, price: price, score: score, minimum_price: minimum_price }
}`)

	validation := nodeCorrectnessOutput(t, files, "src/validation/schemas.ts")
	for _, want := range []string{
		`active: z.enum(["true", "false"]).transform((value) => value === "true")`,
		`page: z.coerce.number().int()`,
		`price: z.coerce.number()`,
		`score: z.coerce.number().int().min(0).max(10)`,
		`minimum_price: z.coerce.number().min(0)`,
	} {
		if !strings.Contains(validation, want) {
			t.Errorf("validation schema missing %q\n%s", want, validation)
		}
	}
	if strings.Contains(validation, "z.coerce.boolean()") {
		t.Fatalf("query booleans must not use truthiness coercion\n%s", validation)
	}
}

func TestNodeModelIndexConstraintsGenerateDrizzleIndexes(t *testing.T) {
	files := parseNodeCorrectnessSource(t, `blueprint "model-indexes" {
  version "1.0.0"
  runtime node
}

model account {
  id uuid primary
  email string index
  tenant_id uuid index
}`)

	schema := nodeCorrectnessOutput(t, files, "src/models/schema.ts")
	for _, want := range []string{
		`jsonb, index } from 'drizzle-orm/pg-core'`,
		`}, (table) => [`,
		`index('accounts_email_idx').on(table.email)`,
		`index('accounts_tenant_id_idx').on(table.tenantId)`,
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("model schema missing %q\n%s", want, schema)
		}
	}
}

func TestNodeEndpointFileInputsFailClosed(t *testing.T) {
	tests := []struct {
		name      string
		decl      string
		inputType string
	}{
		{name: "primitive file", inputType: "file"},
		{name: "MIME type", inputType: "image/*"},
		{name: "nested named type", decl: "type Upload { attachment image/* required }\n", inputType: "Upload"},
		{name: "nested model type", decl: "model upload_payload { id uuid primary attachment image/* required }\n", inputType: "upload_payload"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src := `blueprint "file-input" {
  version "1.0.0"
  runtime node
}

` + test.decl + `POST /api/upload {
  <- attachment ` + test.inputType + ` required
  -> 202 { accepted: true }
}`
			file, parseErrs := parser.ParseFile("file-input.bp", []byte(src))
			if len(parseErrs) > 0 {
				t.Fatalf("parse errors: %v", parseErrs)
			}
			files, err := New().Files(file)
			if err == nil || !strings.Contains(err.Error(), "multipart/form-data") {
				t.Fatalf("expected multipart fail-closed error, got files=%d err=%v", len(files), err)
			}
			if len(files) != 0 {
				t.Fatalf("failed generation returned %d files", len(files))
			}
		})
	}
}

func TestNodeOrdinaryEmitGeneratesAndImportsEventRegistry(t *testing.T) {
	files := parseNodeCorrectnessSource(t, `blueprint "local-events" {
  version "1.0.0"
  runtime node
}

POST /api/events {
  <- id uuid required
  |> emit "item.created" { id: id }
  -> 202 { accepted: true }
}

fn publish_event {
  <- id uuid
  -> accepted bool

  logic {
    |> emit "item.published" { id: id }
    -> true
  }
}

schedule heartbeat {
  cron "*/5 * * * *"
  |> emit "system.heartbeat" { alive: true }
}`)

	outputs := map[string]string{
		"src/routes/events.ts":           "await emit('item.created', { id: id });",
		"src/functions/publish-event.ts": "await emit('item.published', { id: id });",
		"src/schedules/heartbeat.ts":     "await emit('system.heartbeat', { alive: true });",
	}
	for path, call := range outputs {
		content := nodeCorrectnessOutput(t, files, path)
		if !strings.Contains(content, "import { emit } from '../lib/events.js';") {
			t.Errorf("%s must import the event helper\n%s", path, content)
		}
		if !strings.Contains(content, call) {
			t.Errorf("%s must await in-process delivery %q\n%s", path, call, content)
		}
	}
	events := nodeCorrectnessOutput(t, files, "src/lib/events.ts")
	if !strings.Contains(events, "export async function emit(") {
		t.Fatalf("ordinary emit must generate the event registry\n%s", events)
	}
}

func TestNodeExternalEmitFailsClosed(t *testing.T) {
	src := `blueprint "external-event" {
  version "1.0.0"
  runtime node
}

external "audit-service" { url: "https://audit.example.com" }

POST /api/events {
  <- id uuid required
  |> emit "item.created" to(audit_service) { id: id }
  -> 202 { accepted: true }
}`
	file, parseErrs := parser.ParseFile("external-event.bp", []byte(src))
	if len(parseErrs) > 0 {
		t.Fatalf("parse errors: %v", parseErrs)
	}
	files, err := New().Files(file)
	if err == nil || !strings.Contains(err.Error(), "does not yet support external emit to(audit_service)") {
		t.Fatalf("expected external emit fail-closed error, got files=%d err=%v", len(files), err)
	}
	if len(files) != 0 {
		t.Fatalf("failed generation returned %d files", len(files))
	}
}

func TestNodePackageUsesPatchedDependencyFloors(t *testing.T) {
	files := parseNodeCorrectnessSource(t, `blueprint "dependency-floors" {
  version "1.0.0"
  runtime node
}

model item {
  id uuid primary
}`)

	packageJSON := nodeCorrectnessOutput(t, files, "package.json")
	for _, want := range []string{
		`"drizzle-orm": "^0.45.2"`,
		`"drizzle-kit": "^0.31.10"`,
		`"vitest": "^4.1.10"`,
	} {
		if !strings.Contains(packageJSON, want) {
			t.Errorf("generated package is missing patched dependency floor %q\n%s", want, packageJSON)
		}
	}
	for _, obsolete := range []string{`"^0.36.0"`, `"^0.28.0"`, `"^2.1.0"`} {
		if strings.Contains(packageJSON, obsolete) {
			t.Errorf("generated package retained obsolete dependency range %s\n%s", obsolete, packageJSON)
		}
	}
}
