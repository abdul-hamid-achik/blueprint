package js

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const authTestHeader = `blueprint "auth-test" {
  version "1.0.0"
  runtime node
}

secret JWT_SECRET required
`

func authTestFiles(t *testing.T, body string) (map[string]string, []string) {
	t.Helper()
	file, errs := parser.ParseFile("auth.bp", []byte(authTestHeader+body))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return generatedFiles(t, file)
}

// requireAuthCalls returns the requireAuth(...) expressions in a route file.
func requireAuthCalls(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "requireAuth({"); i >= 0 {
			end := strings.Index(line[i:], "})")
			if end >= 0 {
				out = append(out, line[i:i+end+2])
			}
		}
	}
	return out
}

func TestAuthLibEmittedOnlyForEnforcedStrategies(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantLib  bool
		wantWire bool
	}{
		{
			name:     "metadata-only strategies emit no auth module",
			body:     "GET /a {\n  auth bearer\n  -> 200 \"ok\"\n}\nGET /b {\n  auth jwt\n  -> 200 \"ok\"\n}\n",
			wantLib:  false,
			wantWire: false,
		},
		{
			name:     "webhook_sig keeps its own HMAC check and emits no auth module",
			body:     "POST /h {\n  auth webhook_sig using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantLib:  false,
			wantWire: false,
		},
		{
			name:     "enforced jwt on REST emits the module and wires it",
			body:     "GET /a {\n  auth jwt using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantLib:  true,
			wantWire: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contents, paths := authTestFiles(t, tc.body)
			_, hasLib := contents["src/lib/auth.ts"]
			if hasLib != tc.wantLib {
				t.Fatalf("src/lib/auth.ts present = %v, want %v (paths %v)", hasLib, tc.wantLib, paths)
			}
			routes := routeFileContent(t, paths, contents)
			if got := strings.Contains(routes, "requireAuth("); got != tc.wantWire {
				t.Fatalf("requireAuth wired = %v, want %v:\n%s", got, tc.wantWire, routes)
			}
		})
	}
}

func TestAuthLibVerifiesEveryStrategyWithoutNone(t *testing.T) {
	contents, _ := authTestFiles(t, "GET /a {\n  auth jwt using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n")
	lib := contents["src/lib/auth.ts"]
	for _, want := range []string{
		"import { createHash, createHmac, timingSafeEqual } from 'node:crypto';",
		"export function requireAuth(",
		"header.alg !== 'HS256'",
		"if (!secret) return false;",
		"'Basic realm=\"' +",
		"{ error: 'Unauthorized' }, 401",
		"c.req.query('token')",
		"c.req.query('api_key')",
		"payload.exp <= now",
		"payload.nbf > now",
	} {
		if !strings.Contains(lib, want) {
			t.Errorf("src/lib/auth.ts missing %q", want)
		}
	}
	if strings.Contains(lib, "'none'") {
		t.Errorf("auth module must not accept alg none")
	}
	for _, bad := range []string{"crypto-js", "jsonwebtoken", "from 'jose'"} {
		if strings.Contains(lib, bad) {
			t.Errorf("auth module must not depend on %q", bad)
		}
	}
}

func TestAuthRESTWiresBeforeUseAndValidation(t *testing.T) {
	contents, paths := authTestFiles(t, `
middleware check_user {
  before {
    |> guard header.Authorization -> 401 "Unauthorized"
  }
}

POST /a {
  auth jwt using(secret.JWT_SECRET)
  use check_user
  <- name string required
  -> 200 { ok: true }
}
`)
	routes := routeFileContent(t, paths, contents)
	calls := requireAuthCalls(routes)
	if len(calls) != 1 {
		t.Fatalf("want one requireAuth call, got %v in:\n%s", calls, routes)
	}
	call := calls[0]
	if !strings.Contains(call, "allowQuery: false") {
		t.Errorf("REST must be header-only: %s", call)
	}
	if !strings.Contains(call, "strategy: 'jwt'") || !strings.Contains(call, "secret: env.JWT_SECRET") {
		t.Errorf("unexpected requireAuth options: %s", call)
	}
	if !strings.Contains(call, `realm: "auth-test"`) {
		t.Errorf("realm should be the blueprint name: %s", call)
	}
	// Measure inside the route registration, not the import block.
	chain := routes[strings.Index(routes, ".post('/a'"):]
	authAt := strings.Index(chain, "requireAuth(")
	useAt := strings.Index(chain, ", checkUser")
	validateAt := strings.Index(chain, "zValidator(")
	if authAt < 0 || useAt < 0 || validateAt < 0 || authAt > useAt || authAt > validateAt {
		t.Errorf("requireAuth must come before use middleware and zValidator (auth %d, use %d, validate %d)", authAt, useAt, validateAt)
	}
	if !strings.Contains(routes, "import { requireAuth as _requireAuth } from '../lib/auth.js';") {
		t.Errorf("route file must import requireAuth under the _requireAuth alias")
	}
}

// A user middleware named require_auth camel-cases to requireAuth. The auth
// module's import must not collide with it, so the two stay distinct.
func TestAuthDoesNotCollideWithUserRequireAuthMiddleware(t *testing.T) {
	contents, paths := authTestFiles(t, `
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Unauthorized"
  }
}

GET /a {
  auth jwt using(secret.JWT_SECRET)
  use require_auth
  -> 200 { ok: true }
}
`)
	routes := routeFileContent(t, paths, contents)
	if strings.Contains(routes, "import { requireAuth } from '../lib/auth.js'") {
		t.Errorf("auth import must not use the bare name requireAuth:\n%s", routes)
	}
	if !strings.Contains(routes, "import { requireAuth } from '../middleware/require-auth.js';") {
		t.Errorf("user middleware import missing:\n%s", routes)
	}
	if !strings.Contains(routes, "_requireAuth({") {
		t.Errorf("enforced auth call missing:\n%s", routes)
	}
}

func TestAuthSTREAMAllowsQueryFallback(t *testing.T) {
	contents, paths := authTestFiles(t, `
STREAM /s {
  auth bearer using(secret.JWT_SECRET)

  stream {
    |> on timeout(5s) {
      -> { type: "ping" }
    }
  }
}
`)
	routes := routeFileContent(t, paths, contents)
	calls := requireAuthCalls(routes)
	if len(calls) != 1 || !strings.Contains(calls[0], "allowQuery: true") {
		t.Fatalf("STREAM should accept the query credential, got %v in:\n%s", calls, routes)
	}
}

func TestAuthWSWiresBeforeUpgradeWithQueryFallback(t *testing.T) {
	contents, paths := authTestFiles(t, `
WS /ws/chat {
  auth jwt using(secret.JWT_SECRET)

  on_connect {
    |> log "connected"
  }
}
`)
	routes := routeFileContent(t, paths, contents)
	calls := requireAuthCalls(routes)
	if len(calls) != 1 || !strings.Contains(calls[0], "allowQuery: true") {
		t.Fatalf("WS should accept the query credential, got %v in:\n%s", calls, routes)
	}
	if authAt, upgradeAt := strings.Index(routes, "requireAuth("), strings.Index(routes, "upgradeWebSocket((c"); authAt < 0 || upgradeAt < 0 || authAt > upgradeAt {
		t.Errorf("requireAuth must run before upgradeWebSocket (auth %d, upgrade %d)", authAt, upgradeAt)
	}
}

func TestAuthAPIKeyAndBasicWiring(t *testing.T) {
	contents, paths := authTestFiles(t, `
GET /k {
  auth api_key using(secret.JWT_SECRET)
  -> 200 "ok"
}

GET /b {
  auth basic using(secret.JWT_SECRET)
  -> 200 "ok"
}
`)
	routes := routeFileContent(t, paths, contents)
	calls := requireAuthCalls(routes)
	if len(calls) != 2 {
		t.Fatalf("want two requireAuth calls, got %v", calls)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "strategy: 'api_key'") || !strings.Contains(joined, "strategy: 'basic'") {
		t.Errorf("missing strategies in %v", calls)
	}
}

// WS runs enforced auth before use middleware, as REST and STREAM do, so a
// user middleware never runs for an unauthenticated client.
func TestAuthWSWiresBeforeUse(t *testing.T) {
	contents, paths := authTestFiles(t, `
middleware check_user {
  before {
    |> guard header.Authorization -> 401 "Unauthorized"
  }
}

WS /ws/chat {
  auth jwt using(secret.JWT_SECRET)
  use check_user

  on_connect {
    |> log "connected"
  }
}
`)
	routes := routeFileContent(t, paths, contents)
	chain := routes[strings.Index(routes, ".get('/ws/chat'"):]
	authAt := strings.Index(chain, "requireAuth(")
	useAt := strings.Index(chain, ", checkUser")
	if authAt < 0 || useAt < 0 || authAt > useAt {
		t.Errorf("requireAuth must come before use middleware on WS (auth %d, use %d):\n%s", authAt, useAt, chain)
	}
}

// A guard in a use middleware throws BpError outside the route's try/catch;
// app.onError answers it with its status and message, not a 500.
func TestIndexOnErrorAnswersBpError(t *testing.T) {
	contents, paths := authTestFiles(t, `
GET /a {
  -> 200 { ok: true }
}
`)
	var index string
	for _, p := range paths {
		if p == "src/index.ts" {
			index = contents[p]
		}
	}
	for _, want := range []string{
		"import { BpError } from './lib/errors.js';",
		"if (err instanceof BpError) return c.json({ error: err.message }, err.statusCode);",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("src/index.ts missing %q:\n%s", want, index)
		}
	}
	if strings.Index(index, "err instanceof BpError") > strings.Index(index, "'Internal server error'") {
		t.Errorf("BpError must be answered before the generic 500")
	}
}
