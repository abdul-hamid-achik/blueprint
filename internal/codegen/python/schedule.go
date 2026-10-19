package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// scheduleFeatures names what a schedule declaration needs that the Python
// target cannot run faithfully. An empty result means genSchedulePy can
// translate it and src/lib/scheduler.py can register it.
func (g *Generator) scheduleFeatures(s *ast.Schedule) []string {
	seen := map[string]bool{}
	if !validPythonIdentifier(s.Name) {
		seen[fmt.Sprintf("schedule name %q that is not a valid Python identifier", s.Name)] = true
	}
	// CronTrigger.from_crontab takes the standard five fields only and would
	// raise at app startup on anything else, so reject it at build time.
	if fields := strings.Fields(s.Cron); len(fields) != 5 {
		seen[fmt.Sprintf("schedule %s cron %q (only the standard 5-field cron syntax)", s.Name, s.Cron)] = true
	}
	walkEndpointStmtsPy(s.Stmts, func(st ast.ArrowStmt) {
		switch st.(type) {
		case *ast.InputStmt:
			seen["`<-` input in a schedule body (a schedule has no request)"] = true
		case *ast.OutputStmt:
			seen["`->` response output in a schedule body (a schedule has no response)"] = true
		}
		if stepCallsEmit(st) {
			seen["`emit` in a schedule body"] = true
		}
	})
	for _, st := range s.Stmts {
		for _, r := range g.complexStmtFeatures(st) {
			seen[r] = true
		}
	}
	ep := &ast.Endpoint{Stmts: s.Stmts}
	walkEndpointExprs(ep, func(e ast.Expr) {
		if fa, ok := e.(*ast.FieldAccess); ok {
			if base, ok := fa.Base.(*ast.Ident); ok && base.Name == "header" {
				seen["`header` read in a schedule body (a schedule has no request)"] = true
			}
		}
	})
	for _, reason := range pythonArrowNameFeatures(s.Stmts) {
		seen[reason] = true
	}
	for _, reason := range pythonBareWhereFeatures(ep) {
		seen[reason] = true
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// genSchedulePy emits src/schedules/<name>.py: one sync function that runs the
// schedule body. Data operations run on a short-lived SessionLocal() session
// that is closed (and so rolled back, if a write was left uncommitted) when the
// body returns or raises. The body is translated by the same statement emitter
// the REST handlers use, so `log`, `when`, `try/recover`, guards and data
// operations behave as they do in an endpoint.
func (g *Generator) genSchedulePy(s *ast.Schedule) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	usesDB := stmtsTouchDB(s.Stmts)
	writeScheduleImports(&b, s.Stmts, usesDB, g.userFnNames(), g.pipeNames(), false)
	b.WriteString("\n")

	if s.Intent != nil {
		fmt.Fprintf(&b, "# %s\n", s.Intent.Text)
	}
	fmt.Fprintf(&b, "# Runs at cron %q; registered by src/lib/scheduler.py.\n", s.Cron)
	fmt.Fprintf(&b, "def run_%s() -> None:\n", s.Name)

	ctx := g.newBodyCtx(&ast.Endpoint{Stmts: s.Stmts})
	indent := "    "
	if usesDB {
		b.WriteString("    db = SessionLocal()\n")
		b.WriteString("    try:\n")
		indent = "        "
	}
	wrote := false
	for _, st := range s.Stmts {
		if emitBodyStmt(&b, st, ctx, indent) {
			wrote = true
		}
	}
	if !wrote {
		b.WriteString(indent + "pass\n")
	}
	if usesDB {
		b.WriteString("    finally:\n")
		b.WriteString("        db.close()\n")
	}

	return codegen.OutputFile{
		Path:    "src/schedules/" + pyModuleName(s.Name) + ".py",
		Content: []byte(b.String()),
	}
}

// writeScheduleImports emits exactly the imports a schedule body uses, the
// schedule-side counterpart of writeRouteImports (which is keyed on endpoints
// and their request inputs, neither of which a schedule has).
// A worker body (worker=true) logs its guard failures and returns instead of
// raising HTTPException, so it imports the logger rather than FastAPI.
func writeScheduleImports(b *strings.Builder, stmts []ast.ArrowStmt, usesDB bool, userFns, pipes map[string]bool, worker bool) {
	needsNow, needsTimedelta, needsLog, needsEnv := false, false, false, false
	userCalls := map[string]bool{}
	walkEndpointExprs(&ast.Endpoint{Stmts: stmts}, func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.NowLit:
			needsNow = true
		case *ast.FieldAccess:
			if base, ok := v.Base.(*ast.Ident); ok && base.Name == "env" {
				needsEnv = true
			}
			if v.Field == "ago" || pyDurationField(v.Field) != "" {
				needsTimedelta = true
				needsNow = true // ago uses datetime.now(timezone.utc)
			}
		case *ast.FnCall:
			if v.Name == "log" {
				needsLog = true
			}
			if userFns[v.Name] {
				userCalls[v.Name] = true
			}
		}
	})
	needsGuard := stmtsHaveGuards(stmts)
	if worker && needsGuard {
		needsLog = true
		needsGuard = false
	}

	if usesDB {
		sqlImports := []string{"select"}
		if stmtsHavePaginated(stmts) {
			sqlImports = append(sqlImports, "func")
		}
		if stmtsHaveMultiWhere(stmts) {
			sqlImports = append(sqlImports, "and_")
		}
		if stmtsHaveOrInWhere(stmts) {
			sqlImports = append(sqlImports, "or_")
		}
		fmt.Fprintf(b, "from sqlalchemy import %s\n", strings.Join(sqlImports, ", "))
	}
	if stmtsHavePaginated(stmts) {
		b.WriteString("from types import SimpleNamespace\n")
	}
	if needsNow {
		dtImports := []string{"datetime", "timezone"}
		if needsTimedelta {
			dtImports = append(dtImports, "timedelta")
		}
		fmt.Fprintf(b, "from datetime import %s\n", strings.Join(dtImports, ", "))
	}
	if needsGuard {
		b.WriteString("from fastapi import HTTPException\n")
	}
	if usesDB {
		b.WriteString("from src.lib.db import SessionLocal\n")
		b.WriteString("from src.models import schema\n")
	}
	if needsEnv {
		b.WriteString("from src.lib.env import env\n")
	}
	if needsLog {
		b.WriteString("from src.lib.log import logger\n")
	}
	names := make([]string, 0, len(userCalls))
	for n := range userCalls {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(b, "from %s import %s\n", userCallModule(n, pipes), n)
	}
}

// genSchedulerPy emits src/lib/scheduler.py. Every schedule becomes a BullMQ
// job scheduler on the __bp_scheduler queue (the same Lua scripts as the Node
// target), so each tick runs once across all instances sharing Redis. A
// single Worker on that queue dispatches by job name; a failing run is retried
// by BullMQ (3 attempts, exponential backoff from 1s), as on Node. Schedulers
// whose id is no longer declared are removed at startup. start() is a no-op
// under pytest.
func (g *Generator) genSchedulerPy(schedules []*ast.Schedule) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`"""Runs the schedule declarations as BullMQ job schedulers on Redis.

Schedules are registered on the __bp_scheduler queue with the process-local
timezone, as the Node target does. Registration is idempotent at startup, and
a schedule removed from the source has its scheduler removed on the next start.
start() is a no-op under pytest (the Node target skips its scheduler under
Vitest the same way), so importing the app in a test never starts background runs.
"""
import asyncio
import sys
from collections.abc import Callable
from typing import Any

from bullmq import Queue, Worker

from src.lib.queue import connection_options
`)
	for _, s := range schedules {
		fmt.Fprintf(&b, "from src.schedules.%s import run_%s\n", pyModuleName(s.Name), s.Name)
	}
	b.WriteString("\n# (name, cron, run) for every schedule declaration, in source order.\n")
	b.WriteString("SCHEDULES: list[tuple[str, str, Callable[[], None]]] = [\n")
	for _, s := range schedules {
		fmt.Fprintf(&b, "    (%q, %q, run_%s),\n", s.Name, s.Cron, s.Name)
	}
	b.WriteString("]\n\n")
	b.WriteString(`SCHEDULER_QUEUE = "__bp_scheduler"

_queue: Queue | None = None
_worker: Worker | None = None


async def _process(job: Any, token: str) -> None:
    for name, _, run in SCHEDULES:
        if name == job.name:
            await asyncio.to_thread(run)
            return
    raise Exception(f"Unknown scheduled job: {job.name}")


async def start() -> None:
    """Register every schedule, drop stale ones, and start the Worker (no-op under pytest).

    Safe to call again after a failure: registration is idempotent.
    """
    global _queue, _worker
    if "pytest" in sys.modules or _worker is not None:
        return
    if _queue is None:
        _queue = Queue(SCHEDULER_QUEUE, {"connection": connection_options()})
    declared = {name for name, _, _ in SCHEDULES}
    for name, cron, _ in SCHEDULES:
        await _queue.upsertJobScheduler(
            name,
            {"pattern": cron},
            name,
            {},
            {"attempts": 3, "backoff": {"type": "exponential", "delay": 1000}},
        )
    for scheduler in await _queue.getJobSchedulers():
        if scheduler["key"] not in declared:
            await _queue.removeJobScheduler(scheduler["key"])
    _worker = Worker(SCHEDULER_QUEUE, _process, {"connection": connection_options()})


async def stop() -> None:
    """Stop the Worker (waiting for a run in progress), then close the queue."""
    global _queue, _worker
    if _worker is not None:
        await _worker.close()
        _worker = None
    if _queue is not None:
        await _queue.close()
        _queue = None
`)
	return codegen.OutputFile{Path: "src/lib/scheduler.py", Content: []byte(b.String())}
}
