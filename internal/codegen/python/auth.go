package python

import (
	"fmt"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// Endpoint auth on the Python target mirrors the Node target (see
// internal/codegen/js/auth.go). Only `auth <strategy> using(secret.NAME)` is
// enforced; the header-only check runs as a FastAPI route dependency before the
// endpoint's `use` middleware. The Node helpers are unexported in package js,
// so the small pieces needed here are restated instead of importing that package.

// enforcedAuthStrategies are the strategies src/lib/auth.py verifies when they
// carry using(secret.NAME). webhook_sig is verified by src/lib/webhook.py.
var enforcedAuthStrategies = map[string]bool{
	"bearer":  true,
	"jwt":     true,
	"api_key": true,
	"basic":   true,
}

// enforcedAuth is an endpoint `auth` declaration the generated app enforces.
type enforcedAuth struct {
	Strategy string // bearer, jwt, api_key, or basic
	Secret   string // env variable name, the secret's NAME
}

// usingSecretName returns the secret NAME of a strategy written as
// `<strategy>(using(secret.NAME))`. Input AST: FnCall{Name:"using", Args:[FieldAccess{Base:"secret",Field:"KEY"}]}
func usingSecretName(fn *ast.FnCall) (string, bool) {
	if fn == nil || len(fn.Args) == 0 {
		return "", false
	}
	inner, ok := fn.Args[0].(*ast.FnCall)
	if !ok || inner.Name != "using" || len(inner.Args) == 0 {
		return "", false
	}
	fa, ok := inner.Args[0].(*ast.FieldAccess)
	if !ok || fa.Field == "" {
		return "", false
	}
	if base, ok := fa.Base.(*ast.Ident); !ok || base.Name != "secret" {
		return "", false
	}
	return fa.Field, true
}

// endpointEnforcedAuth returns the enforced auth declared by an auth meta value.
// Input AST: FnCall{Name:"jwt", Args:[FnCall{Name:"using", Args:[FieldAccess{Base:"secret",Field:"KEY"}]}]}
func endpointEnforcedAuth(expr ast.Expr) (enforcedAuth, bool) {
	fn, ok := expr.(*ast.FnCall)
	if !ok || !enforcedAuthStrategies[fn.Name] {
		return enforcedAuth{}, false
	}
	secret, ok := usingSecretName(fn)
	if !ok {
		return enforcedAuth{}, false
	}
	return enforcedAuth{Strategy: fn.Name, Secret: secret}, true
}

// endpointEnforcedAuthMeta returns the first enforced auth declared in meta.
func endpointEnforcedAuthMeta(meta []*ast.EndpointMeta) (enforcedAuth, bool) {
	for _, m := range meta {
		if m.Kind != "auth" {
			continue
		}
		if ea, ok := endpointEnforcedAuth(m.Value); ok {
			return ea, true
		}
	}
	return enforcedAuth{}, false
}

// hasEnforcedAuth reports whether any endpoint enforces auth, which is the
// condition for emitting src/lib/auth.py.
func hasEnforcedAuth(endpoints []*ast.Endpoint) bool {
	for _, ep := range endpoints {
		if _, ok := endpointEnforcedAuthMeta(ep.Meta); ok {
			return true
		}
	}
	return false
}

// pythonAuthFeature validates one `auth` metadata entry. It returns "" for an
// enforced declaration and a specific rejection reason for everything else.
// Bare strategies fail closed: Python never runs an unverified auth line.
func pythonAuthFeature(meta *ast.EndpointMeta) string {
	if _, ok := endpointEnforcedAuth(meta.Value); ok {
		return ""
	}
	if _, ok := endpointWebhookSecret(meta.Value); ok {
		return ""
	}
	name := ""
	switch v := meta.Value.(type) {
	case *ast.FnCall:
		name = v.Name
	case *ast.Ident:
		name = v.Name
	}
	switch {
	case name == "webhook_sig":
		return "endpoint `auth webhook_sig` without `using(secret.NAME)` (add `using(secret.NAME)`; Python does not verify bare webhook auth)"
	case enforcedAuthStrategies[name]:
		return fmt.Sprintf("endpoint `auth %s` without `using(secret.NAME)` (add `using(secret.NAME)`; Python does not enforce bare auth)", name)
	default:
		return "endpoint `auth` metadata that Python does not enforce (use `auth bearer|jwt|api_key|basic using(secret.NAME)`)"
	}
}

// endpointJwtClaims reports whether the endpoint's first enforced auth is jwt.
// Such an endpoint receives its verified payload as the `claims` handler
// parameter, and `claims` in its body is read with dict lookups (see claimsFieldPy).
func endpointJwtClaims(meta []*ast.EndpointMeta) bool {
	ea, ok := endpointEnforcedAuthMeta(meta)
	return ok && ea.Strategy == "jwt"
}

// isClaimsAccess reports whether expr is a field chain rooted at claims.
func isClaimsAccess(expr ast.Expr) bool {
	return isFieldChainRootedAt(expr, "claims")
}

// isFieldChainRootedAt reports whether expr is a field chain such as a.b.c
// whose root identifier is name.
func isFieldChainRootedAt(expr ast.Expr, name string) bool {
	for {
		switch v := expr.(type) {
		case *ast.FieldAccess:
			expr = v.Base
		case *ast.Ident:
			return v.Name == name
		default:
			return false
		}
	}
}

// claimsFieldPy renders a field chain rooted at claims as dict lookups, so keys
// stay verbatim (claims.user_id -> claims.get("user_id")). Nested access stays
// safe when an object claim is missing: claims.org.id renders as
// (claims.get("org") or {}).get("id"). The checker guarantees the root is the
// claims parameter of an auth jwt endpoint.
func claimsFieldPy(v *ast.FieldAccess) string {
	return dictFieldChainPy("claims", v)
}

// dictFieldChainPy renders a field chain rooted at the dict variable root as
// .get lookups, the way claimsFieldPy does. A key reads verbatim even when it
// names a dict method, such as items, as it does on Node.
func dictFieldChainPy(root string, v *ast.FieldAccess) string {
	base := root
	if inner, ok := v.Base.(*ast.FieldAccess); ok {
		base = "(" + dictFieldChainPy(root, inner) + " or {})"
	}
	return base + ".get(" + pyStringLiteral(v.Field) + ")"
}

// requireAuthDependency renders the route dependency that verifies an enforced
// auth declaration. It is imported under an alias so a user middleware named
// require_auth cannot collide with it.
func requireAuthDependency(ea enforcedAuth, blueprintName string) string {
	return fmt.Sprintf("Depends(_bp_require_auth(%q, %q, %q))", ea.Strategy, ea.Secret, blueprintName)
}

// blueprintNameForAuth returns the blueprint name used as the realm of Basic
// challenges.
func (g *Generator) blueprintNameForAuth() string {
	if g.file == nil || g.file.Blueprint == nil {
		return ""
	}
	return g.file.Blueprint.Name
}

// genAuthPy emits src/lib/auth.py. It is emitted only when some endpoint
// enforces auth, and it depends only on the standard library, FastAPI, and env.
func (g *Generator) genAuthPy() codegen.OutputFile {
	return codegen.OutputFile{Path: "src/lib/auth.py", Content: []byte(fileHeader(g.sourceFile) + authPySource)}
}

// authPySource mirrors the Node src/lib/auth.ts. Python differences handled
// here: strict base64url segments (base64 silently drops other characters),
// NaN/Infinity rejected in JSON, bool is not a numeric claim, and malformed
// input of any kind is a failure rather than an exception.
const authPySource = `import base64
import binascii
import hashlib
import hmac
import json
import math
import re
import time
from typing import Any

from fastapi import Request
from fastapi.responses import JSONResponse

from src.lib.env import env

BEARER_RE = re.compile(r"Bearer\s+(\S+)\s*", re.IGNORECASE)
BASIC_RE = re.compile(r"Basic\s+([A-Za-z0-9+/]+={0,2})\s*", re.IGNORECASE)
BASE64URL_RE = re.compile(r"[A-Za-z0-9_-]+")


class Unauthorized(Exception):
    """Raised by require_auth and answered by unauthorized_handler."""

    def __init__(self, challenge: str | None) -> None:
        super().__init__("Unauthorized")
        self.challenge = challenge


async def unauthorized_handler(request: Request, exc: Unauthorized) -> JSONResponse:
    headers = {"WWW-Authenticate": exc.challenge} if exc.challenge else None
    return JSONResponse({"error": "Unauthorized"}, status_code=401, headers=headers)


def _same_secret(presented: bytes, expected: bytes) -> bool:
    # Both sides are hashed first, so compare_digest always sees equal lengths.
    return hmac.compare_digest(hashlib.sha256(presented).digest(), hashlib.sha256(expected).digest())


def _b64url_decode(segment: str) -> bytes:
    if BASE64URL_RE.fullmatch(segment) is None:
        raise ValueError("not base64url")
    return base64.urlsafe_b64decode(segment + "=" * (-len(segment) % 4))


def _reject_constant(name: str) -> None:
    raise ValueError("non-finite JSON number " + name)


def _json_object(raw: bytes) -> dict:
    value = json.loads(raw.decode("utf-8"), parse_constant=_reject_constant)
    if not isinstance(value, dict):
        raise ValueError("JWT segment is not a JSON object")
    return value


def _numeric_claim(value: object) -> bool:
    if isinstance(value, bool):
        return False
    if isinstance(value, int):
        return True
    return isinstance(value, float) and math.isfinite(value)


def _verify_jwt(token: str, secret: bytes) -> dict[str, Any] | None:
    """Return the verified payload of an HS256 token, or None."""
    parts = token.split(".")
    if len(parts) != 3:
        return None
    header_part, payload_part, signature_part = parts
    try:
        header = _json_object(_b64url_decode(header_part))
        payload = _json_object(_b64url_decode(payload_part))
        signature = _b64url_decode(signature_part)
    except (ValueError, RecursionError):
        return None
    if header.get("alg") != "HS256":
        return None
    expected = hmac.new(secret, (header_part + "." + payload_part).encode("ascii"), hashlib.sha256).digest()
    if not _same_secret(signature, expected):
        return None
    now = math.floor(time.time())
    if "exp" in payload and (not _numeric_claim(payload["exp"]) or payload["exp"] <= now):
        return None
    if "nbf" in payload and (not _numeric_claim(payload["nbf"]) or payload["nbf"] > now):
        return None
    return payload


def _jwt_claims(request: Request, secret: bytes) -> dict[str, Any] | None:
    token = _bearer_token(request)
    return _verify_jwt(token, secret) if token is not None else None


def _bearer_token(request: Request) -> str | None:
    header = request.headers.get("Authorization")
    if header is None:
        return None
    match = BEARER_RE.fullmatch(header)
    return match.group(1) if match else None


def _basic_credential(request: Request) -> bytes | None:
    header = request.headers.get("Authorization")
    if header is None:
        return None
    match = BASIC_RE.fullmatch(header)
    if match is None:
        return None
    raw = match.group(1).rstrip("=")
    try:
        return base64.b64decode(raw + "=" * (-len(raw) % 4), validate=True)
    except binascii.Error:
        return None


def _verify(request: Request, strategy: str, secret: str) -> bool:
    expected = secret.encode("utf-8")
    if strategy == "bearer":
        token = _bearer_token(request)
        return token is not None and _same_secret(token.encode("utf-8"), expected)
    if strategy == "api_key":
        key = request.headers.get("X-API-Key")
        return bool(key) and _same_secret(key.encode("utf-8"), expected)
    if strategy == "basic":
        credential = _basic_credential(request)
        return credential is not None and _same_secret(credential, expected)
    return False


def _challenge(strategy: str, realm: str) -> str | None:
    if strategy in ("bearer", "jwt"):
        return "Bearer"
    if strategy == "basic":
        safe_realm = realm.replace('"', "").replace("\\", "")
        return 'Basic realm="' + safe_realm + '"'
    return None


def require_auth(strategy: str, secret_name: str, realm: str):
    """Return a FastAPI dependency that rejects requests failing the check.

    The secret is read from env on every request. An unset or empty secret
    fails closed. For jwt the dependency returns the verified payload (the
    endpoint claims value); every other strategy returns None. Dependencies run
    before body validation, so an invalid body from an unauthenticated caller
    gets 401, not 422.
    """
    challenge = _challenge(strategy, realm)

    def dependency(request: Request) -> dict[str, Any] | None:
        secret = getattr(env, secret_name, None)
        if not secret:
            raise Unauthorized(challenge)
        if strategy == "jwt":
            claims = _jwt_claims(request, secret.encode("utf-8"))
            if claims is None:
                raise Unauthorized(challenge)
            return claims
        if not _verify(request, strategy, secret):
            raise Unauthorized(challenge)
        return None

    return dependency
`
