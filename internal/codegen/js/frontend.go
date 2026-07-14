package js

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
	"github.com/abdul-hamid-achik/blueprint/internal/resolve"
)

type frontendBinding struct {
	input           *ast.InputStmt
	typeExpr        ast.TypeExpr
	modelName       string
	collectionModel string
	paginated       bool
	relationships   []string
	expr            ast.Expr
}

type frontendTypeInfo struct {
	ts  string
	zod string
}

type frontendEndpointInputs struct {
	all   []*ast.InputStmt
	path  []*ast.InputStmt
	query []*ast.InputStmt
	body  []*ast.InputStmt
}

func (g *Generator) genFrontendTypes(models []*ast.Model, types []*ast.TypeDecl, aliases []*ast.Alias, enums []*ast.Enum, states []*ast.StateMachine, endpoints []*ast.Endpoint, streams []*ast.StreamEndpoint, ws []*ast.WsEndpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("// Frontend-safe types generated from your Blueprint schema and endpoints\n\n")
	b.WriteString("export type PaginatedResult<T> = {\n")
	b.WriteString("  items: T[];\n")
	b.WriteString("  total: number;\n")
	b.WriteString("};\n\n")
	b.WriteString("export interface ApiError {\n")
	b.WriteString("  status: number;\n")
	b.WriteString("  message: string;\n")
	b.WriteString("  body?: unknown;\n")
	b.WriteString("}\n\n")

	if len(enums) > 0 {
		for _, e := range enums {
			name := e.Name
			fmt.Fprintf(&b, "export const %s = {\n", name)
			for _, v := range e.Variants {
				fmt.Fprintf(&b, "  %s: '%s',\n", v.Name, v.Name)
			}
			b.WriteString("} as const;\n")
			fmt.Fprintf(&b, "export type %s = keyof typeof %s;\n\n", name, name)
			if g.structEnums[e.Name] {
				fmt.Fprintf(&b, "export const %sConfig = {\n", name)
				for _, v := range e.Variants {
					if v.Body != nil && len(v.Body.Entries) > 0 {
						parts := make([]string, len(v.Body.Entries))
						for i, kv := range v.Body.Entries {
							parts[i] = fmt.Sprintf("%s: %s", toCamelCase(kv.Key), exprToJS(kv.Value))
						}
						fmt.Fprintf(&b, "  %s: { %s },\n", v.Name, strings.Join(parts, ", "))
					} else {
						fmt.Fprintf(&b, "  %s: {},\n", v.Name)
					}
				}
				b.WriteString("} as const;\n")
				fmt.Fprintf(&b, "export type %sConfig = typeof %sConfig[%s];\n\n", name, name, name)
			}
		}
	}

	if len(aliases) > 0 {
		for _, a := range aliases {
			fmt.Fprintf(&b, "export type %s = %s;\n\n", a.Name, typeToTS(a.Type))
		}
	}

	for _, s := range states {
		name := toPascalCase(s.Name)
		parts := make([]string, len(s.States))
		for i, state := range s.States {
			parts[i] = fmt.Sprintf("%q", state)
		}
		fmt.Fprintf(&b, "export const %sStates = [%s] as const;\n", name, strings.Join(parts, ", "))
		fmt.Fprintf(&b, "export type %s = (typeof %sStates)[number];\n\n", name, name)
	}

	if len(types) > 0 {
		for _, t := range types {
			fmt.Fprintf(&b, "export interface %s {\n", toPascalCase(t.Name))
			for _, f := range t.Fields {
				opt := ""
				if frontendConstraintsOptional(f.Constraints) {
					opt = "?"
				}
				fmt.Fprintf(&b, "  %s%s: %s;\n", toCamelCase(f.Name), opt, typeToTS(f.Type))
			}
			b.WriteString("}\n\n")
		}
	}

	if len(models) > 0 {
		for _, m := range models {
			fmt.Fprintf(&b, "export interface %s {\n", toPascalCase(m.Name))
			for _, f := range m.Fields {
				info := g.frontendTypeInfoFromModelField(f)
				fmt.Fprintf(&b, "  %s: %s;\n", toCamelCase(f.Name), info.ts)
			}
			for _, f := range m.ComputedFields {
				fmt.Fprintf(&b, "  %s: %s;\n", toCamelCase(f.Name), typeToTS(f.Type))
			}
			b.WriteString("}\n\n")
		}
	}

	for _, ep := range endpoints {
		baseName := frontendEndpointBaseName(ep.Method, ep.Path)
		inputs := frontendEndpointInputGroups(ep)
		if len(inputs.all) > 0 {
			fmt.Fprintf(&b, "export interface %sRequest {\n", baseName)
			for _, inp := range inputs.all {
				opt := ""
				if frontendInputOptional(inp) {
					opt = "?"
				}
				fmt.Fprintf(&b, "  %s%s: %s;\n", inp.Name, opt, typeToTS(inp.Type))
			}
			b.WriteString("}\n\n")
		}
		resp := g.frontendEndpointResponseInfo(ep)
		fmt.Fprintf(&b, "export type %sResponse = %s;\n\n", baseName, indentMultiline(resp.ts, "  "))
	}

	for _, se := range streams {
		baseName := frontendStreamBaseName(se.Path)
		params := extractPathParams(se.Path)
		if len(params) > 0 {
			fmt.Fprintf(&b, "export interface %sRequest {\n", baseName)
			for _, param := range params {
				fmt.Fprintf(&b, "  %s: string;\n", param)
			}
			b.WriteString("}\n\n")
		}
		for _, h := range se.Handlers {
			payloadName := frontendStreamEventTypeName(baseName, frontendStreamEventName(h))
			info := g.frontendStreamHandlerInfo(h)
			fmt.Fprintf(&b, "export type %s = %s;\n\n", payloadName, indentMultiline(info.ts, "  "))
		}
		fmt.Fprintf(&b, "export interface %sHandlers {\n", baseName)
		b.WriteString("  onOpen?: () => void;\n")
		b.WriteString("  onError?: (error: unknown) => void;\n")
		for _, h := range se.Handlers {
			eventName := frontendStreamEventName(h)
			payloadName := frontendStreamEventTypeName(baseName, eventName)
			fmt.Fprintf(&b, "  %s?: (payload: %s) => void;\n", eventName, payloadName)
		}
		b.WriteString("}\n\n")
	}

	for _, we := range ws {
		baseName := frontendWsBaseName(we.Path)
		params := extractPathParams(we.Path)
		if len(params) > 0 {
			fmt.Fprintf(&b, "export interface %sRequest {\n", baseName)
			for _, param := range params {
				fmt.Fprintf(&b, "  %s: string;\n", param)
			}
			b.WriteString("}\n\n")
		}
		messageTypeNames := g.writeFrontendWsPayloadTypes(&b, baseName, we)
		if len(messageTypeNames) == 0 {
			fmt.Fprintf(&b, "export type %sMessage = unknown;\n\n", baseName)
		} else {
			fmt.Fprintf(&b, "export type %sMessage = %s;\n\n", baseName, strings.Join(messageTypeNames, " | "))
		}
	}

	b.WriteString("export interface WsConnection<TMessage = unknown> {\n")
	b.WriteString("  socket: unknown;\n")
	b.WriteString("  send(data: unknown): void;\n")
	b.WriteString("  close(code?: number, reason?: string): void;\n")
	b.WriteString("  onOpen(handler: () => void): void;\n")
	b.WriteString("  onMessage(handler: (message: TMessage) => void): void;\n")
	b.WriteString("  onClose(handler: (event: unknown) => void): void;\n")
	b.WriteString("  onError(handler: (event: unknown) => void): void;\n")
	b.WriteString("}\n\n")

	b.WriteString("export interface RestApiClient {\n")
	for _, ep := range endpoints {
		baseName := frontendEndpointBaseName(ep.Method, ep.Path)
		methodName := frontendRestMethodName(ep.Method, ep.Path)
		if len(frontendEndpointInputGroups(ep).all) > 0 {
			fmt.Fprintf(&b, "  %s(input: %sRequest): Promise<%sResponse>;\n", methodName, baseName, baseName)
		} else {
			fmt.Fprintf(&b, "  %s(): Promise<%sResponse>;\n", methodName, baseName)
		}
	}
	b.WriteString("}\n\n")

	b.WriteString("export interface StreamApiClient {\n")
	for _, se := range streams {
		baseName := frontendStreamBaseName(se.Path)
		methodName := frontendStreamMethodName(se.Path)
		if len(extractPathParams(se.Path)) > 0 {
			fmt.Fprintf(&b, "  %s(handlers: %sHandlers, input: %sRequest): () => void;\n", methodName, baseName, baseName)
		} else {
			fmt.Fprintf(&b, "  %s(handlers: %sHandlers): () => void;\n", methodName, baseName)
		}
	}
	b.WriteString("}\n\n")

	b.WriteString("export interface WsApiClient {\n")
	for _, we := range ws {
		baseName := frontendWsBaseName(we.Path)
		methodName := frontendWsMethodName(we.Path)
		if len(extractPathParams(we.Path)) > 0 {
			fmt.Fprintf(&b, "  %s(input: %sRequest): WsConnection<%sMessage>;\n", methodName, baseName, baseName)
		} else {
			fmt.Fprintf(&b, "  %s(): WsConnection<%sMessage>;\n", methodName, baseName)
		}
	}
	b.WriteString("}\n\n")

	b.WriteString("export interface ApiClient {\n")
	b.WriteString("  rest: RestApiClient;\n")
	b.WriteString("  streams: StreamApiClient;\n")
	b.WriteString("  ws: WsApiClient;\n")
	b.WriteString("}\n")

	return codegen.OutputFile{Path: "src/types/api.ts", Content: []byte(b.String())}
}

func (g *Generator) genFrontendSchemas(models []*ast.Model, types []*ast.TypeDecl, aliases []*ast.Alias, enums []*ast.Enum, states []*ast.StateMachine, endpoints []*ast.Endpoint, streams []*ast.StreamEndpoint, ws []*ast.WsEndpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import { z } from 'zod';\n\n")
	b.WriteString("export const ApiErrorSchema = z.object({\n")
	b.WriteString("  error: z.string(),\n")
	b.WriteString("}).passthrough();\n\n")
	b.WriteString("export const paginatedResultSchema = <T extends z.ZodTypeAny>(itemSchema: T) => z.object({\n")
	b.WriteString("  items: z.array(itemSchema),\n")
	b.WriteString("  total: z.number().int(),\n")
	b.WriteString("});\n\n")

	for _, e := range enums {
		variants := make([]string, len(e.Variants))
		for i, v := range e.Variants {
			variants[i] = fmt.Sprintf(`"%s"`, v.Name)
		}
		fmt.Fprintf(&b, "export const %s = z.enum([%s]);\n\n", frontendSchemaName(e.Name), strings.Join(variants, ", "))
	}

	for _, a := range aliases {
		fmt.Fprintf(&b, "export const %s = %s%s;\n\n", frontendSchemaName(a.Name), frontendTypeToZod(a.Type), constraintsToZod(a.Constraints))
	}

	for _, s := range states {
		variants := make([]string, len(s.States))
		for i, state := range s.States {
			variants[i] = fmt.Sprintf(`"%s"`, state)
		}
		fmt.Fprintf(&b, "export const %s = z.enum([%s]);\n\n", frontendSchemaName(s.Name), strings.Join(variants, ", "))
	}

	for _, t := range types {
		fmt.Fprintf(&b, "export const %s = z.object({\n", frontendSchemaName(t.Name))
		for _, f := range t.Fields {
			fieldSchema := frontendTypeToZod(f.Type) + constraintsToZod(f.Constraints)
			fmt.Fprintf(&b, "  %s: %s,\n", toCamelCase(f.Name), fieldSchema)
		}
		b.WriteString("});\n\n")
	}

	for _, m := range models {
		fmt.Fprintf(&b, "export const %s = z.object({\n", frontendSchemaName(m.Name))
		for _, f := range m.Fields {
			info := g.frontendTypeInfoFromModelField(f)
			fmt.Fprintf(&b, "  %s: %s,\n", toCamelCase(f.Name), info.zod)
		}
		for _, f := range m.ComputedFields {
			fmt.Fprintf(&b, "  %s: %s,\n", toCamelCase(f.Name), frontendTypeToZod(f.Type))
		}
		b.WriteString("});\n\n")
	}

	for _, ep := range endpoints {
		baseName := frontendEndpointBaseName(ep.Method, ep.Path)
		inputs := frontendEndpointInputGroups(ep)
		if len(inputs.all) > 0 {
			fmt.Fprintf(&b, "export const %sRequestSchema = z.object({\n", baseName)
			for _, inp := range inputs.all {
				fmt.Fprintf(&b, "  %s: %s%s,\n", inp.Name, frontendTypeToZod(inp.Type), constraintsToZod(inp.Constraints))
			}
			b.WriteString("});\n\n")
		}
		resp := g.frontendEndpointResponseInfo(ep)
		if resp.ts != "void" {
			fmt.Fprintf(&b, "export const %sResponseSchema = %s;\n\n", baseName, indentMultiline(resp.zod, "  "))
		}
	}

	for _, se := range streams {
		baseName := frontendStreamBaseName(se.Path)
		params := extractPathParams(se.Path)
		if len(params) > 0 {
			fmt.Fprintf(&b, "export const %sRequestSchema = z.object({\n", baseName)
			for _, param := range params {
				fmt.Fprintf(&b, "  %s: z.string(),\n", param)
			}
			b.WriteString("});\n\n")
		}
		for _, h := range se.Handlers {
			payloadName := frontendStreamEventTypeName(baseName, frontendStreamEventName(h))
			info := g.frontendStreamHandlerInfo(h)
			fmt.Fprintf(&b, "export const %sSchema = %s;\n\n", payloadName, indentMultiline(info.zod, "  "))
		}
	}

	for _, we := range ws {
		baseName := frontendWsBaseName(we.Path)
		params := extractPathParams(we.Path)
		if len(params) > 0 {
			fmt.Fprintf(&b, "export const %sRequestSchema = z.object({\n", baseName)
			for _, param := range params {
				fmt.Fprintf(&b, "  %s: z.string(),\n", param)
			}
			b.WriteString("});\n\n")
		}
		messageSchemaNames := g.writeFrontendWsPayloadSchemas(&b, baseName, we)
		if len(messageSchemaNames) == 0 {
			fmt.Fprintf(&b, "export const %sMessageSchema = z.unknown();\n\n", baseName)
		} else if len(messageSchemaNames) == 1 {
			fmt.Fprintf(&b, "export const %sMessageSchema = %s;\n\n", baseName, messageSchemaNames[0])
		} else {
			fmt.Fprintf(&b, "export const %sMessageSchema = z.union([%s]);\n\n", baseName, strings.Join(messageSchemaNames, ", "))
		}
	}

	return codegen.OutputFile{Path: "src/types/schemas.ts", Content: []byte(b.String())}
}

func (g *Generator) genFrontendClient(endpoints []*ast.Endpoint, streams []*ast.StreamEndpoint, ws []*ast.WsEndpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import type * as Api from './api.js';\n")
	b.WriteString("import * as Schemas from './schemas.js';\n\n")
	b.WriteString("type SchemaParser = { parse(value: unknown): unknown };\n\n")
	b.WriteString("interface EventSourceLike {\n")
	b.WriteString("  addEventListener(type: string, listener: (event: { data: string }) => void): void;\n")
	b.WriteString("  close(): void;\n")
	b.WriteString("  onerror: ((event: unknown) => void) | null;\n")
	b.WriteString("  onopen?: ((event: unknown) => void) | null;\n")
	b.WriteString("}\n\n")
	b.WriteString("interface WebSocketLike {\n")
	b.WriteString("  addEventListener(type: string, listener: (event: any) => void): void;\n")
	b.WriteString("  send(data: string): void;\n")
	b.WriteString("  close(code?: number, reason?: string): void;\n")
	b.WriteString("}\n\n")
	b.WriteString("export interface ApiClientOptions {\n")
	b.WriteString("  baseUrl: string;\n")
	b.WriteString("  validateResponses?: boolean;\n")
	b.WriteString("  getAuthHeader?: () => string | null | undefined;\n")
	b.WriteString("  fetch?: typeof fetch;\n")
	b.WriteString("  createEventSource?: (url: string) => EventSourceLike;\n")
	b.WriteString("  createWebSocket?: (url: string) => WebSocketLike;\n")
	b.WriteString("}\n\n")
	b.WriteString("export class ApiClientError extends Error implements Api.ApiError {\n")
	b.WriteString("  status: number;\n")
	b.WriteString("  body?: unknown;\n\n")
	b.WriteString("  constructor(status: number, message: string, body?: unknown) {\n")
	b.WriteString("    super(message);\n")
	b.WriteString("    this.name = 'ApiClientError';\n")
	b.WriteString("    this.status = status;\n")
	b.WriteString("    this.body = body;\n")
	b.WriteString("  }\n")
	b.WriteString("}\n\n")
	b.WriteString("function trimTrailingSlashes(value: string): string {\n")
	b.WriteString("  return value.replace(/\\/+$/, '');\n")
	b.WriteString("}\n\n")
	b.WriteString("function serializeValue(value: unknown): string {\n")
	b.WriteString("  if (value instanceof Date) return value.toISOString();\n")
	b.WriteString("  return String(value);\n")
	b.WriteString("}\n\n")
	b.WriteString("function appendQuery(query: URLSearchParams, key: string, value: unknown): void {\n")
	b.WriteString("  if (value === undefined || value === null) return;\n")
	b.WriteString("  if (Array.isArray(value)) {\n")
	b.WriteString("    for (const item of value) {\n")
	b.WriteString("      query.append(key, serializeValue(item));\n")
	b.WriteString("    }\n")
	b.WriteString("    return;\n")
	b.WriteString("  }\n")
	b.WriteString("  query.set(key, serializeValue(value));\n")
	b.WriteString("}\n\n")
	b.WriteString("function safeJsonParse(value: string): unknown {\n")
	b.WriteString("  try {\n")
	b.WriteString("    return JSON.parse(value);\n")
	b.WriteString("  } catch {\n")
	b.WriteString("    return value;\n")
	b.WriteString("  }\n")
	b.WriteString("}\n\n")
	b.WriteString("async function readResponseBody(res: Response): Promise<unknown> {\n")
	b.WriteString("  const text = await res.text();\n")
	b.WriteString("  if (!text) return undefined;\n")
	b.WriteString("  const contentType = res.headers.get('content-type') ?? '';\n")
	b.WriteString("  if (contentType.includes('application/json')) {\n")
	b.WriteString("    return safeJsonParse(text);\n")
	b.WriteString("  }\n")
	b.WriteString("  return text;\n")
	b.WriteString("}\n\n")
	b.WriteString("function buildHeaders(getAuthHeader?: () => string | null | undefined): HeadersInit {\n")
	b.WriteString("  const headers: Record<string, string> = {};\n")
	b.WriteString("  const authHeader = getAuthHeader?.();\n")
	b.WriteString("  if (authHeader) headers.Authorization = authHeader;\n")
	b.WriteString("  return headers;\n")
	b.WriteString("}\n\n")
	b.WriteString("function toWebSocketUrl(baseUrl: string, path: string): string {\n")
	b.WriteString("  const url = new URL(path, `${trimTrailingSlashes(baseUrl)}/`);\n")
	b.WriteString("  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';\n")
	b.WriteString("  return url.toString();\n")
	b.WriteString("}\n\n")
	b.WriteString("async function requestJson<T>(\n")
	b.WriteString("  doFetch: typeof fetch,\n")
	b.WriteString("  url: string,\n")
	b.WriteString("  init: RequestInit,\n")
	b.WriteString("  schema: SchemaParser | null,\n")
	b.WriteString("  validateResponses: boolean,\n")
	b.WriteString("): Promise<T> {\n")
	b.WriteString("  const res = await doFetch(url, init);\n")
	b.WriteString("  const body = await readResponseBody(res);\n")
	b.WriteString("  if (!res.ok) {\n")
	b.WriteString("    const parsedError = body === undefined ? undefined : Schemas.ApiErrorSchema.safeParse(body);\n")
	b.WriteString("    const message = parsedError?.success ? parsedError.data.error : `Request failed with status ${res.status}`;\n")
	b.WriteString("    throw new ApiClientError(res.status, message, body);\n")
	b.WriteString("  }\n")
	b.WriteString("  if (!schema || body === undefined) {\n")
	b.WriteString("    return body as T;\n")
	b.WriteString("  }\n")
	b.WriteString("  return (validateResponses ? schema.parse(body) : body) as T;\n")
	b.WriteString("}\n\n")
	b.WriteString("function parseEventPayload<T>(schema: SchemaParser, raw: string, validateResponses: boolean): T {\n")
	b.WriteString("  const parsed = safeJsonParse(raw);\n")
	b.WriteString("  return (validateResponses ? schema.parse(parsed) : parsed) as T;\n")
	b.WriteString("}\n\n")
	b.WriteString("export function createApiClient(options: ApiClientOptions): Api.ApiClient {\n")
	b.WriteString("  const baseUrl = trimTrailingSlashes(options.baseUrl);\n")
	b.WriteString("  const validateResponses = options.validateResponses ?? true;\n")
	b.WriteString("  const doFetch = options.fetch ?? fetch;\n")
	b.WriteString("  const createEventSource = options.createEventSource ?? ((url: string) => new (globalThis as any).EventSource(url));\n")
	b.WriteString("  const createWebSocket = options.createWebSocket ?? ((url: string) => new (globalThis as any).WebSocket(url));\n\n")
	b.WriteString("  return {\n")
	b.WriteString("    rest: {\n")
	for _, ep := range endpoints {
		baseName := frontendEndpointBaseName(ep.Method, ep.Path)
		methodName := frontendRestMethodName(ep.Method, ep.Path)
		inputs := frontendEndpointInputGroups(ep)
		if len(inputs.all) > 0 {
			fmt.Fprintf(&b, "      async %s(input: Api.%sRequest): Promise<Api.%sResponse> {\n", methodName, baseName, baseName)
			fmt.Fprintf(&b, "        const request = Schemas.%sRequestSchema.parse(input);\n", baseName)
		} else {
			fmt.Fprintf(&b, "      async %s(): Promise<Api.%sResponse> {\n", methodName, baseName)
		}
		fmt.Fprintf(&b, "        const url = new URL('%s', `${baseUrl}/`);\n", ep.Path)
		for _, inp := range inputs.path {
			fmt.Fprintf(&b, "        url.pathname = url.pathname.replace(':%s', encodeURIComponent(serializeValue(request.%s)));\n", inp.Name, inp.Name)
		}
		for _, inp := range inputs.query {
			fmt.Fprintf(&b, "        appendQuery(url.searchParams, '%s', request.%s);\n", inp.Name, inp.Name)
		}
		b.WriteString("        const headers = buildHeaders(options.getAuthHeader);\n")
		bodyKeys := make([]string, len(inputs.body))
		for i, inp := range inputs.body {
			bodyKeys[i] = fmt.Sprintf("%s: request.%s", inp.Name, inp.Name)
		}
		if len(inputs.body) > 0 {
			b.WriteString("        const body = {")
			for i, part := range bodyKeys {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(part)
			}
			b.WriteString("};\n")
			b.WriteString("        return requestJson(\n")
			b.WriteString("          doFetch,\n")
			b.WriteString("          url.toString(),\n")
			fmt.Fprintf(&b, "          { method: '%s', headers: { ...headers, 'Content-Type': 'application/json' }, body: JSON.stringify(body) },\n", ep.Method)
		} else {
			b.WriteString("        return requestJson(\n")
			b.WriteString("          doFetch,\n")
			b.WriteString("          url.toString(),\n")
			fmt.Fprintf(&b, "          { method: '%s', headers },\n", ep.Method)
		}
		resp := g.frontendEndpointResponseInfo(ep)
		if resp.ts == "void" {
			b.WriteString("          null,\n")
		} else {
			fmt.Fprintf(&b, "          Schemas.%sResponseSchema,\n", baseName)
		}
		b.WriteString("          validateResponses,\n")
		b.WriteString("        ) as Promise<Api.")
		b.WriteString(baseName)
		b.WriteString("Response>;\n")
		b.WriteString("      },\n")
	}
	b.WriteString("    },\n")
	b.WriteString("    streams: {\n")
	for _, se := range streams {
		baseName := frontendStreamBaseName(se.Path)
		methodName := frontendStreamMethodName(se.Path)
		if len(extractPathParams(se.Path)) > 0 {
			fmt.Fprintf(&b, "      %s(handlers: Api.%sHandlers, input: Api.%sRequest): () => void {\n", methodName, baseName, baseName)
			fmt.Fprintf(&b, "        const request = Schemas.%sRequestSchema.parse(input);\n", baseName)
		} else {
			fmt.Fprintf(&b, "      %s(handlers: Api.%sHandlers): () => void {\n", methodName, baseName)
		}
		fmt.Fprintf(&b, "        const url = new URL('%s', `${baseUrl}/`);\n", se.Path)
		for _, param := range extractPathParams(se.Path) {
			fmt.Fprintf(&b, "        url.pathname = url.pathname.replace(':%s', encodeURIComponent(serializeValue(request.%s)));\n", param, param)
		}
		b.WriteString("        const source = createEventSource(url.toString());\n")
		b.WriteString("        if (handlers.onOpen && source.onopen !== undefined) source.onopen = () => handlers.onOpen?.();\n")
		b.WriteString("        source.onerror = (event: unknown) => handlers.onError?.(event);\n")
		for _, h := range se.Handlers {
			eventName := frontendStreamEventName(h)
			payloadName := frontendStreamEventTypeName(baseName, eventName)
			fmt.Fprintf(&b, "        if (handlers.%s) {\n", eventName)
			fmt.Fprintf(&b, "          source.addEventListener('%s', (event: { data: string }) => {\n", eventName)
			fmt.Fprintf(&b, "            const payload = parseEventPayload<Api.%s>(Schemas.%sSchema, event.data, validateResponses);\n", payloadName, payloadName)
			fmt.Fprintf(&b, "            handlers.%s?.(payload);\n", eventName)
			b.WriteString("          });\n")
			b.WriteString("        }\n")
		}
		b.WriteString("        return () => source.close();\n")
		b.WriteString("      },\n")
	}
	b.WriteString("    },\n")
	b.WriteString("    ws: {\n")
	for _, we := range ws {
		baseName := frontendWsBaseName(we.Path)
		methodName := frontendWsMethodName(we.Path)
		if len(extractPathParams(we.Path)) > 0 {
			fmt.Fprintf(&b, "      %s(input: Api.%sRequest): Api.WsConnection<Api.%sMessage> {\n", methodName, baseName, baseName)
			fmt.Fprintf(&b, "        const request = Schemas.%sRequestSchema.parse(input);\n", baseName)
		} else {
			fmt.Fprintf(&b, "      %s(): Api.WsConnection<Api.%sMessage> {\n", methodName, baseName)
		}
		fmt.Fprintf(&b, "        const url = new URL('%s', `${baseUrl}/`);\n", we.Path)
		for _, param := range extractPathParams(we.Path) {
			fmt.Fprintf(&b, "        url.pathname = url.pathname.replace(':%s', encodeURIComponent(serializeValue(request.%s)));\n", param, param)
		}
		b.WriteString("        const socket = createWebSocket(toWebSocketUrl(baseUrl, url.pathname + url.search));\n")
		b.WriteString("        return {\n")
		b.WriteString("          socket,\n")
		b.WriteString("          send(data: unknown) {\n")
		b.WriteString("            socket.send(JSON.stringify(data));\n")
		b.WriteString("          },\n")
		b.WriteString("          close(code?: number, reason?: string) {\n")
		b.WriteString("            socket.close(code, reason);\n")
		b.WriteString("          },\n")
		b.WriteString("          onOpen(handler: () => void) {\n")
		b.WriteString("            socket.addEventListener('open', () => handler());\n")
		b.WriteString("          },\n")
		b.WriteString("          onMessage(handler: (message: Api.")
		b.WriteString(baseName)
		b.WriteString("Message) => void) {\n")
		b.WriteString("            socket.addEventListener('message', (event: { data?: unknown }) => {\n")
		fmt.Fprintf(&b, "              const payload = parseEventPayload<Api.%sMessage>(Schemas.%sMessageSchema, String(event.data ?? ''), validateResponses);\n", baseName, baseName)
		b.WriteString("              handler(payload);\n")
		b.WriteString("            });\n")
		b.WriteString("          },\n")
		b.WriteString("          onClose(handler: (event: unknown) => void) {\n")
		b.WriteString("            socket.addEventListener('close', (event: unknown) => handler(event));\n")
		b.WriteString("          },\n")
		b.WriteString("          onError(handler: (event: unknown) => void) {\n")
		b.WriteString("            socket.addEventListener('error', (event: unknown) => handler(event));\n")
		b.WriteString("          },\n")
		b.WriteString("        };\n")
		b.WriteString("      },\n")
	}
	b.WriteString("    },\n")
	b.WriteString("  };\n")
	b.WriteString("}\n")

	return codegen.OutputFile{Path: "src/types/client.ts", Content: []byte(b.String())}
}

func (g *Generator) genFrontendReactQuery(endpoints []*ast.Endpoint) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("import {\n")
	b.WriteString("  useMutation,\n")
	b.WriteString("  useQuery,\n")
	b.WriteString("  type UseMutationOptions,\n")
	b.WriteString("  type UseMutationResult,\n")
	b.WriteString("  type UseQueryOptions,\n")
	b.WriteString("  type UseQueryResult,\n")
	b.WriteString("} from '@tanstack/react-query';\n")
	b.WriteString("import type * as Api from './api.js';\n")
	b.WriteString("import { createApiClient, type ApiClientOptions, type ApiClientError } from './client.js';\n\n")
	b.WriteString("export interface ReactQueryClientOptions extends ApiClientOptions {\n")
	b.WriteString("  client?: Api.ApiClient;\n")
	b.WriteString("}\n\n")
	b.WriteString("function resolveRestClient(options: ReactQueryClientOptions): Api.RestApiClient {\n")
	b.WriteString("  return options.client?.rest ?? createApiClient(options).rest;\n")
	b.WriteString("}\n\n")

	for _, ep := range endpoints {
		baseName := frontendEndpointBaseName(ep.Method, ep.Path)
		methodName := frontendRestMethodName(ep.Method, ep.Path)
		inputs := frontendEndpointInputGroups(ep)
		if ep.Method == "GET" {
			queryKeyName := methodName + "QueryKey"
			if len(inputs.all) > 0 {
				fmt.Fprintf(&b, "export const %s = (input: Api.%sRequest) => ['%s', input] as const;\n\n", queryKeyName, baseName, methodName)
				fmt.Fprintf(&b, "export function use%sQuery<TData = Api.%sResponse>(\n", baseName, baseName)
				fmt.Fprintf(&b, "  input: Api.%sRequest,\n", baseName)
				b.WriteString("  clientOptions: ReactQueryClientOptions,\n")
				fmt.Fprintf(&b, "  options?: Omit<UseQueryOptions<Api.%sResponse, ApiClientError, TData, ReturnType<typeof %s>>, 'queryKey' | 'queryFn'>,\n", baseName, queryKeyName)
				b.WriteString("): UseQueryResult<TData, ApiClientError> {\n")
				b.WriteString("  const client = resolveRestClient(clientOptions);\n")
				b.WriteString("  return useQuery({\n")
				fmt.Fprintf(&b, "    queryKey: %s(input),\n", queryKeyName)
				fmt.Fprintf(&b, "    queryFn: () => client.%s(input),\n", methodName)
				b.WriteString("    ...options,\n")
				b.WriteString("  });\n")
				b.WriteString("}\n\n")
			} else {
				fmt.Fprintf(&b, "export const %s = () => ['%s'] as const;\n\n", queryKeyName, methodName)
				fmt.Fprintf(&b, "export function use%sQuery<TData = Api.%sResponse>(\n", baseName, baseName)
				b.WriteString("  clientOptions: ReactQueryClientOptions,\n")
				fmt.Fprintf(&b, "  options?: Omit<UseQueryOptions<Api.%sResponse, ApiClientError, TData, ReturnType<typeof %s>>, 'queryKey' | 'queryFn'>,\n", baseName, queryKeyName)
				b.WriteString("): UseQueryResult<TData, ApiClientError> {\n")
				b.WriteString("  const client = resolveRestClient(clientOptions);\n")
				b.WriteString("  return useQuery({\n")
				fmt.Fprintf(&b, "    queryKey: %s(),\n", queryKeyName)
				fmt.Fprintf(&b, "    queryFn: () => client.%s(),\n", methodName)
				b.WriteString("    ...options,\n")
				b.WriteString("  });\n")
				b.WriteString("}\n\n")
			}
			continue
		}

		fmt.Fprintf(&b, "export function use%sMutation(\n", baseName)
		b.WriteString("  clientOptions: ReactQueryClientOptions,\n")
		if len(inputs.all) > 0 {
			fmt.Fprintf(&b, "  options?: UseMutationOptions<Api.%sResponse, ApiClientError, Api.%sRequest>,\n", baseName, baseName)
			fmt.Fprintf(&b, "): UseMutationResult<Api.%sResponse, ApiClientError, Api.%sRequest> {\n", baseName, baseName)
		} else {
			fmt.Fprintf(&b, "  options?: UseMutationOptions<Api.%sResponse, ApiClientError, void>,\n", baseName)
			fmt.Fprintf(&b, "): UseMutationResult<Api.%sResponse, ApiClientError, void> {\n", baseName)
		}
		b.WriteString("  const client = resolveRestClient(clientOptions);\n")
		b.WriteString("  return useMutation({\n")
		if len(inputs.all) > 0 {
			fmt.Fprintf(&b, "    mutationFn: (input) => client.%s(input),\n", methodName)
		} else {
			fmt.Fprintf(&b, "    mutationFn: () => client.%s(),\n", methodName)
		}
		b.WriteString("    ...options,\n")
		b.WriteString("  });\n")
		b.WriteString("}\n\n")
	}

	return codegen.OutputFile{Path: "src/types/react-query.ts", Content: []byte(b.String())}
}

func (g *Generator) genFrontendPackage(baseDir string, bp *ast.Blueprint, apiFile, schemasFile, clientFile codegen.OutputFile, i18nFile, reactQueryFile *codegen.OutputFile) []codegen.OutputFile {
	hasI18n := i18nFile != nil
	files := []codegen.OutputFile{
		g.frontendPackageFile(baseDir, apiFile),
		g.frontendPackageFile(baseDir, schemasFile),
		g.frontendPackageFile(baseDir, clientFile),
		g.genFrontendPackageIndex(baseDir, hasI18n, reactQueryFile != nil),
		g.genFrontendPackageJSON(baseDir, bp, hasI18n, reactQueryFile != nil),
		g.genFrontendPackageREADME(baseDir, bp, reactQueryFile != nil),
		g.genFrontendPackageTSConfig(baseDir),
		g.genFrontendPackageGitignore(baseDir),
	}
	if i18nFile != nil {
		files = append(files, g.frontendPackageFile(baseDir, *i18nFile))
	}
	if reactQueryFile != nil {
		files = append(files, g.frontendPackageFile(baseDir, *reactQueryFile))
	}
	return files
}

func (g *Generator) frontendPackageFile(baseDir string, file codegen.OutputFile) codegen.OutputFile {
	return codegen.OutputFile{
		Path:    filepath.Join(baseDir, "src", filepath.Base(file.Path)),
		Content: file.Content,
	}
}

func (g *Generator) genFrontendPackageIndex(baseDir string, hasI18n, hasReactQuery bool) codegen.OutputFile {
	var b strings.Builder
	b.WriteString(fileHeader(g.sourceFile))
	b.WriteString("export * from './api.js';\n")
	b.WriteString("export * from './schemas.js';\n")
	b.WriteString("export * from './client.js';\n")
	if hasI18n {
		b.WriteString("export * from './i18n.js';\n")
	}
	if hasReactQuery {
		b.WriteString("export * from './react-query.js';\n")
	}
	return codegen.OutputFile{Path: filepath.Join(baseDir, "src", "index.ts"), Content: []byte(b.String())}
}

func (g *Generator) genFrontendI18n(locales []*ast.Locale, translations []*ast.Translation) codegen.OutputFile {
	lib := g.genI18n(locales, translations)
	return codegen.OutputFile{Path: "src/types/i18n.ts", Content: lib.Content}
}

func (g *Generator) genFrontendPackageJSON(baseDir string, bp *ast.Blueprint, hasI18n, hasReactQuery bool) codegen.OutputFile {
	deps := map[string]string{
		"zod": "^3.23.0",
	}
	peerDeps := map[string]string{}
	devDeps := map[string]string{
		"typescript": "^5.7.0",
	}
	if hasReactQuery {
		peerDeps["@tanstack/react-query"] = "^5.59.20"
		peerDeps["react"] = "^18.3.1"
		devDeps["@tanstack/react-query"] = "^5.59.20"
		devDeps["@types/react"] = "^18.3.12"
		devDeps["react"] = "^18.3.1"
	}

	name := toKebabCase(bp.Name) + "-frontend"
	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"name\": %q,\n", name)
	b.WriteString("  \"version\": \"0.1.0\",\n")
	fmt.Fprintf(&b, "  \"description\": %q,\n", fmt.Sprintf("Generated frontend SDK for %s.", bp.Name))
	b.WriteString("  \"license\": \"MIT\",\n")
	b.WriteString("  \"type\": \"module\",\n")
	b.WriteString("  \"sideEffects\": false,\n")
	b.WriteString("  \"keywords\": [\n")
	b.WriteString("    \"blueprint\",\n")
	b.WriteString("    \"sdk\",\n")
	b.WriteString("    \"typescript\",\n")
	b.WriteString("    \"zod\",\n")
	b.WriteString("    \"api-client\"\n")
	b.WriteString("  ],\n")
	b.WriteString("  \"files\": [\n")
	b.WriteString("    \"dist\"\n")
	b.WriteString("  ],\n")
	b.WriteString("  \"main\": \"./dist/index.js\",\n")
	b.WriteString("  \"module\": \"./dist/index.js\",\n")
	b.WriteString("  \"types\": \"./dist/index.d.ts\",\n")
	b.WriteString("  \"publishConfig\": {\n")
	b.WriteString("    \"access\": \"public\"\n")
	b.WriteString("  },\n")
	b.WriteString("  \"exports\": {\n")
	b.WriteString("    \".\": {\n")
	b.WriteString("      \"types\": \"./dist/index.d.ts\",\n")
	b.WriteString("      \"import\": \"./dist/index.js\"\n")
	b.WriteString("    },\n")
	b.WriteString("    \"./api\": {\n")
	b.WriteString("      \"types\": \"./dist/api.d.ts\",\n")
	b.WriteString("      \"import\": \"./dist/api.js\"\n")
	b.WriteString("    },\n")
	b.WriteString("    \"./schemas\": {\n")
	b.WriteString("      \"types\": \"./dist/schemas.d.ts\",\n")
	b.WriteString("      \"import\": \"./dist/schemas.js\"\n")
	b.WriteString("    },\n")
	b.WriteString("    \"./client\": {\n")
	b.WriteString("      \"types\": \"./dist/client.d.ts\",\n")
	b.WriteString("      \"import\": \"./dist/client.js\"\n")
	b.WriteString("    }")
	if hasI18n {
		b.WriteString(",\n")
		b.WriteString("    \"./i18n\": {\n")
		b.WriteString("      \"types\": \"./dist/i18n.d.ts\",\n")
		b.WriteString("      \"import\": \"./dist/i18n.js\"\n")
		b.WriteString("    }")
	}
	if hasReactQuery {
		b.WriteString(",\n")
		b.WriteString("    \"./react-query\": {\n")
		b.WriteString("      \"types\": \"./dist/react-query.d.ts\",\n")
		b.WriteString("      \"import\": \"./dist/react-query.js\"\n")
		b.WriteString("    }\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("  },\n")
	b.WriteString("  \"scripts\": {\n")
	b.WriteString("    \"build\": \"tsc -p tsconfig.json\"\n")
	b.WriteString("  },\n")
	b.WriteString("  \"dependencies\": {\n")
	depKeys := sortedKeys(deps)
	for i, k := range depKeys {
		comma := ","
		if i == len(depKeys)-1 {
			comma = ""
		}
		fmt.Fprintf(&b, "    %q: %q%s\n", k, deps[k], comma)
	}
	b.WriteString("  }")
	if len(peerDeps) > 0 {
		b.WriteString(",\n")
		b.WriteString("  \"peerDependencies\": {\n")
		peerKeys := sortedKeys(peerDeps)
		for i, k := range peerKeys {
			comma := ","
			if i == len(peerKeys)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    %q: %q%s\n", k, peerDeps[k], comma)
		}
		b.WriteString("  },\n")
		b.WriteString("  \"devDependencies\": {\n")
		devKeys := sortedKeys(devDeps)
		for i, k := range devKeys {
			comma := ","
			if i == len(devKeys)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    %q: %q%s\n", k, devDeps[k], comma)
		}
		b.WriteString("  }\n")
	} else {
		b.WriteString(",\n")
		b.WriteString("  \"devDependencies\": {\n")
		devKeys := sortedKeys(devDeps)
		for i, k := range devKeys {
			comma := ","
			if i == len(devKeys)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    %q: %q%s\n", k, devDeps[k], comma)
		}
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return codegen.OutputFile{Path: filepath.Join(baseDir, "package.json"), Content: []byte(b.String())}
}

func (g *Generator) genFrontendPackageTSConfig(baseDir string) codegen.OutputFile {
	content := `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "nodenext",
    "strict": true,
    "declaration": true,
    "declarationMap": true,
    "sourceMap": true,
    "outDir": "dist",
    "rootDir": "src",
    "skipLibCheck": true,
    "esModuleInterop": true,
    "forceConsistentCasingInFileNames": true
  },
  "include": ["src/**/*.ts"]
}
`
	return codegen.OutputFile{Path: filepath.Join(baseDir, "tsconfig.json"), Content: []byte(content)}
}

func (g *Generator) genFrontendPackageGitignore(baseDir string) codegen.OutputFile {
	content := "node_modules/\ndist/\n*.log\n"
	return codegen.OutputFile{Path: filepath.Join(baseDir, ".gitignore"), Content: []byte(content)}
}

func (g *Generator) genFrontendPackageREADME(baseDir string, bp *ast.Blueprint, hasReactQuery bool) codegen.OutputFile {
	packageName := toKebabCase(bp.Name) + "-frontend"
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(packageName)
	b.WriteString("\n\n")
	b.WriteString("Generated frontend SDK for `")
	b.WriteString(bp.Name)
	b.WriteString("`.\n\n")
	b.WriteString("## Install\n\n")
	b.WriteString("```bash\n")
	b.WriteString("bun add " + packageName + "\n")
	if hasReactQuery {
		b.WriteString("bun add @tanstack/react-query react\n")
	}
	b.WriteString("```\n\n")
	b.WriteString("## Usage\n\n")
	b.WriteString("```ts\n")
	b.WriteString("import { createApiClient } from '" + packageName + "';\n\n")
	b.WriteString("const client = createApiClient({\n")
	b.WriteString("  baseUrl: 'https://api.example.com',\n")
	b.WriteString("});\n")
	b.WriteString("```\n\n")
	if hasReactQuery {
		b.WriteString("### React Query\n\n")
		b.WriteString("```ts\n")
		b.WriteString("import { useGetTodosQuery } from '" + packageName + "/react-query';\n\n")
		b.WriteString("const query = useGetTodosQuery({ page: 1 }, {\n")
		b.WriteString("  baseUrl: 'https://api.example.com',\n")
		b.WriteString("});\n")
		b.WriteString("```\n\n")
	}
	b.WriteString("## Publishing\n\n")
	b.WriteString("This package is ready to build and publish, but you should review `package.json` metadata such as the package name, license, and repository fields before publishing your final SDK.\n")
	return codegen.OutputFile{Path: filepath.Join(baseDir, "README.md"), Content: []byte(b.String())}
}

func frontendEndpointBaseName(method, path string) string {
	return toPascalCase(strings.ToLower(method)) + frontendPathTypeName(path, map[string]bool{"api": true})
}

func frontendStreamBaseName(path string) string {
	return "Stream" + frontendPathTypeName(path, map[string]bool{"api": true})
}

func frontendWsBaseName(path string) string {
	return "Ws" + frontendPathTypeName(path, map[string]bool{"ws": true})
}

func frontendRestMethodName(method, path string) string {
	return lowerFirst(frontendEndpointBaseName(method, path))
}

func frontendStreamMethodName(path string) string {
	return "subscribe" + frontendPathTypeName(path, map[string]bool{"api": true})
}

func frontendWsMethodName(path string) string {
	return "connect" + frontendPathTypeName(path, map[string]bool{"ws": true})
}

func frontendStreamEventName(h *ast.StreamHandler) string {
	if h == nil {
		return "message"
	}
	if h.Timeout != "" {
		return "ping"
	}
	if h.EventName == "" {
		return "message"
	}
	return toCamelCase(h.EventName)
}

func frontendStreamEventTypeName(baseName, eventName string) string {
	return baseName + toPascalCase(eventName) + "Event"
}

func frontendWsPayloadTypeName(baseName, suffix string) string {
	return baseName + suffix + "Message"
}

func frontendSchemaName(name string) string {
	return toPascalCase(name) + "Schema"
}

func frontendPathTypeName(path string, ignored map[string]bool) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var segments []string
	var params []string
	for _, part := range parts {
		if part == "" || ignored[part] {
			continue
		}
		if strings.HasPrefix(part, ":") {
			params = append(params, toPascalCase(part[1:]))
			continue
		}
		segments = append(segments, toPascalCase(strings.ReplaceAll(part, "-", "_")))
	}
	name := strings.Join(segments, "")
	if name == "" {
		name = "Root"
	}
	if len(params) > 0 {
		name += "By" + strings.Join(params, "And")
	}
	return name
}

func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func frontendEndpointInputGroups(ep *ast.Endpoint) frontendEndpointInputs {
	var out frontendEndpointInputs
	for _, stmt := range ep.Stmts {
		inp, ok := stmt.(*ast.InputStmt)
		if !ok {
			continue
		}
		out.all = append(out.all, inp)
		if isPathParam(inp.Name, ep.Path) {
			out.path = append(out.path, inp)
		} else if ep.Method == "GET" || ep.Method == "DELETE" {
			out.query = append(out.query, inp)
		} else {
			out.body = append(out.body, inp)
		}
	}
	return out
}

func frontendInputOptional(inp *ast.InputStmt) bool {
	return frontendConstraintsOptional(inp.Constraints) || frontendConstraintsHaveDefault(inp.Constraints)
}

func frontendConstraintsOptional(constraints []*ast.Constraint_) bool {
	for _, c := range constraints {
		if c.Kind == "optional" {
			return true
		}
	}
	return false
}

func frontendConstraintsHaveDefault(constraints []*ast.Constraint_) bool {
	for _, c := range constraints {
		if c.Kind == "default" {
			return true
		}
	}
	return false
}

func frontendMaybeAddTSUnion(ts, suffix string) string {
	if strings.Contains(ts, suffix) {
		return ts
	}
	return ts + " | " + suffix
}

func frontendTypeToZod(t ast.TypeExpr) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		switch v.Name {
		case "string", "text":
			return "z.string()"
		case "int":
			return "z.number().int()"
		case "float", "money":
			return "z.number()"
		case "bool":
			return "z.boolean()"
		case "uuid":
			return "z.string().uuid()"
		case "timestamp":
			return "z.coerce.date()"
		case "json", "file":
			return "z.unknown()"
		default:
			return "z.unknown()"
		}
	case *ast.TypedJSONType:
		return frontendTypeToZod(v.Inner)
	case *ast.TranslationKeyType:
		if len(v.Keys) > 0 {
			parts := make([]string, len(v.Keys))
			for i, key := range v.Keys {
				parts[i] = fmt.Sprintf("%q", key)
			}
			return fmt.Sprintf("z.enum([%s])", strings.Join(parts, ", "))
		}
		return "z.string()"
	case *ast.NamedType:
		return fmt.Sprintf("z.lazy(() => %s)", frontendSchemaName(v.Name))
	case *ast.ListType:
		return fmt.Sprintf("z.array(%s)", frontendTypeToZod(v.Element))
	case *ast.MapType:
		return fmt.Sprintf("z.record(%s, %s)", frontendTypeToZod(v.Key), frontendTypeToZod(v.Value))
	case *ast.EnumInline:
		variants := make([]string, len(v.Variants))
		for i, variant := range v.Variants {
			variants[i] = fmt.Sprintf(`"%s"`, variant)
		}
		return fmt.Sprintf("z.enum([%s])", strings.Join(variants, ", "))
	case *ast.MimeTypeExpr:
		return "z.unknown()"
	default:
		return "z.unknown()"
	}
}

func (g *Generator) frontendTypeInfoFromTypeExpr(t ast.TypeExpr) frontendTypeInfo {
	return frontendTypeInfo{ts: typeToTS(t), zod: frontendTypeToZod(t)}
}

func (g *Generator) frontendTypeInfoFromInput(inp *ast.InputStmt) frontendTypeInfo {
	info := g.frontendTypeInfoFromTypeExpr(inp.Type)
	if frontendConstraintsOptional(inp.Constraints) && !frontendConstraintsHaveDefault(inp.Constraints) {
		info.ts = frontendMaybeAddTSUnion(info.ts, "undefined")
		info.zod += ".optional()"
	}
	return info
}

func (g *Generator) frontendTypeInfoFromModelField(field *ast.Field) frontendTypeInfo {
	info := g.frontendTypeInfoFromTypeExpr(field.Type)
	if frontendConstraintsOptional(field.Constraints) {
		info.ts = frontendMaybeAddTSUnion(info.ts, "null")
		info.zod += ".nullable()"
	}
	return info
}

func (g *Generator) frontendTypeInfoFromTypeField(field *ast.Field) frontendTypeInfo {
	info := g.frontendTypeInfoFromTypeExpr(field.Type)
	if frontendConstraintsOptional(field.Constraints) {
		info.ts = frontendMaybeAddTSUnion(info.ts, "undefined")
		info.zod += ".optional()"
	}
	return info
}

func (g *Generator) frontendEndpointResponseInfo(ep *ast.Endpoint) frontendTypeInfo {
	output := frontendFindLastOutput(ep.Stmts)
	if output == nil || output.Status == "204" || output.Value == nil {
		return frontendTypeInfo{ts: "void", zod: "z.void()"}
	}
	bindings := g.collectFrontendBindings(ep.Stmts)
	return g.frontendTypeInfoFromExpr(output.Value, bindings)
}

func (g *Generator) frontendStreamHandlerInfo(h *ast.StreamHandler) frontendTypeInfo {
	output := frontendFindLastOutput(h.Body)
	bindings := g.collectFrontendBindings(h.Body)
	if output == nil || output.Value == nil {
		return g.frontendTypeInfoFromExpr(&ast.BlockExpr{}, bindings)
	}
	return g.frontendTypeInfoFromExpr(output.Value, bindings)
}

func (g *Generator) frontendWsLifecycleInfo(stmts []ast.ArrowStmt) frontendTypeInfo {
	outputs := frontendCollectOutputs(stmts)
	bindings := g.collectFrontendBindings(stmts)
	if len(outputs) == 0 {
		return frontendTypeInfo{ts: "unknown", zod: "z.unknown()"}
	}
	infos := make([]frontendTypeInfo, 0, len(outputs))
	for _, out := range outputs {
		if out.Value == nil {
			continue
		}
		infos = append(infos, g.frontendTypeInfoFromExpr(out.Value, bindings))
	}
	return mergeFrontendTypeInfos(infos)
}

func mergeFrontendTypeInfos(infos []frontendTypeInfo) frontendTypeInfo {
	if len(infos) == 0 {
		return frontendTypeInfo{ts: "unknown", zod: "z.unknown()"}
	}
	var tsParts []string
	var zodParts []string
	seenTS := make(map[string]bool)
	seenZod := make(map[string]bool)
	for _, info := range infos {
		if info.ts != "" && !seenTS[info.ts] {
			seenTS[info.ts] = true
			tsParts = append(tsParts, info.ts)
		}
		if info.zod != "" && !seenZod[info.zod] {
			seenZod[info.zod] = true
			zodParts = append(zodParts, info.zod)
		}
	}
	merged := frontendTypeInfo{}
	if len(tsParts) == 1 {
		merged.ts = tsParts[0]
	} else {
		merged.ts = strings.Join(tsParts, " | ")
	}
	if len(zodParts) == 1 {
		merged.zod = zodParts[0]
	} else {
		merged.zod = fmt.Sprintf("z.union([%s])", strings.Join(zodParts, ", "))
	}
	return merged
}

func frontendFindLastOutput(stmts []ast.ArrowStmt) *ast.OutputStmt {
	for i := len(stmts) - 1; i >= 0; i-- {
		switch stmt := stmts[i].(type) {
		case *ast.OutputStmt:
			return stmt
		case *ast.TryRecover:
			if out := frontendFindLastOutput(stmt.Recover); out != nil {
				return out
			}
			if out := frontendFindLastOutput(stmt.Try); out != nil {
				return out
			}
		case *ast.WhenStmt:
			if out := frontendFindLastOutput(stmt.Body); out != nil {
				return out
			}
		}
	}
	return nil
}

func frontendCollectOutputs(stmts []ast.ArrowStmt) []*ast.OutputStmt {
	var outputs []*ast.OutputStmt
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.OutputStmt:
			outputs = append(outputs, s)
		case *ast.TryRecover:
			outputs = append(outputs, frontendCollectOutputs(s.Try)...)
			outputs = append(outputs, frontendCollectOutputs(s.Recover)...)
		case *ast.WhenStmt:
			outputs = append(outputs, frontendCollectOutputs(s.Body)...)
		}
	}
	return outputs
}

func (g *Generator) collectFrontendBindings(stmts []ast.ArrowStmt) map[string]frontendBinding {
	bindings := make(map[string]frontendBinding)
	g.collectFrontendBindingsInto(stmts, bindings)
	return bindings
}

func (g *Generator) collectFrontendBindingsInto(stmts []ast.ArrowStmt, bindings map[string]frontendBinding) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.InputStmt:
			bindings[s.Name] = frontendBinding{input: s, typeExpr: s.Type}
		case *ast.StepStmt:
			if s.Binding == "" {
				continue
			}
			binding := frontendBinding{expr: s.Expr}
			if fn, ok := s.Expr.(*ast.FnCall); ok {
				switch fn.Name {
				case "count":
					binding.typeExpr = &ast.PrimitiveType{Name: "int"}
				case "query":
					if len(fn.Args) > 0 {
						if ident, ok := fn.Args[0].(*ast.Ident); ok {
							if queryHasFirst(fn) {
								binding.modelName = ident.Name
							} else {
								binding.collectionModel = ident.Name
								binding.paginated = queryIsPaginated(fn)
							}
							binding.relationships = frontendQueryRelationships(fn)
							binding.expr = nil
						}
					}
				case "fetch", "save", "seed", "update":
					if len(fn.Args) > 0 {
						if ident, ok := fn.Args[0].(*ast.Ident); ok {
							binding.modelName = ident.Name
							binding.expr = nil
						}
					}
				case "map":
					if len(fn.Args) >= 2 {
						if bodyFn, ok := fn.Args[1].(*ast.FnCall); ok && isDataOp(bodyFn.Name) && len(bodyFn.Args) > 0 {
							if ident, ok := bodyFn.Args[0].(*ast.Ident); ok {
								binding.collectionModel = ident.Name
								binding.expr = nil
							}
						}
					}
				}
			}
			bindings[s.Binding] = binding
		case *ast.TryRecover:
			g.collectFrontendBindingsInto(s.Try, bindings)
			g.collectFrontendBindingsInto(s.Recover, bindings)
		}
	}
}

func (g *Generator) frontendTypeInfoFromExpr(expr ast.Expr, bindings map[string]frontendBinding) frontendTypeInfo {
	if expr == nil {
		return frontendTypeInfo{ts: "void", zod: "z.void()"}
	}
	switch v := expr.(type) {
	case *ast.StringLit, *ast.PathExpr:
		return frontendTypeInfo{ts: "string", zod: "z.string()"}
	case *ast.IntLit, *ast.FloatLit, *ast.SizeLit:
		return frontendTypeInfo{ts: "number", zod: "z.number()"}
	case *ast.DurationLit:
		return frontendTypeInfo{ts: "number", zod: "z.number()"}
	case *ast.RateLit:
		return frontendTypeInfo{ts: "string", zod: "z.string()"}
	case *ast.BoolLit:
		return frontendTypeInfo{ts: "boolean", zod: "z.boolean()"}
	case *ast.NullLit:
		return frontendTypeInfo{ts: "null", zod: "z.null()"}
	case *ast.NowLit:
		return frontendTypeInfo{ts: "Date", zod: "z.coerce.date()"}
	case *ast.ParenExpr:
		return g.frontendTypeInfoFromExpr(v.Expr, bindings)
	case *ast.ListExpr:
		return g.frontendTypeInfoFromListExpr(v, bindings)
	case *ast.BlockExpr:
		return frontendTypeInfo{
			ts:  g.frontendObjectType(v.Entries, bindings, ""),
			zod: g.frontendObjectSchema(v.Entries, bindings, ""),
		}
	case *ast.Ident:
		if binding, ok := bindings[v.Name]; ok {
			return g.frontendTypeInfoFromBinding(binding, bindings)
		}
		if g.declaredModels[v.Name] || g.declaredEnums[v.Name] || g.lookupAlias(v.Name) != nil || g.lookupTypeDecl(v.Name) != nil {
			return frontendTypeInfo{ts: toPascalCase(v.Name), zod: fmt.Sprintf("z.lazy(() => %s)", frontendSchemaName(v.Name))}
		}
		return frontendTypeInfo{ts: "unknown", zod: "z.unknown()"}
	case *ast.FieldAccess:
		return g.frontendTypeInfoFromFieldAccess(v, bindings)
	case *ast.IndexAccess:
		return g.frontendTypeInfoFromIndexAccess(v)
	case *ast.UnaryExpr:
		if v.Op == "not" {
			return frontendTypeInfo{ts: "boolean", zod: "z.boolean()"}
		}
		return frontendTypeInfo{ts: "number", zod: "z.number()"}
	case *ast.BinaryExpr:
		return g.frontendTypeInfoFromBinaryExpr(v, bindings)
	case *ast.FnCall:
		return g.frontendTypeInfoFromFnCall(v, bindings)
	default:
		return frontendTypeInfo{ts: "unknown", zod: "z.unknown()"}
	}
}

func (g *Generator) frontendTypeInfoFromBinding(binding frontendBinding, bindings map[string]frontendBinding) frontendTypeInfo {
	if binding.input != nil {
		return g.frontendTypeInfoFromInput(binding.input)
	}
	if binding.modelName != "" {
		return g.frontendModelInfo(binding.modelName, binding.relationships)
	}
	if binding.collectionModel != "" {
		item := g.frontendModelInfo(binding.collectionModel, binding.relationships)
		if binding.paginated {
			return frontendTypeInfo{ts: fmt.Sprintf("PaginatedResult<%s>", item.ts), zod: fmt.Sprintf("paginatedResultSchema(%s)", item.zod)}
		}
		if len(binding.relationships) == 0 {
			return frontendTypeInfo{ts: item.ts + "[]", zod: fmt.Sprintf("z.array(%s)", item.zod)}
		}
		return frontendTypeInfo{ts: "Array<" + item.ts + ">", zod: fmt.Sprintf("z.array(%s)", item.zod)}
	}
	if binding.typeExpr != nil {
		return g.frontendTypeInfoFromTypeExpr(binding.typeExpr)
	}
	if binding.expr != nil {
		return g.frontendTypeInfoFromExpr(binding.expr, bindings)
	}
	return frontendTypeInfo{ts: "unknown", zod: "z.unknown()"}
}

func (g *Generator) frontendTypeInfoFromFieldAccess(access *ast.FieldAccess, bindings map[string]frontendBinding) frontendTypeInfo {
	switch base := access.Base.(type) {
	case *ast.Ident:
		if binding, ok := bindings[base.Name]; ok {
			if info, found := g.frontendFieldInfoFromBinding(binding, access.Field, bindings); found {
				return info
			}
		}
		if info, found := g.frontendFieldInfoFromNamed(base.Name, access.Field); found {
			return info
		}
	case *ast.IndexAccess:
		if ident, ok := base.Base.(*ast.Ident); ok && g.structEnums[ident.Name] {
			if info, found := g.frontendStructEnumFieldInfo(ident.Name, access.Field); found {
				return info
			}
		}
	case *ast.FieldAccess:
		if innerInfo, found := g.frontendNestedFieldInfo(base, access.Field, bindings); found {
			return innerInfo
		}
	}
	return frontendTypeInfo{ts: "unknown", zod: "z.unknown()"}
}

func (g *Generator) frontendNestedFieldInfo(base *ast.FieldAccess, field string, bindings map[string]frontendBinding) (frontendTypeInfo, bool) {
	if ident, ok := base.Base.(*ast.Ident); ok {
		binding, found := bindings[ident.Name]
		if !found || binding.expr == nil {
			return frontendTypeInfo{}, false
		}
		block, ok := binding.expr.(*ast.BlockExpr)
		if !ok {
			return frontendTypeInfo{}, false
		}
		for _, kv := range block.Entries {
			if kv.Key != base.Field {
				continue
			}
			return g.frontendTypeInfoFromExpr(&ast.FieldAccess{Base: kv.Value, Field: field}, bindings), true
		}
	}
	return frontendTypeInfo{}, false
}

func (g *Generator) frontendFieldInfoFromBinding(binding frontendBinding, field string, bindings map[string]frontendBinding) (frontendTypeInfo, bool) {
	modelName := binding.modelName
	if modelName == "" {
		modelName = binding.collectionModel
	}
	for _, relation := range binding.relationships {
		if relation != field {
			continue
		}
		if target, ok := resolve.ModelFieldRef(g.models, modelName, relation); ok {
			info := g.frontendModelInfo(target, nil)
			info.ts += " | null"
			info.zod += ".nullable()"
			return info, true
		}
	}
	if binding.modelName != "" {
		return g.frontendFieldInfoFromNamed(binding.modelName, field)
	}
	if binding.collectionModel != "" && binding.paginated {
		switch field {
		case "items":
			itemName := toPascalCase(binding.collectionModel)
			itemSchema := fmt.Sprintf("z.lazy(() => %s)", frontendSchemaName(binding.collectionModel))
			return frontendTypeInfo{ts: itemName + "[]", zod: fmt.Sprintf("z.array(%s)", itemSchema)}, true
		case "total":
			return frontendTypeInfo{ts: "number", zod: "z.number().int()"}, true
		}
	}
	if binding.input != nil {
		return frontendTypeInfo{}, false
	}
	if binding.typeExpr != nil {
		if named, ok := binding.typeExpr.(*ast.NamedType); ok {
			return g.frontendFieldInfoFromNamed(named.Name, field)
		}
	}
	if binding.expr != nil {
		if block, ok := binding.expr.(*ast.BlockExpr); ok {
			for _, kv := range block.Entries {
				if kv.Key == field {
					return g.frontendTypeInfoFromExpr(kv.Value, bindings), true
				}
			}
		}
	}
	return frontendTypeInfo{}, false
}

func (g *Generator) frontendFieldInfoFromNamed(name, field string) (frontendTypeInfo, bool) {
	if model := g.lookupModel(name); model != nil {
		for _, f := range model.Fields {
			if f.Name == field {
				return g.frontendTypeInfoFromModelField(f), true
			}
		}
		for _, f := range model.ComputedFields {
			if f.Name == field {
				return frontendTypeInfo{ts: typeToTS(f.Type), zod: frontendTypeToZod(f.Type)}, true
			}
		}
	}
	if decl := g.lookupTypeDecl(name); decl != nil {
		for _, f := range decl.Fields {
			if f.Name == field {
				return g.frontendTypeInfoFromTypeField(f), true
			}
		}
	}
	return frontendTypeInfo{}, false
}

func (g *Generator) frontendStructEnumFieldInfo(enumName, field string) (frontendTypeInfo, bool) {
	var infos []frontendTypeInfo
	for _, block := range g.file.Blocks {
		enumDecl, ok := block.(*ast.Enum)
		if !ok || enumDecl.Name != enumName {
			continue
		}
		for _, variant := range enumDecl.Variants {
			if variant.Body == nil {
				continue
			}
			for _, kv := range variant.Body.Entries {
				if kv.Key == field {
					infos = append(infos, g.frontendTypeInfoFromExpr(kv.Value, nil))
				}
			}
		}
	}
	if len(infos) == 0 {
		return frontendTypeInfo{}, false
	}
	return mergeFrontendTypeInfos(infos), true
}

func (g *Generator) frontendTypeInfoFromIndexAccess(access *ast.IndexAccess) frontendTypeInfo {
	if ident, ok := access.Base.(*ast.Ident); ok && g.structEnums[ident.Name] {
		return frontendTypeInfo{ts: fmt.Sprintf("%sConfig", ident.Name), zod: "z.record(z.string(), z.unknown())"}
	}
	return frontendTypeInfo{ts: "unknown", zod: "z.unknown()"}
}

func (g *Generator) frontendTypeInfoFromBinaryExpr(expr *ast.BinaryExpr, bindings map[string]frontendBinding) frontendTypeInfo {
	switch expr.Op {
	case "and", "or", "==", "!=", "<", ">", "<=", ">=", "in":
		return frontendTypeInfo{ts: "boolean", zod: "z.boolean()"}
	case "+":
		left := g.frontendTypeInfoFromExpr(expr.Left, bindings)
		right := g.frontendTypeInfoFromExpr(expr.Right, bindings)
		if left.ts == "string" || right.ts == "string" {
			return frontendTypeInfo{ts: "string", zod: "z.string()"}
		}
		return frontendTypeInfo{ts: "number", zod: "z.number()"}
	default:
		return frontendTypeInfo{ts: "number", zod: "z.number()"}
	}
}

func (g *Generator) frontendTypeInfoFromFnCall(call *ast.FnCall, bindings map[string]frontendBinding) frontendTypeInfo {
	if isDataOp(call.Name) {
		switch call.Name {
		case "count":
			return frontendTypeInfo{ts: "number", zod: "z.number().int()"}
		case "query":
			if len(call.Args) > 0 {
				if ident, ok := call.Args[0].(*ast.Ident); ok {
					item := g.frontendModelInfo(ident.Name, frontendQueryRelationships(call))
					if queryHasFirst(call) {
						return item
					}
					if queryIsPaginated(call) {
						return frontendTypeInfo{ts: fmt.Sprintf("PaginatedResult<%s>", item.ts), zod: fmt.Sprintf("paginatedResultSchema(%s)", item.zod)}
					}
					return frontendTypeInfo{ts: "Array<" + item.ts + ">", zod: fmt.Sprintf("z.array(%s)", item.zod)}
				}
			}
		case "fetch", "save", "seed", "update":
			if len(call.Args) > 0 {
				if ident, ok := call.Args[0].(*ast.Ident); ok {
					return frontendTypeInfo{ts: toPascalCase(ident.Name), zod: fmt.Sprintf("z.lazy(() => %s)", frontendSchemaName(ident.Name))}
				}
			}
		}
	}
	switch call.Name {
	case "clock":
		return frontendTypeInfo{ts: "number", zod: "z.number()"}
	case "hash":
		return frontendTypeInfo{ts: "string", zod: "z.string()"}
	case "map":
		if len(call.Args) >= 2 {
			bodyInfo := g.frontendTypeInfoFromExpr(call.Args[1], bindings)
			return frontendTypeInfo{ts: fmt.Sprintf("(%s)[]", bodyInfo.ts), zod: fmt.Sprintf("z.array(%s)", bodyInfo.zod)}
		}
	}
	return frontendTypeInfo{ts: "unknown", zod: "z.unknown()"}
}

func queryHasFirst(call *ast.FnCall) bool {
	for _, arg := range call.Args[1:] {
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == "first" {
			return true
		}
	}
	return false
}

func frontendQueryRelationships(call *ast.FnCall) []string {
	var relationships []string
	for _, arg := range call.Args[1:] {
		marker, ok := arg.(*ast.FnCall)
		if !ok || marker.Name != "with" {
			continue
		}
		for _, value := range marker.Args {
			if relation, ok := value.(*ast.Ident); ok {
				relationships = append(relationships, relation.Name)
			}
		}
	}
	return relationships
}

func (g *Generator) frontendModelInfo(modelName string, relationships []string) frontendTypeInfo {
	base := frontendTypeInfo{
		ts:  toPascalCase(modelName),
		zod: fmt.Sprintf("z.lazy(() => %s)", frontendSchemaName(modelName)),
	}
	if len(relationships) == 0 {
		return base
	}
	var typeFields []string
	var schemaFields []string
	for _, relation := range relationships {
		target, ok := resolve.ModelFieldRef(g.models, modelName, relation)
		if !ok {
			continue
		}
		typeFields = append(typeFields, fmt.Sprintf("%s: %s | null", toCamelCase(relation), toPascalCase(target)))
		schemaFields = append(schemaFields, fmt.Sprintf("%s: z.lazy(() => %s).nullable()", toCamelCase(relation), frontendSchemaName(target)))
	}
	if len(typeFields) == 0 {
		return base
	}
	base.ts += " & { " + strings.Join(typeFields, "; ") + " }"
	base.zod = fmt.Sprintf("%s.extend({ %s })", frontendSchemaName(modelName), strings.Join(schemaFields, ", "))
	return base
}

func (g *Generator) frontendTypeInfoFromListExpr(list *ast.ListExpr, bindings map[string]frontendBinding) frontendTypeInfo {
	if len(list.Elements) == 0 {
		return frontendTypeInfo{ts: "unknown[]", zod: "z.array(z.unknown())"}
	}
	var infos []frontendTypeInfo
	for _, el := range list.Elements {
		infos = append(infos, g.frontendTypeInfoFromExpr(el, bindings))
	}
	merged := mergeFrontendTypeInfos(infos)
	if strings.Contains(merged.ts, " | ") {
		return frontendTypeInfo{ts: fmt.Sprintf("(%s)[]", merged.ts), zod: fmt.Sprintf("z.array(%s)", merged.zod)}
	}
	return frontendTypeInfo{ts: merged.ts + "[]", zod: fmt.Sprintf("z.array(%s)", merged.zod)}
}

func (g *Generator) frontendObjectType(entries []ast.KVPair, bindings map[string]frontendBinding, indent string) string {
	if len(entries) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, kv := range entries {
		info := g.frontendTypeInfoFromExpr(kv.Value, bindings)
		b.WriteString(indent + "  " + kv.Key + ": ")
		b.WriteString(indentMultiline(info.ts, indent+"  "))
		b.WriteString(";\n")
	}
	b.WriteString(indent + "}")
	return b.String()
}

func (g *Generator) frontendObjectSchema(entries []ast.KVPair, bindings map[string]frontendBinding, indent string) string {
	if len(entries) == 0 {
		return "z.object({})"
	}
	var b strings.Builder
	b.WriteString("z.object({\n")
	for _, kv := range entries {
		info := g.frontendTypeInfoFromExpr(kv.Value, bindings)
		b.WriteString(indent + "  " + kv.Key + ": ")
		b.WriteString(indentMultiline(info.zod, indent+"  "))
		b.WriteString(",\n")
	}
	b.WriteString(indent + "})")
	return b.String()
}

func indentMultiline(value, indent string) string {
	return strings.ReplaceAll(value, "\n", "\n"+indent)
}

func (g *Generator) lookupModel(name string) *ast.Model {
	for _, model := range g.models {
		if model.Name == name {
			return model
		}
	}
	return nil
}

func (g *Generator) lookupTypeDecl(name string) *ast.TypeDecl {
	for _, block := range g.file.Blocks {
		if decl, ok := block.(*ast.TypeDecl); ok && decl.Name == name {
			return decl
		}
	}
	return nil
}

func (g *Generator) lookupAlias(name string) *ast.Alias {
	for _, block := range g.file.Blocks {
		if alias, ok := block.(*ast.Alias); ok && alias.Name == name {
			return alias
		}
	}
	return nil
}

func (g *Generator) writeFrontendWsPayloadTypes(b *strings.Builder, baseName string, endpoint *ast.WsEndpoint) []string {
	var names []string
	lifecycles := []struct {
		suffix string
		stmts  []ast.ArrowStmt
	}{
		{suffix: "Connect", stmts: endpoint.OnConnect},
		{suffix: "Event", stmts: endpoint.OnMessage},
		{suffix: "Disconnect", stmts: endpoint.OnDisconnect},
	}
	for _, lifecycle := range lifecycles {
		outputs := frontendCollectOutputs(lifecycle.stmts)
		if len(outputs) == 0 {
			continue
		}
		name := frontendWsPayloadTypeName(baseName, lifecycle.suffix)
		info := g.frontendWsLifecycleInfo(lifecycle.stmts)
		fmt.Fprintf(b, "export type %s = %s;\n\n", name, indentMultiline(info.ts, "  "))
		names = append(names, name)
	}
	return names
}

func (g *Generator) writeFrontendWsPayloadSchemas(b *strings.Builder, baseName string, endpoint *ast.WsEndpoint) []string {
	var names []string
	lifecycles := []struct {
		suffix string
		stmts  []ast.ArrowStmt
	}{
		{suffix: "Connect", stmts: endpoint.OnConnect},
		{suffix: "Event", stmts: endpoint.OnMessage},
		{suffix: "Disconnect", stmts: endpoint.OnDisconnect},
	}
	for _, lifecycle := range lifecycles {
		outputs := frontendCollectOutputs(lifecycle.stmts)
		if len(outputs) == 0 {
			continue
		}
		name := frontendWsPayloadTypeName(baseName, lifecycle.suffix) + "Schema"
		info := g.frontendWsLifecycleInfo(lifecycle.stmts)
		fmt.Fprintf(b, "export const %s = %s;\n\n", name, indentMultiline(info.zod, "  "))
		names = append(names, name)
	}
	return names
}
