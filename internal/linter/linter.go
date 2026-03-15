package linter

import (
	"fmt"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// Issue represents a lint finding.
type Issue struct {
	File    string
	Line    int
	Col     int
	Level   string // "error", "warning", "info"
	Rule    string
	Message string
	Hint    string
}

func (i Issue) String() string {
	return fmt.Sprintf("%s:%d:%d [%s] %s: %s", i.File, i.Line, i.Col, i.Level, i.Rule, i.Message)
}

// Lint runs all lint rules against the parsed file and returns a list of issues.
func Lint(f *ast.File) []Issue {
	var issues []Issue
	issues = append(issues, checkBlockOrdering(f)...)
	issues = append(issues, checkIntentOnEndpoints(f)...)
	issues = append(issues, checkEmptyEndpoints(f)...)
	return issues
}

// blockPriority returns a numeric ordering priority for a top-level block type.
// Lower numbers come first in canonical order.
func blockPriority(block ast.TopLevel) int {
	switch block.(type) {
	case *ast.Blueprint:
		return 1
	case *ast.Include:
		return 2
	case *ast.Secret:
		return 3
	case *ast.Env:
		return 4
	case *ast.Locale, *ast.Translation:
		return 5
	case *ast.StateMachine:
		return 5
	case *ast.Analytics, *ast.SaveSchema:
		return 6
	case *ast.TypeDecl, *ast.Alias, *ast.Enum:
		return 5
	case *ast.Model:
		return 6
	case *ast.Content:
		return 6
	case *ast.Fn:
		return 7
	case *ast.Pipe:
		return 8
	case *ast.Middleware:
		return 9
	case *ast.Endpoint, *ast.StreamEndpoint, *ast.WsEndpoint:
		return 10
	case *ast.Worker:
		return 11
	case *ast.Schedule:
		return 12
	case *ast.External:
		return 13
	case *ast.Subscribe:
		return 14
	case *ast.Fixture:
		return 15
	case *ast.Test, *ast.TestGroup:
		return 16
	default:
		return 99
	}
}

// blockName returns a human-readable name for a top-level block.
func blockName(block ast.TopLevel) string {
	switch n := block.(type) {
	case *ast.Blueprint:
		return fmt.Sprintf("blueprint %q", n.Name)
	case *ast.Include:
		return fmt.Sprintf("include %q", n.Path)
	case *ast.Secret:
		return fmt.Sprintf("secret %s", n.Name)
	case *ast.Env:
		return fmt.Sprintf("env %s", n.Name)
	case *ast.Locale:
		return fmt.Sprintf("locale %s", n.Code)
	case *ast.Translation:
		return fmt.Sprintf("translation %s", n.Name)
	case *ast.StateMachine:
		return fmt.Sprintf("state %s", n.Name)
	case *ast.Analytics:
		return fmt.Sprintf("analytics %s", n.Name)
	case *ast.SaveSchema:
		return fmt.Sprintf("save %s", n.Name)
	case *ast.TypeDecl:
		return fmt.Sprintf("type %s", n.Name)
	case *ast.Alias:
		return fmt.Sprintf("alias %s", n.Name)
	case *ast.Enum:
		return fmt.Sprintf("enum %s", n.Name)
	case *ast.Model:
		return fmt.Sprintf("model %s", n.Name)
	case *ast.Content:
		return fmt.Sprintf("content %s", n.Name)
	case *ast.Fn:
		return fmt.Sprintf("fn %s", n.Name)
	case *ast.Pipe:
		return fmt.Sprintf("pipe %s", n.Name)
	case *ast.Middleware:
		return fmt.Sprintf("middleware %s", n.Name)
	case *ast.Endpoint:
		return fmt.Sprintf("%s %s", n.Method, n.Path)
	case *ast.StreamEndpoint:
		return fmt.Sprintf("STREAM %s", n.Path)
	case *ast.WsEndpoint:
		return fmt.Sprintf("WS %s", n.Path)
	case *ast.Worker:
		return fmt.Sprintf("worker %s", n.Name)
	case *ast.Schedule:
		return fmt.Sprintf("schedule %s", n.Name)
	case *ast.External:
		return fmt.Sprintf("external %q", n.Name)
	case *ast.Subscribe:
		return fmt.Sprintf("subscribe %q", n.Event)
	case *ast.Fixture:
		return fmt.Sprintf("fixture %q", n.Name)
	case *ast.Test:
		return fmt.Sprintf("test %s", n.Name)
	case *ast.TestGroup:
		return fmt.Sprintf("test_group %s", n.Name)
	default:
		return fmt.Sprintf("unknown %T", block)
	}
}

// blockTypeName returns the canonical category name for ordering messages.
func blockTypeName(block ast.TopLevel) string {
	switch block.(type) {
	case *ast.Blueprint:
		return "blueprint"
	case *ast.Include:
		return "include"
	case *ast.Secret:
		return "secret"
	case *ast.Env:
		return "env"
	case *ast.Locale:
		return "locale"
	case *ast.Translation:
		return "translation"
	case *ast.StateMachine:
		return "state"
	case *ast.Analytics:
		return "analytics"
	case *ast.SaveSchema:
		return "save"
	case *ast.TypeDecl:
		return "type"
	case *ast.Alias:
		return "alias"
	case *ast.Enum:
		return "enum"
	case *ast.Model:
		return "model"
	case *ast.Content:
		return "content"
	case *ast.Fn:
		return "fn"
	case *ast.Pipe:
		return "pipe"
	case *ast.Middleware:
		return "middleware"
	case *ast.Endpoint:
		return "endpoint"
	case *ast.StreamEndpoint:
		return "stream endpoint"
	case *ast.WsEndpoint:
		return "ws endpoint"
	case *ast.Worker:
		return "worker"
	case *ast.Schedule:
		return "schedule"
	case *ast.External:
		return "external"
	case *ast.Subscribe:
		return "subscribe"
	case *ast.Fixture:
		return "fixture"
	case *ast.Test:
		return "test"
	case *ast.TestGroup:
		return "test_group"
	default:
		return "unknown"
	}
}

// checkBlockOrdering checks that top-level blocks follow the canonical ordering from SPEC §3.1.
func checkBlockOrdering(f *ast.File) []Issue {
	var issues []Issue

	allBlocks := make([]ast.TopLevel, 0, 1+len(f.Blocks))
	if f.Blueprint != nil {
		allBlocks = append(allBlocks, f.Blueprint)
	}
	allBlocks = append(allBlocks, f.Blocks...)

	maxPriority := 0
	for _, block := range allBlocks {
		prio := blockPriority(block)
		if prio < maxPriority {
			loc := block.Location()
			issues = append(issues, Issue{
				File:    loc.File,
				Line:    loc.Line,
				Col:     loc.Col,
				Level:   "warning",
				Rule:    "block-ordering",
				Message: fmt.Sprintf("%s should appear before %s blocks", blockName(block), blockTypeName(block)),
				Hint:    "Reorder blocks to follow the canonical order: blueprint, include, secret, env, type/alias/enum, model, fn, pipe, middleware, endpoint, worker, schedule, external, subscribe, fixture, test",
			})
		} else {
			maxPriority = prio
		}
	}

	return issues
}

// checkIntentOnEndpoints checks that all endpoints have an intent annotation.
func checkIntentOnEndpoints(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		switch n := block.(type) {
		case *ast.Endpoint:
			if n.Intent == nil {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "intent-on-endpoints",
					Message: fmt.Sprintf("Endpoint %s %s is missing an @ intent description", n.Method, n.Path),
					Hint:    fmt.Sprintf("Add `@ \"describe what this endpoint does\"` before `%s %s`", n.Method, n.Path),
				})
			}
		case *ast.StreamEndpoint:
			if n.Intent == nil {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "intent-on-endpoints",
					Message: fmt.Sprintf("Endpoint STREAM %s is missing an @ intent description", n.Path),
					Hint:    fmt.Sprintf("Add `@ \"describe what this stream does\"` before `STREAM %s`", n.Path),
				})
			}
		case *ast.WsEndpoint:
			if n.Intent == nil {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "intent-on-endpoints",
					Message: fmt.Sprintf("Endpoint WS %s is missing an @ intent description", n.Path),
					Hint:    fmt.Sprintf("Add `@ \"describe what this websocket does\"` before `WS %s`", n.Path),
				})
			}
		}
	}

	return issues
}

// checkEmptyEndpoints checks for endpoints with no inputs and no arrow statements.
func checkEmptyEndpoints(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		switch n := block.(type) {
		case *ast.Endpoint:
			hasInputs := false
			for _, stmt := range n.Stmts {
				if _, ok := stmt.(*ast.InputStmt); ok {
					hasInputs = true
					break
				}
			}
			if !hasInputs && len(n.Stmts) == 0 {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "empty-endpoint",
					Message: fmt.Sprintf("Endpoint %s %s has no inputs or statements", n.Method, n.Path),
					Hint:    "Add inputs (`<-`) or statements (`|>`, `->`) to implement this endpoint",
				})
			}
		case *ast.StreamEndpoint:
			if len(n.Stmts) == 0 && len(n.Handlers) == 0 {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "empty-endpoint",
					Message: fmt.Sprintf("Endpoint STREAM %s has no inputs or statements", n.Path),
					Hint:    "Add inputs or statements to implement this stream endpoint",
				})
			}
		case *ast.WsEndpoint:
			if len(n.OnConnect) == 0 && len(n.OnMessage) == 0 && len(n.OnDisconnect) == 0 {
				loc := n.Location()
				issues = append(issues, Issue{
					File:    loc.File,
					Line:    loc.Line,
					Col:     loc.Col,
					Level:   "warning",
					Rule:    "empty-endpoint",
					Message: fmt.Sprintf("Endpoint WS %s has no on_connect/on_message/on_disconnect handlers", n.Path),
					Hint:    "Add on_connect, on_message, or on_disconnect blocks to implement this WebSocket endpoint",
				})
			}
		}
	}

	return issues
}
