package python_test

import (
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// wsGuardedBlueprint is a WS endpoint with every pre-accept check: jwt auth, a
// declared uuid path input, and a rate limit, so the handshake order is visible.
const wsGuardedBlueprint = `blueprint "chat" { version "1.0" port 3000 runtime node }
secret JWT_SECRET required
WS /ws/rooms/:id {
  auth jwt using(secret.JWT_SECRET)
  limit 5/min
  <- id uuid required
  on_connect {
    |> log "joined" level(info)
  }
}
`

func readExample(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../../examples/" + name)
	if err != nil {
		t.Fatalf("read example %s: %v", name, err)
	}
	return string(data)
}

func TestWs_RealtimeChatBuildsForPython(t *testing.T) {
	out := buildPython(t, readExample(t, "realtime-chat.bp"))
	app := readPy(t, out, "src/app.py")
	if !strings.Contains(app, "app.include_router(rooms_ws.router)") {
		t.Errorf("app.py does not include the WS router:\n%s", app)
	}
	for _, rel := range []string{"src/lib/realtime.py", "src/lib/events.py", "src/routes/rooms_ws.py"} {
		readPy(t, out, rel)
	}
}

func TestWs_RuntimeImportsRedisClient(t *testing.T) {
	// realtime.py imports redis.asyncio at load time, so every WS project must declare redis.
	out := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
WS /ws/x {
  on_message { |> log "x" }
}
`)
	if pyproject := readPy(t, out, "pyproject.toml"); !strings.Contains(pyproject, `"redis>=5.0"`) {
		t.Errorf("a WS-only project must depend on redis:\n%s", pyproject)
	}
	if rt := readPy(t, out, "src/lib/realtime.py"); !strings.Contains(rt, "import redis.asyncio as redis_async") {
		t.Errorf("realtime.py must import redis.asyncio at load time")
	}
}

func TestWs_HandshakeOrderIsAuthPathLimitAccept(t *testing.T) {
	out := buildPython(t, wsGuardedBlueprint)
	route := readPy(t, out, "src/routes/rooms_ws.py")
	order := []string{
		"claims = authenticate(",
		"id = uuid.UUID(id)",
		"rate_limit.check(websocket, 5, 60000)",
		"await websocket.accept()",
		"result = await run_in_threadpool(_ws_rooms_id_connect",
	}
	last := -1
	for _, want := range order {
		idx := strings.Index(route, want)
		if idx < 0 {
			t.Fatalf("route is missing %q:\n%s", want, route)
		}
		if idx <= last {
			t.Errorf("%q appears before the previous handshake step; Node order is auth, path, limit, then the handshake", want)
		}
		last = idx
	}
	if !strings.Contains(route, "await refuse(websocket, 401)") {
		t.Errorf("auth refusal is not a status-only 401 as on the Node upgrade path:\n%s", route)
	}
}

func TestWs_RoomsAndWhisperEffects(t *testing.T) {
	out := buildPython(t, readExample(t, "realtime-chat.bp"))
	route := readPy(t, out, "src/routes/rooms_ws.py")
	for _, want := range []string{
		`("join", js_str(id))`,
		`("leave", js_str(id))`,
		`("broadcast", js_str(id), ws_dumps({"type": "joined", "sender": sender}))`,
		`("whisper", ws_get(message, "to"), ws_dumps({"type": "dm", "sender": sender, "body": ws_get(message, "body")}))`,
	} {
		if !strings.Contains(route, want) {
			t.Errorf("route is missing room or whisper effect %q", want)
		}
	}
	rt := readPy(t, out, "src/lib/realtime.py")
	for _, want := range []string{
		`WHISPER_CHANNEL = "bp:ws:whisper"`,
		`return "room:" + room`,
		`json.dumps({"to": conn_id, "data": text}`,
		`for conn in list(_rooms.get(room, ())):`,
	} {
		if !strings.Contains(rt, want) {
			t.Errorf("realtime.py is missing %q", want)
		}
	}
}

func TestWs_BackpressureKnobsMatchNode(t *testing.T) {
	out := buildPython(t, readExample(t, "realtime-chat.bp"))
	rt := readPy(t, out, "src/lib/realtime.py")
	for _, want := range []string{
		`BP_WS_MAX_BUFFERED_BYTES = _positive_int_env("BP_WS_MAX_BUFFERED_BYTES", 1024 * 1024)`,
		`self.close(1013, "slow consumer")`,
		`"WS %s: dropping slow consumer (reason: slow consumer; %d bytes buffered)"`,
		`if self._buffered > BP_WS_MAX_BUFFERED_BYTES:`,
	} {
		if !strings.Contains(rt, want) {
			t.Errorf("realtime.py is missing %q", want)
		}
	}
	// Direct replies are queued without the limit check, as on Node.
	direct := rt[strings.Index(rt, "def send_direct"):]
	direct = direct[:strings.Index(direct, "def send_fanout")]
	if strings.Contains(direct, "self._buffered >") {
		t.Errorf("send_direct must not apply the slow-consumer limit:\n%s", direct)
	}
}

func TestWs_GeneratedHandlersHaveNoStubs(t *testing.T) {
	out := buildPython(t, readExample(t, "realtime-chat.bp"))
	route := readPy(t, out, "src/routes/rooms_ws.py")
	for _, banned := range []string{"TODO", "# on_connect", "# on_message", "# on_disconnect", "NotImplemented"} {
		if strings.Contains(route, banned) {
			t.Errorf("WS route contains %q; handlers must be translated or rejected", banned)
		}
	}
}

func TestWs_RejectsUntranslatedBodies(t *testing.T) {
	const head = `blueprint "x" { version "1.0" port 3000 runtime node }
secret JWT_SECRET required
`
	cases := map[string]struct {
		src    string
		reason string
	}{
		"unknown step": {head + `WS /ws/x {
  on_message { |> notify(message) }
}
`, `WS step "notify"`},
		"bare header": {head + `WS /ws/x {
  on_message { |> guard header -> 401 "no" }
}
`, "bare `header`"},
		"nested env read": {head + `WS /ws/x {
  on_message { -> { value: env.JWT_SECRET.length } }
}
`, "nested env read"},
		"webhook signature": {head + `WS /ws/x {
  auth webhook_sig using(secret.JWT_SECRET)
  on_message { |> log "x" }
}
`, "WS `auth webhook_sig`"},
		"non-path input": {head + `WS /ws/x/:id {
  <- name string required
  on_message { |> log "x" }
}
`, `WS input "name" that is not a path parameter`},
		"length of a non-message value": {head + `WS /ws/x {
  on_message { |> guard room.name.length < 2 -> 400 "short" }
}
`, "`.length` on a value that is not a message field"},
		"non-object output": {head + `WS /ws/x {
  on_message { -> "text" }
}
`, "WS output that is not an object literal"},
		"unknown metadata": {head + `WS /ws/x {
  tags ["a"]
  on_message { |> log "x" }
}
`, `WS endpoint metadata "tags"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected the WS body to be rejected")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q does not name %q", err.Error(), tc.reason)
			}
		})
	}
}
