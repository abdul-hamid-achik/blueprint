package js

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// fnSignatureTS returns the typed TypeScript parameter list and return type
// for a Blueprint `fn` declaration. Mirrors the per-fn emit logic in
// genFunction (model-typed inputs collapse to `any` to dodge Drizzle nullable
// field issues; outputs stay `any` for now since OutputStmt is value-shaped,
// not type-shaped). Shared by the merged-stub scaffold path in generateAll so
// user-owned scaffolds expose the real signatures rather than `...args: any[]`.
func (g *Generator) fnSignatureTS(fn *ast.Fn) (paramsStr, retType string) {
	params := make([]string, len(fn.Inputs))
	for i, inp := range fn.Inputs {
		paramType := typeToTS(inp.Type)
		if nt, ok := inp.Type.(*ast.NamedType); ok && g.declaredModels[nt.Name] {
			paramType = "any"
		}
		params[i] = fmt.Sprintf("%s: %s", toCamelCase(inp.Name), paramType)
	}
	retType = "void"
	if len(fn.Outputs) > 0 && fn.Outputs[0].Value != nil {
		retType = "any"
	}
	return strings.Join(params, ", "), retType
}

// --- Functions ---

func (g *Generator) genFunction(fn *ast.Fn) []codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Collect imports from function body
	var ic *importCollector
	if fn.Logic != nil {
		ic = g.collectImports(fn.Logic.Stmts)
	} else {
		ic = newImportCollector()
	}

	// Check param types for model/enum type references.
	// Model-typed params are emitted as `any` to avoid Drizzle nullable field issues,
	// so we don't add them to modelTypes imports.
	for _, inp := range fn.Inputs {
		if nt, ok := inp.Type.(*ast.NamedType); ok {
			if !g.declaredModels[nt.Name] {
				if g.declaredEnums[nt.Name] {
					ic.enumTypes[nt.Name] = true
				}
			}
		}
	}

	// Add data imports if function logic uses data operations
	if fn.Logic != nil && stmtsHaveDataOps(fn.Logic.Stmts) {
		writeDataImports(&b)
	}

	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(fn.Name)

	// Input params — model-typed params use `any` to avoid Drizzle nullable field issues
	params := make([]string, len(fn.Inputs))
	for i, inp := range fn.Inputs {
		paramType := typeToTS(inp.Type)
		if nt, ok := inp.Type.(*ast.NamedType); ok && g.declaredModels[nt.Name] {
			paramType = "any"
		}
		params[i] = fmt.Sprintf("%s: %s", toCamelCase(inp.Name), paramType)
	}

	// Return type
	retType := "void"
	if len(fn.Outputs) > 0 && fn.Outputs[0].Value != nil {
		// Try to infer from output type if possible
		retType = "any"
	}

	var extraFiles []codegen.OutputFile
	if fn.Impl != nil {
		switch fn.Impl.Strategy {
		case "exec":
			// impl exec { cmd: "script.sh" [, args: [...]] }
			// Generates a function that runs a child process via execFile.
			b.WriteString("import { execFile } from 'child_process';\n")
			b.WriteString("import { promisify } from 'util';\n\n")
			b.WriteString("const execFileAsync = promisify(execFile);\n\n")
			cmd := ""
			var extraArgs []string
			for _, kv := range fn.Impl.Entries {
				switch kv.Key {
				case "cmd":
					cmd = exprToString(kv.Value)
				case "args":
					if list, ok := kv.Value.(*ast.ListExpr); ok {
						for _, el := range list.Elements {
							extraArgs = append(extraArgs, exprToJS(el))
						}
					}
				}
			}
			inputArgs := make([]string, len(fn.Inputs))
			for i, inp := range fn.Inputs {
				inputArgs[i] = toCamelCase(inp.Name)
			}
			allArgs := append(extraArgs, inputArgs...) //nolint:gocritic
			argsJS := ""
			if len(allArgs) > 0 {
				argsJS = strings.Join(allArgs, ", ")
			}
			fmt.Fprintf(&b, "export async function %s(%s): Promise<%s> {\n",
				name, strings.Join(params, ", "), retType)
			fmt.Fprintf(&b, "  const { stdout } = await execFileAsync(%q, [%s]);\n", cmd, argsJS)
			b.WriteString("  return stdout;\n")
			b.WriteString("}\n")

		case "http":
			// impl http { method: POST, url: "https://...", body: {...} }
			// Generates a function that performs a fetch call.
			method := "GET"
			url := ""
			bodyExpr := ""
			for _, kv := range fn.Impl.Entries {
				switch kv.Key {
				case "method":
					method = strings.ToUpper(exprToString(kv.Value))
				case "url":
					url = exprToString(kv.Value)
				case "body":
					bodyExpr = exprToJS(kv.Value)
				}
			}
			fmt.Fprintf(&b, "export async function %s(%s): Promise<%s> {\n",
				name, strings.Join(params, ", "), retType)
			if bodyExpr != "" {
				fmt.Fprintf(&b, "  const res = await fetch(%q, {\n", url)
				fmt.Fprintf(&b, "    method: %q,\n", method)
				fmt.Fprintf(&b, "    body: JSON.stringify(%s),\n", bodyExpr)
				b.WriteString("    headers: { 'Content-Type': 'application/json' },\n")
				b.WriteString("  });\n")
			} else {
				fmt.Fprintf(&b, "  const res = await fetch(%q, { method: %q });\n", url, method)
			}
			b.WriteString("  return res.json();\n")
			b.WriteString("}\n")

		default:
			// "node" strategy or unrecognized — native module implementation
			for _, kv := range fn.Impl.Entries {
				if kv.Key == "module" {
					mod := exprToString(kv.Value)
					funcName := name
					for _, kv2 := range fn.Impl.Entries {
						if kv2.Key == "func" {
							funcName = exprToString(kv2.Value)
						}
					}
					importPath, stubPath, localModule := nativeImplModulePaths(mod)
					fmt.Fprintf(&b, "import { %s as %sImpl } from '%s';\n\n", funcName, name, importPath)
					fmt.Fprintf(&b, "export async function %s(%s): Promise<%s> {\n",
						name, strings.Join(params, ", "), retType)
					args := make([]string, len(fn.Inputs))
					for i, inp := range fn.Inputs {
						args[i] = toCamelCase(inp.Name)
					}
					fmt.Fprintf(&b, "  return %sImpl(%s);\n", name, strings.Join(args, ", "))
					b.WriteString("}\n")
					if localModule {
						var sb strings.Builder
						sb.WriteString("// Blueprint implementation scaffold. This file is user-owned; bp build will not overwrite it.\n")
						fmt.Fprintf(&sb, "export async function %s(%s): Promise<%s> {\n", funcName, strings.Join(params, ", "), retType)
						fmt.Fprintf(&sb, "  throw new Error('Not implemented: %s');\n", funcName)
						sb.WriteString("}\n")
						extraFiles = append(extraFiles, codegen.OutputFile{
							Path:      stubPath,
							Content:   []byte(sb.String()),
							UserOwned: true,
						})
					}
					break
				}
			}
		}
	} else if fn.Logic != nil {
		// Inline logic — populate boundVars/varModels from function inputs
		fnCtx := emitCtx{
			kind:        "function",
			boundVars:   make(map[string]string),
			varModels:   make(map[string]string),
			singleVars:  make(map[string]bool),
			asyncFns:    g.buildAsyncFns(),
			structEnums: g.structEnums,
		}
		for _, inp := range fn.Inputs {
			if nt, ok := inp.Type.(*ast.NamedType); ok && g.declaredModels[nt.Name] {
				fnCtx.boundVars[nt.Name] = toCamelCase(inp.Name)
				fnCtx.varModels[toCamelCase(inp.Name)] = nt.Name
			}
		}
		fmt.Fprintf(&b, "export async function %s(%s): Promise<%s> {\n",
			name, strings.Join(params, ", "), retType)
		g.emitArrowStmts(&b, fn.Logic.Stmts, "  ", fnCtx)
		b.WriteString("}\n")
	}

	mainFile := codegen.OutputFile{
		Path:    fmt.Sprintf("src/functions/%s.ts", toKebabCase(fn.Name)),
		Content: []byte(b.String()),
	}
	return append([]codegen.OutputFile{mainFile}, extraFiles...)
}

func nativeImplModulePaths(mod string) (importPath, stubPath string, localModule bool) {
	rawMod := strings.TrimSuffix(mod, ".js")
	if strings.HasPrefix(rawMod, "./internal/") {
		rel := strings.TrimPrefix(rawMod, "./")
		return "../impl/functions/" + rel + ".js", "src/impl/functions/" + rel + ".ts", true
	}
	if !strings.HasSuffix(mod, ".js") {
		mod += ".js"
	}
	return mod, "", false
}

// --- Pipes ---

func (g *Generator) genPipe(p *ast.Pipe) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { BpError } from '../lib/errors.js';\n")

	// Add data imports if pipe logic uses data operations
	if stmtsHaveDataOps(p.Stmts) {
		writeDataImports(&b)
	}

	// Add imports for env, functions, enums, etc.
	ic := g.collectImports(p.Stmts)
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(p.Name)

	// Extract input from first arrow stmt
	params := "input: any"
	for _, s := range p.Stmts {
		if inp, ok := s.(*ast.InputStmt); ok {
			params = fmt.Sprintf("%s: %s", toCamelCase(inp.Name), typeToTS(inp.Type))
			break
		}
	}

	fmt.Fprintf(&b, "export async function %s(%s): Promise<any> {\n", name, params)
	ctx := emitCtx{kind: "function"}
	for _, s := range p.Stmts {
		if _, ok := s.(*ast.InputStmt); ok {
			continue // skip inputs
		}
		g.emitArrowStmts(&b, []ast.ArrowStmt{s}, "  ", ctx)
	}
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/pipes/%s.ts", toKebabCase(p.Name)),
		Content: []byte(b.String()),
	}
}

// --- Middleware ---

func (g *Generator) genMiddleware(mw *ast.Middleware) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { createMiddleware } from 'hono/factory';\n")
	b.WriteString("import { BpError } from '../lib/errors.js';\n")

	// Add data imports if middleware logic uses data operations
	if stmtsHaveDataOps(mw.Before) || stmtsHaveDataOps(mw.After) {
		writeDataImports(&b)
	}

	// Add imports for functions, env, enums, etc.
	ic := g.collectImports(mw.Before)
	ic.merge(g.collectImports(mw.After))
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(mw.Name)

	fmt.Fprintf(&b, "export const %s = createMiddleware(async (c, next) => {\n", name)

	ctx := emitCtx{kind: "middleware", asyncFns: g.buildAsyncFns()}

	if len(mw.Before) > 0 {
		b.WriteString("  // before\n")
		g.emitArrowStmts(&b, mw.Before, "  ", ctx)
	}

	b.WriteString("  await next();\n")

	if len(mw.After) > 0 {
		b.WriteString("  // after\n")
		g.emitArrowStmts(&b, mw.After, "  ", ctx)
	}

	b.WriteString("});\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/middleware/%s.ts", toKebabCase(mw.Name)),
		Content: []byte(b.String()),
	}
}

// --- Workers ---

func (g *Generator) genWorker(w *ast.Worker) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Add data imports if worker logic uses data operations
	if stmtsHaveDataOps(w.Stmts) || stmtsHaveDataOps(w.OnFail) {
		writeDataImports(&b)
	}

	// Add imports for functions, env, storage, enums, etc.
	ic := g.collectImports(w.Stmts)
	ic.merge(g.collectImports(w.OnFail))
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(w.Name)
	fmt.Fprintf(&b, "// Worker: %s\n\n", name)
	queueName := workerQueueName(w)
	timeoutMS := workerTimeoutMS(w)
	retryCount := workerRetryCount(w)
	backoffExpr := workerBackoffExpr(w)
	fmt.Fprintf(&b, "export const %sQueueName = %q;\n", name, queueName)
	fmt.Fprintf(&b, "export const %sTimeoutMs = %d;\n", name, timeoutMS)
	fmt.Fprintf(&b, "export const %sRetryCount = %d;\n", name, retryCount)
	fmt.Fprintf(&b, "export const %sBackoff = %s;\n\n", name, backoffExpr)

	ctx := emitCtx{kind: "function"}
	fmt.Fprintf(&b, "export async function %s(data: any): Promise<void> {\n", name)
	g.emitArrowStmts(&b, w.Stmts, "  ", ctx)
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "export async function %sOnFail(data: any, error: Error): Promise<void> {\n", name)
	if len(w.OnFail) > 0 {
		g.emitArrowStmts(&b, w.OnFail, "  ", ctx)
	}
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/workers/%s.ts", toKebabCase(w.Name)),
		Content: []byte(b.String()),
	}
}

func workerQueueName(w *ast.Worker) string {
	for _, meta := range w.Meta {
		if meta.Kind != "trigger" || meta.Value == nil {
			continue
		}
		if fn, ok := meta.Value.(*ast.FnCall); ok && fn.Name == "queue" && len(fn.Args) > 0 {
			if name := exprToString(fn.Args[0]); name != "" {
				return name
			}
		}
		if name := exprToString(meta.Value); name != "" {
			return name
		}
	}
	return w.Name
}

func workerTimeoutMS(w *ast.Worker) int {
	for _, meta := range w.Meta {
		if meta.Kind == "timeout" && meta.Value != nil {
			if ms := workerMetaInt(meta.Value); ms > 0 {
				return ms
			}
			if dur, ok := meta.Value.(*ast.DurationLit); ok {
				return durationLiteralToMS(dur.Value)
			}
		}
	}
	return 0
}

func workerRetryCount(w *ast.Worker) int {
	for _, meta := range w.Meta {
		if meta.Kind == "retry" && meta.Value != nil {
			if n := workerMetaInt(meta.Value); n > 0 {
				return n
			}
		}
	}
	return 0
}

func workerBackoffExpr(w *ast.Worker) string {
	for _, meta := range w.Meta {
		if meta.Kind != "retry" || len(meta.Extra) == 0 {
			continue
		}
		parts := make([]string, 0, len(meta.Extra))
		for _, kv := range meta.Extra {
			parts = append(parts, fmt.Sprintf("%s: %s", toCamelCase(kv.Key), exprToJS(kv.Value)))
		}
		return fmt.Sprintf("{ %s }", strings.Join(parts, ", "))
	}
	return "null"
}

func workerMetaInt(e ast.Expr) int {
	if e == nil {
		return 0
	}
	if n, err := strconv.Atoi(exprToString(e)); err == nil {
		return n
	}
	return 0
}

func durationLiteralToMS(value string) int {
	value = strings.TrimSpace(value)
	var multiplier int
	var suffix string
	switch {
	case strings.HasSuffix(value, "ms"):
		suffix = "ms"
		multiplier = 1
	case strings.HasSuffix(value, "min"):
		suffix = "min"
		multiplier = 60 * 1000
	case strings.HasSuffix(value, "h"):
		suffix = "h"
		multiplier = 60 * 60 * 1000
	case strings.HasSuffix(value, "s"):
		suffix = "s"
		multiplier = 1000
	default:
		return 0
	}
	num := strings.TrimSuffix(value, suffix)
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0
	}
	return n * multiplier
}

// --- Schedules ---

func (g *Generator) genSchedule(s *ast.Schedule) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Add data imports if schedule logic uses data operations
	if stmtsHaveDataOps(s.Stmts) {
		writeDataImports(&b)
	}

	// Add imports for functions, env, storage, enums, etc.
	ic := g.collectImports(s.Stmts)
	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	name := toCamelCase(s.Name)
	fmt.Fprintf(&b, "// Schedule: %s — cron: %s\n\n", name, s.Cron)

	ctx := emitCtx{kind: "function"}
	fmt.Fprintf(&b, "export const %sCron = '%s';\n\n", name, s.Cron)
	fmt.Fprintf(&b, "export async function %s(): Promise<void> {\n", name)
	g.emitArrowStmts(&b, s.Stmts, "  ", ctx)
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/schedules/%s.ts", toKebabCase(s.Name)),
		Content: []byte(b.String()),
	}
}
