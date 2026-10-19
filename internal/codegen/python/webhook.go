package python

import (
	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// Webhook signature auth on the Python target mirrors the Node target's
// `auth webhook_sig using(secret.NAME)` check (see internal/codegen/js/routes.go):
// HMAC-SHA256 hex over the raw body, header X-Webhook-Signature, an unset or
// empty secret fails closed, and a verified body is parsed into `data`.
// Every rejection answers {"error": ...} through InvalidWebhook.

// endpointWebhookSecret returns the secret NAME of
// `auth webhook_sig using(secret.NAME)`. Bare `auth webhook_sig` is not a
// webhook secret and stays rejected by pythonAuthFeature.
// Input AST: FnCall{Name:"webhook_sig", Args:[FnCall{Name:"using", Args:[FieldAccess{Base:"secret",Field:"KEY"}]}]}
func endpointWebhookSecret(expr ast.Expr) (string, bool) {
	fn, ok := expr.(*ast.FnCall)
	if !ok || fn.Name != "webhook_sig" {
		return "", false
	}
	return usingSecretName(fn)
}

// endpointWebhookSecretMeta returns the webhook secret declared in meta.
func endpointWebhookSecretMeta(meta []*ast.EndpointMeta) (string, bool) {
	for _, m := range meta {
		if m.Kind != "auth" {
			continue
		}
		if secret, ok := endpointWebhookSecret(m.Value); ok {
			return secret, true
		}
	}
	return "", false
}

// hasWebhookSig reports whether any endpoint verifies a webhook signature,
// which is the condition for emitting src/lib/webhook.py.
func hasWebhookSig(endpoints []*ast.Endpoint) bool {
	for _, ep := range endpoints {
		if _, ok := endpointWebhookSecretMeta(ep.Meta); ok {
			return true
		}
	}
	return false
}

// webhookDependency renders the route parameter that verifies the signature
// and binds the parsed payload to `data`. It is a parameter default, not a
// route-level dependency, so the handler receives the payload.
func webhookDependency(secret string) string {
	return "data: Any = Depends(_bp_webhook_signature(" + pyStringLiteral(secret) + "))"
}

// genWebhookPy emits src/lib/webhook.py. It depends only on the standard
// library, FastAPI, and env.
func (g *Generator) genWebhookPy() codegen.OutputFile {
	return codegen.OutputFile{Path: "src/lib/webhook.py", Content: []byte(fileHeader(g.sourceFile) + webhookPySource)}
}

// webhookPySource is the runtime half of `auth webhook_sig`. Python differences
// from src/lib/webhook.ts: the signature must be plain hex with no embedded
// spaces (Node's Buffer.from would stop at junk and accept a valid prefix),
// and NaN/Infinity are rejected in JSON as in auth.py.
const webhookPySource = `import hashlib
import hmac
import json
import re
from typing import Any

from fastapi import Request
from fastapi.responses import JSONResponse

from src.lib.env import env

HEX_RE = re.compile(r"(?:[0-9a-fA-F]{2})*")


class InvalidWebhook(Exception):
    """Raised by require_webhook_signature and answered by invalid_webhook_handler."""

    def __init__(self, status_code: int, message: str) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.message = message


async def invalid_webhook_handler(request: Request, exc: InvalidWebhook) -> JSONResponse:
    return JSONResponse({"error": exc.message}, status_code=exc.status_code)


class WebhookPayload(dict):
    """Parsed webhook JSON. Keys also read as attributes, so data.event.type works as on Node."""

    def __getattribute__(self, name: str) -> Any:
        if not name.startswith("_") and dict.__contains__(self, name):
            return dict.__getitem__(self, name)
        return object.__getattribute__(self, name)


def _reject_constant(name: str) -> None:
    raise ValueError("non-finite JSON number " + name)


def require_webhook_signature(secret_name: str):
    """Return a FastAPI dependency that verifies an HMAC-SHA256 webhook body.

    The raw body is verified before it is parsed. The secret is read from env on
    every request, and an unset or empty secret fails closed with 401.
    """

    async def dependency(request: Request) -> WebhookPayload:
        raw = await request.body()
        secret = getattr(env, secret_name, None)
        if not secret:
            raise InvalidWebhook(401, "Invalid signature")
        presented_hex = request.headers.get("X-Webhook-Signature", "")
        if HEX_RE.fullmatch(presented_hex) is None:
            raise InvalidWebhook(401, "Invalid signature")
        presented = bytes.fromhex(presented_hex)
        expected = hmac.new(secret.encode("utf-8"), raw, hashlib.sha256).digest()
        if not hmac.compare_digest(presented, expected):
            raise InvalidWebhook(401, "Invalid signature")
        try:
            return json.loads(raw.decode("utf-8"), object_hook=WebhookPayload, parse_constant=_reject_constant)
        except (ValueError, RecursionError):
            raise InvalidWebhook(400, "Invalid payload")

    return dependency
`
