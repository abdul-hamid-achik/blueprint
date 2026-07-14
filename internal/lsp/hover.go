package lsp

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// computeHover produces hover markdown for (line, char). Returns the empty
// string when nothing useful is found — callers should treat empty as "no
// hover" and respond with a null result per LSP.
func computeHover(idx *docIndex, line, char int) string {
	if idx == nil {
		return ""
	}
	sym := findSymbolAt(idx, line, char)

	switch sym.Kind {
	case SymbolIntent:
		if it := findIntentAt(idx.file, idx.text, line, char); it != nil {
			return fmt.Sprintf("**Intent**\n\n%s", it.Text)
		}
		return "**@intent** — natural-language description attached to the next block. The compiler treats this as documentation; codegen may use it for OpenAPI summaries."

	case SymbolModel:
		if m, ok := findModel(idx.file, sym.Name); ok {
			return modelHover(m)
		}

	case SymbolFn:
		if fn := findFn(idx.file, sym.Name); fn != nil {
			return fnHover(fn)
		}

	case SymbolPipe:
		if p := findPipe(idx.file, sym.Name); p != nil {
			return pipeHover(p)
		}

	case SymbolMiddleware:
		if m := findMiddleware(idx.file, sym.Name); m != nil {
			return middlewareHover(m)
		}

	case SymbolField:
		if m, ok := findModel(idx.file, sym.Parent); ok {
			if f, ok := findField(m, sym.Name); ok {
				return fieldHover(sym.Parent, f)
			}
			if f, ok := findComputedField(m, sym.Name); ok {
				return computedFieldHover(sym.Parent, f)
			}
		}

	case SymbolDataOp:
		return builtinDataOps[sym.Name]

	case SymbolKeyword:
		return builtinKeywordDocs[sym.Name]
	}

	// Last-resort keyword fallback.
	if sym.Name != "" {
		if doc, ok := builtinKeywordDocs[sym.Name]; ok {
			return doc
		}
	}
	return ""
}

func modelHover(m *ast.Model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Model `%s`**", m.Name)
	if m.Intent != nil && m.Intent.Text != "" {
		fmt.Fprintf(&b, "\n\n%s", m.Intent.Text)
	}
	if len(m.Fields) > 0 {
		b.WriteString("\n\nFields:\n")
		for _, f := range m.Fields {
			if f == nil {
				continue
			}
			fmt.Fprintf(&b, "- `%s` %s%s\n", f.Name, typeString(f.Type), constraintsString(f.Constraints))
		}
	}
	if len(m.ComputedFields) > 0 {
		b.WriteString("\nComputed fields:\n")
		for _, f := range m.ComputedFields {
			if f == nil {
				continue
			}
			fmt.Fprintf(&b, "- `%s` %s *(computed)*\n", f.Name, typeString(f.Type))
		}
	}
	return b.String()
}

func fnHover(fn *ast.Fn) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**fn `%s`**", fn.Name)
	sig := signatureFromInputs(inputsFromFn(fn))
	if sig != "" {
		fmt.Fprintf(&b, " `(%s)`", sig)
	}
	if fn.Intent != nil && fn.Intent.Text != "" {
		fmt.Fprintf(&b, "\n\n%s", fn.Intent.Text)
	}
	return b.String()
}

func pipeHover(p *ast.Pipe) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**pipe `%s`**", p.Name)
	sig := signatureFromInputs(inputsFromPipe(p))
	if sig != "" {
		fmt.Fprintf(&b, " `(%s)`", sig)
	}
	if p.Intent != nil && p.Intent.Text != "" {
		fmt.Fprintf(&b, "\n\n%s", p.Intent.Text)
	}
	return b.String()
}

func middlewareHover(m *ast.Middleware) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**middleware `%s`**", m.Name)
	if m.Intent != nil && m.Intent.Text != "" {
		fmt.Fprintf(&b, "\n\n%s", m.Intent.Text)
	}
	return b.String()
}

func fieldHover(modelName string, f *ast.Field) string {
	return fmt.Sprintf("**`%s.%s`** %s%s", modelName, f.Name, typeString(f.Type), constraintsString(f.Constraints))
}

func computedFieldHover(modelName string, f *ast.ComputedField) string {
	return fmt.Sprintf("**`%s.%s`** %s *(computed, read-only)*", modelName, f.Name, typeString(f.Type))
}

// inputsFromFn / inputsFromPipe collect input statements from a block. Pipes
// don't have a typed Inputs field; we scan the leading `<-` statements.
func inputsFromFn(fn *ast.Fn) []*ast.InputStmt {
	return fn.Inputs
}

func inputsFromPipe(p *ast.Pipe) []*ast.InputStmt {
	var ins []*ast.InputStmt
	for _, s := range p.Stmts {
		if in, ok := s.(*ast.InputStmt); ok {
			ins = append(ins, in)
		}
	}
	return ins
}

func signatureFromInputs(inputs []*ast.InputStmt) string {
	if len(inputs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(inputs))
	for _, in := range inputs {
		parts = append(parts, fmt.Sprintf("%s: %s", in.Name, typeString(in.Type)))
	}
	return strings.Join(parts, ", ")
}

func typeString(t ast.TypeExpr) string {
	if t == nil {
		return "any"
	}
	switch v := t.(type) {
	case *ast.PrimitiveType:
		return v.Name
	case *ast.NamedType:
		return v.Name
	case *ast.ListType:
		return "[" + typeString(v.Element) + "]"
	case *ast.MapType:
		return "map<" + typeString(v.Key) + ", " + typeString(v.Value) + ">"
	case *ast.TypedJSONType:
		return "json<" + typeString(v.Inner) + ">"
	case *ast.EnumInline:
		return "enum(" + strings.Join(v.Variants, " | ") + ")"
	case *ast.MimeTypeExpr:
		return v.Type + "/" + v.Subtype
	}
	return ""
}

func constraintsString(cs []*ast.Constraint_) string {
	if len(cs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		if c == nil {
			continue
		}
		parts = append(parts, c.Kind)
	}
	return " *(" + strings.Join(parts, ", ") + ")*"
}

func findFn(file *ast.File, name string) *ast.Fn {
	if file == nil {
		return nil
	}
	for _, b := range file.Blocks {
		if fn, ok := b.(*ast.Fn); ok && fn.Name == name {
			return fn
		}
	}
	return nil
}

func findPipe(file *ast.File, name string) *ast.Pipe {
	if file == nil {
		return nil
	}
	for _, b := range file.Blocks {
		if p, ok := b.(*ast.Pipe); ok && p.Name == name {
			return p
		}
	}
	return nil
}

func findMiddleware(file *ast.File, name string) *ast.Middleware {
	if file == nil {
		return nil
	}
	for _, b := range file.Blocks {
		if m, ok := b.(*ast.Middleware); ok && m.Name == name {
			return m
		}
	}
	return nil
}
