package js

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// streamFanoutBlueprint has a STREAM endpoint listening for room_event, a subscribe
// handler for the same event, and an endpoint that emits it.
const streamFanoutBlueprint = `blueprint "stream-fanout" {
  version "0.1.0"
  port 3000
  runtime node
}

GET /api/ping {
  |> emit room_event { body: "hi" }
  -> { ok: true }
}

subscribe "room_event" {
  |> log "local subscriber"
}

STREAM /api/rooms/:id/live {
  stream {
    |> on event(room_event) {
      -> { body: event.body }
    }
  }
}`

// plainFanoutBlueprint has the same emit and subscribe wiring but no STREAM endpoint.
const plainFanoutBlueprint = `blueprint "plain-fanout" {
  version "0.1.0"
  port 3000
  runtime node
}

GET /api/ping {
  |> emit room_event { body: "hi" }
  -> { ok: true }
}

subscribe "room_event" {
  |> log "local subscriber"
}`

func generateFanoutFiles(t *testing.T, src string) map[string]string {
	t.Helper()
	files, err := New().Files(parseExternalSource(t, src))
	if err != nil {
		t.Fatalf("Files() error: %v", err)
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = string(f.Content)
	}
	return out
}

func fanoutFile(t *testing.T, files map[string]string, path string) string {
	t.Helper()
	content, ok := files[path]
	if !ok {
		t.Fatalf("%s was not generated", path)
	}
	return content
}

// indexOfOrFail returns strings.Index of want in content and fails the test when absent.
func indexOfOrFail(t *testing.T, content, want string) int {
	t.Helper()
	idx := strings.Index(content, want)
	if idx < 0 {
		t.Fatalf("missing %q in generated events.ts:\n%s", want, content)
	}
	return idx
}

func TestStreamFanoutAbsentWithoutStreamEndpoint(t *testing.T) {
	files := generateFanoutFiles(t, plainFanoutBlueprint)
	events := fanoutFile(t, files, "src/lib/events.ts")

	for _, forbidden := range []string{"bp:stream", "randomUUID", "createClient", "onStream", "setStreamBus", "INSTANCE_ID"} {
		if strings.Contains(events, forbidden) {
			t.Errorf("events.ts without STREAM endpoints must not contain %q:\n%s", forbidden, events)
		}
	}
	if !strings.Contains(events, "export async function emit(event: string, data: unknown) {\n  // Iterate a snapshot") {
		t.Errorf("events.ts without STREAM endpoints should keep the local-only emit:\n%s", events)
	}
	if strings.Contains(fanoutFile(t, files, "package.json"), `"redis"`) {
		t.Error("package.json must not depend on redis without STREAM endpoints or cache")
	}
}

func TestStreamFanoutShapeWithStreamEndpoint(t *testing.T) {
	files := generateFanoutFiles(t, streamFanoutBlueprint)
	events := fanoutFile(t, files, "src/lib/events.ts")

	for _, want := range []string{
		"import { randomUUID } from 'node:crypto';",
		"import { createClient } from 'redis';",
		"export const INSTANCE_ID = randomUUID();",
		"export function onStream(event: string, handler: EventHandler) {",
		"export function offStream(event: string, handler: EventHandler) {",
		"export function setStreamBus(bus: StreamBus | null) {",
		"return 'bp:stream:' + event;",
		"!process.env.VITEST",
		"JSON.stringify({ origin: INSTANCE_ID, event, payload: data })",
		"envelope.origin === INSTANCE_ID",
	} {
		if !strings.Contains(events, want) {
			t.Errorf("events.ts missing %q:\n%s", want, events)
		}
	}

	// emit publishes after local dispatch (in finally), so local handlers stay first.
	emitIdx := indexOfOrFail(t, events, "export async function emit(event: string, data: unknown) {")
	localIdx := strings.Index(events[emitIdx:], "await handler(data);")
	publishIdx := strings.Index(events[emitIdx:], "publishStream(event, data);")
	if localIdx < 0 || publishIdx < 0 || localIdx > publishIdx {
		t.Errorf("emit must dispatch local handlers before publishing to Redis:\n%s", events[emitIdx:])
	}

	// Remote delivery only reaches STREAM listeners: the filter must run before the call.
	deliverIdx := indexOfOrFail(t, events, "function deliverRemote(event: string, message: string) {")
	deliverBody := events[deliverIdx:]
	filterIdx := strings.Index(deliverBody, "if (!streamHandlers.has(handler)) continue;")
	callIdx := strings.Index(deliverBody, "handler(envelope.payload)")
	if filterIdx < 0 || callIdx < 0 || filterIdx > callIdx {
		t.Errorf("deliverRemote must only invoke STREAM listeners:\n%s", deliverBody)
	}
	// And it must not touch the registry except through the STREAM filter.
	if strings.Contains(deliverBody[:callIdx], "emit(") {
		t.Error("deliverRemote must not re-emit into the local bus")
	}

	env := fanoutFile(t, files, "src/lib/env.ts")
	if strings.Contains(env, "REDIS_URL") {
		t.Error("REDIS_URL must stay optional for STREAM-only blueprints (not declared as required in env.ts)")
	}
	if !strings.Contains(fanoutFile(t, files, "package.json"), `"redis": "^5.0.0"`) {
		t.Error("package.json must depend on redis when the blueprint has STREAM endpoints")
	}

	route := fanoutFile(t, files, "src/routes/rooms.ts")
	if !strings.Contains(route, "import { onStream, offStream } from '../lib/events.js';") {
		t.Errorf("stream route must import onStream/offStream:\n%s", route)
	}
	if !strings.Contains(route, "onStream('room_event', _streamHandler0);") ||
		!strings.Contains(route, "stream.onAbort(() => offStream('room_event', _streamHandler0));") {
		t.Errorf("stream route must register and release its listener through onStream/offStream:\n%s", route)
	}
}

// TestStreamFanoutTwoInstancesBun simulates two instances of the generated
// events.ts sharing one in-memory pub/sub broker. It checks that a remote emit
// reaches the remote STREAM listener exactly once, that subscribe handlers never
// run from the Redis path, that self-origin messages are ignored, and that the
// Redis-side channel subscription is released with the last STREAM listener.
func TestStreamFanoutTwoInstancesBun(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not available")
	}

	files := generateFanoutFiles(t, streamFanoutBlueprint)
	events := fanoutFile(t, files, "src/lib/events.ts")

	dir := t.TempDir()
	for _, instance := range []string{"instance-a", "instance-b"} {
		if err := os.MkdirAll(filepath.Join(dir, instance), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, instance, "events.ts"), []byte(events), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Stub the redis package so the import resolves. The test always installs a
	// fake bus first, so this createClient must never be reached.
	redisDir := filepath.Join(dir, "node_modules", "redis")
	if err := os.MkdirAll(redisDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(redisDir, "package.json"), []byte(`{"name":"redis","version":"0.0.0","type":"module","main":"index.js"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(redisDir, "index.js"), []byte(`export function createClient() { throw new Error('real redis client must not be created in this test'); }`), 0o600); err != nil {
		t.Fatal(err)
	}

	testSource := `import { expect, test } from 'bun:test';
import * as A from './instance-a/events.ts';
import * as B from './instance-b/events.ts';

type Sub = { owner: string; onMessage: (message: string) => void };

// Shared in-memory broker, shaped like Redis pub/sub: every subscriber of a
// channel (including the publisher itself) receives each message asynchronously.
const channels = new Map<string, Sub[]>();

function brokerFor(owner: string) {
  return {
    publish(channel: string, message: string) {
      for (const sub of [...(channels.get(channel) ?? [])]) {
        setTimeout(() => sub.onMessage(message), 0);
      }
    },
    subscribe(channel: string, onMessage: (message: string) => void) {
      channels.set(channel, [...(channels.get(channel) ?? []), { owner, onMessage }]);
    },
    unsubscribe(channel: string) {
      const remaining = (channels.get(channel) ?? []).filter((s) => s.owner !== owner);
      if (remaining.length > 0) channels.set(channel, remaining);
      else channels.delete(channel);
    },
  };
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 20));

test('remote emit reaches each remote STREAM listener once; subscribe handlers stay local', async () => {
  A.setStreamBus(brokerFor('A'));
  B.setStreamBus(brokerFor('B'));

  const seen = { aStream: [] as unknown[], aSub: [] as unknown[], bStream: [] as unknown[], bSub: [] as unknown[] };
  const aStream = async (e: unknown) => { seen.aStream.push(e); };
  const bStream = async (e: unknown) => { seen.bStream.push(e); };
  const aSub = async (e: unknown) => { seen.aSub.push(e); };
  const bSub = async (e: unknown) => { seen.bSub.push(e); };

  A.onStream('room_event', aStream);
  B.onStream('room_event', bStream);
  A.on('room_event', aSub);
  B.on('room_event', bSub);

  await B.emit('room_event', { n: 1 });
  await tick();
  expect(seen.bStream).toEqual([{ n: 1 }]);
  expect(seen.bSub).toEqual([{ n: 1 }]);
  expect(seen.aStream).toEqual([{ n: 1 }]);
  expect(seen.aSub).toEqual([]);

  await A.emit('room_event', { n: 2 });
  await tick();
  expect(seen.aStream).toEqual([{ n: 1 }, { n: 2 }]);
  expect(seen.aSub).toEqual([{ n: 2 }]);
  expect(seen.bStream).toEqual([{ n: 1 }, { n: 2 }]);
  expect(seen.bSub).toEqual([{ n: 1 }]);

  A.offStream('room_event', aStream);
  await B.emit('room_event', { n: 3 });
  await tick();
  expect(seen.aStream.length).toBe(2);
  expect(channels.get('bp:stream:room_event')?.map((s) => s.owner)).toEqual(['B']);

  B.offStream('room_event', bStream);
  expect(channels.has('bp:stream:room_event')).toBe(false);

  A.setStreamBus(null);
  B.setStreamBus(null);
});

test('a failing fan-out transport never breaks local emit', async () => {
  A.setStreamBus({
    publish() { throw new Error('redis down'); },
    subscribe() {},
    unsubscribe() {},
  });
  const seen: unknown[] = [];
  const aStream = async (e: unknown) => { seen.push(e); };
  A.onStream('isolated_event', aStream);
  await expect(A.emit('isolated_event', { ok: true })).resolves.toBeUndefined();
  expect(seen).toEqual([{ ok: true }]);
  A.offStream('isolated_event', aStream);
  A.setStreamBus(null);
});

test('without a transport, emit is local-only', async () => {
  A.setStreamBus(null);
  const seen: unknown[] = [];
  const handler = async (e: unknown) => { seen.push(e); };
  A.on('local_only', handler);
  await A.emit('local_only', { x: 1 });
  expect(seen).toEqual([{ x: 1 }]);
});
`
	if err := os.WriteFile(filepath.Join(dir, "fanout.test.ts"), []byte(testSource), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bun", "test", "fanout.test.ts")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("two-instance fan-out test failed: %v\n%s", err, output)
	}
}
