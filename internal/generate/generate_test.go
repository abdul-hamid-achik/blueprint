package generate

import (
	"strings"
	"testing"

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
