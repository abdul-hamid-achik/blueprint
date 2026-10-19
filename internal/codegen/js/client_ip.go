package js

import (
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// hasRateLimitEndpoints reports whether any REST, STREAM, or WS endpoint declares
// `limit`. The rate-limit store and src/lib/client-ip.ts are emitted under the
// same condition.
func hasRateLimitEndpoints(endpoints []*ast.Endpoint, streams []*ast.StreamEndpoint, ws []*ast.WsEndpoint) bool {
	for _, ep := range endpoints {
		for _, meta := range ep.Meta {
			if meta.Kind == "limit" {
				return true
			}
		}
	}
	for _, ep := range streams {
		for _, meta := range ep.Meta {
			if meta.Kind == "limit" {
				return true
			}
		}
	}
	for _, ep := range ws {
		for _, meta := range ep.Meta {
			if meta.Kind == "limit" {
				return true
			}
		}
	}
	return false
}

// genClientIPLib emits src/lib/client-ip.ts, the client address that endpoint
// `limit` keys on. It is emitted whenever some endpoint declares `limit`.
func (g *Generator) genClientIPLib() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(clientIPSource)
	return codegen.OutputFile{Path: "src/lib/client-ip.ts", Content: []byte(b.String())}
}

// clientIPSource follows uvicorn's ProxyHeadersMiddleware: FORWARDED_ALLOW_IPS
// (the same variable uvicorn reads on the Python target) lists the proxies whose
// X-Forwarded-For header is trusted. It is parsed once at module load, without
// net.BlockList, so it also runs under Bun.
const clientIPSource = `// Client address for endpoint ` + "`limit`" + `, resolved like uvicorn's ProxyHeadersMiddleware.
// FORWARDED_ALLOW_IPS lists the peers whose X-Forwarded-For header is trusted, in
// uvicorn's format: "*", exact IPv4 or IPv6 addresses, or IPv4 CIDR ranges,
// comma-separated. Unset means "127.0.0.1,::1" (uvicorn's default); an empty string
// trusts nothing. IPv6 CIDR ranges are not supported and never match.

import type { Context } from 'hono';
import { getConnInfo } from '@hono/node-server/conninfo';

const _forwardedAllowIps = process.env.FORWARDED_ALLOW_IPS ?? '127.0.0.1,::1';
const _alwaysTrust = _forwardedAllowIps === '*';
const _trustedLiterals = new Set<string>();
const _trustedNetworks: { base: number; mask: number }[] = [];

function _parseIPv4(value: string): number | null {
  const parts = value.split('.');
  if (parts.length !== 4) return null;
  let out = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part) || Number(part) > 255) return null;
    out = out * 256 + Number(part);
  }
  return out;
}

function _parseCidr(entry: string): { base: number; mask: number } | null {
  const match = /^(\d{1,3}(?:\.\d{1,3}){3})\/(\d{1,2})$/.exec(entry);
  if (!match) return null;
  const ip = _parseIPv4(match[1]);
  const bits = Number(match[2]);
  if (ip === null || bits > 32) return null;
  const mask = bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0;
  return { base: (ip & mask) >>> 0, mask };
}

if (!_alwaysTrust) {
  for (const entry of _forwardedAllowIps.split(',').map((item) => item.trim())) {
    const network = _parseCidr(entry);
    if (network) _trustedNetworks.push(network);
    else _trustedLiterals.add(entry.toLowerCase());
  }
}

function _isTrusted(host: string): boolean {
  if (_alwaysTrust) return true;
  if (!host) return false;
  if (_trustedLiterals.has(host.toLowerCase())) return true;
  const ip = _parseIPv4(host);
  return ip !== null && _trustedNetworks.some((net) => ((ip & net.mask) >>> 0) === net.base);
}

// Host part of one X-Forwarded-For entry, without a port (bare, [v6]:port, or host:port).
function _hopHost(hop: string): string {
  if (hop.startsWith('[')) {
    const end = hop.indexOf(']');
    if (end === -1) return hop;
    const rest = hop.slice(end + 1);
    return rest === '' || (rest.startsWith(':') && /^\d+$/.test(rest.slice(1))) ? hop.slice(1, end) : hop;
  }
  const colon = hop.indexOf(':');
  if (colon === -1 || hop.indexOf(':', colon + 1) !== -1) return hop;
  return /^\d+$/.test(hop.slice(colon + 1)) ? hop.slice(0, colon) : hop;
}

// clientIp returns the peer address, or the client named by X-Forwarded-For when
// the peer is a trusted proxy. The header is walked right to left and the first
// untrusted hop wins; if every hop is trusted, the leftmost one is used.
export function clientIp(c: Context): string {
  let peer = '';
  try {
    // c.env is undefined under app.request() (tests), so this falls back to 'unknown'.
    peer = getConnInfo(c).remote.address ?? '';
  } catch {
    peer = '';
  }
  if (peer.startsWith('::ffff:')) peer = peer.slice(7);
  const forwarded = c.req.header('x-forwarded-for');
  if (forwarded && _isTrusted(peer)) {
    const hops = forwarded.split(',').map((hop) => _hopHost(hop.trim()));
    const hop = _alwaysTrust ? hops[0] : ([...hops].reverse().find((item) => !_isTrusted(item)) ?? hops[0]);
    if (hop) return hop;
  }
  return peer || 'unknown';
}
`
