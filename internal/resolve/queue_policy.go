package resolve

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// QueueEnqueueContext identifies the top-level declaration that owns an
// enqueue call. Targets use this to fail closed when they cannot create a
// producer in a particular runtime module.
type QueueEnqueueContext string

const (
	QueueEnqueueEndpoint     QueueEnqueueContext = "endpoint"
	QueueEnqueueFunction     QueueEnqueueContext = "function"
	QueueEnqueuePipe         QueueEnqueueContext = "pipe"
	QueueEnqueueMiddleware   QueueEnqueueContext = "middleware"
	QueueEnqueueStream       QueueEnqueueContext = "stream"
	QueueEnqueueWebSocket    QueueEnqueueContext = "websocket"
	QueueEnqueueWorker       QueueEnqueueContext = "worker"
	QueueEnqueueSchedule     QueueEnqueueContext = "schedule"
	QueueEnqueueSubscription QueueEnqueueContext = "subscription"
	QueueEnqueueTest         QueueEnqueueContext = "test"
	QueueEnqueueTestGroup    QueueEnqueueContext = "test_group"
)

// EnqueueSite is one statically named enqueue call and its owning declaration.
type EnqueueSite struct {
	Queue   string
	Context QueueEnqueueContext
	Owner   string
	Section string
	Loc     lexer.Loc
}

// QueuePolicy is the worker delivery policy selected for an enqueue queue.
// RetryCount is the number of additional attempts requested by `retry N`.
type QueuePolicy struct {
	Queue      string
	WorkerName string
	RetryCount int
	Backoff    []ast.KVPair
}

// QueuePolicyFacts contains every enqueue site and the unambiguous worker
// policy selected for each queue referenced by those sites.
type QueuePolicyFacts struct {
	Enqueues []EnqueueSite
	ByQueue  map[string]QueuePolicy
}

// ResolveQueuePolicies maps every enqueue call to exactly one worker trigger.
// Missing and ambiguous worker policies fail before a generator emits files.
func ResolveQueuePolicies(file *ast.File) (*QueuePolicyFacts, error) {
	facts := &QueuePolicyFacts{ByQueue: make(map[string]QueuePolicy)}
	if file == nil {
		return facts, nil
	}

	workersByQueue := make(map[string][]QueuePolicy)
	for _, block := range file.Blocks {
		worker, ok := block.(*ast.Worker)
		if !ok {
			continue
		}
		policy, err := ResolveWorkerQueuePolicy(worker)
		if err != nil {
			return nil, err
		}
		workersByQueue[policy.Queue] = append(workersByQueue[policy.Queue], policy)
	}

	for _, block := range file.Blocks {
		if err := collectBlockEnqueues(block, &facts.Enqueues); err != nil {
			return nil, err
		}
	}

	for _, site := range facts.Enqueues {
		policies := workersByQueue[site.Queue]
		switch len(policies) {
		case 0:
			return nil, fmt.Errorf(
				"enqueue queue %q at %s has no matching worker policy; declare a worker with trigger queue(%q)",
				site.Queue, site.Loc, site.Queue,
			)
		case 1:
			facts.ByQueue[site.Queue] = policies[0]
		default:
			names := make([]string, 0, len(policies))
			for _, policy := range policies {
				names = append(names, policy.WorkerName)
			}
			sort.Strings(names)
			return nil, fmt.Errorf(
				"enqueue queue %q at %s has ambiguous worker policies from %s; exactly one worker may own an enqueued queue",
				site.Queue, site.Loc, strings.Join(names, ", "),
			)
		}
	}

	return facts, nil
}

// ResolveWorkerQueuePolicy validates a worker's trigger and returns the single
// static queue it owns. With no trigger, the worker name remains the queue.
func ResolveWorkerQueuePolicy(worker *ast.Worker) (QueuePolicy, error) {
	if worker == nil {
		return QueuePolicy{}, fmt.Errorf("cannot resolve queue policy for a nil worker")
	}
	policy := QueuePolicy{Queue: worker.Name, WorkerName: worker.Name}
	triggerSeen := false
	retryResolved := false
	for _, meta := range worker.Meta {
		switch meta.Kind {
		case "trigger":
			if triggerSeen {
				return QueuePolicy{}, fmt.Errorf("worker %q at %s has duplicate trigger metadata; declare at most one static queue trigger", worker.Name, meta.Loc)
			}
			triggerSeen = true
			name, err := workerTriggerQueueName(meta.Value)
			if err != nil {
				return QueuePolicy{}, fmt.Errorf("worker %q has invalid trigger at %s: %w", worker.Name, meta.Loc, err)
			}
			policy.Queue = name
		case "retry":
			if retryResolved {
				continue
			}
			if count, ok := nonNegativeInt(meta.Value); ok {
				policy.RetryCount = count
			}
			policy.Backoff = append([]ast.KVPair(nil), meta.Extra...)
			retryResolved = true
		}
	}
	return policy, nil
}

func workerTriggerQueueName(expr ast.Expr) (string, error) {
	if call, ok := expr.(*ast.FnCall); ok {
		if call.Name != "queue" || len(call.Args) != 1 {
			return "", fmt.Errorf("trigger must be a static string or identifier, or queue(static); got %s with %d arguments", call.Name, len(call.Args))
		}
		expr = call.Args[0]
	}
	name := staticQueueName(expr)
	if name == "" {
		return "", fmt.Errorf("trigger requires a non-empty static string or identifier queue name")
	}
	return name, nil
}

func nonNegativeInt(expr ast.Expr) (int, bool) {
	lit, ok := expr.(*ast.IntLit)
	if !ok {
		return 0, false
	}
	value, err := strconv.Atoi(lit.Value)
	return value, err == nil && value >= 0
}

func staticQueueName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.StringLit:
		return value.Value
	case *ast.Ident:
		return value.Name
	default:
		return ""
	}
}

func collectBlockEnqueues(block ast.TopLevel, sites *[]EnqueueSite) error {
	collect := func(stmts []ast.ArrowStmt, context QueueEnqueueContext, owner, section string) error {
		collector := &enqueueSiteCollector{context: context, owner: owner, section: section}
		for _, stmt := range stmts {
			ast.Walk(stmt, collector)
			if collector.err != nil {
				return collector.err
			}
		}
		*sites = append(*sites, collector.sites...)
		return nil
	}

	switch decl := block.(type) {
	case *ast.Fn:
		if decl.Logic != nil {
			return collect(decl.Logic.Stmts, QueueEnqueueFunction, decl.Name, "logic")
		}
	case *ast.Pipe:
		return collect(decl.Stmts, QueueEnqueuePipe, decl.Name, "body")
	case *ast.Middleware:
		if err := collect(decl.Before, QueueEnqueueMiddleware, decl.Name, "before"); err != nil {
			return err
		}
		return collect(decl.After, QueueEnqueueMiddleware, decl.Name, "after")
	case *ast.Endpoint:
		return collect(decl.Stmts, QueueEnqueueEndpoint, decl.Method+" "+decl.Path, "body")
	case *ast.StreamEndpoint:
		if err := collect(decl.Stmts, QueueEnqueueStream, decl.Path, "body"); err != nil {
			return err
		}
		for _, handler := range decl.Handlers {
			if err := collect(handler.Body, QueueEnqueueStream, decl.Path, handler.EventName); err != nil {
				return err
			}
		}
	case *ast.WsEndpoint:
		if err := collect(decl.OnConnect, QueueEnqueueWebSocket, decl.Path, "on_connect"); err != nil {
			return err
		}
		if err := collect(decl.OnMessage, QueueEnqueueWebSocket, decl.Path, "on_message"); err != nil {
			return err
		}
		return collect(decl.OnDisconnect, QueueEnqueueWebSocket, decl.Path, "on_disconnect")
	case *ast.Worker:
		if err := collect(decl.Stmts, QueueEnqueueWorker, decl.Name, "body"); err != nil {
			return err
		}
		return collect(decl.OnFail, QueueEnqueueWorker, decl.Name, "on_fail")
	case *ast.Schedule:
		return collect(decl.Stmts, QueueEnqueueSchedule, decl.Name, "body")
	case *ast.Subscribe:
		return collect(decl.Stmts, QueueEnqueueSubscription, decl.Event, "body")
	case *ast.Test:
		if err := collect(decl.Setup, QueueEnqueueTest, decl.Name, "setup"); err != nil {
			return err
		}
		return collect(decl.Cleanup, QueueEnqueueTest, decl.Name, "cleanup")
	case *ast.TestGroup:
		return collect(decl.SharedSetup, QueueEnqueueTestGroup, decl.Name, "shared_setup")
	}
	return nil
}

type enqueueSiteCollector struct {
	ast.BaseVisitor
	context QueueEnqueueContext
	owner   string
	section string
	sites   []EnqueueSite
	err     error
}

func (v *enqueueSiteCollector) VisitFnCall(call *ast.FnCall) bool {
	if v.err != nil || call.Name != "enqueue" {
		return v.err == nil
	}
	if len(call.Args) != 2 {
		v.err = fmt.Errorf("enqueue at %s requires exactly a static queue name and payload, got %d arguments", call.Location(), len(call.Args))
		return false
	}
	queue := staticQueueName(call.Args[0])
	if queue == "" {
		v.err = fmt.Errorf("enqueue at %s requires a static string or identifier queue name", call.Location())
		return false
	}
	v.sites = append(v.sites, EnqueueSite{
		Queue:   queue,
		Context: v.context,
		Owner:   v.owner,
		Section: v.section,
		Loc:     call.Location(),
	})
	return true
}
