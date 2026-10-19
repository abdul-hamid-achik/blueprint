package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const realtimeRefusalHeader = `blueprint "rt" { version "1.0.0" port 3000 runtime python database postgres }
secret DATABASE_URL required
secret JWT_SECRET required
model room {
  id uuid primary
  name string required
}
`

// TestPython_RealtimeRefusalStreamAuthAndPathInput checks the STREAM refusals:
// no credentials is 401 from the auth dependency, and a malformed uuid segment
// is 422 from FastAPI's path validation (credentials are sent so auth passes).
func TestPython_RealtimeRefusalStreamAuthAndPathInput(t *testing.T) {
	outDir := buildPythonGenTests(t, realtimeRefusalHeader+`
STREAM /api/rooms/:id/live {
  auth jwt using(secret.JWT_SECRET)
  <- id uuid required
  |> room = fetch room(id)
  |> guard room -> 404 "Room not found"

  stream {
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`)
	test := readPy(t, outDir, "tests/test_rooms_realtime.py")
	for _, want := range []string{
		"from tests._harness.realtime import bounded, ws_refusal_status",
		"from tests._harness.auth import auth_headers",
		"def test_stream_api_rooms_id_live_rejects_without_credentials(client: TestClient) -> None:",
		`res = bounded(lambda: client.get("/api/rooms/00000000-0000-0000-0000-000000000000/live"))`,
		"assert res.status_code == 401",
		"def test_stream_api_rooms_id_live_rejects_malformed_id(client: TestClient) -> None:",
		`res = bounded(lambda: client.get("/api/rooms/not-a-uuid/live", headers=auth_headers("jwt", "JWT_SECRET")))`,
		"assert res.status_code == 422",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("STREAM refusal test missing %q:\n%s", want, test)
		}
	}
	if strings.Contains(test, ".json()") || strings.Contains(test, ".iter") {
		t.Errorf("STREAM refusal test must not read the response body:\n%s", test)
	}
	harness := readPy(t, outDir, "tests/_harness/realtime.py")
	for _, want := range []string{"def bounded(", "def ws_refusal_status(", "WebSocketDenialResponse"} {
		if !strings.Contains(harness, want) {
			t.Errorf("realtime harness missing %q:\n%s", want, harness)
		}
	}
}

// TestPython_RealtimeRefusalWsUpgradeAndPathInput checks the WS refusals: the
// handshake is refused with 401 without credentials and 400 for a malformed
// uuid segment.
func TestPython_RealtimeRefusalWsUpgradeAndPathInput(t *testing.T) {
	outDir := buildPythonGenTests(t, realtimeRefusalHeader+`
WS /ws/rooms/:id {
  auth jwt using(secret.JWT_SECRET)
  <- id uuid required

  on_connect {
    -> { type: "welcome" }
  }
}
`)
	test := readPy(t, outDir, "tests/test_ws_realtime.py")
	for _, want := range []string{
		"def test_ws_ws_rooms_id_rejects_without_credentials(client: TestClient) -> None:",
		`assert bounded(lambda: ws_refusal_status(client, "/ws/rooms/00000000-0000-0000-0000-000000000000")) == 401`,
		"def test_ws_ws_rooms_id_rejects_malformed_id(client: TestClient) -> None:",
		`assert bounded(lambda: ws_refusal_status(client, "/ws/rooms/not-a-uuid", headers=auth_headers("jwt", "JWT_SECRET"))) == 400`,
	} {
		if !strings.Contains(test, want) {
			t.Errorf("WS refusal test missing %q:\n%s", want, test)
		}
	}
}

// TestPython_RealtimeRefusalMiddlewareGuard checks that a use middleware whose
// first step is a header guard is answered with the guard's status.
func TestPython_RealtimeRefusalMiddlewareGuard(t *testing.T) {
	outDir := buildPythonGenTests(t, realtimeRefusalHeader+`
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
	test := readPy(t, outDir, "tests/test_feed_realtime.py")
	for _, want := range []string{
		"def test_stream_api_feed_live_refuses_without_header_authorization(client: TestClient) -> None:",
		`res = bounded(lambda: client.get("/api/feed/live"))`,
		"assert res.status_code == 403",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("middleware refusal test missing %q:\n%s", want, test)
		}
	}
}

// TestPython_RealtimeRefusalSkipsWhatCannotRefuse checks that a guard the
// credentials would satisfy, and a path input behind use middleware, are not
// tested.
func TestPython_RealtimeRefusalSkipsWhatCannotRefuse(t *testing.T) {
	outDir := buildPythonGenTests(t, realtimeRefusalHeader+`
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
	test := readPy(t, outDir, "tests/test_rooms_realtime.py")
	if got := strings.Count(test, "\ndef test_"); got != 1 {
		t.Errorf("want only the credential refusal, got %d tests:\n%s", got, test)
	}
	if !strings.Contains(test, "rejects_without_credentials") {
		t.Errorf("credential refusal missing:\n%s", test)
	}
}

// TestPython_RealtimeRefusalNothingToRefuseEmitsNoTests checks that a STREAM
// with no auth, typed path input or guard adds no test files or harness.
func TestPython_RealtimeRefusalNothingToRefuseEmitsNoTests(t *testing.T) {
	outDir := buildPythonGenTests(t, `blueprint "rt" { version "1.0.0" port 3000 runtime python }
STREAM /api/ping {
  stream {
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`)
	for _, rel := range []string{"tests/_harness/realtime.py", "tests/conftest.py", "tests/test_ping_realtime.py"} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err == nil {
			t.Errorf("a STREAM with nothing to refuse must not emit %s", rel)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(outDir, "tests", "test_*.py")); len(matches) > 0 {
		t.Errorf("no test modules expected, got %v", matches)
	}
}
