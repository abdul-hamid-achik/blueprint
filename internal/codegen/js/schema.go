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

	// Build enum name lookup for column type matching
	enumNames := make(map[string]string) // lower(name) -> varName
	for _, e := range enums {
		varName := toCamelCase(e.Name) + "Enum"
		variants := make([]string, len(e.Variants))
		for i, v := range e.Variants {
			variants[i] = fmt.Sprintf("'%s'", v.Name)
		}
		fmt.Fprintf(&b, "export const %s = pgEnum('%s', [%s]);\n\n",
			varName, strings.ToLower(e.Name), strings.Join(variants, ", "))
		enumNames[strings.ToLower(e.Name)] = varName
	}

	// Collect and emit inline enums from model fields
	// Map: enum key (modelName_fieldName) -> { varName, variants }
	inlineEnums := make(map[string]struct {
		varName  string
		variants []string
	})
	for _, m := range models {
		for _, f := range m.Fields {
			if ie, ok := f.Type.(*ast.EnumInline); ok {
				key := m.Name + "_" + f.Name
				ienumVariants := make([]string, len(ie.Variants))
				for i, v := range ie.Variants {
					ienumVariants[i] = fmt.Sprintf("'%s'", v)
				}
				inlineEnums[key] = struct {
					varName  string
					variants []string
				}{
					varName:  toCamelCase(m.Name) + toPascalCase(f.Name) + "Enum",
					variants: ienumVariants,
				}
			}
		}
	}
	// Generate pgEnum declarations for inline enums
	for _, ie := range inlineEnums {
		enumName := strings.ReplaceAll(ie.varName, "Enum", "")
		fmt.Fprintf(&b, "export const %s = pgEnum('%s', [%s]);\n\n",
			ie.varName, strings.ToLower(enumName), strings.Join(ie.variants, ", "))
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

			// Check for inline enum — use the generated pgEnum
			if _, ok := f.Type.(*ast.EnumInline); ok {
				inlineEnumVar := toCamelCase(m.Name) + toPascalCase(f.Name) + "Enum"
				// Use the generated pgEnum instead of text
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

		// Emit TypeScript type from model
		fmt.Fprintf(&b, "export type %s = typeof %s.$inferSelect;\n", toPascalCase(m.Name), toCamelCase(m.Name))
		fmt.Fprintf(&b, "export type New%s = typeof %s.$inferInsert;\n\n", toPascalCase(m.Name), toCamelCase(m.Name))
	}

	return codegen.OutputFile{Path: "src/models/schema.ts", Content: []byte(b.String())}
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
