package linter_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/linter"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const minimalHeader = `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}
`

func TestLint_Clean(t *testing.T) {
	src := minimalHeader + `
model item {
  id   uuid   primary
  name string required
}

@ "List all items"
GET /api/items {
  -> 200 "ok"
}

@ "Get a single item"
GET /api/items/:id {
  <- id uuid required
  -> 200 { id: id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for clean file, got %d:", len(issues))
		for _, iss := range issues {
			t.Errorf("  %s", iss)
		}
	}
}

func TestLint_MissingIntent(t *testing.T) {
	src := minimalHeader + `
GET /api/items {
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "intent-on-endpoints" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
		}
	}
	if !found {
		t.Errorf("expected at least one issue with rule 'intent-on-endpoints', got: %v", issues)
	}
}

func TestLint_MissingIntent_Multiple(t *testing.T) {
	src := minimalHeader + `
GET /api/items {
  -> 200 "ok"
}

POST /api/items {
  <- name string required
  -> 201 "created"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	count := 0
	for _, iss := range issues {
		if iss.Rule == "intent-on-endpoints" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 intent-on-endpoints issues (one per endpoint), got %d", count)
	}
}

func TestLint_BlockOrdering(t *testing.T) {
	// Model placed after endpoint violates canonical ordering.
	src := minimalHeader + `
@ "List items"
GET /api/items {
  -> 200 "ok"
}

model item {
  id   uuid   primary
  name string required
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "block-ordering" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
		}
	}
	if !found {
		t.Errorf("expected at least one issue with rule 'block-ordering', got: %v", issues)
	}
}

func TestLint_EmptyEndpoint(t *testing.T) {
	// An endpoint with no inputs AND no stmts should trigger empty-endpoint.
	// We need to work around the "no stmts" constraint — an endpoint with only
	// the closing brace is truly empty.
	src := minimalHeader + `
@ "Empty endpoint"
GET /api/empty {
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "empty-endpoint" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
		}
	}
	if !found {
		t.Errorf("expected at least one issue with rule 'empty-endpoint', got: %v", issues)
	}
}

func TestLint_EmptyEndpoint_WithOutput_NotEmpty(t *testing.T) {
	// An endpoint with only an output stmt has no inputs but has stmts — NOT empty.
	src := minimalHeader + `
@ "Health check"
GET /api/health {
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "empty-endpoint" {
			t.Errorf("endpoint with output stmt should not be flagged as empty, but got: %s", iss)
		}
	}
}

func TestLint_MultipleIssues(t *testing.T) {
	// Combine: missing intent + block ordering (model after endpoint) + empty endpoint.
	src := minimalHeader + `
GET /api/items {
  -> 200 "ok"
}

GET /api/empty {
}

model item {
  id   uuid   primary
  name string required
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	if len(issues) < 3 {
		t.Errorf("expected at least 3 issues (missing intent x2, block-ordering x1, empty-endpoint x1), got %d:", len(issues))
		for _, iss := range issues {
			t.Errorf("  %s", iss)
		}
	}

	rules := map[string]int{}
	for _, iss := range issues {
		rules[iss.Rule]++
	}
	if rules["intent-on-endpoints"] < 2 {
		t.Errorf("expected at least 2 intent-on-endpoints issues, got %d", rules["intent-on-endpoints"])
	}
	if rules["block-ordering"] < 1 {
		t.Errorf("expected at least 1 block-ordering issue, got %d", rules["block-ordering"])
	}
	if rules["empty-endpoint"] < 1 {
		t.Errorf("expected at least 1 empty-endpoint issue, got %d", rules["empty-endpoint"])
	}
}

func TestLint_IssueString(t *testing.T) {
	issue := linter.Issue{
		File:    "test.bp",
		Line:    10,
		Col:     1,
		Level:   "warning",
		Rule:    "intent-on-endpoints",
		Message: "Endpoint GET /api/items is missing an @ intent description",
	}
	s := issue.String()
	if s == "" {
		t.Error("Issue.String() should not be empty")
	}
	// Should contain file, line, level, rule, message
	for _, want := range []string{"test.bp", "10", "warning", "intent-on-endpoints"} {
		found := false
		for i := 0; i+len(want) <= len(s); i++ {
			if s[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Issue.String() = %q, expected it to contain %q", s, want)
		}
	}
}

func TestLint_IntentAnnotated_NoIssue(t *testing.T) {
	// Endpoint WITH intent should produce 0 intent-on-endpoints issues.
	src := minimalHeader + `
@ "Returns health status of the service"
GET /api/health {
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "intent-on-endpoints" {
			t.Errorf("endpoint with intent should not produce intent-on-endpoints issue, got: %s", iss)
		}
	}
}

func TestLint_WherePredicateSelfEqual_NoMatchingInput(t *testing.T) {
	// `where(name == name)` with NO input named `name` in scope — the
	// predicate is genuinely column-against-itself and ambiguous.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Buggy search"
GET /api/widgets {
  |> all = query widget where(name == name)
  -> 200 { items: all }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "where-predicate-self-equal" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
			if iss.Hint == "" {
				t.Errorf("expected non-empty Hint, got %q", iss.Hint)
			}
		}
	}
	if !found {
		t.Errorf("expected where-predicate-self-equal issue, got: %v", issues)
	}
}

func TestLint_WherePredicateSelfEqual_WithMatchingInput_NoIssue(t *testing.T) {
	// `where(name == name)` WITH input `name` — codegen convention applies
	// (left=column, right=input variable). Don't flag.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Lookup by name"
POST /api/widgets {
  <- name string required
  |> existing = query widget where(name == name) first
  |> guard not existing -> 409 "Already exists"
  |> w = save widget { name: name }
  -> 201 { id: w.id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "where-predicate-self-equal" {
			t.Errorf("predicate with matching input should not flag, got: %s", iss)
		}
	}
}

func TestLint_WherePredicateSelfEqual_PathParam_NoIssue(t *testing.T) {
	// URL path param `:id` binds `id`; `where(id == id)` should NOT flag.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Get widget"
GET /api/widgets/:id {
  |> w = query widget where(id == id) first
  -> 200 { id: w.id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "where-predicate-self-equal" {
			t.Errorf("path-param binding should disambiguate predicate, got: %s", iss)
		}
	}
}

func TestLint_WherePredicateSelfEqual_StepBinding_NoIssue(t *testing.T) {
	// A prior step `|> name = ...` binds `name`; `where(name == name)` should not flag.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Compute and lookup"
GET /api/widgets/lookup {
  |> name = "alpha"
  |> w = query widget where(name == name) first
  -> 200 { id: w.id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "where-predicate-self-equal" {
			t.Errorf("step-binding should disambiguate predicate, got: %s", iss)
		}
	}
}

func TestLint_UnusedInput(t *testing.T) {
	// `unused` is declared but never referenced.
	src := minimalHeader + `
@ "Has unused input"
GET /api/items {
  <- unused string required
  <- limit  int default(10)
  -> 200 { items: [], limit: limit }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var foundUnused bool
	var foundLimit bool
	for _, iss := range issues {
		if iss.Rule == "unused-input" {
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
			if iss.Message == "" || iss.Hint == "" {
				t.Errorf("expected non-empty Message and Hint, got Message=%q Hint=%q", iss.Message, iss.Hint)
			}
			// Find which input it is.
			if iss.Line == 0 {
				continue
			}
			// The unused input is at line 14 (header is 5 lines + blank + 2 lines).
			// Rather than hardcoding lines, check by message content.
			switch {
			case strings.Contains(iss.Message, `"unused"`):
				foundUnused = true
			case strings.Contains(iss.Message, `"limit"`):
				foundLimit = true
			}
		}
	}
	if !foundUnused {
		t.Errorf("expected unused-input issue for `unused`, got: %v", issues)
	}
	if foundLimit {
		t.Errorf("input `limit` IS used in output, should not be flagged")
	}
}

func TestLint_UnusedInput_StringInterpolation_NoIssue(t *testing.T) {
	// Input `name` used only inside a string interpolation should NOT be flagged.
	src := minimalHeader + `
@ "Greeting"
GET /api/hello/:name {
  <- name string required
  -> 200 { message: "Hello, {name}!" }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "unused-input" {
			t.Errorf("input used in string interpolation should not flag, got: %s", iss)
		}
	}
}

func TestLint_UnusedInput_SearchParamConvention_NoIssue(t *testing.T) {
	// `<- search string optional` is auto-applied by codegen as an ILIKE filter.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "List widgets"
GET /api/widgets {
  <- search string optional
  |> items = query widget
  -> 200 { items: items }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "unused-input" {
			t.Errorf("search-convention input should not flag, got: %s", iss)
		}
	}
}

func TestLint_UnusedInput_BlockExprShorthand_NoIssue(t *testing.T) {
	// `save widget { name: name }` references `name` in the block expression value.
	src := minimalHeader + `
model widget {
  id   uuid   primary
  name string required
}

@ "Create"
POST /api/widgets {
  <- name string required
  |> w = save widget { name: name }
  -> 201 { id: w.id }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "unused-input" {
			t.Errorf("block-expr value reference should count as usage, got: %s", iss)
		}
	}
}

func TestLint_SecretBeforeModel_Clean(t *testing.T) {
	// secret → model → endpoint: canonical ordering, expect 0 block-ordering issues.
	src := minimalHeader + `
secret DB_URL required

model user {
  id   uuid   primary
  name string required
}

@ "List users"
GET /api/users {
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "block-ordering" {
			t.Errorf("canonical ordering should not produce block-ordering issue, got: %s", iss)
		}
	}
}

func TestLint_UnenforceableWsAuth_BareBearer(t *testing.T) {
	// `auth bearer` has no generic verification codegen for WS transports —
	// the linter should flag it so it isn't silently unauthenticated.
	src := minimalHeader + `
@ "Chat over WebSocket"
WS /ws/chat {
  auth bearer

  on_connect {
    |> log "connected"
  }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	var found bool
	for _, iss := range issues {
		if iss.Rule == "unenforceable-ws-auth" {
			found = true
			if iss.Level != "warning" {
				t.Errorf("expected level 'warning', got %q", iss.Level)
			}
			if !strings.Contains(iss.Message, "bearer") {
				t.Errorf("expected message to mention 'bearer', got %q", iss.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected an 'unenforceable-ws-auth' issue for `auth bearer` on a WS endpoint, got: %v", issues)
	}
}

func TestLint_WsAuth_WebhookSig_NoIssue(t *testing.T) {
	// `auth webhook_sig using(secret.NAME)` IS enforced by genWsRoute (HMAC
	// check run as middleware before the handshake) — no warning expected.
	src := minimalHeader + `
secret SIGNING_KEY required

@ "Webhook relay over WebSocket"
WS /ws/relay {
  auth webhook_sig using(secret.SIGNING_KEY)

  on_connect {
    |> log "connected"
  }
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "unenforceable-ws-auth" {
			t.Errorf("auth webhook_sig is enforced by codegen and should not be flagged, got: %s", iss)
		}
	}
}

func TestLint_RestAuth_NoUnenforceableWsAuthIssue(t *testing.T) {
	// The rule only applies to WS endpoints — REST/STREAM `auth` meta (even
	// bare identifiers) is out of scope for this rule.
	src := minimalHeader + `
@ "List widgets"
GET /api/widgets {
  auth bearer
  -> 200 "ok"
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	issues := linter.Lint(file)

	for _, iss := range issues {
		if iss.Rule == "unenforceable-ws-auth" {
			t.Errorf("REST endpoint auth should not trigger the WS-specific rule, got: %s", iss)
		}
	}
}

// issuesFor lints src and returns only the findings for rule.
func issuesFor(t *testing.T, src, rule string) []linter.Issue {
	t.Helper()
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	var out []linter.Issue
	for _, iss := range linter.Lint(file) {
		if iss.Rule == rule {
			out = append(out, iss)
		}
	}
	return out
}

// wantIssueAt asserts exactly one finding for rule at line, with the given level.
func wantIssueAt(t *testing.T, issues []linter.Issue, rule, level string, line int) {
	t.Helper()
	if len(issues) != 1 {
		t.Fatalf("expected 1 %s issue, got %d", rule, len(issues))
	}
	iss := issues[0]
	if iss.Level != level || iss.Line != line {
		t.Errorf("%s: want %s at line %d, got %s at line %d (%s)", rule, level, line, iss.Level, iss.Line, iss.Message)
	}
	if iss.Hint == "" {
		t.Errorf("%s: expected an actionable hint", rule)
	}
}

func TestLint_UnusedSecret_Flagged(t *testing.T) {
	src := minimalHeader + `
secret API_KEY required

@ "Health"
GET /api/health {
  -> 200 "ok"
}
`
	wantIssueAt(t, issuesFor(t, src, "unused-secret"), "unused-secret", "warning", 7)
}

func TestLint_UnusedEnv_Flagged(t *testing.T) {
	src := minimalHeader + `
env LOG_LEVEL "info"

@ "Health"
GET /api/health {
  -> 200 "ok"
}
`
	wantIssueAt(t, issuesFor(t, src, "unused-env"), "unused-env", "warning", 7)
}

func TestLint_SecretAndEnvReferences_NotFlagged(t *testing.T) {
	// Each declaration is read from a different reference site. None may be
	// reported, so every site the rule must see is covered here.
	src := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
  use cors { origins: [env.ALLOWED_ORIGIN] }
}

secret WEBHOOK_KEY required
secret SVC_TOKEN required
secret DB_PASS required
secret EXPR_KEY required
env ALLOWED_ORIGIN "https://example.com"
env GREETING "hi"
env SECRET_VIA_ENV "x"

external "svc" {
  url:     "https://svc.example.com"
  timeout: 5s
  auth:    bearer(secret.SVC_TOKEN)
}

@ "Webhook"
POST /api/hook {
  auth webhook_sig using(secret.WEBHOOK_KEY)
  <- body string required
  -> 200 { key: env.SECRET_VIA_ENV }
}

@ "Greet"
GET /api/greet {
  -> 200 { msg: "{env.GREETING} there", key: env.EXPR_KEY }
}

@ "Pass"
GET /api/pass {
  |> pw = env.DB_PASS
  -> 200 { ok: pw }
}
`
	for _, rule := range []string{"unused-secret", "unused-env"} {
		if got := issuesFor(t, src, rule); len(got) != 0 {
			t.Errorf("%s should not fire, got %d: %v", rule, len(got), got)
		}
	}
}

func TestLint_ImplicitInfraEnv_NotFlagged(t *testing.T) {
	// DATABASE_URL and REDIS_URL are read by the generators by name.
	src := minimalHeader + `
secret DATABASE_URL required
secret REDIS_URL required

@ "Health"
GET /api/health {
  -> 200 "ok"
}
`
	if got := issuesFor(t, src, "unused-secret"); len(got) != 0 {
		t.Errorf("implicit infra names must not be flagged, got %v", got)
	}
}

func TestLint_UnusedSecret_SuppressedByOpaqueCode(t *testing.T) {
	// A user-owned impl module (or an unresolved @> slot) can read env by
	// name, so the rule stays silent for the whole file.
	src := minimalHeader + `
secret API_KEY required

fn process_data {
  <- input string
  -> string
  impl node {
    module: "./internal/process"
  }
}
`
	if got := issuesFor(t, src, "unused-secret"); len(got) != 0 {
		t.Errorf("impl node may read the secret, rule should be silent, got %v", got)
	}

	gen := minimalHeader + `
secret API_KEY required

@ "Generated"
POST /api/items {
  <- name string required
  @> "validate the name"
  -> 201 "ok"
}
`
	if got := issuesFor(t, gen, "unused-secret"); len(got) != 0 {
		t.Errorf("@> slot may read the secret, rule should be silent, got %v", got)
	}
}

func TestLint_UnusedFn_Flagged(t *testing.T) {
	src := minimalHeader + `
fn calculate_price {
  <- plan string
  -> float
  logic {
    |> when plan == "free" -> 0
  }
}
`
	wantIssueAt(t, issuesFor(t, src, "unused-function"), "unused-function", "warning", 7)
}

func TestLint_UnusedFn_CalledFromEndpointNotFlagged(t *testing.T) {
	src := minimalHeader + `
fn calculate_price {
  <- plan string
  -> float
  logic {
    |> when plan == "free" -> 0
  }
}

@ "Price"
GET /api/price {
  <- plan string required
  |> price = calculate_price(plan)
  -> 200 { price: price }
}
`
	if got := issuesFor(t, src, "unused-function"); len(got) != 0 {
		t.Errorf("fn called from an endpoint should not be flagged, got %v", got)
	}
}

func TestLint_UnusedFn_SelfCallDoesNotCount(t *testing.T) {
	src := minimalHeader + `
fn count_down {
  <- n int
  -> int
  logic {
    |> next = count_down(n)
    -> next
  }
}
`
	wantIssueAt(t, issuesFor(t, src, "unused-function"), "unused-function", "warning", 7)
}

func TestLint_UnusedPipe_CallForms(t *testing.T) {
	unused := minimalHeader + `
pipe validate_input {
  <- data string
  |> guard data != "" -> 400 "Data cannot be empty"
  -> data
}
`
	wantIssueAt(t, issuesFor(t, unused, "unused-function"), "unused-function", "warning", 7)

	// Bare `pipe name` parses as Ident "pipe_name", the call form as FnCall.
	bare := minimalHeader + `
pipe validate_input {
  <- data string
  |> guard data != "" -> 400 "Data cannot be empty"
  -> data
}

@ "Bare pipe"
POST /api/items {
  <- data string required
  |> pipe validate_input
  -> 201 "ok"
}
`
	if got := issuesFor(t, bare, "unused-function"); len(got) != 0 {
		t.Errorf("bare pipe reference should count as a use, got %v", got)
	}

	call := minimalHeader + `
pipe validate_input {
  <- data string
  |> guard data != "" -> 400 "Data cannot be empty"
  -> data
}

@ "Call pipe"
POST /api/items {
  <- data string required
  |> validated = pipe validate_input(data)
  -> 201 { data: validated }
}
`
	if got := issuesFor(t, call, "unused-function"); len(got) != 0 {
		t.Errorf("pipe call should count as a use, got %v", got)
	}
}

func TestLint_UnusedMiddleware_UseForms(t *testing.T) {
	mw := `
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Missing auth"
  }
}
`
	wantIssueAt(t, issuesFor(t, minimalHeader+mw, "unused-function"), "unused-function", "warning", 7)

	perEndpoint := minimalHeader + mw + `
@ "Private"
GET /api/private {
  use require_auth
  -> 200 "ok"
}
`
	if got := issuesFor(t, perEndpoint, "unused-function"); len(got) != 0 {
		t.Errorf("endpoint use should count as applying the middleware, got %v", got)
	}

	global := `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
  use require_auth
}
` + mw
	if got := issuesFor(t, global, "unused-function"); len(got) != 0 {
		t.Errorf("global use should count as applying the middleware, got %v", got)
	}
}

func TestLint_UnpaginatedList_Flagged(t *testing.T) {
	src := minimalHeader + `
model item {
  id   uuid primary
  name string required
}

@ "List items"
GET /api/items {
  |> items = query item
  -> 200 items
}
`
	wantIssueAt(t, issuesFor(t, src, "unpaginated-list"), "unpaginated-list", "info", 14)
}

func TestLint_UnpaginatedList_BlockOutputFlagged(t *testing.T) {
	src := minimalHeader + `
model item {
  id   uuid primary
  name string required
}

@ "List items"
GET /api/items {
  |> items = query item where(name != "")
  -> 200 { items: items }
}
`
	wantIssueAt(t, issuesFor(t, src, "unpaginated-list"), "unpaginated-list", "info", 14)
}

func TestLint_UnpaginatedList_BoundedNotFlagged(t *testing.T) {
	cases := map[string]string{
		"paginate": `
@ "List"
GET /api/items {
  <- page int default(1)
  <- per_page int default(20)
  |> items = query item paginate(page, per_page)
  -> 200 { items: items }
}
`,
		"first": `
@ "One"
GET /api/items {
  |> item = query item first
  -> 200 { item: item }
}
`,
		"post": `
@ "Create"
POST /api/items {
  |> items = query item
  -> 200 { items: items }
}
`,
		"rebound before output": `
@ "List"
GET /api/items {
  |> items = query item
  |> items = query item first
  -> 200 { items: items }
}
`,
		"not returned": `
@ "Count"
GET /api/items {
  |> items = query item
  -> 200 "ok"
}
`,
	}
	for name, endpoint := range cases {
		src := minimalHeader + `
model item {
  id   uuid primary
  name string required
}
` + endpoint
		if got := issuesFor(t, src, "unpaginated-list"); len(got) != 0 {
			t.Errorf("%s: expected no unpaginated-list, got %v", name, got)
		}
	}
}

func TestLint_FetchWithoutGuard_Flagged(t *testing.T) {
	src := minimalHeader + `
model user {
  id   uuid primary
  name string required
}

@ "Get user"
GET /api/users/:id {
  <- id uuid required
  |> user = fetch user(id)
  -> 200 { name: user.name }
}
`
	wantIssueAt(t, issuesFor(t, src, "fetch-without-guard"), "fetch-without-guard", "warning", 15)
}

func TestLint_FetchWithoutGuard_OutputOnlyFlagged(t *testing.T) {
	src := minimalHeader + `
model user {
  id   uuid primary
  name string required
}

@ "Get user"
GET /api/users/:id {
  <- id uuid required
  |> user = fetch user(id)
  -> 200 { user: user }
}
`
	wantIssueAt(t, issuesFor(t, src, "fetch-without-guard"), "fetch-without-guard", "warning", 15)
}

func TestLint_FetchWithoutGuard_HandledNotFlagged(t *testing.T) {
	cases := map[string]string{
		"guard first": `
  |> user = fetch user(id)
  |> guard user -> 404 "User not found"
  -> 200 { name: user.name }
`,
		"when on binding": `
  |> user = fetch user(id)
  |> when user {
    |> log "found"
  }
  -> 200 { name: user.name }
`,
		"try wrapper": `
  |> user = fetch user(id)
  |> try {
    -> 200 { name: user.name }
  } recover {
    -> 500 { error: "failed" }
  }
`,
	}
	for name, body := range cases {
		src := minimalHeader + `
model user {
  id   uuid primary
  name string required
}

model todo {
  id   uuid primary
  done bool required
}

@ "Get"
GET /api/users/:id {
  <- id uuid required` + body + `}
`
		if got := issuesFor(t, src, "fetch-without-guard"); len(got) != 0 {
			t.Errorf("%s: expected no fetch-without-guard, got %v", name, got)
		}
	}
}

func TestLint_FetchWithoutGuard_ModelNameCollision(t *testing.T) {
	// `query todo` names the model, not the fetched variable, so the binding
	// is never read and nothing is reported.
	src := minimalHeader + `
model todo {
  id   uuid primary
  done bool required
}

@ "Get"
GET /api/todos/:id {
  <- id uuid required
  |> todo = fetch todo(id)
  |> rows = query todo where(done == false)
  -> 200 { rows: rows }
}
`
	if got := issuesFor(t, src, "fetch-without-guard"); len(got) != 0 {
		t.Errorf("model name in query must not count as a read of the binding, got %v", got)
	}
}
