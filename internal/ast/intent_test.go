package ast_test

import (
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// TestPrint_IntentsOnEveryTopLevelKind formats a file with an intent above each
// declaration kind that accepts one. Every intent must survive, sit on the line
// directly above its declaration, and the output must be a fixed point.
func TestPrint_IntentsOnEveryTopLevelKind(t *testing.T) {
	src := `blueprint "intents" {
  version "1.0.0"
  runtime node
}

@ "Signing key for session tokens"
secret JWT_SECRET required

@ "Default log level"
env LOG_LEVEL "info"

@ "Default locale for the UI"
locale "en" default

@ "UI strings"
translation Strings {
  key "greeting"
  locale en {
    "greeting": "Hello"
  }
}

@ "Shared model definitions"
include "includes/models.bp"

@ "Mailing address"
type Address {
  street string
  city   string
}

@ "Validated email address"
alias Email = string format(email)

@ "Upstream auth service"
external "auth-service" {
  url: "http://auth:3001"
}

@ "Sample upload used by tests"
fixture "sample.png" from "testdata/sample.png"
`
	file := parseForPrint(t, src)
	if got := ast.IntentCount(file); got != 9 {
		t.Fatalf("source has %d intents, want 9", got)
	}
	formatted := ast.Print(file)

	pairs := []struct{ intent, decl string }{
		{"Signing key for session tokens", `secret JWT_SECRET required`},
		{"Default log level", `env LOG_LEVEL "info"`},
		{"Default locale for the UI", `locale en default`},
		{"UI strings", `translation Strings {`},
		{"Shared model definitions", `include "includes/models.bp"`},
		{"Mailing address", `type Address {`},
		{"Validated email address", `alias Email = string format(email)`},
		{"Upstream auth service", `external "auth-service" {`},
		{"Sample upload used by tests", `fixture "sample.png" from "testdata/sample.png"`},
	}
	for _, p := range pairs {
		requireLines(t, formatted, `@ "`+p.intent+`"`, p.decl)
	}

	reparsed := parseForPrint(t, formatted)
	if got := ast.IntentCount(reparsed); got != 9 {
		t.Fatalf("formatted output has %d intents, want 9\n%s", got, formatted)
	}
	if same, where := ast.SameStructure(file, reparsed); !same {
		t.Fatalf("formatting changed the AST (%s)\n%s", where, formatted)
	}
	if second := ast.Print(reparsed); second != formatted {
		t.Fatalf("formatting is not idempotent\nfirst:\n%s\nsecond:\n%s", formatted, second)
	}
}
