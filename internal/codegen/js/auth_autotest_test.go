package js

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// authTestFilesWithTests generates the project with contract tests enabled, so
// the test/ files (harness, auth helper, and contract tests) are included.
func authTestFilesWithTests(t *testing.T, body string) (map[string]string, []string) {
	t.Helper()
	file, errs := parser.ParseFile("auth.bp", []byte(authTestHeader+body))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	files, err := New().WithGenTests(true).Files(file)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	contents := map[string]string{}
	var paths []string
	for _, f := range files {
		contents[f.Path] = string(f.Content)
		paths = append(paths, f.Path)
	}
	return contents, paths
}

func TestAutoTestEnforcedJwtSignsAndRejectsUnauthenticated(t *testing.T) {
	contents, _ := authTestFilesWithTests(t, "GET /a {\n  auth jwt using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n")
	helper, ok := contents["test/_harness/auth.ts"]
	if !ok {
		t.Fatalf("jwt endpoints need the test/_harness/auth.ts signer")
	}
	if !strings.Contains(helper, "createHmac('sha256', secret)") || !strings.Contains(helper, "alg: 'HS256'") {
		t.Errorf("signer must produce an HS256 token with node:crypto:\n%s", helper)
	}
	test := contents["test/generated/a.test.ts"]
	for _, want := range []string{
		"signTestJwt(process.env.JWT_SECRET ?? '')",
		"expect(res.status).not.toBe(401);",
		"GET /a rejects requests without credentials",
		"expect(res.status).toBe(401);",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("contract test missing %q:\n%s", want, test)
		}
	}
}

func TestAutoTestEnforcedBearerNoJwtHelper(t *testing.T) {
	contents, _ := authTestFilesWithTests(t, "GET /a {\n  auth bearer using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n")
	if _, ok := contents["test/_harness/auth.ts"]; ok {
		t.Errorf("bearer-only projects must not emit the jwt signer")
	}
	test := contents["test/generated/a.test.ts"]
	if !strings.Contains(test, "'Authorization': 'Bearer ' + (process.env.JWT_SECRET ?? '')") {
		t.Errorf("bearer credential not built from the secret:\n%s", test)
	}
}

func TestAutoTestMetadataOnlyAuthHasNoCredentialsOrNegativeCase(t *testing.T) {
	contents, _ := authTestFilesWithTests(t, "GET /a {\n  auth bearer\n  -> 200 \"ok\"\n}\n")
	test := contents["test/generated/a.test.ts"]
	if strings.Contains(test, "rejects requests without credentials") || strings.Contains(test, "process.env.JWT_SECRET") {
		t.Errorf("metadata-only auth must keep the existing test shape:\n%s", test)
	}
}

func TestAutoTestUseMiddlewareSkipsStrictPositiveAssertion(t *testing.T) {
	contents, _ := authTestFilesWithTests(t, `
middleware check_user {
  before {
    |> guard header.Authorization -> 401 "Unauthorized"
  }
}

GET /a {
  auth bearer using(secret.JWT_SECRET)
  use check_user
  -> 200 "ok"
}
`)
	test := contents["test/generated/a.test.ts"]
	if strings.Contains(test, "not.toBe(401)") {
		t.Errorf("a use middleware may answer 401 legitimately; skip the strict assertion:\n%s", test)
	}
	if !strings.Contains(test, "rejects requests without credentials") {
		t.Errorf("the negative 401 case still applies:\n%s", test)
	}
}
