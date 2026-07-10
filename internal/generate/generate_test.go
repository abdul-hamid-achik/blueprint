package generate

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestFindSlots(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantLen int
		checkFn func(slots []Slot) bool
	}{
		{
			name: "no slots",
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
			GET /api/users {
				<- page int default(1)
				|> users = query user
				-> 200 { users: users }
			}`,
			wantLen: 0,
		},
		{
			name: "single slot in endpoint",
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
			GET /api/users {
				<- page int default(1)
				@> "add pagination"
				-> 200 {}
			}`,
			wantLen: 1,
			checkFn: func(slots []Slot) bool {
				return len(slots) > 0 && slots[0].Text == "add pagination" && slots[0].Context == "GET /api/users endpoint"
			},
		},
		{
			name: "slot in pipe",
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
			pipe validate {
				<- input string
				@> "add validation checks"
				-> input
			}`,
			wantLen: 1,
			checkFn: func(slots []Slot) bool {
				return len(slots) > 0 && slots[0].Text == "add validation checks" && slots[0].Context == "pipe validate"
			},
		},
		{
			name: "slot in worker",
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
			worker process {
				trigger "jobs"
				<- job_id string
				@> "add job processing logic"
			}`,
			wantLen: 1,
			checkFn: func(slots []Slot) bool {
				return len(slots) > 0 && slots[0].Text == "add job processing logic" && slots[0].Context == "worker process"
			},
		},
		{
			name: "slot in schedule",
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
			schedule cleanup {
				cron "0 4 * * 0"
				@> "add cleanup logic"
			}`,
			wantLen: 1,
			checkFn: func(slots []Slot) bool {
				return len(slots) > 0 && slots[0].Text == "add cleanup logic" && slots[0].Context == "schedule cleanup"
			},
		},
		{
			name: "slot in middleware before",
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
			middleware auth {
				before {
					<- key string
					@> "add auth logic"
				}
			}`,
			wantLen: 1,
			checkFn: func(slots []Slot) bool {
				return len(slots) > 0 && slots[0].Text == "add auth logic" && slots[0].Context == "middleware auth"
			},
		},
		{
			name: "slot in endpoint with hint",
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
			GET /api/users {
				<- page int default(1)
				@> "add pagination" using(max_results)
				-> 200 {}
			}`,
			wantLen: 1,
			checkFn: func(slots []Slot) bool {
				return len(slots) > 0 &&
					slots[0].Text == "add pagination" &&
					len(slots[0].Hints) == 1 &&
					strings.Contains(slots[0].Hints[0], "using")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, errs := parser.ParseFile("test.bp", []byte(tt.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			slots := FindSlots(file)
			if len(slots) != tt.wantLen {
				t.Errorf("FindSlots() returned %d slots, want %d", len(slots), tt.wantLen)
			}
			if tt.checkFn != nil && !tt.checkFn(slots) {
				t.Errorf("FindSlots() slots don't match expected: %+v", slots)
			}
		})
	}
}

func TestBuildPrompt(t *testing.T) {
	slot := Slot{
		Line:    10,
		Text:    "add rate limiting",
		Hints:   []string{"using(redis)", "max_requests(100)"},
		Context: "POST /api/upload endpoint",
	}

	prompt := buildPrompt(slot)

	// Check key parts of the prompt
	if !strings.Contains(prompt, "Blueprint (.bp) language assistant") {
		t.Error("prompt should mention Blueprint")
	}
	if !strings.Contains(prompt, "add rate limiting") {
		t.Error("prompt should contain the slot text")
	}
	if !strings.Contains(prompt, "POST /api/upload endpoint") {
		t.Error("prompt should contain context")
	}
	if !strings.Contains(prompt, "using(redis)") {
		t.Error("prompt should contain hints")
	}
	if !strings.Contains(prompt, "Output ONLY valid Blueprint arrow statements") {
		t.Error("prompt should contain rules")
	}
}

func TestCollectHints(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
	GET /api/users {
		<- page int default(1)
		@> "test" using(redis)
		-> 200 {}
	}`

	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	slots := FindSlots(file)
	if len(slots) == 0 {
		t.Fatal("no slots found")
	}

	// Should have 1 hint: "using(redis)"
	if len(slots[0].Hints) != 1 {
		t.Errorf("expected 1 hint, got %d: %v", len(slots[0].Hints), slots[0].Hints)
	}
}

// --- Apply ---

func TestApply(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		replacements map[int]string
		want         string
	}{
		{
			name: "multi-slot mid-file replaces only targeted lines",
			src: "blueprint \"x\" { version \"1.0\" }\n" +
				"GET /api/users {\n" +
				"\t@> \"add pagination\"\n" +
				"\t|> keep = untouched()\n" +
				"\t@> \"add auth\"\n" +
				"\t-> 200 {}\n" +
				"}\n",
			replacements: map[int]string{
				3: `|> page = clamp(page, 1, 100)`,
				5: `|> ok = requireAuth()`,
			},
			want: "blueprint \"x\" { version \"1.0\" }\n" +
				"GET /api/users {\n" +
				"\t|> page = clamp(page, 1, 100)\n" +
				"\t|> keep = untouched()\n" +
				"\t|> ok = requireAuth()\n" +
				"\t-> 200 {}\n" +
				"}\n",
		},
		{
			name:         "slot at line 1 (start of file)",
			src:          "@> \"top level todo\"\nblueprint \"x\" { version \"1.0\" }\n",
			replacements: map[int]string{1: `|> noop = true`},
			want:         "|> noop = true\nblueprint \"x\" { version \"1.0\" }\n",
		},
		{
			name:         "slot on final line, no trailing newline",
			src:          "blueprint \"x\" { version \"1.0\" }\n\t@> \"last line\"",
			replacements: map[int]string{2: `|> done = true`},
			want:         "blueprint \"x\" { version \"1.0\" }\n\t|> done = true",
		},
		{
			name: "unicode content in source and replacement is preserved",
			src: "blueprint \"café\" { version \"1.0\" }\n" +
				"\t@> \"emit a café receipt 🧾\"\n" +
				"\t-> 200 {}\n",
			replacements: map[int]string{2: `|> msg = "café ready 🎉"`},
			want: "blueprint \"café\" { version \"1.0\" }\n" +
				"\t|> msg = \"café ready 🎉\"\n" +
				"\t-> 200 {}\n",
		},
		{
			name:         "empty replacements map is a byte-identical no-op",
			src:          "blueprint \"x\" { version \"1.0\" }\n\t@> \"todo\"\n",
			replacements: map[int]string{},
			want:         "blueprint \"x\" { version \"1.0\" }\n\t@> \"todo\"\n",
		},
		{
			name:         "replacement for a line number past EOF is ignored",
			src:          "line one\nline two\nline three\n",
			replacements: map[int]string{99: "unused"},
			want:         "line one\nline two\nline three\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Apply([]byte(tt.src), tt.replacements)
			if string(got) != tt.want {
				t.Errorf("Apply() mismatch\n got:  %q\nwant:  %q", string(got), tt.want)
			}
		})
	}
}

// TestApply_Idempotent verifies that resolving the same replacement map
// against an already-applied source does not duplicate content: the @>
// line is gone after the first Apply, and reapplying the same
// line->text map simply rewrites the same line to the same text.
func TestApply_Idempotent(t *testing.T) {
	src := []byte("blueprint \"x\" { version \"1.0\" }\n\t@> \"add pagination\"\n\t-> 200 {}\n")
	replacements := map[int]string{2: `|> page = clamp(page, 1, 100)`}

	once := Apply(src, replacements)
	twice := Apply(once, replacements)

	if !bytes.Equal(once, twice) {
		t.Fatalf("applying twice changed output:\nonce:  %q\ntwice: %q", once, twice)
	}

	want := "blueprint \"x\" { version \"1.0\" }\n\t|> page = clamp(page, 1, 100)\n\t-> 200 {}\n"
	if string(twice) != want {
		t.Fatalf("got %q, want %q", twice, want)
	}
}

// TestApply_DoesNotMutateInput guards the --write path's safety: Apply must
// return a fresh byte slice and never mutate the caller's src in place, so a
// dry-run (no --write) or an error path that discards the result never
// touches the source the caller still holds a reference to.
func TestApply_DoesNotMutateInput(t *testing.T) {
	src := []byte("blueprint \"x\" {}\nGET /x {\n\t@> \"todo\"\n}\n")
	original := append([]byte(nil), src...)
	replacements := map[int]string{3: `|> result = doSomething()`}

	out := Apply(src, replacements)

	if !bytes.Equal(src, original) {
		t.Fatalf("Apply mutated its input slice: got %q, want %q", src, original)
	}

	// Mutate the returned slice and confirm the input is unaffected, proving
	// the output does not alias the input's backing array.
	if len(out) > 0 {
		out[0] = 'X'
	}
	if !bytes.Equal(src, original) {
		t.Fatalf("mutating Apply's output affected the input slice (aliasing): got %q, want %q", src, original)
	}
}

// --- callAnthropicAPI ---

// withFakeAnthropicServer starts an httptest.Server running handler and
// redirects the package-level anthropicAPIURL to it for the duration of the
// test, restoring the original value on cleanup. No test in this file makes
// a real network call.
func withFakeAnthropicServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	prev := anthropicAPIURL
	anthropicAPIURL = server.URL
	t.Cleanup(func() { anthropicAPIURL = prev })
}

func TestCallAnthropicAPI(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantText    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "200 with text content returns trimmed text",
			status:   http.StatusOK,
			body:     `{"content":[{"type":"text","text":"  |> result = doThing()  "}]}`,
			wantText: "|> result = doThing()",
		},
		{
			name:        "non-200 status is a clear error, no partial result",
			status:      http.StatusInternalServerError,
			body:        `{"error":"boom"}`,
			wantErr:     true,
			errContains: "500",
		},
		{
			name:        "malformed JSON body is a clear error",
			status:      http.StatusOK,
			body:        `not json`,
			wantErr:     true,
			errContains: "decode response",
		},
		{
			name:        "empty content array is a clear error",
			status:      http.StatusOK,
			body:        `{"content":[]}`,
			wantErr:     true,
			errContains: "no text content",
		},
		{
			name:        "content with no text-typed block is a clear error",
			status:      http.StatusOK,
			body:        `{"content":[{"type":"tool_use","text":"ignored"}]}`,
			wantErr:     true,
			errContains: "no text content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFakeAnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				if r.Header.Get("x-api-key") != "test-key" {
					t.Errorf("expected x-api-key header to be set, got %q", r.Header.Get("x-api-key"))
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})

			client := &http.Client{Timeout: 5 * time.Second}
			text, err := callAnthropicAPI("prompt text", "test-key", client)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (text=%q)", text)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if text != tt.wantText {
				t.Errorf("got %q, want %q", text, tt.wantText)
			}
		})
	}
}

// --- GenerateAll ---

func TestGenerateAll_ZeroSlotsIsNoOp(t *testing.T) {
	// No fake server is installed: with 0 slots, GenerateAll must never
	// attempt an HTTP call, and combined with Apply must not rewrite the
	// file at all.
	src := []byte("blueprint \"x\" { version \"1.0\" }\n")
	original := append([]byte(nil), src...)

	replacements, err := GenerateAll(nil, "fake-api-key")
	if err != nil {
		t.Fatalf("GenerateAll with 0 slots returned an error: %v", err)
	}
	if len(replacements) != 0 {
		t.Fatalf("expected no replacements for 0 slots, got %v", replacements)
	}

	out := Apply(src, replacements)
	if !bytes.Equal(out, original) {
		t.Fatalf("Apply rewrote the source with 0 slots: got %q, want %q", out, original)
	}
	if !bytes.Equal(src, original) {
		t.Fatalf("GenerateAll/Apply mutated the caller's src slice")
	}
}

func TestGenerateAll_EmptyAPIKey(t *testing.T) {
	// Must fail fast without ever reaching the network.
	slots := []Slot{{Line: 2, Text: "add x"}}

	replacements, err := GenerateAll(slots, "")
	if err == nil {
		t.Fatal("expected error for empty API key, got nil")
	}
	if replacements != nil {
		t.Fatalf("expected nil replacements on error, got %v", replacements)
	}
}

func TestGenerateAll_Success(t *testing.T) {
	var calls int32
	withFakeAnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"content":[{"type":"text","text":"|> generated_%d = step()"}]}`, n)
	})

	slots := []Slot{
		{Line: 3, Text: "first"},
		{Line: 7, Text: "second"},
	}

	replacements, err := GenerateAll(slots, "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(replacements) != 2 {
		t.Fatalf("expected 2 replacements, got %d: %v", len(replacements), replacements)
	}
	if replacements[3] != "|> generated_1 = step()" {
		t.Errorf("line 3: got %q", replacements[3])
	}
	if replacements[7] != "|> generated_2 = step()" {
		t.Errorf("line 7: got %q", replacements[7])
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected exactly 2 API calls, got %d", calls)
	}
}

// TestGenerateAll_ErrorAbortsWithoutPartialResult verifies that when a later
// slot fails, GenerateAll returns a nil map (not a partially-filled one) and
// a clear error identifying the failing slot. cmdGenerate only calls Apply
// when GenerateAll returns a nil error, so this nil-map/clear-error contract
// is what keeps a failed run from ever touching the .bp source file.
func TestGenerateAll_ErrorAbortsWithoutPartialResult(t *testing.T) {
	var calls int32
	withFakeAnthropicServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"|> ok = step()"}]}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`server exploded`))
	})

	slots := []Slot{
		{Line: 2, Text: "first"},
		{Line: 4, Text: "second"},
	}

	replacements, err := GenerateAll(slots, "test-key")
	if err == nil {
		t.Fatal("expected an error from the failing second slot")
	}
	if replacements != nil {
		t.Fatalf("expected nil replacements on error (no partial result), got %v", replacements)
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error should identify the failing slot's line: %v", err)
	}
	if !strings.Contains(err.Error(), "second") {
		t.Errorf("error should identify the failing slot's text: %v", err)
	}
}
