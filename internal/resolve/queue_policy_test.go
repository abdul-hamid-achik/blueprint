package resolve_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

func TestResolveQueuePoliciesMatchesNestedEndpointEnqueues(t *testing.T) {
	file := parseQueuePolicyFile(t, `
worker deliver_email {
  trigger queue("emails")
  retry 3 backoff(exponential, base: 1s, max: 30s)
  |> log "deliver"
}

POST /emails {
  <- urgent bool
  |> when urgent {
    |> enqueue "emails" { urgent: urgent }
  }
  |> try {
    |> enqueue "emails" { urgent: false }
  } recover {
    |> log "retry later"
  }
  -> 202 { accepted: true }
}
`)

	facts, err := resolve.ResolveQueuePolicies(file)
	if err != nil {
		t.Fatalf("ResolveQueuePolicies: %v", err)
	}
	if len(facts.Enqueues) != 2 {
		t.Fatalf("enqueue sites=%d, want 2: %+v", len(facts.Enqueues), facts.Enqueues)
	}
	for _, site := range facts.Enqueues {
		if site.Context != resolve.QueueEnqueueEndpoint {
			t.Errorf("context=%q, want endpoint", site.Context)
		}
	}
	policy, ok := facts.ByQueue["emails"]
	if !ok {
		t.Fatalf("missing emails policy: %+v", facts.ByQueue)
	}
	if policy.WorkerName != "deliver_email" || policy.RetryCount != 3 {
		t.Fatalf("policy=%+v", policy)
	}
	if len(policy.Backoff) != 3 {
		t.Fatalf("backoff=%+v, want strategy/base/max", policy.Backoff)
	}
}

func TestResolveQueuePoliciesRejectsMissingWorker(t *testing.T) {
	file := parseQueuePolicyFile(t, `
POST /jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { accepted: true }
}
`)

	facts, err := resolve.ResolveQueuePolicies(file)
	if err == nil {
		t.Fatal("expected missing worker policy error")
	}
	if facts != nil {
		t.Fatalf("facts must be nil on error: %+v", facts)
	}
	for _, want := range []string{`enqueue queue "jobs"`, "no matching worker policy", `trigger queue("jobs")`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestResolveQueuePoliciesRejectsAmbiguousWorkers(t *testing.T) {
	file := parseQueuePolicyFile(t, `
worker alpha {
  trigger queue("jobs")
  |> log "alpha"
}
worker beta {
  trigger queue("jobs")
  |> log "beta"
}
POST /jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { accepted: true }
}
`)

	_, err := resolve.ResolveQueuePolicies(file)
	if err == nil {
		t.Fatal("expected ambiguous worker policy error")
	}
	for _, want := range []string{`enqueue queue "jobs"`, "ambiguous worker policies", "alpha, beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestResolveQueuePoliciesReportsUnsupportedContextFacts(t *testing.T) {
	file := parseQueuePolicyFile(t, `
worker deliver_email {
  trigger queue("emails")
  |> log "deliver"
}
fn queue_email {
  -> bool
  logic {
    |> enqueue "emails" { id: "1" }
    -> true
  }
}
`)

	facts, err := resolve.ResolveQueuePolicies(file)
	if err != nil {
		t.Fatalf("ResolveQueuePolicies: %v", err)
	}
	if len(facts.Enqueues) != 1 {
		t.Fatalf("enqueue sites=%d, want 1", len(facts.Enqueues))
	}
	site := facts.Enqueues[0]
	if site.Context != resolve.QueueEnqueueFunction || site.Owner != "queue_email" || site.Section != "logic" {
		t.Fatalf("site=%+v", site)
	}
}

func TestResolveWorkerQueuePolicyValidatesTriggerShape(t *testing.T) {
	tests := []struct {
		name    string
		worker  *ast.Worker
		want    string
		wantErr string
	}{
		{"default", &ast.Worker{Name: "deliver_email"}, "deliver_email", ""},
		{"direct string", workerWithTrigger(&ast.StringLit{Value: "jobs.v1"}), "jobs.v1", ""},
		{"direct ident", workerWithTrigger(&ast.Ident{Name: "jobs"}), "jobs", ""},
		{"queue call", workerWithTrigger(&ast.FnCall{Name: "queue", Args: []ast.Expr{&ast.StringLit{Value: "jobs"}}}), "jobs", ""},
		{"empty", workerWithTrigger(&ast.StringLit{}), "", "non-empty static"},
		{"dynamic call", workerWithTrigger(&ast.FnCall{Name: "lookup", Args: []ast.Expr{&ast.StringLit{Value: "jobs"}}}), "", "trigger must be"},
		{"queue arity", workerWithTrigger(&ast.FnCall{Name: "queue", Args: []ast.Expr{&ast.StringLit{Value: "jobs"}, &ast.StringLit{Value: "extra"}}}), "", "2 arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := resolve.ResolveWorkerQueuePolicy(tt.worker)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error=%v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || policy.Queue != tt.want {
				t.Fatalf("policy=%+v err=%v, want queue %q", policy, err, tt.want)
			}
		})
	}

	duplicate := workerWithTrigger(&ast.StringLit{Value: "one"})
	duplicate.Meta = append(duplicate.Meta, &ast.WorkerMeta{Kind: "trigger", Value: &ast.StringLit{Value: "two"}})
	if _, err := resolve.ResolveWorkerQueuePolicy(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate trigger metadata") {
		t.Fatalf("duplicate trigger error=%v", err)
	}
}

func TestResolveQueuePoliciesRejectsMalformedEnqueueArity(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("args_%d", count), func(t *testing.T) {
			args := []ast.Expr{&ast.StringLit{Value: "jobs"}, &ast.BlockExpr{}}
			if count < len(args) {
				args = args[:count]
			} else if count > len(args) {
				args = append(args, &ast.StringLit{Value: "extra"})
			}
			file := &ast.File{Blocks: []ast.TopLevel{
				workerWithTrigger(&ast.StringLit{Value: "jobs"}),
				&ast.Endpoint{Method: "POST", Path: "/jobs", Stmts: []ast.ArrowStmt{&ast.StepStmt{Expr: &ast.FnCall{Name: "enqueue", Args: args}}}},
			}}
			if _, err := resolve.ResolveQueuePolicies(file); err == nil || !strings.Contains(err.Error(), "requires exactly a static queue name and payload") {
				t.Fatalf("arity %d error=%v", count, err)
			}
		})
	}
}

func workerWithTrigger(value ast.Expr) *ast.Worker {
	return &ast.Worker{Name: "deliver_email", Meta: []*ast.WorkerMeta{{Kind: "trigger", Value: value}}}
}

func parseQueuePolicyFile(t *testing.T, body string) *ast.File {
	t.Helper()
	src := `blueprint "queue-policy" {
  version "1.0.0"
  port 3000
  runtime node
  queue redis
}
secret REDIS_URL required
` + body
	file, errs := parser.ParseFile("queue_policy.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return file
}
