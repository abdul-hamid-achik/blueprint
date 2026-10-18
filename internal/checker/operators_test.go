package checker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// operatorModels declares the typed operands the operator tests draw on. The
// field names are chosen so that where(...) predicates can mix columns with
// same-named arrow inputs (`name`, `plan`) without ambiguity.
const operatorModels = `
enum Plan {
  free
  pro
}

enum Color {
  red
  blue
}

model thing {
  id      int       primary
  uid     uuid      required
  name    string    required
  price   money     required
  qty     int       required
  ratio   float     required
  active  bool      required
  plan    Plan      required
  note    string
  created timestamp required
  tags    json
}
`

// withOperatorModels prepends the shared models and a database block to one
// endpoint body.
func withOperatorModels(body string) string {
	return headerWithDB + operatorModels + body
}

// operatorErrors returns only the C050 diagnostics, so tests can assert the
// new rule fired exactly as often as expected without depending on other
// checker messages.
func operatorErrors(errs []CheckError) []CheckError {
	var out []CheckError
	for _, e := range errs {
		if e.Code == CodeOperatorTypeMismatch {
			out = append(out, e)
		}
	}
	return out
}

func TestOperatorMismatchReportsIncompatibleOperands(t *testing.T) {
	tests := map[string]struct {
		body string
	}{
		"string ordered against int literal": {
			body: `GET /p { -> 200 { x: "abc" > 3 } }`,
		},
		"string ordered against int input": {
			body: `GET /p {
  <- name string required
  <- n int required
  |> guard name < n -> 400 "bad"
  -> 200 "ok"
}`,
		},
		"string equal to int input": {
			body: `GET /p {
  <- name string required
  <- n int required
  |> guard name == n -> 400 "bad"
  -> 200 "ok"
}`,
		},
		"bool equal to string literal": {
			body: `GET /p {
  <- flag bool required
  |> guard flag == "yes" -> 400 "bad"
  -> 200 "ok"
}`,
		},
		"bool plus int": {
			body: `GET /p {
  <- flag bool required
  <- n int required
  |> total = flag + n
  -> 200 total
}`,
		},
		"int plus string": {
			body: `GET /p {
  <- n int required
  <- s string required
  |> total = n + s
  -> 200 total
}`,
		},
		"int minus string literal": {
			body: `GET /p {
  <- n int required
  |> guard n - "x" -> 400 "bad"
  -> 200 "ok"
}`,
		},
		"multiply string input": {
			body: `GET /p {
  <- s string required
  |> total = s * 2
  -> 200 total
}`,
		},
		"bool ordered against bool": {
			body: `GET /p {
  <- flag bool required
  |> guard flag < true -> 400 "bad"
  -> 200 "ok"
}`,
		},
		"negate string": {
			body: `GET /p {
  <- s string required
  |> guard -s == 3 -> 400 "bad"
  -> 200 "ok"
}`,
		},
		"named enums of different declarations": {
			body: `GET /p {
  <- p Plan required
  <- c Color required
  |> guard p == c -> 400 "bad"
  -> 200 "ok"
}`,
		},
		"enum equal to int": {
			body: `GET /p {
  <- p Plan required
  |> guard p == 3 -> 400 "bad"
  -> 200 "ok"
}`,
		},
		"step assignment mixing bool and int": {
			body: `GET /p {
  <- flag bool required
  <- n int required
  |> total = flag * n
  -> 200 total
}`,
		},
		"output arithmetic on bool": {
			body: `GET /p {
  <- flag bool required
  -> 200 { total: flag - 1 }
}`,
		},
		"when condition comparing bool to string": {
			body: `GET /p {
  <- flag bool required
  <- total int required
  |> when flag == "yes": total = 1
  -> 200 "ok"
}`,
		},
		"where money column against string input": {
			body: `GET /p {
  <- s string required
  |> rows = query thing where(price == s)
  -> 200 rows
}`,
		},
		"where string column against int input": {
			body: `GET /p {
  <- n int required
  |> rows = query thing where(name > n)
  -> 200 rows
}`,
		},
		"where string column arithmetic": {
			body: `GET /p {
  |> rows = query thing where(name * 2 > 3)
  -> 200 rows
}`,
		},
		"where bool column against string input": {
			body: `GET /p {
  <- s string required
  |> rows = query thing where(active == s)
  -> 200 rows
}`,
		},
		"where string column in int list": {
			body: `GET /p {
  |> rows = query thing where(name in [1, 2])
  -> 200 rows
}`,
		},
		"where parenthesized field keeps its column type": {
			body: `GET /p {
  |> rows = query thing where((name) > 3)
  -> 200 rows
}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			errs := check(t, withOperatorModels(tt.body))
			found := operatorErrors(errs)
			if len(found) != 1 {
				t.Fatalf("expected exactly one C050, got %d: %v", len(found), errs)
			}
			if len(errs) != 1 {
				t.Fatalf("expected no other diagnostics, got: %v", errs)
			}
			if !strings.HasPrefix(found[0].Message, "operator ") {
				t.Errorf("message should name the operator, got %q", found[0].Message)
			}
		})
	}
}

func TestOperatorMismatchAcceptsCompatibleOperands(t *testing.T) {
	tests := map[string]string{
		"numeric family mixes freely": `GET /p {
  <- n int required
  <- m money required
  <- f float required
  |> same = n == m
  |> close = f < n
  |> sum = n + f
  |> diff = m - n
  |> ratio = f / n
  -> 200 { same: same, close: close, sum: sum, diff: diff, ratio: ratio }
}`,
		"string concatenation": `GET /p {
  <- s string required
  |> text = s + "suffix"
  -> 200 text
}`,
		"string and uuid equality": `GET /p {
  <- id uuid required
  |> guard id == "3f2f" -> 400 "bad"
  -> 200 "ok"
}`,
		"enum against string literal and list": `GET /p {
  <- p Plan required
  |> guard p == "pro" -> 400 "bad"
  |> guard p in ["free", "pro"] -> 400 "bad"
  -> 200 "ok"
}`,
		"enum against string input": `GET /p {
  <- p Plan required
  <- s string required
  |> guard p == s -> 400 "bad"
  -> 200 "ok"
}`,
		"enum against enum member access": `GET /p {
  <- p Plan required
  |> guard p == Plan.pro -> 400 "bad"
  -> 200 "ok"
}`,
		"same enum from two inputs": `GET /p {
  <- a Plan required
  <- b Plan required
  |> guard a == b -> 400 "bad"
  -> 200 "ok"
}`,
		"bool against bool": `GET /p {
  <- flag bool required
  |> guard flag == true -> 400 "bad"
  -> 200 "ok"
}`,
		"timestamp against string and now": `GET /p {
  <- ts timestamp required
  |> guard ts > "2024-01-01" -> 400 "bad"
  |> guard ts > now -> 400 "bad"
  -> 200 "ok"
}`,
		"duration literal against timestamp": `GET /p {
  <- ts timestamp required
  |> guard ts < now - 1d -> 400 "bad"
  -> 200 "ok"
}`,
		"null comparisons": `GET /p {
  <- n int optional
  |> guard n != null -> 400 "bad"
  |> guard n == null -> 400 "bad"
  -> 200 "ok"
}`,
		"optional input compared with its type": `GET /p {
  <- n int optional
  |> guard n > 3 -> 400 "bad"
  -> 200 "ok"
}`,
		"boolean operators are not checked here": `GET /p {
  <- n int required
  <- s string required
  |> guard n > 0 and s == "x" -> 400 "bad"
  -> 200 "ok"
}`,
		"json field is unknown": `GET /p {
  <- payload json required
  |> guard payload.count > "x" -> 400 "bad"
  -> 200 "ok"
}`,
		"json column in where": `GET /p {
  |> rows = query thing where(tags == 3)
  -> 200 rows
}`,
		"model value is not a scalar": `GET /p {
  <- id int required
  |> row = fetch thing(id)
  |> guard row == 3 -> 400 "bad"
  -> 200 "ok"
}`,
		"where with matching column types": `GET /p {
  <- n int required
  |> rows = query thing where(qty >= n, price > 0, plan == "pro", id in [1, 2])
  |> rows2 = query thing where(name == "x", uid == "y", note != null)
  |> rows3 = query thing where(created > now, price * 2 > 10, (ratio) < 1.5)
  -> 200 { rows: rows, rows2: rows2, rows3: rows3 }
}`,
		"where bare name does not read a same-named input": `GET /p {
  <- qty string required
  |> rows = query thing where(qty > 3)
  -> 200 rows
}`,
		"negative literal": `GET /p {
  <- n int required
  |> guard n > -5 -> 400 "bad"
  -> 200 "ok"
}`,
		"scheduled duration query": `GET /p {
  |> rows = query thing where(created < 90.days.ago)
  -> 200 rows
}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			expectNoErrors(t, check(t, withOperatorModels(body)))
		})
	}
}

func TestOperatorMismatchInComputedFieldsUsesC050(t *testing.T) {
	source := headerWithDB + `
model product {
  id    int    primary
  name  string required
  price int    required
  computed bad bool = name > price
}
GET /p { |> rows = query product -> 200 rows }`
	errs := check(t, source)
	found := operatorErrors(errs)
	if len(found) != 1 {
		t.Fatalf("expected one C050 from the computed field, got %d: %v", len(found), errs)
	}
	if !strings.Contains(found[0].Message, "computed") {
		t.Errorf("expected the existing computed-field message, got %q", found[0].Message)
	}
}

func TestOperatorMismatchDeduplicatesRepeatedVisits(t *testing.T) {
	errs := check(t, withOperatorModels(`GET /p {
  <- s string required
  <- n int required
  |> guard s == n -> 400 "bad"
  -> 200 "ok"
}`))
	if got := len(operatorErrors(errs)); got != 1 {
		t.Fatalf("expected a single C050 for one comparison, got %d: %v", got, errs)
	}
}

// TestOperatorFixturesAndExamplesStayClean is the zero-false-positive gate:
// every shipped example and valid fixture must still check with no errors.
func TestOperatorFixturesAndExamplesStayClean(t *testing.T) {
	var files []string
	for _, pattern := range []string{"../../examples/*.bp", "../../testdata/valid/*.bp"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no examples or valid fixtures found")
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
				t.Fatalf("parse errors: %v", parseErrors)
			}
			if errs := Check(file); len(errs) > 0 {
				t.Fatalf("expected no checker errors, got: %v", errs)
			}
		})
	}
}
