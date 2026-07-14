package js

import (
	"fmt"
	"slices"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// --- Types ---

func (g *Generator) genTypes(types []*ast.TypeDecl, aliases []*ast.Alias, enums []*ast.Enum, states []*ast.StateMachine) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	for _, e := range enums {
		name := e.Name
		isStruct := g.structEnums[e.Name]
		if isStruct {
			// Struct enum: generate plain string enum + separate config object
			fmt.Fprintf(&b, "export const %s = {\n", name)
			for _, v := range e.Variants {
				fmt.Fprintf(&b, "  %s: '%s',\n", v.Name, v.Name)
			}
			b.WriteString("} as const;\n")
			fmt.Fprintf(&b, "export type %s = keyof typeof %s;\n\n", name, name)
			// Config object with struct field data
			fmt.Fprintf(&b, "export const %sConfig: Record<string, any> = {\n", name)
			for _, v := range e.Variants {
				if v.Body != nil && len(v.Body.Entries) > 0 {
					parts := make([]string, len(v.Body.Entries))
					for i, kv := range v.Body.Entries {
						parts[i] = fmt.Sprintf("%s: %s", toCamelCase(kv.Key), exprToJS(kv.Value))
					}
					fmt.Fprintf(&b, "  %s: { %s },\n", v.Name, strings.Join(parts, ", "))
				} else {
					fmt.Fprintf(&b, "  %s: {},\n", v.Name)
				}
			}
			b.WriteString("};\n\n")
		} else {
			fmt.Fprintf(&b, "export const %s = {\n", name)
			for _, v := range e.Variants {
				fmt.Fprintf(&b, "  %s: '%s',\n", v.Name, v.Name)
			}
			b.WriteString("} as const;\n")
			fmt.Fprintf(&b, "export type %s = keyof typeof %s;\n\n", name, name)
		}
	}

	for _, a := range aliases {
		fmt.Fprintf(&b, "export type %s = %s;\n\n", a.Name, typeToTS(a.Type))
	}

	for _, s := range states {
		statesName := toPascalCase(s.Name)
		stateVals := make([]string, len(s.States))
		for i, state := range s.States {
			stateVals[i] = fmt.Sprintf("%q", state)
		}
		fmt.Fprintf(&b, "export const %sStates = [%s] as const;\n", statesName, strings.Join(stateVals, ", "))
		fmt.Fprintf(&b, "export type %s = (typeof %sStates)[number];\n", statesName, statesName)
		transitionMap := make(map[string][]string)
		for _, st := range s.States {
			transitionMap[st] = []string{}
		}
		for _, tr := range s.Transitions {
			transitionMap[tr.From] = append(transitionMap[tr.From], tr.To)
		}
		parts := make([]string, 0, len(s.States))
		for _, st := range s.States {
			targets := transitionMap[st]
			quoted := make([]string, len(targets))
			for i, t := range targets {
				quoted[i] = fmt.Sprintf("%q", t)
			}
			parts = append(parts, fmt.Sprintf("  %s: [%s]", st, strings.Join(quoted, ", ")))
		}
		fmt.Fprintf(&b, "export const %sTransitions = {\n%s\n} as const;\n\n", statesName, strings.Join(parts, ",\n"))
	}

	for _, t := range types {
		fmt.Fprintf(&b, "export interface %s {\n", toPascalCase(t.Name))
		for _, f := range t.Fields {
			opt := ""
			for _, c := range f.Constraints {
				if c.Kind == "optional" {
					opt = "?"
					break
				}
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", toCamelCase(f.Name), opt, typeToTS(f.Type))
		}
		b.WriteString("}\n\n")
	}

	return codegen.OutputFile{Path: "src/types.ts", Content: []byte(b.String())}
}

// --- Models / Schema ---

func (g *Generator) genSchema(models []*ast.Model, enums []*ast.Enum) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { pgTable, pgEnum, text, integer, real, boolean, uuid, timestamp, jsonb } from 'drizzle-orm/pg-core';\n\n")

	namedTypes := collectNamedTypesFromModels(models)
	if len(namedTypes) > 0 {
		fmt.Fprintf(&b, "import type { %s } from '../types.js';\n\n", strings.Join(namedTypes, ", "))
	}

	// Build the enum-name lookup for column type matching, plus a registry of
	// every pgEnum keyed by its Postgres type name. A declared enum and an
	// inline enum that lower to the same type name must NOT emit two
	// `pgEnum('name', ...)` declarations — that yields duplicate `CREATE TYPE`
	// statements which collide in a drizzle-kit migration.
	enumNames := make(map[string]string) // lower(name) -> varName
	type pgEnumReg struct {
		varName  string
		variants string // joined; same name + same variants → reuse, don't redeclare
	}
	pgEnums := make(map[string]pgEnumReg) // pgTypeName -> declaration
	for _, e := range enums {
		varName := toCamelCase(e.Name) + "Enum"
		variants := make([]string, len(e.Variants))
		for i, v := range e.Variants {
			variants[i] = fmt.Sprintf("'%s'", v.Name)
		}
		joined := strings.Join(variants, ", ")
		pgName := strings.ToLower(e.Name)
		fmt.Fprintf(&b, "export const %s = pgEnum('%s', [%s]);\n\n", varName, pgName, joined)
		enumNames[pgName] = varName
		pgEnums[pgName] = pgEnumReg{varName: varName, variants: joined}
	}

	// Resolve each inline-enum field to the pgEnum var its column references.
	// When an inline enum's type name + variants already exist (typically a
	// declared enum with the same variants), reuse that declaration instead of
	// emitting a duplicate. Iterating models/fields in source order also makes
	// the output deterministic (the previous map-iteration was not).
	inlineFieldEnumVar := make(map[string]string) // "model_field" -> varName
	for _, m := range models {
		for _, f := range m.Fields {
			ie, ok := f.Type.(*ast.EnumInline)
			if !ok {
				continue
			}
			key := m.Name + "_" + f.Name
			varName := toCamelCase(m.Name) + toPascalCase(f.Name) + "Enum"
			variants := make([]string, len(ie.Variants))
			for i, v := range ie.Variants {
				variants[i] = fmt.Sprintf("'%s'", v)
			}
			joined := strings.Join(variants, ", ")
			pgName := strings.ToLower(toCamelCase(m.Name) + toPascalCase(f.Name))
			if existing, seen := pgEnums[pgName]; seen {
				if existing.variants == joined {
					// Same type name + same variants — reference the existing decl.
					inlineFieldEnumVar[key] = existing.varName
					continue
				}
				// Same type name, different variants — disambiguate so two
				// genuinely distinct enums don't collide on CREATE TYPE.
				pgName = m.Name + "_" + f.Name
			}
			fmt.Fprintf(&b, "export const %s = pgEnum('%s', [%s]);\n\n", varName, pgName, joined)
			pgEnums[pgName] = pgEnumReg{varName: varName, variants: joined}
			inlineFieldEnumVar[key] = varName
		}
	}

	// Emit each model as a pgTable
	for _, m := range models {
		tableName := pluralize(m.Name)
		fmt.Fprintf(&b, "export const %s = pgTable('%s', {\n", toCamelCase(m.Name), tableName)

		for _, f := range m.Fields {
			colName := toCamelCase(f.Name)

			// Check for named type matching a declared enum
			if nt, ok := f.Type.(*ast.NamedType); ok {
				if enumVar, exists := enumNames[strings.ToLower(nt.Name)]; exists {
					fmt.Fprintf(&b, "  %s: %s('%s')", colName, enumVar, f.Name)
					g.writeFieldConstraints(&b, f)
					b.WriteString(",\n")
					continue
				}
			}

			// Check for inline enum — reference the resolved pgEnum (deduped
			// against declared enums above).
			if _, ok := f.Type.(*ast.EnumInline); ok {
				inlineEnumVar := inlineFieldEnumVar[m.Name+"_"+f.Name]
				fmt.Fprintf(&b, "  %s: %s('%s')", colName, inlineEnumVar, f.Name)
				g.writeFieldConstraints(&b, f)
				b.WriteString(",\n")
				continue
			}

			colType := typeToDrizzle(f.Type)
			fmt.Fprintf(&b, "  %s: %s('%s')", colName, colType, f.Name)
			if jt, ok := f.Type.(*ast.TypedJSONType); ok {
				fmt.Fprintf(&b, ".$type<%s>()", typeToTS(jt.Inner))
			}
			g.writeFieldConstraints(&b, f)
			b.WriteString(",\n")
		}
		b.WriteString("});\n\n")

		// Emit TypeScript types. Computed fields extend the selected record but
		// never the insert shape because they are not persisted columns.
		fmt.Fprintf(&b, "export type %s = typeof %s.$inferSelect", toPascalCase(m.Name), toCamelCase(m.Name))
		if len(m.ComputedFields) > 0 {
			b.WriteString(" & {\n")
			for _, field := range m.ComputedFields {
				fmt.Fprintf(&b, "  %s: %s;\n", toCamelCase(field.Name), typeToTS(field.Type))
			}
			b.WriteString("}")
		}
		b.WriteString(";\n")
		fmt.Fprintf(&b, "export type New%s = typeof %s.$inferInsert;\n", toPascalCase(m.Name), toCamelCase(m.Name))
		if len(m.ComputedFields) > 0 {
			fmt.Fprintf(&b, "export function compute%s(row: typeof %s.$inferSelect): %s;\n", toPascalCase(m.Name), toCamelCase(m.Name), toPascalCase(m.Name))
			fmt.Fprintf(&b, "export function compute%s(row: null): null;\n", toPascalCase(m.Name))
			fmt.Fprintf(&b, "export function compute%s(row: undefined): undefined;\n", toPascalCase(m.Name))
			fmt.Fprintf(&b, "export function compute%s(row: typeof %s.$inferSelect | null | undefined): %s | null | undefined {\n", toPascalCase(m.Name), toCamelCase(m.Name), toPascalCase(m.Name))
			b.WriteString("  if (row == null) return row;\n")
			b.WriteString("  const result: any = { ...row };\n")
			for _, field := range m.ComputedFields {
				fmt.Fprintf(&b, "  Object.defineProperty(result, %q, { enumerable: true, get: () => %s });\n", toCamelCase(field.Name), computedExprToJS(field.Expr, "result"))
			}
			fmt.Fprintf(&b, "  return result as %s;\n", toPascalCase(m.Name))
			b.WriteString("}\n")
		}
		b.WriteString("\n")
	}

	return codegen.OutputFile{Path: "src/models/schema.ts", Content: []byte(b.String())}
}

// computedExprToJS renders the pure expression subset accepted by the
// checker. Identifiers always refer to properties already present on result
// (persisted fields or an earlier computed field).
func computedExprToJS(expr ast.Expr, row string) string {
	switch value := expr.(type) {
	case *ast.StringLit:
		return fmt.Sprintf(`"%s"`, jsEscapeString(value.Value))
	case *ast.IntLit:
		return value.Value
	case *ast.FloatLit:
		return value.Value
	case *ast.BoolLit:
		if value.Value {
			return "true"
		}
		return "false"
	case *ast.Ident:
		return row + "." + toCamelCase(value.Name)
	case *ast.ParenExpr:
		return "(" + computedExprToJS(value.Expr, row) + ")"
	case *ast.UnaryExpr:
		op := value.Op
		if op == "not" {
			op = "!"
		}
		return op + computedExprToJS(value.Operand, row)
	case *ast.BinaryExpr:
		op := value.Op
		switch op {
		case "and":
			op = "&&"
		case "or":
			op = "||"
		case "==":
			op = "==="
		case "!=":
			op = "!=="
		}
		return fmt.Sprintf("%s %s %s", computedExprToJS(value.Left, row), op, computedExprToJS(value.Right, row))
	default:
		return "undefined /* checker rejected unsupported computed expression */"
	}
}

func collectNamedTypesFromModels(models []*ast.Model) []string {
	set := map[string]struct{}{}
	for _, m := range models {
		for _, f := range m.Fields {
			collectNamedTypesFromTypeExpr(f.Type, set)
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, toPascalCase(name))
	}
	slices.Sort(names)
	return names
}

func collectNamedTypesFromTypeExpr(t ast.TypeExpr, set map[string]struct{}) {
	switch v := t.(type) {
	case *ast.TypedJSONType:
		collectNamedTypesFromTypeExpr(v.Inner, set)
	case *ast.NamedType:
		set[v.Name] = struct{}{}
	case *ast.ListType:
		collectNamedTypesFromTypeExpr(v.Element, set)
	case *ast.MapType:
		collectNamedTypesFromTypeExpr(v.Key, set)
		collectNamedTypesFromTypeExpr(v.Value, set)
	}
}

// writeFieldConstraints writes Drizzle column constraints for a field.
func (g *Generator) writeFieldConstraints(b *strings.Builder, f *ast.Field) {
	// Check if this is a uuid primary key — add .defaultRandom()
	isUUIDPrimary := false
	if pt, ok := f.Type.(*ast.PrimitiveType); ok && pt.Name == "uuid" {
		for _, c := range f.Constraints {
			if c.Kind == "primary" {
				isUUIDPrimary = true
				break
			}
		}
	}

	hasDefault := false
	isRequired := false
	for _, c := range f.Constraints {
		if c.Kind == "default" {
			hasDefault = true
		}
		if c.Kind == "required" {
			isRequired = true
		}
	}

	for _, c := range f.Constraints {
		switch c.Kind {
		case "primary":
			if isUUIDPrimary {
				b.WriteString(".defaultRandom().primaryKey()")
			} else {
				b.WriteString(".primaryKey()")
			}
		case "required":
			// Emit .notNull() unless field has a default (Drizzle sets notNull implicitly with defaults)
			if !hasDefault {
				b.WriteString(".notNull()")
			}
		case "unique":
			b.WriteString(".unique()")
		case "default":
			if _, ok := c.Value.(*ast.NowLit); ok {
				b.WriteString(".defaultNow()")
			} else {
				val := exprToJS(c.Value)
				// Ident values (e.g., enum variant names like "pending") must be quoted as strings
				if _, ok := c.Value.(*ast.Ident); ok {
					val = fmt.Sprintf(`"%s"`, exprToString(c.Value))
				}
				fmt.Fprintf(b, ".default(%s)", val)
			}
			// Fields with defaults are implicitly not-null in Drizzle
			if isRequired {
				b.WriteString(".notNull()")
			}
		case "ref":
			if ref := exprToString(c.Value); ref != "" {
				fmt.Fprintf(b, ".references(() => %s.id)", toCamelCase(ref))
			}
		}
	}
}

// --- Validation ---

func (g *Generator) genValidation(endpoints []*ast.Endpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { z } from 'zod';\n")

	namedTypes := collectNamedTypesFromEndpointInputs(endpoints)
	if len(namedTypes) > 0 {
		schemaNames := make([]string, len(namedTypes))
		for i, name := range namedTypes {
			schemaNames[i] = frontendSchemaName(name)
		}
		fmt.Fprintf(&b, "import { %s } from '../types/schemas.js';\n", strings.Join(schemaNames, ", "))
	}
	b.WriteString("\n")

	emitted := make(map[string]bool) // track emitted schema names

	for _, ep := range endpoints {
		// Collect non-path-param inputs
		var inputs []*ast.InputStmt
		for _, s := range ep.Stmts {
			if inp, ok := s.(*ast.InputStmt); ok {
				if !isPathParam(inp.Name, ep.Path) {
					inputs = append(inputs, inp)
				}
			}
		}
		if len(inputs) == 0 {
			continue
		}

		schemaName := endpointSchemaName(ep.Method, ep.Path)
		if emitted[schemaName] {
			continue // skip duplicates
		}
		emitted[schemaName] = true

		isQuerySource := ep.Method == "GET" || ep.Method == "DELETE"

		fmt.Fprintf(&b, "export const %s = z.object({\n", schemaName)
		for _, inp := range inputs {
			var zodType string
			if isQuerySource {
				zodType = typeToZodCoerce(inp.Type)
			} else {
				zodType = typeToZod(inp.Type)
			}
			zodType += constraintsToZod(inp.Constraints)
			fmt.Fprintf(&b, "  %s: %s,\n", inp.Name, zodType)
		}
		b.WriteString("});\n\n")
	}

	return codegen.OutputFile{Path: "src/validation/schemas.ts", Content: []byte(b.String())}
}

func collectNamedTypesFromEndpointInputs(endpoints []*ast.Endpoint) []string {
	set := map[string]struct{}{}
	for _, ep := range endpoints {
		for _, stmt := range ep.Stmts {
			inp, ok := stmt.(*ast.InputStmt)
			if !ok {
				continue
			}
			collectNamedTypesFromTypeExpr(inp.Type, set)
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func endpointSchemaName(method, path string) string {
	// GET /api/todos -> getTodosSchema
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var meaningful []string
	for _, p := range parts {
		if p == "api" || p == "" || strings.HasPrefix(p, ":") {
			continue
		}
		meaningful = append(meaningful, toPascalCase(strings.ReplaceAll(p, "-", "_")))
	}
	return strings.ToLower(method) + strings.Join(meaningful, "") + "Schema"
}
