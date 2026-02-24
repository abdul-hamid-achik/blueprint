package js

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

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
			b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<%s> {\n",
				name, strings.Join(params, ", "), retType))
			b.WriteString(fmt.Sprintf("  const { stdout } = await execFileAsync(%q, [%s]);\n", cmd, argsJS))
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
			b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<%s> {\n",
				name, strings.Join(params, ", "), retType))
			if bodyExpr != "" {
				b.WriteString(fmt.Sprintf("  const res = await fetch(%q, {\n", url))
				b.WriteString(fmt.Sprintf("    method: %q,\n", method))
				b.WriteString(fmt.Sprintf("    body: JSON.stringify(%s),\n", bodyExpr))
				b.WriteString("    headers: { 'Content-Type': 'application/json' },\n")
				b.WriteString("  });\n")
			} else {
				b.WriteString(fmt.Sprintf("  const res = await fetch(%q, { method: %q });\n", url, method))
			}
			b.WriteString("  return res.json();\n")
			b.WriteString("}\n")

		default:
			// "node" strategy or unrecognized — native module implementation
			for _, kv := range fn.Impl.Entries {
				if kv.Key == "module" {
					mod := exprToString(kv.Value)
					// Ensure .js extension for ESM compatibility
					if !strings.HasSuffix(mod, ".js") {
						mod = mod + ".js"
					}
					funcName := name
					for _, kv2 := range fn.Impl.Entries {
						if kv2.Key == "func" {
							funcName = exprToString(kv2.Value)
						}
					}
					b.WriteString(fmt.Sprintf("import { %s as %sImpl } from '%s';\n\n", funcName, name, mod))
					b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<%s> {\n",
						name, strings.Join(params, ", "), retType))
					args := make([]string, len(fn.Inputs))
					for i, inp := range fn.Inputs {
						args[i] = toCamelCase(inp.Name)
					}
					b.WriteString(fmt.Sprintf("  return %sImpl(%s);\n", name, strings.Join(args, ", ")))
					b.WriteString("}\n")
					// Generate a stub for local internal module paths (./internal/...)
					rawMod := strings.TrimSuffix(mod, ".js")
					if strings.HasPrefix(rawMod, "./internal/") {
						stubPath := fmt.Sprintf("src/functions/%s.ts", strings.TrimPrefix(rawMod, "./"))
						var sb strings.Builder
						sb.WriteString(fmt.Sprintf("// Stub generated by Blueprint — implement %s here\n", funcName))
						sb.WriteString(fmt.Sprintf("export async function %s(...args: any[]): Promise<any> {\n", funcName))
						sb.WriteString(fmt.Sprintf("  throw new Error('Not implemented: %s');\n", funcName))
						sb.WriteString("}\n")
						extraFiles = append(extraFiles, codegen.OutputFile{
							Path:    stubPath,
							Content: []byte(sb.String()),
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
		b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<%s> {\n",
			name, strings.Join(params, ", "), retType))
		g.emitArrowStmts(&b, fn.Logic.Stmts, "  ", fnCtx)
		b.WriteString("}\n")
	}

	mainFile := codegen.OutputFile{
		Path:    fmt.Sprintf("src/functions/%s.ts", toKebabCase(fn.Name)),
		Content: []byte(b.String()),
	}
	return append([]codegen.OutputFile{mainFile}, extraFiles...)
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

	b.WriteString(fmt.Sprintf("export async function %s(%s): Promise<any> {\n", name, params))
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

	b.WriteString(fmt.Sprintf("export const %s = createMiddleware(async (c, next) => {\n", name))

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
	b.WriteString(fmt.Sprintf("// Worker: %s\n\n", name))

	ctx := emitCtx{kind: "function"}
	b.WriteString(fmt.Sprintf("export async function %s(data: any): Promise<void> {\n", name))
	g.emitArrowStmts(&b, w.Stmts, "  ", ctx)
	if len(w.OnFail) > 0 {
		b.WriteString("}\n\n")
		b.WriteString(fmt.Sprintf("export async function %sOnFail(data: any, error: Error): Promise<void> {\n", name))
		g.emitArrowStmts(&b, w.OnFail, "  ", ctx)
	}
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/workers/%s.ts", toKebabCase(w.Name)),
		Content: []byte(b.String()),
	}
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
	b.WriteString(fmt.Sprintf("// Schedule: %s — cron: %s\n\n", name, s.Cron))

	ctx := emitCtx{kind: "function"}
	b.WriteString(fmt.Sprintf("export const %sCron = '%s';\n\n", name, s.Cron))
	b.WriteString(fmt.Sprintf("export async function %s(): Promise<void> {\n", name))
	g.emitArrowStmts(&b, s.Stmts, "  ", ctx)
	b.WriteString("}\n")

	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/schedules/%s.ts", toKebabCase(s.Name)),
		Content: []byte(b.String()),
	}
}
