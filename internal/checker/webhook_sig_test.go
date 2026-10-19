package checker

import "testing"

const hookSecret = "\nsecret HOOK_KEY required\n"

// auth webhook_sig signs the request body, so it is rejected where there is
// none to sign (C056).
func TestWebhookSigWithoutBodyRejected(t *testing.T) {
	cases := map[string]string{
		"get": `
GET /api/poll {
  auth webhook_sig using(secret.HOOK_KEY)
  -> 200 "ok"
}
`,
		"stream": `
STREAM /api/live {
  auth webhook_sig using(secret.HOOK_KEY)
  stream {
    |> on timeout(5s) {
      -> { type: "ping" }
    }
  }
}
`,
		"ws": `
WS /ws/chat {
  auth webhook_sig using(secret.HOOK_KEY)
  on_connect {
    |> log "connected"
  }
}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			errs := check(t, header+hookSecret+body)
			found := false
			for _, e := range errs {
				if e.Code == CodeWebhookSigWithoutBody {
					found = true
				}
			}
			if !found {
				t.Fatalf("want %s, got %v", CodeWebhookSigWithoutBody, errs)
			}
		})
	}
}

func TestWebhookSigWithBodyAllowed(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			src := header + hookSecret + method + ` /api/hook {
  auth webhook_sig using(secret.HOOK_KEY)
  -> 200 "ok"
}
`
			for _, e := range check(t, src) {
				if e.Code == CodeWebhookSigWithoutBody {
					t.Fatalf("%s must accept webhook_sig: %v", method, e)
				}
			}
		})
	}
}
