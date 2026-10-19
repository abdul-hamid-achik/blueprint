package python_test

import (
	"strings"
	"testing"
)

// eventKeysBlueprint exercises every place the Node target camel-cases object
// keys on the realtime paths: emit payloads, STREAM frames (event and timeout),
// event field reads and where predicates, WS broadcast and whisper payloads,
// and WS message field reads. REST responses and WS direct replies keep the
// .bp spelling on both targets.
const eventKeysBlueprint = `blueprint "keys" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model room {
  id uuid primary
  name string required
}
POST /api/rooms/:id/messages {
  <- id uuid required
  <- user_id string required
  |> emit new_message { user_id: user_id, room_id: id, meta: { sent_by: user_id } }
  -> 200 { user_id: user_id, room_id: id }
}
STREAM /api/rooms/:id/live {
  <- id uuid required
  stream {
    |> on event(new_message) where(room_id == id) {
      -> { type: "msg", user_id: event.user_id, meta: { sent_by: event.meta.sent_by } }
    }
    |> on timeout(2s) {
      -> { type: "ping", room_id: id }
    }
  }
}
WS /ws/rooms/:id {
  on_connect {
    -> { type: "connected", room_id: id }
  }
  on_message {
    |> broadcast room(id) { type: "msg", user_id: message.user_id }
    |> whisper connection(message.target_conn) { type: "dm", user_id: message.user_id }
    -> { type: "ack", user_id: message.user_id }
  }
}
`

// TestPython_EventKeysMatchNode asserts the wire keys of emit, STREAM frames,
// WS fan-out payloads and event/message reads are the camel-cased keys Node uses.
func TestPython_EventKeysMatchNode(t *testing.T) {
	outDir := buildPython(t, eventKeysBlueprint)

	routes := readPy(t, outDir, "src/routes/rooms.py")
	for _, want := range []string{
		// emit payload, top level and nested: Node renders {roomId, userId, meta: {sentBy}}.
		`bp_emit("new_message", {"userId": user_id, "roomId": id, "meta": {"sentBy": user_id}})`,
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("emit payload missing %q, got:\n%s", want, routes)
		}
	}
	// REST response keeps the .bp spelling.
	if !strings.Contains(routes, `"user_id": user_id, "room_id": id`) {
		t.Errorf("REST response must keep .bp keys, got:\n%s", routes)
	}
}

func TestPython_EventKeysStreamFramesAndReads(t *testing.T) {
	outDir := buildPython(t, eventKeysBlueprint)
	stream := readPy(t, outDir, "src/routes/rooms_stream.py")
	for _, want := range []string{
		// where predicate on a bare event field reads the camel key.
		`if not ((event.get("roomId", MISSING) == wire(id))):`,
		// SSE frame keys are camel-cased; event.<field> reads use the camel key;
		// nested objects are camel-cased too.
		`frame_data({"type": "msg", "userId": event.get("userId", MISSING), "meta": frame_data({"sentBy": (event.get("meta") or {}).get("sentBy", MISSING)})})`,
		// Timeout frame: the key is camel-cased, the scope value is not.
		`frame_data({"type": "ping", "roomId": id})`,
	} {
		if !strings.Contains(stream, want) {
			t.Errorf("STREAM frame missing %q, got:\n%s", want, stream)
		}
	}
}

func TestPython_EventKeysWsPayloadsAndReads(t *testing.T) {
	outDir := buildPython(t, eventKeysBlueprint)
	route := readPy(t, outDir, "src/routes/rooms_ws.py")
	for _, want := range []string{
		// broadcast and whisper payloads are camel-cased; the message read is too.
		`("broadcast", js_str(id), ws_dumps({"type": "msg", "userId": ws_get(message, "userId")}))`,
		`("whisper", ws_get(message, "targetConn"), ws_dumps({"type": "dm", "userId": ws_get(message, "userId")}))`,
		// The direct WS reply keeps the .bp spelling (Node sends it verbatim).
		`("send", ws_dumps({"type": "ack", "user_id": ws_get(message, "userId")}))`,
		`("send", ws_dumps({"type": "connected", "room_id": id}))`,
	} {
		if !strings.Contains(route, want) {
			t.Errorf("WS route missing %q, got:\n%s", want, route)
		}
	}
}
