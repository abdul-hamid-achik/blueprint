package js

import (
	"strings"
	"testing"
)

const clientIPRestLimitSrc = `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

GET /api/ping {
  limit 3/min

  -> 200 { ok: true }
}`

const clientIPNoLimitSrc = `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

GET /api/ping {
  -> 200 { ok: true }
}`

func TestClientIPLibOnlyWhenLimitDeclared(t *testing.T) {
	withLimit := generateFanoutFiles(t, clientIPRestLimitSrc)
	if _, ok := withLimit["src/lib/client-ip.ts"]; !ok {
		t.Fatal("src/lib/client-ip.ts should be emitted when an endpoint declares limit")
	}

	withoutLimit := generateFanoutFiles(t, clientIPNoLimitSrc)
	if _, ok := withoutLimit["src/lib/client-ip.ts"]; ok {
		t.Fatal("src/lib/client-ip.ts should not be emitted without any limit")
	}
	for path, content := range withoutLimit {
		if strings.Contains(content, "clientIp") {
			t.Errorf("%s should not reference clientIp without a limit", path)
		}
	}
}

func TestClientIPLibContent(t *testing.T) {
	lib := fanoutFile(t, generateFanoutFiles(t, clientIPRestLimitSrc), "src/lib/client-ip.ts")
	for _, want := range []string{
		"import type { Context } from 'hono';",
		"import { getConnInfo } from '@hono/node-server/conninfo';",
		"const _forwardedAllowIps = process.env.FORWARDED_ALLOW_IPS ?? '127.0.0.1,::1';",
		"const _alwaysTrust = _forwardedAllowIps === '*';",
		"export function clientIp(c: Context): string {",
		"if (peer.startsWith('::ffff:')) peer = peer.slice(7);",
		"[...hops].reverse().find((item) => !_isTrusted(item))",
		"return peer || 'unknown';",
	} {
		if !strings.Contains(lib, want) {
			t.Errorf("client-ip.ts missing %q:\n%s", want, lib)
		}
	}
	// net.BlockList is Node-only; the helper must also run under Bun.
	if strings.Contains(lib, "BlockList") {
		t.Error("client-ip.ts must not use net.BlockList (it must also run under Bun)")
	}
}

func TestClientIPRouteImportsAndKey(t *testing.T) {
	files := generateFanoutFiles(t, clientIPRestLimitSrc)
	routes := map[string]string{}
	for path, content := range files {
		if strings.HasPrefix(path, "src/routes/") && strings.Contains(content, "_rateLimitStore") {
			routes[path] = content
		}
	}
	if len(routes) == 0 {
		t.Fatal("expected a generated REST route file with a rate-limit store")
	}
	for path, route := range routes {
		checkRestRouteClientIP(t, path, route)
	}
}

func checkRestRouteClientIP(t *testing.T, path, route string) {
	t.Helper()
	for _, want := range []string{
		"import { clientIp } from '../lib/client-ip.js';",
		"const _clientIp = clientIp(c);",
		"const _rateKey = `${_clientIp}:${c.req.path}`;",
	} {
		if !strings.Contains(route, want) {
			t.Errorf("%s missing %q:\n%s", path, want, route)
		}
	}
	if strings.Contains(route, "x-forwarded-for") {
		t.Errorf("%s must not read x-forwarded-for directly:\n%s", path, route)
	}
}

func TestClientIPImportedByStreamAndWsRoutes(t *testing.T) {
	files := generateFanoutFiles(t, `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

STREAM /api/updates {
  limit 5/min

  stream {
    |> on event(update) {
      |> log "sending update"
    }
  }
}

WS /ws/chat {
  limit 5/min

  on_connect {
    |> log "connected"
  }
  on_message {
    |> log "received"
  }
  on_disconnect {
    |> log "disconnected"
  }
}`)
	for path, content := range files {
		if !strings.HasPrefix(path, "src/routes/") {
			continue
		}
		if !strings.Contains(content, "_rateLimitStore") {
			continue
		}
		if !strings.Contains(content, "import { clientIp } from '../lib/client-ip.js';") {
			t.Errorf("%s uses rate limits but does not import clientIp", path)
		}
		if !strings.Contains(content, "const _clientIp = clientIp(c);") {
			t.Errorf("%s does not key rate limits with clientIp(c)", path)
		}
	}
	if len(streamRouteFiles(files)) == 0 || len(wsRouteFiles(files)) == 0 {
		t.Fatal("expected generated STREAM and WS route files")
	}
}
