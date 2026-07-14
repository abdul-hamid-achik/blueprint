package js

import (
	"fmt"
	"strconv"
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
// off() removes a previously registered handler — callers that subscribe
// per-connection (e.g. STREAM/SSE routes) must unsubscribe on disconnect or
// the handler list grows without bound for the lifetime of the process.
export function off(event: string, handler: EventHandler) {
  const handlers = subscriptions[event];
  if (!handlers) return;
  const idx = handlers.indexOf(handler);
  if (idx !== -1) handlers.splice(idx, 1);
}
export async function emit(event: string, data: unknown) {
  // Iterate a snapshot of the handlers array — a handler that calls off()
  // during emit() would splice the live array and cause other subscribers
  // to miss the event (e.g. a STREAM/SSE client disconnecting mid-emit).
  for (const handler of [...(subscriptions[event] ?? [])]) {
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
		fmt.Fprintf(&b, "// %s\n", sub.Intent.Text)
	}
	fmt.Fprintf(&b, "export async function %s(event: unknown): Promise<void> {\n", handlerName)

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

type externalAuthSpec struct {
	strategy   string
	header     string
	prefix     string
	namespace  string
	credential string
}

// validateExternalConfigurations prevents arbitrary external auth expressions
// from leaking into generated TypeScript. External credentials are a narrow,
// explicitly interpreted use of secret.NAME/env.NAME (similar to webhook auth),
// not general Blueprint expression evaluation.
func validateExternalConfigurations(file *ast.File) error {
	if file == nil {
		return nil
	}

	secrets := make(map[string]bool)
	envs := make(map[string]bool)
	for _, block := range file.Blocks {
		switch decl := block.(type) {
		case *ast.Secret:
			secrets[decl.Name] = true
		case *ast.Env:
			envs[decl.Name] = true
		}
	}

	for _, block := range file.Blocks {
		ext, ok := block.(*ast.External)
		if !ok {
			continue
		}

		seen := make(map[string]bool)
		for _, entry := range ext.Entries {
			key := strings.ToLower(entry.Key)
			if key != "auth" && key != "retry" {
				continue
			}
			if seen[key] {
				return fmt.Errorf("node target cannot generate external %q at %s: duplicate %s entry", ext.Name, entry.Loc, key)
			}
			seen[key] = true

			switch key {
			case "auth":
				spec, err := parseExternalAuth(entry.Value)
				if err != nil {
					return fmt.Errorf("node target cannot generate auth for external %q at %s: %w", ext.Name, entry.Loc, err)
				}
				declared := secrets
				declarationKind := "secret"
				if spec.namespace == "env" {
					declared = envs
					declarationKind = "env"
				}
				if !declared[spec.credential] {
					return fmt.Errorf(
						"node target cannot generate auth for external %q at %s: %s.%s references an undeclared %s; declare it before the external block",
						ext.Name, entry.Loc, spec.namespace, spec.credential, declarationKind,
					)
				}
			case "retry":
				if _, err := parseExternalRetry(entry.Value); err != nil {
					return fmt.Errorf("node target cannot generate retry for external %q at %s: %w", ext.Name, entry.Loc, err)
				}
			}
		}
	}

	return nil
}

func parseExternalAuth(expr ast.Expr) (externalAuthSpec, error) {
	call, ok := expr.(*ast.FnCall)
	if !ok {
		return externalAuthSpec{}, fmt.Errorf("auth must be bearer(secret.NAME), jwt(secret.NAME), basic(secret.NAME), or api_key(secret.NAME)")
	}
	if len(call.Args) != 1 {
		return externalAuthSpec{}, fmt.Errorf("auth strategy %q requires exactly one credential", call.Name)
	}

	spec := externalAuthSpec{strategy: strings.ToLower(call.Name)}
	switch spec.strategy {
	case "bearer", "jwt":
		spec.header = "Authorization"
		spec.prefix = "Bearer "
	case "basic":
		spec.header = "Authorization"
		spec.prefix = "Basic "
	case "api_key":
		spec.header = "X-API-Key"
	default:
		return externalAuthSpec{}, fmt.Errorf("unsupported auth strategy %q; supported strategies are bearer, jwt, basic, and api_key", call.Name)
	}

	field, ok := call.Args[0].(*ast.FieldAccess)
	if !ok {
		return externalAuthSpec{}, fmt.Errorf("auth strategy %q must read its credential from secret.NAME or env.NAME", call.Name)
	}
	base, ok := field.Base.(*ast.Ident)
	if !ok || (base.Name != "secret" && base.Name != "env") || field.Field == "" {
		return externalAuthSpec{}, fmt.Errorf("auth strategy %q must read its credential from secret.NAME or env.NAME", call.Name)
	}
	spec.namespace = base.Name
	spec.credential = field.Field
	return spec, nil
}

func parseExternalRetry(expr ast.Expr) (int, error) {
	lit, ok := expr.(*ast.IntLit)
	if !ok {
		return 0, fmt.Errorf("retry must be a non-negative integer counting additional attempts")
	}
	count, err := strconv.Atoi(lit.Value)
	if err != nil || count < 0 {
		return 0, fmt.Errorf("retry must be a non-negative integer counting additional attempts")
	}
	return count, nil
}

func (g *Generator) genExternal(externals []*ast.External) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { env } from './env.js';\n\n")
	b.WriteString(`type ExternalAuthConfig = {
  strategy: 'bearer' | 'jwt' | 'basic' | 'api_key';
  header: string;
  prefix: string;
  credential: string;
  value: string | undefined;
};

type ExternalServiceConfig = {
  url?: string;
  base?: string;
  timeout?: number;
  retry?: number;
  auth?: ExternalAuthConfig;
  [key: string]: unknown;
};

function externalHeaders(config: ExternalServiceConfig, serviceName: string, hasBody: boolean): Record<string, string> {
  const headers: Record<string, string> = {};
  if (hasBody) headers['Content-Type'] = 'application/json';
  if (config.auth) {
    if (!config.auth.value) {
      throw new Error('External service ' + serviceName + ' auth credential ' + config.auth.credential + ' is missing');
    }
    headers[config.auth.header] = config.auth.prefix + config.auth.value;
  }
  return headers;
}

function retryableExternalStatus(status: number): boolean {
  return status === 408 || status === 429 || status >= 500;
}

async function callExternal(
  config: ExternalServiceConfig,
  serviceName: string,
  method: string,
  path: string,
  body?: unknown,
): Promise<any> {
  const baseUrl = config.url ?? config.base ?? '';
  const timeout = config.timeout ?? 30000;
  const retryCount = config.retry ?? 0;
  const headers = externalHeaders(config, serviceName, body !== undefined);
  let lastError: unknown;

  for (let attempt = 0; attempt <= retryCount; attempt++) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeout);
    let result: { response: Response } | { error: unknown };
    try {
      result = {
        response: await fetch(baseUrl + path, {
          method,
          signal: controller.signal,
          headers,
          ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
        }),
      };
    } catch (error) {
      result = { error };
    } finally {
      clearTimeout(timer);
    }

    if ('error' in result) {
      lastError = result.error;
      if (attempt < retryCount) continue;
      throw result.error;
    }

    const res = result.response;
    if (!res.ok) {
      const error = new Error('External call failed: ' + res.status + ' ' + res.statusText);
      if (attempt < retryCount && retryableExternalStatus(res.status)) {
        lastError = error;
        continue;
      }
      throw error;
    }

    const contentType = res.headers.get('content-type') ?? '';
    if (contentType.includes('application/json')) return res.json();
    return res.text();
  }

  throw lastError instanceof Error ? lastError : new Error('External call to ' + serviceName + ' failed');
}

`)

	for _, ext := range externals {
		rawName := strings.Trim(ext.Name, `"`)
		normalized := normalizeServiceName(rawName)
		configName := toCamelCase(normalized)
		pascalName := toPascalCase(normalized)
		fmt.Fprintf(&b, "// External service: %s\n", ext.Name)
		fmt.Fprintf(&b, "export const %s = {\n", configName)
		for _, kv := range ext.Entries {
			if strings.EqualFold(kv.Key, "auth") {
				auth, _ := parseExternalAuth(kv.Value) // validated by Files before emission
				fmt.Fprintf(
					&b,
					"  auth: { strategy: %q, header: %q, prefix: %q, credential: %q, value: env.%s == null ? undefined : String(env.%s) },\n",
					auth.strategy, auth.header, auth.prefix, auth.credential, auth.credential,
					auth.credential,
				)
				continue
			}
			fmt.Fprintf(&b, "  %s: %s,\n", toCamelCase(kv.Key), exprToJS(kv.Value))
		}
		b.WriteString("} satisfies ExternalServiceConfig;\n\n")

		// Generate the call helper function for this external service.
		// call<PascalName>(method, path, body?) wraps fetch with the service config.
		fmt.Fprintf(&b, "export async function call%s(\n", pascalName)
		b.WriteString("  method: string,\n")
		b.WriteString("  path: string,\n")
		b.WriteString("  body?: unknown,\n")
		b.WriteString("): Promise<any> {\n")
		fmt.Fprintf(&b, "  return callExternal(%s, %q, method, path, body);\n", configName, rawName)
		b.WriteString("}\n\n")
	}

	return codegen.OutputFile{Path: "src/lib/external.ts", Content: []byte(b.String())}
}
