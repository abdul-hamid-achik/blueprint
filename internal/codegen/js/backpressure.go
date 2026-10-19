package js

import (
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// genBackpressureLib emits src/lib/backpressure.ts, the bounded-delivery helpers
// shared by STREAM (SSE) routes and WS fan-out. It is emitted whenever the
// blueprint declares STREAM or WS endpoints.
//
// Limits are read once at startup from BP_SSE_MAX_PENDING (events queued per SSE
// client) and BP_WS_MAX_BUFFERED_BYTES (unsent bytes per WS socket). The file
// carries no npm dependencies: the WS helper reads bufferedAmount from the
// underlying `ws` socket that @hono/node-ws exposes as ctx.raw.
func (g *Generator) genBackpressureLib() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(backpressureSource)
	return codegen.OutputFile{Path: "src/lib/backpressure.ts", Content: []byte(b.String())}
}

const backpressureSource = `// Realtime backpressure: bounded delivery for STREAM (SSE) clients and WS rooms.
// Limits are read once at startup from the environment:
//   BP_SSE_MAX_PENDING        events queued per SSE client (default 256)
//   BP_WS_MAX_BUFFERED_BYTES  unsent bytes per WS socket (default 1048576)
// Missing, non-numeric, or non-positive values fall back to the default.
// An SSE client is dropped when it stays more than BP_SSE_MAX_PENDING events
// behind for BP_SSE_SLOW_GRACE_MS, or at once past BP_SSE_HARD_CAP, so a burst
// emitted in one tick does not drop a client that keeps up.

function positiveIntEnv(name: string, fallback: number): number {
  const value = Math.floor(Number(process.env[name]));
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

export const BP_SSE_MAX_PENDING = positiveIntEnv('BP_SSE_MAX_PENDING', 256);
export const BP_WS_MAX_BUFFERED_BYTES = positiveIntEnv('BP_WS_MAX_BUFFERED_BYTES', 1024 * 1024);
export const BP_SSE_SLOW_GRACE_MS = 5000;
export const BP_SSE_HARD_CAP = BP_SSE_MAX_PENDING * 16;

// The subset of Hono's SSE stream that the writer uses.
export interface SseMessage {
  event?: string;
  data: string;
  id?: string;
  retry?: number;
}

export interface SseStream {
  writeSSE(message: SseMessage): Promise<void>;
  abort(): void;
}

export interface SseWriter {
  send(message: SseMessage): void;
  close(): void;
}

// createSseWriter returns the per-client write queue for one STREAM connection.
// send() never blocks: messages are written one at a time, in arrival order, on a
// promise chain, so writes for one client never run concurrently and a slow reader
// cannot stall emit() for other clients. A client that stays more than
// BP_SSE_MAX_PENDING messages behind for BP_SSE_SLOW_GRACE_MS, or reaches
// BP_SSE_HARD_CAP pending messages, is a slow consumer: its stream is aborted
// (which runs its onAbort cleanup, including offStream) and one warning is logged.
// close() discards whatever is still queued when the client disconnects.
export function createSseWriter(stream: SseStream, endpoint: string): SseWriter {
  let pending = 0;
  let overSince = 0;
  let closed = false;
  let tail: Promise<void> = Promise.resolve();
  return {
    send(message) {
      if (closed) return;
      if (pending >= BP_SSE_MAX_PENDING) {
        const now = Date.now();
        if (overSince === 0) overSince = now;
        if (pending >= BP_SSE_HARD_CAP || now - overSince >= BP_SSE_SLOW_GRACE_MS) {
          closed = true;
          console.warn('STREAM ' + endpoint + ': dropping slow consumer (reason: slow consumer; ' + pending + ' events pending)');
          stream.abort();
          return;
        }
      }
      pending++;
      tail = tail
        .then(async () => {
          if (!closed) await stream.writeSSE(message);
        })
        .catch(() => {
          // A failed write means the connection is gone or broken: drop the rest
          // of the queue and abort, so the client's onAbort cleanup runs.
          closed = true;
          stream.abort();
        })
        .finally(() => {
          pending--;
          if (pending < BP_SSE_MAX_PENDING) overSince = 0;
        });
    },
    close() {
      closed = true;
    },
  };
}

// The subset of the Hono WebSocket context used for fan-out. Under @hono/node-ws,
// raw is the underlying ` + "`ws`" + ` WebSocket, which exposes bufferedAmount.
export interface WsSocket {
  readonly readyState: number;
  readonly raw?: unknown;
  send(data: string): void;
  close(code?: number, reason?: string): void;
}

const WS_OPEN = 1;
const WS_TRY_AGAIN_LATER = 1013;

function bufferedBytes(ws: WsSocket): number {
  const raw = ws.raw as { bufferedAmount?: unknown } | undefined;
  const amount = raw?.bufferedAmount;
  return typeof amount === 'number' ? amount : 0;
}

// wsFanOutSend delivers one room message to one socket; every room fan-out send
// goes through it. Sockets that are not OPEN are skipped. A socket whose unsent
// buffer exceeds BP_WS_MAX_BUFFERED_BYTES is a slow consumer: onEvict removes it
// from its rooms, it is closed with 1013 ("slow consumer"), and one warning is
// logged. Returns true when the message was sent.
export function wsFanOutSend(ws: WsSocket, data: string, room: string, onEvict: (ws: WsSocket) => void): boolean {
  if (ws.readyState !== WS_OPEN) return false;
  const buffered = bufferedBytes(ws);
  if (buffered > BP_WS_MAX_BUFFERED_BYTES) {
    console.warn('WS room ' + room + ': dropping slow consumer (reason: slow consumer; ' + buffered + ' bytes buffered)');
    onEvict(ws);
    ws.close(WS_TRY_AGAIN_LATER, 'slow consumer');
    return false;
  }
  ws.send(data);
  return true;
}
`
