package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// nodeTestPreflightIssue describes an authored-test feature that the Node test
// generator cannot execute faithfully. This is deliberately a bp test
// preflight, rather than a codegen rejection: users can still build a project
// that contains these declarative examples and provide a hand-written Vitest
// equivalent alongside it.
type nodeTestPreflightIssue struct {
	Location string
	TestName string
	Message  string
	Hint     string
}

// preflightNodeAuthoredTests parses and checks the source independently of the
// build so bp test can stop before writing output, installing dependencies, or
// invoking Vitest. Syntax/include/checker failures are intentionally left to
// cmdBuildWithOptions, which already renders those diagnostics with source
// context.
func preflightNodeAuthoredTests(filename, outDir string) []nodeTestPreflightIssue {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	file, parseErrors := parser.ParseFile(filename, src)
	if len(parseErrors) > 0 {
		return nil
	}
	if includeErrors := resolveIncludes(file, filename); len(includeErrors) > 0 {
		return nil
	}
	if checkErrors := checker.Check(file); len(checkErrors) > 0 {
		return nil
	}
	return nodeAuthoredTestIssues(file, outDir, explicitZeroRepeatOffsets(file, filename, src))
}

// The parser stores both an omitted repeat modifier and repeat(0) as zero.
// Keep this small token-side fact so preflight can reject the explicit form,
// which the generator would otherwise silently execute once.
type nodeTestSourceOffset struct {
	file   string
	offset int
}

func explicitZeroRepeatOffsets(file *ast.File, filename string, src []byte) map[nodeTestSourceOffset]lexer.Loc {
	result := make(map[nodeTestSourceOffset]lexer.Loc)
	scanZeroRepeatOffsets(result, filename, src)
	seenFiles := map[string]bool{filename: true}
	for _, block := range file.Blocks {
		test, ok := block.(*ast.Test)
		if !ok || test.Request == nil || seenFiles[test.Request.Loc.File] {
			continue
		}
		seenFiles[test.Request.Loc.File] = true
		includedSource, err := os.ReadFile(test.Request.Loc.File)
		if err != nil {
			continue
		}
		scanZeroRepeatOffsets(result, test.Request.Loc.File, includedSource)
	}
	return result
}

func scanZeroRepeatOffsets(result map[nodeTestSourceOffset]lexer.Loc, filename string, src []byte) {
	tokens, errs := lexer.Tokenize(filename, src)
	if len(errs) > 0 {
		return
	}
	for i := 0; i+4 < len(tokens); i++ {
		if tokens[i].Kind != lexer.TokenRequest ||
			tokens[i+1].Kind != lexer.TokenRepeat ||
			tokens[i+2].Kind != lexer.TokenLParen ||
			tokens[i+3].Kind != lexer.TokenInt ||
			tokens[i+3].Value != "0" ||
			tokens[i+4].Kind != lexer.TokenRParen {
			continue
		}
		result[nodeTestSourceOffset{file: tokens[i].Loc.File, offset: tokens[i].Loc.Offset}] = tokens[i+1].Loc
	}
}

func printNodeTestPreflightIssues(issues []nodeTestPreflightIssue) {
	noun := "surface"
	if len(issues) != 1 {
		noun = "surfaces"
	}
	fmt.Fprintf(os.Stderr, "Error: authored Node tests use %d unsupported %s; Vitest was not started:\n", len(issues), noun)
	for _, issue := range issues {
		fmt.Fprintf(os.Stderr, "  %s: test %q: %s\n", issue.Location, issue.TestName, issue.Message)
		if issue.Hint != "" {
			fmt.Fprintf(os.Stderr, "    Hint: %s\n", issue.Hint)
		}
	}
}

type nodeTestPreflightIndex struct {
	file        *ast.File
	outDir      string
	functions   map[string]*ast.Fn
	pipes       map[string]*ast.Pipe
	middlewares map[string]*ast.Middleware
	models      map[string]*ast.Model
	endpoints   []*ast.Endpoint
	zeroRepeats map[nodeTestSourceOffset]lexer.Loc
	hasDatabase bool
}

func newNodeTestPreflightIndex(file *ast.File, outDir string, zeroRepeats map[nodeTestSourceOffset]lexer.Loc) *nodeTestPreflightIndex {
	idx := &nodeTestPreflightIndex{
		file:        file,
		outDir:      outDir,
		functions:   make(map[string]*ast.Fn),
		pipes:       make(map[string]*ast.Pipe),
		middlewares: make(map[string]*ast.Middleware),
		models:      make(map[string]*ast.Model),
		zeroRepeats: zeroRepeats,
	}
	if file.Blueprint != nil {
		for _, entry := range file.Blueprint.Entries {
			if entry.Key == "database" {
				idx.hasDatabase = true
				break
			}
		}
	}
	for _, block := range file.Blocks {
		switch node := block.(type) {
		case *ast.Fn:
			idx.functions[node.Name] = node
		case *ast.Pipe:
			idx.pipes[node.Name] = node
		case *ast.Middleware:
			idx.middlewares[node.Name] = node
		case *ast.Model:
			idx.models[node.Name] = node
			idx.hasDatabase = true
		case *ast.Content:
			model := node.AsModel()
			idx.models[model.Name] = model
			idx.hasDatabase = true
		case *ast.Endpoint:
			idx.endpoints = append(idx.endpoints, node)
		}
	}
	return idx
}

func nodeAuthoredTestIssues(file *ast.File, outDir string, zeroRepeats map[nodeTestSourceOffset]lexer.Loc) []nodeTestPreflightIssue {
	idx := newNodeTestPreflightIndex(file, outDir, zeroRepeats)
	var issues []nodeTestPreflightIssue
	groups := make([]*ast.TestGroup, 0)
	for _, block := range file.Blocks {
		switch node := block.(type) {
		case *ast.Test:
			// Test groups reference these top-level tests by name; there are no
			// separate nested Test nodes in the current AST, so this pass covers
			// grouped and ungrouped cases alike.
			issues = append(issues, idx.issuesForTest(node)...)
		case *ast.TestGroup:
			groups = append(groups, node)
		}
	}
	for _, group := range groups {
		if len(group.SharedSetup) == 0 {
			continue
		}
		for _, testName := range group.Tests {
			issues = append(issues, nodeTestPreflightIssue{
				Location: group.SharedSetup[0].Location().String(),
				TestName: testName,
				Message:  fmt.Sprintf("test_group %q shared_setup statements are not emitted into generated Vitest", group.Name),
				Hint:     "Move required setup into each authored test, or use a hand-written Vitest describe/beforeAll block.",
			})
		}
	}
	return issues
}

func (idx *nodeTestPreflightIndex) issuesForTest(test *ast.Test) []nodeTestPreflightIssue {
	var issues []nodeTestPreflightIssue
	seen := make(map[string]bool)
	add := func(key, location, message, hint string) {
		if seen[key] {
			return
		}
		seen[key] = true
		issues = append(issues, nodeTestPreflightIssue{
			Location: location,
			TestName: test.Name,
			Message:  message,
			Hint:     hint,
		})
	}

	if test.Target == nil {
		add(
			"missing-target",
			test.Loc.String(),
			"no target request would be emitted for this authored test",
			"Add target METHOD /literal/path, or move non-HTTP behavior into a hand-written Vitest test.",
		)
	} else if testPathHasPlaceholder(test.Target.Path) {
		add(
			"dynamic-path",
			test.Target.Loc.String(),
			fmt.Sprintf("target path %q still contains a :parameter placeholder", test.Target.Path),
			"Put a literal value in the authored test target (for example /api/items/123); setup bindings are not substituted into target paths yet.",
		)
	}

	if len(test.Cleanup) > 0 {
		add(
			"cleanup",
			test.Cleanup[0].Location().String(),
			"cleanup statements are parsed but not emitted into the generated Vitest case",
			"Use the generated PGlite reset for database isolation, or move this teardown into a hand-written Vitest test.",
		)
	}
	if test.Request != nil {
		key := nodeTestSourceOffset{file: test.Request.Loc.File, offset: test.Request.Loc.Offset}
		if loc, explicitZero := idx.zeroRepeats[key]; explicitZero {
			add(
				"zero-repeat",
				loc.String(),
				"request repeat(0) would be silently emitted as one request",
				"Use repeat(1) for one request or a positive count greater than one for repetition.",
			)
		}
	}

	setupBindings := make(map[string]bool)
	for i, stmt := range test.Setup {
		step, ok := stmt.(*ast.StepStmt)
		if !ok {
			add(
				fmt.Sprintf("setup-statement:%d", i),
				stmt.Location().String(),
				fmt.Sprintf("setup statement %T is not in the authored-test setup allowlist", stmt),
				"Keep setup to seed/save data steps (and optional log steps), or use a hand-written Vitest beforeAll block.",
			)
			continue
		}
		call, ok := step.Expr.(*ast.FnCall)
		if !ok {
			add(
				fmt.Sprintf("setup-expression:%d", i),
				step.Loc.String(),
				"setup step is not a supported seed, save, or log call",
				"Use |> binding = seed model { ... }, |> binding = save model { ... }, or a simple unbound log call.",
			)
			continue
		}
		switch call.Name {
		case "seed", "save":
			if reason := idx.unsupportedSetupWrite(call, setupBindings); reason != "" {
				add(
					fmt.Sprintf("setup-%s:%d", call.Name, i),
					call.Loc.String(),
					reason,
					"Use a declared model, a literal object body, and values made only from literals or earlier setup bindings.",
				)
				continue
			}
		case "log":
			if step.Binding != "" || len(call.Args) > 2 {
				add(
					fmt.Sprintf("setup-log:%d", i),
					call.Loc.String(),
					"setup log must be an unbound log call with at most a value and log level",
					"Remove the binding/extra arguments, or use a hand-written Vitest beforeAll block.",
				)
				continue
			}
			if len(call.Args) > 0 {
				if reason := unsupportedTestValue(call.Args[0], setupBindings, false); reason != "" {
					add(
						fmt.Sprintf("setup-log-value:%d", i),
						call.Args[0].Location().String(),
						"setup log value "+reason,
						"Log a literal or an earlier setup binding.",
					)
				}
			}
			if len(call.Args) == 2 {
				level, ok := call.Args[1].(*ast.Ident)
				if !ok || !supportedConsoleLevel(level.Name) {
					add(
						fmt.Sprintf("setup-log-level:%d", i),
						call.Args[1].Location().String(),
						"setup log level is not a supported console method",
						"Use debug, info, log, warn, or error.",
					)
				}
			}
		default:
			add(
				fmt.Sprintf("setup-call:%d", i),
				call.Loc.String(),
				fmt.Sprintf("setup call %q is not in the authored-test setup allowlist", call.Name),
				"Keep setup to seed/save data steps (and optional log steps); query/update/delete and runtime-only helpers need a hand-written Vitest setup.",
			)
			continue
		}
		if step.Binding != "" {
			setupBindings[step.Binding] = true
		}
	}

	var body ast.Expr
	hasFixtureRequestValue := false
	if test.Request != nil {
		entryCounts := make(map[string]int)
		for _, entry := range test.Request.Entries {
			key := strings.ToLower(entry.Key)
			entryCounts[key]++
			if (key == "body" || key == "auth") && entry.Key != key {
				add(
					"request-entry-case:"+key,
					entry.Loc.String(),
					fmt.Sprintf("request entry %q is ignored because generated authored tests require lowercase %q", entry.Key, key),
					fmt.Sprintf("Rename the entry to %s.", key),
				)
			}
			switch key {
			case "body":
				body = entry.Value
				if _, ok := entry.Value.(*ast.BlockExpr); !ok {
					add(
						"request-body-shape",
						entry.Loc.String(),
						"request body must be an object for the supported JSON authored-test path",
						"Wrap the JSON fields in body { ... }, or use a hand-written Vitest request.",
					)
				}
				if test.Target != nil && (strings.EqualFold(test.Target.Method, "GET") || strings.EqualFold(test.Target.Method, "HEAD")) {
					add(
						"request-body-method",
						entry.Loc.String(),
						fmt.Sprintf("%s authored requests cannot carry the generated JSON body", strings.ToUpper(test.Target.Method)),
						"Move values into the literal target query string, or use a hand-written Vitest request.",
					)
				}
				if reason := unsupportedTestValue(entry.Value, setupBindings, true); reason != "" {
					add(
						"request-body-value",
						entry.Value.Location().String(),
						"request body "+reason,
						"Use JSON literals and fields from supported seed/save setup bindings; function calls are not imported into authored test files.",
					)
				}
			case "auth":
				if reason := unsupportedTestAuth(entry.Value); reason != "" {
					add(
						"auth",
						entry.Loc.String(),
						reason,
						"Use exactly one of auth api_key(expr), auth bearer(expr), auth jwt(expr), or auth basic(pre_encoded_expr).",
					)
				} else if call, ok := entry.Value.(*ast.FnCall); ok {
					reason := unsupportedTestValue(call.Args[0], setupBindings, false)
					if ident, wholeSetupRow := call.Args[0].(*ast.Ident); reason == "" && wholeSetupRow && setupBindings[ident.Name] {
						reason = fmt.Sprintf("uses the whole setup row %q instead of a scalar field", ident.Name)
					}
					if reason != "" {
						add(
							"auth-value",
							call.Args[0].Location().String(),
							"auth value "+reason,
							"Use a scalar literal or a field from a supported seed/save setup binding.",
						)
					}
				}
			case "header", "headers":
				add(
					"request-headers",
					entry.Loc.String(),
					"custom request headers are ignored by the authored-test generator",
					"Use a supported auth entry, or write a Vitest request with an explicit headers object.",
				)
			case "multipart", "form", "form_data", "formdata", "file", "files":
				add(
					"multipart",
					entry.Loc.String(),
					fmt.Sprintf("request entry %q requires form/multipart encoding, but authored requests are emitted as JSON", entry.Key),
					"Construct FormData in a hand-written Vitest test until authored multipart requests are implemented.",
				)
			default:
				add(
					"request-entry:"+key,
					entry.Loc.String(),
					fmt.Sprintf("request entry %q is not emitted by the authored-test generator", entry.Key),
					"Authored Node requests currently support only body and auth entries; use a hand-written Vitest test for this request shape.",
				)
			}

			for _, call := range callsInNode(entry.Value) {
				if call.Name == "fixture" {
					hasFixtureRequestValue = true
					fixtureName := fixtureCallName(call)
					if fixtureName == "" {
						fixtureName = "<dynamic>"
					}
					add(
						"fixture-request",
						call.Loc.String(),
						fmt.Sprintf("fixture(%q) is JSON-stringified instead of copied and encoded as multipart data", fixtureName),
						"Manage the asset path and FormData in a hand-written Vitest test.",
					)
					continue
				}
			}
		}
		for key, count := range entryCounts {
			if (key == "body" || key == "auth") && count > 1 {
				add(
					"duplicate-request-entry:"+key,
					test.Request.Loc.String(),
					fmt.Sprintf("request contains %d %q entries, but the authored-test emitter supports at most one", count, key),
					"Keep one body and at most one auth entry.",
				)
			}
		}
	}

	hasBodyAssertion := false
	modelAssertionCount := 0
	for _, assertion := range test.Expect {
		if assertion.Kind == "body" {
			hasBodyAssertion = true
		}
		if assertion.Kind == "model" {
			modelAssertionCount++
		}
	}
	if modelAssertionCount > 1 {
		add(
			"multiple-model-assertions",
			test.Loc.String(),
			"multiple model assertions redeclare the generated _row binding in one Vitest case",
			"Combine conditions into one model assertion, or use a hand-written Vitest test for multiple database queries.",
		)
	}
	if len(test.Expect) == 0 {
		add(
			"missing-expectation",
			test.Loc.String(),
			"expect block is missing or contains no executable assertion",
			"Add at least one supported status, body, header, model, or last_status assertion.",
		)
	}
	for i, assertion := range test.Expect {
		if reason := idx.unsupportedAssertion(assertion, hasBodyAssertion); reason != "" {
			add(
				fmt.Sprintf("assertion:%d", i),
				assertion.Loc.String(),
				reason,
				"Use an assertion form documented for generated Vitest, or move this expectation into a hand-written test.",
			)
		}
	}

	if body != nil && !hasFixtureRequestValue {
		if endpoint := idx.endpointForTarget(test.Target); endpoint != nil {
			for _, field := range multipartBodyFields(endpoint, body) {
				add(
					"multipart-field:"+field,
					body.Location().String(),
					fmt.Sprintf("body field %q targets a file input and would be sent as JSON instead of multipart data", field),
					"Construct FormData in a hand-written Vitest test until authored multipart requests are implemented.",
				)
			}
		}
	}

	for _, fn := range idx.nativeFunctionsLoadedByApp() {
		message, hint, incomplete := nativeFunctionRequirement(fn, idx.outDir)
		if !incomplete {
			continue
		}
		add("native:"+fn.Name, fn.Impl.Loc.String(), message, hint)
	}

	return issues
}

func (idx *nodeTestPreflightIndex) unsupportedSetupWrite(call *ast.FnCall, bindings map[string]bool) string {
	if !idx.hasDatabase {
		return fmt.Sprintf("setup %s requires a declared blueprint database", call.Name)
	}
	if len(call.Args) != 2 {
		return fmt.Sprintf("setup %s expects exactly a model and an object body", call.Name)
	}
	model, ok := call.Args[0].(*ast.Ident)
	if !ok || idx.models[model.Name] == nil {
		return fmt.Sprintf("setup %s must name a declared model as its first argument", call.Name)
	}
	body, ok := call.Args[1].(*ast.BlockExpr)
	if !ok {
		return fmt.Sprintf("setup %s body must be an object literal", call.Name)
	}
	for _, entry := range body.Entries {
		if reason := unsupportedTestValue(entry.Value, bindings, true); reason != "" {
			return fmt.Sprintf("setup %s field %q %s", call.Name, entry.Key, reason)
		}
	}
	return ""
}

// unsupportedTestValue is the deliberately small expression boundary shared
// by setup, request bodies, and auth values. The authored test file imports no
// Blueprint functions, pipes, env object, or runtime helpers, so accepting an
// arbitrary expression here would turn a preflight success into a TS error (or
// an undefined value) later.
func unsupportedTestValue(expr ast.Expr, bindings map[string]bool, aggregates bool) string {
	switch value := expr.(type) {
	case *ast.StringLit:
		if strings.ContainsAny(value.Value, "{}") {
			return "uses string interpolation, whose names are not resolved in authored test files"
		}
		return ""
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.NullLit,
		*ast.NowLit, *ast.DurationLit, *ast.SizeLit, *ast.RateLit, *ast.PathExpr:
		return ""
	case *ast.Ident:
		if bindings[value.Name] {
			return ""
		}
		return fmt.Sprintf("references %q, which is not a supported setup binding", value.Name)
	case *ast.FieldAccess:
		if reason := unsupportedTestValue(value.Base, bindings, false); reason != "" {
			return reason
		}
		return ""
	case *ast.ParenExpr:
		return unsupportedTestValue(value.Expr, bindings, aggregates)
	case *ast.UnaryExpr:
		return unsupportedTestValue(value.Operand, bindings, false)
	case *ast.BinaryExpr:
		if reason := unsupportedTestValue(value.Left, bindings, false); reason != "" {
			return reason
		}
		return unsupportedTestValue(value.Right, bindings, false)
	case *ast.ListExpr:
		if !aggregates {
			return "uses an array where a scalar value is required"
		}
		for _, element := range value.Elements {
			if reason := unsupportedTestValue(element, bindings, true); reason != "" {
				return reason
			}
		}
		return ""
	case *ast.BlockExpr:
		if !aggregates {
			return "uses an object where a scalar value is required"
		}
		for _, entry := range value.Entries {
			if reason := unsupportedTestValue(entry.Value, bindings, true); reason != "" {
				return reason
			}
		}
		return ""
	case *ast.FnCall:
		return fmt.Sprintf("calls %q, but authored test files do not import callable expressions", value.Name)
	case *ast.IndexAccess:
		return "uses bracket access, which is outside the authored-test value allowlist"
	default:
		return fmt.Sprintf("uses unsupported expression %T", expr)
	}
}

func (idx *nodeTestPreflightIndex) unsupportedAssertion(assertion *ast.Assertion, hasBodyAssertion bool) string {
	fields := tokenizePreflightAssertion(assertion.Raw)
	if len(fields) == 0 || fields[0] != assertion.Kind {
		return fmt.Sprintf("assertion %q cannot be parsed faithfully", assertion.Raw)
	}
	switch assertion.Kind {
	case "status", "last_status":
		if len(fields) != 2 || !validTestStatus(fields[1]) {
			return fmt.Sprintf("status assertion %q must contain exactly an HTTP status (100-599) or 1xx-5xx class", assertion.Raw)
		}
		return ""
	case "body":
		path, op, rhs, ok := parseSimpleValueAssertion(fields, "body")
		if !ok || len(path) < 2 {
			return fmt.Sprintf("body assertion %q is not a supported field assertion", assertion.Raw)
		}
		switch op {
		case "exists", "not_exists":
			if rhs != "" {
				return fmt.Sprintf("body assertion %q has trailing tokens", assertion.Raw)
			}
		case "is":
			if !supportedTestType(rhs) {
				return fmt.Sprintf("body type assertion %q uses an unsupported type", assertion.Raw)
			}
		case "==", "!=":
			if !safeAssertionLiteral(rhs) {
				return fmt.Sprintf("body equality assertion %q must compare with one literal value", assertion.Raw)
			}
		default:
			return fmt.Sprintf("body assertion %q uses an unsupported operator", assertion.Raw)
		}
		return ""
	case "header":
		path, op, rhs, ok := parseSimpleValueAssertion(fields, "header")
		if !ok || len(path) != 2 || op != "==" || !safeHeaderName(path[1]) || !safeAssertionLiteral(rhs) {
			return fmt.Sprintf("header assertion %q must be header.Name == literal with a simple header name", assertion.Raw)
		}
		return ""
	case "model":
		return idx.unsupportedModelAssertion(assertion.Raw, fields, hasBodyAssertion)
	case "duration":
		return fmt.Sprintf("timing assertion %q is emitted only as a TODO comment", assertion.Raw)
	default:
		return fmt.Sprintf("assertion kind %q is not emitted as an executable Vitest expectation", assertion.Kind)
	}
}

func tokenizePreflightAssertion(raw string) []string {
	var tokens []string
	for i := 0; i < len(raw); {
		if raw[i] == ' ' || raw[i] == '\t' {
			i++
			continue
		}
		if raw[i] == '"' {
			j := i + 1
			for j < len(raw) && raw[j] != '"' {
				if raw[j] == '\\' && j+1 < len(raw) {
					j++
				}
				j++
			}
			if j < len(raw) {
				j++
			}
			tokens = append(tokens, raw[i:j])
			i = j
			continue
		}
		j := i
		for j < len(raw) && raw[j] != ' ' && raw[j] != '\t' {
			j++
		}
		tokens = append(tokens, raw[i:j])
		i = j
	}
	return tokens
}

func parseSimpleValueAssertion(fields []string, root string) (path []string, op, rhs string, ok bool) {
	if len(fields) < 2 || fields[0] != root {
		return nil, "", "", false
	}
	path = []string{root}
	i := 1
	for i < len(fields) && fields[i] == "." {
		if i+1 >= len(fields) || !simpleAssertionSegment(fields[i+1]) {
			return nil, "", "", false
		}
		path = append(path, fields[i+1])
		i += 2
	}
	if i >= len(fields) {
		return nil, "", "", false
	}
	op = fields[i]
	i++
	switch op {
	case "exists":
		return path, op, "", i == len(fields)
	case "not":
		if i < len(fields) && fields[i] == "exists" && i+1 == len(fields) {
			return path, "not_exists", "", true
		}
		return nil, "", "", false
	case "is", "==", "!=":
		if i+1 != len(fields) {
			return nil, "", "", false
		}
		return path, op, fields[i], true
	default:
		return nil, "", "", false
	}
}

func simpleAssertionSegment(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validTestStatus(value string) bool {
	if len(value) == 3 && value[1:] == "xx" && value[0] >= '1' && value[0] <= '5' {
		return true
	}
	status, err := strconv.Atoi(value)
	return err == nil && status >= 100 && status <= 599 && strconv.Itoa(status) == value
}

func supportedTestType(value string) bool {
	switch value {
	case "string", "uuid", "email", "url", "number", "int", "float", "bool", "boolean":
		return true
	}
	return false
}

func supportedConsoleLevel(value string) bool {
	switch value {
	case "debug", "info", "log", "warn", "error":
		return true
	}
	return false
}

func safeAssertionLiteral(value string) bool {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		inner := value[1 : len(value)-1]
		// emitTestAssertion converts these tokens to a single-quoted JS literal
		// without escaping. Reject values that would terminate/change that
		// literal or create an invalid multiline/trailing-escape token.
		return !strings.ContainsAny(inner, "'\\\n\r")
	}
	if value == "true" || value == "false" || value == "null" {
		return true
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return true
	}
	return false
}

func safeHeaderName(value string) bool {
	// The current assertion emitter does not reassemble lexer-separated hyphens,
	// so accept only the name shape it can reproduce exactly.
	return simpleAssertionSegment(value)
}

func (idx *nodeTestPreflightIndex) unsupportedModelAssertion(raw string, fields []string, hasBodyAssertion bool) string {
	if !idx.hasDatabase {
		return fmt.Sprintf("model assertion %q requires a declared blueprint database", raw)
	}
	if len(fields) < 8 || fields[0] != "model" {
		return fmt.Sprintf("model assertion %q is malformed", raw)
	}
	model := idx.models[fields[1]]
	if model == nil {
		return fmt.Sprintf("model assertion %q names undeclared model %q", raw, fields[1])
	}
	if fields[2] != "where" || fields[3] != "(" {
		return fmt.Sprintf("model assertion %q must use where(...) with at least one equality", raw)
	}
	close := -1
	for i := 4; i < len(fields); i++ {
		if fields[i] == ")" {
			close = i
			break
		}
	}
	if close <= 4 {
		return fmt.Sprintf("model assertion %q has an empty or unterminated where clause", raw)
	}
	if !((close+2 == len(fields) && fields[close+1] == "exists") ||
		(close+3 == len(fields) && fields[close+1] == "not" && fields[close+2] == "exists")) {
		return fmt.Sprintf("model assertion %q must end with exists or not exists", raw)
	}
	modelFields := make(map[string]bool, len(model.Fields))
	for _, field := range model.Fields {
		modelFields[field.Name] = true
	}
	conditions := splitAssertionConditions(fields[4:close])
	if len(conditions) == 0 {
		return fmt.Sprintf("model assertion %q has no conditions", raw)
	}
	for _, condition := range conditions {
		if len(condition) < 3 || !simpleAssertionSegment(condition[0]) || !modelFields[condition[0]] || condition[1] != "==" {
			return fmt.Sprintf("model assertion %q must use declared fields with ==", raw)
		}
		rhs := condition[2:]
		if len(rhs) == 1 && safeAssertionLiteral(rhs[0]) {
			continue
		}
		if len(rhs) >= 3 && rhs[0] == "body" && hasBodyAssertion && validAssertionPathTokens(rhs) {
			continue
		}
		return fmt.Sprintf("model assertion %q has an unsupported right-hand value", raw)
	}
	return ""
}

func splitAssertionConditions(fields []string) [][]string {
	var conditions [][]string
	start := 0
	for i, field := range fields {
		if field != "," {
			continue
		}
		if i == start {
			return nil
		}
		conditions = append(conditions, fields[start:i])
		start = i + 1
	}
	if start >= len(fields) {
		return nil
	}
	return append(conditions, fields[start:])
}

func validAssertionPathTokens(fields []string) bool {
	if len(fields) < 3 || !simpleAssertionSegment(fields[0]) {
		return false
	}
	for i := 1; i < len(fields); i += 2 {
		if i+1 >= len(fields) || fields[i] != "." || !simpleAssertionSegment(fields[i+1]) {
			return false
		}
	}
	return true
}

func unsupportedTestAuth(expr ast.Expr) string {
	call, ok := expr.(*ast.FnCall)
	if !ok {
		return "auth must use a supported auth helper; the current value would be ignored"
	}
	switch call.Name {
	case "api_key", "bearer", "jwt", "basic":
		if len(call.Args) != 1 {
			return fmt.Sprintf("auth %s expects exactly one value; this form produces an invalid or incomplete header", call.Name)
		}
		return ""
	default:
		return fmt.Sprintf("auth scheme %q is not emitted by the authored-test generator", call.Name)
	}
}

func testPathHasPlaceholder(path string) bool {
	for _, segment := range strings.Split(testTargetPathOnly(path), "/") {
		if strings.HasPrefix(segment, ":") && len(segment) > 1 {
			return true
		}
	}
	return false
}

func fixtureCallName(call *ast.FnCall) string {
	if len(call.Args) != 1 {
		return ""
	}
	if name, ok := call.Args[0].(*ast.StringLit); ok {
		return name.Value
	}
	return ""
}

func (idx *nodeTestPreflightIndex) endpointForTarget(target *ast.TestTarget) *ast.Endpoint {
	if target == nil {
		return nil
	}
	for _, endpoint := range idx.endpoints {
		if !strings.EqualFold(endpoint.Method, target.Method) {
			continue
		}
		if endpointPathMatches(endpoint.Path, target.Path) {
			return endpoint
		}
	}
	return nil
}

func endpointPathMatches(endpointPath, targetPath string) bool {
	endpointParts := strings.Split(strings.Trim(endpointPath, "/"), "/")
	targetParts := strings.Split(strings.Trim(testTargetPathOnly(targetPath), "/"), "/")
	if len(endpointParts) != len(targetParts) {
		return false
	}
	for i := range endpointParts {
		if strings.HasPrefix(endpointParts[i], ":") {
			if targetParts[i] == "" {
				return false
			}
			continue
		}
		if endpointParts[i] != targetParts[i] {
			return false
		}
	}
	return true
}

func testTargetPathOnly(target string) string {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		return target[:i]
	}
	return target
}

func multipartBodyFields(endpoint *ast.Endpoint, body ast.Expr) []string {
	block, ok := body.(*ast.BlockExpr)
	if !ok {
		return nil
	}
	fileInputs := make(map[string]bool)
	for _, stmt := range endpoint.Stmts {
		input, ok := stmt.(*ast.InputStmt)
		if !ok {
			continue
		}
		switch typ := input.Type.(type) {
		case *ast.MimeTypeExpr:
			fileInputs[input.Name] = true
		case *ast.PrimitiveType:
			if typ.Name == "file" {
				fileInputs[input.Name] = true
			}
		}
	}
	var fields []string
	for _, entry := range block.Entries {
		if fileInputs[entry.Key] {
			fields = append(fields, entry.Key)
		}
	}
	return fields
}

type nodeCallCollector struct {
	ast.BaseVisitor
	calls []*ast.FnCall
}

func (c *nodeCallCollector) VisitFnCall(call *ast.FnCall) bool {
	c.calls = append(c.calls, call)
	return true
}

func callsInNode(node ast.Node) []*ast.FnCall {
	if node == nil {
		return nil
	}
	collector := &nodeCallCollector{}
	ast.Walk(node, collector)
	return collector.calls
}

func callsInStatements(stmts []ast.ArrowStmt) []*ast.FnCall {
	collector := &nodeCallCollector{}
	for _, stmt := range stmts {
		ast.Walk(stmt, collector)
	}
	return collector.calls
}

// nativeFunctionsLoadedByApp follows declared fn/pipe calls from every module
// imported by src/index.ts. Vitest imports the whole generated app, not only
// the target endpoint, so a missing native dependency in a sibling route,
// worker, schedule, subscription, stream, or websocket can fail the suite at
// module-load time before the authored request runs.
func (idx *nodeTestPreflightIndex) nativeFunctionsLoadedByApp() []*ast.Fn {
	var pending []*ast.FnCall
	middlewareNames := make(map[string]bool)
	collectMeta := func(meta []*ast.EndpointMeta) {
		for _, entry := range meta {
			if entry.Kind == "use" && entry.Use != nil {
				middlewareNames[entry.Use.Name] = true
			}
		}
	}
	if idx.file.Blueprint != nil {
		for _, use := range idx.file.Blueprint.Uses {
			middlewareNames[use.Name] = true
		}
	}
	for _, block := range idx.file.Blocks {
		switch node := block.(type) {
		case *ast.Endpoint:
			pending = append(pending, callsInStatements(node.Stmts)...)
			collectMeta(node.Meta)
		case *ast.StreamEndpoint:
			pending = append(pending, callsInStatements(node.Stmts)...)
			for _, handler := range node.Handlers {
				pending = append(pending, callsInStatements(handler.Body)...)
			}
			collectMeta(node.Meta)
		case *ast.WsEndpoint:
			pending = append(pending, callsInStatements(node.OnConnect)...)
			pending = append(pending, callsInStatements(node.OnMessage)...)
			pending = append(pending, callsInStatements(node.OnDisconnect)...)
			collectMeta(node.Meta)
		case *ast.Worker:
			pending = append(pending, callsInStatements(node.Stmts)...)
			pending = append(pending, callsInStatements(node.OnFail)...)
		case *ast.Schedule:
			pending = append(pending, callsInStatements(node.Stmts)...)
		case *ast.Subscribe:
			pending = append(pending, callsInStatements(node.Stmts)...)
		}
	}
	for name := range middlewareNames {
		if middleware := idx.middlewares[name]; middleware != nil {
			pending = append(pending, callsInStatements(middleware.Before)...)
			pending = append(pending, callsInStatements(middleware.After)...)
		}
	}

	seenFunctions := make(map[string]bool)
	seenPipes := make(map[string]bool)
	var native []*ast.Fn
	for len(pending) > 0 {
		call := pending[0]
		pending = pending[1:]
		if fn := idx.functions[call.Name]; fn != nil {
			if seenFunctions[fn.Name] {
				continue
			}
			seenFunctions[fn.Name] = true
			if fn.Impl != nil {
				switch fn.Impl.Strategy {
				case "node", "exec":
					native = append(native, fn)
				}
			}
			if fn.Logic != nil {
				pending = append(pending, callsInStatements(fn.Logic.Stmts)...)
			}
			continue
		}
		if pipe := idx.pipes[call.Name]; pipe != nil {
			if seenPipes[pipe.Name] {
				continue
			}
			seenPipes[pipe.Name] = true
			pending = append(pending, callsInStatements(pipe.Stmts)...)
		}
	}
	return native
}

var unimplementedNativeFunction = regexp.MustCompile(`throw\s+new\s+Error\(\s*["']Not implemented:`)

func nativeFunctionRequirement(fn *ast.Fn, outDir string) (message, hint string, incomplete bool) {
	switch fn.Impl.Strategy {
	case "exec":
		command := implEntryString(fn.Impl, "cmd")
		if command == "" {
			return fmt.Sprintf("fn %q uses impl exec without a command", fn.Name), "Set impl exec cmd to an executable available to the generated test process.", true
		}
		available, safe := execCommandAvailable(command, outDir)
		if !safe {
			return fmt.Sprintf("fn %q uses exec asset %q outside the generated project", fn.Name, command),
				"Use a PATH command or a relative executable that stays under the generated output directory.", true
		}
		if available {
			return "", "", false
		}
		return fmt.Sprintf("fn %q requires exec asset %q, which is not available to the generated test process", fn.Name, command),
			fmt.Sprintf("Install the command on PATH or place the relative executable under %s before running bp test.", outDir), true
	case "node":
		module := implEntryString(fn.Impl, "module")
		if module == "" {
			return fmt.Sprintf("fn %q uses impl node without a module", fn.Name), "Set module to a user-owned implementation; ./internal/... modules receive a scaffold from bp build.", true
		}

		if path, local, safe := localNativeImplementationPath(module, outDir); local {
			if !safe {
				return fmt.Sprintf("fn %q uses local native module %q outside the generated implementation directory", fn.Name, module),
					"Use a normalized ./internal/... module path without parent-directory traversal.", true
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return fmt.Sprintf("target execution reaches fn %q, whose native implementation is missing at %s", fn.Name, path),
					fmt.Sprintf("Run bp build first, implement %s, then rerun bp test.", path), true
			}
			if unimplementedNativeFunction.Match(body) {
				return fmt.Sprintf("target execution reaches fn %q, whose scaffold at %s still throws Not implemented", fn.Name, path),
					fmt.Sprintf("Implement %s before running this authored test.", path), true
			}
			return "", "", false
		}

		available, safe := nodeModuleAvailable(module, outDir)
		if !safe {
			return fmt.Sprintf("fn %q uses native module %q outside the generated project", fn.Name, module),
				"Use a package import or a relative module that stays under src/functions.", true
		}
		if available {
			return "", "", false
		}
		return fmt.Sprintf("the generated app loads fn %q, whose emitted native module %q is not present in the generated project", fn.Name, generatedNativeModule(module)),
			"Provide the module in the generated project, or use module: \"./internal/...\" and implement Blueprint's user-owned scaffold before bp test.", true
	}
	return "", "", false
}

func implEntryString(impl *ast.ImplBlock, key string) string {
	for _, entry := range impl.Entries {
		if entry.Key != key {
			continue
		}
		switch value := entry.Value.(type) {
		case *ast.StringLit:
			return value.Value
		case *ast.Ident:
			return value.Name
		}
	}
	return ""
}

func localNativeImplementationPath(module, outDir string) (path string, local, safe bool) {
	raw := strings.TrimSuffix(module, ".js")
	if !strings.HasPrefix(raw, "./internal/") {
		return "", false, true
	}
	rel := strings.TrimPrefix(raw, "./")
	base := filepath.Join(outDir, "src", "impl", "functions")
	path, safe = pathWithin(base, filepath.FromSlash(rel)+".ts")
	return path, true, safe
}

func generatedNativeModule(module string) string {
	if !strings.HasSuffix(module, ".js") {
		return module + ".js"
	}
	return module
}

func nodeModuleAvailable(module, outDir string) (available, safe bool) {
	emitted := generatedNativeModule(module)
	if strings.HasPrefix(emitted, "node:") || strings.HasSuffix(module, ".ts") {
		// nativeImplModulePaths appends .js to extensionless/node:/.ts values;
		// those emitted specifiers do not name the originally requested module.
		return false, true
	}
	if filepath.IsAbs(emitted) {
		return false, false
	}
	if strings.HasPrefix(emitted, "./") || strings.HasPrefix(emitted, "../") {
		candidate, ok := pathWithin(filepath.Join(outDir, "src", "functions"), filepath.FromSlash(emitted))
		if !ok {
			return false, false
		}
		return sourceModuleExists(strings.TrimSuffix(candidate, ".js")), true
	}
	if strings.Contains(emitted, `\`) {
		return false, false
	}
	parts := strings.Split(emitted, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false, false
		}
	}
	packagePath := emitted
	if strings.HasPrefix(emitted, "@") && len(parts) >= 2 {
		packagePath = filepath.Join(parts[0], parts[1])
	} else if len(parts) > 0 {
		packagePath = parts[0]
	}
	packageRoot, ok := pathWithin(filepath.Join(outDir, "node_modules"), packagePath)
	if !ok {
		return false, false
	}
	_, err := os.Stat(packageRoot)
	return err == nil, true
}

func sourceModuleExists(base string) bool {
	candidates := []string{base, base + ".ts", base + ".js", filepath.Join(base, "index.ts"), filepath.Join(base, "index.js")}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func execCommandAvailable(command, outDir string) (available, safe bool) {
	if strings.ContainsAny(command, `/\\`) {
		candidate := command
		if filepath.IsAbs(candidate) {
			if !isPathWithin(outDir, candidate) {
				return false, false
			}
		} else {
			var ok bool
			candidate, ok = pathWithin(outDir, filepath.FromSlash(command))
			if !ok {
				return false, false
			}
		}
		info, err := os.Stat(candidate)
		return err == nil && !info.IsDir() && info.Mode()&0o111 != 0, true
	}
	_, err := exec.LookPath(command)
	return err == nil, true
}

func pathWithin(base, relative string) (string, bool) {
	if filepath.IsAbs(relative) {
		return "", false
	}
	candidate := filepath.Join(base, relative)
	if !isPathWithin(base, candidate) {
		return "", false
	}
	return candidate, true
}

func isPathWithin(base, candidate string) bool {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, candidateAbs)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
