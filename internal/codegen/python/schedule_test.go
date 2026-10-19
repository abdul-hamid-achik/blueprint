package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// scheduleHead declares the database and one model every schedule test uses.
const scheduleHead = `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
secret DATABASE_URL required
model job {
  id uuid primary
  status string default("queued")
  created timestamp default(now)
}
`

func TestPython_ScheduleEmitsBodyRunnerAndLifespan(t *testing.T) {
	out := buildPython(t, scheduleHead+`schedule sweep_jobs {
  cron "* * * * *"
  |> stale = query job where(status == "queued", created < 1.minutes.ago)
  |> map stale: update job { status: "expired" }
  |> finished = query job where(status == "done")
  |> delete finished
  |> log "Expired {stale.count} stale jobs, deleted {finished.count} finished jobs"
}
`)
	body := readPy(t, out, "src/schedules/sweep_jobs.py")
	for _, want := range []string{
		"from src.lib.db import SessionLocal\n",
		"from src.models import schema\n",
		"def run_sweep_jobs() -> None:\n    db = SessionLocal()\n    try:\n",
		"        for item in stale:\n            item.status = \"expired\"\n",
		"        db.commit()\n",
		"        for _row in finished:\n            db.delete(_row)\n",
		"        logger.info(f\"Expired {len(stale)} stale jobs, deleted {len(finished)} finished jobs\")\n",
		"    finally:\n        db.close()\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("schedule module missing %q:\n%s", want, body)
		}
	}

	sched := readPy(t, out, "src/lib/scheduler.py")
	for _, want := range []string{
		"from src.schedules.sweep_jobs import run_sweep_jobs\n",
		`("sweep_jobs", "* * * * *", run_sweep_jobs),`,
		"await _queue.upsertJobScheduler(",
		`{"pattern": cron},`,
		`{"attempts": 3, "backoff": {"type": "exponential", "delay": 1000}},`,
		`SCHEDULER_QUEUE = "__bp_scheduler"`,
		`Worker(SCHEDULER_QUEUE, _process, {"connection": connection_options()})`,
		"if scheduler[\"key\"] not in declared:",
		`if "pytest" in sys.modules`,
		"raise Exception(f\"Unknown scheduled job: {job.name}\")",
	} {
		if !strings.Contains(sched, want) {
			t.Errorf("scheduler module missing %q:\n%s", want, sched)
		}
	}
	readPy(t, out, "src/schedules/__init__.py")

	app := readPy(t, out, "src/app.py")
	for _, want := range []string{
		"from src.lib import scheduler\n",
		"async def lifespan(app: FastAPI):\n    bp_queue.bind_loop()\n    await scheduler.start()\n",
		"        await scheduler.stop()\n",
		`app = FastAPI(title="x", version="1.0", lifespan=lifespan)`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.py missing %q:\n%s", want, app)
		}
	}

	pyproject := readPy(t, out, "pyproject.toml")
	if !strings.Contains(pyproject, `"bullmq>=3.4,<4",`) {
		t.Errorf("pyproject.toml should pin the official bullmq 3.4.x client when a schedule exists:\n%s", pyproject)
	}
	if strings.Contains(pyproject, "apscheduler") {
		t.Errorf("pyproject.toml must not depend on APScheduler:\n%s", pyproject)
	}
	if env := readPy(t, out, "src/lib/env.py"); !strings.Contains(env, "REDIS_URL: str\n") {
		t.Errorf("env.py should require REDIS_URL when a schedule exists:\n%s", env)
	}
}

func TestPython_ScheduleWithoutDataOpsSkipsSession(t *testing.T) {
	out := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
schedule heartbeat {
  cron "*/5 * * * *"
  |> log "alive"
}
`)
	body := readPy(t, out, "src/schedules/heartbeat.py")
	if strings.Contains(body, "SessionLocal") || strings.Contains(body, "db.close") {
		t.Errorf("a schedule with no data operations must not open a session:\n%s", body)
	}
	if !strings.Contains(body, "def run_heartbeat() -> None:\n    logger.info(\"alive\")\n") {
		t.Errorf("unexpected body:\n%s", body)
	}
	sched := readPy(t, out, "src/lib/scheduler.py")
	if !strings.Contains(sched, `("heartbeat", "*/5 * * * *", run_heartbeat),`) {
		t.Errorf("scheduler does not register heartbeat:\n%s", sched)
	}
	if strings.Contains(sched, "from src.lib.db") {
		t.Errorf("scheduler should not import the database layer for a body that needs none")
	}
}

func TestPython_ScheduleBodyImportsGuardsAndTryRecover(t *testing.T) {
	out := buildPython(t, scheduleHead+`schedule guarded {
  cron "0 4 * * 0"
  |> rows = query job where(status == "done")
  |> guard rows.count -> 409 "nothing to do"
  |> try {
    |> delete rows
  } recover {
    |> log "delete failed: {error.message}"
  }
}
`)
	body := readPy(t, out, "src/schedules/guarded.py")
	for _, want := range []string{
		"from fastapi import HTTPException\n",
		"from src.lib.log import logger\n",
		"if not (len(rows)):\n            raise HTTPException(status_code=409, detail=\"nothing to do\")\n",
		"        except Exception as error:\n            db.rollback()\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("schedule module missing %q:\n%s", want, body)
		}
	}
}

func TestPython_NoScheduleLeavesSchedulerOut(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	for _, rel := range []string{"src/lib/scheduler.py", "src/schedules/__init__.py"} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err == nil {
			t.Errorf("%s should not be generated without a schedule", rel)
		}
	}
	app := readPy(t, outDir, "src/app.py")
	if strings.Contains(app, "lifespan") || strings.Contains(app, "scheduler") {
		t.Errorf("app.py should not wire a scheduler without a schedule:\n%s", app)
	}
	if strings.Contains(readPy(t, outDir, "pyproject.toml"), "bullmq") {
		t.Errorf("pyproject.toml should not depend on bullmq without a schedule or worker")
	}
}

func TestPython_ScheduleRejectsWhatTheBodyCannotRun(t *testing.T) {
	cases := map[string]struct {
		src    string
		reason string
	}{
		"response output": {
			src:    "schedule s {\n  cron \"* * * * *\"\n  |> log \"x\"\n  -> 200 \"done\"\n}\n",
			reason: "response output",
		},
		"request input": {
			src:    "schedule s {\n  cron \"* * * * *\"\n  <- name string\n  |> log \"x\"\n}\n",
			reason: "`<-` input",
		},
		"header read": {
			src:    "schedule s {\n  cron \"* * * * *\"\n  |> log header.Authorization\n}\n",
			reason: "`header` read",
		},
		"emit": {
			src:    "schedule s {\n  cron \"* * * * *\"\n  |> emit tick { n: 1 }\n}\n",
			reason: "`emit`",
		},
		"four-field cron": {
			src:    "schedule s {\n  cron \"* * * *\"\n  |> log \"x\"\n}\n",
			reason: "5-field cron",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(scheduleHead+tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected the python target to reject the schedule")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error should name %q: %v", tc.reason, err)
			}
			if strings.Contains(err.Error(), "`schedule` declarations") {
				t.Errorf("schedule should no longer be rejected as a whole: %v", err)
			}
		})
	}
}

func TestPython_CollectionCountAndSelfMapUpdateTranslate(t *testing.T) {
	out := buildPython(t, scheduleHead+`GET /api/done {
  |> rows = query job where(status == "done")
  |> map rows: update job { status: "archived" }
  -> 200 { archived: rows.count }
}
`)
	route := readPy(t, out, "src/routes/done.py")
	for _, want := range []string{
		"    for item in rows:\n        item.status = \"archived\"\n    db.commit()\n",
		`"archived": len(rows)`,
	} {
		if !strings.Contains(route, want) {
			t.Errorf("route missing %q:\n%s", want, route)
		}
	}
}
