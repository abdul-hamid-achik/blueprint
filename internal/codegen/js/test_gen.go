package js

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// --- Tests ---

func (g *Generator) genTest(t *ast.Test, fixtures []*ast.Fixture) codegen.OutputFile {
	var b strings.Builder

	// Build fixture lookup map.
	fixtureMap := make(map[string]*ast.Fixture)
	for _, f := range fixtures {
		fixtureMap[f.Name] = f
	}

	// Collect setup bindings that need hoisting to describe scope so they're
	// accessible in both beforeAll and the it() body.
	hoistedVarNames := make(map[string]bool)
	var hoistedVarList []string
	for _, stmt := range t.Setup {
		if step, ok := stmt.(*ast.StepStmt); ok && step.Binding != "" {
			name := toCamelCase(step.Binding)
			if !hoistedVarNames[name] {
				hoistedVarNames[name] = true
				hoistedVarList = append(hoistedVarList, name)
			}
		}
	}

	// Detect if any from-path fixtures are referenced in the request body.
	needsFSImport := false
	if t.Request != nil {
		for _, kv := range t.Request.Entries {
			if kv.Key == "body" && testExprUsesFixture(kv.Value, fixtureMap, true) {
				needsFSImport = true
			}
		}
	}

	// Check assertion kinds so we know whether to parse the response body.
	hasBodyAssertions := false
	hasLastStatusAssertions := false
	needsDbImport := stmtsHaveDataOps(t.Setup) || stmtsHaveDataOps(t.Cleanup)
	for _, a := range t.Expect {
		if a.Kind == "body" {
			hasBodyAssertions = true
		}
		if a.Kind == "last_status" {
			hasLastStatusAssertions = true
		}
		if a.Kind == "model" {
			needsDbImport = true
		}
	}

	// Imports
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { describe, it, expect, beforeAll } from 'vitest';\n")
	if needsFSImport {
		b.WriteString("import { readFileSync } from 'fs';\n")
		b.WriteString("import { join } from 'path';\n")
	}
	if needsDbImport {
		b.WriteString("import { db } from '../src/lib/db.js';\n")
		b.WriteString("import * as schema from '../src/models/schema.js';\n")
		b.WriteString("import { eq, and } from 'drizzle-orm';\n")
	}
	b.WriteString("import app from '../src/index.js';\n\n")

	name := toCamelCase(t.Name)
	if t.Intent != nil {
		fmt.Fprintf(&b, "// %s\n", t.Intent.Text)
	}
	fmt.Fprintf(&b, "describe('%s', () => {\n", name)

	// Hoist setup variables to describe scope.
	for _, v := range hoistedVarList {
		fmt.Fprintf(&b, "  let %s: any;\n", v)
	}
	if len(hoistedVarList) > 0 {
		b.WriteString("\n")
	}

	// Setup — pre-populate ctx.declared with hoisted names so emitArrowStmts
	// emits plain assignments instead of const declarations.
	if len(t.Setup) > 0 {
		ctx := emitCtx{
			kind:       "function",
			declared:   hoistedVarNames,
			boundVars:  make(map[string]string),
			varModels:  make(map[string]string),
			singleVars: make(map[string]bool),
		}
		b.WriteString("  beforeAll(async () => {\n")
		g.emitArrowStmts(&b, t.Setup, "    ", ctx)
		b.WriteString("  });\n\n")
	}

	// Repeat wrapper.
	repeat := 1
	if t.Request != nil && t.Request.Repeat > 1 {
		repeat = t.Request.Repeat
	}
	fmt.Fprintf(&b, "  it('%s', async () => {\n", t.Name)
	indent := "    "
	if repeat > 1 {
		if hasLastStatusAssertions {
			b.WriteString("    let lastRes: any;\n")
		}
		fmt.Fprintf(&b, "    for (let _i = 0; _i < %d; _i++) {\n", repeat)
		indent = "      "
	}

	// HTTP request.
	if t.Target != nil {
		method := strings.ToUpper(t.Target.Method)
		fmt.Fprintf(&b, "%sconst res = await app.request('%s', {\n", indent, t.Target.Path)
		fmt.Fprintf(&b, "%s  method: '%s',\n", indent, method)

		if t.Request != nil {
			// Auth header (e.g. auth api_key(key.key_hash) → X-API-Key header).
			for _, kv := range t.Request.Entries {
				if kv.Key == "auth" {
					if hdr := testAuthHeader(kv.Value); hdr != "" {
						fmt.Fprintf(&b, "%s  headers: %s,\n", indent, hdr)
					}
				}
			}
			// Request body with fixture() resolution.
			for _, kv := range t.Request.Entries {
				if kv.Key == "body" {
					fmt.Fprintf(&b, "%s  body: JSON.stringify(%s),\n", indent, testResolveExpr(kv.Value, fixtureMap))
				}
			}
		}
		fmt.Fprintf(&b, "%s});\n", indent)
		if repeat > 1 && hasLastStatusAssertions {
			fmt.Fprintf(&b, "%slastRes = res;\n", indent)
		}
	}

	// Parse response body once if any body assertions exist.
	if hasBodyAssertions {
		fmt.Fprintf(&b, "%sconst body = await res.json() as any;\n", indent)
	}

	// Assertions.
	for _, a := range t.Expect {
		if repeat > 1 && a.Kind == "last_status" {
			continue
		}
		emitTestAssertion(&b, a, indent)
	}

	if repeat > 1 {
		b.WriteString("    }\n")
		for _, a := range t.Expect {
			if a.Kind == "last_status" {
				emitLastStatusAssertion(&b, a, "    ", "lastRes")
			}
		}
	}
	b.WriteString("  });\n")
	b.WriteString("});\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("test/%s.test.ts", toKebabCase(t.Name)),
		Content: []byte(b.String()),
	}
}

// testExprUsesFixture reports whether expr contains a fixture() call that
// references a from-path fixture (requiresFSImport=true) or any fixture.
func testExprUsesFixture(expr ast.Expr, fixtures map[string]*ast.Fixture, requiresFSImport bool) bool {
	switch v := expr.(type) {
	case *ast.FnCall:
		if v.Name == "fixture" && len(v.Args) == 1 {
			if s, ok := v.Args[0].(*ast.StringLit); ok {
				if f, ok := fixtures[s.Value]; ok {
					if requiresFSImport {
						return f.FromPath != ""
					}
					return true
				}
			}
			return !requiresFSImport
		}
		for _, a := range v.Args {
			if testExprUsesFixture(a, fixtures, requiresFSImport) {
				return true
			}
		}
	case *ast.BlockExpr:
		for _, kv := range v.Entries {
			if testExprUsesFixture(kv.Value, fixtures, requiresFSImport) {
				return true
			}
		}
	}
	return false
}

// testResolveExpr converts an expression to JS, resolving fixture() calls.
func testResolveExpr(expr ast.Expr, fixtures map[string]*ast.Fixture) string {
	switch v := expr.(type) {
	case *ast.FnCall:
		if v.Name == "fixture" && len(v.Args) == 1 {
			if s, ok := v.Args[0].(*ast.StringLit); ok {
				if f, ok := fixtures[s.Value]; ok {
					if f.FromPath != "" {
						return fmt.Sprintf("readFileSync(join(import.meta.dirname, '..', '%s'))", f.FromPath)
					}
					if f.Generated != nil {
						// Extract size if present (e.g. size: 15mb → Buffer.alloc(15 * 1024 * 1024)).
						for _, kv := range f.Generated.Entries {
							if kv.Key == "size" {
								sizeExpr := exprToJS(kv.Value)
								return fmt.Sprintf("((_s) => { if (_s <= 0) throw new RangeError('Buffer size must be positive'); return Buffer.alloc(_s); })(%s)", sizeExpr)
							}
						}
						return "Buffer.alloc(0)"
					}
				}
				return fmt.Sprintf("null /* fixture %q not found */", s.Value)
			}
		}
		// Regular function call — resolve args recursively.
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = testResolveExpr(a, fixtures)
		}
		return fmt.Sprintf("%s(%s)", toCamelCase(v.Name), strings.Join(args, ", "))
	case *ast.BlockExpr:
		parts := make([]string, 0, len(v.Entries))
		for _, kv := range v.Entries {
			parts = append(parts, fmt.Sprintf("%s: %s", kv.Key, testResolveExpr(kv.Value, fixtures)))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	default:
		return exprToJS(expr)
	}
}

// testAuthHeader generates a JS headers object for a test auth entry.
// e.g. auth api_key(key.key_hash) → { 'X-API-Key': key.keyHash }
func testAuthHeader(authExpr ast.Expr) string {
	fn, ok := authExpr.(*ast.FnCall)
	if !ok {
		return ""
	}
	argJS := ""
	if len(fn.Args) > 0 {
		argJS = exprToJS(fn.Args[0])
	}
	switch fn.Name {
	case "api_key":
		return fmt.Sprintf("{ 'X-API-Key': %s }", argJS)
	case "bearer", "jwt":
		return fmt.Sprintf("{ 'Authorization': `Bearer ${%s}` }", argJS)
	case "basic":
		return fmt.Sprintf("{ 'Authorization': `Basic ${%s}` }", argJS)
	}
	return ""
}

// tokenizeAssertion splits an assertion raw string into tokens, keeping quoted
// strings (including those with spaces) as single tokens. This prevents fragile
// whitespace-based splitting from breaking assertions like: status == "hello world"
func tokenizeAssertion(raw string) []string {
	var tokens []string
	i := 0
	for i < len(raw) {
		// Skip whitespace
		if raw[i] == ' ' || raw[i] == '\t' {
			i++
			continue
		}
		// Quoted string — collect until closing quote
		if raw[i] == '"' {
			j := i + 1
			for j < len(raw) && raw[j] != '"' {
				if raw[j] == '\\' && j+1 < len(raw) {
					j++ // skip escaped char
				}
				j++
			}
			if j < len(raw) {
				j++ // include closing quote
			}
			tokens = append(tokens, raw[i:j])
			i = j
			continue
		}
		// Regular token — collect until whitespace
		j := i
		for j < len(raw) && raw[j] != ' ' && raw[j] != '\t' {
			j++
		}
		tokens = append(tokens, raw[i:j])
		i = j
	}
	return tokens
}

// emitTestAssertion emits a single expect() call for a test assertion.
func emitTestAssertion(b *strings.Builder, a *ast.Assertion, indent string) {
	fields := tokenizeAssertion(a.Raw)
	switch a.Kind {
	case "status":
		// "status 200" — second field is the numeric code.
		if len(fields) >= 2 {
			emitStatusExpectation(b, indent, "res.status", fields[1])
		}
	case "body":
		// Patterns: body . field is type  /  body . field == value  /  body . field exists
		path, op, rhs := parseAssertionFields(fields)
		if path == "" {
			fmt.Fprintf(b, "%s// TODO: assert %s\n", indent, a.Raw)
			return
		}
		jsPath := assertionPathToJS(path)
		switch op {
		case "is":
			emitTypeExpect(b, jsPath, rhs, indent)
		case "==":
			fmt.Fprintf(b, "%sexpect(%s).toBe(%s);\n", indent, jsPath, rhs)
		case "!=":
			fmt.Fprintf(b, "%sexpect(%s).not.toBe(%s);\n", indent, jsPath, rhs)
		case "exists":
			fmt.Fprintf(b, "%sexpect(%s).toBeDefined();\n", indent, jsPath)
		case "not_exists":
			fmt.Fprintf(b, "%sexpect(%s).toBeUndefined();\n", indent, jsPath)
		default:
			fmt.Fprintf(b, "%s// TODO: assert %s\n", indent, a.Raw)
		}
	case "header":
		// header . Name == value
		path, op, rhs := parseAssertionFields(fields)
		if path != "" && op == "==" {
			parts := strings.SplitN(path, ".", 2)
			headerName := ""
			if len(parts) == 2 {
				headerName = parts[1]
			}
			fmt.Fprintf(b, "%sexpect(res.headers.get('%s')).toBe(%s);\n", indent, headerName, rhs)
		} else {
			fmt.Fprintf(b, "%s// TODO: assert %s\n", indent, a.Raw)
		}
	case "model":
		// model job where ( id == body . job_id , status == "done" ) exists
		emitModelAssertion(b, fields, indent)
	case "duration":
		// duration < 500ms  — timing assertion (approximate)
		fmt.Fprintf(b, "%s// TODO: assert %s\n", indent, a.Raw)
	case "last_status":
		emitLastStatusAssertion(b, a, indent, "res")
	default:
		fmt.Fprintf(b, "%s// TODO: assert %s\n", indent, a.Raw)
	}
}

func emitLastStatusAssertion(b *strings.Builder, a *ast.Assertion, indent, responseVar string) {
	fields := tokenizeAssertion(a.Raw)
	if len(fields) >= 2 {
		emitStatusExpectation(b, indent, responseVar+".status", fields[1])
		return
	}
	fmt.Fprintf(b, "%s// TODO: assert %s\n", indent, a.Raw)
}

func emitStatusExpectation(b *strings.Builder, indent, statusExpr, want string) {
	if len(want) == 3 && want[1:] == "xx" && want[0] >= '1' && want[0] <= '5' {
		lower := int(want[0]-'0') * 100
		fmt.Fprintf(b, "%sexpect(%s).toBeGreaterThanOrEqual(%d);\n", indent, statusExpr, lower)
		fmt.Fprintf(b, "%sexpect(%s).toBeLessThan(%d);\n", indent, statusExpr, lower+100)
		return
	}
	fmt.Fprintf(b, "%sexpect(%s).toBe(%s);\n", indent, statusExpr, want)
}

func assertionPathToJS(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return path
	}
	for i := range parts {
		if i == 0 {
			continue
		}
		if !isValidJSIdentifier(parts[i]) {
			parts[i] = fmt.Sprintf("[%q]", parts[i])
		} else {
			parts[i] = "." + parts[i]
		}
	}
	return parts[0] + strings.Join(parts[1:], "")
}

func isValidJSIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && r != '$' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && r != '$' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// emitModelAssertion emits a db query assertion for model existence checks.
// Raw format: "model job where ( id == body . job_id , status == \"done\" ) exists"
// On parse failure, emits expect(true).toBe(false) so tests fail loudly.
func emitModelAssertion(b *strings.Builder, fields []string, indent string) {
	raw := strings.Join(fields, " ")

	// fields[0] == "model", fields[1] == model name
	if len(fields) < 2 {
		fmt.Fprintf(b, "%sexpect(true).toBe(false); // PARSE ERROR: could not parse model assertion: %s\n", indent, raw)
		return
	}
	modelName := fields[1]

	// Find where-clause: scan for "(" and ")"
	start := -1
	end_ := -1
	for i, f := range fields {
		if f == "(" && start == -1 {
			start = i + 1
		}
		if f == ")" {
			end_ = i
		}
	}

	// Validate: must have matching parens with content
	if start < 0 || end_ < 0 || end_ <= start {
		fmt.Fprintf(b, "%sexpect(true).toBe(false); // PARSE ERROR: missing or empty where clause in model assertion: %s\n", indent, raw)
		return
	}

	// Determine exists/not_exists — last meaningful token after ")"
	exists := true
	tailValid := false
	if end_+1 < len(fields) {
		tail := fields[end_+1]
		switch tail {
		case "exists":
			tailValid = true
		case "not":
			exists = false
			tailValid = true
		}
	}
	if !tailValid {
		fmt.Fprintf(b, "%sexpect(true).toBe(false); // PARSE ERROR: missing exists/not after where in model assertion: %s\n", indent, raw)
		return
	}

	// Parse conditions between ( and )
	condFields := fields[start:end_]
	var conditions []string
	var parseErrors []string
	var current []string
	for _, f := range condFields {
		if f == "," {
			cond := parseModelCondition(current, modelName)
			if cond == "" {
				parseErrors = append(parseErrors, strings.Join(current, " "))
			} else {
				conditions = append(conditions, cond)
			}
			current = nil
		} else {
			current = append(current, f)
		}
	}
	if len(current) > 0 {
		cond := parseModelCondition(current, modelName)
		if cond == "" {
			parseErrors = append(parseErrors, strings.Join(current, " "))
		} else {
			conditions = append(conditions, cond)
		}
	}

	// If any conditions failed to parse, emit a failing assertion
	if len(parseErrors) > 0 {
		fmt.Fprintf(b, "%sexpect(true).toBe(false); // PARSE ERROR: could not parse model condition(s): %s\n", indent, strings.Join(parseErrors, "; "))
		return
	}

	// Must have at least one condition
	if len(conditions) == 0 {
		fmt.Fprintf(b, "%sexpect(true).toBe(false); // PARSE ERROR: no conditions found in model assertion: %s\n", indent, raw)
		return
	}

	// Emit the query — use singular model name for schema reference (matches schema.ts exports)
	schemaTable := "schema." + toCamelCase(modelName)
	fmt.Fprintf(b, "%sconst _row = await db.select().from(%s)", indent, schemaTable)
	if len(conditions) == 1 {
		fmt.Fprintf(b, ".where(%s)", conditions[0])
	} else if len(conditions) > 1 {
		b.WriteString(".where(and(\n")
		for i, c := range conditions {
			sep := ","
			if i == len(conditions)-1 {
				sep = ""
			}
			fmt.Fprintf(b, "%s  %s%s\n", indent, c, sep)
		}
		fmt.Fprintf(b, "%s))", indent)
	}
	b.WriteString(".limit(1);\n")
	if exists {
		fmt.Fprintf(b, "%sexpect(_row.length).toBeGreaterThan(0);\n", indent)
	} else {
		fmt.Fprintf(b, "%sexpect(_row.length).toBe(0);\n", indent)
	}
}

// parseModelCondition converts a condition token slice into a Drizzle eq() call.
// e.g. ["id", "==", "body", ".", "job_id"] → `eq(schema.job.id, body.job_id)`
// modelName should be the singular model name matching the schema.ts export.
// Returns an error comment if the condition cannot be parsed, so tests fail visibly
// instead of silently dropping assertions.
func parseModelCondition(tokens []string, modelName string) string {
	if len(tokens) < 3 {
		return fmt.Sprintf("/* ERROR: malformed condition, expected at least 3 tokens, got %d: %s */", len(tokens), strings.Join(tokens, " "))
	}
	// Find operator
	opIdx := -1
	for i, t := range tokens {
		if t == "==" || t == "!=" {
			opIdx = i
			break
		}
	}
	if opIdx < 0 {
		return fmt.Sprintf("/* ERROR: no operator found in condition: %s */", strings.Join(tokens, " "))
	}

	// LHS: join tokens before op, strip dots → field name
	lhsParts := make([]string, 0)
	for _, t := range tokens[:opIdx] {
		if t != "." {
			lhsParts = append(lhsParts, t)
		}
	}
	// RHS: join tokens after op
	rhsParts := make([]string, 0)
	for _, t := range tokens[opIdx+1:] {
		if t != "." {
			rhsParts = append(rhsParts, t)
		}
	}

	if len(lhsParts) == 0 || len(rhsParts) == 0 {
		return fmt.Sprintf("/* ERROR: empty LHS or RHS in condition: %s */", strings.Join(tokens, " "))
	}

	lhsField := toCamelCase(lhsParts[len(lhsParts)-1])
	schemaTable := "schema." + toCamelCase(modelName)
	lhsJS := schemaTable + "." + lhsField

	// Build RHS JS expression
	var rhs string
	if len(rhsParts) == 1 {
		v := rhsParts[0]
		// Quoted string → single-quote
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			rhs = "'" + v[1:len(v)-1] + "'"
		} else {
			rhs = v
		}
	} else {
		// Multi-part: response body paths preserve Blueprint response keys.
		if rhsParts[0] == "body" {
			rhs = assertionPathToJS(strings.Join(rhsParts, "."))
		} else {
			last := toCamelCase(rhsParts[len(rhsParts)-1])
			rhs = rhsParts[0] + "." + last
		}
	}

	op := tokens[opIdx]
	if op == "==" {
		return fmt.Sprintf("eq(%s, %s)", lhsJS, rhs)
	}
	return fmt.Sprintf("ne(%s, %s)", lhsJS, rhs)
}

// parseAssertionFields parses token fields from a Raw assertion string.
// Returns the dotted path, operator (is/==/!=/exists/not_exists), and rhs value.
func parseAssertionFields(fields []string) (path, op, rhs string) {
	var pathParts []string
	i := 0
	for i < len(fields) {
		f := fields[i]
		if f == "." {
			i++
			continue
		}
		if f == "is" || f == "==" || f == "!=" || f == "exists" || f == "not" {
			break
		}
		pathParts = append(pathParts, f)
		i++
	}
	if len(pathParts) == 0 || i >= len(fields) {
		return "", "", ""
	}
	path = strings.Join(pathParts, ".")
	op = fields[i]
	if op == "exists" {
		return path, "exists", ""
	}
	if op == "not" && i+1 < len(fields) && fields[i+1] == "exists" {
		return path, "not_exists", ""
	}
	if i+1 < len(fields) {
		v := fields[i+1]
		// Quoted string token (e.g. "done") → convert to JS single-quote string.
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			rhs = "'" + v[1:len(v)-1] + "'"
		} else {
			rhs = v
		}
	}
	return path, op, rhs
}

// emitTypeExpect emits a type assertion for test output.
func emitTypeExpect(b *strings.Builder, jsPath, typeName, indent string) {
	switch typeName {
	case "string":
		fmt.Fprintf(b, "%sexpect(typeof %s).toBe('string');\n", indent, jsPath)
	case "uuid":
		fmt.Fprintf(b, "%sexpect(%s).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i);\n", indent, jsPath)
	case "email":
		fmt.Fprintf(b, `%sexpect(%s).toMatch(/^[^\s@]+@[^\s@]+\.[^\s@]+$/);`+"\n", indent, jsPath)
	case "url":
		fmt.Fprintf(b, "%sexpect(() => new URL(%s)).not.toThrow();\n", indent, jsPath)
	case "number", "int", "float":
		fmt.Fprintf(b, "%sexpect(typeof %s).toBe('number');\n", indent, jsPath)
	case "bool", "boolean":
		fmt.Fprintf(b, "%sexpect(typeof %s).toBe('boolean');\n", indent, jsPath)
	default:
		fmt.Fprintf(b, "%sexpect(%s).toBeDefined(); // type: %s\n", indent, jsPath, typeName)
	}
}
