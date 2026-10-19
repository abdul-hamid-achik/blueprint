package checker

import (
	"strings"
	"testing"
)

const whisperHeader = `blueprint "whisper" {
  version "1.0.0"
  runtime node
}
`

func hasErrorCode(errs []CheckError, code string, substr string) bool {
	for _, e := range errs {
		if e.Code == code && strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// whisper connection(id) { data } and connection.id are accepted in every WS
// handler: the connection is bound the same way close is.
func TestWhisperAndConnectionIdAllowedInWSHandlers(t *testing.T) {
	src := whisperHeader + `
WS /ws/chat {
  on_connect {
    |> log "connected {connection.id}" level(info)
    -> { id: connection.id }
  }
  on_message {
    |> whisper connection(message.to) { from: connection.id, body: message.body }
  }
  on_disconnect {
    |> whisper connection(connection.id) { from: connection.id }
  }
}
`
	if errs := check(t, src); len(errs) > 0 {
		t.Fatalf("whisper and connection.id must check in WS handlers: %v", errs)
	}
}

// connection exists only inside WS handlers; outside them it is unbound (C035),
// and whisper is rejected there too.
func TestConnectionAndWhisperOutsideWSAreUnbound(t *testing.T) {
	src := whisperHeader + `
GET /api/me {
  |> log "{connection.id}" level(info)
  |> whisper connection(header.X-Peer) { body: "hi" }
  -> { id: connection.id }
}
`
	errs := check(t, src)
	if !hasErrorCode(errs, CodeUnboundIdentifier, `"connection"`) {
		t.Fatalf("expected C035 for connection outside WS, got %v", errs)
	}
	if !hasErrorCode(errs, CodeUnboundIdentifier, `"whisper" is only available in WS handlers`) {
		t.Fatalf("expected C035 for whisper outside WS, got %v", errs)
	}
}

// connection exposes only id; any other field is an error.
func TestConnectionOnlyExposesID(t *testing.T) {
	src := whisperHeader + `
WS /ws/chat {
  on_connect {
    -> { user: connection.user }
  }
}
`
	errs := check(t, src)
	if !hasErrorCode(errs, CodeInvalidWsTarget, `connection has no field "user"`) {
		t.Fatalf("expected C054 for connection.user, got %v", errs)
	}
}

// whisper addresses one connection. A room target is rejected with a pointer to
// broadcast, and a target without a data block is rejected too.
func TestWhisperRequiresConnectionTarget(t *testing.T) {
	errs := check(t, whisperHeader+`
WS /ws/chat {
  on_message {
    |> whisper room(message.room) { body: message.body }
  }
}
`)
	found := false
	for _, e := range errs {
		if e.Code == CodeInvalidWsTarget && strings.Contains(e.Message, "whisper target must be connection(id)") &&
			strings.Contains(e.Hint, "broadcast room") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected C054 with a broadcast hint for whisper room(x), got %v", errs)
	}

	errs = check(t, whisperHeader+`
WS /ws/chat {
  on_message {
    |> whisper connection(message.to)
  }
}
`)
	if !hasErrorCode(errs, CodeInvalidWsTarget, "whisper needs a data block") {
		t.Fatalf("expected C054 for whisper without a data block, got %v", errs)
	}

}

// The connection id argument is checked like any other expression.
func TestWhisperTargetArgumentIsChecked(t *testing.T) {
	errs := check(t, whisperHeader+`
WS /ws/chat {
  on_message {
    |> whisper connection(recipient) { body: message.body }
  }
}
`)
	if !hasErrorCode(errs, CodeUnboundIdentifier, `"recipient"`) {
		t.Fatalf("expected the connection id argument to be checked, got %v", errs)
	}
}

// The generated handler declares `connection` for every socket, so a WS path
// parameter, step binding, or inject alias with that name is rejected.
func TestConnectionIsReservedInWSEndpoints(t *testing.T) {
	cases := map[string]string{
		"path parameter": `
WS /ws/:connection {
  <- connection string required
  on_connect {
    -> { type: "connected" }
  }
}
`,
		"step binding": `
WS /ws/chat {
  on_message {
    |> when message.type == "x" {
      |> connection = message.body
    }
  }
}
`,
		"inject alias": `
WS /ws/chat {
  on_connect {
    |> inject header.X-Peer as connection
  }
}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			errs := check(t, whisperHeader+body)
			if !hasErrorCode(errs, CodeInvalidWsTarget, "connection") {
				t.Fatalf("expected C054 for %s named connection, got %v", name, errs)
			}
		})
	}
}

// join, leave, and broadcast need the room registry of a generated WS route
// file, so they are unbound (C035) in REST and STREAM handlers.
func TestRoomBuiltinsOutsideWSAreUnbound(t *testing.T) {
	src := whisperHeader + `
POST /api/rooms/:id/notify {
  <- id string required
  |> broadcast room(id) { type: "notice" }
  -> 200 { ok: true }
}

STREAM /api/live/:id {
  <- id string required
  |> join room(id)
  stream {
    |> on timeout(1s) {
      -> { type: "ping" }
    }
  }
}
`
	errs := check(t, src)
	for _, name := range []string{"broadcast", "join"} {
		if !hasErrorCode(errs, CodeUnboundIdentifier, `"`+name+`" is only available in WS handlers`) {
			t.Fatalf("expected C035 for %s outside WS, got %v", name, errs)
		}
	}
}
