package js

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// realtimeValidationBlueprint declares path-param inputs on a STREAM and a WS
// endpoint, with no database so the generated app can be imported under bun.
const realtimeValidationBlueprint = `blueprint "realtime-validation" {
  version "0.1.0"
  port 3000
  runtime node
  cache redis
}

@ "Stream room events"
STREAM /api/rooms/:id/live {
  <- id uuid required

  stream {
    |> on timeout(5min) {
      -> { type: "ping" }
    }
  }
}

@ "Stream page events"
STREAM /api/pages/:page/live {
  <- page int min(1)

  stream {
    |> on timeout(5min) {
      -> { type: "ping" }
    }
  }
}

@ "Follow a room over WebSocket"
WS /ws/rooms/:id {
  <- id uuid required

  on_connect {
    -> { type: "connected", id: id }
  }
}
`

func generateRealtimeValidation(t *testing.T) string {
	t.Helper()
	file, errs := parser.ParseFile("realtime-validation.bp", []byte(realtimeValidationBlueprint))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := New().Generate(file, outDir); err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	return outDir
}

func TestRealtimePathInputsGenerateValidation(t *testing.T) {
	outDir := generateRealtimeValidation(t)

	// Each STREAM resource gets its own route file: /api/rooms/... and /api/pages/...
	rooms, err := os.ReadFile(filepath.Join(outDir, "src/routes/rooms.ts"))
	if err != nil {
		t.Fatalf("read rooms stream route: %v", err)
	}
	pages, err := os.ReadFile(filepath.Join(outDir, "src/routes/pages.ts"))
	if err != nil {
		t.Fatalf("read pages stream route: %v", err)
	}
	roomsStr, pagesStr := string(rooms), string(pages)
	for _, want := range []string{
		"import { z } from 'zod';",
		"const _pathParams = z.object({ id: z.string().uuid() }).safeParse({ id: c.req.param('id') });",
		"if (!_pathParams.success) return c.json(_pathParams, 400 as const);",
		"const id = _pathParams.data.id;",
	} {
		if !strings.Contains(roomsStr, want) {
			t.Errorf("rooms stream route missing %q:\n%s", want, roomsStr)
		}
	}
	if !strings.Contains(pagesStr, "z.object({ page: z.coerce.number().int().min(1) })") {
		t.Errorf("pages stream route should apply int constraints to :page:\n%s", pagesStr)
	}
	// Validation must run before the SSE stream is opened.
	if strings.Index(roomsStr, "_pathParams.success") > strings.Index(roomsStr, "streamSSE(c") {
		t.Errorf("stream path validation must precede streamSSE:\n%s", roomsStr)
	}

	ws, err := os.ReadFile(filepath.Join(outDir, "src/routes/ws.ts"))
	if err != nil {
		t.Fatalf("read ws route: %v", err)
	}
	wsStr := string(ws)
	validateAt := strings.Index(wsStr, "if (!_pathParams.success) return c.json(_pathParams, 400 as const);\n  return next();")
	upgradeAt := strings.Index(wsStr, "upgradeWebSocket((c) => {")
	if validateAt < 0 || upgradeAt < 0 || validateAt > upgradeAt {
		t.Fatalf("ws path validation must be a pre-upgrade middleware before upgradeWebSocket:\n%s", wsStr)
	}
	if !strings.Contains(wsStr, "const id = z.object({ id: z.string().uuid() }).parse({ id: c.req.param('id') }).id;") {
		t.Errorf("ws closure should read the typed, validated path param:\n%s", wsStr)
	}
}

func TestRealtimePathValidationRuntime(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not available")
	}
	outDir := generateRealtimeValidation(t)
	requireTypeScriptCompile(t, outDir)

	testSource := `import { expect, test } from 'bun:test';

process.env.VITEST = '1';
const { default: app } = await import('./src/index.ts');

const ROOM = '6f9619ff-8b86-4d11-b0d2-517f1f4e3c2a';

test('STREAM rejects a malformed uuid with the zValidator 400 shape', async () => {
  const res = await app.request('/api/rooms/not-a-uuid/live');
  expect(res.status).toBe(400);
  const body: any = await res.json();
  expect(body.success).toBe(false);
  expect(body.error.issues[0].path).toEqual(['id']);
});

test('STREAM opens the event stream for a valid uuid', async () => {
  const res = await app.request('/api/rooms/' + ROOM + '/live');
  expect(res.status).toBe(200);
  expect(res.headers.get('content-type')).toContain('text/event-stream');
  await res.body?.cancel();
});

test('STREAM applies int constraints to path params', async () => {
  expect((await app.request('/api/pages/0/live')).status).toBe(400);
  expect((await app.request('/api/pages/abc/live')).status).toBe(400);
  const res = await app.request('/api/pages/2/live');
  expect(res.status).toBe(200);
  await res.body?.cancel();
});

test('WS rejects a malformed uuid before the upgrade', async () => {
  const res = await app.request('/ws/rooms/not-a-uuid', {
    headers: { Upgrade: 'websocket', Connection: 'Upgrade' },
  });
  expect(res.status).toBe(400);
  const body: any = await res.json();
  expect(body.success).toBe(false);
  expect(body.error.issues[0].path).toEqual(['id']);
});
`
	if err := os.WriteFile(filepath.Join(outDir, "realtime-validation.test.ts"), []byte(testSource), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bun", "test", "realtime-validation.test.ts")
	cmd.Dir = outDir
	// env.ts parses the declared env schema at import time; this blueprint only declares REDIS_URL.
	cmd.Env = append(os.Environ(), "REDIS_URL=redis://127.0.0.1:1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("realtime validation runtime test failed: %v\n%s", err, output)
	}
	t.Logf("bun test output:\n%s", output)
}
