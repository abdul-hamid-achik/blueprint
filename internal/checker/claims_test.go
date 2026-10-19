package checker

import "testing"

// jwtSecret declares the secret the jwt endpoints below verify against.
const jwtSecret = "\nsecret JWT_SECRET required\n"

func TestClaimsAllowedOnJwtEndpoints(t *testing.T) {
	cases := map[string]string{
		"rest": `
GET /api/me {
  auth jwt using(secret.JWT_SECRET)
  |> sub = claims.sub
  -> { sub: claims.sub, role: claims.role, org: claims.org.id }
}
`,
		"stream": `
STREAM /api/live/:id {
  auth jwt using(secret.JWT_SECRET)
  <- id string required
  stream {
    |> on timeout(1s) {
      -> { sub: claims.sub }
    }
  }
}
`,
		"ws": `
WS /ws/chat {
  auth jwt using(secret.JWT_SECRET)
  on_connect {
    -> { sub: claims.sub }
  }
  on_message {
    |> log "from {claims.sub}" level(info)
  }
  on_disconnect {
    |> log "bye {claims.sub}" level(info)
  }
}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if errs := check(t, header+jwtSecret+body); len(errs) > 0 {
				t.Fatalf("claims must check on a jwt %s endpoint: %v", name, errs)
			}
		})
	}
}

// claims is not bound outside jwt endpoints, so it is an unbound identifier
// (C035) with the hint that names the declaration that enables it.
func TestClaimsUnboundWithoutJwt(t *testing.T) {
	cases := map[string]string{
		"no auth": `
GET /api/me {
  -> { sub: claims.sub }
}
`,
		"bearer": `
GET /api/me {
  auth bearer using(secret.JWT_SECRET)
  -> { sub: claims.sub }
}
`,
		"api_key": `
GET /api/me {
  auth api_key using(secret.JWT_SECRET)
  -> { sub: claims.sub }
}
`,
		"basic": `
GET /api/me {
  auth basic using(secret.JWT_SECRET)
  -> { sub: claims.sub }
}
`,
		"ws without jwt": `
WS /ws/chat {
  on_connect {
    -> { sub: claims.sub }
  }
}
`,
		"stream without jwt": `
STREAM /api/live {
  stream {
    |> on timeout(1s) {
      -> { sub: claims.sub }
    }
  }
}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			errs := check(t, header+jwtSecret+body)
			if !hasErrorCode(errs, CodeUnboundIdentifier, `"claims" is only available on endpoints with auth jwt`) {
				t.Fatalf("expected C035 for claims in %s, got %v", name, errs)
			}
			if !hasHint(errs, "claims is available only on endpoints with auth jwt using(secret.NAME)") {
				t.Fatalf("expected the claims hint in %s, got %v", name, errs)
			}
		})
	}
}

// A non-jwt endpoint may still bind its own claims name: only jwt endpoints
// reserve it.
func TestClaimsIsOrdinaryNameWithoutJwt(t *testing.T) {
	src := header + `
GET /api/me {
  |> claims = header.X-Token
  -> { token: claims }
}
`
	if errs := check(t, src); len(errs) > 0 {
		t.Fatalf("claims may be a step binding on a non-jwt endpoint: %v", errs)
	}
}

// On jwt endpoints claims is reserved: a path parameter, input, step binding,
// inject alias, or use middleware named claims is C055.
func TestReservedClaimsOnJwtEndpoints(t *testing.T) {
	cases := map[string]string{
		"path param": `
GET /api/:claims/me {
  auth jwt using(secret.JWT_SECRET)
  -> { ok: true }
}
`,
		"input": `
GET /api/me {
  auth jwt using(secret.JWT_SECRET)
  <- claims string required
  -> { ok: true }
}
`,
		"step binding": `
GET /api/me {
  auth jwt using(secret.JWT_SECRET)
  |> claims = "x"
  -> { ok: true }
}
`,
		"inject alias": `
GET /api/me {
  auth jwt using(secret.JWT_SECRET)
  |> inject header.X-Token as claims
  -> { ok: true }
}
`,
		"ws step binding": `
WS /ws/chat {
  auth jwt using(secret.JWT_SECRET)
  on_message {
    |> claims = "x"
  }
}
`,
		"stream input": `
STREAM /api/live/:id {
  auth jwt using(secret.JWT_SECRET)
  <- id string required
  <- claims string required
  stream {
    |> on timeout(1s) {
      -> { ok: true }
    }
  }
}
`,
		"middleware inject": `
middleware take_claims {
  before {
    |> inject header.X-Token as claims
  }
}

GET /api/me {
  use take_claims
  auth jwt using(secret.JWT_SECRET)
  -> { ok: true }
}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			errs := check(t, header+jwtSecret+body)
			if !hasErrorCode(errs, CodeReservedClaims, "claims is reserved on auth jwt endpoints") {
				t.Fatalf("expected C055 for %s, got %v", name, errs)
			}
		})
	}
}

// The same declarations are fine on endpoints without jwt.
func TestReservedClaimsNotEnforcedWithoutJwt(t *testing.T) {
	errs := check(t, header+jwtSecret+`
GET /api/:claims/me {
  auth bearer using(secret.JWT_SECRET)
  <- claims string required
  -> { ok: true }
}
`)
	if hasErrorCode(errs, CodeReservedClaims, "") {
		t.Fatalf("claims is not reserved without jwt: %v", errs)
	}
}

func hasHint(errs []CheckError, hint string) bool {
	for _, e := range errs {
		if e.Hint == hint {
			return true
		}
	}
	return false
}
