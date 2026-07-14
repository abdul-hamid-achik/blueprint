package js

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

const propertyTestRuns = 32

// propertyTypeIndex is the generation-time view of named input types. Keeping
// this resolution in Go makes the emitted fast-check arbitraries explicit and
// deterministic instead of deriving values from Zod internals at runtime.
type propertyTypeIndex struct {
	types   map[string]*ast.TypeDecl
	aliases map[string]*ast.Alias
}

func newPropertyTypeIndex(types []*ast.TypeDecl, aliases []*ast.Alias) propertyTypeIndex {
	idx := propertyTypeIndex{
		types:   make(map[string]*ast.TypeDecl, len(types)),
		aliases: make(map[string]*ast.Alias, len(aliases)),
	}
	for _, decl := range types {
		idx.types[decl.Name] = decl
	}
	for _, alias := range aliases {
		idx.aliases[alias.Name] = alias
	}
	return idx
}

// validatePropertyTestSupport is deliberately fail-closed. A property mode
// that silently skips a difficult input or invokes live infrastructure would
// look green without testing the declared service boundary.
func (g *Generator) validatePropertyTestSupport(endpoints []*ast.Endpoint, types []*ast.TypeDecl, aliases []*ast.Alias, fns []*ast.Fn, pipes []*ast.Pipe) error {
	if len(endpoints) == 0 {
		return fmt.Errorf("property tests require at least one REST endpoint")
	}

	idx := newPropertyTypeIndex(types, aliases)
	fnByName := make(map[string]*ast.Fn, len(fns))
	for _, fn := range fns {
		fnByName[fn.Name] = fn
	}
	pipeByName := make(map[string]*ast.Pipe, len(pipes))
	for _, pipe := range pipes {
		pipeByName[pipe.Name] = pipe
	}

	for _, ep := range endpoints {
		label := strings.ToUpper(ep.Method) + " " + ep.Path
		for _, meta := range ep.Meta {
			switch meta.Kind {
			case "limit":
				return fmt.Errorf("property tests do not support %s: in-process rate-limit state would make repeated runs order-dependent", label)
			case "auth":
				return fmt.Errorf("property tests do not support %s auth metadata: credential-aware request arbitraries are not generated yet", label)
			}
		}

		for _, stmt := range ep.Stmts {
			inp, ok := stmt.(*ast.InputStmt)
			if !ok {
				continue
			}
			if isPathParam(inp.Name, ep.Path) {
				if propertyInputOptional(inp.Constraints) {
					return fmt.Errorf("property tests do not support optional/defaulted path input %q on %s", inp.Name, label)
				}
				if !propertyPathTypeSupported(inp.Type) {
					return fmt.Errorf("property tests do not support path input %q on %s: use string, text, int, or uuid", inp.Name, label)
				}
			} else if (ep.Method == "GET" || ep.Method == "DELETE") && !g.propertyQueryTypeSupported(inp.Type, idx, map[string]bool{}) {
				return fmt.Errorf("property tests do not support query input %q on %s: query fuzzing is limited to scalar and enum values", inp.Name, label)
			}
			constraints := propertyTransportConstraints(inp, ep.Path)
			if _, err := g.propertyArbitrary(inp.Type, constraints, idx, map[string]bool{}); err != nil {
				return fmt.Errorf("property tests do not support input %q on %s: %w", inp.Name, label, err)
			}
		}

		if err := g.validatePropertyHermeticStmts(ep.Stmts, fnByName, pipeByName, map[string]bool{}); err != nil {
			return fmt.Errorf("property tests do not support %s: %w", label, err)
		}
		if propertyStmtsReferenceHeader(ep.Stmts) {
			return fmt.Errorf("property tests do not support %s: header-aware request arbitraries are not generated yet", label)
		}
		for _, meta := range ep.Meta {
			if meta.Kind != "use" || meta.Use == nil {
				continue
			}
			if mw := g.middlewares[meta.Use.Name]; mw != nil {
				if propertyStmtsReferenceHeader(mw.Before) || propertyStmtsReferenceHeader(mw.After) {
					return fmt.Errorf("property tests do not support %s middleware %q: header-aware request arbitraries are not generated yet", label, mw.Name)
				}
				if err := g.validatePropertyHermeticStmts(mw.Before, fnByName, pipeByName, map[string]bool{}); err != nil {
					return fmt.Errorf("property tests do not support %s middleware %q: %w", label, mw.Name, err)
				}
				if err := g.validatePropertyHermeticStmts(mw.After, fnByName, pipeByName, map[string]bool{}); err != nil {
					return fmt.Errorf("property tests do not support %s middleware %q: %w", label, mw.Name, err)
				}
			}
		}
	}

	// Global custom middleware runs for every generated request.
	if g.file.Blueprint != nil {
		for _, use := range g.file.Blueprint.Uses {
			if mw := g.middlewares[use.Name]; mw != nil {
				if propertyStmtsReferenceHeader(mw.Before) || propertyStmtsReferenceHeader(mw.After) {
					return fmt.Errorf("property tests do not support global middleware %q: header-aware request arbitraries are not generated yet", mw.Name)
				}
				if err := g.validatePropertyHermeticStmts(mw.Before, fnByName, pipeByName, map[string]bool{}); err != nil {
					return fmt.Errorf("property tests do not support global middleware %q: %w", mw.Name, err)
				}
				if err := g.validatePropertyHermeticStmts(mw.After, fnByName, pipeByName, map[string]bool{}); err != nil {
					return fmt.Errorf("property tests do not support global middleware %q: %w", mw.Name, err)
				}
			}
		}
	}
	return nil
}

func (g *Generator) validatePropertyHermeticStmts(stmts []ast.ArrowStmt, fns map[string]*ast.Fn, pipes map[string]*ast.Pipe, visiting map[string]bool) error {
	var found error
	walkStmts(stmts, func(expr ast.Expr) {
		if found != nil {
			return
		}
		if _, ok := expr.(*ast.NowLit); ok {
			found = fmt.Errorf("now is wall-clock dependent; inject a deterministic time function before enabling properties")
			return
		}
		call, ok := expr.(*ast.FnCall)
		if !ok {
			return
		}
		switch call.Name {
		case "call":
			found = fmt.Errorf("external HTTP calls are non-hermetic")
			return
		case "enqueue":
			found = fmt.Errorf("enqueue requires a live Redis queue")
			return
		case "upload", "download", "delete_s3_object":
			found = fmt.Errorf("storage calls require a live external service")
			return
		case "track":
			found = fmt.Errorf("analytics calls require a live sink")
			return
		case "emit":
			found = fmt.Errorf("event emission may invoke side-effecting subscribers")
			return
		case "sleep":
			found = fmt.Errorf("sleep would make fuzz runs time-dependent")
			return
		case "clock":
			found = fmt.Errorf("clock is wall-clock dependent; inject a deterministic time function before enabling properties")
			return
		case "join", "leave", "broadcast":
			found = fmt.Errorf("realtime room call %q may connect to live Redis", call.Name)
			return
		case "on":
			found = fmt.Errorf("event registration is process-global and would leak state across property runs")
			return
		}
		if err := g.validatePropertyForeignKeyWrite(call); err != nil {
			found = err
			return
		}

		if fn := fns[call.Name]; fn != nil {
			key := "fn:" + fn.Name
			if visiting[key] {
				found = fmt.Errorf("recursive inline function call graph at %q cannot be proven terminating and hermetic", fn.Name)
				return
			}
			if fn.Impl != nil {
				found = fmt.Errorf("function %q uses user/native impl %s, which is not proven hermetic", fn.Name, fn.Impl.Strategy)
				return
			}
			if fn.Logic == nil {
				found = fmt.Errorf("function %q has no inline logic that can be verified as hermetic", fn.Name)
				return
			}
			visiting[key] = true
			found = g.validatePropertyHermeticStmts(fn.Logic.Stmts, fns, pipes, visiting)
			delete(visiting, key)
			return
		}
		if pipe := pipes[call.Name]; pipe != nil {
			key := "pipe:" + pipe.Name
			if visiting[key] {
				found = fmt.Errorf("recursive inline pipe call graph at %q cannot be proven terminating and hermetic", pipe.Name)
				return
			}
			visiting[key] = true
			found = g.validatePropertyHermeticStmts(pipe.Stmts, fns, pipes, visiting)
			delete(visiting, key)
			return
		}
		if !isDataOp(call.Name) && !isBuiltinFn(call.Name) {
			found = fmt.Errorf("call %q is not a declared hermetic function, pipe, or builtin", call.Name)
		}
	})
	return found
}

// validatePropertyForeignKeyWrite rejects generated write requests that would
// need a referenced parent row. Property runs reset the database before every
// case and do not synthesize seed data, so arbitrary foreign-key values would
// otherwise turn a valid-request property into a deterministic 500.
func (g *Generator) validatePropertyForeignKeyWrite(call *ast.FnCall) error {
	if call == nil || (call.Name != "save" && call.Name != "seed" && call.Name != "update") || len(call.Args) < 2 {
		return nil
	}

	models := g.models
	if ident, ok := call.Args[0].(*ast.Ident); ok {
		if model := g.findModel(ident.Name); model != nil {
			models = []*ast.Model{model}
		}
	}

	for _, arg := range call.Args[1:] {
		block, ok := arg.(*ast.BlockExpr)
		if !ok {
			continue
		}
		for _, entry := range block.Entries {
			for _, model := range models {
				for _, field := range model.Fields {
					parent := refTarget(field)
					if field.Name != entry.Key || parent == "" {
						continue
					}
					return fmt.Errorf("%s writes ref-backed field %q on model %q; property runs reset the database and do not seed referenced %q parents", call.Name, field.Name, model.Name, parent)
				}
			}
		}
	}

	return nil
}

func propertyStmtsReferenceHeader(stmts []ast.ArrowStmt) bool {
	found := false
	walkStmts(stmts, func(expr ast.Expr) {
		switch value := expr.(type) {
		case *ast.FieldAccess:
			if base, ok := value.Base.(*ast.Ident); ok && base.Name == "header" {
				found = true
			}
		case *ast.StringLit:
			if strings.Contains(value.Value, "{header.") {
				found = true
			}
		}
	})
	return found
}

func propertyPathTypeSupported(t ast.TypeExpr) bool {
	primitive, ok := t.(*ast.PrimitiveType)
	if !ok {
		return false
	}
	switch primitive.Name {
	case "string", "text", "int", "uuid":
		return true
	default:
		return false
	}
}

func (g *Generator) propertyQueryTypeSupported(t ast.TypeExpr, idx propertyTypeIndex, visiting map[string]bool) bool {
	switch value := t.(type) {
	case *ast.PrimitiveType:
		switch value.Name {
		case "string", "text", "int", "float", "bool", "uuid", "timestamp", "money":
			return true
		default:
			return false
		}
	case *ast.EnumInline, *ast.TranslationKeyType:
		return true
	case *ast.NamedType:
		if len(g.enumVariants[value.Name]) > 0 {
			return true
		}
		if visiting[value.Name] {
			return false
		}
		alias := idx.aliases[value.Name]
		if alias == nil {
			// Structural and unknown named types cannot round-trip through a
			// URLSearchParams scalar without becoming "[object Object]".
			return false
		}
		visiting[value.Name] = true
		ok := g.propertyQueryTypeSupported(alias.Type, idx, visiting)
		delete(visiting, value.Name)
		return ok
	default:
		return false
	}
}

func propertyInputOptional(constraints []*ast.Constraint_) bool {
	for _, constraint := range constraints {
		if constraint.Kind == "optional" || constraint.Kind == "default" {
			return true
		}
	}
	return false
}

// propertyTransportConstraints applies constraints imposed by the request
// transport before validating or rendering an arbitrary. In particular, an
// empty string cannot occupy a URL path segment even though it satisfies an
// unconstrained z.string(). The returned slice never mutates the source AST.
func propertyTransportConstraints(inp *ast.InputStmt, endpointPath string) []*ast.Constraint_ {
	constraints := append([]*ast.Constraint_(nil), inp.Constraints...)
	if !isPathParam(inp.Name, endpointPath) {
		return constraints
	}
	primitive, ok := inp.Type.(*ast.PrimitiveType)
	if !ok || (primitive.Name != "string" && primitive.Name != "text") {
		return constraints
	}

	foundMin := false
	for i, constraint := range constraints {
		if constraint.Kind != "min" {
			continue
		}
		foundMin = true
		value, err := propertyConstraintInt(constraint)
		if err != nil || value >= 1 {
			continue
		}
		clone := *constraint
		clone.Value = &ast.IntLit{Loc: constraint.Loc, Value: "1"}
		constraints[i] = &clone
	}
	if !foundMin {
		constraints = append(constraints, &ast.Constraint_{Kind: "min", Value: &ast.IntLit{Value: "1"}})
	}
	return constraints
}

func (g *Generator) genPropertyTests(endpoints []*ast.Endpoint, hasDB bool, types []*ast.TypeDecl, aliases []*ast.Alias) ([]codegen.OutputFile, error) {
	idx := newPropertyTypeIndex(types, aliases)
	groups := make(map[string][]*ast.Endpoint)
	var resources []string
	for _, ep := range endpoints {
		resource := extractResource(ep.Path)
		if _, exists := groups[resource]; !exists {
			resources = append(resources, resource)
		}
		groups[resource] = append(groups[resource], ep)
	}
	sort.Strings(resources)

	files := make([]codegen.OutputFile, 0, len(resources))
	for _, resource := range resources {
		file, err := g.genPropertyTestFile(resource, groups[resource], hasDB, idx)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (g *Generator) genPropertyTestFile(resource string, endpoints []*ast.Endpoint, hasDB bool, idx propertyTypeIndex) (codegen.OutputFile, error) {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("// Deterministic property tests. A failure reports a replayable fast-check seed/path.\n")
	b.WriteString("import { describe, it, expect, vi } from 'vitest';\n")
	b.WriteString("import fc from 'fast-check';\n\n")
	if hasDB {
		b.WriteString("vi.mock('../../src/lib/db', async () => ({ db: (await import('../_harness/db.js')).db }));\n\n")
	}
	b.WriteString("import app from '../../src/index.js';\n")
	if hasDB {
		b.WriteString("import { resetDb } from '../_harness/db.js';\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "describe('%s (generated properties)', () => {\n", resource)
	for _, ep := range endpoints {
		if err := g.emitPropertyTest(&b, ep, hasDB, idx); err != nil {
			return codegen.OutputFile{}, err
		}
	}
	b.WriteString("});\n")
	return codegen.OutputFile{
		Path:    fmt.Sprintf("test/generated/%s.property.test.ts", toKebabCase(resource)),
		Content: []byte(b.String()),
	}, nil
}

func (g *Generator) emitPropertyTest(b *strings.Builder, ep *ast.Endpoint, hasDB bool, idx propertyTypeIndex) error {
	method := strings.ToUpper(ep.Method)
	label := method + " " + ep.Path
	inputs := make([]*ast.InputStmt, 0)
	for _, stmt := range ep.Stmts {
		if inp, ok := stmt.(*ast.InputStmt); ok {
			inputs = append(inputs, inp)
		}
	}

	fields := make([]string, 0, len(inputs))
	for _, inp := range inputs {
		constraints := propertyTransportConstraints(inp, ep.Path)
		arb, err := g.propertyArbitrary(inp.Type, constraints, idx, map[string]bool{})
		if err != nil {
			return fmt.Errorf("property test emission failed for input %q on %s: %w", inp.Name, label, err)
		}
		fields = append(fields, fmt.Sprintf("%s: %s", inp.Name, arb))
	}
	arbitrary := "fc.constant({})"
	if len(fields) > 0 {
		arbitrary = "fc.record({ " + strings.Join(fields, ", ") + " })"
	}

	fmt.Fprintf(b, "  it(%q, async () => {\n", label+" accepts generated valid requests")
	b.WriteString("    await fc.assert(\n")
	fmt.Fprintf(b, "      fc.asyncProperty(%s, async (input) => {\n", arbitrary)
	if hasDB {
		b.WriteString("        await resetDb();\n")
	}
	fmt.Fprintf(b, "        let path = %q;\n", ep.Path)
	for _, inp := range inputs {
		if isPathParam(inp.Name, ep.Path) {
			fmt.Fprintf(b, "        path = path.replace(%q, encodeURIComponent(String(input.%s)));\n", ":"+inp.Name, inp.Name)
		}
	}

	var queryInputs, bodyInputs []*ast.InputStmt
	for _, inp := range inputs {
		if isPathParam(inp.Name, ep.Path) {
			continue
		}
		if method == "GET" || method == "DELETE" {
			queryInputs = append(queryInputs, inp)
		} else {
			bodyInputs = append(bodyInputs, inp)
		}
	}
	if len(queryInputs) > 0 {
		b.WriteString("        const query = new URLSearchParams();\n")
		for _, inp := range queryInputs {
			fmt.Fprintf(b, "        if (input.%s !== undefined) query.set(%q, String(input.%s));\n", inp.Name, inp.Name, inp.Name)
		}
		b.WriteString("        const queryString = query.toString();\n")
		b.WriteString("        if (queryString) path += `?${queryString}`;\n")
	}

	requestOptions := fmt.Sprintf("{ method: %q }", method)
	if len(bodyInputs) > 0 {
		bodyFields := make([]string, 0, len(bodyInputs))
		for _, inp := range bodyInputs {
			bodyFields = append(bodyFields, fmt.Sprintf("%s: input.%s", inp.Name, inp.Name))
		}
		requestOptions = fmt.Sprintf("{ method: %q, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ %s }) }", method, strings.Join(bodyFields, ", "))
	}
	fmt.Fprintf(b, "        const response = await app.request(path, %s);\n", requestOptions)
	fmt.Fprintf(b, "        expect([%s]).toContain(response.status);\n", strings.Join(g.collectPropertyStatuses(ep), ", "))
	b.WriteString("      }),\n")
	fmt.Fprintf(b, "      { seed: %d, numRuns: %d, endOnFailure: true },\n", propertySeed(label), propertyTestRuns)
	b.WriteString("    );\n")
	b.WriteString("  });\n\n")
	return nil
}

func propertySeed(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	// fast-check accepts signed 32-bit seeds. Keep generated literals positive
	// to avoid a formatting/sign conversion surprise across JS runtimes.
	return h.Sum32() & 0x7fffffff
}

func (g *Generator) collectPropertyStatuses(ep *ast.Endpoint) []string {
	set := make(map[string]bool)
	collectStatuses(ep.Stmts, set)
	for _, meta := range ep.Meta {
		if meta.Kind == "auth" {
			set["401"] = true
		}
		if meta.Kind == "use" && meta.Use != nil {
			if mw := g.middlewares[meta.Use.Name]; mw != nil {
				collectStatuses(mw.Before, set)
				collectStatuses(mw.After, set)
			}
		}
	}
	if ep.OnError != nil && ep.OnError.Status != "" {
		set[ep.OnError.Status] = true
	}
	if g.file.Blueprint != nil {
		for _, use := range g.file.Blueprint.Uses {
			if mw := g.middlewares[use.Name]; mw != nil {
				collectStatuses(mw.Before, set)
				collectStatuses(mw.After, set)
			}
		}
	}
	statuses := make([]string, 0, len(set))
	for status := range set {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	return statuses
}

func (g *Generator) propertyArbitrary(t ast.TypeExpr, constraints []*ast.Constraint_, idx propertyTypeIndex, visiting map[string]bool) (string, error) {
	base, err := g.propertyArbitraryBase(t, constraints, idx, visiting)
	if err != nil {
		return "", err
	}
	if propertyInputOptional(constraints) {
		base = "fc.option(" + base + ", { nil: undefined })"
	}
	return base, nil
}

func (g *Generator) propertyArbitraryBase(t ast.TypeExpr, constraints []*ast.Constraint_, idx propertyTypeIndex, visiting map[string]bool) (string, error) {
	switch value := t.(type) {
	case *ast.PrimitiveType:
		return propertyPrimitiveArbitrary(value.Name, constraints)
	case *ast.TypedJSONType:
		return g.propertyArbitrary(value.Inner, constraints, idx, visiting)
	case *ast.TranslationKeyType:
		if len(value.Keys) == 0 {
			return propertyStringArbitrary(constraints)
		}
		return propertyConstantsArbitrary(value.Keys), nil
	case *ast.EnumInline:
		if len(value.Variants) == 0 {
			return "", fmt.Errorf("inline enum has no variants")
		}
		return propertyConstantsArbitrary(value.Variants), nil
	case *ast.ListType:
		if propertyHasConstraint(constraints, "format") {
			return "", fmt.Errorf("format constraints are not supported on lists")
		}
		element, err := g.propertyArbitrary(value.Element, nil, idx, visiting)
		if err != nil {
			return "", err
		}
		min, max, err := propertyLengthBounds(constraints, 0, 4)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("fc.array(%s, { minLength: %d, maxLength: %d })", element, min, max), nil
	case *ast.MapType:
		key, ok := value.Key.(*ast.PrimitiveType)
		if !ok || (key.Name != "string" && key.Name != "text") {
			return "", fmt.Errorf("map keys must be string/text for JSON request generation")
		}
		if propertyHasValidationConstraints(constraints) {
			return "", fmt.Errorf("min/max/format constraints are not supported on maps")
		}
		item, err := g.propertyArbitrary(value.Value, nil, idx, visiting)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("fc.dictionary(fc.string({ minLength: 1, maxLength: 12 }), %s, { maxKeys: 4 })", item), nil
	case *ast.NamedType:
		if variants := g.enumVariants[value.Name]; len(variants) > 0 {
			if propertyHasValidationConstraints(constraints) {
				return "", fmt.Errorf("min/max/format constraints are not supported on named enums")
			}
			return propertyConstantsArbitrary(variants), nil
		}
		if visiting[value.Name] {
			return "", fmt.Errorf("recursive named type %q cannot be bounded safely", value.Name)
		}
		if alias := idx.aliases[value.Name]; alias != nil {
			visiting[value.Name] = true
			combined := append(append([]*ast.Constraint_{}, alias.Constraints...), constraints...)
			arb, err := g.propertyArbitrary(alias.Type, combined, idx, visiting)
			delete(visiting, value.Name)
			return arb, err
		}
		if decl := idx.types[value.Name]; decl != nil {
			if propertyHasValidationConstraints(constraints) {
				return "", fmt.Errorf("min/max/format constraints are not supported on object type %q", value.Name)
			}
			visiting[value.Name] = true
			fields := make([]string, 0, len(decl.Fields))
			for _, field := range decl.Fields {
				arb, err := g.propertyArbitrary(field.Type, field.Constraints, idx, visiting)
				if err != nil {
					delete(visiting, value.Name)
					return "", fmt.Errorf("field %s.%s: %w", value.Name, field.Name, err)
				}
				fields = append(fields, fmt.Sprintf("%s: %s", toCamelCase(field.Name), arb))
			}
			delete(visiting, value.Name)
			return "fc.record({ " + strings.Join(fields, ", ") + " })", nil
		}
		return "", fmt.Errorf("named type %q is not an enum, alias, or structural type", value.Name)
	case *ast.MimeTypeExpr:
		return "", fmt.Errorf("MIME/file inputs cannot be represented by JSON property requests")
	default:
		return "", fmt.Errorf("unsupported input type %T", t)
	}
}

func propertyPrimitiveArbitrary(name string, constraints []*ast.Constraint_) (string, error) {
	switch name {
	case "string", "text":
		return propertyStringArbitrary(constraints)
	case "uuid":
		min, max, err := propertyLengthBounds(constraints, 36, 36)
		if err != nil {
			return "", err
		}
		if min > 36 || max < 36 {
			return "", fmt.Errorf("uuid length constraints exclude all UUID values")
		}
		return "fc.uuid()", nil
	case "int":
		min, max, err := propertyIntegerBounds(constraints, -1000, 1000)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("fc.integer({ min: %d, max: %d })", min, max), nil
	case "float", "money":
		min, max, err := propertyNumberBounds(constraints, -1000, 1000)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("fc.double({ min: %s, max: %s, noNaN: true, noDefaultInfinity: true })", formatPropertyFloat(min), formatPropertyFloat(max)), nil
	case "bool":
		if propertyHasValidationConstraints(constraints) {
			return "", fmt.Errorf("min/max/format constraints are not supported on bool")
		}
		return "fc.boolean()", nil
	case "timestamp":
		if propertyHasValidationConstraints(constraints) {
			return "", fmt.Errorf("min/max/format constraints are not supported on timestamp")
		}
		return "fc.date({ noInvalidDate: true }).map((value) => value.toISOString())", nil
	case "json":
		if propertyHasValidationConstraints(constraints) {
			return "", fmt.Errorf("min/max/format constraints are not supported on json")
		}
		return "fc.jsonValue()", nil
	case "file":
		return "", fmt.Errorf("file inputs require multipart transport, which property tests do not generate")
	default:
		return "", fmt.Errorf("unknown primitive type %q", name)
	}
}

func propertyStringArbitrary(constraints []*ast.Constraint_) (string, error) {
	min, max, err := propertyLengthBounds(constraints, 0, 64)
	if err != nil {
		return "", err
	}
	format := ""
	for _, constraint := range constraints {
		if constraint.Kind == "format" {
			format = exprToString(constraint.Value)
		}
	}
	var base string
	switch format {
	case "", "uuid", "ip", "date":
		// Node's constraintsToZod currently enforces email/url only. Mirror the
		// executable schema instead of promising a stricter generator contract.
		base = fmt.Sprintf("fc.string({ minLength: %d, maxLength: %d })", min, max)
	case "email":
		// fast-check's shortest email is a@a.aa: one local-part
		// character plus its shortest generated dotted domain.
		if max < 6 {
			return "", fmt.Errorf("email length constraints exclude all generated email values (minimum length is 6)")
		}
		base = "fc.emailAddress()"
	case "url":
		// webUrl() defaults to http(s) and a dotted domain, so its shortest
		// value is http://a.aa.
		if max < 11 {
			return "", fmt.Errorf("URL length constraints exclude all generated URL values (minimum length is 11)")
		}
		base = "fc.webUrl()"
	default:
		return "", fmt.Errorf("unsupported string format %q", format)
	}
	if format == "email" || format == "url" {
		base += fmt.Sprintf(".filter((value) => value.length >= %d && value.length <= %d)", min, max)
	}
	return base, nil
}

func propertyConstantsArbitrary(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return "fc.constantFrom(" + strings.Join(quoted, ", ") + ")"
}

func propertyLengthBounds(constraints []*ast.Constraint_, defaultMin, defaultMax int) (int, int, error) {
	min, max := defaultMin, defaultMax
	hasExplicitMin := false
	hasExplicitMax := false
	for _, constraint := range constraints {
		switch constraint.Kind {
		case "min", "max":
			value, err := propertyConstraintInt(constraint)
			if err != nil {
				return 0, 0, err
			}
			if constraint.Kind == "min" {
				if !hasExplicitMin || value > min {
					min = value
				}
				hasExplicitMin = true
			} else {
				if !hasExplicitMax || value < max {
					max = value
				}
				hasExplicitMax = true
			}
		}
	}
	if !hasExplicitMax && max < min && defaultMax < min {
		max = min + 64
	}
	if min < 0 || max < min {
		return 0, 0, fmt.Errorf("invalid length bounds min=%d max=%d", min, max)
	}
	return min, max, nil
}

func propertyIntegerBounds(constraints []*ast.Constraint_, defaultMin, defaultMax int64) (int64, int64, error) {
	min, max := defaultMin, defaultMax
	hasExplicitMin := false
	hasExplicitMax := false
	for _, constraint := range constraints {
		switch constraint.Kind {
		case "min", "max":
			value, err := propertyConstraintInt64(constraint)
			if err != nil {
				return 0, 0, err
			}
			if constraint.Kind == "min" {
				if !hasExplicitMin || value > min {
					min = value
				}
				hasExplicitMin = true
			} else {
				if !hasExplicitMax || value < max {
					max = value
				}
				hasExplicitMax = true
			}
		}
	}
	if !hasExplicitMax && max < min && defaultMax < min {
		max = min + 2000
	}
	if max < min {
		return 0, 0, fmt.Errorf("invalid numeric bounds min=%d max=%d", min, max)
	}
	return min, max, nil
}

func propertyNumberBounds(constraints []*ast.Constraint_, defaultMin, defaultMax float64) (float64, float64, error) {
	min, max := defaultMin, defaultMax
	hasExplicitMin := false
	hasExplicitMax := false
	for _, constraint := range constraints {
		switch constraint.Kind {
		case "min", "max":
			value, err := propertyConstraintFloat(constraint)
			if err != nil {
				return 0, 0, err
			}
			if constraint.Kind == "min" {
				if !hasExplicitMin || value > min {
					min = value
				}
				hasExplicitMin = true
			} else {
				if !hasExplicitMax || value < max {
					max = value
				}
				hasExplicitMax = true
			}
		}
	}
	if !hasExplicitMax && max < min && defaultMax < min {
		max = min + 2000
	}
	if max < min {
		return 0, 0, fmt.Errorf("invalid numeric bounds min=%s max=%s", formatPropertyFloat(min), formatPropertyFloat(max))
	}
	return min, max, nil
}

func propertyConstraintInt(constraint *ast.Constraint_) (int, error) {
	value, err := propertyConstraintInt64(constraint)
	if err != nil {
		return 0, err
	}
	converted := int(value)
	if int64(converted) != value {
		return 0, fmt.Errorf("%s constraint is outside the supported integer range", constraint.Kind)
	}
	return converted, nil
}

func propertyConstraintInt64(constraint *ast.Constraint_) (int64, error) {
	literal, ok := constraint.Value.(*ast.IntLit)
	if !ok {
		return 0, fmt.Errorf("%s constraint must be an integer literal", constraint.Kind)
	}
	value, err := strconv.ParseInt(literal.Value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s constraint %q", constraint.Kind, literal.Value)
	}
	return value, nil
}

func propertyConstraintFloat(constraint *ast.Constraint_) (float64, error) {
	var raw string
	switch value := constraint.Value.(type) {
	case *ast.IntLit:
		raw = value.Value
	case *ast.FloatLit:
		raw = value.Value
	default:
		return 0, fmt.Errorf("%s constraint must be a numeric literal", constraint.Kind)
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s constraint %q", constraint.Kind, raw)
	}
	return parsed, nil
}

func formatPropertyFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func propertyHasConstraint(constraints []*ast.Constraint_, kind string) bool {
	for _, constraint := range constraints {
		if constraint.Kind == kind {
			return true
		}
	}
	return false
}

func propertyHasValidationConstraints(constraints []*ast.Constraint_) bool {
	return propertyHasConstraint(constraints, "min") || propertyHasConstraint(constraints, "max") || propertyHasConstraint(constraints, "format")
}
