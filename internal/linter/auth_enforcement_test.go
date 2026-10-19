package linter_test

import (
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/linter"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// ruleCount lints src and counts issues for rule.
func ruleCount(t *testing.T, src, rule string) int {
	t.Helper()
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	n := 0
	for _, iss := range linter.Lint(file) {
		if iss.Rule == rule {
			n++
		}
	}
	return n
}

const authSecretHeader = minimalHeader + "\nsecret JWT_SECRET required\n"

func TestLint_UnenforcedAuth_RESTWithoutUsing_Warns(t *testing.T) {
	src := authSecretHeader + `
@ "List reports"
GET /api/reports {
  auth jwt
  -> 200 "ok"
}
`
	if n := ruleCount(t, src, "unenforced-auth"); n != 1 {
		t.Fatalf("unenforced-auth count = %d, want 1", n)
	}
}

func TestLint_UnenforcedAuth_STREAMWithoutUsing_Warns(t *testing.T) {
	src := authSecretHeader + `
@ "Live reports"
STREAM /api/reports/live {
  auth bearer

  stream {
    |> on timeout(5s) {
      -> { type: "ping" }
    }
  }
}
`
	if n := ruleCount(t, src, "unenforced-auth"); n != 1 {
		t.Fatalf("unenforced-auth count = %d, want 1", n)
	}
}

func TestLint_UnenforcedAuth_WithUsing_NoWarning(t *testing.T) {
	src := authSecretHeader + `
@ "List reports"
GET /api/reports {
  auth jwt using(secret.JWT_SECRET)
  -> 200 "ok"
}
`
	if n := ruleCount(t, src, "unenforced-auth"); n != 0 {
		t.Fatalf("unenforced-auth count = %d, want 0", n)
	}
}

func TestLint_UnenforcedAuth_WebhookSig_NoWarning(t *testing.T) {
	src := authSecretHeader + `
@ "Hook"
POST /api/hook {
  auth webhook_sig using(secret.JWT_SECRET)
  -> 200 "ok"
}
`
	if n := ruleCount(t, src, "unenforced-auth"); n != 0 {
		t.Fatalf("unenforced-auth count = %d, want 0", n)
	}
}

func TestLint_UnenforcedAuth_EndpointUse_NoWarning(t *testing.T) {
	src := authSecretHeader + `
middleware check_token {
  before {
    |> guard header.Authorization -> 401 "Unauthorized"
  }
}

@ "List reports"
GET /api/reports {
  auth bearer
  use check_token
  -> 200 "ok"
}
`
	if n := ruleCount(t, src, "unenforced-auth"); n != 0 {
		t.Fatalf("unenforced-auth count = %d, want 0 when the endpoint has a use middleware", n)
	}
}

func TestLint_UnenforcedAuth_BlueprintUse_NoWarning(t *testing.T) {
	src := authSecretHeader + `
middleware check_token {
  before {
    |> guard header.Authorization -> 401 "Unauthorized"
  }
}

@ "List reports"
GET /api/reports {
  auth bearer
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	// Attach a blueprint-level `use` so the rule sees a global middleware.
	if file.Blueprint == nil {
		t.Fatal("no blueprint block")
	}
	file.Blueprint.Uses = append(file.Blueprint.Uses, &ast.UseStmt{Name: "check_token"})
	for _, iss := range linter.Lint(file) {
		if iss.Rule == "unenforced-auth" {
			t.Fatalf("unexpected unenforced-auth with a blueprint-level use: %v", iss)
		}
	}
}

func TestLint_UnenforceableWsAuth_WithUsing_NoWarning(t *testing.T) {
	src := authSecretHeader + `
@ "Chat"
WS /ws/chat {
  auth jwt using(secret.JWT_SECRET)

  on_connect {
    |> log "connected"
  }
}
`
	if n := ruleCount(t, src, "unenforceable-ws-auth"); n != 0 {
		t.Fatalf("unenforceable-ws-auth count = %d, want 0", n)
	}
}

func TestLint_UnenforceableWsAuth_BareJwt_StillWarns(t *testing.T) {
	src := authSecretHeader + `
@ "Chat"
WS /ws/chat {
  auth jwt

  on_connect {
    |> log "connected"
  }
}
`
	if n := ruleCount(t, src, "unenforceable-ws-auth"); n != 1 {
		t.Fatalf("unenforceable-ws-auth count = %d, want 1", n)
	}
}
