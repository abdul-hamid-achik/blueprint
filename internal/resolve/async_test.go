package resolve_test

import (
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

func TestAsyncFunctionsCollectsFnsAndPipes(t *testing.T) {
	src := `blueprint "t" { version "1.0" port 3000 runtime node }
fn hash_password {
  <- pw string
  -> string
  impl node { module: "./internal/hash", func: "hash" }
}
fn another_fn {
  <- x int
  -> int
  impl node { module: "./internal/x", func: "x" }
}
pipe validate_input {
  <- file image/*
  -> file
}
model user { id uuid primary }
GET /api/users {
  |> users = query user
  -> 200 { users: users }
}
`
	file, errs := parser.ParseFile("t.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	got := resolve.AsyncFunctions(file)

	for _, want := range []string{"hash_password", "another_fn", "validate_input"} {
		if !got[want] {
			t.Errorf("expected %q in async function set, got %v", want, got)
		}
	}
	// Models and endpoints must not be included.
	if got["user"] {
		t.Errorf("models should not be in async function set, got %v", got)
	}
}

func TestAsyncFunctionsHandlesEmptyFile(t *testing.T) {
	if got := resolve.AsyncFunctions(nil); len(got) != 0 {
		t.Errorf("nil file should yield empty map, got %v", got)
	}
}
