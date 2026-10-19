package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const authBlueprint = `blueprint "shop" { version "1.0" port 3000 runtime node }
secret API_KEY
secret SIGNING_KEY
GET /api/guard {
  auth bearer using(secret.API_KEY)
  -> 200 "ok"
}
POST /api/guard {
  auth jwt using(secret.SIGNING_KEY)
  tags ["auth"]
  -> 200 "ok"
}
GET /api/open {
  -> 200 "ok"
}
`

func TestPython_AuthEmitsVerifierAndEnforcedRoutes(t *testing.T) {
	outDir := buildPython(t, authBlueprint)
	authPy := readPy(t, outDir, "src/lib/auth.py")
	for _, want := range []string{
		"hmac.compare_digest(hashlib.sha256(presented).digest(), hashlib.sha256(expected).digest())",
		`BEARER_RE = re.compile(r"Bearer\s+(\S+)\s*", re.IGNORECASE)`,
		`BASIC_RE = re.compile(r"Basic\s+([A-Za-z0-9+/]+={0,2})\s*", re.IGNORECASE)`,
		`BASE64URL_RE = re.compile(r"[A-Za-z0-9_-]+")`,
		"parse_constant=_reject_constant",
		"validate=True",
		"isinstance(value, bool)",
		`JSONResponse({"error": "Unauthorized"}, status_code=401, headers=headers)`,
		`if header.get("alg") != "HS256":`,
		`getattr(env, secret_name, None),`,
		`if not secret:`,
		`if claims is None:`,
		`def require_auth(strategy: str, secret_name: str, realm: str, allow_query: bool = False):`,
	} {
		if !strings.Contains(authPy, want) {
			t.Errorf("src/lib/auth.py missing %q", want)
		}
	}

	routes := readPy(t, outDir, "src/routes/guard.py")
	if strings.Contains(routes, "allow_query") {
		t.Errorf("REST routes must not read query-string credentials")
	}
	for _, want := range []string{
		`@router.get("/api/guard", dependencies=[Depends(_bp_require_auth("bearer", "API_KEY", "shop"))], responses={401: {"description": "Unauthorized"}})`,
		`@router.post("/api/guard", tags=["auth"], responses={401: {"description": "Unauthorized"}})`,
		`async def post_guard(claims: Annotated[dict[str, Any], Depends(_bp_require_auth("jwt", "SIGNING_KEY", "shop"))]):`,
		"from src.lib.auth import require_auth as _bp_require_auth",
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("routes missing %q:\n%s", want, routes)
		}
	}
	if strings.Contains(routes, `@router.get("/api/open", dependencies`) {
		t.Errorf("unauthenticated endpoint must not get a dependency")
	}

	app := readPy(t, outDir, "src/app.py")
	if !strings.Contains(app, "app.add_exception_handler(Unauthorized, unauthorized_handler)") {
		t.Errorf("app.py must register the Unauthorized handler:\n%s", app)
	}
}

func TestPython_NoAuthFileWithoutEnforcedAuth(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/open {
  -> 200 "ok"
}
`)
	if _, err := os.Stat(filepath.Join(outDir, "src/lib/auth.py")); err == nil {
		t.Errorf("src/lib/auth.py must not be emitted without enforced auth")
	}
	app := readPy(t, outDir, "src/app.py")
	if strings.Contains(app, "unauthorized_handler") {
		t.Errorf("app.py must not register auth handling without enforced auth")
	}
}

func TestPython_AuthRejectListAcceptsOnlyEnforcedForms(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"bare jwt": {
			src:  "GET /api/x {\n  auth jwt\n  -> 200 \"ok\"\n}\n",
			want: "endpoint `auth jwt` without `using(secret.NAME)`",
		},
		"bare session": {
			src:  "GET /api/x {\n  auth session\n  -> 200 \"ok\"\n}\n",
			want: "endpoint `auth` metadata that Python does not enforce",
		},
		"bare webhook_sig": {
			src:  "GET /api/x {\n  auth webhook_sig\n  -> 200 \"ok\"\n}\n",
			want: "endpoint `auth webhook_sig` without `using(secret.NAME)`",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			src := `blueprint "x" { version "1.0" port 3000 runtime node }
secret SIGNING_KEY
` + tc.src
			file, errs := parser.ParseFile("t.bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected a rejection containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestPython_AuthGenTestsSendCredentialsAndAssertUnauthorized(t *testing.T) {
	file, errs := parser.ParseFile("t.bp", []byte(authBlueprint))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := python.New().WithGenTests(true).Generate(file, outDir); err != nil {
		t.Fatalf("python generate: %v", err)
	}
	conftest := readPy(t, outDir, "tests/conftest.py")
	for _, want := range []string{
		`os.environ["API_KEY"] = "bp-test-secret"`,
		`os.environ["SIGNING_KEY"] = "bp-test-secret"`,
	} {
		if !strings.Contains(conftest, want) {
			t.Errorf("tests/conftest.py missing fixed test secret %q", want)
		}
	}
	harness := readPy(t, outDir, "tests/_harness/auth.py")
	for _, want := range []string{"def sign_test_jwt(", "def auth_headers(strategy: str, name: str)", `if strategy == "api_key":`} {
		if !strings.Contains(harness, want) {
			t.Errorf("tests/_harness/auth.py missing %q", want)
		}
	}
	tests := readPy(t, outDir, "tests/test_guard.py")
	for _, want := range []string{
		"from tests._harness.auth import auth_headers",
		`res = client.post("/api/guard", headers=auth_headers("jwt", "SIGNING_KEY"))`,
		`res = client.get("/api/guard", headers=auth_headers("bearer", "API_KEY"))`,
		"def test_get_api_guard_rejects_without_credentials(client: TestClient) -> None:",
		`res = client.get("/api/guard")`,
		"assert res.status_code == 401",
	} {
		if !strings.Contains(tests, want) {
			t.Errorf("tests/test_guard.py missing %q:\n%s", want, tests)
		}
	}
}

func TestPython_AuthComposesWithUseMiddleware(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
secret API_KEY
middleware require_header {
  before {
    |> guard header.Tenant -> 403 "Missing tenant"
  }
}
GET /api/tenant {
  auth bearer using(secret.API_KEY)
  use require_header
  -> 200 "ok"
}
`)
	routes := readPy(t, outDir, "src/routes/tenant.py")
	for _, want := range []string{
		"from fastapi import APIRouter, Depends\n",
		"from src.lib.auth import require_auth as _bp_require_auth\n",
		"from src.middleware.require_header import require_header\n",
		`@router.get("/api/tenant", dependencies=[Depends(_bp_require_auth("bearer", "API_KEY", "x"))], responses={401: {"description": "Unauthorized"}})`,
		"require_header = Depends(require_header)",
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("tenant route missing %q:\n%s", want, routes)
		}
	}
	if strings.Count(routes, "from fastapi import") != 1 {
		t.Errorf("expected one fastapi import line, got:\n%s", routes)
	}
}
