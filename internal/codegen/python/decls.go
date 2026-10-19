package python

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

// Named `enum`, `type`, and `alias` declarations.
//
// Python mirrors the Node target's shapes:
//   - enum Name { a b }      -> src/models/enums.py: class Name(str, enum.Enum)
//   - type Name { f T ... }  -> src/models/types.py: class Name(BaseModel)
//   - alias Name = T ...     -> src/models/types.py: Name = Annotated[...]
//
// Enum values are the exact variant strings. A declared enum on a model
// column maps to a SQLAlchemy Enum whose Postgres type name is the lowercased
// Blueprint name, the same name Drizzle's pgEnum uses on Node.

const (
	pyEnumsModule = "src.models.enums"
	pyTypesModule = "src.models.types"
	pyEnumsFile   = "src/models/enums.py"
	pyTypesFile   = "src/models/types.py"
)

// declIndex maps declared names to their declarations.
type declIndex struct {
	enums   map[string]*ast.Enum
	aliases map[string]*ast.Alias
	types   map[string]*ast.TypeDecl
}

// decls returns the declaration index for g.file, building it once per Files call.
func (g *Generator) decls() *declIndex {
	if g.idx != nil {
		return g.idx
	}
	idx := &declIndex{
		enums:   map[string]*ast.Enum{},
		aliases: map[string]*ast.Alias{},
		types:   map[string]*ast.TypeDecl{},
	}
	if g.file != nil {
		for _, b := range g.file.Blocks {
			switch n := b.(type) {
			case *ast.Enum:
				idx.enums[n.Name] = n
			case *ast.Alias:
				idx.aliases[n.Name] = n
			case *ast.TypeDecl:
				idx.types[n.Name] = n
			}
		}
	}
	g.idx = idx
	return idx
}

// namedKind reports whether name is a declared enum, alias, or type. It returns
// "" for anything else (for example a model, which endpoint inputs cannot use).
func (g *Generator) namedKind(name string) string {
	idx := g.decls()
	switch {
	case idx.enums[name] != nil:
		return "enum"
	case idx.aliases[name] != nil:
		return "alias"
	case idx.types[name] != nil:
		return "type"
	}
	return ""
}

// pythonMemberNameReason rejects names that are valid identifiers but break
// at import or silently lose data: `mro` is reserved by Enum, and a leading
// underscore makes a Pydantic field private, so its value is dropped.
func pythonMemberNameReason(name string) string {
	if strings.HasPrefix(name, "_") {
		return "a leading underscore makes the Python attribute private"
	}
	if name == "mro" {
		return "`mro` is reserved by Python's enum module"
	}
	return ""
}

// pyClassName is the Python class name for a Blueprint declaration.
func pyClassName(name string) string { return common.PascalCase(name) }

// pgEnumTypeName is the Postgres type name Drizzle's pgEnum uses for a declared
// enum on Node (strings.ToLower of the Blueprint name). Python must match it so
// both targets create the same database type.
func pgEnumTypeName(name string) string { return strings.ToLower(name) }

// enumIsStruct reports whether an enum carries per-variant bodies. Struct enums
// (`Plan { free { rate_limit: ... } }`) also expose data through indexing such
// as `Plan[x].field`, which has no Python Enum equivalent. They stay rejected.
func enumIsStruct(e *ast.Enum) bool {
	for _, v := range e.Variants {
		if v.Body != nil {
			return true
		}
	}
	return false
}

// literalAnnotation renders an inline enum as a Literal of its variant strings.
func literalAnnotation(variants []string) string {
	quoted := make([]string, len(variants))
	for i, v := range variants {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "Literal[" + strings.Join(quoted, ", ") + "]"
}

// typeRefs calls visit for every declared-name reference inside t.
func typeRefs(t ast.TypeExpr, visit func(name string)) {
	switch v := t.(type) {
	case *ast.NamedType:
		visit(v.Name)
	case *ast.TypedJSONType:
		typeRefs(v.Inner, visit)
	case *ast.ListType:
		typeRefs(v.Element, visit)
	case *ast.MapType:
		typeRefs(v.Key, visit)
		typeRefs(v.Value, visit)
	}
}

// pyAnnotation returns the Python annotation for a Blueprint type expression,
// without Optional or Annotated wrappers. Unknown shapes fall back to a TODO
// marker; unsupportedFeatures rejects those shapes before any file is emitted.
func (g *Generator) pyAnnotation(t ast.TypeExpr) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "string", "text":
			return "str"
		case "uuid":
			return "UUID"
		case "int", "money":
			return "int"
		case "float":
			return "float"
		case "bool":
			return "bool"
		case "timestamp":
			return "datetime"
		case "json":
			return "dict"
		}
	case *ast.NamedType:
		return pyClassName(v.Name)
	case *ast.EnumInline:
		return literalAnnotation(v.Variants)
	case *ast.TypedJSONType:
		if inner, ok := v.Inner.(*ast.NamedType); ok {
			return pyClassName(inner.Name)
		}
		return "dict"
	case *ast.ListType:
		return "list[" + g.pyAnnotation(v.Element) + "]"
	case *ast.MapType:
		return "dict[" + g.pyAnnotation(v.Key) + ", " + g.pyAnnotation(v.Value) + "]"
	}
	return "str  # TODO(python): unmapped Blueprint type"
}

// enumDefault renders an enum variant used as a default value. A named enum
// uses its Python member (Status.pending); an inline enum uses the string.
func (g *Generator) enumDefault(t ast.TypeExpr, variant string) (string, bool) {
	switch v := t.(type) {
	case *ast.EnumInline:
		for _, candidate := range v.Variants {
			if candidate == variant {
				return fmt.Sprintf("%q", variant), true
			}
		}
	case *ast.NamedType:
		if e := g.decls().enums[v.Name]; e != nil {
			for _, candidate := range e.Variants {
				if candidate.Name == variant {
					return pyClassName(v.Name) + "." + variant, true
				}
			}
		}
	}
	return "", false
}

// resolveAlias follows alias declarations to the type they wrap. A cycle or an
// unknown name returns the input unchanged; cycles are rejected earlier.
func (g *Generator) resolveAlias(t ast.TypeExpr) ast.TypeExpr {
	seen := map[string]bool{}
	for {
		named, ok := t.(*ast.NamedType)
		if !ok {
			return t
		}
		alias := g.decls().aliases[named.Name]
		if alias == nil || seen[named.Name] {
			return t
		}
		seen[named.Name] = true
		t = alias.Type
	}
}

// pyDefaultLiteral renders an input or field default. Enum variant identifiers
// are resolved against the declared type (through aliases); everything else
// uses the general expression renderer.
func (g *Generator) pyDefaultLiteral(t ast.TypeExpr, value ast.Expr) string {
	if ident, ok := value.(*ast.Ident); ok {
		if rendered, ok := g.enumDefault(g.resolveAlias(t), ident.Name); ok {
			return rendered
		}
	}
	return exprToPy(value)
}

// pyDefaultMatchesType reports whether a default expression can be rendered
// for t. Enum defaults must name a declared variant.
func (g *Generator) pyDefaultMatchesType(t ast.TypeExpr, value ast.Expr) bool {
	if _, ok := value.(*ast.NullLit); ok {
		return true
	}
	if ident, ok := value.(*ast.Ident); ok {
		_, matched := g.enumDefault(g.resolveAlias(t), ident.Name)
		return matched
	}
	switch primitive := g.resolveAlias(t).(type) {
	case *ast.PrimitiveType:
		switch primitive.Name {
		case "string", "text":
			_, ok := value.(*ast.StringLit)
			return ok
		case "int", "money":
			_, ok := value.(*ast.IntLit)
			return ok
		case "float":
			switch value.(type) {
			case *ast.IntLit, *ast.FloatLit:
				return true
			}
		case "bool":
			_, ok := value.(*ast.BoolLit)
			return ok
		case "json":
			switch value.(type) {
			case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.ListExpr, *ast.BlockExpr:
				return true
			}
		}
	case *ast.ListType:
		_, ok := value.(*ast.ListExpr)
		return ok
	case *ast.MapType:
		_, ok := value.(*ast.BlockExpr)
		return ok
	}
	return false
}

// typeDeclFeature reports why a type expression used inside a `type` field or
// an `alias` cannot be emitted. Shapes that endpoint inputs support are
// accepted; named references must be declared enums, aliases, or types.
func (g *Generator) typeDeclFeature(t ast.TypeExpr) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "string", "text", "uuid", "int", "float", "bool", "timestamp", "json", "money":
			return ""
		default:
			return fmt.Sprintf("type field of type %q", v.Name)
		}
	case *ast.NamedType:
		if g.namedKind(v.Name) == "" {
			return fmt.Sprintf("type field of undeclared or model type %q", v.Name)
		}
		return ""
	case *ast.EnumInline:
		if len(v.Variants) == 0 {
			return "inline enum with no variants"
		}
		return ""
	case *ast.ListType:
		return g.typeDeclFeature(v.Element)
	case *ast.MapType:
		if reason := g.typeDeclFeature(v.Key); reason != "" {
			return reason
		}
		return g.typeDeclFeature(v.Value)
	case *ast.TypedJSONType:
		return "typed `json<T>` values inside type or alias declarations"
	default:
		return fmt.Sprintf("type field of type %T", t)
	}
}

// declConstraintFeature checks one constraint on a `type` field or `alias`.
// Only required/optional, min/max, and format are emitted by the Python target.
func (g *Generator) declConstraintFeature(owner string, t ast.TypeExpr, c *ast.Constraint_) string {
	switch c.Kind {
	case "required", "optional":
		return ""
	case "min", "max":
		// Bounds apply to the scalar underneath an alias chain.
		if reason := g.pythonInputConstraintFeature(g.resolveAlias(t), c); reason != "" {
			return owner + ": " + reason
		}
		return ""
	case "format":
		if _, aliased := t.(*ast.NamedType); aliased {
			return owner + ": format(...) on an alias of another alias (apply the format on the base alias)"
		}
		if reason := formatFeature(t, c); reason != "" {
			return owner + ": " + reason
		}
		return ""
	default:
		return fmt.Sprintf("%s: constraint %q", owner, c.Kind)
	}
}

// declFeatures validates enum, type, and alias declarations. Each reason names
// the declaration so the user can find it in the source.
func (g *Generator) declFeatures(b ast.TopLevel) []string {
	var out []string
	switch n := b.(type) {
	case *ast.Enum:
		if enumIsStruct(n) {
			out = append(out, fmt.Sprintf("struct `enum %s` with variant bodies", n.Name))
		}
		if len(n.Variants) == 0 {
			out = append(out, fmt.Sprintf("`enum %s` with no variants", n.Name))
		}
		for _, v := range n.Variants {
			if !validPythonIdentifier(v.Name) {
				out = append(out, fmt.Sprintf("enum %s variant %q that is not a valid Python identifier", n.Name, v.Name))
			} else if reason := pythonMemberNameReason(v.Name); reason != "" {
				out = append(out, fmt.Sprintf("enum %s variant %q: %s", n.Name, v.Name, reason))
			}
		}
	case *ast.TypeDecl:
		owner := fmt.Sprintf("type %s", n.Name)
		for _, f := range n.Fields {
			if !validPythonIdentifier(f.Name) {
				out = append(out, fmt.Sprintf("%s field %q that is not a valid Python identifier", owner, f.Name))
			} else if reason := pythonMemberNameReason(f.Name); reason != "" {
				out = append(out, fmt.Sprintf("%s field %q: %s", owner, f.Name, reason))
			}
			if reason := g.typeDeclFeature(f.Type); reason != "" {
				out = append(out, fmt.Sprintf("%s: %s", owner, reason))
			}
			for _, c := range f.Constraints {
				if reason := g.declConstraintFeature(owner+"."+f.Name, f.Type, c); reason != "" {
					out = append(out, reason)
				}
			}
		}
		out = append(out, g.cycleFeatures(owner, n.Name, g.declDeps)...)
	case *ast.Alias:
		owner := fmt.Sprintf("alias %s", n.Name)
		if reason := g.typeDeclFeature(n.Type); reason != "" {
			out = append(out, fmt.Sprintf("%s: %s", owner, reason))
		}
		if named, ok := n.Type.(*ast.NamedType); ok && g.namedKind(named.Name) == "type" {
			out = append(out, fmt.Sprintf("%s: alias of type declaration %q", owner, named.Name))
		}
		for _, c := range n.Constraints {
			if c.Kind == "required" || c.Kind == "optional" {
				out = append(out, fmt.Sprintf("%s: %s constraint (aliases name a type; apply it on the field)", owner, c.Kind))
				continue
			}
			if reason := g.declConstraintFeature(owner, n.Type, c); reason != "" {
				out = append(out, reason)
			}
		}
		out = append(out, g.cycleFeatures(owner, n.Name, g.declDeps)...)
	}
	return out
}

// declDeps returns the type and alias declarations that the named declaration
// refers to directly, whether it is an alias or a `type`.
func (g *Generator) declDeps(name string) []string {
	idx := g.decls()
	var deps []string
	visit := func(t ast.TypeExpr) {
		typeRefs(t, func(ref string) {
			if g.namedKind(ref) == "type" || g.namedKind(ref) == "alias" {
				deps = append(deps, ref)
			}
		})
	}
	if a := idx.aliases[name]; a != nil {
		visit(a.Type)
	}
	if t := idx.types[name]; t != nil {
		for _, f := range t.Fields {
			visit(f.Type)
		}
	}
	return deps
}

// cycleFeatures reports a declaration that reaches itself through other type
// or alias declarations. Python class and alias definitions must be ordered,
// so cycles cannot be emitted. Every declaration in a cycle is checked on its
// own, so a cycle that does not pass through start is reported at its member.
func (g *Generator) cycleFeatures(owner, start string, deps func(string) []string) []string {
	seen := map[string]bool{}
	var reaches func(name string) bool
	reaches = func(name string) bool {
		for _, d := range deps(name) {
			if d == start {
				return true
			}
			if seen[d] {
				continue
			}
			seen[d] = true
			if reaches(d) {
				return true
			}
		}
		return false
	}
	if reaches(start) {
		return []string{owner + ": recursive type declarations"}
	}
	return nil
}

// orderDecls sorts declaration names so every name comes after the names it
// references. Output stays deterministic: ties follow source order.
func orderDecls(names []string, deps func(string) []string) []string {
	sorted := append([]string(nil), names...)
	state := map[string]int{}
	var out []string
	var visit func(name string)
	visit = func(name string) {
		if state[name] != 0 {
			return
		}
		state[name] = 1
		ds := deps(name)
		sort.Strings(ds)
		for _, d := range ds {
			visit(d)
		}
		state[name] = 2
		out = append(out, name)
	}
	for _, n := range sorted {
		visit(n)
	}
	return out
}

// declaredEnums returns enum declarations in source order.
func (g *Generator) declaredEnums() []*ast.Enum {
	var out []*ast.Enum
	for _, b := range g.file.Blocks {
		if e, ok := b.(*ast.Enum); ok {
			out = append(out, e)
		}
	}
	return out
}

// declaredTypes returns `type` declarations in source order.
func (g *Generator) declaredTypes() []*ast.TypeDecl {
	var out []*ast.TypeDecl
	for _, b := range g.file.Blocks {
		if t, ok := b.(*ast.TypeDecl); ok {
			out = append(out, t)
		}
	}
	return out
}

// declaredAliases returns `alias` declarations in source order.
func (g *Generator) declaredAliases() []*ast.Alias {
	var out []*ast.Alias
	for _, b := range g.file.Blocks {
		if a, ok := b.(*ast.Alias); ok {
			out = append(out, a)
		}
	}
	return out
}

// sampleJSONValue is sampleJSONValuePy extended with declared names: an enum
// samples its first variant, an alias samples its wrapped type with its own
// constraints, and a type samples each field. Generated contract tests use
// these values, so they must satisfy the declaration.
func (g *Generator) sampleJSONValue(t ast.TypeExpr, cs []*ast.Constraint_) string {
	switch v := t.(type) {
	case *ast.NamedType:
		switch g.namedKind(v.Name) {
		case "enum":
			if e := g.decls().enums[v.Name]; e != nil && len(e.Variants) > 0 {
				return fmt.Sprintf("%q", e.Variants[0].Name)
			}
		case "alias":
			a := g.decls().aliases[v.Name]
			return g.sampleJSONValue(a.Type, append(append([]*ast.Constraint_{}, a.Constraints...), cs...))
		case "type":
			var parts []string
			for _, f := range g.decls().types[v.Name].Fields {
				parts = append(parts, fmt.Sprintf("%q: %s", f.Name, g.sampleJSONValue(f.Type, f.Constraints)))
			}
			return "{" + strings.Join(parts, ", ") + "}"
		}
	case *ast.TypedJSONType:
		if _, ok := v.Inner.(*ast.NamedType); ok {
			return g.sampleJSONValue(v.Inner, cs)
		}
	case *ast.ListType:
		return "[]"
	}
	return sampleJSONValuePy(t, cs)
}

// sampleQueryValue is sampleQueryValuePy extended with declared names (see
// sampleJSONValue).
func (g *Generator) sampleQueryValue(t ast.TypeExpr, cs []*ast.Constraint_) string {
	switch v := t.(type) {
	case *ast.NamedType:
		switch g.namedKind(v.Name) {
		case "enum":
			if e := g.decls().enums[v.Name]; e != nil && len(e.Variants) > 0 {
				return e.Variants[0].Name
			}
		case "alias":
			a := g.decls().aliases[v.Name]
			return g.sampleQueryValue(a.Type, append(append([]*ast.Constraint_{}, a.Constraints...), cs...))
		}
	}
	return sampleQueryValuePy(t, cs)
}

// inputArgs returns the FastAPI Query/Body/Path keyword arguments for an input:
// its own min/max constraints plus those of any alias it names. FastAPI reads
// the parameter default, not Annotated metadata, when a default is given, so an
// alias's Field(...) alone would not enforce the bound on a query or body value.
func (g *Generator) inputArgs(t ast.TypeExpr, cs []*ast.Constraint_) []string {
	args := inputValidationArgs(t, cs)
	var fromAliases []string
	cur := t
	seen := map[string]bool{}
	for {
		named, ok := cur.(*ast.NamedType)
		if !ok || g.namedKind(named.Name) != "alias" || seen[named.Name] {
			break
		}
		seen[named.Name] = true
		a := g.decls().aliases[named.Name]
		fromAliases = append(fromAliases, inputValidationArgs(g.resolveAlias(a.Type), a.Constraints)...)
		cur = a.Type
	}
	return mergeBoundArgs(append(args, fromAliases...))
}

// mergeBoundArgs keeps one keyword per bound name. Several aliases in a chain
// can each bound the same value; a duplicate keyword is a Python syntax error,
// so the strictest numeric bound wins (largest lower bound, smallest upper).
func mergeBoundArgs(args []string) []string {
	var out []string
	index := map[string]int{}
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			out = append(out, arg)
			continue
		}
		i, seen := index[key]
		if !seen {
			index[key] = len(out)
			out = append(out, arg)
			continue
		}
		prev := strings.SplitN(out[i], "=", 2)[1]
		pn, perr := strconv.Atoi(prev)
		vn, verr := strconv.Atoi(value)
		if perr != nil || verr != nil {
			continue
		}
		lower := key == "ge" || key == "gt" || key == "min_length"
		if (lower && vn > pn) || (!lower && vn < pn) {
			out[i] = key + "=" + value
		}
	}
	return out
}

// modelFieldTypeFeature reports a model field type the SQLAlchemy and Pydantic
// emitters cannot map faithfully. Declared enums are supported directly, and
// aliases over a scalar store as that scalar with the alias on the read model.
// `type` classes are not yet: Node stores them as text and the Python column
// would silently differ. json<T> over a declared type would lose its shape, and
// named types inside list or map columns have no mapping.
func (g *Generator) modelFieldTypeFeature(m *ast.Model, f *ast.Field) string {
	switch v := f.Type.(type) {
	case *ast.EnumInline:
		// The inline column's Postgres type name must not reuse a declared enum's.
		name := inlineEnumTypeName(m, f)
		for _, e := range g.declaredEnums() {
			if pgEnumTypeName(e.Name) == name {
				return fmt.Sprintf("inline enum column type name %q collides with enum %s", name, e.Name)
			}
		}
	case *ast.NamedType:
		switch g.namedKind(v.Name) {
		case "enum":
		case "alias":
			// An alias over a scalar stores as that scalar. A primary-key alias is
			// rejected: the uuid4 default keys on the primitive type.
			if _, scalar := g.resolveAlias(v).(*ast.PrimitiveType); !scalar {
				return fmt.Sprintf("alias %q over a non-scalar type (only scalar aliases can type a model field yet)", v.Name)
			}
			for _, c := range f.Constraints {
				if c.Kind == "primary" {
					return fmt.Sprintf("primary key typed by alias %q", v.Name)
				}
			}
		default:
			return fmt.Sprintf("type-typed model field %q (only declared enums and scalar aliases can type a model field yet)", v.Name)
		}
	case *ast.TypedJSONType:
		if inner, ok := v.Inner.(*ast.NamedType); ok && g.namedKind(inner.Name) != "" {
			return "typed `json<T>` model fields over a declared type"
		}
	case *ast.ListType, *ast.MapType:
		named := false
		typeRefs(f.Type, func(string) { named = true })
		if named || hasInlineEnum(f.Type) {
			return "enum or named types inside list/map model fields"
		}
	}
	for _, c := range f.Constraints {
		if c.Kind != "default" {
			continue
		}
		ident, isIdent := c.Value.(*ast.Ident)
		if !isIdent {
			continue
		}
		if g.namedKind(identTypeName(f.Type)) == "enum" || hasInlineEnum(f.Type) {
			if !g.pyDefaultMatchesType(f.Type, ident) {
				return fmt.Sprintf("default %q is not a variant of the field's enum", ident.Name)
			}
		}
	}
	return ""
}

// identTypeName returns the declared name of a NamedType, or "".
func identTypeName(t ast.TypeExpr) string {
	if named, ok := t.(*ast.NamedType); ok {
		return named.Name
	}
	return ""
}

// genEnumsPy emits src/models/enums.py with one str Enum per declared enum.
// Values are the exact variant strings, so JSON and database values match the
// Blueprint source and the Node target.
func (g *Generator) genEnumsPy(enums []*ast.Enum) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import enum\n\n")
	for _, e := range enums {
		b.WriteString("\n")
		if e.Intent != nil {
			fmt.Fprintf(&b, "# %s\n", e.Intent.Text)
		}
		fmt.Fprintf(&b, "class %s(str, enum.Enum):\n", pyClassName(e.Name))
		if len(e.Variants) == 0 {
			b.WriteString("    pass\n")
		}
		for _, v := range e.Variants {
			fmt.Fprintf(&b, "    %s = %q\n", v.Name, v.Name)
		}
	}
	return codegen.OutputFile{Path: pyEnumsFile, Content: []byte(b.String())}
}

// genTypesPy emits src/models/types.py: aliases first, then `type` classes,
// each in dependency order.
func (g *Generator) genTypesPy() codegen.OutputFile {
	idx := g.decls()
	aliasNames := make([]string, 0, len(idx.aliases))
	for _, a := range g.declaredAliases() {
		aliasNames = append(aliasNames, a.Name)
	}
	typeNames := make([]string, 0, len(idx.types))
	for _, t := range g.declaredTypes() {
		typeNames = append(typeNames, t.Name)
	}
	aliasOrder := orderDecls(aliasNames, func(name string) []string {
		var deps []string
		typeRefs(idx.aliases[name].Type, func(ref string) {
			if idx.aliases[ref] != nil {
				deps = append(deps, ref)
			}
		})
		return deps
	})
	// Aliases are emitted before any type, so only type-to-type edges order
	// the class definitions.
	typeOrder := orderDecls(typeNames, func(name string) []string {
		var deps []string
		for _, ref := range g.declDeps(name) {
			if idx.types[ref] != nil {
				deps = append(deps, ref)
			}
		}
		return deps
	})

	imports := newImportSet()
	needsField := false
	needsOptional := false
	needsAnnotated := false
	needsDatetime := false
	needsUUID := false
	needsFormats := false
	needsLiteral := false
	scan := func(t ast.TypeExpr, cs []*ast.Constraint_, extra []string) {
		typeRefs(t, func(ref string) {
			if g.namedKind(ref) == "enum" {
				imports.add(pyEnumsModule, pyClassName(ref))
			}
		})
		walkTypePrimitives(t, func(p *ast.PrimitiveType) {
			switch p.Name {
			case "uuid":
				needsUUID = true
			case "timestamp":
				needsDatetime = true
			}
		})
		if hasInlineEnum(t) {
			needsLiteral = true
		}
		if formatTypeFor(t, cs) != "" {
			needsFormats = true
		}
		if len(extra) > 0 {
			needsAnnotated = true
			needsField = true
		}
	}
	var body strings.Builder
	for _, name := range aliasOrder {
		a := idx.aliases[name]
		if body.Len() > 0 {
			body.WriteString("\n")
		}
		if a.Intent != nil {
			fmt.Fprintf(&body, "# %s\n", a.Intent.Text)
		}
		extra := g.fieldMetadata(a.Type, a.Constraints)
		scan(a.Type, a.Constraints, extra)
		base := annotatedType(g.pyAnnotation(a.Type), a.Type, a.Constraints)
		if len(extra) > 0 {
			base = fmt.Sprintf("Annotated[%s, %s]", base, strings.Join(extra, ", "))
		}
		fmt.Fprintf(&body, "%s = %s\n\n", pyClassName(name), base)
	}
	for _, name := range typeOrder {
		t := idx.types[name]
		if body.Len() > 0 {
			body.WriteString("\n")
		}
		if t.Intent != nil {
			fmt.Fprintf(&body, "# %s\n", t.Intent.Text)
		}
		fmt.Fprintf(&body, "class %s(BaseModel):\n", pyClassName(name))
		if len(t.Fields) == 0 {
			body.WriteString("    pass\n")
		}
		for _, f := range t.Fields {
			extra := g.fieldMetadata(f.Type, f.Constraints)
			scan(f.Type, f.Constraints, extra)
			base := g.pyAnnotation(f.Type)
			annot := annotatedType(base, f.Type, f.Constraints, extra...)
			optional := false
			for _, c := range f.Constraints {
				if c.Kind == "optional" {
					optional = true
				}
			}
			if optional {
				needsOptional = true
				annot = "Optional[" + annot + "]"
				fmt.Fprintf(&body, "    %s: %s = None\n", f.Name, annot)
				continue
			}
			fmt.Fprintf(&body, "    %s: %s\n", f.Name, annot)
		}
		body.WriteString("\n\n")
	}

	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	var stdlib []string
	if needsAnnotated {
		stdlib = append(stdlib, "Annotated")
	}
	if needsLiteral {
		stdlib = append(stdlib, "Literal")
	}
	if needsOptional {
		stdlib = append(stdlib, "Optional")
	}
	if len(stdlib) > 0 {
		fmt.Fprintf(&b, "from typing import %s\n", strings.Join(stdlib, ", "))
	}
	if needsDatetime {
		b.WriteString("from datetime import datetime\n")
	}
	if needsUUID {
		b.WriteString("from uuid import UUID\n")
	}
	pyd := []string{"BaseModel"}
	if needsField {
		pyd = append(pyd, "Field")
	}
	fmt.Fprintf(&b, "from pydantic import %s\n", strings.Join(pyd, ", "))
	if needsFormats {
		b.WriteString("from src.lib import formats\n")
	}
	for _, line := range imports.lines() {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(body.String())
	return codegen.OutputFile{Path: pyTypesFile, Content: []byte(strings.TrimRight(b.String(), "\n") + "\n")}
}

// fieldMetadata returns the typing.Annotated metadata (pydantic Field arguments)
// for min/max constraints on a type field or alias. Format is handled by the
// formats.py type instead, so it is not included here.
func (g *Generator) fieldMetadata(t ast.TypeExpr, cs []*ast.Constraint_) []string {
	args := inputValidationArgs(g.resolveAlias(t), cs)
	if len(args) == 0 {
		return nil
	}
	return []string{"Field(" + strings.Join(args, ", ") + ")"}
}

// walkTypePrimitives visits primitive types nested in t.
func walkTypePrimitives(t ast.TypeExpr, visit func(*ast.PrimitiveType)) {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		visit(v)
	case *ast.TypedJSONType:
		walkTypePrimitives(v.Inner, visit)
	case *ast.ListType:
		walkTypePrimitives(v.Element, visit)
	case *ast.MapType:
		walkTypePrimitives(v.Key, visit)
		walkTypePrimitives(v.Value, visit)
	}
}

// hasInlineEnum reports whether t contains an inline enum anywhere.
func hasInlineEnum(t ast.TypeExpr) bool {
	switch v := t.(type) {
	case *ast.EnumInline:
		return true
	case *ast.TypedJSONType:
		return hasInlineEnum(v.Inner)
	case *ast.ListType:
		return hasInlineEnum(v.Element)
	case *ast.MapType:
		return hasInlineEnum(v.Key) || hasInlineEnum(v.Value)
	}
	return false
}

// importSet collects `from <module> import <name>` lines, grouped per module.
type importSet struct {
	names map[string]map[string]bool
}

func newImportSet() *importSet { return &importSet{names: map[string]map[string]bool{}} }

func (s *importSet) add(module, name string) {
	if s.names[module] == nil {
		s.names[module] = map[string]bool{}
	}
	s.names[module][name] = true
}

func (s *importSet) lines() []string {
	mods := make([]string, 0, len(s.names))
	for m := range s.names {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	var out []string
	for _, m := range mods {
		list := make([]string, 0, len(s.names[m]))
		for n := range s.names[m] {
			list = append(list, n)
		}
		sort.Strings(list)
		out = append(out, fmt.Sprintf("from %s import %s", m, strings.Join(list, ", ")))
	}
	return out
}
