package lsp

import (
	"strings"
	"testing"
)

func TestComputedField_HoverDefinitionAndModelSummary(t *testing.T) {
	const uri = "file:///tmp/computed-lsp.bp"
	source := `blueprint "demo" {}
model user {
  id uuid primary
  first_name string required
  computed display_name string = first_name + "!"
}
GET /users/:id {
  <- id uuid required
  |> user = fetch user(id)
  -> 200 user.display_name
}
`
	idx := buildIndex(uri, source)
	if len(idx.errors) != 0 {
		t.Fatalf("parse errors: %+v", idx.errors)
	}

	line, col := indexOf(source, "user.display_name")
	hover := computeHover(idx, line, col+len("user."))
	if !strings.Contains(hover, "user.display_name") || !strings.Contains(hover, "computed, read-only") {
		t.Fatalf("unexpected computed-field hover: %q", hover)
	}

	definition := computeDefinition(uri, idx, line, col+len("user."))
	if definition == nil {
		t.Fatal("computed field definition is nil")
	}
	rng := definition["range"].(lspRange)
	declarationLine := strings.Split(source, "\n")[rng.Start.Line]
	if selected := declarationLine[rng.Start.Character:rng.End.Character]; selected != "display_name" {
		t.Fatalf("definition selected %q instead of display_name (range=%+v)", selected, rng)
	}

	modelLine, modelCol := indexOf(source, "model user")
	modelSummary := computeHover(idx, modelLine, modelCol+len("model "))
	if !strings.Contains(modelSummary, "Computed fields:") || !strings.Contains(modelSummary, "`display_name` string *(computed)*") {
		t.Fatalf("model hover omits computed field: %q", modelSummary)
	}
}

func TestDefinitionRangeSelectsDeclarationNameNotKeyword(t *testing.T) {
	const uri = "file:///tmp/definition-range.bp"
	source := `blueprint "demo" {}
model account { id uuid primary }
GET /accounts { |> rows = query account -> 200 { rows: rows } }
`
	idx := buildIndex(uri, source)
	line, col := indexOf(source, "query account")
	definition := computeDefinition(uri, idx, line, col+len("query "))
	if definition == nil {
		t.Fatal("model definition is nil")
	}
	rng := definition["range"].(lspRange)
	declarationLine := strings.Split(source, "\n")[rng.Start.Line]
	if selected := declarationLine[rng.Start.Character:rng.End.Character]; selected != "account" {
		t.Fatalf("definition selected %q instead of account (range=%+v)", selected, rng)
	}
}
