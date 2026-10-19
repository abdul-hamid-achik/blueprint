package checker

import "testing"

// C053 covers `using(...)` on the enforced auth strategies (bearer, jwt,
// api_key, basic) and rejects using(...) on strategies that take no secret.
func TestEnforcedAuthUsingChecks(t *testing.T) {
	const secrets = "\nsecret JWT_SECRET required\n"
	tests := []struct {
		name    string
		src     string
		wantMsg string // empty means no error at all
		wantC   string
	}{
		{
			name:    "jwt with declared secret is accepted",
			src:     header + secrets + "GET /x {\n  auth jwt using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantMsg: "",
		},
		{
			name:    "bearer with declared secret is accepted",
			src:     header + secrets + "GET /x {\n  auth bearer using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantMsg: "",
		},
		{
			name:    "api_key with declared secret is accepted",
			src:     header + secrets + "GET /x {\n  auth api_key using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantMsg: "",
		},
		{
			name:    "basic with declared secret is accepted",
			src:     header + secrets + "GET /x {\n  auth basic using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantMsg: "",
		},
		{
			name:    "bare strategy without using stays metadata only",
			src:     header + secrets + "GET /x {\n  auth jwt\n  -> 200 \"ok\"\n}\n",
			wantMsg: "",
		},
		{
			name:    "jwt using with a non-secret argument",
			src:     header + secrets + "GET /x {\n  auth jwt using(env.JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantMsg: "auth jwt requires using(secret.NAME)",
			wantC:   "C053",
		},
		{
			name:    "bearer using with a bare identifier",
			src:     header + secrets + "GET /x {\n  auth bearer using(JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantMsg: "auth bearer requires using(secret.NAME)",
			wantC:   "C053",
		},
		{
			name:    "api_key references an undeclared secret",
			src:     header + secrets + "GET /x {\n  auth api_key using(secret.API_KEY)\n  -> 200 \"ok\"\n}\n",
			wantMsg: `auth api_key references unknown secret "API_KEY"`,
			wantC:   "C053",
		},
		{
			name:    "basic references an undeclared secret",
			src:     header + secrets + "GET /x {\n  auth basic using(secret.CREDS)\n  -> 200 \"ok\"\n}\n",
			wantMsg: `auth basic references unknown secret "CREDS"`,
			wantC:   "C053",
		},
		{
			name:    "session takes no secret",
			src:     header + secrets + "GET /x {\n  auth session using(secret.JWT_SECRET)\n  -> 200 \"ok\"\n}\n",
			wantMsg: "auth session does not accept using(...)",
			wantC:   "C053",
		},
		{
			name:    "STREAM jwt using with an undeclared secret",
			src:     header + secrets + "STREAM /s {\n  auth jwt using(secret.NOPE)\n  stream {\n    |> on timeout(5s) {\n      -> { type: \"ping\" }\n    }\n  }\n}\n",
			wantMsg: `auth jwt references unknown secret "NOPE"`,
			wantC:   "C053",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := check(t, tt.src)
			if tt.wantMsg == "" {
				expectNoErrors(t, errs)
				return
			}
			expectErrorContaining(t, errs, tt.wantMsg)
			for _, e := range errs {
				if e.Message == tt.wantMsg || e.Code == tt.wantC {
					if e.Code != tt.wantC {
						t.Errorf("code = %q, want %q", e.Code, tt.wantC)
					}
					return
				}
			}
			t.Errorf("no error with code %s for %q; got %v", tt.wantC, tt.wantMsg, errs)
		})
	}
}
