package js

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// realtimeFixtures are the blueprints that declare both STREAM and WS endpoints.
var realtimeFixtures = []string{
	"examples/realtime-chat.bp",
	"testdata/valid/all_features.bp",
}

func generateRealtimeFixture(t *testing.T, rel string) map[string]string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(projectRoot(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return generateFanoutFiles(t, string(src))
}

// streamRouteFiles returns the generated STREAM route files (src/routes/*-stream.ts
// or the resource file that holds a stream endpoint).
func streamRouteFiles(files map[string]string) map[string]string {
	out := map[string]string{}
	for path, content := range files {
		if strings.HasPrefix(path, "src/routes/") && strings.Contains(content, "streamSSE(") {
			out[path] = content
		}
	}
	return out
}

func wsRouteFiles(files map[string]string) map[string]string {
	out := map[string]string{}
	for path, content := range files {
		if strings.HasPrefix(path, "src/routes/") && strings.Contains(content, "upgradeWebSocket((") {
			out[path] = content
		}
	}
	return out
}

func TestRealtimeBackpressureLibEmittedWithRealtimeEndpoints(t *testing.T) {
	for _, rel := range realtimeFixtures {
		t.Run(rel, func(t *testing.T) {
			files := generateRealtimeFixture(t, rel)
			lib := fanoutFile(t, files, "src/lib/backpressure.ts")
			for _, want := range []string{
				"export function createSseWriter(",
				"export function wsFanOutSend(",
				"BP_SSE_MAX_PENDING = positiveIntEnv('BP_SSE_MAX_PENDING', 256)",
				"BP_WS_MAX_BUFFERED_BYTES = positiveIntEnv('BP_WS_MAX_BUFFERED_BYTES', 1024 * 1024)",
				"stream.abort();",
				"ws.close(WS_TRY_AGAIN_LATER, 'slow consumer')",
				"'STREAM ' + endpoint + ': dropping slow consumer (reason: slow consumer;",
			} {
				if !strings.Contains(lib, want) {
					t.Errorf("src/lib/backpressure.ts missing %q", want)
				}
			}
			if strings.Contains(lib, "import ") {
				t.Errorf("src/lib/backpressure.ts must not import anything (no new dependencies)")
			}
		})
	}
}

func TestRealtimeBackpressureLibOmittedWithoutRealtimeEndpoints(t *testing.T) {
	files := generateFanoutFiles(t, plainFanoutBlueprint)
	if _, ok := files["src/lib/backpressure.ts"]; ok {
		t.Errorf("src/lib/backpressure.ts should not be generated without STREAM or WS endpoints")
	}
}

func TestStreamRoutesUseSerializedBoundedWriter(t *testing.T) {
	for _, rel := range realtimeFixtures {
		t.Run(rel, func(t *testing.T) {
			streams := streamRouteFiles(generateRealtimeFixture(t, rel))
			if len(streams) == 0 {
				t.Fatalf("%s produced no STREAM route files", rel)
			}
			for path, content := range streams {
				if !strings.Contains(content, "import { createSseWriter } from '../lib/backpressure.js';") {
					t.Errorf("%s does not import createSseWriter", path)
				}
				if n := strings.Count(content, "createSseWriter(stream, "); n == 0 {
					t.Errorf("%s creates no SSE writer", path)
				}
				if n := strings.Count(content, "stream.onAbort(() => _sse.close());"); n != strings.Count(content, "createSseWriter(stream, ") {
					t.Errorf("%s: every SSE writer must be closed on abort (got %d close hooks)", path, n)
				}
				// Every write goes through the queue; no direct writeSSE calls remain
				// in route code (the queue itself lives in src/lib/backpressure.ts).
				if strings.Contains(content, "stream.writeSSE(") {
					t.Errorf("%s writes to the stream directly instead of through _sse", path)
				}
			}
		})
	}
}

func TestWsRouteFanOutGoesThroughBoundedHelper(t *testing.T) {
	// Direct socket sends in fan-out loops are the bug this guards against.
	bareFanOut := regexp.MustCompile(`if \(_w?s?\.readyState === 1\) _\w+\.send\(`)
	for _, rel := range realtimeFixtures {
		t.Run(rel, func(t *testing.T) {
			wsFiles := wsRouteFiles(generateRealtimeFixture(t, rel))
			if len(wsFiles) == 0 {
				t.Fatalf("%s produced no WS route files", rel)
			}
			for path, content := range wsFiles {
				if bareFanOut.MatchString(content) {
					t.Errorf("%s has a fan-out loop that sends without the slow-consumer check", path)
				}
				if strings.Contains(content, "_w.send(_msg)") {
					t.Errorf("%s: Redis subscriber callback sends without the slow-consumer check", path)
				}
				if !strings.Contains(content, "import { wsFanOutSend } from '../lib/backpressure.js';") {
					t.Errorf("%s does not import wsFanOutSend", path)
				}
				if !strings.Contains(content, "function _fanOut(roomId: string, data: string) {") {
					t.Errorf("%s does not define the _fanOut helper", path)
				}
				// The Redis subscriber callback (join) and the local broadcast path both
				// route through _fanOut, whenever the file contains those operations.
				if strings.Contains(content, "_redisSub.subscribe(") && !strings.Contains(content, "(_msg: string) => _fanOut(") {
					t.Errorf("%s: Redis subscriber callback does not deliver through _fanOut", path)
				}
				if strings.Contains(content, "_redisPub.publish(") && !strings.Contains(content, "} else { _fanOut(") {
					t.Errorf("%s: local broadcast fallback does not deliver through _fanOut", path)
				}
			}
		})
	}
}

func TestRealtimeLimitsDocumentedInGeneratedLib(t *testing.T) {
	lib := fanoutFile(t, generateRealtimeFixture(t, "examples/realtime-chat.bp"), "src/lib/backpressure.ts")
	for _, want := range []string{
		"positiveIntEnv(name: string, fallback: number): number",
		"Number.isFinite(value) && value > 0 ? value : fallback",
		"WS_OPEN = 1",
	} {
		if !strings.Contains(lib, want) {
			t.Errorf("backpressure.ts missing %q", want)
		}
	}
}
