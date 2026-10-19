package python_test

import (
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// useRealtimeHead is the middleware every `use` test below shares: it reads a
// header, fetches a row by it, guards the row with 401, and injects it.
const useRealtimeHead = `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
secret JWT_SECRET required
model account {
  id string primary
  name string required
}
middleware require_account {
  before {
    |> guard header.Authorization -> 401 "Missing token"
    |> acct = fetch account(header.Authorization)
    |> guard acct -> 401 "Unknown token"
    |> inject acct as current_user
  }
}
`

func TestPython_StreamUseRunsBeforeTheStreamOpens(t *testing.T) {
	out := buildPython(t, useRealtimeHead+`STREAM /api/notes/live {
  use require_account
  |> guard current_user.name -> 403 "Nameless account"
  stream {
    |> on event(note) { -> { text: event.text, by: current_user.name } }
    |> on timeout(1s) { -> { type: "ping" } }
  }
}
`)
	route := readPy(t, out, "src/routes/notes_stream.py")
	readPy(t, out, "src/middleware/require_account.py")
	for _, want := range []string{
		"from src.middleware.require_account import require_account",
		"def _use_stream_notes_live_0(authorization):",
		"    db = SessionLocal()\n",
		"        return require_account(authorization, db=db)\n",
		"    except HTTPException as exc:\n        raise StreamError(exc.status_code, exc.detail) from None\n",
		"def _pre_stream_notes_live(current_user):",
		`by": current_user.name`,
	} {
		if !strings.Contains(route, want) {
			t.Errorf("STREAM route is missing %q:\n%s", want, route)
		}
	}
	// The use call precedes the pre-stream helper, which precedes the stream
	// registration, so a refused request never opens a stream.
	order := []string{
		"current_user = await run_in_threadpool(_use_stream_notes_live_0, bp_request.headers.get(\"Authorization\"))",
		"_pre = await run_in_threadpool(_pre_stream_notes_live, current_user)",
		"stream = Stream(",
	}
	last := -1
	for _, want := range order {
		idx := strings.Index(route, want)
		if idx < 0 {
			t.Fatalf("STREAM route is missing %q:\n%s", want, route)
		}
		if idx <= last {
			t.Errorf("%q appears before the previous step; the use call must run first", want)
		}
		last = idx
	}
}

func TestPython_StreamUseWithoutHeadersOrDatabaseNeedsNoSession(t *testing.T) {
	out := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
middleware tag_request {
  before {
    |> log "tagged"
  }
}
STREAM /api/tags/live {
  use tag_request
  stream { |> on timeout(1s) { -> { type: "t" } } }
}
`)
	route := readPy(t, out, "src/routes/tags_stream.py")
	if strings.Contains(route, "SessionLocal()") {
		t.Errorf("a use without a database touch must not open a session:\n%s", route)
	}
	if !strings.Contains(route, "tag_request = await run_in_threadpool(_use_stream_tags_live_0)") {
		t.Errorf("STREAM route must call the use helper with no header arguments:\n%s", route)
	}
}

func TestPython_WsUseRunsInTheHandshakeAfterAuthBeforeAccept(t *testing.T) {
	out := buildPython(t, useRealtimeHead+`WS /ws/accounts/:id {
  auth jwt using(secret.JWT_SECRET)
  limit 5/min
  use require_account
  <- id uuid required
  on_connect {
    -> { type: "welcome", id: id, name: current_user.name }
  }
  on_message {
    -> { echo: message.body, by: current_user.name }
  }
}
`)
	route := readPy(t, out, "src/routes/accounts_ws.py")
	for _, want := range []string{
		"def _use_ws_accounts_id_0(authorization):",
		"        raise WsGuardError(exc.status_code, exc.detail) from None\n",
		"def _ws_accounts_id_connect(id, claims, connection, *, current_user=None):",
		"def _ws_accounts_id_message(message, id, claims, connection, *, current_user=None):",
		"def _ws_accounts_id_disconnect(id, claims, connection, *, current_user=None):",
		"result = await run_in_threadpool(_ws_accounts_id_connect, id, claims, connection, current_user=current_user)",
		"result = await run_in_threadpool(_ws_accounts_id_message, message, id, claims, connection, current_user=current_user)",
		"result = await run_in_threadpool(_ws_accounts_id_disconnect, id, claims, connection, current_user=current_user)",
		"        await refuse(websocket, exc.status_code)\n",
	} {
		if !strings.Contains(route, want) {
			t.Errorf("WS route is missing %q:\n%s", want, route)
		}
	}
	// Handshake order: auth, use, path inputs, limit, then accept().
	order := []string{
		"claims = authenticate(",
		"current_user = await run_in_threadpool(_use_ws_accounts_id_0, websocket.headers.get(\"Authorization\"))",
		"id = uuid.UUID(id)",
		"rate_limit.check(websocket, 5, 60000)",
		"await websocket.accept()",
	}
	last := -1
	for _, want := range order {
		idx := strings.Index(route, want)
		if idx < 0 {
			t.Fatalf("WS route is missing %q:\n%s", want, route)
		}
		if idx <= last {
			t.Errorf("%q appears before the previous handshake step", want)
		}
		last = idx
	}
}

func TestPython_WsUseFixtureBuilds(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/valid/python_stream_ws_use.bp")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	out := buildPython(t, string(data))
	readPy(t, out, "src/routes/notes_stream.py")
	readPy(t, out, "src/routes/accounts_ws.py")
}

func TestPython_RealtimeUseRejections(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"stream middleware with an after block": {
			src: useRealtimeHead + `middleware audit { before { |> log "x" } after { |> log "y" } }
STREAM /api/x {
  use audit
  stream { |> on timeout(1s) { -> { type: "t" } } }
}
`,
			want: "`middleware after` bodies",
		},
		"ws middleware with an after block": {
			src: useRealtimeHead + `middleware audit { before { |> log "x" } after { |> log "y" } }
WS /ws/x {
  use audit
  on_message { |> log "x" }
}
`,
			want: "`middleware after` bodies",
		},
		"ws use with configuration": {
			src: useRealtimeHead + `WS /ws/x {
  use require_account { level: "debug" }
  on_message { |> log "x" }
}
`,
			want: `configured endpoint middleware use "require_account"`,
		},
		"ws use of an undeclared middleware": {
			src: useRealtimeHead + `WS /ws/x {
  use missing
  on_message { |> log "x" }
}
`,
			want: `endpoint use of non-generated middleware "missing"`,
		},
		"ws middleware injecting a reserved handler name": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
secret DATABASE_URL required
model account {
  id string primary
  name string required
}
middleware reserved {
  before {
    |> acct = fetch account(header.Authorization)
    |> guard acct -> 401 "no"
    |> inject acct as connection
  }
}
WS /ws/x {
  use reserved
  on_message { |> log "x" }
}
`,
			want: `WS ` + "`use reserved`" + ` that injects "connection"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected the use to be rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err.Error(), tc.want)
			}
		})
	}
}
