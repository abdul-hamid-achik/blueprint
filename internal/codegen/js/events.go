package js

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// --- Subscriptions ---

// genEventsLib generates the shared event subscription registry (src/lib/events.ts).
func (g *Generator) genEventsLib() codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString(`// Event subscription registry — wire up in src/index.ts
export type EventHandler = (event: unknown) => Promise<void>;
export const subscriptions: Record<string, EventHandler[]> = {};
export function on(event: string, handler: EventHandler) {
  if (!subscriptions[event]) subscriptions[event] = [];
  subscriptions[event].push(handler);
}
export async function emit(event: string, data: unknown) {
  for (const handler of subscriptions[event] ?? []) {
    await handler(data);
  }
}
`)
	return codegen.OutputFile{Path: "src/lib/events.ts", Content: []byte(b.String())}
}

// genSubscribe generates an event subscription handler file (src/subscriptions/<name>.ts).
func (g *Generator) genSubscribe(sub *ast.Subscribe) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))

	// Collect imports from subscription body
	ic := g.collectImports(sub.Stmts)

	// Add data imports if logic uses data operations
	if stmtsHaveDataOps(sub.Stmts) {
		writeDataImports(&b)
	}

	ic.writeImports(&b, g.hasStorage)
	b.WriteString("\n")

	// Handler function name: "on" + PascalCase(event)
	handlerName := "on" + toPascalCase(strings.ReplaceAll(sub.Event, ".", "_"))

	if sub.Intent != nil {
		b.WriteString(fmt.Sprintf("// %s\n", sub.Intent.Text))
	}
	b.WriteString(fmt.Sprintf("export async function %s(event: unknown): Promise<void> {\n", handlerName))

	ctx := emitCtx{
		kind:        "function",
		declared:    make(map[string]bool),
		asyncFns:    g.buildAsyncFns(),
		structEnums: g.structEnums,
	}
	g.emitArrowStmts(&b, sub.Stmts, "  ", ctx)
	b.WriteString("}\n")

	// Build file name from event name (replace dots with hyphens)
	fileName := toKebabCase(strings.ReplaceAll(sub.Event, ".", "-"))
	return codegen.OutputFile{
		Path:    fmt.Sprintf("src/subscriptions/%s.ts", fileName),
		Content: []byte(b.String()),
	}
}

// --- External ---

func (g *Generator) genExternal(externals []*ast.External) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { env } from './env.js';\n\n")

	for _, ext := range externals {
		rawName := strings.Trim(ext.Name, `"`)
		normalized := normalizeServiceName(rawName)
		configName := toCamelCase(normalized)
		pascalName := toPascalCase(normalized)
		b.WriteString(fmt.Sprintf("// External service: %s\n", ext.Name))
		b.WriteString(fmt.Sprintf("export const %s = {\n", configName))
		for _, kv := range ext.Entries {
			b.WriteString(fmt.Sprintf("  %s: %s,\n", toCamelCase(kv.Key), exprToJS(kv.Value)))
		}
		b.WriteString("};\n\n")

		// Generate the call helper function for this external service.
		// call<PascalName>(method, path, body?) wraps fetch with the service config.
		b.WriteString(fmt.Sprintf("export async function call%s(\n", pascalName))
		b.WriteString("  method: string,\n")
		b.WriteString("  path: string,\n")
		b.WriteString("  body?: unknown,\n")
		b.WriteString("): Promise<any> {\n")
		b.WriteString(fmt.Sprintf("  const baseUrl = (%s as any).url ?? (%s as any).base ?? '';\n", configName, configName))
		b.WriteString(fmt.Sprintf("  const timeout = (%s as any).timeout ?? 30000;\n", configName))
		b.WriteString("  const controller = new AbortController();\n")
		b.WriteString("  const timer = setTimeout(() => controller.abort(), timeout);\n")
		b.WriteString("  try {\n")
		b.WriteString("    const res = await fetch(`${baseUrl}${path}`, {\n")
		b.WriteString("      method,\n")
		b.WriteString("      signal: controller.signal,\n")
		b.WriteString("      ...(body !== undefined ? {\n")
		b.WriteString("        body: JSON.stringify(body),\n")
		b.WriteString("        headers: { 'Content-Type': 'application/json' },\n")
		b.WriteString("      } : {}),\n")
		b.WriteString("    });\n")
		b.WriteString("    if (!res.ok) throw new Error(`External call failed: ${res.status} ${res.statusText}`);\n")
		b.WriteString("    const ct = res.headers.get('content-type') ?? '';\n")
		b.WriteString("    if (ct.includes('application/json')) return res.json();\n")
		b.WriteString("    return res.text();\n")
		b.WriteString("  } finally {\n")
		b.WriteString("    clearTimeout(timer);\n")
		b.WriteString("  }\n")
		b.WriteString("}\n\n")
	}

	return codegen.OutputFile{Path: "src/lib/external.ts", Content: []byte(b.String())}
}
