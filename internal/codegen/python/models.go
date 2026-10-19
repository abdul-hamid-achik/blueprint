package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

// genSchemaPy emits src/models/schema.py — SQLAlchemy 2.0 declarative models.
// One class per Blueprint `model`. Field types and constraints map directly:
// uuid primary → primary_key + default=uuid4; string required → String, nullable=False;
// timestamp default(now) → DateTime default=datetime.utcnow; ref(other) → ForeignKey.
//
// The metadata exported here is what alembic/env.py imports for autogenerate.
func (g *Generator) genSchemaPy(models []*ast.Model) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	imports := g.schemaImports(models)
	sort.Strings(imports)
	for _, imp := range imports {
		b.WriteString(imp)
		b.WriteString("\n")
	}
	b.WriteString("\n\n")
	b.WriteString("class Base(DeclarativeBase):\n    pass\n\n")

	for i, m := range models {
		if i > 0 {
			b.WriteString("\n")
		}
		g.emitSQLAlchemyModel(&b, m)
	}
	return codegen.OutputFile{Path: "src/models/schema.py", Content: []byte(b.String())}
}

// schemaImports collects the import lines that the schema module needs. The set
// is deduplicated and sorted in genSchemaPy.
func (g *Generator) schemaImports(models []*ast.Model) []string {
	set := map[string]bool{
		"from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column": true,
	}
	colTypes := map[string]bool{}
	needsForeignKey := false
	needsUUIDDefault := false
	needsDateTimeDefault := false
	hasOptional := false

	enumImports := newImportSet()
	needsMappedDatetime := false
	needsMappedUUID := false
	for _, m := range models {
		for _, f := range m.Fields {
			ct := g.columnTypeFor(m, f)
			colTypes[ct.importName] = true
			// Mapped[...] annotations name datetime and uuid.UUID for these
			// scalars, including through an alias, so the modules must be imported.
			if pt, ok := g.resolveAlias(f.Type).(*ast.PrimitiveType); ok {
				switch pt.Name {
				case "timestamp":
					needsMappedDatetime = true
				case "uuid":
					needsMappedUUID = true
				}
			}
			typeRefs(f.Type, func(ref string) {
				if g.namedKind(ref) == "enum" {
					enumImports.add(pyEnumsModule, pyClassName(ref))
				}
			})
			for _, c := range f.Constraints {
				switch c.Kind {
				case "ref":
					needsForeignKey = true
				case "default":
					if _, ok := c.Value.(*ast.NowLit); ok {
						needsDateTimeDefault = true
					}
				case "optional":
					hasOptional = true
				}
			}
			if pt, ok := f.Type.(*ast.PrimitiveType); ok && pt.Name == "uuid" {
				for _, c := range f.Constraints {
					if c.Kind == "primary" {
						needsUUIDDefault = true
					}
				}
			}
		}
	}

	cols := make([]string, 0, len(colTypes))
	for ct := range colTypes {
		cols = append(cols, ct)
	}
	sort.Strings(cols)
	if needsForeignKey {
		set["from sqlalchemy import "+strings.Join(append(cols, "ForeignKey"), ", ")] = true
	} else if len(cols) > 0 {
		set["from sqlalchemy import "+strings.Join(cols, ", ")] = true
	}
	if needsUUIDDefault || needsMappedUUID {
		set["import uuid"] = true
	}
	if needsDateTimeDefault || needsMappedDatetime {
		set["from datetime import datetime"] = true
	}
	if hasOptional {
		set["from typing import Optional"] = true
	}

	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	out = append(out, enumImports.lines()...)
	return out
}

// emitSQLAlchemyModel writes one declarative class. Table name = pluralized
// model name (matching the JS/Drizzle convention so the same DB works for
// both targets).
func (g *Generator) emitSQLAlchemyModel(b *strings.Builder, m *ast.Model) {
	className := common.PascalCase(m.Name)
	tableName := common.Pluralize(m.Name)

	if m.Intent != nil {
		fmt.Fprintf(b, "# %s\n", m.Intent.Text)
	}
	fmt.Fprintf(b, "class %s(Base):\n", className)
	fmt.Fprintf(b, "    __tablename__ = %q\n\n", tableName)

	for _, f := range m.Fields {
		g.emitSQLAlchemyField(b, m, f)
	}
}

// columnTypeFor is sqlAlchemyColumnType with the owning model in scope. An
// inline enum column must be named: Postgres cannot create or compile an
// unnamed ENUM. The name follows Node's rule, lowercase(model + Field), so both
// targets create the same type for the same column.
func (g *Generator) columnTypeFor(m *ast.Model, f *ast.Field) colType {
	ct := g.sqlAlchemyColumnType(f.Type)
	if _, inline := f.Type.(*ast.EnumInline); inline {
		ct.constructor = strings.TrimSuffix(ct.constructor, ")") +
			fmt.Sprintf(", name=%q)", inlineEnumTypeName(m, f))
	}
	return ct
}

// inlineEnumTypeName is the Postgres type name for an inline enum model column.
func inlineEnumTypeName(m *ast.Model, f *ast.Field) string {
	return strings.ToLower(common.PascalCase(m.Name) + common.PascalCase(f.Name))
}

func (g *Generator) emitSQLAlchemyField(b *strings.Builder, m *ast.Model, f *ast.Field) {
	ct := g.columnTypeFor(m, f)
	pyType := g.sqlAlchemyMappedAnnotation(f.Type, f.Constraints)

	var args []string
	args = append(args, ct.constructor)

	// Constraints
	isPrimary, isUnique, isRequired, hasDefault := false, false, false, false
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
		case "ref":
			if ref := exprAsName(c.Value); ref != "" {
				// Foreign key to <plural>(id). The reference goes on the
				// existing column type, not as a separate arg.
				args = append(args, fmt.Sprintf("ForeignKey(%q)", common.Pluralize(ref)+".id"))
			}
		}
	}

	if isPrimary {
		args = append(args, "primary_key=True")
	}
	if isUnique {
		args = append(args, "unique=True")
	}
	// SQLAlchemy nullability inverts Blueprint's required/optional.
	if isRequired || isPrimary {
		args = append(args, "nullable=False")
	}

	for _, c := range f.Constraints {
		if c.Kind != "default" {
			continue
		}
		if d := g.sqlAlchemyDefault(f.Type, c.Value); d != "" {
			args = append(args, "default="+d)
			hasDefault = true
		}
	}
	// uuid primary keys get a Python-side default of uuid.uuid4.
	if isPrimary && !hasDefault {
		if pt, ok := f.Type.(*ast.PrimitiveType); ok && pt.Name == "uuid" {
			args = append(args, "default=uuid.uuid4")
		}
	}

	fmt.Fprintf(b, "    %s: Mapped[%s] = mapped_column(%s)\n",
		f.Name, pyType, strings.Join(args, ", "))
}

// sqlAlchemyColumnType carries both the import target (e.g. "String") and the
// constructor call used in mapped_column(...) (e.g. `String()`). For most
// primitives the two match; UUID needs an as_uuid kwarg and Enum needs the
// inline value list.
type colType struct {
	importName  string // what to import from sqlalchemy
	constructor string // first arg of mapped_column
}

func (g *Generator) sqlAlchemyColumnType(t ast.TypeExpr) colType {
	// An alias over a scalar stores as that scalar; the alias only changes the
	// Python annotation on the read model.
	if named, ok := t.(*ast.NamedType); ok && g.namedKind(named.Name) == "alias" {
		t = g.resolveAlias(t)
	}
	switch v := t.(type) {
	case *ast.NamedType:
		// A declared enum uses the Postgres type name Drizzle's pgEnum uses on
		// Node, so both targets create the same type. Values are the variant
		// strings, not the Python member names.
		return colType{"Enum", fmt.Sprintf(
			"Enum(%s, name=%q, values_callable=lambda e: [m.value for m in e])",
			pyClassName(v.Name), pgEnumTypeName(v.Name))}
	case *ast.PrimitiveType:
		switch v.Name {
		case "string":
			return colType{"String", "String()"}
		case "text":
			return colType{"Text", "Text()"}
		case "int", "money":
			return colType{"Integer", "Integer()"}
		case "float":
			return colType{"Float", "Float()"}
		case "bool":
			return colType{"Boolean", "Boolean()"}
		case "uuid":
			return colType{"UUID", "UUID(as_uuid=True)"}
		case "timestamp":
			return colType{"DateTime", "DateTime()"}
		case "json":
			return colType{"JSON", "JSON()"}
		}
	case *ast.TypedJSONType:
		return colType{"JSON", "JSON()"}
	case *ast.EnumInline:
		quoted := make([]string, len(v.Variants))
		for i, n := range v.Variants {
			quoted[i] = fmt.Sprintf("%q", n)
		}
		return colType{"Enum", fmt.Sprintf("Enum(%s)", strings.Join(quoted, ", "))}
	}
	// UUID lives in sqlalchemy.dialects.postgresql or the generic Uuid type
	// in 2.0. We use the generic to keep it portable; users can swap to
	// `from sqlalchemy.dialects.postgresql import UUID` if they need pg-specific.
	return colType{"String", "String()"}
}

// pythonAnnotationForField produces the Python annotation for non-SQLAlchemy
// contexts (e.g. Pydantic model fields when no special primitive mapping
// applies). Optional fields become Optional[X].
func (g *Generator) pythonAnnotationForField(t ast.TypeExpr, cs []*ast.Constraint_) string {
	base := g.pyAnnotation(t)
	// Inline enum model fields stay plain str on the read model; the column
	// keeps its string values and no Literal is emitted here.
	if _, inline := t.(*ast.EnumInline); inline || base == "" || strings.Contains(base, "TODO") {
		base = "str"
	}
	base = annotatedType(base, t, cs, pydanticFieldBounds(t, cs)...)
	for _, c := range cs {
		if c.Kind == "optional" {
			return "Optional[" + base + "]"
		}
	}
	return base
}

// pydanticFieldBounds returns the pydantic Field(...) argument for min/max on a
// model field, as a one-element slice, or nil. Bounds use the same mapping as
// endpoint inputs and type/alias fields: length for string, text, list, and
// map; value for int, float, and money. Other types carry no bounds, and a
// bound whose value does not fit the type is skipped.
func pydanticFieldBounds(t ast.TypeExpr, cs []*ast.Constraint_) []string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "string", "text", "int", "float", "money":
		default:
			return nil
		}
	case *ast.ListType, *ast.MapType:
	default:
		return nil
	}
	var bounds []*ast.Constraint_
	for _, c := range cs {
		if (c.Kind == "min" || c.Kind == "max") && pythonConstraintValueMatches(t, c.Value) {
			bounds = append(bounds, c)
		}
	}
	args := inputValidationArgs(t, bounds)
	if len(args) == 0 {
		return nil
	}
	return []string{"Field(" + strings.Join(args, ", ") + ")"}
}

// sqlAlchemyMappedAnnotation returns the type for `Mapped[<X>]` — different
// from pythonAnnotationForField because SQLAlchemy's UUID column maps to
// Python's uuid.UUID (not str) and DateTime maps to datetime.datetime.
// Routes/Pydantic use str/datetime via their own paths; this is only used
// inside src/models/schema.py.
func (g *Generator) sqlAlchemyMappedAnnotation(t ast.TypeExpr, cs []*ast.Constraint_) string {
	base := "str"
	if named, ok := t.(*ast.NamedType); ok && g.namedKind(named.Name) == "alias" {
		t = g.resolveAlias(t)
	}
	switch v := t.(type) {
	case *ast.NamedType:
		base = pyClassName(v.Name)
	case *ast.PrimitiveType:
		switch v.Name {
		case "uuid":
			base = "uuid.UUID"
		case "timestamp":
			base = "datetime"
		case "int", "money":
			base = "int"
		case "float":
			base = "float"
		case "bool":
			base = "bool"
		case "json":
			base = "dict"
		}
	case *ast.TypedJSONType:
		base = "dict"
	}
	for _, c := range cs {
		if c.Kind == "optional" {
			return "Optional[" + base + "]"
		}
	}
	return base
}

// sqlAlchemyDefault renders a Blueprint default-value expression as the
// Python value passed to mapped_column(default=...). Returns "" to skip.
func (g *Generator) sqlAlchemyDefault(t ast.TypeExpr, e ast.Expr) string {
	switch v := e.(type) {
	case *ast.NowLit:
		return "datetime.utcnow"
	case *ast.BoolLit:
		if v.Value {
			return "True"
		}
		return "False"
	case *ast.IntLit:
		return v.Value
	case *ast.FloatLit:
		return v.Value
	case *ast.StringLit:
		return fmt.Sprintf("%q", v.Value)
	case *ast.Ident:
		// Enum variant identifier. A declared enum uses its member (Status.x);
		// an inline enum or other column uses the quoted string.
		if rendered, ok := g.enumDefault(t, v.Name); ok {
			return rendered
		}
		return fmt.Sprintf("%q", v.Name)
	}
	return ""
}

func exprAsName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	if s, ok := e.(*ast.StringLit); ok {
		return s.Value
	}
	return ""
}

// genPydanticPy emits src/models/pydantic.py — one BaseModel per Blueprint
// model for response serialisation. Phase 2 keeps it to a single "read" view
// per model (every declared field); Create/Update partials can come later.
func (g *Generator) genPydanticPy(models []*ast.Model) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	needsUUID := false
	needsDateTime := false
	needsOptional := false
	needsFormats := false
	needsField := false
	for _, m := range models {
		for _, f := range m.Fields {
			if len(pydanticFieldBounds(f.Type, f.Constraints)) > 0 {
				needsField = true
			}
			if pt, ok := f.Type.(*ast.PrimitiveType); ok {
				if pt.Name == "uuid" {
					needsUUID = true
				}
				if pt.Name == "timestamp" {
					needsDateTime = true
				}
			}
			for _, c := range f.Constraints {
				if c.Kind == "optional" {
					needsOptional = true
				}
			}
			if formatTypeFor(f.Type, f.Constraints) != "" {
				needsFormats = true
			}
		}
	}
	pyd := []string{"BaseModel", "ConfigDict"}
	if needsField {
		pyd = append(pyd, "Field")
	}
	fmt.Fprintf(&b, "from pydantic import %s\n", strings.Join(pyd, ", "))
	if needsUUID {
		b.WriteString("from uuid import UUID\n")
	}
	if needsDateTime {
		b.WriteString("from datetime import datetime\n")
	}
	var typing []string
	if needsField {
		typing = append(typing, "Annotated")
	}
	if needsOptional {
		typing = append(typing, "Optional")
	}
	if len(typing) > 0 {
		fmt.Fprintf(&b, "from typing import %s\n", strings.Join(typing, ", "))
	}
	if needsFormats {
		b.WriteString("from src.lib import formats\n")
	}
	enumImports := newImportSet()
	for _, m := range models {
		for _, f := range m.Fields {
			typeRefs(f.Type, func(ref string) {
				switch g.namedKind(ref) {
				case "enum":
					enumImports.add(pyEnumsModule, pyClassName(ref))
				case "alias":
					enumImports.add(pyTypesModule, pyClassName(ref))
				}
			})
		}
	}
	for _, line := range enumImports.lines() {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")

	for i, m := range models {
		if i > 0 {
			b.WriteString("\n\n")
		}
		g.emitPydanticModel(&b, m)
	}
	return codegen.OutputFile{Path: "src/models/pydantic.py", Content: []byte(b.String())}
}

func (g *Generator) emitPydanticModel(b *strings.Builder, m *ast.Model) {
	className := common.PascalCase(m.Name)
	if m.Intent != nil {
		fmt.Fprintf(b, "# %s\n", m.Intent.Text)
	}
	fmt.Fprintf(b, "class %s(BaseModel):\n", className)
	b.WriteString("    model_config = ConfigDict(from_attributes=True)\n\n")
	for _, f := range m.Fields {
		annot := g.pythonAnnotationForField(f.Type, f.Constraints)
		// Pydantic primitive overrides: timestamp → datetime, uuid → UUID.
		if pt, ok := f.Type.(*ast.PrimitiveType); ok {
			switch pt.Name {
			case "uuid":
				if strings.HasPrefix(annot, "Optional[") {
					annot = "Optional[UUID]"
				} else {
					annot = "UUID"
				}
			case "timestamp":
				if strings.HasPrefix(annot, "Optional[") {
					annot = "Optional[datetime]"
				} else {
					annot = "datetime"
				}
			}
		}
		fmt.Fprintf(b, "    %s: %s\n", f.Name, annot)
	}
}
