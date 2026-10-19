package js

import (
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// genWsConnectionsLib emits src/lib/ws-connections.ts, the per-process registry
// behind `whisper connection(id) { data }`. It is emitted only when the blueprint
// uses whisper, so WS-only blueprints keep their existing output.
func (g *Generator) genWsConnectionsLib() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(wsConnectionsSource)
	return codegen.OutputFile{Path: "src/lib/ws-connections.ts", Content: []byte(b.String())}
}

const wsConnectionsSource = `// WebSocket connection registry for whisper connection(id) { data }.
// Every WS connection gets an id when it opens (connection.id). This module keeps
// the open sockets of this process by id. A whisper is written directly when the
// id is connected here; otherwise it is published on the bp:ws:whisper Redis
// channel, which every instance subscribes to, and the instance that holds the
// connection delivers it. Without REDIS_URL, a whisper to an id that is not
// connected in this process is dropped. Every send goes through wsFanOutSend.
import { wsFanOutSend, type WsSocket } from './backpressure.js';

const WHISPER_CHANNEL = 'bp:ws:whisper';

const _connections = new Map<string, WsSocket>();
const _idBySocket = new WeakMap<WsSocket, string>();
let _whisperSub: any = null;
let _whisperPub: any = null;
let _whisperInit: Promise<void> | null = null;

// The first registration subscribes this instance to the whisper channel. REDIS_URL
// is read from process.env, like the room fan-out; without it nothing is subscribed.
function _ensureWhisperRedis(): Promise<void> {
  const _redisUrl = process.env.REDIS_URL;
  if (!_whisperInit && _redisUrl) {
    _whisperInit = (async () => {
      const { createClient } = await import('redis');
      _whisperSub = createClient({ url: _redisUrl });
      _whisperPub = createClient({ url: _redisUrl });
      await _whisperSub.connect();
      await _whisperPub.connect();
      await _whisperSub.subscribe(WHISPER_CHANNEL, (_msg: string) => _deliverWhisper(_msg));
    })().catch((e: any) => { console.error('Redis whisper init failed:', e); });
  }
  return _whisperInit ?? Promise.resolve();
}

// registerWsConnection adds an open socket under its connection id.
export function registerWsConnection(id: string, ws: WsSocket): void {
  _connections.set(id, ws);
  _idBySocket.set(ws, id);
  void _ensureWhisperRedis();
}

// unregisterWsSocket removes a closed or evicted socket from the registry.
export function unregisterWsSocket(ws: WsSocket): void {
  const id = _idBySocket.get(ws);
  if (id === undefined) return;
  _idBySocket.delete(ws);
  if (_connections.get(id) === ws) _connections.delete(id);
}

function _evictWhisperSocket(ws: WsSocket): void {
  unregisterWsSocket(ws);
}

// _deliverLocal sends data to a connection of this process. It returns false when
// the id is not connected here or the socket is not open.
function _deliverLocal(id: string, data: string): boolean {
  const ws = _connections.get(id);
  if (!ws) return false;
  return wsFanOutSend(ws, data, 'connection ' + id, _evictWhisperSocket);
}

function _deliverWhisper(message: string): void {
  let parsed: { to?: unknown; data?: unknown };
  try {
    parsed = JSON.parse(message);
  } catch {
    return;
  }
  if (typeof parsed.to !== 'string' || typeof parsed.data !== 'string') return;
  _deliverLocal(parsed.to, parsed.data);
}

// wsWhisper sends body, which is already JSON-encoded, to the connection whose
// connection.id is to. An id connected nowhere is a no-op, not an error.
export async function wsWhisper(to: unknown, body: string): Promise<void> {
  if (to === undefined || to === null) return;
  const id = String(to);
  if (_connections.has(id)) {
    _deliverLocal(id, body);
    return;
  }
  await _ensureWhisperRedis();
  if (_whisperPub?.isOpen) {
    await _whisperPub.publish(WHISPER_CHANNEL, JSON.stringify({ to: id, data: body }));
  }
}
`
