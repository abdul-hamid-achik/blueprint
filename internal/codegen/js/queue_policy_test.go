package js

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestQueuePolicyGenerationRejectsMissingAndAmbiguousWorkers(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "missing",
			body: `POST /jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { accepted: true }
}`,
			want: []string{`enqueue queue "jobs"`, "no matching worker policy"},
		},
		{
			name: "ambiguous",
			body: `worker alpha {
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
}`,
			want: []string{`enqueue queue "jobs"`, "ambiguous worker policies", "alpha, beta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := parseQueueCodegenFile(t, tt.body)
			files, err := New().Files(file)
			if err == nil {
				t.Fatal("expected queue policy error")
			}
			if len(files) != 0 {
				t.Fatalf("fail-closed generation returned %d files", len(files))
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
		})
	}
}

func TestQueuePolicyGenerationOmitsBackoffWithoutRetries(t *testing.T) {
	file := parseQueueCodegenFile(t, `worker process_job {
  trigger queue("jobs")
  retry 0 backoff(exponential, base: 1s)
  |> log "process"
}
POST /jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { accepted: true }
}`)

	files, err := New().Files(file)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	worker := outputContent(t, files, "src/workers/process-job.ts")
	if !strings.Contains(worker, `export const processJobJobOptions = { attempts: 1 };`) {
		t.Fatalf("worker options must use one attempt without backoff:\n%s", worker)
	}
	if !strings.Contains(worker, `export const processJobBackoff = null;`) {
		t.Fatalf("backoff metadata must be null when retry count is zero:\n%s", worker)
	}
	if !strings.Contains(worker, `export const processJobBackoffStrategy = null;`) {
		t.Fatalf("custom strategy must be omitted when retry count is zero:\n%s", worker)
	}
	optionsLine := lineContaining(worker, "processJobJobOptions")
	if strings.Contains(optionsLine, "backoff") {
		t.Fatalf("backoff must be omitted without retries: %s", optionsLine)
	}

	route := outputContent(t, files, "src/routes/jobs.ts")
	queueID := workerProducerQueueIdentifier("process_job")
	optionsID := workerJobOptionsIdentifier("process_job")
	for _, want := range []string{
		`import { processJobJobOptions as ` + optionsID + ` } from '../workers/process-job.js';`,
		`await ` + queueID + `.add('job', { id: "1" }, ` + optionsID + `);`,
	} {
		if !strings.Contains(route, want) {
			t.Errorf("route missing %q:\n%s", want, route)
		}
	}
}

func TestQueuePolicyGenerationRejectsUnsupportedEnqueueContexts(t *testing.T) {
	baseWorker := matchingQueueWorker()
	tests := []struct {
		name    string
		context string
		block   ast.TopLevel
	}{
		{"function", "function", &ast.Fn{Name: "producer", Logic: &ast.LogicBlock{Stmts: enqueueStatements()}}},
		{"pipe", "pipe", &ast.Pipe{Name: "producer", Stmts: enqueueStatements()}},
		{"middleware", "middleware", &ast.Middleware{Name: "producer", Before: enqueueStatements()}},
		{"stream", "stream", &ast.StreamEndpoint{Path: "/events", Stmts: enqueueStatements()}},
		{"websocket", "websocket", &ast.WsEndpoint{Path: "/socket", OnMessage: enqueueStatements()}},
		{"worker", "worker", &ast.Worker{Name: "producer", Stmts: enqueueStatements()}},
		{"schedule", "schedule", &ast.Schedule{Name: "producer", Stmts: enqueueStatements()}},
		{"subscription", "subscription", &ast.Subscribe{Event: "job.created", Stmts: enqueueStatements()}},
		{"test", "test", &ast.Test{Name: "producer", Setup: enqueueStatements()}},
		{"test_group", "test_group", &ast.TestGroup{Name: "producer", SharedSetup: enqueueStatements()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &ast.File{Blocks: []ast.TopLevel{baseWorker, tt.block}}
			files, err := New().Files(file)
			if err == nil {
				t.Fatal("expected unsupported enqueue context error")
			}
			if len(files) != 0 {
				t.Fatalf("fail-closed generation returned %d files", len(files))
			}
			for _, want := range []string{"supports enqueue only in HTTP endpoint bodies", tt.context, `queue "jobs"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
		})
	}
}

func TestQueuePolicyGenerationRejectsUnsupportedBackoffConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		backoff string
		want    string
	}{
		{"strategy", "backoff(linear, base: 1s)", `strategy "linear" is unsupported`},
		{"option", "backoff(exponential, jitter: 1s)", `option "jitter" is unsupported`},
		{"delay", "backoff(exponential, base: dynamic_delay)", "base must be a non-negative integer millisecond value or duration"},
		{"missing delay", "backoff(exponential)", "backoff requires base or delay"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := parseQueueCodegenFile(t, `worker process_job {
  trigger queue("jobs")
  retry 2 `+tt.backoff+`
  |> log "process"
}
POST /jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { accepted: true }
}`)
			files, err := New().Files(file)
			if err == nil {
				t.Fatal("expected unsupported backoff error")
			}
			if len(files) != 0 {
				t.Fatalf("fail-closed generation returned %d files", len(files))
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error missing %q: %v", tt.want, err)
			}
		})
	}
}

func TestQueuePolicyGenerationPlainRetryIsImmediate(t *testing.T) {
	file := parseQueueCodegenFile(t, `worker process_job {
  trigger queue("jobs")
  retry 2
  |> log "process"
}
POST /jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { accepted: true }
}`)

	files, err := New().Files(file)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	worker := outputContent(t, files, "src/workers/process-job.ts")
	if !strings.Contains(worker, `export const processJobJobOptions = { attempts: 3 };`) {
		t.Fatalf("plain retry must use immediate attempts without backoff:\n%s", worker)
	}
}

func TestQueuePolicyGenerationUsesSafeWorkerDerivedProducerIdentifiers(t *testing.T) {
	file := parseQueueCodegenFile(t, `worker dotted_worker {
  trigger queue("jobs.v1")
  |> log "dotted"
}
worker dashed_worker {
  trigger queue("jobs-v1")
  |> log "dashed"
}
worker quoted_worker {
  trigger queue("jobs.\"priority\"")
  |> log "quoted"
}
POST /jobs {
  |> enqueue "jobs.v1" { id: "1" }
  |> enqueue "jobs-v1" { id: "2" }
  |> enqueue "jobs.\"priority\"" { id: "3" }
  -> 202 { accepted: true }
}`)

	files, err := New().Files(file)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	route := outputContent(t, files, "src/routes/jobs.ts")
	workers := map[string]string{
		"dotted_worker": `"jobs.v1"`,
		"dashed_worker": `"jobs-v1"`,
		"quoted_worker": `"jobs.\"priority\""`,
	}
	seen := make(map[string]bool)
	for worker, queueLiteral := range workers {
		queueID := workerProducerQueueIdentifier(worker)
		optionsID := workerJobOptionsIdentifier(worker)
		if seen[queueID] || seen[optionsID] {
			t.Fatalf("worker-derived identifier collision for %q", worker)
		}
		seen[queueID], seen[optionsID] = true, true
		for _, want := range []string{
			`const ` + queueID + ` = new Queue(` + queueLiteral,
			` as ` + optionsID + ` } from '../workers/`,
			`await ` + queueID + `.add('job',`,
		} {
			if !strings.Contains(route, want) {
				t.Errorf("route missing %q:\n%s", want, route)
			}
		}
	}
}

func TestWorkerDurationUnitsApplyToTimeoutAndBackoff(t *testing.T) {
	units := map[string]int{
		"ms":    1,
		"s":     1000,
		"min":   60 * 1000,
		"h":     60 * 60 * 1000,
		"hour":  60 * 60 * 1000,
		"hours": 60 * 60 * 1000,
		"d":     24 * 60 * 60 * 1000,
		"day":   24 * 60 * 60 * 1000,
		"days":  24 * 60 * 60 * 1000,
	}
	for unit, multiplier := range units {
		t.Run(unit, func(t *testing.T) {
			literal := "2" + unit
			worker := &ast.Worker{Name: "consumer", Meta: []*ast.WorkerMeta{
				{Kind: "timeout", Value: &ast.DurationLit{Value: literal}},
				{Kind: "retry", Value: &ast.IntLit{Value: "1"}, Extra: []ast.KVPair{{Key: "base", Value: &ast.DurationLit{Value: literal}}}},
			}}
			if err := validateWorkerDeliveryPolicy(worker); err != nil {
				t.Fatalf("validateWorkerDeliveryPolicy: %v", err)
			}
			if got, want := workerTimeoutMS(worker), 2*multiplier; got != want {
				t.Errorf("timeout=%d, want %d", got, want)
			}
			wantDelay := "delay: " + durationToMS(literal)
			if got := workerJobOptionsExpr(worker); !strings.Contains(got, wantDelay) {
				t.Errorf("job options %q missing %q", got, wantDelay)
			}
		})
	}
}

func TestQueuePolicyGenerationRejectsMalformedWorkerTriggers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ast.Worker)
		want   string
	}{
		{"duplicate", func(worker *ast.Worker) {
			worker.Meta = append(worker.Meta, &ast.WorkerMeta{Kind: "trigger", Value: &ast.StringLit{Value: "other"}})
		}, "duplicate trigger metadata"},
		{"dynamic", func(worker *ast.Worker) {
			worker.Meta[0].Value = &ast.FnCall{Name: "lookup", Args: []ast.Expr{&ast.StringLit{Value: "jobs"}}}
		}, "invalid trigger"},
		{"arity", func(worker *ast.Worker) {
			worker.Meta[0].Value = &ast.FnCall{Name: "queue", Args: []ast.Expr{&ast.StringLit{Value: "jobs"}, &ast.StringLit{Value: "extra"}}}
		}, "2 arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := parseQueueCodegenFile(t, `worker process_job {
  trigger queue("jobs")
  |> log "process"
}
POST /jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { accepted: true }
}`)
			worker := file.Blocks[len(file.Blocks)-2].(*ast.Worker)
			tt.mutate(worker)
			files, err := New().Files(file)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want containing %q", err, tt.want)
			}
			if len(files) != 0 {
				t.Fatalf("fail-closed generation returned %d files", len(files))
			}
		})
	}
}

func TestQueuePolicyCappedBackoffRuntime(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not available")
	}
	file := parseQueueCodegenFile(t, `worker process_job {
  trigger queue("jobs")
  retry 4 backoff(exponential, base: 1s, max: 4s)
  |> log "process"
}
POST /jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { accepted: true }
}`)
	outDir := t.TempDir()
	if err := New().Generate(file, outDir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	script := `
import { processJobBackoffStrategy } from './src/workers/process-job.ts';
if (typeof processJobBackoffStrategy !== 'function') throw new Error('missing strategy');
const type = 'blueprint_capped_exponential';
const actual = [1, 2, 3, 4].map((attempt) => processJobBackoffStrategy(attempt, type));
if (actual.join(',') !== '1000,2000,4000,4000') throw new Error('unexpected delays: ' + actual.join(','));
let rejected = false;
try { processJobBackoffStrategy(1, 'exponential'); } catch { rejected = true; }
if (!rejected) throw new Error('strategy accepted the wrong BullMQ type');
`
	cmd := exec.Command("bun", "-e", script)
	cmd.Dir = outDir
	tmp := t.TempDir()
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "BUN_TMPDIR="+tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("capped backoff runtime check failed: %v\n%s", err, out)
	}
}

func parseQueueCodegenFile(t *testing.T, body string) *ast.File {
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

func matchingQueueWorker() *ast.Worker {
	return &ast.Worker{
		Name: "consumer",
		Meta: []*ast.WorkerMeta{{
			Kind: "trigger",
			Value: &ast.FnCall{
				Name: "queue",
				Args: []ast.Expr{&ast.StringLit{Value: "jobs"}},
			},
		}},
	}
}

func enqueueStatements() []ast.ArrowStmt {
	return []ast.ArrowStmt{&ast.StepStmt{Expr: &ast.FnCall{
		Name: "enqueue",
		Args: []ast.Expr{
			&ast.StringLit{Value: "jobs"},
			&ast.BlockExpr{},
		},
	}}}
}

func outputContent(t *testing.T, files []codegen.OutputFile, path string) string {
	t.Helper()
	for _, file := range files {
		if file.Path == path {
			return string(file.Content)
		}
	}
	t.Fatalf("missing output %s", path)
	return ""
}

func lineContaining(content, needle string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}
