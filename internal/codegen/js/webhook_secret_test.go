package js

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// TestWebhookSigFailsClosedOnEmptySecret asserts that every generated
// webhook_sig check (REST, STREAM, WS) rejects an unset or empty secret with
// the same 401 body before it computes the HMAC. Without this, a request signed
// with the empty key would verify.
func TestWebhookSigFailsClosedOnEmptySecret(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

secret WEBHOOK_KEY required

POST /api/hook {
  auth webhook_sig using(secret.WEBHOOK_KEY)
  -> 200 "ok"
}

STREAM /api/updates {
  auth webhook_sig using(secret.WEBHOOK_KEY)

  stream {
    |> on event(update) {
      |> log "sending update"
    }
  }
}

WS /ws/chat {
  auth webhook_sig using(secret.WEBHOOK_KEY)

  on_connect {
    |> log "connected"
  }
  on_message {
    |> log "received"
  }
  on_disconnect {
    |> log "disconnected"
  }
}`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := New().Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	guard := "if (!env.WEBHOOK_KEY) return c.json({ error: 'Invalid signature' }, 401 as const);\n"
	for _, rel := range []string{"src/routes/hook.ts", "src/routes/updates.ts", "src/routes/ws.ts"} {
		data, err := os.ReadFile(filepath.Join(outDir, rel))
		if err != nil {
			t.Fatalf("%s should exist: %v", rel, err)
		}
		body := string(data)
		hmac := strings.Index(body, "createHmac('sha256', env.WEBHOOK_KEY)")
		if hmac == -1 {
			t.Fatalf("%s: expected a webhook HMAC check, got:\n%s", rel, body)
		}
		guardAt := strings.Index(body, guard)
		if guardAt == -1 || guardAt > hmac {
			t.Errorf("%s: empty-secret guard must precede the HMAC check, got:\n%s", rel, body)
		}
	}
}
