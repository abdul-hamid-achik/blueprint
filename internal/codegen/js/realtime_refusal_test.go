package js

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const realtimeRefusalHeader = `blueprint "rt" { version "1.0.0" port 3000 runtime node }
secret JWT_SECRET required
`

// realtimeRefusalFiles generates the project with contract tests enabled and
// returns every emitted file by path.
func realtimeRefusalFiles(t *testing.T, body string) map[string]string {
	t.Helper()
	file, errs := parser.ParseFile("rt.bp", []byte(realtimeRefusalHeader+body))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	files, err := New().WithGenTests(true).Files(file)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	contents := map[string]string{}
	for _, f := range files {
		contents[f.Path] = string(f.Content)
	}
	return contents
}

func TestRealtimeRefusal_StreamAuthAndPathInput(t *testing.T) {
	contents := realtimeRefusalFiles(t, `
STREAM /api/rooms/:id/live {
  auth jwt using(secret.JWT_SECRET)
  <- id uuid required

  stream {
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`)
	test, ok := contents["test/generated/rooms-realtime.test.ts"]
	if !ok {
		t.Fatalf("STREAM refusals need test/generated/rooms-realtime.test.ts; got %v", keysOf(contents))
	}
	for _, want := range []string{
		"import { signTestJwt } from '../_harness/auth.js';",
		"STREAM GET /api/rooms/:id/live rejects requests without credentials",
		"await app.request(`/api/rooms/00000000-0000-0000-0000-000000000000/live`, { method: 'GET' });",
		"STREAM GET /api/rooms/:id/live rejects a malformed id",
		"await app.request(`/api/rooms/not-a-uuid/live`, { method: 'GET', headers: { 'Authorization': 'Bearer ' + signTestJwt(process.env.JWT_SECRET ?? '') } });",
		"expect(res.status).toBe(401);",
		"expect(res.status).toBe(400);",
		"}, 5000);",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("realtime refusal test missing %q:\n%s", want, test)
		}
	}
	// A refusal is read for its status only: a stream body is never consumed.
	for _, banned := range []string{".json()", ".text()", "getReader", "upgradeWebSocket"} {
		if strings.Contains(test, banned) {
			t.Errorf("STREAM refusal test must not read the response (%q):\n%s", banned, test)
		}
	}
	if _, ok := contents["test/_harness/auth.ts"]; !ok {
		t.Errorf("jwt refusal tests need the test/_harness/auth.ts signer")
	}
}

func TestRealtimeRefusal_WsUpgradeRequestAndPathInput(t *testing.T) {
	contents := realtimeRefusalFiles(t, `
WS /ws/rooms/:id {
  auth jwt using(secret.JWT_SECRET)
  <- id uuid required

  on_connect {
    -> { type: "welcome" }
  }
}
`)
	test, ok := contents["test/generated/ws-realtime.test.ts"]
	if !ok {
		t.Fatalf("WS refusals need test/generated/ws-realtime.test.ts; got %v", keysOf(contents))
	}
	for _, want := range []string{
		"WS GET /ws/rooms/:id rejects requests without credentials",
		"'Upgrade': 'websocket'",
		"'Connection': 'Upgrade'",
		"'Sec-WebSocket-Version': '13'",
		"'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ=='",
		"WS GET /ws/rooms/:id rejects a malformed id",
		"await app.request(`/ws/rooms/not-a-uuid`",
		"expect(res.status).toBe(400);",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("WS refusal test missing %q:\n%s", want, test)
		}
	}
}

func TestRealtimeRefusal_MiddlewareGuardHeader(t *testing.T) {
	contents := realtimeRefusalFiles(t, `
middleware require_token {
  before {
    |> guard header.Authorization -> 403 "Missing token"
  }
}

STREAM /api/feed/live {
  use require_token

  stream {
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`)
	test, ok := contents["test/generated/feed-realtime.test.ts"]
	if !ok {
		t.Fatalf("guarded STREAM needs a refusal test; got %v", keysOf(contents))
	}
	for _, want := range []string{
		"STREAM GET /api/feed/live refuses requests without header Authorization (use require_token)",
		"await app.request(`/api/feed/live`, { method: 'GET' });",
		"expect(res.status).toBe(403);",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("middleware refusal test missing %q:\n%s", want, test)
		}
	}
	if strings.Contains(test, "signTestJwt") {
		t.Errorf("an unauthenticated endpoint must not sign credentials:\n%s", test)
	}
}

// Credentials that carry the guarded header would satisfy the guard, so the
// middleware refusal is not generated; a use middleware also keeps the path
// refusal out, because it could answer first.
func TestRealtimeRefusal_SkipsGuardSatisfiedByCredentialsAndUseBeforePath(t *testing.T) {
	contents := realtimeRefusalFiles(t, `
middleware require_authorization {
  before {
    |> guard header.Authorization -> 401 "Missing token"
  }
}

STREAM /api/rooms/:id/live {
  auth bearer using(secret.JWT_SECRET)
  use require_authorization
  <- id uuid required

  stream {
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`)
	test := contents["test/generated/rooms-realtime.test.ts"]
	if got := strings.Count(test, "  it('"); got != 1 {
		t.Errorf("want only the credential refusal, got %d tests:\n%s", got, test)
	}
	if !strings.Contains(test, "rejects requests without credentials") {
		t.Errorf("auth refusal missing:\n%s", test)
	}
}

func TestRealtimeRefusal_NothingToRefuseEmitsNoTests(t *testing.T) {
	contents := realtimeRefusalFiles(t, `
STREAM /api/ping {
  stream {
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`)
	for path := range contents {
		if strings.HasPrefix(path, "test/") {
			t.Errorf("a STREAM with nothing to refuse must emit no test files, got %s", path)
		}
	}
}

func TestRealtimeRefusal_OffWithoutGenTests(t *testing.T) {
	file, errs := parser.ParseFile("rt.bp", []byte(realtimeRefusalHeader+`
STREAM /api/rooms/:id/live {
  auth jwt using(secret.JWT_SECRET)
  <- id uuid required

  stream {
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	files, err := New().Files(file)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "test/") {
			t.Errorf("refusal tests need --gen-tests, got %s", f.Path)
		}
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
