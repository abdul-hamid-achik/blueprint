package diag_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/diag"
)

func TestLookupFindsKnownCode(t *testing.T) {
	body, ok := diag.Lookup("C001")
	if !ok {
		t.Fatal("expected C001 to be documented")
	}
	if !strings.HasPrefix(body, "### C001") {
		t.Errorf("expected body to start with heading, got: %q", body)
	}
	if !strings.Contains(body, "missing blueprint block") {
		t.Errorf("expected body to contain title text, got: %q", body)
	}
}

func TestLookupNormalizesCase(t *testing.T) {
	if _, ok := diag.Lookup("c001"); !ok {
		t.Errorf("lowercase lookup should hit C001")
	}
	if _, ok := diag.Lookup("  C001  "); !ok {
		t.Errorf("whitespace-padded lookup should hit C001")
	}
}

func TestLookupUnknownCodeReturnsFalse(t *testing.T) {
	if body, ok := diag.Lookup("C999"); ok {
		t.Errorf("unknown code must return false, got %q", body)
	}
	if _, ok := diag.Lookup(""); ok {
		t.Errorf("empty code must return false")
	}
}

func TestLookupStopsAtNextHeading(t *testing.T) {
	body, ok := diag.Lookup("C001")
	if !ok {
		t.Fatal("expected C001 to be documented")
	}
	// The returned section MUST stop before C002 — otherwise grow-coverage
	// would silently bleed unrelated content into every lookup.
	if strings.Contains(body, "C002") {
		t.Errorf("C001 body bled into C002 section:\n%s", body)
	}
}

// TestErrorCodesDocInSync is the drift guard: internal/diag/error-codes.md is
// the canonical copy `bp explain` reads from; docs/error-codes.md is what the
// VitePress site serves. They must stay byte-identical so the in-binary help
// and the website never disagree. If this test fails, update both files (or
// regenerate one from the other) in the same commit.
func TestErrorCodesDocInSync(t *testing.T) {
	embedded, err := os.ReadFile("error-codes.md")
	if err != nil {
		t.Fatalf("read internal/diag/error-codes.md: %v", err)
	}
	siteCopy, err := os.ReadFile("../../docs/error-codes.md")
	if err != nil {
		t.Fatalf("read docs/error-codes.md: %v", err)
	}
	if !bytes.Equal(embedded, siteCopy) {
		t.Fatal("internal/diag/error-codes.md and docs/error-codes.md are out of sync.\n" +
			"These files must match — `bp explain` reads the embedded copy and the website serves the docs/ copy.\n" +
			"Update both, or regenerate one from the other.")
	}
}
