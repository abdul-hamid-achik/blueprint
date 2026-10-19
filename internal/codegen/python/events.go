package python

import (
	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// usesEmit reports whether any endpoint or STREAM body calls the `emit` step.
// Either one needs src/lib/events.py.
func usesEmit(endpoints []*ast.Endpoint) bool {
	found := false
	for _, ep := range endpoints {
		walkEndpointStmtsPy(ep.Stmts, func(s ast.ArrowStmt) {
			if stepCallsEmit(s) {
				found = true
			}
		})
	}
	return found
}

// stepCallsEmit reports whether s is a `|> emit ...` step.
func stepCallsEmit(s ast.ArrowStmt) bool {
	step, ok := s.(*ast.StepStmt)
	if !ok {
		return false
	}
	fn, ok := step.Expr.(*ast.FnCall)
	return ok && fn.Name == "emit"
}

// walkEndpointStmtsPy visits every statement, including those nested in when
// and try/recover bodies.
func walkEndpointStmtsPy(stmts []ast.ArrowStmt, visit func(ast.ArrowStmt)) {
	for _, s := range stmts {
		visit(s)
		switch v := s.(type) {
		case *ast.WhenStmt:
			walkEndpointStmtsPy(v.Body, visit)
		case *ast.TryRecover:
			walkEndpointStmtsPy(v.Try, visit)
			walkEndpointStmtsPy(v.Recover, visit)
		}
	}
}

// genEventsPy emits src/lib/events.py: the in-process event registry behind
// STREAM listeners and the `emit` step, plus the Redis fan-out used when
// REDIS_URL is set in the process environment. WS handlers can call emit()
// from this module too.
func (g *Generator) genEventsPy() codegen.OutputFile {
	return codegen.OutputFile{Path: "src/lib/events.py", Content: []byte(fileHeader(g.sourceFile) + eventsPySource)}
}

// eventsPySource mirrors src/lib/events.ts and src/lib/backpressure.ts from the
// Node target. Differences are documented in docs/language-reference.md: the
// bounded queue counts messages (not bytes), Redis channel payloads keep the
// .bp key spelling, and frames carry the JSON the handler rendered.
const eventsPySource = `import asyncio
import inspect
import json
import math
import os
import queue
import threading
import time
import uuid
from collections import deque
from contextlib import suppress
from typing import Any, AsyncIterator, Callable

from fastapi import Request
from fastapi.encoders import jsonable_encoder
from fastapi.responses import JSONResponse, StreamingResponse

from src.lib.log import logger


def _positive_int_env(name: str, fallback: int) -> int:
    try:
        value = math.floor(float(os.environ[name]))
    except (KeyError, ValueError, OverflowError):
        return fallback
    return value if value > 0 else fallback


# Backpressure knobs, read once at startup like the Node target. A client
# whose queue stays at BP_SSE_MAX_PENDING messages for longer than the grace
# period, or reaches BP_SSE_HARD_CAP, is closed with one warning.
BP_SSE_MAX_PENDING = _positive_int_env("BP_SSE_MAX_PENDING", 256)
BP_SSE_HARD_CAP = BP_SSE_MAX_PENDING * 16
BP_SSE_SLOW_GRACE_S = 5.0

# Random per-process id; Redis messages carrying it are our own echoes.
INSTANCE_ID = uuid.uuid4().hex


class _Missing:
    def __repr__(self) -> str:
        return "MISSING"


# MISSING marks an event field that was absent. frame_data drops those keys,
# the way JSON.stringify drops undefined on the Node target.
MISSING: Any = _Missing()


class StreamError(Exception):
    """A pre-stream failure (guard). Answered as {"error": ...} before any stream starts."""

    def __init__(self, status_code: int, message: str) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.message = message


async def stream_error_handler(request: Request, exc: StreamError) -> JSONResponse:
    return JSONResponse({"error": exc.message}, status_code=exc.status_code)


def frame_data(fields: dict[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in fields.items() if value is not MISSING}


def or_missing(value: Any) -> Any:
    """A header or env value that is absent (None) becomes MISSING, so frame_data drops its key."""
    return MISSING if value is None else value


def wire(value: Any) -> Any:
    """Return value in the JSON form emit payloads arrive in, for where() comparisons."""
    return jsonable_encoder(value)


Handler = Callable[[dict[str, Any]], "tuple[str, dict[str, Any]] | None"]

_lock = threading.Lock()
_listeners: dict[str, list["Stream"]] = {}


def listener_count(event: str) -> int:
    """Number of live STREAM connections listening for event on this instance."""
    with _lock:
        return len(_listeners.get(event, ()))


def emit(event: str, data: dict[str, Any]) -> None:
    """Deliver event to this instance's STREAM listeners and, when REDIS_URL is set, to other instances.

    Safe to call from the event loop or from a worker thread. Local delivery is
    queued onto each listener's own loop, so emit never waits for a slow client.
    """
    payload = jsonable_encoder(data)
    _deliver_local(event, payload)
    _publish(event, payload)


def _deliver_local(event: str, payload: Any) -> None:
    with _lock:
        targets = list(_listeners.get(event, ()))
    for stream in targets:
        stream._post(event, payload)


def _sse_frame(name: str, data: dict[str, Any]) -> bytes:
    body = json.dumps(jsonable_encoder(data), separators=(",", ":"), ensure_ascii=False)
    return ("event: " + name + "\ndata: " + body + "\n\n").encode("utf-8")


class Stream:
    """One STREAM connection: its event handlers, timers and bounded outgoing queue."""

    def __init__(self, endpoint: str) -> None:
        self.endpoint = endpoint
        self._handlers: dict[str, list[Handler]] = {}
        self._timers: list[tuple[str, float, Callable[[], Any]]] = []
        self._tasks: list[asyncio.Task] = []
        # Frames still being computed by an async handler or timer (a handler that calls a fn).
        self._frame_tasks: set[asyncio.Task] = set()
        self._items: deque[bytes] = deque()
        self._pending = 0
        self._over_since: float | None = None
        self._closed = False
        self._loop: asyncio.AbstractEventLoop | None = None
        self._wake: asyncio.Event | None = None

    def on_event(self, event: str, handler: Handler) -> None:
        """Register a handler; it returns (frame name, data) to send, or None to skip the event."""
        self._handlers.setdefault(event, []).append(handler)

    def every(self, name: str, seconds: float, render: Callable[[], Any]) -> None:
        """Send a frame named name every seconds, starting after the first interval."""
        self._timers.append((name, seconds, render))

    def start(self) -> None:
        """Register listeners and timers. Call from the event loop before returning the response."""
        loop = asyncio.get_running_loop()
        self._loop = loop
        self._wake = asyncio.Event()
        with _lock:
            for event in self._handlers:
                _listeners.setdefault(event, []).append(self)
        for name, seconds, render in self._timers:
            self._tasks.append(loop.create_task(self._tick(name, seconds, render)))
        url = os.environ.get("REDIS_URL")
        if url:
            _ensure_fanout(loop, url)
        logger.debug("STREAM %s: connected (%d event listeners, %d timers)", self.endpoint, len(self._handlers), len(self._timers))

    def close(self) -> None:
        """Remove every listener, cancel timers and drop anything still queued. Idempotent."""
        if self._closed:
            return
        self._closed = True
        with _lock:
            for event in self._handlers:
                subscribers = _listeners.get(event, [])
                if self in subscribers:
                    subscribers.remove(self)
                if not subscribers:
                    _listeners.pop(event, None)
        for task in self._tasks:
            task.cancel()
        for task in list(self._frame_tasks):
            task.cancel()
        self._items.clear()
        self._pending = 0
        if self._wake is not None:
            self._wake.set()
        logger.debug("STREAM %s: disconnected, listeners removed", self.endpoint)

    def _post(self, event: str, payload: Any) -> None:
        if self._loop is None:
            return
        try:
            self._loop.call_soon_threadsafe(self._offer, event, payload)
        except RuntimeError:
            # The listener's loop is already closed; the connection is gone.
            pass

    def _offer(self, event: str, payload: Any) -> None:
        for handler in list(self._handlers.get(event, ())):
            if self._closed:
                return
            try:
                frame = handler(payload)
            except Exception:
                logger.exception("STREAM %s: handler failed for %s", self.endpoint, event)
                continue
            if inspect.isawaitable(frame):
                # An async handler (one that calls a fn) answers when it finishes; the frames keep arrival order per handler.
                self._track(asyncio.get_running_loop().create_task(self._finish(event, frame)))
                continue
            if frame is not None:
                self._push(frame[0], frame[1])

    async def _finish(self, event: str, pending: Any) -> None:
        try:
            frame = await pending
        except Exception:
            logger.exception("STREAM %s: handler failed for %s", self.endpoint, event)
            return
        if frame is not None:
            self._push(frame[0], frame[1])

    def _track(self, task: asyncio.Task) -> None:
        self._frame_tasks.add(task)
        task.add_done_callback(self._frame_tasks.discard)

    def _push(self, name: str, data: dict[str, Any]) -> None:
        if self._closed:
            return
        if self._pending >= BP_SSE_MAX_PENDING:
            now = time.monotonic()
            if self._over_since is None:
                self._over_since = now
            if self._pending >= BP_SSE_HARD_CAP or now - self._over_since >= BP_SSE_SLOW_GRACE_S:
                logger.warning("STREAM %s: dropping slow consumer (reason: slow consumer; %d events pending)", self.endpoint, self._pending)
                self.close()
                return
        self._pending += 1
        self._items.append(_sse_frame(name, data))
        if self._wake is not None:
            self._wake.set()

    async def _tick(self, name: str, seconds: float, render: Callable[[], dict[str, Any]]) -> None:
        while True:
            await asyncio.sleep(seconds)
            try:
                data = render()
                if inspect.isawaitable(data):
                    data = await data
            except Exception:
                logger.exception("STREAM %s: timeout handler failed", self.endpoint)
                continue
            self._push(name, data)

    async def frames(self) -> AsyncIterator[bytes]:
        try:
            while True:
                while not self._items:
                    if self._closed:
                        return
                    assert self._wake is not None
                    self._wake.clear()
                    await self._wake.wait()
                if self._closed:
                    return
                frame = self._items.popleft()
                yield frame
                # Counted until the write completes, like Node's pending count.
                if not self._closed:
                    self._pending -= 1
                    if self._pending < BP_SSE_MAX_PENDING:
                        self._over_since = None
        finally:
            self.close()


class EventStream(StreamingResponse):
    """text/event-stream response that always runs Stream.close, even when the client drops mid-write."""

    def __init__(self, stream: Stream) -> None:
        super().__init__(
            stream.frames(),
            headers={"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "Connection": "keep-alive"},
        )
        self._stream = stream

    async def __call__(self, scope: Any, receive: Any, send: Any) -> None:
        try:
            await super().__call__(scope, receive, send)
        finally:
            self._stream.close()


# ---- Redis fan-out (only when REDIS_URL is set in the process environment) ----

_fanout_loop: asyncio.AbstractEventLoop | None = None
_fanout_task: asyncio.Task | None = None
_outbox: queue.Queue = queue.Queue(maxsize=1000)
_publisher_started = False
_redis_error_logged = False


def _channel(event: str) -> str:
    return "bp:stream:" + event


def _log_redis_error(exc: BaseException) -> None:
    global _redis_error_logged
    if _redis_error_logged:
        return
    _redis_error_logged = True
    logger.error("STREAM Redis fan-out unavailable; delivering to local clients only: %s", exc)


def _publish(event: str, payload: Any) -> None:
    global _publisher_started
    url = os.environ.get("REDIS_URL")
    if not url:
        return
    with _lock:
        if not _publisher_started:
            _publisher_started = True
            threading.Thread(target=_publisher, args=(url,), name="bp-stream-publisher", daemon=True).start()
    message = json.dumps({"origin": INSTANCE_ID, "event": event, "payload": payload}, separators=(",", ":"), ensure_ascii=False)
    try:
        _outbox.put_nowait((_channel(event), message))
    except queue.Full:
        _log_redis_error(RuntimeError("publish backlog is full"))


def _publisher(url: str) -> None:
    import redis

    client = redis.Redis.from_url(url)
    while True:
        channel, message = _outbox.get()
        try:
            client.publish(channel, message)
        except Exception as exc:
            _log_redis_error(exc)


def _ensure_fanout(loop: asyncio.AbstractEventLoop, url: str) -> None:
    global _fanout_loop, _fanout_task
    if _fanout_loop is loop and _fanout_task is not None and not _fanout_task.done():
        return
    _fanout_loop = loop
    _fanout_task = loop.create_task(_fanout_forever(url))


async def _fanout_forever(url: str) -> None:
    while True:
        try:
            await _fanout_session(url)
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            _log_redis_error(exc)
        await asyncio.sleep(1.0)


async def _fanout_session(url: str) -> None:
    import redis.asyncio as redis_async

    client = redis_async.Redis.from_url(url, decode_responses=True)
    pubsub = client.pubsub()
    subscribed: set[str] = set()
    try:
        while True:
            # Reconcile with the live listeners: subscribe new events, drop unused ones.
            with _lock:
                desired = {_channel(event) for event in _listeners}
            if desired - subscribed:
                await pubsub.subscribe(*sorted(desired - subscribed))
            if subscribed - desired:
                await pubsub.unsubscribe(*sorted(subscribed - desired))
            subscribed = desired
            if not subscribed:
                await asyncio.sleep(0.2)
                continue
            message = await pubsub.get_message(ignore_subscribe_messages=True, timeout=0.2)
            if message is not None and message.get("type") == "message":
                _on_remote(str(message["channel"]), str(message["data"]))
    finally:
        with suppress(Exception):
            await pubsub.reset()


def _on_remote(channel: str, raw: str) -> None:
    try:
        envelope = json.loads(raw)
    except ValueError as exc:
        logger.error("STREAM fan-out: malformed message on %s: %s", channel, exc)
        return
    if not isinstance(envelope, dict) or envelope.get("origin") == INSTANCE_ID:
        return
    event = envelope.get("event")
    if not isinstance(event, str) or channel != _channel(event):
        return
    payload = envelope.get("payload")
    _deliver_local(event, payload if isinstance(payload, dict) else {})
`
