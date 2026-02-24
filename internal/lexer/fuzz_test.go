package lexer

import "testing"

func FuzzTokenize(f *testing.F) {
	// Seed corpus with representative .bp fragments covering key token kinds
	seeds := []string{
		// Blueprint block
		`blueprint "test" { version "1.0" }`,
		// Model declaration
		`model user { id uuid primary }`,
		// HTTP method + path + inputs
		`GET /api/test { <- name string required }`,
		// Secret declaration
		`secret API_KEY required`,
		// String interpolation
		`"hello {name}"`,
		// Pipe step with save
		`|> x = save item { name: name }`,
		// Output arrow
		`-> 200 { id: x.id }`,
		// Guard with negation
		`guard !auth -> 401 "Unauthorized"`,
		// Numeric literals
		`123 45.67 "string" true false`,
		// Function declaration
		`fn compute(x int) int { }`,
		// Input with constraints
		`<- page int default(1) min(1) max(100)`,
		// Comment line
		`# comment line`,
		// Intent marker
		`@ "description"`,
		// Generate marker
		`@> "generate something"`,
		// Durations and sizes
		`500ms 5s 10min 1hour 1day 30days`,
		`512b 10kb 10mb 1gb`,
		// Rates
		`10/min 100/hour 1000/day`,
		// Logical operators
		`and or not in`,
		// Comparison operators
		`== != < > <= >=`,
		// Delimiters
		`{ } [ ] ( ) , : .`,
		// Arithmetic
		`+ - * /`,
		// All HTTP methods
		`GET POST PUT PATCH DELETE STREAM WS`,
		// Field access chain
		`user.name.first`,
		// Keywords
		`model fn pipe middleware secret env include type alias enum test fixture worker schedule external subscribe`,
		// WebSocket lifecycle
		`on_connect on_message on_disconnect on_fail on_error`,
		// Cron keyword
		`cron "0 4 * * 0"`,
		// Mixed assignment
		`|> x = query user`,
		// Negative number
		`-1 -3.14`,
		// Path with params
		`POST /api/users/:id/comments/:comment_id`,
		// Empty string
		`""`,
		// Escaped string
		`"escaped \"quote\""`,
		// Large nested braces
		`{ key: { nested: "value" } }`,
		// Trailing comma
		`["a", "b",]`,
		// Empty input
		``,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// Should not panic on any input — errors are fine, panics are not
		tokens, _ := Tokenize("fuzz.bp", []byte(input))
		_ = tokens
	})
}
