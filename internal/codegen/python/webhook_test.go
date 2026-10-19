package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const webhookBlueprint = `blueprint "hooks" { version "1.0" port 3000 runtime node }
secret WEBHOOK_KEY
POST /hooks/stripe {
  auth webhook_sig using(secret.WEBHOOK_KEY)
  -> 200 { ok: true }
}
POST /hooks/echo {
  auth webhook_sig using(secret.WEBHOOK_KEY)
  -> 200 { kind: data.type, items: data.items, customer: data.customer.id }
}
`

func TestPython_WebhookSigEmitsVerifierAndDataParam(t *testing.T) {
	outDir := buildPython(t, webhookBlueprint)
	webhookPy := readPy(t, outDir, "src/lib/webhook.py")
	for _, want := range []string{
		`hmac.new(secret.encode("utf-8"), raw, hashlib.sha256).digest()`,
		"hmac.compare_digest(presented, expected)",
		"async def dependency(request: Request)",
		"raw = await request.body()",
		"if not secret:\n            raise InvalidWebhook(401, \"Invalid signature\")",
		`request.headers.get("X-Webhook-Signature", "")`,
		`HEX_RE = re.compile(r"(?:[0-9a-fA-F]{2})*")`,
		`json.loads(raw.decode("utf-8"), parse_constant=_reject_constant)`,
		`raise InvalidWebhook(400, "Invalid payload")`,
		`return JSONResponse({"error": exc.message}, status_code=exc.status_code)`,
	} {
		if !strings.Contains(webhookPy, want) {
			t.Errorf("src/lib/webhook.py missing %q:\n%s", want, webhookPy)
		}
	}

	routes := readPy(t, outDir, "src/routes/hooks.py")
	for _, want := range []string{
		`@router.post("/hooks/stripe")`,
		`async def post_hooks_stripe(data: Any = Depends(_bp_webhook_signature("WEBHOOK_KEY"))):`,
		// Payload reads are dict lookups: keys stay verbatim even when they name
		// a dict method (items), and a missing nested object reads as None.
		`{"kind": data.get("type"), "items": data.get("items"), "customer": (data.get("customer") or {}).get("id")}`,
		"from src.lib.webhook import require_webhook_signature as _bp_webhook_signature",
		"from typing import Any",
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("hooks routes missing %q:\n%s", want, routes)
		}
	}
	if strings.Contains(routes, "dependencies=") {
		t.Errorf("webhook_sig must not add a route-level auth dependency:\n%s", routes)
	}

	app := readPy(t, outDir, "src/app.py")
	for _, want := range []string{
		"from src.lib.webhook import InvalidWebhook, invalid_webhook_handler",
		"app.add_exception_handler(InvalidWebhook, invalid_webhook_handler)",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.py missing %q:\n%s", want, app)
		}
	}
	if strings.Contains(app, "unauthorized_handler") {
		t.Errorf("app.py must not register bearer/jwt auth handling for webhook_sig alone")
	}
}

func TestPython_NoWebhookFileWithoutWebhookSig(t *testing.T) {
	outDir := buildPython(t, authBlueprint)
	if _, err := os.Stat(filepath.Join(outDir, "src/lib/webhook.py")); err == nil {
		t.Errorf("src/lib/webhook.py must not be emitted without webhook_sig")
	}
	if app := readPy(t, outDir, "src/app.py"); strings.Contains(app, "InvalidWebhook") {
		t.Errorf("app.py must not register webhook handling without webhook_sig")
	}
}

func TestPython_WebhookSigGenTestsExpectUnsignedRejection(t *testing.T) {
	file, errs := parser.ParseFile("t.bp", []byte(webhookBlueprint))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := python.New().WithGenTests(true).Generate(file, outDir); err != nil {
		t.Fatalf("python generate: %v", err)
	}
	tests := readPy(t, outDir, "tests/test_hooks.py")
	for _, want := range []string{
		"def test_post_hooks_stripe_rejects_without_credentials(client: TestClient) -> None:",
		`res = client.post("/hooks/stripe")`,
	} {
		if !strings.Contains(tests, want) {
			t.Errorf("tests/test_hooks.py missing %q:\n%s", want, tests)
		}
	}
	if !strings.Contains(tests, "401") {
		t.Errorf("tests/test_hooks.py must allow 401 for unsigned webhook requests:\n%s", tests)
	}
}

func TestPython_WebhookSigRejectsBareForms(t *testing.T) {
	cases := map[string]string{
		"no using": "POST /hooks/x {\n  auth webhook_sig\n  -> 200 \"ok\"\n}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte("blueprint \"x\" { version \"1.0\" port 3000 runtime node }\nsecret WEBHOOK_KEY\n"+src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			if err := python.New().Generate(file, t.TempDir()); err == nil {
				t.Fatalf("expected Python to reject %s", name)
			}
		})
	}
}
