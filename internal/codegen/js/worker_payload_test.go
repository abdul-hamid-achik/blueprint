package js

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// The producer camel-cases payload keys (as emit does), so the worker must
// read each input under its camelCase name. Before, it read `_bpData.job_id`
// while the producer sent `jobId`, and every input arrived undefined.
func TestWorkerReadsTheKeysTheProducerSends(t *testing.T) {
	src := `blueprint "payload" {
  version  "0.1.0"
  port     3000
  runtime  node
  database postgres
  queue    redis
}

model job {
  id     uuid   primary
  status string default("pending")
}

@ "Queue a job"
POST /jobs {
  <- job_id uuid required
  |> enqueue "jobs" { job_id: job_id }
  -> 202 { queued: true }
}

worker process_job {
  trigger  queue("jobs")

  <- job_id  uuid
  |> job = fetch job(job_id)
  |> update job { status: "done" }
}
`
	file, errs := parser.ParseFile("payload.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if checkErrs := checker.Check(file); len(checkErrs) > 0 {
		t.Fatalf("checker errors: %v", checkErrs)
	}
	outDir := t.TempDir()
	if err := New().Generate(file, outDir); err != nil {
		t.Fatalf("generate error: %v", err)
	}
	route, err := os.ReadFile(filepath.Join(outDir, "src/routes/jobs.ts"))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := os.ReadFile(filepath.Join(outDir, "src/workers/process-job.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(route), ".add('job', { jobId: jobId }") {
		t.Fatalf("producer payload should use camelCase keys:\n%s", route)
	}
	if !strings.Contains(string(worker), "const jobId = _bpData.jobId;") {
		t.Fatalf("worker should read the camelCase key the producer sends:\n%s", worker)
	}
	if strings.Contains(string(worker), "_bpData.job_id") {
		t.Fatalf("worker still reads the .bp spelling:\n%s", worker)
	}
}
