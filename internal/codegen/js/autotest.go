package js

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// Auto-generated tests + in-memory (PGlite) harness.
//
// When the --gen-tests flag is set, genAutoTests emits a self-contained Vitest
// suite that runs against an in-memory Postgres (PGlite) instead of a live
// database. It produces:
//
//   test/_harness/ddl.ts     CREATE TABLE DDL mirroring src/models/schema.ts (database services)
//   test/_harness/db.ts      PGlite-backed drizzle `db` + resetDb() (database services)
//   test/_harness/setup.ts   dummy env so src/lib/env.ts import-time parse passes
//   vitest.config.ts         registers the setup file
//   test/generated/<res>.ts  one contract+happy-path test per route group
//
// Each generated test file mocks `../src/lib/db` (so every route uses the
// in-memory db) and `@hono/node-server` (so importing the app does not start a
// real listener). Assertions are intentionally lenient: a request must return
// one of the statuses the endpoint (and its middleware) can declare, and when
// it lands on a 2xx object response the response shape is checked against the
// declared output keys. This stays meaningful without being flaky for
// endpoints whose success path depends on business logic or native stubs.

const zeroUUID = "00000000-0000-0000-0000-000000000000"

// genAutoTests builds the in-memory test harness and per-endpoint contract tests.
func (g *Generator) genAutoTests(endpoints []*ast.Endpoint, secrets []*ast.Secret, hasDB bool) []codegen.OutputFile {
	var files []codegen.OutputFile
	if hasDB {
		files = append(files, g.genTestDDLFile())
		files = append(files, g.genTestDBFile())
	}
	files = append(files, g.genTestSetupFile(secrets))
	files = append(files, g.genVitestConfig())

	groups := make(map[string][]*ast.Endpoint)
	var order []string
	for _, ep := range endpoints {
		res := extractResource(ep.Path)
		if _, ok := groups[res]; !ok {
			order = append(order, res)
		}
		groups[res] = append(groups[res], ep)
	}
	sort.Strings(order)
	for _, res := range order {
		files = append(files, g.genContractTestFile(res, groups[res], hasDB))
	}
	return files
}

// --- Harness files ---

func (g *Generator) genTestDDLFile() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("// In-memory Postgres schema for tests. Mirrors src/models/schema.ts.\n\n")
	b.WriteString("export const DDL = `\n")
	b.WriteString(g.buildTestDDL())
	b.WriteString("`;\n")
	return codegen.OutputFile{Path: "test/_harness/ddl.ts", Content: []byte(b.String())}
}

// buildTestDDL renders CREATE TABLE statements for every model, followed by
// ALTER TABLE foreign keys (emitted last so table creation order never matters).
func (g *Generator) buildTestDDL() string {
	var b strings.Builder
	var fks []string
	for _, m := range g.models {
		table := pluralize(m.Name)
		fmt.Fprintf(&b, "CREATE TABLE IF NOT EXISTS \"%s\" (\n", table)
		cols := make([]string, 0, len(m.Fields))
		for _, f := range m.Fields {
			cols = append(cols, "  "+testColumnDDL(f))
			if ref := refTarget(f); ref != "" {
				fks = append(fks, fmt.Sprintf(
					"ALTER TABLE \"%s\" ADD CONSTRAINT \"%s_%s_fkey\" FOREIGN KEY (\"%s\") REFERENCES \"%s\" (\"id\");",
					table, table, f.Name, f.Name, pluralize(ref)))
			}
		}
		b.WriteString(strings.Join(cols, ",\n"))
		b.WriteString("\n);\n")
	}
	for _, fk := range fks {
		b.WriteString(fk)
		b.WriteString("\n")
	}
	return b.String()
}

// testColumnDDL renders a single column definition mirroring writeFieldConstraints/typeToDrizzle.
func testColumnDDL(f *ast.Field) string {
	parts := []string{fmt.Sprintf("\"%s\"", f.Name), testPgType(f.Type)}

	isPrimary, isUnique, isRequired, hasDefault, isUUID := false, false, false, false, false
	var defaultSQL string
	if pt, ok := f.Type.(*ast.PrimitiveType); ok && pt.Name == "uuid" {
		isUUID = true
	}
	for _, c := range f.Constraints {
		switch c.Kind {
		case "primary":
			isPrimary = true
		case "unique":
			isUnique = true
		case "required":
			isRequired = true
		case "default":
			hasDefault = true
			defaultSQL = sqlDefaultLiteral(c.Value)
		}
	}

	if isPrimary {
		parts = append(parts, "PRIMARY KEY")
		if isUUID {
			parts = append(parts, "DEFAULT gen_random_uuid()")
		}
		return strings.Join(parts, " ")
	}
	if hasDefault && defaultSQL != "" {
		parts = append(parts, "DEFAULT "+defaultSQL)
	}
	if isRequired {
		parts = append(parts, "NOT NULL")
	}
	if isUnique {
		parts = append(parts, "UNIQUE")
	}
	return strings.Join(parts, " ")
}

// testPgType maps a Blueprint type to a Postgres column type, mirroring typeToDrizzle.
func testPgType(t ast.TypeExpr) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "int":
			return "integer"
		case "float":
			return "real"
		case "bool":
			return "boolean"
		case "uuid":
			return "uuid"
		case "timestamp":
			return "timestamp"
		case "json":
			return "jsonb"
		case "money":
			return "integer"
		default:
			return "text"
		}
	case *ast.TypedJSONType:
		return "jsonb"
	default:
		return "text"
	}
}

// sqlDefaultLiteral renders a default value expression as a SQL literal, or "" to skip.
func sqlDefaultLiteral(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.NowLit:
		return "now()"
	case *ast.BoolLit:
		if v.Value {
			return "true"
		}
		return "false"
	case *ast.IntLit:
		return v.Value
	case *ast.FloatLit:
		return v.Value
	case *ast.StringLit:
		return "'" + strings.ReplaceAll(v.Value, "'", "''") + "'"
	case *ast.Ident:
		return "'" + v.Name + "'"
	default:
		return ""
	}
}

func (g *Generator) genTestDBFile() codegen.OutputFile {
	tables := make([]string, 0, len(g.models))
	for _, m := range g.models {
		tables = append(tables, fmt.Sprintf("'%s'", pluralize(m.Name)))
	}
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`import { PGlite } from '@electric-sql/pglite';
import { drizzle } from 'drizzle-orm/pglite';
import * as schema from '../../src/models/schema.js';
import { DDL } from './ddl.js';

const client = await PGlite.create();
await client.exec(DDL);

export const db = drizzle(client, { schema });

`)
	fmt.Fprintf(&b, "const TABLES = [%s];\n\n", strings.Join(tables, ", "))
	b.WriteString(`// resetDb truncates all tables between tests for isolation.
export async function resetDb(): Promise<void> {
  if (TABLES.length === 0) return;
  const list = TABLES.map((t) => '"' + t + '"').join(', ');
  await client.exec(` + "`TRUNCATE ${list} RESTART IDENTITY CASCADE;`" + `);
}
`)
	return codegen.OutputFile{Path: "test/_harness/db.ts", Content: []byte(b.String())}
}

func (g *Generator) genTestSetupFile(secrets []*ast.Secret) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("// Vitest setup: provide dummy env vars so src/lib/env.ts import-time validation\n")
	b.WriteString("// passes. The real database is never used — tests mock src/lib/db with PGlite.\n\n")
	b.WriteString("process.env.NODE_ENV ||= 'test';\n")
	b.WriteString("process.env.DATABASE_URL ||= 'postgres://test:test@localhost:5432/test';\n")
	b.WriteString("process.env.REDIS_URL ||= 'redis://localhost:6379';\n")
	seen := map[string]bool{"DATABASE_URL": true, "REDIS_URL": true, "NODE_ENV": true}
	for _, s := range secrets {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		fmt.Fprintf(&b, "process.env.%s ||= 'test';\n", s.Name)
	}
	return codegen.OutputFile{Path: "test/_harness/setup.ts", Content: []byte(b.String())}
}

func (g *Generator) genVitestConfig() codegen.OutputFile {
	content := `import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    setupFiles: ['./test/_harness/setup.ts'],
  },
});
`
	return codegen.OutputFile{Path: "vitest.config.ts", Content: []byte(content)}
}

// --- Per-endpoint contract tests ---

func (g *Generator) genContractTestFile(resource string, eps []*ast.Endpoint, hasDB bool) codegen.OutputFile {
	var body strings.Builder
	needsSeed := false
	for _, ep := range eps {
		if g.emitContractTest(&body, ep, &needsSeed) {
			needsSeed = true
		}
	}

	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { describe, it, expect, beforeEach, vi } from 'vitest';\n\n")
	if hasDB {
		b.WriteString("vi.mock('../../src/lib/db', async () => ({ db: (await import('../_harness/db.js')).db }));\n\n")
	}
	b.WriteString("import app from '../../src/index.js';\n")
	if hasDB {
		b.WriteString("import { resetDb")
		if needsSeed {
			b.WriteString(", db")
		}
		b.WriteString(" } from '../_harness/db.js';\n")
		if needsSeed {
			b.WriteString("import * as schema from '../../src/models/schema.js';\n")
		}
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "describe('%s (generated contract)', () => {\n", resource)
	if hasDB {
		b.WriteString("  beforeEach(async () => { await resetDb(); });\n\n")
	}
	b.WriteString(body.String())
	b.WriteString("});\n")

	return codegen.OutputFile{Path: fmt.Sprintf("test/generated/%s.test.ts", toKebabCase(resource)), Content: []byte(b.String())}
}

// emitContractTest writes one `it` block for an endpoint and reports whether it seeds the db.
func (g *Generator) emitContractTest(b *strings.Builder, ep *ast.Endpoint, fileNeedsSeed *bool) bool {
	method := strings.ToUpper(ep.Method)
	indent := "    "
	seedCounter := 0
	var seedBody strings.Builder
	seeds := false

	// Seed the model fetched by a path param so the success path is reachable.
	pathParams := extractPathParams(ep.Path)
	seedParamVar := map[string]string{} // path param name -> seeded row JS var
	if len(pathParams) > 0 {
		if model := firstDataOpModel(ep.Stmts); model != "" {
			if v := g.emitSeedModel(&seedBody, model, indent, &seedCounter, map[string]bool{}); v != "" {
				// Bind the row to every path param (typically a single :id).
				for _, p := range pathParams {
					seedParamVar[p] = v
				}
				seeds = true
			}
		}
	}

	// Collect inputs and split by where they travel.
	var pathInputs, queryInputs, bodyInputs []*ast.InputStmt
	useQuery := method == "GET" || method == "DELETE"
	for _, s := range ep.Stmts {
		inp, ok := s.(*ast.InputStmt)
		if !ok {
			continue
		}
		switch {
		case isPathParam(inp.Name, ep.Path):
			pathInputs = append(pathInputs, inp)
		case useQuery:
			queryInputs = append(queryInputs, inp)
		default:
			bodyInputs = append(bodyInputs, inp)
		}
	}
	_ = pathInputs

	// Build the request URL (path + querystring).
	url := g.buildRequestPath(ep.Path, seedParamVar)
	if len(queryInputs) > 0 {
		qs := make([]string, 0, len(queryInputs))
		for _, inp := range queryInputs {
			qs = append(qs, inp.Name+"="+sampleQueryValue(g, inp.Type, inp.Constraints))
		}
		url = strings.TrimSuffix(url, "`") + "?" + strings.Join(qs, "&") + "`"
	}

	fmt.Fprintf(b, "  it('%s %s responds with a declared status', async () => {\n", method, ep.Path)
	if seeds {
		b.WriteString(seedBody.String())
	}

	// Request options.
	opts := fmt.Sprintf("method: '%s'", method)
	if len(bodyInputs) > 0 {
		parts := make([]string, 0, len(bodyInputs))
		for _, inp := range bodyInputs {
			parts = append(parts, fmt.Sprintf("%s: %s", inp.Name, sampleJSExpr(g, inp.Type, inp.Constraints)))
		}
		opts += ", headers: { 'Content-Type': 'application/json' }"
		opts += fmt.Sprintf(", body: JSON.stringify({ %s })", strings.Join(parts, ", "))
	}
	fmt.Fprintf(b, "%sconst res = await app.request(%s, { %s });\n", indent, url, opts)

	// Status containment assertion.
	statuses := g.collectAllowedStatuses(ep)
	fmt.Fprintf(b, "%sexpect([%s]).toContain(res.status);\n", indent, strings.Join(statuses, ", "))

	// Opportunistic response-shape checks for 2xx object outputs.
	for _, sc := range collectShapeChecks(ep.Stmts) {
		fmt.Fprintf(b, "%sif (res.status === %s) {\n", indent, sc.status)
		fmt.Fprintf(b, "%s  const body = await res.json() as any;\n", indent)
		for _, k := range sc.keys {
			fmt.Fprintf(b, "%s  expect(body).toHaveProperty('%s');\n", indent, k)
		}
		fmt.Fprintf(b, "%s}\n", indent)
	}

	b.WriteString("  });\n\n")
	return seeds
}

// buildRequestPath renders a JS template literal for the endpoint path, substituting
// seeded ids for bound path params and synthesized values for the rest.
func (g *Generator) buildRequestPath(path string, seedParamVar map[string]string) string {
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if !strings.HasPrefix(seg, ":") {
			continue
		}
		name := seg[1:]
		if v, ok := seedParamVar[name]; ok {
			segs[i] = "${" + v + ".id}"
		} else {
			segs[i] = zeroUUID
		}
	}
	return "`" + strings.Join(segs, "/") + "`"
}

// emitSeedModel inserts a row for modelName (seeding ref parents first) and returns
// the JS variable holding the inserted row, or "" if the model is unknown.
func (g *Generator) emitSeedModel(b *strings.Builder, modelName, indent string, counter *int, visiting map[string]bool) string {
	m := g.findModel(modelName)
	if m == nil || visiting[modelName] {
		return ""
	}
	visiting[modelName] = true
	defer delete(visiting, modelName)

	// Seed foreign-key parents first.
	fkValues := map[string]string{} // camelCase fk column -> parent.id JS
	for _, f := range m.Fields {
		ref := refTarget(f)
		if ref == "" {
			continue
		}
		if pv := g.emitSeedModel(b, ref, indent, counter, visiting); pv != "" {
			fkValues[toCamelCase(f.Name)] = pv + ".id"
		}
	}

	// Build the insert value object: required, no-default, non-FK fields + FK parent ids.
	var kvs []string
	for _, f := range m.Fields {
		col := toCamelCase(f.Name)
		if v, ok := fkValues[col]; ok {
			kvs = append(kvs, fmt.Sprintf("%s: %s", col, v))
			continue
		}
		if !fieldNeedsSeedValue(f) {
			continue
		}
		kvs = append(kvs, fmt.Sprintf("%s: %s", col, modelFieldSampleJS(g, f)))
	}

	v := fmt.Sprintf("_seed%d", *counter)
	*counter++
	fmt.Fprintf(b, "%sconst %s = (await db.insert(schema.%s).values({ %s }).returning())[0];\n",
		indent, v, toCamelCase(m.Name), strings.Join(kvs, ", "))
	return v
}

// fieldNeedsSeedValue reports whether a field must be given an explicit value when seeding
// (required, no default, not the auto-generated primary key).
func fieldNeedsSeedValue(f *ast.Field) bool {
	required, hasDefault, primary := false, false, false
	for _, c := range f.Constraints {
		switch c.Kind {
		case "required":
			required = true
		case "default":
			hasDefault = true
		case "primary":
			primary = true
		}
	}
	return required && !hasDefault && !primary
}

// --- Value synthesis ---

// sampleJSExpr returns a JS expression literal honoring the type and its constraints.
func sampleJSExpr(g *Generator, t ast.TypeExpr, constraints []*ast.Constraint_) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "int", "money":
			return intSampleStr(constraints)
		case "float":
			return "1.5"
		case "bool":
			return "true"
		case "uuid":
			return "'" + zeroUUID + "'"
		case "timestamp":
			return "new Date().toISOString()"
		default: // string, text
			return "'" + stringSample(constraints) + "'"
		}
	case *ast.TypedJSONType:
		return "{}"
	case *ast.ListType:
		return "[]"
	case *ast.MapType:
		return "{}"
	case *ast.EnumInline:
		if len(v.Variants) > 0 {
			return "'" + v.Variants[0] + "'"
		}
		return "'test'"
	case *ast.NamedType:
		if variants := g.enumVariants[v.Name]; len(variants) > 0 {
			return "'" + variants[0] + "'"
		}
		return "{}"
	default:
		return "'test'"
	}
}

// sampleQueryValue returns a raw (unquoted) value for use in a query string.
func sampleQueryValue(g *Generator, t ast.TypeExpr, constraints []*ast.Constraint_) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "int", "money":
			return intSampleStr(constraints)
		case "float":
			return "1.5"
		case "bool":
			return "true"
		case "uuid":
			return zeroUUID
		default:
			return stringSample(constraints)
		}
	case *ast.EnumInline:
		if len(v.Variants) > 0 {
			return v.Variants[0]
		}
		return "test"
	case *ast.NamedType:
		if variants := g.enumVariants[v.Name]; len(variants) > 0 {
			return variants[0]
		}
		return "test"
	default:
		return "test"
	}
}

// modelFieldSampleJS synthesizes a seed value for a model field, using a unique value
// for unique columns to avoid collisions.
func modelFieldSampleJS(g *Generator, f *ast.Field) string {
	for _, c := range f.Constraints {
		if c.Kind == "unique" {
			if pt, ok := f.Type.(*ast.PrimitiveType); ok && (pt.Name == "string" || pt.Name == "text") {
				return "crypto.randomUUID()"
			}
		}
	}
	return sampleJSExpr(g, f.Type, f.Constraints)
}

func intSampleStr(constraints []*ast.Constraint_) string {
	for _, c := range constraints {
		if c.Kind == "min" {
			if il, ok := c.Value.(*ast.IntLit); ok {
				return il.Value
			}
		}
	}
	return "1"
}

func stringSample(constraints []*ast.Constraint_) string {
	for _, c := range constraints {
		if c.Kind == "format" {
			switch exprToString(c.Value) {
			case "email":
				return "test@example.com"
			case "url":
				return "https://example.com"
			case "uuid":
				return zeroUUID
			}
		}
	}
	return "test"
}

// --- Endpoint analysis ---

// firstDataOpModel returns the model name of the first data operation in stmts
// (e.g. "todo" from `fetch todo(id)`), searching nested blocks.
func firstDataOpModel(stmts []ast.ArrowStmt) string {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.StepStmt:
			if fn, ok := v.Expr.(*ast.FnCall); ok && isDataOp(fn.Name) && len(fn.Args) > 0 {
				if id, ok := fn.Args[0].(*ast.Ident); ok {
					return id.Name
				}
			}
		case *ast.WhenStmt:
			if m := firstDataOpModel(v.Body); m != "" {
				return m
			}
		case *ast.TryRecover:
			if m := firstDataOpModel(v.Try); m != "" {
				return m
			}
		}
	}
	return ""
}

// collectAllowedStatuses returns the sorted set of HTTP statuses an endpoint may return,
// including its own guards/outputs, used-middleware guards, and validation/runtime fallbacks.
func (g *Generator) collectAllowedStatuses(ep *ast.Endpoint) []string {
	set := map[string]bool{"400": true, "500": true}
	collectStatuses(ep.Stmts, set)
	for _, meta := range ep.Meta {
		if meta.Kind == "use" && meta.Use != nil {
			if mw := g.middlewares[meta.Use.Name]; mw != nil {
				collectStatuses(mw.Before, set)
				collectStatuses(mw.After, set)
			}
		}
		if meta.Kind == "auth" {
			set["401"] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func collectStatuses(stmts []ast.ArrowStmt, set map[string]bool) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.OutputStmt:
			st := v.Status
			if st == "" {
				st = "200"
			}
			set[st] = true
		case *ast.GuardStmt:
			if v.Status != "" {
				set[v.Status] = true
			}
		case *ast.WhenStmt:
			collectStatuses(v.Body, set)
		case *ast.TryRecover:
			collectStatuses(v.Try, set)
			collectStatuses(v.Recover, set)
		}
	}
}

type shapeCheck struct {
	status string
	keys   []string
}

// collectShapeChecks finds 2xx outputs whose body is an object literal and returns the
// declared top-level keys to assert on the response.
func collectShapeChecks(stmts []ast.ArrowStmt) []shapeCheck {
	var checks []shapeCheck
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.OutputStmt:
			status := v.Status
			if status == "" {
				status = "200"
			}
			if !strings.HasPrefix(status, "2") {
				continue
			}
			if blk, ok := v.Value.(*ast.BlockExpr); ok && len(blk.Entries) > 0 {
				keys := make([]string, 0, len(blk.Entries))
				for _, kv := range blk.Entries {
					keys = append(keys, kv.Key)
				}
				checks = append(checks, shapeCheck{status: status, keys: keys})
			}
		case *ast.WhenStmt:
			checks = append(checks, collectShapeChecks(v.Body)...)
		case *ast.TryRecover:
			checks = append(checks, collectShapeChecks(v.Try)...)
		}
	}
	return checks
}

// refTarget returns the referenced model name for a field's ref() constraint, or "".
func refTarget(f *ast.Field) string {
	for _, c := range f.Constraints {
		if c.Kind == "ref" {
			return exprToString(c.Value)
		}
	}
	return ""
}
