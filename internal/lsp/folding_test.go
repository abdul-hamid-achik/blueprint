package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

// nestedBlueprint has nested blocks (test request/body/expect, try/recover)
// and braces inside a string and a comment that must not create folds.
const nestedBlueprint = `blueprint "fold" {
  version "0.1.0"
  port 3000
  runtime node
}
model user {
  id uuid primary
  email string required
}
# a comment with a { brace in it
test login_flow {
  target POST /login
  request {
    body {
      email: "a@b.c"
    }
  }
  expect {
    status 200
  }
}
pipe render {
  <- value string
  |> try {
    |> log "{ not a block }"
  } recover {
    |> log "failed"
  }
  -> value
}
`

// expectedFoldBlocks is the number of multi-line brace blocks in
// nestedBlueprint: blueprint, model, test, request, body, expect, pipe, try,
// and recover.
const expectedFoldBlocks = 9

func TestFoldingRanges_OneRangePerMultiLineBlock(t *testing.T) {
	idx := buildIndex("file:///tmp/fold.bp", nestedBlueprint)
	if len(idx.errors) != 0 {
		t.Fatalf("fixture must parse cleanly: %+v", idx.errors)
	}
	ranges := foldingRanges(idx.file.SourceTokens)
	if len(ranges) != expectedFoldBlocks {
		t.Fatalf("got %d ranges, want %d: %+v", len(ranges), expectedFoldBlocks, ranges)
	}

	lines := strings.Split(nestedBlueprint, "\n")
	for _, r := range ranges {
		if !strings.HasSuffix(strings.TrimSpace(lines[r.StartLine]), "{") {
			t.Errorf("fold %+v does not start on a line ending in '{': %q", r, lines[r.StartLine])
		}
		closing := strings.TrimSpace(lines[r.EndLine+1])
		if !strings.HasPrefix(closing, "}") {
			t.Errorf("fold %+v does not stop before a closing brace: %q", r, closing)
		}
	}

	// The try block folds up to the `} recover {` line, and recover starts there.
	tryStart := lineIndexOf(lines, "|> try {")
	recoverStart := lineIndexOf(lines, "} recover {")
	foundTry, foundRecover := false, false
	for _, r := range ranges {
		if r.StartLine == tryStart && r.EndLine == recoverStart-1 {
			foundTry = true
		}
		if r.StartLine == recoverStart {
			foundRecover = true
		}
	}
	if !foundTry || !foundRecover {
		t.Fatalf("try/recover folds missing (try=%v recover=%v): %+v", foundTry, foundRecover, ranges)
	}
}

func TestFoldingRanges_SingleLineBlocksAreSkipped(t *testing.T) {
	text := "model a { id uuid primary }\nenum b { x y }\n"
	idx := buildIndex("file:///tmp/one-line.bp", text)
	if got := foldingRanges(idx.file.SourceTokens); len(got) != 0 {
		t.Fatalf("single-line blocks must not fold, got %+v", got)
	}
}

func TestServer_FoldingRangeRoundTrip(t *testing.T) {
	const uri = "file:///tmp/fold-roundtrip.bp"
	resp := requestResponse(t, uri, nestedBlueprint, "textDocument/foldingRange", nil)
	var ranges []lspFoldingRange
	if err := json.Unmarshal(resp.Result, &ranges); err != nil {
		t.Fatalf("decode ranges: %v (raw=%s)", err, resp.Result)
	}
	if len(ranges) != expectedFoldBlocks {
		t.Fatalf("got %d ranges over the wire, want %d", len(ranges), expectedFoldBlocks)
	}
}

func TestServer_FoldingRangeUnknownDocumentReturnsEmptyArray(t *testing.T) {
	resp := requestUnopened(t, "file:///tmp/never-opened-fold.bp", "textDocument/foldingRange")
	if got := string(resp.Result); got != "[]" {
		t.Fatalf("unknown document must return [], got %s", got)
	}
}
