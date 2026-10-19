package python

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

// The Python queue runtime uses the official bullmq package (3.4.x), which
// runs the same Lua scripts as Node BullMQ. A worker declaration becomes
// src/workers/<name>.py (run_<name>/on_fail_<name>) plus a Worker in
// src/lib/workers.py; an enqueue step in a REST body calls enqueue_job in
// src/lib/queue.py. Payload keys are camelCase on the wire, as on Node.

// pyBackoffPolicy is the `backoff(...)` clause of a worker retry.
type pyBackoffPolicy struct {
	strategy string // fixed | exponential
	delayMS  int
	maxMS    int
	hasMax   bool
}

// pyWorkerPolicy is the validated delivery policy of one worker.
type pyWorkerPolicy struct {
	worker    *ast.Worker
	queue     string
	retry     int
	backoff   *pyBackoffPolicy // nil when there is no backoff clause or no retry
	timeoutMS int
}

// pyDurationMS converts an integer millisecond or duration literal to ms.
func pyDurationMS(e ast.Expr) (int, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		n, err := strconv.Atoi(v.Value)
		return n, err == nil && n >= 0
	case *ast.DurationLit:
		number, multiplier, ok := splitDurationLiteral(v.Value)
		if !ok {
			return 0, false
		}
		f, err := strconv.ParseFloat(number, 64)
		if err != nil || f < 0 {
			return 0, false
		}
		return int(f * float64(multiplier)), true
	}
	return 0, false
}

// pyWorkerPolicyFor validates a worker's trigger, retry, backoff and timeout
// metadata with the rules the Node target applies, and returns the policy.
func pyWorkerPolicyFor(w *ast.Worker) (pyWorkerPolicy, error) {
	policy, err := resolve.ResolveWorkerQueuePolicy(w)
	if err != nil {
		return pyWorkerPolicy{}, err
	}
	out := pyWorkerPolicy{worker: w, queue: policy.Queue}
	seenRetry, seenTimeout := false, false
	for _, meta := range w.Meta {
		switch meta.Kind {
		case "trigger":
		case "timeout":
			if seenTimeout {
				return out, fmt.Errorf("worker %q has duplicate timeout metadata", w.Name)
			}
			seenTimeout = true
			ms, ok := pyDurationMS(meta.Value)
			if !ok {
				return out, fmt.Errorf("worker %q timeout must be a non-negative integer millisecond value or duration", w.Name)
			}
			out.timeoutMS = ms
		case "retry":
			if seenRetry {
				return out, fmt.Errorf("worker %q has duplicate retry metadata", w.Name)
			}
			seenRetry = true
			count, ok := meta.Value.(*ast.IntLit)
			n := 0
			if ok {
				n, err = strconv.Atoi(count.Value)
				ok = err == nil && n >= 0
			}
			if !ok {
				return out, fmt.Errorf("worker %q retry must be a non-negative integer counting additional attempts", w.Name)
			}
			out.retry = n
			if len(meta.Extra) == 0 {
				continue
			}
			backoff := &pyBackoffPolicy{strategy: "fixed"}
			seen := map[string]bool{}
			seenDelay := false
			for _, kv := range meta.Extra {
				if seen[kv.Key] || ((kv.Key == "base" || kv.Key == "delay") && seenDelay) {
					return out, fmt.Errorf("worker %q backoff has duplicate %q configuration", w.Name, kv.Key)
				}
				seen[kv.Key] = true
				switch kv.Key {
				case "strategy":
					s := identOrStringName(kv.Value)
					if s != "fixed" && s != "exponential" {
						return out, fmt.Errorf("worker %q backoff strategy %q is unsupported; use fixed or exponential", w.Name, s)
					}
					backoff.strategy = s
				case "base", "delay":
					seenDelay = true
					ms, ok := pyDurationMS(kv.Value)
					if !ok {
						return out, fmt.Errorf("worker %q backoff %s must be a non-negative integer millisecond value or duration", w.Name, kv.Key)
					}
					backoff.delayMS = ms
				case "max":
					ms, ok := pyDurationMS(kv.Value)
					if !ok {
						return out, fmt.Errorf("worker %q backoff max must be a non-negative integer millisecond value or duration", w.Name)
					}
					backoff.maxMS = ms
					backoff.hasMax = true
				default:
					return out, fmt.Errorf("worker %q backoff option %q is unsupported; use strategy, base, delay, or max", w.Name, kv.Key)
				}
			}
			if !seenDelay {
				return out, fmt.Errorf("worker %q backoff requires base or delay", w.Name)
			}
			if n > 0 {
				out.backoff = backoff
			}
		default:
			return out, fmt.Errorf("worker %q metadata %q is not supported by the Python target", w.Name, meta.Kind)
		}
	}
	return out, nil
}

// jobOptionsLiteral is the producer job options dict, identical to the Node
// `<name>JobOptions` export: attempts = retries + 1, and a backoff type that
// names the capped custom strategy when `max` is set.
func (p pyWorkerPolicy) jobOptionsLiteral() string {
	parts := []string{fmt.Sprintf(`"attempts": %d`, p.retry+1)}
	if p.backoff != nil {
		typ := p.backoff.strategy
		if p.backoff.hasMax {
			typ = "blueprint_capped_" + typ
		}
		parts = append(parts, fmt.Sprintf(`"backoff": {"type": %q, "delay": %d}`, typ, p.backoff.delayMS))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// backoffStrategyPy returns the Python custom backoff function for a capped
// policy, or "" when BullMQ's built-in strategies are enough. It mirrors the
// Node workerCustomBackoffStrategyExpr formula.
func (p pyWorkerPolicy) backoffStrategyPy(fnName string) string {
	if p.backoff == nil || !p.backoff.hasMax {
		return ""
	}
	typ := "blueprint_capped_" + p.backoff.strategy
	delay := strconv.Itoa(p.backoff.delayMS)
	if p.backoff.strategy == "exponential" {
		delay += " * 2 ** max(attempts_made - 1, 0)"
	}
	return fmt.Sprintf(`def %s(attempts_made: int, backoff_type: str | None, error: Exception, job: object) -> int:
    if backoff_type != %q:
        raise Exception("Unknown Blueprint backoff strategy: " + str(backoff_type))
    return min(%s, %d)
`, fnName, typ, delay, p.backoff.maxMS)
}

// enqueueOptions maps every enqueued queue name to its producer job options.
func (g *Generator) enqueueOptions() map[string]string {
	out := map[string]string{}
	facts, err := resolve.ResolveQueuePolicies(g.file)
	if err != nil || facts == nil {
		return out
	}
	for queue, policy := range facts.ByQueue {
		for _, w := range g.workers() {
			if w.Name != policy.WorkerName {
				continue
			}
			if p, err := pyWorkerPolicyFor(w); err == nil {
				out[queue] = p.jobOptionsLiteral()
			}
		}
	}
	return out
}

// workers returns every worker declaration in the file.
func (g *Generator) workers() []*ast.Worker {
	if g.file == nil {
		return nil
	}
	var out []*ast.Worker
	for _, b := range g.file.Blocks {
		if w, ok := b.(*ast.Worker); ok {
			out = append(out, w)
		}
	}
	return out
}

// usesEnqueue reports whether any endpoint in the list calls enqueue.
func usesEnqueue(endpoints []*ast.Endpoint) bool {
	for _, ep := range endpoints {
		if endpointEnqueues(ep) {
			return true
		}
	}
	return false
}

// endpointEnqueues reports whether an endpoint body calls enqueue anywhere.
func endpointEnqueues(ep *ast.Endpoint) bool {
	found := false
	walkEndpointExprs(ep, func(e ast.Expr) {
		if fc, ok := e.(*ast.FnCall); ok && fc.Name == "enqueue" {
			found = true
		}
	})
	return found
}

// enqueueFeatures validates an enqueue step; Python translates only the
// static queue + object-literal payload form.
func enqueueFeatures(fn *ast.FnCall) []string {
	if len(fn.Args) != 2 {
		return []string{"`enqueue` requires exactly a static queue name and an object payload"}
	}
	switch fn.Args[0].(type) {
	case *ast.StringLit, *ast.Ident:
	default:
		return []string{"`enqueue` queue name that is not a static string or identifier"}
	}
	if _, ok := fn.Args[1].(*ast.BlockExpr); !ok {
		return []string{"`enqueue` payload that is not an object literal"}
	}
	return nil
}

// emitEnqueue translates `|> enqueue "q" { ... }` into enqueue_job, which
// hands the add to the app's event loop (the handler runs in a threadpool).
func emitEnqueue(b *strings.Builder, fn *ast.FnCall, ctx *bodyCtx, indent string) {
	name := identOrStringName(fn.Args[0])
	payload := blockExprToPyDictWithCtx(fn.Args[1].(*ast.BlockExpr), ctx)
	opts := "{}"
	if ctx != nil {
		if o, ok := ctx.enqueueOpts[name]; ok {
			opts = o
		}
	}
	fmt.Fprintf(b, "%senqueue_job(%s, %s, %s)\n", indent, pyStringLiteral(name), payload, opts)
}

// queueFeatures returns the reasons a queue-related declaration is rejected:
// policy errors and enqueue sites outside REST endpoint bodies.
func (g *Generator) queueFeatures() []string {
	facts, err := resolve.ResolveQueuePolicies(g.file)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, site := range facts.Enqueues {
		if site.Context != resolve.QueueEnqueueEndpoint {
			out = append(out, fmt.Sprintf("enqueue outside REST endpoint bodies (queue %q in %s %q)", site.Queue, site.Context, site.Owner))
		}
	}
	return out
}

// workerFeatures names what a worker body cannot run faithfully on Python.
func (g *Generator) workerFeatures(w *ast.Worker) []string {
	seen := map[string]bool{}
	add := func(r string) { seen[r] = true }
	if !validPythonIdentifier(w.Name) {
		add(fmt.Sprintf("worker name %q that is not a valid Python identifier", w.Name))
	}
	if _, err := pyWorkerPolicyFor(w); err != nil {
		add(err.Error())
	}
	for _, in := range workerInputs(w.Stmts) {
		if !validPythonIdentifier(in.Name) {
			add(fmt.Sprintf("worker input %q that is not a valid Python identifier", in.Name))
		}
	}
	all := append(append([]ast.ArrowStmt{}, w.Stmts...), w.OnFail...)
	ep := &ast.Endpoint{Stmts: all}
	walkEndpointStmtsPy(all, func(st ast.ArrowStmt) {
		switch st.(type) {
		case *ast.OutputStmt:
			add("`->` response output in a worker body (a worker has no response)")
		}
		if stepCallsEmit(st) {
			add("`emit` in a worker body")
		}
	})
	for _, st := range all {
		for _, r := range g.complexStmtFeatures(st) {
			add(r)
		}
	}
	walkEndpointExprs(ep, func(e ast.Expr) {
		if fa, ok := e.(*ast.FieldAccess); ok {
			if base, ok := fa.Base.(*ast.Ident); ok && base.Name == "header" {
				add("`header` read in a worker body (a worker has no request)")
			}
		}
	})
	for _, reason := range pythonArrowNameFeatures(all) {
		add(reason)
	}
	for _, reason := range pythonBareWhereFeatures(ep) {
		add(reason)
	}
	// A bare `update <model>` / `delete <model>` needs a binding from a
	// preceding step in the same body; the Node `<model>_id` stand-in is not
	// translated.
	bound := map[string]bool{}
	collectStepBindings(all, bound)
	walkEndpointExprs(ep, func(e ast.Expr) {
		fc, ok := e.(*ast.FnCall)
		if !ok || (fc.Name != "update" && fc.Name != "delete") || len(fc.Args) < 1 {
			return
		}
		target := identName(fc.Args[0])
		if target == "" || bound[target] || !g.isModelName(target) {
			return
		}
		add(fmt.Sprintf("bare `%s %s` in a worker body without a preceding binding of %s", fc.Name, target, target))
	})
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// collectStepBindings records every step binding name, including nested ones.
func collectStepBindings(stmts []ast.ArrowStmt, out map[string]bool) {
	for _, st := range stmts {
		switch v := st.(type) {
		case *ast.StepStmt:
			if v.Binding != "" {
				out[v.Binding] = true
			}
		case *ast.WhenStmt:
			collectStepBindings(v.Body, out)
		case *ast.TryRecover:
			collectStepBindings(v.Try, out)
			collectStepBindings(v.Recover, out)
		}
	}
}

func (g *Generator) isModelName(name string) bool {
	for _, m := range g.models() {
		if m.Name == name {
			return true
		}
	}
	return false
}

// workerInputs returns the `<- name type` inputs of a worker body.
func workerInputs(stmts []ast.ArrowStmt) []*ast.InputStmt {
	var out []*ast.InputStmt
	for _, s := range stmts {
		if in, ok := s.(*ast.InputStmt); ok {
			out = append(out, in)
		}
	}
	return out
}

// workerInputRead reads one worker input from the job payload under its
// camelCase wire key (as Node's enqueue sends and its worker reads it).
// uuid inputs are converted so they bind to UUID columns.
func workerInputRead(in *ast.InputStmt) []string {
	key := common.CamelCase(in.Name)
	lines := []string{fmt.Sprintf("    %s = _bp_data.get(%s)", in.Name, pyStringLiteral(key))}
	if p, ok := in.Type.(*ast.PrimitiveType); ok && p.Name == "uuid" {
		lines = append(lines, fmt.Sprintf("    %s = UUID(str(%s)) if %s is not None else None", in.Name, in.Name, in.Name))
	}
	return lines
}

// writeWorkerFunc emits one sync function (run_<name> or on_fail_<name>).
// Data operations use a short-lived session, as schedules do.
func (g *Generator) writeWorkerFunc(b *strings.Builder, fnName string, stmts []ast.ArrowStmt, inputs []*ast.InputStmt) {
	fmt.Fprintf(b, "def %s(_bp_data: dict, _bp_error: Exception | None = None) -> None:\n", fnName)
	for _, in := range inputs {
		for _, line := range workerInputRead(in) {
			b.WriteString(line + "\n")
		}
	}
	ctx := g.newBodyCtx(&ast.Endpoint{Stmts: stmts})
	ctx.worker = true
	indent := "    "
	usesDB := stmtsTouchDB(stmts)
	if usesDB {
		b.WriteString("    db = SessionLocal()\n    try:\n")
		indent = "        "
	}
	wrote := false
	for _, st := range stmts {
		if emitBodyStmt(b, st, ctx, indent) {
			wrote = true
		}
	}
	if !wrote {
		b.WriteString(indent + "pass\n")
	}
	if usesDB {
		b.WriteString("    finally:\n        db.close()\n")
	}
	b.WriteString("\n\n")
}

// genWorkerPy emits src/workers/<name>.py.
func (g *Generator) genWorkerPy(w *ast.Worker) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	inputs := workerInputs(w.Stmts)
	all := append(append([]ast.ArrowStmt{}, w.Stmts...), w.OnFail...)
	writeWorkerImports(&b, all, inputs, g.userFnNames(), g.pipeNames())
	b.WriteString("\n")
	if w.Intent != nil {
		fmt.Fprintf(&b, "# %s\n", w.Intent.Text)
	}
	if p, err := pyWorkerPolicyFor(w); err == nil {
		fmt.Fprintf(&b, "# Runs on queue %q; registered by src/lib/workers.py.\n", p.queue)
	}
	b.WriteString("\n")
	g.writeWorkerFunc(&b, "run_"+w.Name, w.Stmts, inputs)
	g.writeWorkerFunc(&b, "on_fail_"+w.Name, w.OnFail, inputs)
	return codegen.OutputFile{
		Path:    "src/workers/" + pyModuleName(w.Name) + ".py",
		Content: []byte(strings.TrimRight(b.String(), "\n") + "\n"),
	}
}

// writeWorkerImports emits the imports a worker module uses. The body
// imports come from the schedule writer, with a worker's guard logging to
// logger instead of raising HTTPException.
func writeWorkerImports(b *strings.Builder, stmts []ast.ArrowStmt, inputs []*ast.InputStmt, userFns, pipes map[string]bool) {
	for _, in := range inputs {
		if p, ok := in.Type.(*ast.PrimitiveType); ok && p.Name == "uuid" {
			b.WriteString("from uuid import UUID\n")
			break
		}
	}
	writeScheduleImports(b, stmts, stmtsTouchDB(stmts), userFns, pipes, true)
}

// genQueuePy emits src/lib/queue.py: lazily created BullMQ producers on the
// app's event loop, and the enqueue_job bridge used by sync REST handlers.
func (g *Generator) genQueuePy() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`"""BullMQ producers on Redis, shared by enqueue steps and the scheduler.

Queues are created on the app's event loop and closed at shutdown. A REST
handler that enqueues is a sync function run in FastAPI's threadpool, so
enqueue_job hands the add to the loop and waits for it. Under pytest, and
before the app lifespan has started, enqueue_job raises instead of waiting.
start_when_reachable starts the scheduler and the workers once Redis answers,
so an unreachable Redis at boot delays them instead of the whole app.
"""
import asyncio
import sys
from collections.abc import Awaitable, Callable
from typing import Any
from urllib.parse import unquote, urlsplit

from bullmq import Queue
from fastapi.encoders import jsonable_encoder
from redis.asyncio import Redis

from src.lib.env import env
from src.lib.log import logger

ENQUEUE_TIMEOUT_SECONDS = 30.0
# Delay between attempts to reach Redis while the scheduler and workers wait to start.
REDIS_RETRY_SECONDS = 5.0
# Socket timeout for every BullMQ connection. Workers block for up to 10 s on
# the job marker; a connection whose reply never arrives (a half-open socket)
# then fails after this timeout and reconnects, instead of leaving the worker
# silently idle.
REDIS_SOCKET_TIMEOUT_SECONDS = 30


def connection_options() -> dict[str, Any]:
    """BullMQ connection options parsed from REDIS_URL, with the socket timeout."""
    url = urlsplit(env.REDIS_URL)
    opts: dict[str, Any] = {
        "host": url.hostname or "localhost",
        "port": url.port or 6379,
        "db": int(url.path.lstrip("/") or 0),
        "socket_timeout": REDIS_SOCKET_TIMEOUT_SECONDS,
    }
    if url.username:
        opts["username"] = unquote(url.username)
    if url.password is not None:
        opts["password"] = unquote(url.password)
    if url.scheme == "rediss":
        opts["ssl"] = True
    return opts


_queues: dict[str, Queue] = {}
_loop: asyncio.AbstractEventLoop | None = None


async def _ping() -> None:
    client = Redis(**connection_options(), socket_connect_timeout=REDIS_RETRY_SECONDS)
    try:
        await client.ping()
    finally:
        await client.aclose()


async def start_when_reachable(*starts: Callable[[], Awaitable[None]]) -> None:
    """Await each start in order once Redis answers PING (no-op under pytest).

    Runs as a background task of the app lifespan, so the app serves requests
    meanwhile. A failed attempt is logged and retried every REDIS_RETRY_SECONDS;
    each start must be safe to call again after a failure.
    """
    if "pytest" in sys.modules:
        return
    pending = list(starts)
    while pending:
        try:
            await _ping()
            while pending:
                await pending[0]()
                pending.pop(0)
        except Exception as error:
            logger.warning(
                f"Schedules and workers not started ({error}); retrying in {REDIS_RETRY_SECONDS:g}s"
            )
            await asyncio.sleep(REDIS_RETRY_SECONDS)


def bind_loop() -> None:
    """Record the running event loop; called from the app lifespan."""
    global _loop
    _loop = asyncio.get_running_loop()


async def close_all() -> None:
    """Close every producer queue and detach from the event loop."""
    global _loop
    _loop = None
    while _queues:
        _, queue = _queues.popitem()
        await queue.close()


async def _add(queue_name: str, data: Any, opts: dict[str, Any]) -> None:
    queue = _queues.get(queue_name)
    if queue is None:
        queue = Queue(queue_name, {"connection": connection_options()})
        _queues[queue_name] = queue
    # Job name 'job' and payload keys match the Node target's enqueue.
    await queue.add("job", data, opts)


def enqueue_job(queue_name: str, data: dict[str, Any], opts: dict[str, Any]) -> None:
    """Add one job to a BullMQ queue. Called from a sync REST handler."""
    if "pytest" in sys.modules:
        raise RuntimeError("enqueue needs the running app; it is not available under pytest")
    if _loop is None:
        raise RuntimeError(f"enqueue to {queue_name!r} needs the app lifespan, which is not running")
    payload = jsonable_encoder(data)
    future = asyncio.run_coroutine_threadsafe(_add(queue_name, payload, opts), _loop)
    try:
        future.result(timeout=ENQUEUE_TIMEOUT_SECONDS)
    except TimeoutError:
        future.cancel()
        raise RuntimeError(
            f"enqueue to {queue_name!r} did not complete within {ENQUEUE_TIMEOUT_SECONDS:g}s"
        ) from None
`)
	return codegen.OutputFile{Path: "src/lib/queue.py", Content: []byte(b.String())}
}

// genWorkersRegistryPy emits src/lib/workers.py: one BullMQ Worker per
// declaration, with the Node target's timeout and final-attempt compensation.
func (g *Generator) genWorkersRegistryPy(workers []*ast.Worker) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`"""Runs the worker declarations as BullMQ Workers on Redis.

A timed-out run rejects the wait but does not cancel the thread running the
body, as on the Node target. On the configured final attempt a failing run
calls its on_fail once with the payload and the error, and the error is
re-raised so BullMQ records the failure. start() is a no-op under pytest.
"""
import asyncio
import sys
from collections.abc import Awaitable, Callable
from typing import Any

from bullmq import Worker

from src.lib.queue import connection_options
`)
	for _, w := range workers {
		fmt.Fprintf(&b, "from src.workers.%s import on_fail_%s, run_%s\n", pyModuleName(w.Name), w.Name, w.Name)
	}
	b.WriteString(`

async def _with_timeout(awaitable: Awaitable[None], seconds: float) -> None:
    try:
        await asyncio.wait_for(awaitable, seconds)
    except TimeoutError:
        raise RuntimeError("Worker timeout") from None


def _processor(
    run: Callable[[dict], None],
    on_fail: Callable[[dict, Exception], None],
    timeout_seconds: float,
) -> Callable[[Any, str], Awaitable[None]]:
    async def process(job: Any, token: str) -> None:
        data = dict(job.data or {})
        try:
            if timeout_seconds > 0:
                await _with_timeout(asyncio.to_thread(run, data), timeout_seconds)
            else:
                await asyncio.to_thread(run, data)
        except Exception as error:
            if job.attemptsMade + 1 >= job.opts.get("attempts", 1):
                await asyncio.to_thread(on_fail, data, error)
            raise

    return process


`)
	var specs []string
	for _, w := range workers {
		p, err := pyWorkerPolicyFor(w)
		if err != nil {
			// Validated by unsupportedFeatures before emission.
			panic(fmt.Sprintf("worker %s: %v", w.Name, err))
		}
		fnName := "None"
		if s := p.backoffStrategyPy("_backoff_" + w.Name); s != "" {
			b.WriteString(s + "\n\n")
			fnName = "_backoff_" + w.Name
		}
		seconds := strconv.FormatFloat(float64(p.timeoutMS)/1000, 'f', -1, 64)
		specs = append(specs, fmt.Sprintf("    (%q, _processor(run_%s, on_fail_%s, %s), %s),", p.queue, w.Name, w.Name, seconds, fnName))
	}
	b.WriteString("# (queue, processor, custom backoff strategy) for every worker declaration.\n")
	b.WriteString("_SPECS: list[tuple[str, Callable[[Any, str], Awaitable[None]], Callable[..., int] | None]] = [\n")
	b.WriteString(strings.Join(specs, "\n"))
	b.WriteString(`
]

_workers: list[Worker] = []


async def start() -> None:
    """Start one Worker per declaration (no-op under pytest)."""
    if "pytest" in sys.modules or _workers:
        return
    for queue_name, processor, strategy in _SPECS:
        opts: dict[str, Any] = {"connection": connection_options()}
        if strategy is not None:
            opts["settings"] = {"backoffStrategy": strategy}
        _workers.append(Worker(queue_name, processor, opts))


async def stop() -> None:
    """Close every Worker, waiting for jobs in progress."""
    while _workers:
        await _workers.pop().close()
`)
	return codegen.OutputFile{Path: "src/lib/workers.py", Content: []byte(b.String())}
}

// hasQueueRuntime reports whether the file declares a worker or a schedule,
// which need the BullMQ runtime (queue, workers, scheduler) and REDIS_URL.
func (g *Generator) hasQueueRuntime() bool {
	if len(g.workers()) > 0 {
		return true
	}
	for _, b := range g.file.Blocks {
		if _, ok := b.(*ast.Schedule); ok {
			return true
		}
	}
	return false
}
