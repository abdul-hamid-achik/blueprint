package python

import (
	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// usesRealtime reports whether the file has WS endpoints, which need the
// connection runtime in src/lib/realtime.py.
func usesRealtime(wss []*ast.WsEndpoint) bool {
	return len(wss) > 0
}

// genRealtimePy emits src/lib/realtime.py: the per-process WebSocket runtime.
// It mirrors src/lib/ws-connections.ts, the room helpers in the Node WS route,
// and wsFanOutSend in src/lib/backpressure.ts. Handlers run in a worker thread
// and return their effects; apply_effects carries them out on the event loop,
// so socket and room state is only touched from one thread.
func (g *Generator) genRealtimePy() codegen.OutputFile {
	return codegen.OutputFile{Path: "src/lib/realtime.py", Content: []byte(fileHeader(g.sourceFile) + realtimePySource)}
}

const realtimePySource = `"""WebSocket runtime: connection registry, rooms, whisper, backpressure and Redis fan-out.

Mirrors the Node target's src/lib/ws-connections.ts, the room helpers of the WS
route, and wsFanOutSend in src/lib/backpressure.ts. Handlers run in a worker
thread and return their effects; apply_effects carries them out on the event loop.
"""

import asyncio
import json
import math
import os
import uuid
from contextlib import suppress
from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any

import redis.asyncio as redis_async
from fastapi import WebSocket
from starlette.responses import Response

from src.lib.events import _positive_int_env
from src.lib.events import emit as bp_emit
from src.lib.log import logger

# Read once at startup, like the Node target. A socket whose unsent bytes are
# over this limit when the next room or whisper message reaches it is a slow
# consumer: it is removed from its rooms and closed with 1013 "slow consumer".
BP_WS_MAX_BUFFERED_BYTES = _positive_int_env("BP_WS_MAX_BUFFERED_BYTES", 1024 * 1024)
# How long a closing socket may take to flush its queued frames before its writer is cancelled.
WS_CLOSE_FLUSH_S = 5.0
# How long a join waits for Redis to confirm its room subscription before it publishes.
WS_SUBSCRIBE_WAIT_S = 2.0
WHISPER_CHANNEL = "bp:ws:whisper"


class WsGuardError(Exception):
    """A guard failure in a WS handler. The handler turns it into an {"error": ...} frame."""

    def __init__(self, status_code: int, message: str) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.message = message


class WsResult:
    """The effects one handler invocation produced, in source order, and the bindings it made.

    Effects are tuples: ("send", text), ("whisper", to, text), ("close", code, reason),
    ("join", room), ("leave", room), ("broadcast", room, text), ("emit", event, payload).
    """

    __slots__ = ("effects", "bindings")

    def __init__(self, effects: list, bindings: dict[str, Any] | None = None) -> None:
        self.effects = effects
        self.bindings = bindings or {}


class _CloseFrame:
    __slots__ = ("code", "reason")

    def __init__(self, code: int, reason: str) -> None:
        self.code = code
        self.reason = reason


def _js_default(value: Any) -> Any:
    # JSON.stringify renders a Date as an ISO string with milliseconds and a Z suffix.
    if isinstance(value, datetime):
        if value.tzinfo is None:
            value = value.replace(tzinfo=timezone.utc)
        return value.astimezone(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")
    if isinstance(value, date):
        return value.isoformat()
    if isinstance(value, uuid.UUID):
        return str(value)
    if isinstance(value, Decimal):
        return float(value)
    raise TypeError("not JSON serializable: " + type(value).__name__)


def ws_dumps(value: Any) -> str:
    """Encode a frame the way JSON.stringify does: compact, in insertion order, without ASCII escapes."""
    return json.dumps(value, default=_js_default, separators=(",", ":"), ensure_ascii=False)


def _reject_constant(name: str) -> None:
    raise ValueError("non-finite JSON number " + name)


def ws_parse(text: str) -> Any:
    """JSON.parse for a client frame. NaN and Infinity are rejected, as JavaScript rejects them."""
    return json.loads(text, parse_constant=_reject_constant)


def js_truthy(value: Any) -> bool:
    """JavaScript truthiness: only null, false, 0, NaN and the empty string are falsy here."""
    if value is None or value is False:
        return False
    if isinstance(value, bool):
        return True
    if isinstance(value, (int, float)):
        return value != 0 and not (isinstance(value, float) and math.isnan(value))
    if isinstance(value, str):
        return value != ""
    return True


def js_length(value: Any) -> float:
    """Read .length the way JavaScript does: UTF-16 units for strings, and undefined (inf here) otherwise."""
    if value is None:
        raise TypeError("cannot read property 'length' of null")
    if isinstance(value, str):
        return len(value.encode("utf-16-le")) // 2
    if isinstance(value, list):
        return len(value)
    return math.inf


def ws_get(obj: Any, key: str) -> Any:
    """Read obj.key on a client JSON value: null throws, and other non-objects read as undefined."""
    if obj is None:
        raise TypeError("cannot read property '" + key + "' of null")
    if isinstance(obj, dict):
        return obj.get(key)
    if key == "length":
        return js_length(obj)
    return None


def js_str(value: Any) -> str:
    """String(value), used for room and connection ids."""
    if value is None:
        return "undefined"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, str):
        return value
    if isinstance(value, uuid.UUID):
        return str(value)
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    if isinstance(value, (int, float)):
        return str(value)
    return ws_dumps(value)


class WsConnection:
    """One WebSocket: its connection id, its bounded outgoing queue and the writer task that drains it."""

    def __init__(self, websocket: WebSocket) -> None:
        self.id = str(uuid.uuid4())
        self._websocket = websocket
        self._queue: asyncio.Queue = asyncio.Queue()
        self._buffered = 0
        self._open = True
        self._closing = False
        self._writer: asyncio.Task | None = None

    @property
    def open(self) -> bool:
        return self._open

    def start(self) -> None:
        """Register the socket under its id and start its writer. Call on the event loop after accept()."""
        _connections[self.id] = self
        self._writer = asyncio.get_running_loop().create_task(self._drain())
        _ensure_whisper_subscription()

    def send_direct(self, text: str) -> None:
        """Reply on this socket. Replies are not limited by BP_WS_MAX_BUFFERED_BYTES, as on Node."""
        self._enqueue(text)

    def send_fanout(self, text: str, label: str) -> bool:
        """Deliver a room or whisper message. A socket over the limit is evicted and closed with 1013."""
        if not self._open:
            return False
        if self._buffered > BP_WS_MAX_BUFFERED_BYTES:
            logger.warning("WS %s: dropping slow consumer (reason: slow consumer; %d bytes buffered)", label, self._buffered)
            evict(self)
            self.close(1013, "slow consumer")
            return False
        self._enqueue(text)
        return True

    def close(self, code: int, reason: str) -> None:
        """Queue a close frame behind the frames already queued. Sends after it are dropped."""
        if not self._open:
            return
        self._open = False
        self._closing = True
        self._queue.put_nowait(_CloseFrame(code, reason))

    def _enqueue(self, text: str) -> None:
        if not self._open:
            return
        size = len(text.encode("utf-8"))
        self._buffered += size
        self._queue.put_nowait((text, size))

    async def _drain(self) -> None:
        while True:
            item = await self._queue.get()
            if isinstance(item, _CloseFrame):
                with suppress(Exception):
                    await self._websocket.close(item.code, item.reason)
                return
            text, size = item
            try:
                await self._websocket.send_text(text)
            except Exception:
                # The peer is gone: nothing still queued can be delivered.
                self._open = False
                self._closing = False
                return
            finally:
                self._buffered -= size

    async def finish(self) -> None:
        """Called when the handler is done. A closing socket gets its queued frames out before the writer stops."""
        self._open = False
        if _connections.get(self.id) is self:
            del _connections[self.id]
        writer = self._writer
        if writer is None:
            return
        if self._closing:
            await asyncio.wait({writer}, timeout=WS_CLOSE_FLUSH_S)
        if not writer.done():
            writer.cancel()


# Every structure below is touched only on the event loop.
_connections: dict[str, WsConnection] = {}
# Insertion-ordered members, so a fan-out reaches sockets in the order they joined, as on Node.
_rooms: dict[str, dict[WsConnection, None]] = {}
_pub: Any = None
_pubsub: Any = None
_pubsub_task: asyncio.Task | None = None
_subscribed: set[str] = set()
_confirmed: dict[str, asyncio.Event] = {}
_redis_error_logged = False


def evict(conn: WsConnection) -> None:
    """Remove a socket from every room and from the connection registry."""
    for room in list(_rooms):
        members = _rooms[room]
        members.pop(conn, None)
        if not members:
            _drop_room(room)
    if _connections.get(conn.id) is conn:
        del _connections[conn.id]


def _drop_room(room: str) -> None:
    # An emptied room stops listening for its channel; the unsubscribe runs on the loop.
    _rooms.pop(room, None)
    channel = _room_channel(room)
    if channel in _subscribed:
        asyncio.get_running_loop().create_task(_unsubscribe(channel))


def _room_channel(room: str) -> str:
    return "room:" + room


def _log_redis_error(exc: BaseException) -> None:
    # Log the first failure only, so an outage cannot flood the console.
    global _redis_error_logged
    if _redis_error_logged:
        return
    _redis_error_logged = True
    logger.error("WS Redis pub/sub unavailable; delivering to local sockets only: %s", exc)


def _ensure_whisper_subscription() -> None:
    # Whisper targets can live on any instance, so every instance listens for them.
    if os.environ.get("REDIS_URL"):
        asyncio.get_running_loop().create_task(_subscribe(WHISPER_CHANNEL))


async def _ensure_pubsub() -> Any:
    # The subscriber connection is created on the first subscription, when REDIS_URL is set.
    global _pubsub, _pubsub_task
    if _pubsub is None:
        client = redis_async.Redis.from_url(os.environ["REDIS_URL"], decode_responses=True)
        _pubsub = client.pubsub()
        _pubsub_task = asyncio.get_running_loop().create_task(_reader())
    return _pubsub


async def _subscribe(channel: str) -> None:
    # Returns once Redis confirms the subscription, so a publish that follows is delivered here.
    if not os.environ.get("REDIS_URL"):
        return
    event = _confirmed.setdefault(channel, asyncio.Event())
    if channel in _subscribed:
        if not event.is_set():
            with suppress(asyncio.TimeoutError):
                await asyncio.wait_for(event.wait(), WS_SUBSCRIBE_WAIT_S)
        return
    try:
        pubsub = await _ensure_pubsub()
        event.clear()
        _subscribed.add(channel)
        await pubsub.subscribe(channel)
        with suppress(asyncio.TimeoutError):
            await asyncio.wait_for(event.wait(), WS_SUBSCRIBE_WAIT_S)
    except Exception as exc:
        _subscribed.discard(channel)
        _log_redis_error(exc)


async def _unsubscribe(channel: str) -> None:
    if channel not in _subscribed or _pubsub is None:
        return
    _subscribed.discard(channel)
    with suppress(Exception):
        await _pubsub.unsubscribe(channel)


async def _reader() -> None:
    while True:
        try:
            message = await _pubsub.get_message(timeout=1.0)
            if message is None:
                continue
            kind = message.get("type")
            if kind == "subscribe":
                event = _confirmed.get(str(message.get("channel")))
                if event is not None:
                    event.set()
            elif kind == "message":
                _on_remote(str(message["channel"]), str(message["data"]))
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            # redis-py restores the subscriptions when the connection comes back.
            _log_redis_error(exc)
            await asyncio.sleep(1.0)


def _on_remote(channel: str, raw: str) -> None:
    if channel == WHISPER_CHANNEL:
        _deliver_whisper(raw)
    elif channel.startswith("room:"):
        _fan_out(channel[len("room:"):], raw)


def _deliver_whisper(raw: str) -> None:
    try:
        parsed = json.loads(raw)
    except ValueError:
        return
    if not isinstance(parsed, dict):
        return
    to = parsed.get("to")
    data = parsed.get("data")
    if not isinstance(to, str) or not isinstance(data, str):
        return
    _deliver_local(to, data)


async def _publish(channel: str, text: str) -> bool:
    # False when REDIS_URL is unset or the publish failed; the caller then delivers locally.
    global _pub
    url = os.environ.get("REDIS_URL")
    if not url:
        return False
    try:
        if _pub is None:
            _pub = redis_async.Redis.from_url(url, decode_responses=True)
        await _pub.publish(channel, text)
        return True
    except Exception as exc:
        _log_redis_error(exc)
        return False


def _fan_out(room: str, text: str) -> None:
    for conn in list(_rooms.get(room, ())):
        conn.send_fanout(text, "room " + room)


def _deliver_local(conn_id: str, text: str) -> bool:
    conn = _connections.get(conn_id)
    if conn is None:
        return False
    return conn.send_fanout(text, "connection " + conn_id)


async def whisper(to: Any, text: str) -> None:
    """Send text to the socket whose connection.id is to. An id connected nowhere is a no-op."""
    if to is None:
        return
    conn_id = js_str(to)
    if conn_id in _connections:
        _deliver_local(conn_id, text)
        return
    await _publish(WHISPER_CHANNEL, json.dumps({"to": conn_id, "data": text}, separators=(",", ":")))


async def join(conn: WsConnection, room: str) -> None:
    _rooms.setdefault(room, {})[conn] = None
    await _subscribe(_room_channel(room))


def leave(conn: WsConnection, room: str) -> None:
    members = _rooms.get(room)
    if members is None:
        return
    members.pop(conn, None)
    if not members:
        _drop_room(room)


async def broadcast(room: str, text: str) -> None:
    # Every instance receives the message through its room subscription. Without Redis, only this process delivers.
    if not await _publish(_room_channel(room), text):
        _fan_out(room, text)


async def apply_effects(conn: WsConnection, result: WsResult) -> None:
    # Node runs direct frames as they are reached and room work afterwards, so the
    # order is: sends and whispers, then the close, then joins, leaves, broadcasts and emits.
    for effect in result.effects:
        if effect[0] == "send":
            conn.send_direct(effect[1])
        elif effect[0] == "whisper":
            await whisper(effect[1], effect[2])
    for effect in result.effects:
        if effect[0] == "close":
            conn.close(effect[1], effect[2])
    for effect in result.effects:
        kind = effect[0]
        if kind == "join":
            await join(conn, effect[1])
        elif kind == "leave":
            leave(conn, effect[1])
        elif kind == "broadcast":
            await broadcast(effect[1], effect[2])
        elif kind == "emit":
            bp_emit(effect[1], effect[2])


async def refuse(websocket: WebSocket, status_code: int) -> None:
    """Refuse the handshake with a status and no body, as the Node target's upgrade path does."""
    try:
        await websocket.send_denial_response(Response(status_code=status_code))
    except RuntimeError:
        # The server lacks the websocket.http.response extension: close before accept,
        # and the server answers the handshake with 403.
        await websocket.close()
`
