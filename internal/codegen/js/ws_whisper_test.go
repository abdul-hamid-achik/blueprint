package js

import (
	"strings"
	"testing"
)

const whisperBlueprint = `blueprint "whisper-probe" {
  version "1.0.0"
  port    3100
  runtime node
}

WS /ws/chat {
  on_connect {
    -> { id: connection.id }
  }
  on_message {
    |> whisper connection(message.to) { from: connection.id, body: message.body }
  }
}
`

const wsOnlyBlueprint = `blueprint "ws-only" {
  version "1.0.0"
  port    3100
  runtime node
}

WS /ws/chat {
  on_connect {
    -> { id: connection.id }
  }
}
`

// The registry and whisper delivery live in src/lib/ws-connections.ts: one
// process-wide map, delivery through the backpressure helper, and one Redis
// channel that every instance subscribes to.
func TestWhisperRegistryAndDeliveryLib(t *testing.T) {
	files := generateFanoutFiles(t, whisperBlueprint)
	lib := fanoutFile(t, files, "src/lib/ws-connections.ts")
	for _, want := range []string{
		"import { wsFanOutSend, type WsSocket } from './backpressure.js';",
		"const _connections = new Map<string, WsSocket>();",
		"const WHISPER_CHANNEL = 'bp:ws:whisper';",
		"export function registerWsConnection(id: string, ws: WsSocket): void {",
		"_connections.set(id, ws);",
		"export function unregisterWsSocket(ws: WsSocket): void {",
		"if (_connections.get(id) === ws) _connections.delete(id);",
		"return wsFanOutSend(ws, data, 'connection ' + id, _evictWhisperSocket);",
		"export async function wsWhisper(to: unknown, body: string): Promise<void> {",
		"const _redisUrl = process.env.REDIS_URL;",
		"await import('redis');",
		"await _whisperSub.subscribe(WHISPER_CHANNEL, (_msg: string) => _deliverWhisper(_msg));",
		"if (_whisperPub?.isOpen) {",
		"await _whisperPub.publish(WHISPER_CHANNEL, JSON.stringify({ to: id, data: body }));",
	} {
		if !strings.Contains(lib, want) {
			t.Errorf("ws-connections.ts missing %q:\n%s", want, lib)
		}
	}
	// A whisper to a connection on another instance must not be dropped by a
	// local-only path: only local ids skip publishing.
	if !strings.Contains(lib, "if (_connections.has(id)) {\n    _deliverLocal(id, body);\n    return;\n  }") {
		t.Errorf("wsWhisper must deliver locally and return before publishing:\n%s", lib)
	}
}

// The route wires the registry: connection.id is one uuid per socket, the socket
// registers on open and leaves on close, and whisper awaits wsWhisper with the
// JSON-encoded data.
func TestWhisperRouteWiring(t *testing.T) {
	files := generateFanoutFiles(t, whisperBlueprint)
	routes := wsRouteFiles(files)
	if len(routes) != 1 {
		t.Fatalf("expected one WS route file, got %d", len(routes))
	}
	var route string
	for _, content := range routes {
		route = content
	}
	for _, want := range []string{
		"import { randomUUID } from 'node:crypto';",
		"import { registerWsConnection, unregisterWsSocket, wsWhisper } from '../lib/ws-connections.js';",
		"const _connectionId = randomUUID();",
		"const connection = { id: _connectionId };",
		"registerWsConnection(_connectionId, ws);",
		"unregisterWsSocket(ws);",
		"await wsWhisper(message.to, JSON.stringify({ from: connection.id, body: message.body }));",
		"ws.send(JSON.stringify({ id: connection.id }));",
	} {
		if !strings.Contains(route, want) {
			t.Errorf("WS route missing %q:\n%s", want, route)
		}
	}
	if strings.Contains(route, "// TODO: implement whisper") || strings.Contains(route, "// TODO: implement connection") {
		t.Errorf("whisper and connection must not get unknown-call stubs:\n%s", route)
	}
	// Room fan-out keeps its own destination label through the same helper.
	if !strings.Contains(route, "wsFanOutSend(_w, data, 'room ' + roomId, _evictSocket)") {
		t.Errorf("room fan-out must label its destination through wsFanOutSend:\n%s", route)
	}
}

// Without whisper the registry is not emitted and sockets are not registered,
// but connection.id is still generated for each socket.
func TestWsWithoutWhisperHasNoRegistry(t *testing.T) {
	files := generateFanoutFiles(t, wsOnlyBlueprint)
	if _, ok := files["src/lib/ws-connections.ts"]; ok {
		t.Error("ws-connections.ts must not be emitted when no endpoint uses whisper")
	}
	var route string
	for _, content := range wsRouteFiles(files) {
		route = content
	}
	if strings.Contains(route, "registerWsConnection") || strings.Contains(route, "unregisterWsSocket") {
		t.Errorf("route must not touch the registry without whisper:\n%s", route)
	}
	if !strings.Contains(route, "const connection = { id: _connectionId };") {
		t.Errorf("connection.id must be bound without whisper:\n%s", route)
	}
}
