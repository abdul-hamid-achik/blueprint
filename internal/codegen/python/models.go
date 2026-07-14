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

	imports := schemaImports(models)
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
		emitSQLAlchemyModel(&b, m)
	}
	return codegen.OutputFile{Path: "src/models/schema.py", Content: []byte(b.String())}
}

// schemaImports collects the import lines that the schema module needs. The set
// is deduplicated and sorted in genSchemaPy.
func schemaImports(models []*ast.Model) []string {
	set := map[string]bool{
		"from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column": true,
	}
	colTypes := map[string]bool{}
	needsForeignKey := false
	needsUUIDDefault := false
	needsDateTimeDefault := false
	hasOptional := false

	for _, m := range models {
		for _, f := range m.Fields {
			ct := sqlAlchemyColumnType(f.Type)
			colTypes[ct.importName] = true
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
	if needsUUIDDefault {
		set["import uuid"] = true
	}
	if needsDateTimeDefault {
		set["from datetime import datetime"] = true
	}
	if hasOptional {
		set["from typing import Optional"] = true
	}

	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	return out
}

// emitSQLAlchemyModel writes one declarative class. Table name = pluralized
// model name (matching the JS/Drizzle convention so the same DB works for
// both targets).
func emitSQLAlchemyModel(b *strings.Builder, m *ast.Model) {
	className := common.PascalCase(m.Name)
	tableName := common.Pluralize(m.Name)

	if m.Intent != nil {
		fmt.Fprintf(b, "# %s\n", m.Intent.Text)
	}
	fmt.Fprintf(b, "class %s(Base):\n", className)
	fmt.Fprintf(b, "    __tablename__ = %q\n\n", tableName)

	for _, f := range m.Fields {
		emitSQLAlchemyField(b, f)
	}
}

func emitSQLAlchemyField(b *strings.Builder, f *ast.Field) {
	ct := sqlAlchemyColumnType(f.Type)
	pyType := sqlAlchemyMappedAnnotation(f.Type, f.Constraints)

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
		if d := sqlAlchemyDefault(c.Value); d != "" {
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

func sqlAlchemyColumnType(t ast.TypeExpr) colType {
	switch v := t.(type) {
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
func pythonAnnotationForField(t ast.TypeExpr, cs []*ast.Constraint_) string {
	base := pythonTypeAnnotation(t)
	if base == "" || strings.Contains(base, "TODO") {
		base = "str"
	}
	for _, c := range cs {
		if c.Kind == "optional" {
			return "Optional[" + base + "]"
		}
	}
	return base
}

// sqlAlchemyMappedAnnotation returns the type for `Mapped[<X>]` — different
// from pythonAnnotationForField because SQLAlchemy's UUID column maps to
// Python's uuid.UUID (not str) and DateTime maps to datetime.datetime.
// Routes/Pydantic use str/datetime via their own paths; this is only used
// inside src/models/schema.py.
func sqlAlchemyMappedAnnotation(t ast.TypeExpr, cs []*ast.Constraint_) string {
	base := "str"
	switch v := t.(type) {
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
func sqlAlchemyDefault(e ast.Expr) string {
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
		// Enum variant identifier — quote as a string.
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
	b.WriteString("from pydantic import BaseModel, ConfigDict\n")

	needsUUID := false
	needsDateTime := false
	needsOptional := false
	for _, m := range models {
		for _, f := range m.Fields {
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
		}
	}
	if needsUUID {
		b.WriteString("from uuid import UUID\n")
	}
	if needsDateTime {
		b.WriteString("from datetime import datetime\n")
	}
	if needsOptional {
		b.WriteString("from typing import Optional\n")
	}
	b.WriteString("\n")

	for i, m := range models {
		if i > 0 {
			b.WriteString("\n\n")
		}
		emitPydanticModel(&b, m)
	}
	return codegen.OutputFile{Path: "src/models/pydantic.py", Content: []byte(b.String())}
}

func emitPydanticModel(b *strings.Builder, m *ast.Model) {
	className := common.PascalCase(m.Name)
	if m.Intent != nil {
		fmt.Fprintf(b, "# %s\n", m.Intent.Text)
	}
	fmt.Fprintf(b, "class %s(BaseModel):\n", className)
	b.WriteString("    model_config = ConfigDict(from_attributes=True)\n\n")
	for _, f := range m.Fields {
		annot := pythonAnnotationForField(f.Type, f.Constraints)
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
