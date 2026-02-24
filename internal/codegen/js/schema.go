package js

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// --- Types ---

func (g *Generator) genTypes(types []*ast.TypeDecl, aliases []*ast.Alias, enums []*ast.Enum) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	for _, e := range enums {
		name := e.Name
		isStruct := g.structEnums[e.Name]
		if isStruct {
			// Struct enum: generate plain string enum + separate config object
			b.WriteString(fmt.Sprintf("export const %s = {\n", name))
			for _, v := range e.Variants {
				b.WriteString(fmt.Sprintf("  %s: '%s',\n", v.Name, v.Name))
			}
			b.WriteString("} as const;\n")
			b.WriteString(fmt.Sprintf("export type %s = keyof typeof %s;\n\n", name, name))
			// Config object with struct field data
			b.WriteString(fmt.Sprintf("export const %sConfig: Record<string, any> = {\n", name))
			for _, v := range e.Variants {
				if v.Body != nil && len(v.Body.Entries) > 0 {
					parts := make([]string, len(v.Body.Entries))
					for i, kv := range v.Body.Entries {
						parts[i] = fmt.Sprintf("%s: %s", toCamelCase(kv.Key), exprToJS(kv.Value))
					}
					b.WriteString(fmt.Sprintf("  %s: { %s },\n", v.Name, strings.Join(parts, ", ")))
				} else {
					b.WriteString(fmt.Sprintf("  %s: {},\n", v.Name))
				}
			}
			b.WriteString("};\n\n")
		} else {
			b.WriteString(fmt.Sprintf("export const %s = {\n", name))
			for _, v := range e.Variants {
				b.WriteString(fmt.Sprintf("  %s: '%s',\n", v.Name, v.Name))
			}
			b.WriteString("} as const;\n")
			b.WriteString(fmt.Sprintf("export type %s = keyof typeof %s;\n\n", name, name))
		}
	}

	for _, a := range aliases {
		b.WriteString(fmt.Sprintf("export type %s = %s;\n\n", a.Name, typeToTS(a.Type)))
	}

	for _, t := range types {
		b.WriteString(fmt.Sprintf("export interface %s {\n", toPascalCase(t.Name)))
		for _, f := range t.Fields {
			opt := ""
			for _, c := range f.Constraints {
				if c.Kind == "optional" {
					opt = "?"
					break
				}
			}
			b.WriteString(fmt.Sprintf("  %s%s: %s;\n", toCamelCase(f.Name), opt, typeToTS(f.Type)))
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

	// Build enum name lookup for column type matching
	enumNames := make(map[string]string) // lower(name) -> varName
	for _, e := range enums {
		varName := toCamelCase(e.Name) + "Enum"
		variants := make([]string, len(e.Variants))
		for i, v := range e.Variants {
			variants[i] = fmt.Sprintf("'%s'", v.Name)
		}
		b.WriteString(fmt.Sprintf("export const %s = pgEnum('%s', [%s]);\n\n",
			varName, strings.ToLower(e.Name), strings.Join(variants, ", ")))
		enumNames[strings.ToLower(e.Name)] = varName
	}

	// Emit each model as a pgTable
	for _, m := range models {
		tableName := pluralize(m.Name)
		b.WriteString(fmt.Sprintf("export const %s = pgTable('%s', {\n", toCamelCase(m.Name), tableName))

		for _, f := range m.Fields {
			colName := toCamelCase(f.Name)

			// Check for named type matching a declared enum
			if nt, ok := f.Type.(*ast.NamedType); ok {
				if enumVar, exists := enumNames[strings.ToLower(nt.Name)]; exists {
					b.WriteString(fmt.Sprintf("  %s: %s('%s')", colName, enumVar, f.Name))
					g.writeFieldConstraints(&b, f)
					b.WriteString(",\n")
					continue
				}
			}

			// Check for inline enum — generate a pgEnum for it
			if ie, ok := f.Type.(*ast.EnumInline); ok {
				inlineEnumVar := toCamelCase(m.Name) + toPascalCase(f.Name) + "Enum"
				variants := make([]string, len(ie.Variants))
				for i, v := range ie.Variants {
					variants[i] = fmt.Sprintf("'%s'", v)
				}
				// Insert the enum declaration before this table (will be above in output)
				// For simplicity, use text with a check comment for now
				b.WriteString(fmt.Sprintf("  %s: text('%s')", colName, f.Name))
				_ = inlineEnumVar
				g.writeFieldConstraints(&b, f)
				b.WriteString(",\n")
				continue
			}

			colType := typeToDrizzle(f.Type)
			b.WriteString(fmt.Sprintf("  %s: %s('%s')", colName, colType, f.Name))
			g.writeFieldConstraints(&b, f)
			b.WriteString(",\n")
		}
		b.WriteString("});\n\n")

		// Emit TypeScript type from model
		b.WriteString(fmt.Sprintf("export type %s = typeof %s.$inferSelect;\n", toPascalCase(m.Name), toCamelCase(m.Name)))
		b.WriteString(fmt.Sprintf("export type New%s = typeof %s.$inferInsert;\n\n", toPascalCase(m.Name), toCamelCase(m.Name)))
	}

	return codegen.OutputFile{Path: "src/models/schema.ts", Content: []byte(b.String())}
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
				b.WriteString(fmt.Sprintf(".default(%s)", val))
			}
			// Fields with defaults are implicitly not-null in Drizzle
			if isRequired {
				b.WriteString(".notNull()")
			}
		case "ref":
			if ref := exprToString(c.Value); ref != "" {
				b.WriteString(fmt.Sprintf(".references(() => %s.id)", toCamelCase(ref)))
			}
		}
	}
}

// --- Validation ---

func (g *Generator) genValidation(endpoints []*ast.Endpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { z } from 'zod';\n\n")

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

		b.WriteString(fmt.Sprintf("export const %s = z.object({\n", schemaName))
		for _, inp := range inputs {
			var zodType string
			if isQuerySource {
				zodType = typeToZodCoerce(inp.Type)
			} else {
				zodType = typeToZod(inp.Type)
			}
			zodType += constraintsToZod(inp.Constraints)
			b.WriteString(fmt.Sprintf("  %s: %s,\n", toCamelCase(inp.Name), zodType))
		}
		b.WriteString("});\n\n")
	}

	return codegen.OutputFile{Path: "src/validation/schemas.ts", Content: []byte(b.String())}
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
