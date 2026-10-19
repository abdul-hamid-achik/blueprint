package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// streamBlueprint is the Python-supported STREAM shape: jwt auth, a pre-stream
// fetch and guard, a filtered event handler, and a timeout ping.
const streamBlueprint = `blueprint "chat" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
secret JWT_SECRET required
model room {
  id uuid primary
  name string required
}
POST /api/rooms/:id/messages {
  <- id uuid required
  <- body string required
  |> emit new_message { room_id: id, body: body }
  -> 200 { ok: true }
}
STREAM /api/rooms/:id/live {
  tags ["rooms", "streaming"]
  auth jwt using(secret.JWT_SECRET)
  limit 60/min
  <- id uuid required
  |> room = fetch room(id)
  |> guard room -> 404 "Room not found"
  stream {
    |> on event(new_message) where(room_id == id) {
      -> { body: event.body, sender: claims.username }
    }
    |> on timeout(2s) {
      -> { type: "ping" }
    }
  }
}
`

func TestPython_StreamRouteShape(t *testing.T) {
	outDir := buildPython(t, streamBlueprint)
	route := readPy(t, outDir, "src/routes/rooms_stream.py")

	for _, want := range []string{
		// Pre-stream block: sync helper on its own session, run off the loop.
		"def _pre_stream_rooms_id_live(id, claims):",
		"db = SessionLocal()",
		"db.close()",
		`raise StreamError(404, "Room not found")`,
		"_pre = await run_in_threadpool(_pre_stream_rooms_id_live, id, claims)",
		`room = _pre["room"]`,
		// Auth on STREAM accepts the query fallback; the claims parameter comes from it.
		`claims: Annotated[dict[str, Any], Depends(_bp_require_auth("jwt", "JWT_SECRET", "chat", allow_query=True))]`,
		// Rate limit runs before the stream starts.
		"rate_limit.check(bp_request, 60, 60000)",
		// Filter and render per subscriber; event fields are read by their Node key (room_id -> roomId).
		`if not ((event.get("roomId", MISSING) == wire(id))):`,
		`return ("new_message", frame_data({"body": event.get("body", MISSING), "sender": claims.get("username")}))`,
		// Timeout: named ping, rendered object, first send after the interval.
		`stream.every("ping", 2, _tick_1)`,
		`stream.on_event("new_message", _on_event_0)`,
		"stream.start()",
		"return EventStream(stream)",
	} {
		if !strings.Contains(route, want) {
			t.Errorf("rooms_stream.py missing %q, got:\n%s", want, route)
		}
	}
	for _, banned := range []string{"Depends(get_db)", "sse_starlette", "TODO", "asyncio.sleep(", "yield {"} {
		if strings.Contains(route, banned) {
			t.Errorf("rooms_stream.py must not contain %q, got:\n%s", banned, route)
		}
	}

	events := readPy(t, outDir, "src/lib/events.py")
	for _, want := range []string{
		"BP_SSE_MAX_PENDING = _positive_int_env(\"BP_SSE_MAX_PENDING\", 256)",
		"BP_SSE_HARD_CAP = BP_SSE_MAX_PENDING * 16",
		"BP_SSE_SLOW_GRACE_S = 5.0",
		`"bp:stream:" + event`,
		"def emit(event: str, data: dict[str, Any]) -> None:",
		`os.environ.get("REDIS_URL")`,
		`"Content-Type": "text/event-stream"`,
	} {
		if !strings.Contains(events, want) {
			t.Errorf("events.py missing %q", want)
		}
	}

	app := readPy(t, outDir, "src/app.py")
	if !strings.Contains(app, "app.add_exception_handler(StreamError, stream_error_handler)") {
		t.Errorf("app.py must answer StreamError with the {error} handler:\n%s", app)
	}

	pyproject := readPy(t, outDir, "pyproject.toml")
	if !strings.Contains(pyproject, `"redis>=5.0"`) || strings.Contains(pyproject, "sse-starlette") {
		t.Errorf("pyproject.toml should depend on redis for STREAM fan-out and not on sse-starlette:\n%s", pyproject)
	}

	routes := readPy(t, outDir, "src/routes/rooms.py")
	for _, want := range []string{
		"from src.lib.events import emit as bp_emit",
		`bp_emit("new_message", {"roomId": id, "body": body})`,
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("rooms.py missing %q, got:\n%s", want, routes)
		}
	}
}

func TestPython_StreamWithoutFanoutStillBuildsWithoutRedisDependency(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
STREAM /api/ticks {
  stream {
    |> on timeout(1s) { -> { type: "tick" } }
  }
}
`)
	route := readPy(t, outDir, "src/routes/ticks_stream.py")
	if !strings.Contains(route, `stream.every("ping", 1, _tick_0)`) {
		t.Errorf("timeout-only stream should register its timer, got:\n%s", route)
	}
	if strings.Contains(route, "_pre_stream") || strings.Contains(route, "SessionLocal") {
		t.Errorf("a stream without pre-stream statements must not open a session, got:\n%s", route)
	}
	if _, err := os.Stat(filepath.Join(outDir, "src/lib/auth.py")); err == nil {
		t.Errorf("auth.py must not be emitted for an unauthenticated stream")
	}
}

func TestPython_NoEventsModuleWithoutStreamOrEmit(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/x { -> 200 "ok" }
`)
	if _, err := os.Stat(filepath.Join(outDir, "src/lib/events.py")); err == nil {
		t.Errorf("src/lib/events.py must not be emitted without STREAM or emit")
	}
}

func TestPython_EmitWithoutStreamEmitsEventsModule(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
secret NAME
POST /api/x {
  <- body string required
  |> emit saved { body: body }
  -> 200 "ok"
}
`)
	routes := readPy(t, outDir, "src/routes/x.py")
	if !strings.Contains(routes, `bp_emit("saved", {"body": body})`) {
		t.Errorf("emit step should call the events module, got:\n%s", routes)
	}
	if _, err := os.Stat(filepath.Join(outDir, "src/lib/events.py")); err != nil {
		t.Errorf("src/lib/events.py must be emitted for emit: %v", err)
	}
	app := readPy(t, outDir, "src/app.py")
	if strings.Contains(app, "StreamError") {
		t.Errorf("app.py must not register StreamError without a STREAM endpoint")
	}
}

func TestPython_AuthQueryFallbackIsOptInForStreams(t *testing.T) {
	outDir := buildPython(t, streamBlueprint)
	auth := readPy(t, outDir, "src/lib/auth.py")
	for _, want := range []string{
		"def authenticate(",
		"allow_query: bool = False",
		`query_token=request.query_params.get("token") if allow_query else None,`,
		`query_api_key=request.query_params.get("api_key") if allow_query else None,`,
		"if authorization is not None:",
	} {
		if !strings.Contains(auth, want) {
			t.Errorf("auth.py missing %q", want)
		}
	}
	basic := strings.Index(auth, "if strategy == \"basic\":")
	if basic < 0 || strings.Contains(auth[basic:basic+160], "query") {
		t.Errorf("basic auth must stay header-only, got:\n%s", auth)
	}
}

// TestPython_StreamRejections pins the STREAM forms that stay fail-closed. Each
// case must name its feature in the codegen error.
func TestPython_StreamRejections(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"use middleware": {
			src: `middleware audit { before { |> log "x" } }
STREAM /api/x {
  use audit
  stream { |> on timeout(1s) { -> { type: "t" } } }
}
`,
			want: `STREAM endpoint metadata "use"`,
		},
		"cache metadata": {
			src: `STREAM /api/x {
  cache 60s
  stream { |> on timeout(1s) { -> { type: "t" } } }
}
`,
			want: `STREAM endpoint metadata "cache"`,
		},
		"webhook auth": {
			src: `secret SIG
STREAM /api/x {
  auth webhook_sig using(secret.SIG)
  stream { |> on timeout(1s) { -> { type: "t" } } }
}
`,
			want: "STREAM `auth webhook_sig`",
		},
		"bare auth": {
			src: `STREAM /api/x {
  auth bearer
  stream { |> on timeout(1s) { -> { type: "t" } } }
}
`,
			want: "endpoint `auth bearer` without `using(secret.NAME)`",
		},
		"try recover before the stream": {
			src: `secret DATABASE_URL
model item { id uuid primary }
STREAM /api/x {
  <- id uuid required
  |> try {
    |> row = fetch item(id)
  } recover {
    -> 500 "x"
  }
  stream { |> on timeout(1s) { -> { type: "t" } } }
}
`,
			want: "STREAM statement",
		},
		"unknown name in output": {
			src: `STREAM /api/x {
  stream { |> on event(e) { -> { body: missing_value } } }
}
`,
			want: `STREAM handler name "missing_value"`,
		},
		"function call in output": {
			src: `STREAM /api/x {
  stream { |> on event(e) { -> { body: lower(event.body) } } }
}
`,
			want: "value-expression call",
		},
		"nested event field in where": {
			src: `STREAM /api/x {
  stream { |> on event(e) where(payload.room == 1) { -> { body: event.body } } }
}
`,
			want: `nested event field "payload"`,
		},
		"zero timeout": {
			src: `STREAM /api/x {
  stream { |> on timeout(0s) { -> { type: "t" } } }
}
`,
			want: "STREAM `on timeout(0s)` duration",
		},
		"handler output that is not an object": {
			src: `STREAM /api/x {
  stream { |> on event(e) { -> "text" } }
}
`,
			want: "STREAM handler output that is not an object literal",
		},
		"header access in a handler": {
			src: `STREAM /api/x {
  stream { |> on event(e) { -> { h: header.x_trace } } }
}
`,
			want: "header references in a STREAM handler",
		},
		"external emit": {
			src: `POST /api/x {
  <- body string required
  |> emit saved to(billing)
  -> 200 "ok"
}
`,
			want: "`emit` payload that is not an object literal",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			src := `blueprint "x" { version "1.0" port 3000 runtime node }
` + tc.src
			file, errs := parser.ParseFile("t.bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected a rejection containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestPython_StreamAcceptsSupportedBodies checks the forms that must build:
// a pre-stream guard, a `where` filter on an input, and a timeout-only stream.
func TestPython_StreamAcceptsSupportedBodies(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
STREAM /api/rooms/:id/presence {
  <- id uuid required
  stream {
    |> on event(presence) where(room_id == id) {
      -> { online: event.count, members: event.members }
    }
    |> on timeout(30s) {
      -> { online: 0, members: [] }
    }
  }
}
`
	outDir := buildPython(t, src)
	route := readPy(t, outDir, "src/routes/rooms_stream.py")
	for _, want := range []string{
		`return frame_data({"online": 0, "members": []})`,
		`frame_data({"online": event.get("count", MISSING), "members": event.get("members", MISSING)})`,
		`stream.every("ping", 30, _tick_1)`,
	} {
		if !strings.Contains(route, want) {
			t.Errorf("presence stream missing %q, got:\n%s", want, route)
		}
	}
}
