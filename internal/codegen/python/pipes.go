package python

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// `pipe` declarations have inline bodies only: the grammar gives them no
// `impl` block, so unlike `fn` there is no user-owned scaffold. Each pipe is
// generated as src/pipes/<name>.py, mirroring the Node target's
// src/pipes/<name>.ts. The body translator accepts a deliberately small
// subset (guards, plain `->` outputs, block literals, and calls to declared
// fn/pipe names) and pipeFeatures rejects everything else, so a successful
// build never contains a silently dropped or placeholder step.

// genPipes emits the package marker and one module per declared pipe.
func (g *Generator) genPipes(pipes []*ast.Pipe) []codegen.OutputFile {
	if len(pipes) == 0 {
		return nil
	}
	out := []codegen.OutputFile{emptyInit("src/pipes/__init__.py", g.sourceFile)}
	for _, p := range pipes {
		out = append(out, g.genPipe(p))
	}
	return out
}

// genPipe emits src/pipes/<name>.py. Callers must have passed pipeFeatures
// first; the emitter assumes every statement is in the supported subset.
//
// The Python function is synchronous, matching the fn wrappers. Node pipes
// are async; for the translated subset (pure guards, outputs, and calls) the
// observable result is the same.
func (g *Generator) genPipe(p *ast.Pipe) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	imports := pipeImports(p, g.pipeNames())
	if imports.guards {
		b.WriteString("from fastapi import HTTPException\n")
	}
	for _, name := range imports.fns {
		fmt.Fprintf(&b, "from src.functions.%s import %s\n", name, name)
	}
	for _, name := range imports.pipes {
		fmt.Fprintf(&b, "from src.pipes.%s import %s\n", name, name)
	}
	if imports.guards || len(imports.fns) > 0 || len(imports.pipes) > 0 {
		b.WriteString("\n")
	}

	param := pipeParamName(p)
	fmt.Fprintf(&b, "def %s(%s):\n", p.Name, param)

	ctx := &bodyCtx{
		path:          p.Name,
		inputs:        map[string]bool{param: true},
		varModel:      map[string]string{},
		fkAliases:     map[string]string{},
		bindingModels: map[string]string{},
		userFns:       g.userFnNames(),
	}
	const indent = "    "
	returned := false
	for _, s := range p.Stmts {
		switch v := s.(type) {
		case *ast.InputStmt:
			continue
		case *ast.IntentStep:
			fmt.Fprintf(&b, "%s# %s\n", indent, strings.ReplaceAll(v.Text, "\n", " "))
		case *ast.GuardStmt:
			emitGuard(&b, v, ctx, indent)
		case *ast.OutputStmt:
			value := "None"
			if v.Value != nil {
				value = exprToPyWithCtx(v.Value, ctx)
			}
			fmt.Fprintf(&b, "%sreturn %s\n", indent, value)
			returned = true
		case *ast.StepStmt:
			emitPipeStep(&b, v, ctx, indent)
			returned = false
		}
	}
	if !returned {
		fmt.Fprintf(&b, "%sreturn None\n", indent)
	}

	return codegen.OutputFile{
		Path:    "src/pipes/" + p.Name + ".py",
		Content: []byte(b.String()),
	}
}

// emitPipeStep translates a step that pipeFeatures already accepted: a
// block literal binding or a call to a declared fn/pipe.
func emitPipeStep(b *strings.Builder, s *ast.StepStmt, ctx *bodyCtx, indent string) {
	if blk, ok := s.Expr.(*ast.BlockExpr); ok && s.Binding != "" {
		fmt.Fprintf(b, "%s%s = %s\n", indent, s.Binding, exprToPyWithCtx(blk, ctx))
		return
	}
	if fn, ok := s.Expr.(*ast.FnCall); ok && ctx.userFns[fn.Name] {
		emitUserFnCall(b, s.Binding, fn, ctx, indent)
	}
}

// pipeParamName returns the single parameter name: the declared `<-` input,
// or the implicit `input` the checker assigns to a pipe without one
// (see checker.pipeCallInputs).
func pipeParamName(p *ast.Pipe) string {
	for _, s := range p.Stmts {
		if in, ok := s.(*ast.InputStmt); ok {
			return in.Name
		}
	}
	return "input"
}

// pipeImportSet lists the generated modules a pipe body references.
type pipeImportSet struct {
	guards bool     // needs fastapi.HTTPException
	fns    []string // src.functions.<name> callees, sorted
	pipes  []string // src.pipes.<name> callees, sorted
}

func pipeImports(p *ast.Pipe, pipes map[string]bool) pipeImportSet {
	var out pipeImportSet
	fns := map[string]bool{}
	pipeCalls := map[string]bool{}
	for _, s := range p.Stmts {
		switch v := s.(type) {
		case *ast.GuardStmt:
			out.guards = true
		case *ast.StepStmt:
			if fn, ok := v.Expr.(*ast.FnCall); ok {
				if pipes[fn.Name] {
					pipeCalls[fn.Name] = true
				} else {
					fns[fn.Name] = true
				}
			}
		}
	}
	for name := range fns {
		out.fns = append(out.fns, name)
	}
	for name := range pipeCalls {
		out.pipes = append(out.pipes, name)
	}
	sort.Strings(out.fns)
	sort.Strings(out.pipes)
	return out
}

// pipeNames returns the set of declared pipe names.
func (g *Generator) pipeNames() map[string]bool {
	out := map[string]bool{}
	if g.file == nil {
		return out
	}
	for _, b := range g.file.Blocks {
		if p, ok := b.(*ast.Pipe); ok {
			out[p.Name] = true
		}
	}
	return out
}

// userCallModule returns the Python module a step call to a declared fn or
// pipe must import from. Pipes live under src.pipes, fns under src.functions.
func userCallModule(name string, pipes map[string]bool) string {
	if pipes[name] {
		return "src.pipes." + name
	}
	return "src.functions." + name
}

// pipeFeatures reports every construct in a pipe that the Python target does
// not translate. An empty result means the pipe body is in the supported
// subset.
func (g *Generator) pipeFeatures(p *ast.Pipe) []string {
	var out []string
	pipes := g.pipeNames()
	userFns := g.userFnNames()
	for _, s := range p.Stmts {
		switch v := s.(type) {
		case *ast.InputStmt:
			if len(v.Constraints) > 0 {
				out = append(out, fmt.Sprintf("constraints on the input of pipe %q", p.Name))
			}
		case *ast.IntentStep:
		case *ast.GuardStmt:
			out = append(out, pipeExprFeatures(p.Name, v.Condition)...)
		case *ast.OutputStmt:
			if v.Status != "" {
				out = append(out, fmt.Sprintf("status code on the output of pipe %q", p.Name))
			}
			if v.Value != nil {
				out = append(out, pipeExprFeatures(p.Name, v.Value)...)
			}
		case *ast.StepStmt:
			out = append(out, pipeStepFeatures(p.Name, v, userFns, pipes)...)
		case *ast.WhenStmt:
			out = append(out, fmt.Sprintf("`when` blocks in pipe %q", p.Name))
		case *ast.TryRecover:
			out = append(out, fmt.Sprintf("`try`/`recover` in pipe %q", p.Name))
		default:
			out = append(out, fmt.Sprintf("%T statements in pipe %q", s, p.Name))
		}
	}
	if pipeCallCycle(p.Name, pipes, g.pipeCallGraph()) {
		out = append(out, fmt.Sprintf("recursive pipe call graph through %q (Python module imports cannot be cyclic)", p.Name))
	}
	return out
}

func pipeStepFeatures(pipeName string, s *ast.StepStmt, userFns, pipes map[string]bool) []string {
	if blk, ok := s.Expr.(*ast.BlockExpr); ok && s.Binding != "" {
		var out []string
		for _, kv := range blk.Entries {
			out = append(out, pipeExprFeatures(pipeName, kv.Value)...)
		}
		return out
	}
	fn, ok := s.Expr.(*ast.FnCall)
	if !ok {
		return []string{fmt.Sprintf("step expressions other than block literals or fn/pipe calls in pipe %q", pipeName)}
	}
	if !userFns[fn.Name] {
		return []string{fmt.Sprintf("step %q in pipe %q (pipes can call only declared fn/pipe names; data operations need an endpoint database session)", fn.Name, pipeName)}
	}
	var out []string
	for _, arg := range fn.Args {
		out = append(out, pipeExprFeatures(pipeName, arg)...)
	}
	return out
}

// pipeExprFeatures walks an expression that a pipe body emits. Only
// constructs whose Python translation needs no endpoint-scoped names
// (env, header, db session, datetime/timedelta imports) are accepted.
func pipeExprFeatures(pipeName string, e ast.Expr) []string {
	var out []string
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch v := e.(type) {
		case nil:
		case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.NullLit,
			*ast.SizeLit, *ast.RateLit, *ast.PathExpr, *ast.Ident:
		case *ast.NowLit, *ast.DurationLit:
			out = append(out, fmt.Sprintf("time and duration literals in pipe %q (need datetime imports)", pipeName))
		case *ast.FieldAccess:
			if id, ok := v.Base.(*ast.Ident); ok && (id.Name == "env" || id.Name == "header") {
				out = append(out, fmt.Sprintf("`%s.%s` in pipe %q (only endpoint and middleware bodies can read it)", id.Name, v.Field, pipeName))
				return
			}
			if _, nested := v.Base.(*ast.FieldAccess); nested || v.Field == "ago" || pyDurationField(v.Field) != "" {
				out = append(out, fmt.Sprintf("nested or duration field access `.%s` in pipe %q", v.Field, pipeName))
				return
			}
			if v.Field == "count" {
				out = append(out, fmt.Sprintf("`.count` in pipe %q (collection length needs endpoint cardinality)", pipeName))
				return
			}
			walk(v.Base)
		case *ast.IndexAccess:
			walk(v.Base)
			walk(v.Index)
		case *ast.BinaryExpr:
			walk(v.Left)
			walk(v.Right)
		case *ast.UnaryExpr:
			walk(v.Operand)
		case *ast.ParenExpr:
			walk(v.Expr)
		case *ast.ListExpr:
			for _, el := range v.Elements {
				walk(el)
			}
		case *ast.BlockExpr:
			for _, kv := range v.Entries {
				walk(kv.Value)
			}
		case *ast.FnCall:
			out = append(out, fmt.Sprintf("call to %q inside an expression in pipe %q (bind it with a step instead)", v.Name, pipeName))
		default:
			out = append(out, fmt.Sprintf("%T expressions in pipe %q", e, pipeName))
		}
	}
	walk(e)
	return out
}

// pipeCallGraph maps each pipe to the pipe names its steps call directly.
func (g *Generator) pipeCallGraph() map[string][]string {
	pipes := g.pipeNames()
	out := map[string][]string{}
	if g.file == nil {
		return out
	}
	for _, b := range g.file.Blocks {
		p, ok := b.(*ast.Pipe)
		if !ok {
			continue
		}
		for _, s := range p.Stmts {
			if st, ok := s.(*ast.StepStmt); ok {
				if fn, ok := st.Expr.(*ast.FnCall); ok && pipes[fn.Name] {
					out[p.Name] = append(out[p.Name], fn.Name)
				}
			}
		}
	}
	return out
}

// pipeCallCycle reports whether name can reach itself through pipe calls.
func pipeCallCycle(name string, pipes map[string]bool, graph map[string][]string) bool {
	seen := map[string]bool{}
	var reach func(cur string) bool
	reach = func(cur string) bool {
		for _, next := range graph[cur] {
			if next == name {
				return true
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			if reach(next) {
				return true
			}
		}
		return false
	}
	return pipes[name] && reach(name)
}
