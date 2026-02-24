package parser

import "testing"

func FuzzParseFile(f *testing.F) {
	// Seed corpus with representative .bp programs of varying complexity
	seeds := []string{
		// Minimal valid blueprint
		`blueprint "test" { version "1.0" port 3000 runtime node }`,
		// Blueprint with model and endpoint
		`blueprint "test" { version "1.0" port 3000 runtime node database postgres }
model item { id uuid primary name string required }
GET /api/items { <- page int |> items = query item -> 200 { items: items } }`,
		// Blueprint with secret and POST
		`blueprint "x" { version "1.0" }
secret KEY required
POST /api/x { <- name string required |> x = save item { name: name } -> 201 { id: x.id } }`,
		// Bare minimum
		`blueprint "t" { version "1" }`,
		// Garbage — should not panic
		`{}{}{}`,
		// Empty input
		``,
		// Keyword alone
		`blueprint`,
		// Model with all constraint types
		`blueprint "t" { version "1" }
model user {
  id       uuid      primary
  email    string    required unique
  name     string    required
  age      int       default(0) min(0) max(200)
  created  timestamp default(now)
}`,
		// Multiple endpoints
		`blueprint "t" { version "1" runtime node }
GET /api/health { -> 200 { status: "ok" } }
POST /api/items { <- name string required |> item = save item { name: name } -> 201 { id: item.id } }
DELETE /api/items/:id { <- id uuid required |> delete item id -> 204 }`,
		// Function declaration
		`blueprint "t" { version "1" }
fn compute(x int, y int) int { }`,
		// Pipe declaration
		`blueprint "t" { version "1" }
pipe transform { <- input string |> result = call format(input) -> result }`,
		// Middleware
		`blueprint "t" { version "1" }
middleware auth { |> guard !token -> 401 "Unauthorized" }`,
		// Guard with when
		`blueprint "t" { version "1" }
GET /api/x { <- role string |> when role == "admin" { -> 200 { admin: true } } -> 200 { admin: false } }`,
		// Try/recover
		`blueprint "t" { version "1" }
POST /api/x { <- name string |> try { |> x = save item { name: name } } recover { -> 500 "Failed" } -> 201 { id: x.id } }`,
		// Secret and env
		`secret API_KEY required
secret DB_URL required
env MAX_SIZE 10mb
env DEBUG false`,
		// Intent and generate
		`@ "A description"
blueprint "t" { version "1" }
@> "generate an endpoint"`,
		// STREAM endpoint
		`blueprint "t" { version "1" runtime node }
STREAM /api/events { -> 200 { event: "tick" } }`,
		// WS endpoint
		`blueprint "t" { version "1" runtime node }
WS /api/ws { on_connect { -> { type: "connected" } } on_message { <- data string -> { echo: data } } }`,
		// Schedule
		`blueprint "t" { version "1" }
schedule daily_cleanup { cron "0 4 * * *" |> delete item where(created < 90.days.ago) }`,
		// External
		`blueprint "t" { version "1" }
external stripe { base "https://api.stripe.com" }`,
		// Test block
		`blueprint "t" { version "1" }
test "create item" { POST /api/items { name: "test" } -> 201 }`,
		// Deeply invalid — should not panic
		`{{{{{`,
		`-> -> -> <-`,
		`model { model { model`,
		`"unterminated string`,
		`GET /api/x { <- <- <- }`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// Should not panic — parse errors are fine, panics are not
		_, _ = ParseFile("fuzz.bp", []byte(input))
	})
}
