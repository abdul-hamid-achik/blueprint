package python_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// workerHead declares the database, Redis and a job model every worker test uses.
const workerHead = `blueprint "x" { version "1.0" port 3000 runtime node database postgres queue redis }
secret DATABASE_URL required
secret REDIS_URL required
model job {
  id uuid primary
  status string default("queued")
}
`

func TestPython_WorkerProducerOptionsMatchNode(t *testing.T) {
	out := buildPython(t, workerHead+`POST /api/jobs {
  <- job_id uuid
  |> enqueue "notify" { job_id: job_id, retries: 2 }
  -> 202 { ok: true }
}
worker notify {
  trigger queue("notify")
  retry 2 backoff(exponential, base: 1s, max: 5s)
  <- job_id uuid
  |> log "notified"
}
`)
	route := readPy(t, out, "src/routes/jobs.py")
	// Node: notifyJobOptions = { attempts: 3, backoff: { type: "blueprint_capped_exponential", delay: 1 * 1000 } }
	if !strings.Contains(route, `enqueue_job("notify", {"jobId": job_id, "retries": 2}, {"attempts": 3, "backoff": {"type": "blueprint_capped_exponential", "delay": 1000}})`) {
		t.Errorf("producer must send the Node job options with camelCase payload keys:\n%s", route)
	}
	workers := readPy(t, out, "src/lib/workers.py")
	// Node: Math.min(1 * 1000 * Math.pow(2, Math.max(attemptsMade - 1, 0)), 5 * 1000)
	for _, want := range []string{
		`if backoff_type != "blueprint_capped_exponential":`,
		`return min(1000 * 2 ** max(attempts_made - 1, 0), 5000)`,
		`opts["settings"] = {"backoffStrategy": strategy}`,
		`("notify", _processor(run_notify, on_fail_notify, 0), _backoff_notify),`,
	} {
		if !strings.Contains(workers, want) {
			t.Errorf("workers.py missing %q:\n%s", want, workers)
		}
	}
}

func TestPython_WorkerFixedRetryProducerOptions(t *testing.T) {
	out := buildPython(t, workerHead+`POST /api/jobs {
  <- job_id uuid
  |> enqueue "jobs" { job_id: job_id }
  -> 202 { ok: true }
}
worker process_job {
  trigger queue("jobs")
  retry 2 backoff(fixed, base: 1s)
  timeout 30s
  <- job_id uuid
  |> log "processed"
}
`)
	route := readPy(t, out, "src/routes/jobs.py")
	if !strings.Contains(route, `{"attempts": 3, "backoff": {"type": "fixed", "delay": 1000}}`) {
		t.Errorf("fixed retry must produce attempts 3 and a fixed backoff:\n%s", route)
	}
	workers := readPy(t, out, "src/lib/workers.py")
	if !strings.Contains(workers, `("jobs", _processor(run_process_job, on_fail_process_job, 30), None),`) {
		t.Errorf("worker timeout must be passed in seconds with no custom strategy:\n%s", workers)
	}
	if strings.Contains(workers, "def _backoff_process_job") {
		t.Errorf("a fixed uncapped backoff needs no custom strategy:\n%s", workers)
	}
}

func TestPython_WorkerReadsCamelCaseInputsAndGuardsReturn(t *testing.T) {
	out := buildPython(t, workerHead+`worker process_job {
  trigger queue("jobs")
  <- order_id uuid
  |> guard order_id -> 409 "missing order"
  |> log "processing"
  on_fail {
    |> log "failed"
  }
}
`)
	body := readPy(t, out, "src/workers/process_job.py")
	for _, want := range []string{
		`order_id = _bp_data.get("orderId")`,
		`order_id = UUID(str(order_id)) if order_id is not None else None`,
		"def run_process_job(_bp_data: dict, _bp_error: Exception | None = None) -> None:",
		"    if not (order_id):\n        logger.error(\"missing order\")\n        return\n",
		"def on_fail_process_job(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("worker module missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "HTTPException") {
		t.Errorf("a worker guard must not raise HTTPException:\n%s", body)
	}
}

func TestPython_QueueModuleUsesOfficialBullMQAndRefusesUnsafeEnqueue(t *testing.T) {
	out := buildPython(t, workerHead+`POST /api/jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { ok: true }
}
worker process_job {
  trigger queue("jobs")
  |> log "x"
}
`)
	queue := readPy(t, out, "src/lib/queue.py")
	for _, want := range []string{
		"from bullmq import Queue\n",
		`await queue.add("job", data, opts)`,
		`if "pytest" in sys.modules:`,
		"asyncio.run_coroutine_threadsafe(_add(queue_name, payload, opts), _loop)",
	} {
		if !strings.Contains(queue, want) {
			t.Errorf("queue.py missing %q:\n%s", want, queue)
		}
	}
	if !strings.Contains(readPy(t, out, "pyproject.toml"), `"bullmq>=3.4,<4",`) {
		t.Errorf("pyproject.toml must pin bullmq 3.4.x")
	}
	if !strings.Contains(readPy(t, out, "src/lib/env.py"), "REDIS_URL: str = Field(min_length=1)") {
		t.Errorf("REDIS_URL must stay required for workers")
	}
}

// An unreachable Redis at boot must not keep the app from serving: the lifespan
// starts the workers in a background task that waits for Redis.
func TestPython_WorkersStartInBackgroundOnceRedisAnswers(t *testing.T) {
	out := buildPython(t, workerHead+`POST /api/jobs {
  |> enqueue "jobs" { id: "1" }
  -> 202 { ok: true }
}
worker process_job {
  trigger queue("jobs")
  |> log "x"
}
`)
	app := readPy(t, out, "src/app.py")
	for _, want := range []string{
		"bp_jobs = asyncio.create_task(bp_queue.start_when_reachable(workers.start))\n",
		"        bp_jobs.cancel()\n",
		"        await workers.stop()\n",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.py missing %q:\n%s", want, app)
		}
	}
	if strings.Contains(app, "await workers.start()") {
		t.Errorf("the lifespan must not await workers.start() before serving:\n%s", app)
	}
	queue := readPy(t, out, "src/lib/queue.py")
	for _, want := range []string{
		"async def start_when_reachable(*starts: Callable[[], Awaitable[None]]) -> None:",
		"            await _ping()\n",
		"            await asyncio.sleep(REDIS_RETRY_SECONDS)\n",
	} {
		if !strings.Contains(queue, want) {
			t.Errorf("queue.py missing %q:\n%s", want, queue)
		}
	}
}

func TestPython_WorkerRejectsResponseOutputAndUnboundUpdate(t *testing.T) {
	cases := map[string]string{
		"worker response output": workerHead + `worker process_job {
  trigger queue("jobs")
  |> log "x"
  -> 200 "ok"
}
`,
		"unbound update": workerHead + `worker process_job {
  trigger queue("jobs")
  <- job_id uuid
  on_fail {
    |> update job { status: "failed" }
  }
}
`,
		"enqueue from a function": workerHead + `worker process_job {
  trigger queue("jobs")
  |> log "x"
}
fn queue_it {
  -> bool
  logic {
    |> enqueue "jobs" { id: "1" }
    -> true
  }
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("x.bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse: %v", errs)
			}
			_, err := python.New().Files(file)
			if err == nil || !strings.Contains(err.Error(), "python target does not yet support") {
				t.Fatalf("want unsupported-feature error, got %v", err)
			}
		})
	}
}
