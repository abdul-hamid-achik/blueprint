package python_test

import (
	"os"
	"strings"
	"testing"
)

// readFixture returns testdata/valid/<name>, the fixtures the Python runtime checks run.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../../testdata/valid/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

// assertContains fails for each want that is missing from the text.
func assertContains(t *testing.T, label, text string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s is missing %q:\n%s", label, want, text)
		}
	}
}

func TestPython_RealtimeSurfaceFixtureBuilds(t *testing.T) {
	out := buildPython(t, readFixture(t, "python_realtime_surface.bp"))

	ws := readPy(t, out, "src/routes/rooms_ws.py")
	assertContains(t, "WS route", ws, []string{
		// Headers are read from the handshake, before accept(), into one dict.
		`bp_headers = {"x-trace": websocket.headers.get("x-trace")}`,
		"await websocket.accept()",
		// Every handler takes the dict as a keyword, as it takes `use` values.
		"def _ws_rooms_id_connect(id, connection, *, bp_headers=None):",
		"def _ws_rooms_id_message(message, id, connection, *, bp_headers=None):",
		"result = await run_in_threadpool(_ws_rooms_id_message, message, id, connection, bp_headers=bp_headers)",
		// A missing header or env value drops its key, as undefined does on Node.
		`**({"trace": bp_headers.get("x-trace")} if (bp_headers.get("x-trace")) is not None else {})`,
		`**({"greeting": env.GREETING} if (env.GREETING) is not None else {})`,
		"from src.lib.env import env",
		// An inline `when` runs its step under the if.
		`if js_truthy((ws_get(message, "body") == "close")):` + "\n            _fx.append((\"close\", 1000, \"\"))\n            return WsResult(_fx, None)",
		// try/recover: the failed try keeps the frames it sent; recover sees str(error).
		"        try:\n            _fx.append((\"send\", ws_dumps({\"echo\": shout(ws_get(message, \"body\"))})))",
		"        except Exception as error:\n            _fx.append((\"send\", ws_dumps({\"type\": \"error\", \"reason\": str(error)})))",
		// A declared fn is imported and called from the sync helper.
		"from src.functions.shout import shout",
	})
	if strings.Contains(ws, "except HTTPException") {
		t.Errorf("a WS try must recover from a guard, not re-raise HTTPException:\n%s", ws)
	}

	stream := readPy(t, out, "src/routes/rooms_stream.py")
	assertContains(t, "STREAM route", stream, []string{
		`bp_headers = {"x-trace": bp_request.headers.get("x-trace")}`,
		"_pre = await run_in_threadpool(_pre_stream_rooms_id_greetings, id, bp_headers)",
		"def _pre_stream_rooms_id_greetings(id, bp_headers):",
		"    except Exception as error:\n        logger.error(",
		// A handler that calls a fn runs in a worker thread and is awaited by the Stream.
		"def _on_event_0_sync(event):",
		"async def _on_event_0(event):\n        return await run_in_threadpool(_on_event_0_sync, event)",
		`"text": shout(event.get("text", MISSING))`,
		`"trace": or_missing(bp_headers.get("x-trace"))`,
		`"greeting": or_missing(env.GREETING)`,
		"def _tick_1():\n        return frame_data({\"type\": \"ping\", \"trace\": or_missing(bp_headers.get(\"x-trace\"))})",
		"from src.lib.env import env",
		"from src.functions.shout import shout",
	})

	events := readPy(t, out, "src/lib/events.py")
	assertContains(t, "events.py", events, []string{
		"import inspect",
		"def or_missing(value: Any) -> Any:",
		"if inspect.isawaitable(frame):",
		"if inspect.isawaitable(data):",
	})
}

func TestPython_WsSurfaceIsAbsentWithoutHeadersEnvOrFns(t *testing.T) {
	// A WS endpoint that reads neither header, env nor a fn keeps its earlier shape:
	// no header dict, no env import, no fn import.
	out := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
WS /ws/x/:room {
  <- room string required
  on_connect { |> join room(room) }
  on_message { -> { echo: message.body } }
}
`)
	route := readPy(t, out, "src/routes/x_ws.py")
	for _, banned := range []string{"bp_headers", "from src.lib.env import env", "src.functions."} {
		if strings.Contains(route, banned) {
			t.Errorf("WS route without header, env or fn reads contains %q", banned)
		}
	}
}

func TestPython_WsFnCallsAndInlineWhenBuild(t *testing.T) {
	out := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
fn shout {
  <- text string
  -> string
  impl node {
    module: "./internal/shout"
    func: "shout"
  }
}
WS /ws/x/:room {
  <- room string required
  on_message {
    |> shouted = shout(message.body)
    |> when message.body == "stop": close
    |> when message.body == "say": broadcast room(room) { text: shouted }
    -> { echo: shouted, trace: header.X-Trace }
  }
}
`)
	route := readPy(t, out, "src/routes/x_ws.py")
	assertContains(t, "WS route", route, []string{
		"shouted = shout(ws_get(message, \"body\"))",
		"if js_truthy((ws_get(message, \"body\") == \"stop\")):",
		"if js_truthy((ws_get(message, \"body\") == \"say\")):\n            _fx.append((\"broadcast\", js_str(room), ws_dumps({\"text\": shouted})))",
		"**({\"trace\": bp_headers.get(\"x-trace\")} if (bp_headers.get(\"x-trace\")) is not None else {})",
		"from src.functions.shout import shout",
	})
}

func TestPython_WsTryWithTwoWritesUsesASavepoint(t *testing.T) {
	// A WS try with two writes and no guard or output rolls its writes back on a
	// SAVEPOINT, the same rule REST and Node's db.transaction use.
	out := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model note {
  id   uuid primary
  body string required
}
WS /ws/notes/:id {
  <- id uuid required
  on_message {
    |> try {
      |> first = save note { body: message.body }
      |> second = save note { body: message.body }
    } recover {
      -> { type: "error", reason: error.message }
    }
  }
}
`)
	route := readPy(t, out, "src/routes/notes_ws.py")
	assertContains(t, "WS route", route, []string{
		"        try:\n            with db.begin_nested():\n                first = schema.Note(body=ws_get(message, \"body\"))",
		"                db.refresh(second)\n            db.commit()\n        except Exception as error:\n            db.rollback()",
		"_fx.append((\"send\", ws_dumps({\"type\": \"error\", \"reason\": str(error)})))",
	})
}
