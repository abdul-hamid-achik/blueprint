// Package importer contains deliberately conservative source-to-Blueprint
// scaffolders. Importers preserve structural facts only; they never claim to
// translate imperative handler behavior.
package importer

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// Source is one TypeScript source file supplied to the scaffolder.
type Source struct {
	Path string
	Data []byte
}

// HandlerReport describes what was structurally recovered from one route and
// calls out the behavior that was intentionally not translated.
type HandlerReport struct {
	Source        string
	Line          int
	Method        string
	Path          string
	Inputs        int
	Mapped        []string
	Dropped       []string
	SchemaNames   []string
	DuplicateKey  bool
	SkippedReason string
}

// Report is the honest fidelity report returned with every scaffold.
type Report struct {
	Models        int
	Routes        int
	SkippedRoutes int
	Inputs        int
	Handlers      []HandlerReport
	Warnings      []string
}

// Result is a canonical, checker-validated Blueprint scaffold and its report.
type Result struct {
	File   *ast.File
	Source string
	Report Report
}

// Options configures TypeScript scaffolding.
type Options struct {
	Name string
}

type zodSchema struct {
	Name   string
	Fields []*ast.InputStmt
}

type zodSchemaUse struct {
	Name   string
	Target string
}

var (
	pgTableAssignment = regexp.MustCompile(`(?m)\b([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:[A-Za-z_$][A-Za-z0-9_$]*\s*\.\s*)?pgTable\s*\(`)
	pgEnumAssignment  = regexp.MustCompile(`(?m)\b([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:[A-Za-z_$][A-Za-z0-9_$]*\s*\.\s*)?pgEnum\s*\(`)
	zodAssignment     = regexp.MustCompile(`(?m)\b([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:z\s*\.\s*)?object\s*\(`)
	honoRouteCall     = regexp.MustCompile(`(?m)\.\s*(get|post|put|patch|delete|head|options|all)\s*\(`)
	honoBasePath      = regexp.MustCompile(`(?m)\b([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*[^;\n]*?\.\s*basePath\s*\(`)
	honoRouteMount    = regexp.MustCompile(`(?m)\.\s*route\s*\(`)
	zodValidatorCall  = regexp.MustCompile(`(?m)\b(?:[A-Za-z_$][A-Za-z0-9_$]*\s*\.\s*)?zValidator\s*\(`)
	zodCallName       = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*\s*\(`)
	identifierRE      = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	integerRE         = regexp.MustCompile(`^-?[0-9]+$`)
	floatRE           = regexp.MustCompile(`^-?[0-9]+\.[0-9]+$`)
)

// ImportTypeScript extracts Drizzle pgTable declarations, Hono routes, and
// referenced Zod object schemas. Handler bodies are always replaced by an
// explicit TODO plus a 501 response; this function is a rewrite scaffolder,
// not a behavior-preserving transpiler.
func ImportTypeScript(sources []Source, opts Options) (Result, error) {
	if len(sources) == 0 {
		return Result{}, fmt.Errorf("no TypeScript source files were provided")
	}
	name := normalizeName(opts.Name)
	if name == "" {
		name = "imported_service"
	}

	ordered := append([]Source(nil), sources...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })

	schemas := map[string]zodSchema{}
	var warnings []string
	for _, src := range ordered {
		defs, ws := extractZodSchemas(src)
		warnings = append(warnings, ws...)
		for key, def := range defs {
			if previous, exists := schemas[key]; exists {
				warnings = append(warnings, fmt.Sprintf("%s: duplicate Zod schema %s ignored (first seen with %d field(s))", src.Path, key, len(previous.Fields)))
				continue
			}
			schemas[key] = def
		}
	}

	drizzleEnums := map[string][]string{}
	for _, src := range ordered {
		defs, ws := extractDrizzleEnums(src)
		warnings = append(warnings, ws...)
		for name, variants := range defs {
			if _, exists := drizzleEnums[name]; exists {
				warnings = append(warnings, fmt.Sprintf("%s: duplicate Drizzle pgEnum builder %s ignored", src.Path, name))
				continue
			}
			drizzleEnums[name] = variants
		}
	}

	modelsByName := map[string]*ast.Model{}
	modelOrder := []string{}
	for _, src := range ordered {
		models, ws := extractDrizzleModels(src, drizzleEnums)
		warnings = append(warnings, ws...)
		for _, model := range models {
			if _, exists := modelsByName[model.Name]; exists {
				warnings = append(warnings, fmt.Sprintf("%s: duplicate imported model %s ignored", src.Path, model.Name))
				continue
			}
			modelsByName[model.Name] = model
			modelOrder = append(modelOrder, model.Name)
		}
	}
	sort.Strings(modelOrder)
	// A partial source selection can reference a Drizzle table that was not
	// imported. Keeping that ref would make the scaffold fail semantic checks,
	// so retain the field but drop only the unverifiable relationship and say so.
	for _, modelName := range modelOrder {
		model := modelsByName[modelName]
		for _, field := range model.Fields {
			kept := field.Constraints[:0]
			for _, constraint := range field.Constraints {
				if constraint.Kind == "ref" {
					if ref, ok := constraint.Value.(*ast.Ident); !ok || modelsByName[ref.Name] == nil {
						warnings = append(warnings, fmt.Sprintf("%s:%d: dropped unverifiable ref on %s.%s because the referenced table was not imported", field.Loc.File, field.Loc.Line, model.Name, field.Name))
						continue
					}
				}
				kept = append(kept, constraint)
			}
			field.Constraints = kept
		}
	}

	var endpoints []*ast.Endpoint
	var handlers []HandlerReport
	seenRoutes := map[string]bool{}
	for _, src := range ordered {
		routes, reports, ws := extractHonoRoutes(src, schemas, seenRoutes)
		warnings = append(warnings, ws...)
		endpoints = append(endpoints, routes...)
		handlers = append(handlers, reports...)
	}
	if len(modelsByName) == 0 && len(endpoints) == 0 {
		return Result{}, fmt.Errorf("no supported structure found (expected Drizzle pgTable declarations or Hono route calls)")
	}

	loc := lexer.Loc{File: "<import>", Line: 1, Col: 1}
	entries := []ast.KVPair{
		{Loc: loc, Key: "version", Value: &ast.StringLit{Loc: loc, Value: "0.1.0"}},
		{Loc: loc, Key: "port", Value: &ast.IntLit{Loc: loc, Value: "8080"}},
		{Loc: loc, Key: "runtime", Value: &ast.Ident{Loc: loc, Name: "node"}},
	}
	if len(modelsByName) > 0 {
		entries = append(entries, ast.KVPair{Loc: loc, Key: "database", Value: &ast.Ident{Loc: loc, Name: "postgres"}})
	}
	file := &ast.File{
		Loc: loc,
		Blueprint: &ast.Blueprint{
			Loc:     loc,
			Intent:  &ast.Intent{Loc: loc, Text: "Imported structural scaffold. Review every TODO before using this service."},
			Name:    name,
			Entries: entries,
		},
	}
	for _, modelName := range modelOrder {
		file.Blocks = append(file.Blocks, modelsByName[modelName])
	}
	for _, endpoint := range endpoints {
		file.Blocks = append(file.Blocks, endpoint)
	}

	printed := ast.Print(file)
	parsed, parseErrs := parser.ParseFile("<imported>.bp", []byte(printed))
	if len(parseErrs) > 0 {
		return Result{}, fmt.Errorf("internal import scaffold was not parseable: %s", parseErrs[0].Message)
	}
	if checkErrs := checker.Check(parsed); len(checkErrs) > 0 {
		return Result{}, fmt.Errorf("internal import scaffold failed semantic validation: %s", checkErrs[0].Message)
	}

	report := Report{Models: len(modelOrder), Routes: len(endpoints), Handlers: handlers, Warnings: dedupeStrings(warnings)}
	for _, handler := range handlers {
		report.Inputs += handler.Inputs
		if handler.SkippedReason != "" || handler.DuplicateKey {
			report.SkippedRoutes++
		}
	}
	return Result{File: parsed, Source: ast.Print(parsed), Report: report}, nil
}

func extractDrizzleEnums(src Source) (map[string][]string, []string) {
	text := string(src.Data)
	clean := stripComments(text)
	patternText := maskStringContents(clean)
	matches := pgEnumAssignment.FindAllStringSubmatchIndex(patternText, -1)
	result := map[string][]string{}
	var warnings []string
	for _, match := range matches {
		name := clean[match[2]:match[3]]
		open := match[1] - 1
		close := matchingDelimiter(clean, open, '(', ')')
		if close < 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: unterminated pgEnum %s skipped", src.Path, lineAt(text, open), name))
			continue
		}
		args := splitTopLevel(clean[open+1:close], ',')
		if len(args) < 2 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: pgEnum %s has no inline variants array", src.Path, lineAt(text, open), name))
			continue
		}
		array := strings.TrimSpace(args[1])
		if len(array) < 2 || array[0] != '[' {
			warnings = append(warnings, fmt.Sprintf("%s:%d: dynamic pgEnum variants for %s cannot be imported", src.Path, lineAt(text, open), name))
			continue
		}
		closeArray := matchingDelimiter(array, 0, '[', ']')
		if closeArray < 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: unterminated pgEnum variants for %s", src.Path, lineAt(text, open), name))
			continue
		}
		var variants []string
		valid := true
		for _, raw := range splitTopLevel(array[1:closeArray], ',') {
			variant, ok := stringLiteral(raw)
			if !ok || normalizeName(variant) != variant {
				valid = false
				break
			}
			variants = append(variants, variant)
		}
		if !valid || len(variants) == 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: pgEnum %s variants must be static Blueprint identifiers; enum was not imported", src.Path, lineAt(text, open), name))
			continue
		}
		result[name] = variants
	}
	return result, warnings
}

func extractDrizzleModels(src Source, enums map[string][]string) ([]*ast.Model, []string) {
	text := string(src.Data)
	clean := stripComments(text)
	patternText := maskStringContents(clean)
	matches := pgTableAssignment.FindAllStringSubmatchIndex(patternText, -1)
	var models []*ast.Model
	var warnings []string
	for _, match := range matches {
		variable := clean[match[2]:match[3]]
		open := match[1] - 1
		close := matchingDelimiter(clean, open, '(', ')')
		if close < 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: unterminated pgTable(%s) declaration skipped", src.Path, lineAt(text, open), variable))
			continue
		}
		args := splitTopLevel(clean[open+1:close], ',')
		if len(args) < 2 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: pgTable(%s) has no inline columns object", src.Path, lineAt(text, open), variable))
			continue
		}
		tableName, ok := stringLiteral(strings.TrimSpace(args[0]))
		if !ok {
			tableName = variable
			warnings = append(warnings, fmt.Sprintf("%s:%d: dynamic pgTable name for %s; inferred model name from the variable", src.Path, lineAt(text, open), variable))
		}
		object, ok := objectContents(strings.TrimSpace(args[1]))
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s:%d: non-inline columns object for %s cannot be imported", src.Path, lineAt(text, open), variable))
			continue
		}
		if len(args) > 2 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: pgTable indexes/extra configuration for %s were not imported; verify the model TODO", src.Path, lineAt(text, open), variable))
		}
		modelName := normalizeName(singularize(tableName))
		if modelName == "" {
			warnings = append(warnings, fmt.Sprintf("%s:%d: could not derive a Blueprint model name for %s", src.Path, lineAt(text, open), variable))
			continue
		}
		if modelName != tableName {
			warnings = append(warnings, fmt.Sprintf("%s:%d: Drizzle table %q was scaffolded as Blueprint model %q; verify naming and generated table mapping", src.Path, lineAt(text, open), tableName, modelName))
		}
		modelLoc := lexer.Loc{File: src.Path, Line: lineAt(text, open), Col: 1, Offset: open}
		model := &ast.Model{
			Loc:    modelLoc,
			Intent: &ast.Intent{Loc: modelLoc, Text: "TODO(import): verify this Drizzle model, defaults, indexes, and relationships."},
			Name:   modelName,
		}
		seenFields := map[string]bool{}
		for _, rawField := range splitTopLevel(object, ',') {
			key, value, ok := splitProperty(rawField)
			if !ok {
				if strings.TrimSpace(rawField) != "" {
					warnings = append(warnings, fmt.Sprintf("%s:%d: unsupported Drizzle column entry %q skipped", src.Path, modelLoc.Line, abbreviate(rawField)))
				}
				continue
			}
			field, warning := drizzleField(src.Path, modelLoc, key, value, enums)
			if warning != "" {
				warnings = append(warnings, warning)
			}
			if field != nil {
				if seenFields[field.Name] {
					warnings = append(warnings, fmt.Sprintf("%s:%d: duplicate imported field %s.%s ignored", src.Path, modelLoc.Line, modelName, field.Name))
					continue
				}
				seenFields[field.Name] = true
				model.Fields = append(model.Fields, field)
			}
		}
		if len(model.Fields) == 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: model %s had no supported inline columns and was skipped", src.Path, modelLoc.Line, modelName))
			continue
		}
		models = append(models, model)
	}
	return models, warnings
}

func drizzleField(path string, loc lexer.Loc, rawKey, value string, enums map[string][]string) (*ast.Field, string) {
	rawName := unquoteProperty(rawKey)
	propertyName := normalizeName(rawName)
	if propertyName == "" {
		return nil, fmt.Sprintf("%s:%d: unsupported Drizzle field name %q skipped", path, loc.Line, rawKey)
	}
	builder := firstBuilderName(value)
	typeName := mapDrizzleType(builder)
	var notes []string
	name := propertyName
	builderArgs := firstBuilderArgs(value)
	if len(builderArgs) == 0 {
		notes = append(notes, "could not verify the SQL column name")
	} else if sqlName, ok := stringLiteral(builderArgs[0]); ok {
		normalizedSQLName := normalizeName(sqlName)
		if normalizedSQLName == "" {
			notes = append(notes, fmt.Sprintf("could not represent SQL column name %q; used property name %q", sqlName, propertyName))
		} else {
			name = normalizedSQLName
			if name != propertyName {
				notes = append(notes, fmt.Sprintf("used SQL column name %q instead of Drizzle property %q", name, propertyName))
			}
		}
	} else {
		notes = append(notes, fmt.Sprintf("dynamic SQL column name; used property name %q", propertyName))
	}
	if len(builderArgs) > 1 {
		notes = append(notes, "dropped Drizzle builder options such as length, precision, scale, mode, or timezone")
	}
	if chainHas(value, "array") {
		return nil, fmt.Sprintf("%s:%d: Drizzle field %s uses an array column that Blueprint codegen cannot preserve and was skipped", path, loc.Line, name)
	}
	var typeExpr ast.TypeExpr
	if variants := enums[builder]; len(variants) > 0 {
		typeExpr = &ast.EnumInline{Loc: loc, Variants: append([]string(nil), variants...)}
	} else if typeName != "" {
		typeExpr = &ast.PrimitiveType{Loc: loc, Name: typeName}
	} else {
		return nil, fmt.Sprintf("%s:%d: Drizzle field %s uses unsupported builder %q and was skipped", path, loc.Line, name, builder)
	}
	fieldLoc := loc
	field := &ast.Field{Loc: fieldLoc, Name: name, Type: typeExpr}
	if rawName != name {
		notes = append(notes, fmt.Sprintf("renamed identifier %q to %q", rawName, name))
	}
	primary := chainHas(value, "primaryKey")
	if primary {
		field.Constraints = append(field.Constraints, &ast.Constraint_{Loc: fieldLoc, Kind: "primary"})
	}
	if chainHas(value, "unique") {
		field.Constraints = append(field.Constraints, &ast.Constraint_{Loc: fieldLoc, Kind: "unique"})
	}
	if chainHas(value, "notNull") && !primary {
		field.Constraints = append(field.Constraints, &ast.Constraint_{Loc: fieldLoc, Kind: "required"})
	} else if !primary {
		field.Constraints = append(field.Constraints, &ast.Constraint_{Loc: fieldLoc, Kind: "optional"})
	}
	if chainHas(value, "defaultNow") {
		field.Constraints = append(field.Constraints, &ast.Constraint_{Loc: fieldLoc, Kind: "default", Value: &ast.NowLit{Loc: fieldLoc}})
	} else if rawDefault, ok := chainArgument(value, "default"); ok {
		if literal := scalarLiteral(strings.TrimSpace(rawDefault), fieldLoc); literal != nil {
			field.Constraints = append(field.Constraints, &ast.Constraint_{Loc: fieldLoc, Kind: "default", Value: literal})
		} else {
			notes = append(notes, "dropped dynamic default")
		}
	}
	if chainHas(value, "generatedAlwaysAs") || chainHas(value, "generatedAlwaysAsIdentity") {
		notes = append(notes, "dropped generated/computed expression")
	}
	if chainHas(value, "defaultRandom") && !primary {
		notes = append(notes, "dropped defaultRandom() because Blueprint only generates random UUID defaults for primary UUID fields")
	}
	if builder == "serial" || builder == "smallserial" || builder == "bigserial" {
		notes = append(notes, "dropped serial identity semantics")
	}
	switch builder {
	case "bigint", "bigserial", "numeric", "decimal":
		notes = append(notes, fmt.Sprintf("mapped %s to Blueprint numeric semantics; verify range and precision", builder))
	case "date", "time":
		notes = append(notes, fmt.Sprintf("mapped SQL %s to Blueprint timestamp semantics", builder))
	case "bytea", "binary":
		notes = append(notes, fmt.Sprintf("mapped SQL %s to Blueprint file semantics", builder))
	}
	if ref, targetField, ok := regexpReference(value); ok {
		if targetField == "id" {
			field.Constraints = append(field.Constraints, &ast.Constraint_{Loc: fieldLoc, Kind: "ref", Value: &ast.Ident{Loc: fieldLoc, Name: normalizeName(singularize(ref))}})
		} else {
			notes = append(notes, fmt.Sprintf("dropped reference to %s.%s because Blueprint refs target id", ref, targetField))
		}
	} else if chainHas(value, "references") {
		notes = append(notes, "dropped dynamic or unsupported references() target")
	}
	if raw, ok := chainArgument(value, "references"); ok && len(splitTopLevel(raw, ',')) > 1 {
		notes = append(notes, "dropped references() actions/options such as onDelete or onUpdate")
	}
	knownCalls := map[string]bool{
		builder: true, "primaryKey": true, "unique": true, "notNull": true,
		"defaultNow": true, "default": true, "defaultRandom": true,
		"references": true, "generatedAlwaysAs": true, "generatedAlwaysAsIdentity": true,
	}
	for _, call := range callNames(value) {
		if !knownCalls[call] {
			notes = append(notes, fmt.Sprintf("dropped Drizzle call %s()", call))
		}
	}
	if strings.Contains(value, ".$type") {
		notes = append(notes, "dropped Drizzle $type metadata")
	}
	if len(notes) > 0 {
		return field, fmt.Sprintf("%s:%d: imported field %s with changes: %s; verify the model TODO", path, loc.Line, name, strings.Join(dedupeStrings(notes), "; "))
	}
	return field, ""
}

func extractZodSchemas(src Source) (map[string]zodSchema, []string) {
	result := map[string]zodSchema{}
	text := string(src.Data)
	clean := stripComments(text)
	patternText := maskStringContents(clean)
	matches := zodAssignment.FindAllStringSubmatchIndex(patternText, -1)
	var warnings []string
	for _, match := range matches {
		name := clean[match[2]:match[3]]
		open := match[1] - 1
		close := matchingDelimiter(clean, open, '(', ')')
		if close < 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: unterminated Zod object %s skipped", src.Path, lineAt(text, open), name))
			continue
		}
		args := splitTopLevel(clean[open+1:close], ',')
		if len(args) == 0 {
			continue
		}
		object, ok := objectContents(strings.TrimSpace(args[0]))
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s:%d: non-inline Zod object %s cannot be imported", src.Path, lineAt(text, open), name))
			continue
		}
		loc := lexer.Loc{File: src.Path, Line: lineAt(text, open), Col: 1, Offset: open}
		var fields []*ast.InputStmt
		for _, rawField := range splitTopLevel(object, ',') {
			key, value, ok := splitProperty(rawField)
			if !ok {
				continue
			}
			field, warning := zodInput(src.Path, loc, key, value)
			if warning != "" {
				warnings = append(warnings, warning)
			}
			if field != nil {
				fields = append(fields, field)
			}
		}
		result[name] = zodSchema{Name: name, Fields: fields}
	}
	return result, warnings
}

func zodInput(path string, loc lexer.Loc, rawKey, value string) (*ast.InputStmt, string) {
	rawName := unquoteProperty(rawKey)
	name := normalizeName(rawName)
	if name == "" {
		return nil, fmt.Sprintf("%s:%d: unsupported Zod field name %q skipped", path, loc.Line, rawKey)
	}
	for _, semantic := range []string{"transform", "preprocess", "pipe"} {
		if hasCall(value, semantic) {
			return nil, fmt.Sprintf("%s:%d: Zod field %s uses type-changing %s() semantics and was skipped", path, loc.Line, name, semantic)
		}
	}
	if hasCall(value, "nullable") || hasCall(value, "nullish") {
		return nil, fmt.Sprintf("%s:%d: Zod field %s is nullable/nullish; Blueprint optional values do not preserve explicit null semantics, so the field was skipped", path, loc.Line, name)
	}
	if hasCall(value, "optional") && !outerChainHas(value, "optional") {
		return nil, fmt.Sprintf("%s:%d: Zod field %s has nested optional element/member semantics that Blueprint cannot preserve and was skipped", path, loc.Line, name)
	}
	typeExpr, typeName := mapZodType(value, loc)
	if typeExpr == nil {
		return nil, fmt.Sprintf("%s:%d: Zod field %s uses an unsupported type and was skipped", path, loc.Line, name)
	}
	input := &ast.InputStmt{Loc: loc, Name: name, Type: typeExpr}
	var notes []string
	if rawName != name {
		notes = append(notes, fmt.Sprintf("renamed identifier %q to %q", rawName, name))
	}
	if outerChainHas(value, "optional") {
		input.Constraints = append(input.Constraints, &ast.Constraint_{Loc: loc, Kind: "optional"})
	} else {
		input.Constraints = append(input.Constraints, &ast.Constraint_{Loc: loc, Kind: "required"})
	}
	if raw, ok := outerChainArgument(value, "min"); ok {
		if constraint := zodBoundLiteral(strings.TrimSpace(raw), typeName, loc); constraint != nil {
			input.Constraints = append(input.Constraints, &ast.Constraint_{Loc: loc, Kind: "min", Value: constraint})
		} else {
			notes = append(notes, "dropped non-representable min() validation")
		}
	}
	if raw, ok := outerChainArgument(value, "max"); ok {
		if constraint := zodBoundLiteral(strings.TrimSpace(raw), typeName, loc); constraint != nil {
			input.Constraints = append(input.Constraints, &ast.Constraint_{Loc: loc, Kind: "max", Value: constraint})
		} else {
			notes = append(notes, "dropped non-representable max() validation")
		}
	}
	if raw, ok := outerChainArgument(value, "default"); ok {
		if literal := scalarLiteral(strings.TrimSpace(raw), loc); literal != nil {
			input.Constraints = removeConstraint(input.Constraints, "required")
			input.Constraints = append(input.Constraints, &ast.Constraint_{Loc: loc, Kind: "default", Value: literal})
		} else {
			notes = append(notes, "dropped dynamic default")
		}
	}
	if typeName == "string" {
		switch {
		case outerChainHas(value, "email") && outerChainHas(value, "url"):
			notes = append(notes, "dropped conflicting email()/url() validation")
		case outerChainHas(value, "email"):
			input.Constraints = append(input.Constraints, &ast.Constraint_{Loc: loc, Kind: "format", Value: &ast.Ident{Loc: loc, Name: "email"}})
		case outerChainHas(value, "url"):
			input.Constraints = append(input.Constraints, &ast.Constraint_{Loc: loc, Kind: "format", Value: &ast.Ident{Loc: loc, Name: "url"}})
		}
	}
	compact := strings.ReplaceAll(value, " ", "")
	if strings.Contains(compact, "z.coerce.") {
		notes = append(notes, "dropped Zod input coercion behavior")
	}
	if strings.Contains(compact, "z.object(") || strings.Contains(compact, "z.record(") {
		notes = append(notes, "collapsed nested Zod object/record validation to json")
	}
	if typeName == "list" && zodArrayElementHasSemantics(value) {
		notes = append(notes, "dropped Zod array-element constraints or defaults")
	}
	knownCalls := map[string]bool{
		"string": true, "number": true, "bigint": true, "boolean": true,
		"date": true, "any": true, "unknown": true, "record": true,
		"object": true, "array": true, "optional": true, "min": true,
		"max": true, "default": true, "email": true, "url": true,
		"uuid": true, "datetime": true, "int": true,
	}
	for _, call := range callNames(value) {
		if !knownCalls[call] {
			notes = append(notes, fmt.Sprintf("dropped Zod %s() semantics", call))
		}
	}
	if len(notes) > 0 {
		return input, fmt.Sprintf("%s:%d: imported Zod input %s with changes: %s", path, loc.Line, name, strings.Join(dedupeStrings(notes), "; "))
	}
	return input, ""
}

func extractHonoRoutes(src Source, schemas map[string]zodSchema, seen map[string]bool) ([]*ast.Endpoint, []HandlerReport, []string) {
	text := string(src.Data)
	clean := stripComments(text)
	patternText := maskStringContents(clean)
	basePaths, baseWarnings := extractHonoBasePaths(src.Path, text, clean, patternText)
	matches := honoRouteCall.FindAllStringSubmatchIndex(patternText, -1)
	var endpoints []*ast.Endpoint
	var reports []HandlerReport
	warnings := append([]string(nil), baseWarnings...)
	for _, mount := range honoRouteMount.FindAllStringIndex(patternText, -1) {
		open := mount[1] - 1
		close := matchingDelimiter(clean, open, '(', ')')
		if close < 0 {
			continue
		}
		args := splitTopLevel(clean[open+1:close], ',')
		if len(args) > 0 {
			prefix, _ := stringLiteral(args[0])
			warnings = append(warnings, fmt.Sprintf("%s:%d: Hono route mount %q was not flattened; import the mounted router sources and verify their prefix manually", src.Path, lineAt(text, open), prefix))
		}
	}
	for _, match := range matches {
		method := strings.ToUpper(clean[match[2]:match[3]])
		open := match[1] - 1
		close := matchingDelimiter(clean, open, '(', ')')
		if close < 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: unterminated Hono %s route skipped", src.Path, lineAt(text, open), method))
			continue
		}
		args := splitTopLevel(clean[open+1:close], ',')
		if len(args) < 2 {
			continue
		}
		path, ok := stringLiteral(strings.TrimSpace(args[0]))
		line := lineAt(text, open)
		if !ok || !strings.HasPrefix(path, "/") {
			reason := "dynamic/non-path route target cannot be represented"
			warnings = append(warnings, fmt.Sprintf("%s:%d: %s route skipped: %s", src.Path, line, method, reason))
			reports = append(reports, HandlerReport{Source: src.Path, Line: line, Method: method, Path: "<dynamic>", SkippedReason: reason, Dropped: []string{"route declaration and entire handler body"}})
			continue
		}
		if method == "HEAD" || method == "OPTIONS" || method == "ALL" {
			reason := fmt.Sprintf("Blueprint does not currently expose the %s endpoint method", method)
			warnings = append(warnings, fmt.Sprintf("%s:%d: %s %s skipped: %s", src.Path, line, method, path, reason))
			reports = append(reports, HandlerReport{Source: src.Path, Line: line, Method: method, Path: path, SkippedReason: reason, Dropped: []string{"route declaration and entire handler body"}})
			continue
		}
		if strings.ContainsAny(path, "*?{}") {
			reason := "wildcard, optional, and brace-style route segments are not representable"
			warnings = append(warnings, fmt.Sprintf("%s:%d: %s %s skipped: %s", src.Path, line, method, path, reason))
			reports = append(reports, HandlerReport{Source: src.Path, Line: line, Method: method, Path: path, SkippedReason: reason, Dropped: []string{"route declaration and entire handler body"}})
			continue
		}
		rawPath := path
		path = normalizeRoutePath(path)
		if rawPath != path {
			warnings = append(warnings, fmt.Sprintf("%s:%d: normalized Hono path %q to Blueprint path %q; verify parameter names", src.Path, line, rawPath, path))
		}
		receiver := receiverForRoute(clean, match[0])
		appliedBasePath := ""
		if prefix := basePaths[receiver]; prefix != "" {
			path = joinRoutePath(prefix, path)
			appliedBasePath = prefix
		}
		key := method + " " + path
		if seen[key] {
			warnings = append(warnings, fmt.Sprintf("%s:%d: duplicate route %s skipped", src.Path, line, key))
			reports = append(reports, HandlerReport{Source: src.Path, Line: line, Method: method, Path: path, DuplicateKey: true, Dropped: []string{"duplicate route and handler body"}})
			continue
		}
		seen[key] = true
		loc := lexer.Loc{File: src.Path, Line: line, Col: 1, Offset: open}
		callText := clean[open+1 : close]
		schemaUses, schemaUseWarnings := referencedSchemas(callText, schemas)
		for _, warning := range schemaUseWarnings {
			warnings = append(warnings, fmt.Sprintf("%s:%d: %s on %s", src.Path, line, warning, key))
		}
		if regexp.MustCompile(`\bz\s*\.\s*object\s*\(`).MatchString(maskStringContents(callText)) {
			warnings = append(warnings, fmt.Sprintf("%s:%d: inline Zod object on %s was not imported; name the schema and pass it to the validator", src.Path, line, key))
		}
		var inputs []*ast.InputStmt
		inputNames := map[string]bool{}
		pathNames := map[string]bool{}
		for _, param := range pathParams(path) {
			pathNames[param] = true
		}
		// Param validators take precedence so a path declaration keeps its
		// source type/constraints when the same name also appears elsewhere.
		sort.SliceStable(schemaUses, func(i, j int) bool {
			return schemaUses[i].Target == "param" && schemaUses[j].Target != "param"
		})
		expectedTarget := "json"
		if method == "GET" || method == "DELETE" {
			expectedTarget = "query"
		}
		var schemaNames []string
		usedSchemas := map[string]bool{}
		for _, use := range schemaUses {
			compatible := use.Target == expectedTarget || use.Target == "param"
			if !compatible {
				warnings = append(warnings, fmt.Sprintf("%s:%d: skipped Zod schema %s from %s validator on %s; Blueprint maps %s inputs to %s and cannot preserve this transport", src.Path, line, use.Name, use.Target, key, method, expectedTarget))
				continue
			}
			lifted := false
			for _, sourceInput := range schemas[use.Name].Fields {
				if use.Target == "param" && !pathNames[sourceInput.Name] {
					warnings = append(warnings, fmt.Sprintf("%s:%d: skipped %s.%s because the param validator field is not present in route path %s", src.Path, line, use.Name, sourceInput.Name, path))
					continue
				}
				if use.Target != "param" && pathNames[sourceInput.Name] {
					warnings = append(warnings, fmt.Sprintf("%s:%d: skipped %s.%s from the %s validator because it collides with path parameter :%s", src.Path, line, use.Name, sourceInput.Name, use.Target, sourceInput.Name))
					continue
				}
				if inputNames[sourceInput.Name] {
					warnings = append(warnings, fmt.Sprintf("%s:%d: duplicate input %s from route schemas on %s ignored", src.Path, line, sourceInput.Name, key))
					continue
				}
				clone := cloneInput(sourceInput, loc)
				inputs = append(inputs, clone)
				inputNames[clone.Name] = true
				lifted = true
			}
			if lifted && !usedSchemas[use.Name] {
				schemaNames = append(schemaNames, use.Name)
				usedSchemas[use.Name] = true
			}
		}
		for _, param := range pathParams(path) {
			if inputNames[param] {
				continue
			}
			inputs = append(inputs, &ast.InputStmt{Loc: loc, Name: param, Type: &ast.PrimitiveType{Loc: loc, Name: "string"}, Constraints: []*ast.Constraint_{{Loc: loc, Kind: "required"}}})
			inputNames[param] = true
		}
		todo := fmt.Sprintf("TODO(import): rewrite the original %s %s handler; imperative behavior was not preserved.", method, path)
		stmts := make([]ast.ArrowStmt, 0, len(inputs)+2)
		for _, input := range inputs {
			stmts = append(stmts, input)
		}
		stmts = append(stmts,
			&ast.IntentStep{Loc: loc, Text: todo},
			&ast.OutputStmt{Loc: loc, Status: "501", Value: &ast.BlockExpr{Loc: loc, Entries: []ast.KVPair{{Loc: loc, Key: "error", Value: &ast.StringLit{Loc: loc, Value: todo}}}}},
		)
		endpoint := &ast.Endpoint{Loc: loc, Intent: &ast.Intent{Loc: loc, Text: todo}, Method: method, Path: path, Stmts: stmts}
		endpoints = append(endpoints, endpoint)
		mapped := []string{"HTTP method", "path"}
		if len(schemaNames) > 0 {
			mapped = append(mapped, "transport-compatible zValidator input schema")
		}
		if len(pathParams(path)) > 0 {
			mapped = append(mapped, "path parameters")
		}
		if appliedBasePath != "" {
			mapped = append(mapped, "Hono basePath")
		}
		reports = append(reports, HandlerReport{
			Source: src.Path, Line: line, Method: method, Path: path, Inputs: len(inputs), Mapped: mapped,
			Dropped: []string{"entire imperative handler body"}, SchemaNames: schemaNames,
		})
	}
	return endpoints, reports, warnings
}

func extractHonoBasePaths(path, original, clean, patternText string) (map[string]string, []string) {
	result := map[string]string{}
	var warnings []string
	for _, match := range honoBasePath.FindAllStringSubmatchIndex(patternText, -1) {
		name := clean[match[2]:match[3]]
		open := match[1] - 1
		close := matchingDelimiter(clean, open, '(', ')')
		if close < 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: unterminated Hono basePath for %s", path, lineAt(original, open), name))
			continue
		}
		args := splitTopLevel(clean[open+1:close], ',')
		if len(args) == 0 {
			continue
		}
		prefix, ok := stringLiteral(args[0])
		if !ok || !strings.HasPrefix(prefix, "/") {
			warnings = append(warnings, fmt.Sprintf("%s:%d: dynamic Hono basePath for %s was not applied", path, lineAt(original, open), name))
			continue
		}
		result[name] = normalizeRoutePath(prefix)
	}
	return result, warnings
}

func mapDrizzleType(builder string) string {
	switch builder {
	case "uuid":
		return "uuid"
	case "varchar", "text", "char":
		return "string"
	case "integer", "smallint", "serial", "smallserial", "bigint", "bigserial":
		return "int"
	case "real", "doublePrecision", "numeric", "decimal":
		return "float"
	case "boolean":
		return "bool"
	case "timestamp", "date", "time":
		return "timestamp"
	case "json", "jsonb":
		return "json"
	case "bytea", "binary":
		return "file"
	default:
		return ""
	}
}

func mapZodType(value string, loc lexer.Loc) (ast.TypeExpr, string) {
	compact := strings.ReplaceAll(value, " ", "")
	primitive := func(name string) (ast.TypeExpr, string) { return &ast.PrimitiveType{Loc: loc, Name: name}, name }
	if strings.Contains(compact, "z.array(") || strings.HasPrefix(compact, "array(") {
		open := strings.Index(compact, "array(") + len("array")
		if open >= len("array") {
			close := matchingDelimiter(compact, open, '(', ')')
			if close > open {
				inner, _ := mapZodType(compact[open+1:close], loc)
				if inner != nil {
					return &ast.ListType{Loc: loc, Element: inner}, "list"
				}
			}
		}
	}
	switch {
	case strings.Contains(compact, "z.record(") || strings.Contains(compact, "z.object(") || strings.Contains(compact, "z.unknown()") || strings.Contains(compact, "z.any()"):
		return primitive("json")
	case strings.Contains(compact, "z.string()") || strings.HasPrefix(compact, "string()"):
		if strings.Contains(compact, ".uuid(") || strings.Contains(compact, ".uuid()") {
			return primitive("uuid")
		}
		if strings.Contains(compact, ".datetime(") || strings.Contains(compact, ".datetime()") {
			return primitive("timestamp")
		}
		return primitive("string")
	case strings.Contains(compact, "z.number()") || strings.Contains(compact, "z.coerce.number()") || strings.HasPrefix(compact, "number()"):
		if strings.Contains(compact, ".int(") || strings.Contains(compact, ".int()") {
			return primitive("int")
		}
		return primitive("float")
	case strings.Contains(compact, "z.bigint()") || strings.HasPrefix(compact, "bigint()"):
		return primitive("int")
	case strings.Contains(compact, "z.boolean()") || strings.HasPrefix(compact, "boolean()"):
		return primitive("bool")
	case strings.Contains(compact, "z.date()") || strings.HasPrefix(compact, "date()"):
		return primitive("timestamp")
	default:
		return nil, ""
	}
}

func firstBuilderName(value string) string {
	re := regexp.MustCompile(`^\s*(?:[A-Za-z_$][A-Za-z0-9_$]*\s*\.\s*)?([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	m := re.FindStringSubmatch(value)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func firstBuilderArgs(value string) []string {
	re := regexp.MustCompile(`^\s*(?:[A-Za-z_$][A-Za-z0-9_$]*\s*\.\s*)?[A-Za-z_$][A-Za-z0-9_$]*\s*\(`)
	match := re.FindStringIndex(maskStringContents(value))
	if match == nil {
		return nil
	}
	open := match[1] - 1
	close := matchingDelimiter(value, open, '(', ')')
	if close < 0 {
		return nil
	}
	return splitTopLevel(value[open+1:close], ',')
}

func callNames(value string) []string {
	matches := zodCallName.FindAllString(maskStringContents(value), -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(match), "("))
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

func hasCall(value, name string) bool {
	for _, call := range callNames(value) {
		if call == name {
			return true
		}
	}
	return false
}

// outerZodSuffix returns only the chain applied to the field's outer schema.
// This prevents an element constraint such as z.array(z.string().min(1)) from
// being misread as a minimum length on the array itself.
func outerZodSuffix(value string) string {
	masked := maskStringContents(value)
	open := strings.IndexByte(masked, '(')
	if open < 0 {
		return ""
	}
	close := matchingDelimiter(value, open, '(', ')')
	if close < 0 || close+1 >= len(value) {
		return ""
	}
	return value[close+1:]
}

func outerChainHas(value, method string) bool {
	return chainHas(outerZodSuffix(value), method)
}

func outerChainArgument(value, method string) (string, bool) {
	return chainArgument(outerZodSuffix(value), method)
}

func zodArrayElementHasSemantics(value string) bool {
	masked := maskStringContents(value)
	re := regexp.MustCompile(`(?:\bz\s*\.\s*)?\barray\s*\(`)
	match := re.FindStringIndex(masked)
	if match == nil {
		return false
	}
	open := match[1] - 1
	close := matchingDelimiter(value, open, '(', ')')
	if close < 0 {
		return true
	}
	typeOnlyCalls := map[string]bool{
		"string": true, "number": true, "bigint": true, "boolean": true,
		"date": true, "any": true, "unknown": true, "record": true,
		"object": true, "array": true, "uuid": true, "datetime": true,
		"int": true,
	}
	for _, call := range callNames(value[open+1 : close]) {
		if !typeOnlyCalls[call] {
			return true
		}
	}
	return false
}

func zodBoundLiteral(raw, typeName string, loc lexer.Loc) ast.Expr {
	if integerRE.MatchString(raw) {
		return &ast.IntLit{Loc: loc, Value: raw}
	}
	if (typeName == "float" || typeName == "money") && floatRE.MatchString(raw) {
		return &ast.FloatLit{Loc: loc, Value: raw}
	}
	return nil
}

func referencedSchemas(call string, schemas map[string]zodSchema) ([]zodSchemaUse, []string) {
	args := splitTopLevel(call, ',')
	var structuralArgs []string
	for _, arg := range args[1:] {
		masked := maskStringContents(arg)
		if strings.Contains(masked, "=>") || regexp.MustCompile(`\bfunction\b`).MatchString(masked) {
			continue
		}
		structuralArgs = append(structuralArgs, arg)
	}
	var uses []zodSchemaUse
	var warnings []string
	recognized := map[string]bool{}
	seenUses := map[string]bool{}
	for _, arg := range structuralArgs {
		masked := maskStringContents(arg)
		for _, match := range zodValidatorCall.FindAllStringIndex(masked, -1) {
			open := match[1] - 1
			close := matchingDelimiter(arg, open, '(', ')')
			if close < 0 {
				warnings = append(warnings, "unterminated zValidator(...) was not imported")
				continue
			}
			validatorArgs := splitTopLevel(arg[open+1:close], ',')
			if len(validatorArgs) < 2 {
				warnings = append(warnings, "zValidator(...) without a static target and named schema was not imported")
				continue
			}
			target, targetOK := stringLiteral(validatorArgs[0])
			schemaName := strings.TrimSpace(validatorArgs[1])
			if !targetOK || !identifierRE.MatchString(schemaName) {
				warnings = append(warnings, "dynamic zValidator target/schema was not imported")
				continue
			}
			if _, ok := schemas[schemaName]; !ok {
				warnings = append(warnings, fmt.Sprintf("zValidator %s schema %s is not a supported named inline Zod object", target, schemaName))
				continue
			}
			recognized[schemaName] = true
			key := target + "\x00" + schemaName
			if !seenUses[key] {
				uses = append(uses, zodSchemaUse{Name: schemaName, Target: target})
				seenUses[key] = true
			}
		}
	}
	searchableParts := make([]string, len(structuralArgs))
	for i, arg := range structuralArgs {
		searchableParts[i] = maskStringContents(arg)
	}
	searchable := strings.Join(searchableParts, "\n")
	var names []string
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		if re.MatchString(searchable) && !recognized[name] {
			warnings = append(warnings, fmt.Sprintf("schema %s was mentioned outside a recognized zValidator(\"target\", %s) call and was not imported", name, name))
		}
	}
	return uses, warnings
}

func cloneInput(input *ast.InputStmt, loc lexer.Loc) *ast.InputStmt {
	clone := &ast.InputStmt{Loc: loc, Name: input.Name, Type: cloneType(input.Type, loc)}
	for _, c := range input.Constraints {
		clone.Constraints = append(clone.Constraints, &ast.Constraint_{Loc: loc, Kind: c.Kind, Value: c.Value})
	}
	return clone
}

func cloneType(t ast.TypeExpr, loc lexer.Loc) ast.TypeExpr {
	switch value := t.(type) {
	case *ast.PrimitiveType:
		return &ast.PrimitiveType{Loc: loc, Name: value.Name}
	case *ast.ListType:
		return &ast.ListType{Loc: loc, Element: cloneType(value.Element, loc)}
	case *ast.MapType:
		return &ast.MapType{Loc: loc, Key: cloneType(value.Key, loc), Value: cloneType(value.Value, loc)}
	default:
		return t
	}
}

func normalizeRoutePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[i] = ":" + normalizeName(strings.TrimPrefix(part, ":"))
		}
	}
	return strings.Join(parts, "/")
}

func receiverBeforeDot(src string, dot int) string {
	i := dot - 1
	for i >= 0 && unicode.IsSpace(rune(src[i])) {
		i--
	}
	end := i + 1
	for i >= 0 {
		r := rune(src[i])
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$') {
			break
		}
		i--
	}
	if end <= i+1 {
		return ""
	}
	return src[i+1 : end]
}

func receiverForRoute(src string, dot int) string {
	if receiver := receiverBeforeDot(src, dot); receiver != "" {
		return receiver
	}
	start := dot - 1
	for start >= 0 && src[start] != ';' && src[start] != '\n' {
		start--
	}
	segment := maskStringContents(src[start+1 : dot])
	re := regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*\.\s*(?:get|post|put|patch|delete|head|options|all)\s*\(`)
	match := re.FindStringSubmatch(segment)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func joinRoutePath(prefix, route string) string {
	if prefix == "/" || prefix == "" {
		return route
	}
	if route == "/" {
		return strings.TrimSuffix(prefix, "/")
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(route, "/")
}

func pathParams(path string) []string {
	var params []string
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, ":") && len(part) > 1 {
			params = append(params, normalizeName(part[1:]))
		}
	}
	return params
}

func normalizeName(value string) string {
	runes := []rune(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for i, r := range runes {
		if unicode.IsUpper(r) {
			previousIsLowerOrDigit := i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]))
			endsAcronym := i > 0 && unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if (previousIsLowerOrDigit || endsAcronym) && b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
			}
		}
		valid := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
		if !valid {
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}
		if r == '_' {
			if b.Len() == 0 || lastUnderscore {
				continue
			}
			lastUnderscore = true
		} else {
			lastUnderscore = false
		}
		b.WriteRune(unicode.ToLower(r))
	}
	result := strings.Trim(b.String(), "_")
	if result != "" && result[0] >= '0' && result[0] <= '9' {
		result = "imported_" + result
	}
	if result == "computed" || lexer.LookupKeyword(result) != lexer.TokenIdent {
		result += "_value"
	}
	return result
}

func singularize(value string) string {
	lower := strings.ToLower(value)
	irregular := map[string]string{"people": "person", "men": "man", "women": "woman", "children": "child", "teeth": "tooth", "feet": "foot", "mice": "mouse", "geese": "goose"}
	if v, ok := irregular[lower]; ok {
		return v
	}
	switch {
	case strings.HasSuffix(lower, "ies") && len(value) > 3:
		return value[:len(value)-3] + "y"
	case strings.HasSuffix(lower, "sses"), strings.HasSuffix(lower, "shes"), strings.HasSuffix(lower, "ches"), strings.HasSuffix(lower, "xes"), strings.HasSuffix(lower, "zes"):
		return value[:len(value)-2]
	case strings.HasSuffix(lower, "s") && !strings.HasSuffix(lower, "ss") && len(value) > 1:
		return value[:len(value)-1]
	default:
		return value
	}
}

func stripComments(src string) string {
	b := []byte(src)
	out := append([]byte(nil), b...)
	for i := 0; i < len(b); {
		switch {
		case i+1 < len(b) && b[i] == '/' && b[i+1] == '/':
			out[i], out[i+1] = ' ', ' '
			i += 2
			for i < len(b) && b[i] != '\n' {
				out[i] = ' '
				i++
			}
		case i+1 < len(b) && b[i] == '/' && b[i+1] == '*':
			out[i], out[i+1] = ' ', ' '
			i += 2
			for i < len(b) {
				if i+1 < len(b) && b[i] == '*' && b[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i += 2
					break
				}
				if b[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
		case b[i] == '\'' || b[i] == '"' || b[i] == '`':
			quote := b[i]
			i++
			for i < len(b) {
				if b[i] == '\\' {
					i += 2
					continue
				}
				if b[i] == quote {
					i++
					break
				}
				i++
			}
		default:
			i++
		}
	}
	return string(out)
}

// maskStringContents preserves byte offsets and quote delimiters while hiding
// text inside strings/templates from source-pattern regular expressions.
func maskStringContents(src string) string {
	b := []byte(src)
	out := append([]byte(nil), b...)
	for i := 0; i < len(b); i++ {
		if b[i] != '\'' && b[i] != '"' && b[i] != '`' {
			continue
		}
		quote := b[i]
		for i++; i < len(b); i++ {
			if b[i] == '\\' {
				out[i] = ' '
				if i+1 < len(b) {
					i++
					out[i] = ' '
				}
				continue
			}
			if b[i] == quote {
				break
			}
			if b[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	return string(out)
}

func matchingDelimiter(src string, open int, left, right byte) int {
	if open < 0 || open >= len(src) || src[open] != left {
		return -1
	}
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '\'', '"', '`':
			quote := src[i]
			for i++; i < len(src); i++ {
				if src[i] == '\\' {
					i++
					continue
				}
				if src[i] == quote {
					break
				}
			}
		default:
			if src[i] == left {
				depth++
			} else if src[i] == right {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

func splitTopLevel(src string, separator byte) []string {
	var result []string
	start := 0
	paren, brace, bracket := 0, 0, 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\'', '"', '`':
			quote := src[i]
			for i++; i < len(src); i++ {
				if src[i] == '\\' {
					i++
					continue
				}
				if src[i] == quote {
					break
				}
			}
		case '(':
			paren++
		case ')':
			paren--
		case '{':
			brace++
		case '}':
			brace--
		case '[':
			bracket++
		case ']':
			bracket--
		default:
			if src[i] == separator && paren == 0 && brace == 0 && bracket == 0 {
				result = append(result, strings.TrimSpace(src[start:i]))
				start = i + 1
			}
		}
	}
	result = append(result, strings.TrimSpace(src[start:]))
	return result
}

func splitProperty(src string) (string, string, bool) {
	parts := splitTopLevel(src, ':')
	if len(parts) < 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(strings.Join(parts[1:], ":")), true
}

func objectContents(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value[0] != '{' {
		return "", false
	}
	close := matchingDelimiter(value, 0, '{', '}')
	if close < 0 {
		return "", false
	}
	return value[1:close], true
}

func stringLiteral(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || (value[0] != '\'' && value[0] != '"' && value[0] != '`') || value[len(value)-1] != value[0] {
		return "", false
	}
	if value[0] == '`' && strings.Contains(value, "${") {
		return "", false
	}
	if value[0] == '`' {
		return value[1 : len(value)-1], true
	}
	unquoted, err := strconv.Unquote(`"` + strings.ReplaceAll(strings.ReplaceAll(value[1:len(value)-1], `\"`, `"`), `"`, `\"`) + `"`)
	if err != nil {
		return value[1 : len(value)-1], true
	}
	return unquoted, true
}

func unquoteProperty(value string) string {
	if literal, ok := stringLiteral(strings.TrimSpace(value)); ok {
		return literal
	}
	if identifierRE.MatchString(strings.TrimSpace(value)) {
		return strings.TrimSpace(value)
	}
	return ""
}

func chainHas(value, method string) bool {
	re := regexp.MustCompile(`\.\s*` + regexp.QuoteMeta(method) + `\s*\(`)
	return re.MatchString(value)
}

func chainArgument(value, method string) (string, bool) {
	re := regexp.MustCompile(`\.\s*` + regexp.QuoteMeta(method) + `\s*\(`)
	loc := re.FindStringIndex(value)
	if loc == nil {
		return "", false
	}
	open := strings.LastIndex(value[loc[0]:loc[1]], "(") + loc[0]
	close := matchingDelimiter(value, open, '(', ')')
	if close < 0 {
		return "", false
	}
	return value[open+1 : close], true
}

func regexpReference(value string) (string, string, bool) {
	re := regexp.MustCompile(`\.\s*references\s*\(\s*\(\s*\)\s*=>\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\.\s*([A-Za-z_$][A-Za-z0-9_$]*)`)
	m := re.FindStringSubmatch(value)
	if len(m) == 3 {
		return m[1], m[2], true
	}
	return "", "", false
}

func scalarLiteral(value string, loc lexer.Loc) ast.Expr {
	if literal, ok := stringLiteral(value); ok {
		return &ast.StringLit{Loc: loc, Value: literal}
	}
	if integerRE.MatchString(value) {
		return &ast.IntLit{Loc: loc, Value: value}
	}
	if floatRE.MatchString(value) {
		return &ast.FloatLit{Loc: loc, Value: value}
	}
	if value == "true" || value == "false" {
		return &ast.BoolLit{Loc: loc, Value: value == "true"}
	}
	return nil
}

func removeConstraint(values []*ast.Constraint_, kind string) []*ast.Constraint_ {
	result := values[:0]
	for _, value := range values {
		if value.Kind != kind {
			result = append(result, value)
		}
	}
	return result
}

func lineAt(src string, offset int) int {
	if offset < 0 {
		return 1
	}
	if offset > len(src) {
		offset = len(src)
	}
	return 1 + strings.Count(src[:offset], "\n")
}

func abbreviate(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 72 {
		return value[:69] + "..."
	}
	return value
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
