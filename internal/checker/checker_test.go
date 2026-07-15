package checker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// --- Helpers ---

func check(t *testing.T, src string) []CheckError {
	t.Helper()
	file, parseErrors := parser.ParseFile("test.bp", []byte(src))
	if len(parseErrors) > 0 {
		var msgs []string
		for _, e := range parseErrors {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("unexpected parse errors:\n%s", strings.Join(msgs, "\n"))
	}
	return Check(file)
}

func expectErrors(t *testing.T, errors []CheckError, count int) {
	t.Helper()
	if len(errors) != count {
		var msgs []string
		for _, e := range errors {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("expected %d error(s), got %d:\n%s", count, len(errors), strings.Join(msgs, "\n"))
	}
}

func expectNoErrors(t *testing.T, errors []CheckError) {
	t.Helper()
	expectErrors(t, errors, 0)
}

func expectErrorContaining(t *testing.T, errors []CheckError, substr string) {
	t.Helper()
	for _, e := range errors {
		if strings.Contains(e.Message, substr) {
			return
		}
	}
	var msgs []string
	for _, e := range errors {
		msgs = append(msgs, e.Message)
	}
	t.Fatalf("expected error containing %q, got:\n%s", substr, strings.Join(msgs, "\n"))
}

const header = `blueprint "test" {
  version "1.0"
  port    3000
  runtime node
}
`

const headerWithDB = `blueprint "test" {
  version  "1.0"
  port     3000
  runtime  node
  database postgres
}
`

func TestBlueprintConfigurationIsUnambiguousAndWellTyped(t *testing.T) {
	cases := map[string]struct {
		source string
		want   string
	}{
		"duplicate": {
			source: `blueprint "test" { version "1.0" version "2.0" runtime node }`,
			want:   `duplicate blueprint entry "version"`,
		},
		"version type": {
			source: `blueprint "test" { version 1 runtime node }`,
			want:   "blueprint version must be a string",
		},
		"port type": {
			source: `blueprint "test" { version "1.0" port "3000" runtime node }`,
			want:   "blueprint port must be an integer",
		},
		"port range": {
			source: `blueprint "test" { version "1.0" port 70000 runtime node }`,
			want:   "outside 1..65535",
		},
		"runtime type": {
			source: `blueprint "test" { version "1.0" runtime "node" }`,
			want:   "blueprint runtime must be an identifier",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			errs := check(t, tc.source)
			expectErrors(t, errs, 1)
			expectErrorContaining(t, errs, tc.want)
		})
	}
}

// ═══════════════════════════════════════════════
// Name Uniqueness Tests (1-6)
// ═══════════════════════════════════════════════

func TestDuplicateModelNames(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id uuid primary
}
model user {
  id uuid primary
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate model")
}

func TestDuplicateFnNames(t *testing.T) {
	errs := check(t, header+`
fn process {
  <- data string
  impl node { file: "process.js" }
}
fn process {
  <- data string
  impl node { file: "process.js" }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate fn")
}

func TestDuplicatePipeNames(t *testing.T) {
	errs := check(t, header+`
pipe validate {
  <- name string
  -> name
}
pipe validate {
  <- name string
  -> name
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate pipe")
}

func TestDuplicateMiddlewareNames(t *testing.T) {
	errs := check(t, header+`
middleware auth {
  before {
    |> log "checking auth"
  }
}

middleware auth {
  before {
    |> log "checking auth"
  }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate middleware")
}

func TestTranslationBundleRejectsUnknownKey(t *testing.T) {
	errs := check(t, header+`
locale en default

translation mission_text {
  key "mission.start"
  locale en {
    "mission.missing": "Missing"
  }
}
`)
	expectErrorContaining(t, errs, "defines unknown key")
}

func TestTranslationKeyTypeRejectsUnknownNamespace(t *testing.T) {
	errs := check(t, header+`
type MissionDefinition {
  title_key tkey(mission_text)
}
`)
	expectErrorContaining(t, errs, "unknown translation namespace")
}

func TestSaveSchemaRejectsUnknownModel(t *testing.T) {
	errs := check(t, header+`
save player_progress {
  model save_slot
  version_field save_version
  latest 2
}
`)
	expectErrorContaining(t, errs, "references unknown model")
}

func TestSaveSchemaRejectsMigrationBeyondLatest(t *testing.T) {
	errs := check(t, headerWithDB+`
model save_slot {
  id uuid primary
  save_version int
}

save player_progress {
  model save_slot
  version_field save_version
  latest 2
  migrate 2 -> 3
}
`)
	expectErrorContaining(t, errs, "exceeds latest version")
}

func TestDuplicateEnumNames(t *testing.T) {
	errs := check(t, header+`
enum Status {
  active
  inactive
}
enum Status {
  pending
  done
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate enum")
}

func TestDuplicateSecretNames(t *testing.T) {
	errs := check(t, header+`
secret API_KEY required
secret API_KEY required
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "duplicate secret")
}

// ═══════════════════════════════════════════════
// Naming Convention: snake_case (7-12)
// ═══════════════════════════════════════════════

func TestModelNameNotSnakeCase(t *testing.T) {
	errs := check(t, headerWithDB+`
model MyModel {
  id uuid primary
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestFieldNameNotSnakeCase(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id       uuid   primary
  userName string required
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestPipeNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
pipe ValidateName {
  <- name string
  -> name
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestFnNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
fn ProcessData {
  <- data string
  impl node { file: "process.js" }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestWorkerNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
worker ProcessQueue {
  trigger queue("jobs")
  |> log "processing"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestScheduleNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
schedule DailyCleanup {
  cron "0 0 * * *"
  |> log "cleaning"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

// ═══════════════════════════════════════════════
// Naming Convention: SCREAMING_SNAKE_CASE (13-14)
// ═══════════════════════════════════════════════

func TestSecretNotScreamingSnakeCase(t *testing.T) {
	errs := check(t, header+`
secret apiKey required
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be SCREAMING_SNAKE_CASE")
}

func TestEnvNotScreamingSnakeCase(t *testing.T) {
	errs := check(t, header+`
env maxSize 10mb
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be SCREAMING_SNAKE_CASE")
}

// ═══════════════════════════════════════════════
// Naming Convention: PascalCase (15-17)
// ═══════════════════════════════════════════════

func TestEnumNotPascalCase(t *testing.T) {
	errs := check(t, header+`
enum status {
  active
  inactive
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be PascalCase")
}

func TestTypeNotPascalCase(t *testing.T) {
	errs := check(t, header+`
type image_file {
  url    string
  width  int
  height int
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be PascalCase")
}

func TestAliasNotPascalCase(t *testing.T) {
	errs := check(t, header+`
alias email = string format(email)
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be PascalCase")
}

// ═══════════════════════════════════════════════
// Structural Validation (18-22)
// ═══════════════════════════════════════════════

func TestMissingBlueprintBlock(t *testing.T) {
	// Parse a file that has no blueprint block
	src := `model item {
  id uuid primary
}
`
	file, _ := parser.ParseFile("test.bp", []byte(src))
	errs := Check(file)
	expectErrorContaining(t, errs, "missing blueprint block")
}

func TestArrowOrderingInputAfterStep(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  |> log "step"
  <- name string required
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "input (<-) must come before")
}

func TestArrowOrderingInputAfterOutput(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  -> 200 "ok"
  <- name string required
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "input (<-) must come before")
}

func TestArrowOrderingStepAfterOutput(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  <- name string required
  -> 200 "ok"
  |> log "too late"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "steps (|>) must come before outputs")
}

func TestNestedTryRecover(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  |> try {
    |> try {
      |> log "nested"
    } recover {
      |> log "inner recover"
    }
  } recover {
    |> log "outer recover"
  }
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "try/recover cannot be nested")
}

// ═══════════════════════════════════════════════
// Reference Validation (23-27)
// ═══════════════════════════════════════════════

func TestUnknownTypeRef(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id    uuid      primary
  data  UnknownType required
}
`)
	expectErrorContaining(t, errs, `unknown type "UnknownType"`)
}

func TestUnknownModelRef(t *testing.T) {
	errs := check(t, headerWithDB+`
model post {
  id      uuid primary
  user_id uuid ref(user)
}
`)
	expectErrorContaining(t, errs, `ref references unknown model "user"`)
}

func TestValidModelRef(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id uuid primary
}
model post {
  id      uuid primary
  user_id uuid ref(user)
}
`)
	expectNoErrors(t, errs)
}

func TestTestGroupReferencesUnknownTest(t *testing.T) {
	errs := check(t, header+`
test_group all_tests {
  tests [nonexistent_test]
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `test_group references unknown test "nonexistent_test"`)
}

func TestMiddlewareRefWrongKind(t *testing.T) {
	errs := check(t, headerWithDB+`
model auth {
  id uuid primary
}
POST /api/test {
  use auth
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `"auth" is a model, not a middleware`)
}

func TestUnboundIdentifierInArrowExpression(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  |> copy = missing
  |> missing = "declared too late"
  -> 200 copy
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `unbound identifier "missing"`)
}

func TestArrowIdentifierResolutionAcceptsLocalsGlobalsAndContexts(t *testing.T) {
	errs := check(t, headerWithDB+`
env MODE "test"

enum Plan {
  free
  pro
}

model user {
  id    uuid   primary
  email string required
}

fn normalize {
  <- value string
  -> string
  impl node { module: "./normalize" }
}

middleware require_user {
  before {
    |> inject header.Authorization as current_user
  }
}

POST /api/users/:id {
  use require_user
  <- id uuid required
  <- email string required
  |> existing = query user where(email == email) first
  |> normalized = normalize(email)
  |> selected = Plan.pro
  |> try {
    |> result = normalize(normalized)
  } recover {
    |> log error.message level(error)
  }
  -> 200 {
    id: id,
    existing: existing,
    result: result,
    selected: selected,
    current_user: current_user.id,
    mode: env.MODE,
  }
}
`)
	expectNoErrors(t, errs)
}

func TestArrowIdentifierResolutionRejectsAccidentalRuntimeGlobals(t *testing.T) {
	errs := check(t, header+`
secret API_KEY required

model user {
  id uuid primary
}

GET /api/test {
  -> 200 { auth: auth.id, token: token, key: secret.API_KEY, model: user }
}
`)
	expectErrors(t, errs, 4)
	for _, name := range []string{"auth", "token", "secret", "user"} {
		expectErrorContaining(t, errs, `unbound identifier "`+name+`"`)
	}
}

func TestWebhookPayloadDataIsInScope(t *testing.T) {
	errs := check(t, header+`
secret SIGNING_KEY required

POST /webhook {
  auth webhook_sig using(secret.SIGNING_KEY)
  |> when data.type == "created" {
    |> log data.id
  }
  -> 200 { received: true }
}
`)
	expectNoErrors(t, errs)
}

func TestArrowIdentifierResolutionAcceptsStreamAndWebSocketContexts(t *testing.T) {
	errs := check(t, header+`
WS /ws/:room_id {
  on_connect {
    |> sender = header.Authorization
  }
  on_message {
    |> guard message.body -> 400 "Empty message"
    -> { room_id: room_id, sender: sender, body: message.body }
  }
  on_disconnect {
    |> log sender
  }
}

STREAM /events/:record_id {
  stream {
    |> on event(updated) where(id == record_id) {
      -> { id: record_id, payload: event.payload }
    }
  }
}
`)
	expectNoErrors(t, errs)
}

func TestGlobalMiddlewareInjectionIsNotAnEndpointContext(t *testing.T) {
	errs := check(t, `blueprint "test" {
  version "1.0"
  runtime node
  use require_user
}

middleware require_user {
  before {
    |> inject header.Authorization as current_user
  }
}

GET /api/me {
  -> 200 current_user.id
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `unbound identifier "current_user"`)
}

func TestMiddlewareAfterInjectionIsNotAnEndpointContext(t *testing.T) {
	errs := check(t, header+`
middleware observe_response {
  after {
    |> inject header.X-Trace-Id as trace_id
  }
}

GET /api/test {
  use observe_response
  -> 200 trace_id
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `unbound identifier "trace_id"`)
}

func TestImplicitMapResultBindingIsInScope(t *testing.T) {
	errs := check(t, headerWithDB+`
model product {
  id uuid primary
}

model order_item {
  id uuid primary
}

POST /api/orders {
  |> products = query product
  |> map products: save order_item {}
  -> 200 order_items
}
`)
	expectNoErrors(t, errs)
}

func TestDuplicateEnumVariant(t *testing.T) {
	errs := check(t, header+`
enum Plan {
  free
  pro
  free
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `duplicate variant "free" in enum "Plan"`)
}

func TestDuplicateFunctionInput(t *testing.T) {
	errs := check(t, header+`
fn transform_record {
  <- value string
  <- value int
  -> string
  impl node { module: "./internal/transform-record" }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `duplicate input "value"`)
	if !strings.Contains(errs[0].Hint, "First declared at test.bp:") {
		t.Fatalf("expected duplicate input hint to identify the first declaration, got %q", errs[0].Hint)
	}
}

func TestDuplicateArrowInput(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  <- value string
  <- value int
  -> 200 value
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `duplicate input "value"`)
}

func TestDuplicatePipeInput(t *testing.T) {
	errs := check(t, header+`
pipe normalize_value {
  <- value string
  <- value string
  -> value
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `duplicate input "value"`)
}

func TestDuplicateLocalBinding(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  |> result = "first"
  |> result = "second"
  -> 200 result
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `duplicate local binding "result"`)
	if !strings.Contains(errs[0].Hint, "First bound at test.bp:") {
		t.Fatalf("expected duplicate binding hint to identify the first binding, got %q", errs[0].Hint)
	}
}

func TestInputReassignmentRemainsValid(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  <- value string required
  |> value = value + "x"
  |> value = value + "y"
  -> 200 value
}
`)
	expectNoErrors(t, errs)
}

func TestKnownModelFieldAccessRejectsTypo(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id    uuid   primary
  email string required
}

GET /api/users/:id {
  <- id uuid required
  |> row = fetch user(id)
  -> 200 row.emial
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `model "user" has no field "emial"`)
	if !strings.Contains(errs[0].Hint, `did you mean "email"?`) {
		t.Fatalf("expected field typo suggestion, got %q", errs[0].Hint)
	}
}

func TestKnownModelFieldAccessInMapRejectsTypo(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id    uuid   primary
  email string required
}

POST /api/users/log {
  |> rows = query user
  |> map rows: log item.emial
  -> 204
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `model "user" has no field "emial"`)
}

func TestModelTypedFunctionInputFieldValidation(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id    uuid   primary
  email string required
}

fn user_email {
  <- account user
  -> string
  logic {
    -> account.emial
  }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `model "user" has no field "emial"`)
}

func TestKnownModelFieldAccessAllowsCollectionsPaginationFKAndJSON(t *testing.T) {
	errs := check(t, headerWithDB+`
model product {
  id      uuid   primary
  stock   int    required
  details json   optional
}

model cart_item {
  id         uuid primary
  product_id uuid ref(product) required
}

GET /api/items/:id {
  <- id uuid required
  |> products = query product
  |> page = query product paginate(1, 20)
  |> item = fetch cart_item(id)
  |> product = fetch product(id)
  |> log products.id
  |> log products.length
  |> log page.items
  |> log page.total
  |> log item.product.stock
  -> 200 product.details.anything
}
`)
	expectNoErrors(t, errs)
}

func TestModelInputReassignmentRejectsWrongTypeWithoutStaleFieldDiagnostic(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id uuid primary
}

fn stringify_user {
  <- account user
  -> string
  logic {
    |> account = "plain"
    -> account.anything
  }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `cannot assign string "plain" to input "account" of type model user`)
}

func TestRestPathParameterRequiresInputBinding(t *testing.T) {
	errs := check(t, header+`
GET /api/items/:id {
  -> 200 id
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `unbound identifier "id"`)
}

func TestWorkerOnFailOnlySeesInputsAndError(t *testing.T) {
	errs := check(t, headerWithDB+`
model job {
  id uuid primary
}

worker process_job {
  trigger queue("jobs")
  <- job_id uuid
  |> row = fetch job(job_id)

  on_fail {
    |> log job_id
    |> log error.message
    |> log row.id
  }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `unbound identifier "row"`)
}

func TestWhenBodyBindingDoesNotLeak(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  <- enabled bool required
  |> when enabled {
    |> conditional = "value"
    |> log conditional
  }
  -> 200 conditional
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `unbound identifier "conditional"`)
}

func TestUnknownDataModelStillChecksWhereValues(t *testing.T) {
	errs := check(t, header+`
GET /api/test {
  |> rows = query typo where(id == missing)
  -> 200 rows
}
`)
	expectErrors(t, errs, 2)
	expectErrorContaining(t, errs, `data operation "query" references unknown model "typo"`)
	expectErrorContaining(t, errs, `unbound identifier "missing"`)
}

func TestStringInterpolationIdentifierResolution(t *testing.T) {
	errs := check(t, header+`
POST /api/greet {
  <- name string required
  |> greeting = "Hello {name}"
  |> log "Unknown {missing.value}"
  -> 200 "{greeting}"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `unbound identifier "missing"`)
}

func TestRecoverErrorInterpolationIsInScope(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  |> try {
    |> log "working"
  } recover {
    |> log "Failed: {error.message}"
  }
  -> 200 "ok"
}
`)
	expectNoErrors(t, errs)
}

// ═══════════════════════════════════════════════
// Valid Files — No Errors Expected (28-34)
// ═══════════════════════════════════════════════

func TestValidMinimalFile(t *testing.T) {
	errs := check(t, header)
	expectNoErrors(t, errs)
}

func TestValidModelWithPrimitives(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id      uuid      primary
  name    string    required
  count   int       default(0)
  price   float     optional
  active  bool      default(true)
  created timestamp default(now)
  data    json      optional
}
`)
	expectNoErrors(t, errs)
}

func TestValidEndpointWithArrows(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id   uuid   primary
  name string required
}
POST /api/items {
  <- name string required
  |> log "creating item"
  -> 201 { name: name }
}
`)
	expectNoErrors(t, errs)
}

func TestValidPipe(t *testing.T) {
	errs := check(t, header+`
pipe validate_name {
  <- name string
  |> guard name != "" -> 400 "Name required"
  -> name
}
`)
	expectNoErrors(t, errs)
}

func TestValidEnumAndAlias(t *testing.T) {
	errs := check(t, header+`
enum Status {
  active
  inactive
}
alias Email = string format(email)
`)
	expectNoErrors(t, errs)
}

func TestValidSecretAndEnv(t *testing.T) {
	errs := check(t, header+`
secret API_KEY required
secret DB_URL  required
env MAX_SIZE   10mb
env LOG_LEVEL  "info"
`)
	expectNoErrors(t, errs)
}

func TestValidMiddleware(t *testing.T) {
	errs := check(t, header+`
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Missing auth"
  }
}
`)
	expectNoErrors(t, errs)
}

// ═══════════════════════════════════════════════
// Edge Cases (35-40)
// ═══════════════════════════════════════════════

func TestSingleWordSnakeCase(t *testing.T) {
	// Single word names are valid snake_case
	errs := check(t, headerWithDB+`
model item {
  id   uuid   primary
  name string required
}
`)
	expectNoErrors(t, errs)
}

func TestSingleWordScreamingSnakeCase(t *testing.T) {
	// Single uppercase word is valid SCREAMING_SNAKE_CASE
	errs := check(t, header+`
secret TOKEN required
env PORT 3000
`)
	expectNoErrors(t, errs)
}

func TestMultipleErrors(t *testing.T) {
	errs := check(t, headerWithDB+`
model MyModel {
  id       uuid   primary
  UserName string required
}
`)
	// Should get errors for both model name and field name
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 errors, got %d", len(errs))
	}
}

func TestArrowOrderingInPipe(t *testing.T) {
	errs := check(t, header+`
pipe bad_pipe {
  |> log "step first"
  <- name string
  -> name
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "input (<-) must come before")
}

func TestValidTypeWithFields(t *testing.T) {
	errs := check(t, header+`
type ImageFile {
  url    string
  width  int
  height int
}
`)
	expectNoErrors(t, errs)
}

func TestTypeFieldNameNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
type BadType {
  url       string
  imageSize int
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "must be snake_case")
}

func TestPathParameterNotSnakeCase(t *testing.T) {
	errs := check(t, header+`
GET /api/items/:itemId {
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, "path parameter")
}

func TestValidPathParameter(t *testing.T) {
	errs := check(t, header+`
GET /api/items/:item_id {
  -> 200 "ok"
}
`)
	expectNoErrors(t, errs)
}

// ═══════════════════════════════════════════════
// File-Based Tests — Valid Fixtures (41)
// ═══════════════════════════════════════════════

func TestValidFixturesPassChecker(t *testing.T) {
	files, err := filepath.Glob("../../testdata/valid/*.bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no valid fixtures found")
	}

	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			file, parseErrors := parser.ParseFile(name, src)
			if len(parseErrors) > 0 {
				t.Skipf("parse errors (not a checker concern): %v", parseErrors)
			}

			checkErrors := Check(file)
			if len(checkErrors) > 0 {
				var msgs []string
				for _, e := range checkErrors {
					msgs = append(msgs, e.Error())
				}
				t.Errorf("checker errors in valid fixture:\n%s", strings.Join(msgs, "\n"))
			}
		})
	}
}

func TestInvalidFixturesFailChecker(t *testing.T) {
	// Files that should produce checker (semantic) errors.
	checkerFiles := map[string]bool{
		"duplicate_model_name.bp": true,
		"duplicate_endpoint.bp":   true,
		"lowercase_secret.bp":     true,
		"lowercase_type.bp":       true,
		"uppercase_model.bp":      true,
		"unknown_type.bp":         true,
		"unknown_function.bp":     true,
		"output_before_step.bp":   true,
		"wrong_arrow_order.bp":    true,
		"nested_try.bp":           true,
		"deep_nesting.bp":         true,
		"unknown_ref_target.bp":   true,
		"unknown_external.bp":     true,
	}

	files, err := filepath.Glob("../../testdata/invalid/*.bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no invalid fixtures found")
	}

	for _, f := range files {
		name := filepath.Base(f)
		if !checkerFiles[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			file, parseErrors := parser.ParseFile(name, src)
			if len(parseErrors) > 0 {
				t.Skipf("has parser errors (not checker-only): %v", parseErrors)
			}

			checkErrors := Check(file)
			if len(checkErrors) == 0 {
				t.Fatalf("%s: expected checker errors, got none", name)
			}
		})
	}
}

// ═══════════════════════════════════════════════
// Error Formatting (42)
// ═══════════════════════════════════════════════

func TestFormatCheckError(t *testing.T) {
	src := []byte("model Bad {\n  id uuid primary\n}\n")
	err := CheckError{
		Loc:     lexer.Loc{File: "test.bp", Line: 1, Col: 7},
		Message: `model name "Bad" must be snake_case`,
		Hint:    "Use lowercase letters",
	}
	formatted := FormatCheckError(err, src)
	if !strings.Contains(formatted, "model Bad") {
		t.Errorf("expected source line in output, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "snake_case") {
		t.Errorf("expected error message in output, got:\n%s", formatted)
	}
}

// ═══════════════════════════════════════════════
// Scope Tests (43-44)
// ═══════════════════════════════════════════════

func TestScopeDefineAndLookup(t *testing.T) {
	scope := NewScope(nil)
	sym := &Symbol{Name: "foo", Kind: SymModel}
	if existing := scope.Define(sym); existing != nil {
		t.Fatal("expected no existing symbol")
	}
	if found := scope.Lookup("foo"); found == nil {
		t.Fatal("expected to find symbol")
	}
	if found := scope.Lookup("bar"); found != nil {
		t.Fatal("expected nil for undefined symbol")
	}
}

func TestScopeParentLookup(t *testing.T) {
	parent := NewScope(nil)
	parent.Define(&Symbol{Name: "global_var", Kind: SymModel})

	child := NewScope(parent)
	child.Define(&Symbol{Name: "local_var", Kind: SymVariable})

	// Child can see parent symbols
	if found := child.Lookup("global_var"); found == nil {
		t.Fatal("child should find parent symbol")
	}
	// Child can see own symbols
	if found := child.Lookup("local_var"); found == nil {
		t.Fatal("child should find own symbol")
	}
	// Parent can't see child symbols
	if found := parent.Lookup("local_var"); found != nil {
		t.Fatal("parent should not find child symbol")
	}
}

func TestScopeDuplicateDefine(t *testing.T) {
	scope := NewScope(nil)
	sym1 := &Symbol{Name: "foo", Kind: SymModel}
	sym2 := &Symbol{Name: "foo", Kind: SymFn}

	if existing := scope.Define(sym1); existing != nil {
		t.Fatal("first define should succeed")
	}
	if existing := scope.Define(sym2); existing == nil {
		t.Fatal("second define should return existing")
	}
}

// ═══════════════════════════════════════════════
// Naming Helper Unit Tests (45-47)
// ═══════════════════════════════════════════════

func TestIsSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"hello", true},
		{"hello_world", true},
		{"a", true},
		{"a1", true},
		{"my_var_2", true},
		{"", false},
		{"Hello", false},
		{"hello_World", false},
		{"_hello", false},
		{"hello_", false},
		{"hello__world", false},
		{"hello-world", false},
	}
	for _, tt := range tests {
		if got := isSnakeCase(tt.input); got != tt.want {
			t.Errorf("isSnakeCase(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsScreamingSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"HELLO", true},
		{"HELLO_WORLD", true},
		{"A", true},
		{"API_KEY_2", true},
		{"", false},
		{"hello", false},
		{"Hello", false},
		{"HELLO_", false},
		{"_HELLO", false},
		{"HELLO__WORLD", false},
		{"HELLO-WORLD", false},
	}
	for _, tt := range tests {
		if got := isScreamingSnakeCase(tt.input); got != tt.want {
			t.Errorf("isScreamingSnakeCase(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsPascalCase(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Hello", true},
		{"HelloWorld", true},
		{"Plan", true},
		{"ImageFile", true},
		{"Url", true},
		{"", false},
		{"hello", false},
		{"HELLO", false},
		{"hello_world", false},
		{"Hello_World", false},
		{"H", false}, // no lowercase
	}
	for _, tt := range tests {
		if got := isPascalCase(tt.input); got != tt.want {
			t.Errorf("isPascalCase(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ═══════════════════════════════════════════════
// Levenshtein Distance Tests
// ═══════════════════════════════════════════════

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
		{"abc", "abd", 1},
		{"abc", "abcd", 1},
		{"a", "b", 1},
	}
	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSuggestName(t *testing.T) {
	candidates := []string{"user", "item", "order", "product"}

	tests := []struct {
		name string
		want string
	}{
		{"usr", "user"},       // close to "user"
		{"itm", "item"},       // close to "item"
		{"ordr", "order"},     // close to "order"
		{"produc", "product"}, // close to "product"
		{"zzzzzzzzz", ""},     // too far from everything
		{"User", "user"},      // case insensitive
	}
	for _, tt := range tests {
		got := suggestName(tt.name, candidates)
		if got != tt.want {
			t.Errorf("suggestName(%q, ...) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSuggestNameEmptyCandidates(t *testing.T) {
	got := suggestName("foo", nil)
	if got != "" {
		t.Errorf("suggestName with nil candidates = %q, want empty", got)
	}
}

// ═══════════════════════════════════════════════
// "Did you mean?" Integration Tests
// ═══════════════════════════════════════════════

func TestUnknownMiddlewareWithSuggestion(t *testing.T) {
	errs := check(t, header+`
middleware require_auth {
  before {
    |> guard header.Authorization -> 401 "Missing auth"
  }
}
POST /api/test {
  use reqire_auth
  -> 200 "ok"
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `unknown middleware "reqire_auth"`)
	// Check the hint contains a suggestion
	if !strings.Contains(errs[0].Hint, `did you mean "require_auth"`) {
		t.Errorf("expected hint to suggest 'require_auth', got hint: %s", errs[0].Hint)
	}
}

func TestUnknownTypeWithSuggestion(t *testing.T) {
	errs := check(t, headerWithDB+`
enum Status {
  active
  inactive
}
model item {
  id     uuid    primary
  status Statsu  required
}
`)
	expectErrorContaining(t, errs, `unknown type "Statsu"`)
	// Find the error about the unknown type
	for _, e := range errs {
		if strings.Contains(e.Message, `unknown type "Statsu"`) {
			if !strings.Contains(e.Hint, `did you mean "Status"`) {
				t.Errorf("expected hint to suggest 'Status', got hint: %s", e.Hint)
			}
			return
		}
	}
}

func TestUnknownModelRefWithSuggestion(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id uuid primary
}
model post {
  id      uuid primary
  user_id uuid ref(usr)
}
`)
	expectErrorContaining(t, errs, `ref references unknown model "usr"`)
	for _, e := range errs {
		if strings.Contains(e.Message, `ref references unknown model "usr"`) {
			if !strings.Contains(e.Hint, `did you mean "user"`) {
				t.Errorf("expected hint to suggest 'user', got hint: %s", e.Hint)
			}
			return
		}
	}
}

func TestUnknownFunctionCallWithSuggestion(t *testing.T) {
	errs := check(t, header+`
fn process_data {
  <- input string
  -> output string
  impl node { module: "./internal/process-data" }
}

POST /api/test {
  <- input string required
  |> result = proces_data(input)
  -> 200 result
}
`)
	expectErrorContaining(t, errs, `unknown function "proces_data"`)
	for _, e := range errs {
		if strings.Contains(e.Message, `unknown function "proces_data"`) {
			if !strings.Contains(e.Hint, `did you mean "process_data"`) {
				t.Errorf("expected hint to suggest 'process_data', got hint: %s", e.Hint)
			}
			return
		}
	}
}

func TestFunctionCallArity(t *testing.T) {
	fn := `
fn combine_values {
  <- left  string
  <- right string
  -> string
  impl node { module: "./internal/combine-values" }
}
`
	tests := []struct {
		name      string
		call      string
		wantError string
	}{
		{name: "exact", call: `combine_values("a", "b")`},
		{name: "too few", call: `combine_values("a")`, wantError: `function "combine_values" expects 2 arguments, got 1`},
		{name: "too many", call: `combine_values("a", "b", "c")`, wantError: `function "combine_values" expects 2 arguments, got 3`},
		{name: "none", call: `combine_values()`, wantError: `function "combine_values" expects 2 arguments, got 0`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := check(t, header+fn+`
POST /api/test {
  |> result = `+tt.call+`
  -> 200 result
}
`)
			if tt.wantError == "" {
				expectNoErrors(t, errs)
				return
			}
			expectErrors(t, errs, 1)
			expectErrorContaining(t, errs, tt.wantError)
			if !strings.Contains(errs[0].Hint, "combine_values(left, right)") ||
				!strings.Contains(errs[0].Hint, "declared at test.bp:") {
				t.Fatalf("expected arity hint to include signature and declaration, got %q", errs[0].Hint)
			}
		})
	}
}

func TestZeroArgumentFunctionCallArity(t *testing.T) {
	errs := check(t, header+`
fn make_value {
  -> string
  impl node { module: "./internal/make-value" }
}

POST /api/test {
  |> result = make_value()
  -> 200 result
}
`)
	expectNoErrors(t, errs)
}

func TestFunctionOptionalInputStillRequiresAnArgument(t *testing.T) {
	errs := check(t, header+`
fn label_value {
  <- value  string
  <- suffix string optional
  -> string
  impl node { module: "./internal/label-value" }
}

POST /api/test {
  |> result = label_value("x")
  -> 200 result
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `function "label_value" expects 2 arguments, got 1`)
}

func TestDeclaredFunctionCallArgumentTypes(t *testing.T) {
	tests := []struct {
		name      string
		inputType string
		argument  string
		setup     string
		wantError string
	}{
		{
			name:      "primitive mismatch",
			inputType: "int",
			argument:  `"many"`,
			wantError: `argument 1 to function "consume_value" expects int, got string "many"`,
		},
		{
			name:      "numeric widening",
			inputType: "float",
			argument:  `1`,
		},
		{
			name:      "unknown json stays conservative",
			inputType: "int",
			argument:  `payload`,
			setup:     `<- payload json required`,
		},
		{
			name:      "optional to required",
			inputType: "string",
			argument:  `value`,
			setup:     `<- value string optional`,
			wantError: `argument 1 to function "consume_value" expects string, got optional string`,
		},
		{
			name:      "guard narrows optional",
			inputType: "string",
			argument:  `value`,
			setup:     "<- value string optional\n  |> guard value -> 400 \"value required\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := check(t, header+`
fn consume_value {
  <- value `+tt.inputType+`
  -> string
  impl node { module: "./internal/consume-value" }
}

POST /api/test {
  `+tt.setup+`
  |> result = consume_value(`+tt.argument+`)
  -> 200 result
}
`)
			if tt.wantError == "" {
				expectNoErrors(t, errs)
				return
			}
			expectErrors(t, errs, 1)
			expectErrorContaining(t, errs, tt.wantError)
			if !strings.Contains(errs[0].Hint, `Parameter "value" is declared at test.bp:`) {
				t.Fatalf("expected parameter declaration hint, got %q", errs[0].Hint)
			}
		})
	}
}

func TestDeclaredPipeCallArgumentTypeMismatch(t *testing.T) {
	errs := check(t, header+`
pipe require_enabled {
  <- enabled bool
  -> enabled
}

POST /api/test {
  |> result = pipe require_enabled("yes")
  -> 200 result
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `argument 1 to pipe "require_enabled" expects bool, got string "yes"`)
}

func TestTypedInputReassignmentAndCollectionLiterals(t *testing.T) {
	tests := []struct {
		name      string
		typeExpr  string
		value     string
		wantError string
	}{
		{name: "primitive mismatch", typeExpr: "int", value: `"many"`, wantError: `cannot assign string "many" to input "value" of type int`},
		{name: "optional accepts null", typeExpr: "string optional", value: `null`},
		{name: "list accepts elements", typeExpr: "list(bool)", value: `[true, false]`},
		{name: "list rejects element", typeExpr: "list(bool)", value: `[1]`, wantError: `cannot assign list(int) to input "value" of type list(bool)`},
		{name: "map rejects value", typeExpr: "map(string, int)", value: `{ one: "wrong" }`, wantError: `cannot assign map(string, string) to input "value" of type map(string, int)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := check(t, header+`
POST /api/test {
  <- value `+tt.typeExpr+`
  |> value = `+tt.value+`
  -> 200 value
}
`)
			if tt.wantError == "" {
				expectNoErrors(t, errs)
				return
			}
			expectErrors(t, errs, 1)
			expectErrorContaining(t, errs, tt.wantError)
		})
	}
}

func TestNamedEnumAndModelArgumentTypes(t *testing.T) {
	errs := check(t, headerWithDB+`
enum Status {
  active
  disabled
}

model user {
  id uuid primary
}

model team {
  id uuid primary
}

fn set_status {
  <- status Status
  -> string
  impl node { module: "./internal/set-status" }
}

fn handle_user {
  <- account user
  -> string
  impl node { module: "./internal/handle-user" }
}

POST /api/test {
  |> valid = set_status("active")
  |> invalid = set_status("missing")
  |> group = fetch team("team-id")
  |> handled = handle_user(group)
  -> 200 handled
}
`)
	expectErrors(t, errs, 2)
	expectErrorContaining(t, errs, `argument 1 to function "set_status" expects enum Status, got string "missing"`)
	expectErrorContaining(t, errs, `argument 1 to function "handle_user" expects model user, got optional model team`)
}

func TestModelWriteValueTypes(t *testing.T) {
	errs := check(t, headerWithDB+`
model item {
  id       uuid primary
  quantity int
  status   enum(active, disabled)
  note     string optional
  metadata json
}

POST /api/items {
  <- metadata json required
  |> saved = save item {
    quantity: "many"
    status: "missing"
    note: null
    metadata: metadata
  }
  -> 201 saved
}
`)
	expectErrors(t, errs, 2)
	expectErrorContaining(t, errs, `save field "quantity" on model "item" expects int, got string "many"`)
	expectErrorContaining(t, errs, `save field "status" on model "item" expects enum(active, disabled), got string "missing"`)
}

func TestOptionalFetchAndQueryFirstRequirePositiveGuard(t *testing.T) {
	errs := check(t, headerWithDB+`
model user {
  id   uuid primary
  name string required
}

fn require_user {
  <- account user
  -> string
  impl node { module: "./internal/require-user" }
}

fn require_name {
  <- name string
  -> string
  impl node { module: "./internal/require-name" }
}

GET /unguarded {
  |> existing = fetch user("user-id")
  |> first = query user first
  |> model_result = require_user(existing)
  |> field_result = require_name(existing.name)
  |> first_result = require_user(first)
  -> 200 model_result
}

GET /guarded {
  |> existing = fetch user("user-id")
  |> guard existing -> 404 "Not found"
  |> model_result = require_user(existing)
  |> field_result = require_name(existing.name)
  -> 200 field_result
}

GET /negated {
  |> existing = fetch user("user-id")
  |> guard not existing -> 404 "Expected absence"
  |> model_result = require_user(existing)
  -> 200 model_result
}
`)
	expectErrors(t, errs, 4)
	expectErrorContaining(t, errs, `argument 1 to function "require_user" expects model user, got optional model user`)
	expectErrorContaining(t, errs, `argument 1 to function "require_name" expects string, got optional string`)

	modelErrors := 0
	for _, err := range errs {
		if strings.Contains(err.Message, `function "require_user" expects model user, got optional model user`) {
			modelErrors++
		}
	}
	if modelErrors != 3 {
		t.Fatalf("expected optional-model errors for fetch, query first, and negated guard; got %d: %v", modelErrors, errs)
	}
}

func TestTryRecoverDoesNotLeakNarrowingOrReassignmentFacts(t *testing.T) {
	errs := check(t, header+`
fn require_value {
  <- value string
  -> string
  impl node { module: "./internal/require-value" }
}

POST /api/test {
  <- value      string optional
  <- reassigned string optional
  |> try {
    |> guard value -> 400 "value required"
    |> reassigned = "ready"
    |> inside = require_value(value)
  } recover {
    |> recovered = require_value(value)
  }
  |> after_guard = require_value(value)
  |> after_assign = require_value(reassigned)
  -> 200 inside
}
`)
	expectErrors(t, errs, 3)
	expectErrorContaining(t, errs, `function "require_value" expects string, got optional string`)
	for _, err := range errs {
		if strings.Contains(err.Message, `unbound identifier "inside"`) {
			t.Fatalf("new try binding must remain visible after recover: %v", errs)
		}
	}
}

func TestHeterogeneousCompositeLiteralsUseExpectedMemberTypes(t *testing.T) {
	errs := check(t, header+`
fn require_numbers {
  <- values list(int)
  -> string
  impl node { module: "./internal/require-numbers" }
}

fn require_scores {
  <- values map(string, int)
  -> string
  impl node { module: "./internal/require-scores" }
}

POST /api/test {
  <- numbers list(int)
  <- scores map(string, int)
  |> numbers = [1, "wrong"]
  |> scores = { first: 1, second: "wrong" }
  |> list_result = require_numbers([1, "wrong"])
  |> map_result = require_scores({ first: 1, second: "wrong" })
  -> 200 list_result
}
`)
	expectErrors(t, errs, 4)
	expectErrorContaining(t, errs, `input "numbers" of type list(int)`)
	expectErrorContaining(t, errs, `input "scores" of type map(string, int)`)
	expectErrorContaining(t, errs, `argument 1 to function "require_numbers" expects list(int)`)
	expectErrorContaining(t, errs, `argument 1 to function "require_scores" expects map(string, int)`)
}

func TestInlineWhenValidatesTypedInputAssignmentOnly(t *testing.T) {
	errs := check(t, header+`
POST /api/test {
  <- apply       bool required
  <- enabled     bool required
  <- candidate   string optional
  <- destination string required
  <- filters     json required
  |> when apply: enabled = "yes"
  |> when candidate: destination = candidate
  |> when candidate: filters.status = candidate
  -> 200 destination
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `cannot assign string "yes" to input "enabled" of type bool`)
}

func TestPipeCallArity(t *testing.T) {
	pipe := `
pipe normalize_value {
  <- value string
  -> value
}
`
	tests := []struct {
		name      string
		call      string
		wantError string
	}{
		{name: "exact", call: `pipe normalize_value("x")`},
		{name: "too few", call: `pipe normalize_value()`, wantError: `pipe "normalize_value" expects 1 argument, got 0`},
		{name: "too many", call: `pipe normalize_value("x", "y")`, wantError: `pipe "normalize_value" expects 1 argument, got 2`},
		{name: "bare", call: `pipe normalize_value`, wantError: `pipe "normalize_value" expects 1 argument, got 0`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := check(t, header+pipe+`
POST /api/test {
  |> result = `+tt.call+`
  -> 200 result
}
`)
			if tt.wantError == "" {
				expectNoErrors(t, errs)
				return
			}
			expectErrors(t, errs, 1)
			expectErrorContaining(t, errs, tt.wantError)
		})
	}
}

func TestPipeWithoutDeclaredInputUsesImplicitArity(t *testing.T) {
	errs := check(t, header+`
pipe make_value {
  -> "ok"
}

POST /api/test {
  |> result = pipe make_value()
  -> 200 result
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `pipe "make_value" expects 1 argument, got 0`)
}

func TestPipeRejectsMultipleInputsUnsupportedByTargets(t *testing.T) {
	errs := check(t, header+`
pipe combine_values {
  <- left  string
  <- right string
  -> left
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `pipe "combine_values" may declare at most one input, got 2`)
}

func TestCallableNameCannotCollideWithBuiltin(t *testing.T) {
	errs := check(t, header+`
fn clock {
  <- value string
  -> string
  impl node { module: "./internal/clock" }
}
`)
	expectErrors(t, errs, 1)
	expectErrorContaining(t, errs, `function name "clock" is reserved by a built-in`)
}

func TestWebhookAuthRequiresSecretUsing(t *testing.T) {
	errs := check(t, header+`
POST /webhooks/stripe {
  auth webhook_sig
  -> 200 "ok"
}
`)
	expectErrorContaining(t, errs, "auth webhook_sig requires using(secret.NAME)")
}

func TestWebhookAuthRequiresDeclaredSecret(t *testing.T) {
	errs := check(t, header+`
POST /webhooks/stripe {
  auth webhook_sig using(secret.STRIPE_KEY)
  -> 200 "ok"
}
`)
	expectErrorContaining(t, errs, `auth webhook_sig references unknown secret "STRIPE_KEY"`)
}

func TestStorageOperationRequiresStorage(t *testing.T) {
	errs := check(t, header+`
POST /api/upload {
  <- file image/* required
  |> stored = upload(file, "bucket")
  -> 200 stored
}
`)
	expectErrorContaining(t, errs, `storage operation "upload" requires blueprint storage`)
}

func TestNamesOfKind(t *testing.T) {
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: "user", Kind: SymModel})
	scope.Define(&Symbol{Name: "item", Kind: SymModel})
	scope.Define(&Symbol{Name: "process", Kind: SymFn})

	models := scope.NamesOfKind(SymModel)
	if len(models) != 2 {
		t.Fatalf("expected 2 model names, got %d", len(models))
	}

	fns := scope.NamesOfKind(SymFn)
	if len(fns) != 1 {
		t.Fatalf("expected 1 fn name, got %d", len(fns))
	}

	pipes := scope.NamesOfKind(SymPipe)
	if len(pipes) != 0 {
		t.Fatalf("expected 0 pipe names, got %d", len(pipes))
	}
}
