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
	writeScheduleImports(&b, s.Stmts, usesDB, g.userFnNames(), g.pipeNames())
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
func writeScheduleImports(b *strings.Builder, stmts []ast.ArrowStmt, usesDB bool, userFns, pipes map[string]bool) {
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

// genSchedulerPy emits src/lib/scheduler.py. Every schedule is registered with
// APScheduler's cron trigger. A tick queues the run on one worker thread, so
// runs never overlap and queued runs execute in order, as the Node target's
// single scheduler worker does. A failing run is retried with the same budget
// as the Node target (3 attempts, exponential backoff from 1s) and each failure
// is logged.
func (g *Generator) genSchedulerPy(schedules []*ast.Schedule) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`"""Runs the schedule declarations in-process on APScheduler 3.x.

Schedules use the process-local timezone, as the Node target does. start() is a
no-op under pytest (the Node target skips its scheduler under Vitest the same
way), so importing the app in a test never starts background runs.
"""
import sys
import time
from collections.abc import Callable
from concurrent.futures import ThreadPoolExecutor

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger

from src.lib.log import logger
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
	b.WriteString(`MAX_ATTEMPTS = 3
RETRY_BASE_SECONDS = 1.0

_scheduler: BackgroundScheduler | None = None
_executor: ThreadPoolExecutor | None = None


def _run(name: str, run: Callable[[], None]) -> None:
    for attempt in range(1, MAX_ATTEMPTS + 1):
        try:
            run()
            return
        except Exception:
            logger.exception("schedule %s failed (attempt %d of %d)", name, attempt, MAX_ATTEMPTS)
            if attempt < MAX_ATTEMPTS:
                time.sleep(RETRY_BASE_SECONDS * 2 ** (attempt - 1))


def _tick(name: str, run: Callable[[], None]) -> None:
    if _executor is not None:
        _executor.submit(_run, name, run)


def start() -> None:
    """Register every schedule and start the scheduler (no-op under pytest)."""
    global _scheduler, _executor
    if "pytest" in sys.modules or _scheduler is not None:
        return
    _executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix="bp-schedule")
    _scheduler = BackgroundScheduler()
    for name, cron, run in SCHEDULES:
        _scheduler.add_job(
            _tick,
            CronTrigger.from_crontab(cron),
            args=[name, run],
            id=name,
            max_instances=1,
            coalesce=True,
        )
    _scheduler.start()


def stop() -> None:
    """Stop ticking and wait for the run in progress. Queued runs that have not started are dropped."""
    global _scheduler, _executor
    if _scheduler is None or _executor is None:
        return
    _scheduler.shutdown(wait=False)
    _executor.shutdown(wait=True, cancel_futures=True)
    _scheduler = None
    _executor = None
`)
	return codegen.OutputFile{Path: "src/lib/scheduler.py", Content: []byte(b.String())}
}
