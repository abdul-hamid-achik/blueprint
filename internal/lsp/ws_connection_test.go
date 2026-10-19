package lsp

import (
	"strings"
	"testing"
)

const wsConnectionSource = `blueprint "chat" {}
WS /ws/chat {
  on_connect {
    -> { id: connection.id }
  }
  on_message {
    |> whisper connection(message.to) { from: connection.id, body: message.body }
  }
}
`

// connection.id hovers as the WS connection id, and whisper hovers as the
// single-connection built-in.
func TestWsConnection_HoverDocumentsIdAndWhisper(t *testing.T) {
	idx := buildIndex("file:///tmp/ws-connection.bp", wsConnectionSource)
	if len(idx.errors) != 0 {
		t.Fatalf("parse errors: %+v", idx.errors)
	}

	line, col := indexOf(wsConnectionSource, "connection.id")
	hover := computeHover(idx, line, col+len("connection."))
	if !strings.Contains(hover, "connection.id") || !strings.Contains(hover, "UUID") {
		t.Fatalf("unexpected connection.id hover: %q", hover)
	}

	line, col = indexOf(wsConnectionSource, "whisper")
	hover = computeHover(idx, line, col)
	if !strings.Contains(hover, "**whisper**") {
		t.Fatalf("unexpected whisper hover: %q", hover)
	}
}

// connection is a rename-reserved name: renaming a binding to it would shadow
// the WS connection.
func TestWsConnection_NameIsRenameReserved(t *testing.T) {
	if validateRenameName("connection") == nil {
		t.Error("connection accepted as a rename target")
	}
}
