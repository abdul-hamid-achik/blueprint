package js

import (
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// genAuthLib emits src/lib/auth.ts, the verification module behind
// `auth <strategy> using(secret.NAME)` for bearer, jwt, api_key, and basic.
// It is emitted only when some REST, STREAM, or WS endpoint declares one of
// those (see hasEnforcedAuth). Routes wire it in with requireAuth(...).
//
// Secrets are compared as SHA-256 digests with timingSafeEqual, so unequal
// lengths do not leak through timing. An unset or empty secret, or an empty
// presented credential, always fails closed. JWT verification accepts HS256
// only and rejects every other algorithm, including "none".
func (g *Generator) genAuthLib() codegen.OutputFile {
	src := fileHeader(g.sourceFile) + authLibSource
	return codegen.OutputFile{Path: "src/lib/auth.ts", Content: []byte(src)}
}

const authLibSource = `import { createHash, createHmac, timingSafeEqual } from 'node:crypto';
import type { Context, MiddlewareHandler } from 'hono';

// Strategies this module verifies. The secret holds the expected credential:
// the token for bearer, the HS256 signing key for jwt, the key for api_key,
// and "user:password" for basic.
export type AuthStrategy = 'bearer' | 'jwt' | 'api_key' | 'basic';

// JwtClaims is the verified payload of an auth jwt endpoint. requireAuth stores
// it on the request context as "claims", and the handler reads it from there.
export type JwtClaims = Record<string, any>;

export interface RequireAuthOptions {
  strategy: AuthStrategy;
  // The secret from env.ts. An unset or empty value fails closed.
  secret: string | undefined;
  // STREAM and WS endpoints also accept ?token= (bearer, jwt) and ?api_key=
  // (api_key), because browsers cannot set headers on EventSource or WebSocket.
  allowQuery: boolean;
  // Used in the Basic challenge: WWW-Authenticate: Basic realm="<realm>".
  realm: string;
}

const BASE64URL = /^[A-Za-z0-9_-]+$/;

// Both sides are hashed first, so timingSafeEqual always compares equal-length
// buffers and the length of the secret does not leak.
function sameSecret(presented: string | Buffer, expected: string | Buffer): boolean {
  const a = createHash('sha256').update(presented).digest();
  const b = createHash('sha256').update(expected).digest();
  return timingSafeEqual(a, b);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function bearerCredential(c: Context, allowQuery: boolean): string | undefined {
  const header = c.req.header('Authorization');
  if (header !== undefined) {
    const match = /^Bearer\s+(\S+)\s*$/i.exec(header);
    return match ? match[1] : undefined;
  }
  return allowQuery ? c.req.query('token') : undefined;
}

function basicCredential(c: Context): string | undefined {
  const header = c.req.header('Authorization');
  if (header === undefined) return undefined;
  const match = /^Basic\s+([A-Za-z0-9+/]+={0,2})\s*$/i.exec(header);
  if (!match) return undefined;
  return Buffer.from(match[1], 'base64').toString('utf8');
}

function apiKeyCredential(c: Context, allowQuery: boolean): string | undefined {
  const header = c.req.header('X-API-Key');
  if (header !== undefined) return header;
  return allowQuery ? c.req.query('api_key') : undefined;
}

// verifyJwt accepts only compact HS256 tokens. Each segment must be strict
// base64url (Node's decoder silently skips other characters), the header must
// name HS256, the payload must be a JSON object, and the signature must match.
// exp and nbf are checked against the current time when present. It returns
// the verified payload, or undefined when any check fails.
function verifyJwt(token: string, secret: string): JwtClaims | undefined {
  const parts = token.split('.');
  if (parts.length !== 3) return undefined;
  const [headerPart, payloadPart, signaturePart] = parts;
  if (!BASE64URL.test(headerPart) || !BASE64URL.test(payloadPart) || !BASE64URL.test(signaturePart)) {
    return undefined;
  }

  let header: unknown;
  let payload: unknown;
  try {
    header = JSON.parse(Buffer.from(headerPart, 'base64url').toString('utf8'));
    payload = JSON.parse(Buffer.from(payloadPart, 'base64url').toString('utf8'));
  } catch {
    return undefined;
  }
  if (!isRecord(header) || header.alg !== 'HS256') return undefined;
  if (!isRecord(payload)) return undefined;

  const expected = createHmac('sha256', secret).update(headerPart + '.' + payloadPart).digest();
  const actual = Buffer.from(signaturePart, 'base64url');
  if (!sameSecret(actual, expected)) return undefined;

  const now = Math.floor(Date.now() / 1000);
  if (payload.exp !== undefined && (typeof payload.exp !== 'number' || !Number.isFinite(payload.exp) || payload.exp <= now)) {
    return undefined;
  }
  if (payload.nbf !== undefined && (typeof payload.nbf !== 'number' || !Number.isFinite(payload.nbf) || payload.nbf > now)) {
    return undefined;
  }
  return payload;
}

// verifiedClaims reads the jwt credential and verifies it against the secret.
function verifiedClaims(c: Context, options: RequireAuthOptions): JwtClaims | undefined {
  const { secret, allowQuery } = options;
  if (!secret) return undefined;
  const token = bearerCredential(c, allowQuery);
  return token ? verifyJwt(token, secret) : undefined;
}

function verify(c: Context, options: RequireAuthOptions): boolean {
  const { strategy, secret, allowQuery } = options;
  if (!secret) return false;

  switch (strategy) {
    case 'bearer': {
      const token = bearerCredential(c, allowQuery);
      return !!token && sameSecret(token, secret);
    }
    case 'jwt':
      return verifiedClaims(c, options) !== undefined;
    case 'api_key': {
      const key = apiKeyCredential(c, allowQuery);
      return !!key && sameSecret(key, secret);
    }
    case 'basic': {
      const credential = basicCredential(c);
      return !!credential && sameSecret(credential, secret);
    }
    default:
      return false;
  }
}

// challenge is the WWW-Authenticate value for a 401. api_key has no standard
// scheme, so it sends none.
function challenge(options: RequireAuthOptions): string | undefined {
  switch (options.strategy) {
    case 'bearer':
    case 'jwt':
      return 'Bearer';
    case 'basic':
      return 'Basic realm="' + options.realm.replace(/["\\]/g, '') + '"';
    default:
      return undefined;
  }
}

// unauthorized is the 401 {"error":"Unauthorized"} response with its challenge.
function unauthorized(c: Context, options: RequireAuthOptions) {
  const value = challenge(options);
  if (value !== undefined) c.header('WWW-Authenticate', value);
  return c.json({ error: 'Unauthorized' }, 401);
}

// requireAuth rejects the request with 401 {"error":"Unauthorized"} before the
// endpoint runs. For jwt it also stores the verified payload on the request
// context as "claims"; the endpoint handler reads it from there.
export function requireAuth(options: RequireAuthOptions): MiddlewareHandler {
  return async (c, next) => {
    if (options.strategy === 'jwt') {
      const claims = verifiedClaims(c, options);
      if (claims === undefined) return unauthorized(c, options);
      c.set('claims', claims);
    } else if (!verify(c, options)) {
      return unauthorized(c, options);
    }
    await next();
  };
}
`
