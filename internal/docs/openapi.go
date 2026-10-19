package docs

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// GenerateOpenAPI generates an OpenAPI 3.1 JSON spec from a parsed Blueprint AST file.
func GenerateOpenAPI(f *ast.File) ([]byte, error) {
	resolveTranslationKeyTypes(f)
	result := map[string]any{
		"openapi": "3.1.0",
	}

	// --- info ---
	info := buildInfo(f)
	result["info"] = info

	// --- servers ---
	result["servers"] = buildServers(f)

	// --- paths ---
	paths, err := buildPaths(f)
	if err != nil {
		return nil, err
	}
	result["paths"] = paths

	// --- components ---
	components := buildComponents(f)
	result["components"] = components

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("openapi: marshal error: %w", err)
	}
	return data, nil
}

func resolveTranslationKeyTypes(file *ast.File) {
	if file == nil {
		return
	}
	translations := map[string][]string{}
	for _, block := range file.Blocks {
		if tr, ok := block.(*ast.Translation); ok {
			translations[tr.Name] = append([]string(nil), tr.Keys...)
		}
	}
	ast.Walk(file, &translationKeyResolver{translations: translations})
}

type translationKeyResolver struct {
	ast.BaseVisitor
	translations map[string][]string
}

func (r *translationKeyResolver) VisitTranslationKeyType(node *ast.TranslationKeyType) bool {
	if keys, ok := r.translations[node.Namespace]; ok {
		node.Keys = append(node.Keys[:0], keys...)
	}
	return true
}

// buildInfo constructs the info object from the blueprint block.
func buildInfo(f *ast.File) map[string]any {
	info := map[string]any{}

	if f.Blueprint == nil {
		info["title"] = "Blueprint API"
		info["version"] = "0.1.0"
		return info
	}

	info["title"] = f.Blueprint.Name

	// version from entries
	version := "0.1.0"
	for _, e := range f.Blueprint.Entries {
		if e.Key == "version" {
			if s := exprToString(e.Value); s != "" {
				version = s
			}
		}
	}
	info["version"] = version

	// description from intent
	if f.Blueprint.Intent != nil && f.Blueprint.Intent.Text != "" {
		info["description"] = f.Blueprint.Intent.Text
	}

	return info
}

// buildServers returns the server list. The Node target listens on the
// blueprint `port` entry (3000 when absent), so the local URL is derived from it.
func buildServers(f *ast.File) []any {
	port := 3000
	if f.Blueprint != nil {
		for _, e := range f.Blueprint.Entries {
			if lit, ok := e.Value.(*ast.IntLit); ok && e.Key == "port" {
				if n, err := strconv.Atoi(lit.Value); err == nil && n > 0 {
					port = n
				}
			}
		}
	}
	return []any{map[string]any{"url": fmt.Sprintf("http://localhost:%d", port)}}
}

// buildPaths constructs the paths object from all endpoint top-level blocks.
// Operation IDs are made unique in declaration order. Two operations that
// claim the same METHOD + path are an error rather than a silent overwrite.
func buildPaths(f *ast.File) (map[string]any, error) {
	paths := map[string]any{}
	usedOpIDs := map[string]bool{}
	declared := map[string]string{} // "METHOD /openapi/path" -> Blueprint declaration

	addOperation := func(method, bpPath string, op map[string]any) error {
		oaPath := toOpenAPIPath(bpPath)
		key := strings.ToUpper(method) + " " + oaPath
		if prev, dup := declared[key]; dup {
			return fmt.Errorf("openapi: %s is declared by both %s and %s", key, prev, bpPath)
		}
		declared[key] = bpPath
		op["operationId"] = uniqueOperationID(usedOpIDs, op["operationId"].(string))
		pathItem, exists := paths[oaPath]
		if !exists {
			pathItem = map[string]any{}
			paths[oaPath] = pathItem
		}
		pathItem.(map[string]any)[strings.ToLower(method)] = op
		return nil
	}

	for _, block := range f.Blocks {
		switch b := block.(type) {
		case *ast.Endpoint:
			if err := addOperation(b.Method, b.Path, buildOperation(b)); err != nil {
				return nil, err
			}

		case *ast.StreamEndpoint:
			// SSE endpoints are exposed as GET with text/event-stream response
			if err := addOperation("GET", b.Path, buildStreamOperation(b)); err != nil {
				return nil, err
			}

		case *ast.WsEndpoint:
			// WebSocket endpoints are exposed as GET with 101 upgrade response
			if err := addOperation("GET", b.Path, buildWsOperation(b)); err != nil {
				return nil, err
			}
		}
	}

	return paths, nil
}

// uniqueOperationID returns base, or base followed by the first free numeric
// suffix (2, 3, ...) when an earlier operation already claimed it.
func uniqueOperationID(used map[string]bool, base string) string {
	id := base
	for n := 2; used[id]; n++ {
		id = base + strconv.Itoa(n)
	}
	used[id] = true
	return id
}

// operationSummary returns the intent text, or "METHOD /path" when the
// endpoint has no intent, so every operation carries a summary.
func operationSummary(intent *ast.Intent, method, bpPath string) string {
	if intent != nil && intent.Text != "" {
		return intent.Text
	}
	return method + " " + bpPath
}

// buildStreamOperation builds an OpenAPI operation for a STREAM (SSE) endpoint.
func buildStreamOperation(ep *ast.StreamEndpoint) map[string]any {
	op := map[string]any{}
	op["summary"] = operationSummary(ep.Intent, "STREAM", ep.Path)
	op["operationId"] = buildOperationId("stream", ep.Path)
	applyEndpointMeta(op, ep.Meta)
	if params := buildPathParameters(ep.Path, arrowInputs(ep.Stmts)); len(params) > 0 {
		op["parameters"] = params
	}
	responses := map[string]any{
		"200": map[string]any{
			"description": "Server-Sent Events stream",
			"content": map[string]any{
				"text/event-stream": map[string]any{
					"schema": map[string]any{"type": "string"},
				},
			},
		},
	}
	// Stream setup statements run before the SSE response starts, so their
	// guards answer with the error body and the guard status.
	addGuardResponses(responses, ep.Stmts)
	// Declared path inputs are validated before the stream starts.
	addGeneratedErrorResponses(responses, ep.Meta, len(arrowInputs(ep.Stmts)) > 0)
	op["responses"] = responses
	return op
}

// buildWsOperation builds an OpenAPI operation for a WebSocket endpoint.
func buildWsOperation(ep *ast.WsEndpoint) map[string]any {
	op := map[string]any{}
	op["summary"] = operationSummary(ep.Intent, "WS", ep.Path)
	op["operationId"] = buildOperationId("ws", ep.Path)
	applyEndpointMeta(op, ep.Meta)
	if params := buildPathParameters(ep.Path, arrowInputs(ep.Stmts)); len(params) > 0 {
		op["parameters"] = params
	}
	responses := map[string]any{
		"101": map[string]any{
			"description": "Switching Protocols — WebSocket upgrade",
		},
	}
	// Declared path inputs are validated before the upgrade.
	addGeneratedErrorResponses(responses, ep.Meta, len(arrowInputs(ep.Stmts)) > 0)
	op["responses"] = responses
	// x-websocket extension for tooling that supports it
	op["x-websocket"] = true
	return op
}

// applyEndpointMeta copies the tags and security requirement from endpoint
// metadata. An endpoint without an auth declaration gets an explicit empty
// security list, meaning no requirement is declared (operation-level security
// is required by Redocly, and middleware checks are not modeled).
func applyEndpointMeta(op map[string]any, meta []*ast.EndpointMeta) {
	for _, m := range meta {
		if m.Kind == "tags" {
			if tags := exprToStringSlice(m.Value); len(tags) > 0 {
				op["tags"] = tags
			}
		}
	}
	security := []any{}
	name, _, hasAuth := endpointAuthScheme(meta)
	if hasAuth {
		security = append(security, map[string]any{name: []any{}})
	}
	op["security"] = security
	if names := middlewareNames(meta); len(names) > 0 {
		// Middleware is opaque to the spec. Listed so readers can see it, and
		// without an auth declaration the empty security list must not read as public.
		op["x-blueprint-middleware"] = names
		if !hasAuth {
			op["description"] = "Runs middleware " + strings.Join(names, ", ") +
				"; middleware checks are not modeled in this spec, so no security requirement is listed."
		}
	}
}

// buildOperation constructs the operation object for a single endpoint.
func buildOperation(ep *ast.Endpoint) map[string]any {
	op := map[string]any{}

	// summary from intent
	op["summary"] = operationSummary(ep.Intent, ep.Method, ep.Path)

	// operationId: method + path segments in camelCase
	op["operationId"] = buildOperationId(ep.Method, ep.Path)

	applyEndpointMeta(op, ep.Meta)

	inputs := arrowInputs(ep.Stmts)

	// path parameters from URL pattern
	pathParams := extractPathParams(ep.Path)

	// parameters array
	params := buildParameters(ep.Method, ep.Path, inputs, pathParams)
	if len(params) > 0 {
		op["parameters"] = params
	}

	// requestBody for POST/PUT/PATCH, only when some input travels in the body
	method := strings.ToUpper(ep.Method)
	if (method == "POST" || method == "PUT" || method == "PATCH") && hasBodyInputs(inputs, pathParams) {
		op["requestBody"] = buildRequestBody(inputs, pathParams)
	}

	// responses from OutputStmt nodes and runtime-generated errors
	op["responses"] = buildResponses(ep, hasBodyInputs(inputs, pathParams) || validatesPathInputs(inputs, pathParams))

	return op
}

// hasBodyInputs reports whether any non-path input exists. The Node target
// validates those (JSON body, or query string for GET/DELETE) with zValidator.
func hasBodyInputs(inputs []*ast.InputStmt, pathParams map[string]bool) bool {
	for _, input := range inputs {
		if !pathParams[input.Name] {
			return true
		}
	}
	return false
}

// validatesPathInputs reports whether a REST operation has a declared path
// input. The Node target validates every such input at runtime and answers a
// malformed segment with the zValidator-shaped 400.
func validatesPathInputs(inputs []*ast.InputStmt, pathParams map[string]bool) bool {
	for _, input := range inputs {
		if pathParams[input.Name] {
			return true
		}
	}
	return false
}

// pathParamNames returns the ":name" segments of a Blueprint path in URL order.
func pathParamNames(path string) []string {
	var names []string
	seen := map[string]bool{}
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, ":") && !seen[segment[1:]] {
			seen[segment[1:]] = true
			names = append(names, segment[1:])
		}
	}
	return names
}

// arrowInputs returns the `<-` inputs declared in an arrow body. STREAM and WS
// inputs are path parameters (the checker enforces that), so their declared
// types document the path parameter schemas.
func arrowInputs(stmts []ast.ArrowStmt) []*ast.InputStmt {
	var inputs []*ast.InputStmt
	for _, stmt := range stmts {
		if input, ok := stmt.(*ast.InputStmt); ok {
			inputs = append(inputs, input)
		}
	}
	return inputs
}

// buildPathParameters returns the required path parameters in URL order.
// A declared input of the same name supplies the schema, otherwise it is a string.
func buildPathParameters(path string, inputs []*ast.InputStmt) []any {
	var params []any
	for _, paramName := range pathParamNames(path) {
		p := map[string]any{
			"in":       "path",
			"name":     paramName,
			"required": true,
			"schema":   map[string]any{"type": "string"},
		}
		for _, input := range inputs {
			if input.Name == paramName {
				p["schema"] = typeToJSONSchema(input.Type, input.Constraints)
				break
			}
		}
		params = append(params, p)
	}
	return params
}

// buildParameters returns OpenAPI parameter objects for an endpoint.
func buildParameters(method, path string, inputs []*ast.InputStmt, pathParams map[string]bool) []any {
	// Add path parameters first (from URL template)
	params := buildPathParameters(path, inputs)

	// For GET/DELETE endpoints, non-path inputs become query parameters
	m := strings.ToUpper(method)
	if m == "GET" || m == "DELETE" || m == "HEAD" || m == "OPTIONS" {
		for _, input := range inputs {
			if pathParams[input.Name] {
				// already handled above as path param
				continue
			}
			p := map[string]any{
				"in":     "query",
				"name":   input.Name,
				"schema": typeToJSONSchema(input.Type, input.Constraints),
			}
			if inputIsRequired(input.Constraints) {
				p["required"] = true
			}
			params = append(params, p)
		}
	}

	return params
}

// inputIsRequired reports whether the Node target rejects a request without
// the input. Inputs are zod fields, so they are required unless they declare
// `optional` or a `default(...)`.
func inputIsRequired(constraints []*ast.Constraint_) bool {
	return !hasConstraint(constraints, "optional") && !hasConstraint(constraints, "default")
}

// buildRequestBody returns an OpenAPI requestBody object.
func buildRequestBody(inputs []*ast.InputStmt, pathParams map[string]bool) map[string]any {
	properties := map[string]any{}
	var required []string

	for _, input := range inputs {
		if pathParams[input.Name] {
			continue
		}
		schema := typeToJSONSchema(input.Type, input.Constraints)
		properties[input.Name] = schema
		if inputIsRequired(input.Constraints) {
			required = append(required, input.Name)
		}
	}

	inlineSchema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		inlineSchema["required"] = required
	}

	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": inlineSchema,
			},
		},
	}
}

// buildResponses returns the responses object for an endpoint. validatesInput
// is true when the Node target validates request inputs (zValidator, or a
// parsed uuid/int path parameter), which answers 400 with the same body.
func buildResponses(ep *ast.Endpoint, validatesInput bool) map[string]any {
	responses := map[string]any{}

	for _, stmt := range ep.Stmts {
		output, ok := stmt.(*ast.OutputStmt)
		if !ok {
			continue
		}
		status := output.Status
		if status == "" {
			status = "200"
		}
		resp := buildResponseObject(output)
		responses[status] = resp
	}

	// Also walk TryRecover blocks for output statements
	for _, stmt := range ep.Stmts {
		tr, ok := stmt.(*ast.TryRecover)
		if !ok {
			continue
		}
		for _, s := range tr.Try {
			if output, ok := s.(*ast.OutputStmt); ok {
				status := output.Status
				if status == "" {
					status = "200"
				}
				if _, exists := responses[status]; !exists {
					responses[status] = buildResponseObject(output)
				}
			}
		}
		for _, s := range tr.Recover {
			if output, ok := s.(*ast.OutputStmt); ok {
				status := output.Status
				if status == "" {
					status = "500"
				}
				if _, exists := responses[status]; !exists {
					responses[status] = buildResponseObject(output)
				}
			}
		}
	}

	// guard statements answer their failing status with the error body
	addGuardResponses(responses, ep.Stmts)

	// on_error block
	if ep.OnError != nil {
		status := ep.OnError.Status
		if status == "" {
			status = "500"
		}
		if _, exists := responses[status]; !exists {
			msg := ep.OnError.Message
			if msg == "" {
				msg = "Error"
			}
			responses[status] = map[string]any{
				"description": msg,
			}
		}
		// Also add a default response
		responses["default"] = map[string]any{
			"description": ep.OnError.Message,
		}
	}

	// Ensure at least one response
	if len(responses) == 0 {
		responses["200"] = map[string]any{
			"description": "Success",
		}
	}

	addGeneratedErrorResponses(responses, ep.Meta, validatesInput)
	// The Node target's app.onError answers any unhandled error with 500 and
	// the error body, for every REST route.
	addJSONResponse(responses, "500", httpStatusDescription("500"), errorBodySchema())

	return responses
}

// addGuardResponses adds one error response per guard status. Guards that
// share a status list their messages in declaration order.
func addGuardResponses(responses map[string]any, stmts []ast.ArrowStmt) {
	var statuses []string
	messages := map[string][]string{}
	walkGuards(stmts, func(g *ast.GuardStmt) {
		if g.Status == "" {
			return
		}
		if _, seen := messages[g.Status]; !seen {
			statuses = append(statuses, g.Status)
		}
		for _, m := range messages[g.Status] {
			if m == g.Message {
				return
			}
		}
		messages[g.Status] = append(messages[g.Status], g.Message)
	})
	for _, status := range statuses {
		description := strings.Join(messages[status], "; ")
		if description == "" {
			description = httpStatusDescription(status)
		}
		addJSONResponse(responses, status, description, errorBodySchema())
	}
}

// walkGuards calls visit for every guard in stmts, including those inside try/recover.
func walkGuards(stmts []ast.ArrowStmt, visit func(*ast.GuardStmt)) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.GuardStmt:
			visit(s)
		case *ast.TryRecover:
			walkGuards(s.Try, visit)
			walkGuards(s.Recover, visit)
		}
	}
}

// addGeneratedErrorResponses adds the errors the Node target emits from endpoint
// metadata and input validation: zValidator 400, webhook 401/400, and the rate
// limit 429. Each body uses the shape the generated code returns.
func addGeneratedErrorResponses(responses map[string]any, meta []*ast.EndpointMeta, validatesInput bool) {
	if validatesInput {
		addJSONResponse(responses, "400", httpStatusDescription("400"), validationErrorSchema())
	}
	for _, m := range meta {
		switch m.Kind {
		case "auth":
			if authStrategy(m.Value) == "webhook_sig" {
				addJSONResponse(responses, "401", httpStatusDescription("401"), errorBodySchema())
				addJSONResponse(responses, "400", httpStatusDescription("400"), errorBodySchema())
			}
		case "limit":
			addJSONResponse(responses, "429", httpStatusDescription("429"), errorBodySchema())
		}
	}
}

// addJSONResponse adds a JSON response for status unless it is already declared.
// When the status already has a JSON body with another schema, both are listed
// under oneOf. Declared responses without a body are left as they are.
func addJSONResponse(responses map[string]any, status, description string, schema map[string]any) {
	existing, ok := responses[status].(map[string]any)
	if !ok {
		responses[status] = jsonResponse(description, schema)
		return
	}
	content, _ := existing["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	if media == nil {
		return
	}
	current, _ := media["schema"].(map[string]any)
	if current == nil {
		return
	}
	if alts, ok := current["oneOf"].([]any); ok && len(current) == 1 {
		for _, alt := range alts {
			if sameJSON(alt, schema) {
				return
			}
		}
		media["schema"] = map[string]any{"oneOf": append(alts, schema)}
		return
	}
	if sameJSON(current, schema) {
		return
	}
	media["schema"] = map[string]any{"oneOf": []any{current, schema}}
}

// jsonResponse returns a response object whose body is application/json with schema.
func jsonResponse(description string, schema map[string]any) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		},
	}
}

// sameJSON reports whether two schema values marshal to the same JSON.
func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// errorBodySchema is the `{ "error": string }` body the Node target returns for
// guards, webhook rejections, rate limiting and unhandled errors.
func errorBodySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"error": map[string]any{"type": "string"},
		},
		"required": []string{"error"},
	}
}

// validationErrorSchema is the body of a failed zValidator check. The Node target
// uses @hono/zod-validator's default hook, which returns the zod safeParse result
// (`{ success: false, error: ZodError }`) with status 400.
func validationErrorSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"success": map[string]any{"const": false},
			"error": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"const": "ZodError"},
					"issues": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"code":    map[string]any{"type": "string"},
								"path":    map[string]any{"type": "array", "items": map[string]any{"type": []string{"string", "integer"}}},
								"message": map[string]any{"type": "string"},
							},
							"required": []string{"code", "path", "message"},
						},
					},
				},
				"required": []string{"issues"},
			},
		},
		"required": []string{"success", "error"},
	}
}

// endpointAuthScheme maps an endpoint `auth` declaration to an OpenAPI security
// scheme. The scheme name is shared by every endpoint with the same strategy.
// Only the webhook HMAC is enforced by the generated server; the other strategies
// are declared requirements, and the description says so.
func endpointAuthScheme(meta []*ast.EndpointMeta) (string, map[string]any, bool) {
	for _, m := range meta {
		if m.Kind != "auth" {
			continue
		}
		strategy := authStrategy(m.Value)
		notVerified := "Declared with `auth " + strategy + "`; the generated Node server does not verify it. Use middleware for credential checks."
		switch strategy {
		case "bearer":
			return "bearerAuth", map[string]any{"type": "http", "scheme": "bearer", "description": notVerified}, true
		case "jwt":
			return "jwtAuth", map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "JWT", "description": notVerified}, true
		case "basic":
			return "basicAuth", map[string]any{"type": "http", "scheme": "basic", "description": notVerified}, true
		case "api_key":
			return "apiKeyAuth", map[string]any{
				"type": "apiKey", "in": "header", "name": "X-API-Key",
				"description": notVerified + " Blueprint does not name the header; X-API-Key is assumed.",
			}, true
		case "session":
			return "sessionAuth", map[string]any{
				"type": "apiKey", "in": "cookie", "name": "session",
				"description": notVerified + " Blueprint does not name the transport; a cookie named session is assumed.",
			}, true
		case "webhook_sig":
			return "webhookSignature", map[string]any{
				"type": "apiKey", "in": "header", "name": "X-Webhook-Signature",
				"description": "HMAC-SHA256 hex digest of the raw request body, keyed with the secret named in the endpoint's auth declaration. Enforced by the generated Node server.",
			}, true
		}
	}
	return "", nil, false
}

// authStrategy returns the strategy name of an auth value: `bearer` for a bare
// identifier, `api_key` for `api_key(...)`, and so on.
func authStrategy(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.FnCall:
		return v.Name
	}
	return ""
}

// middlewareNames returns the names of the middleware an endpoint uses, in declaration order.
func middlewareNames(meta []*ast.EndpointMeta) []string {
	var names []string
	for _, m := range meta {
		if m.Kind == "use" && m.Use != nil {
			names = append(names, m.Use.Name)
		}
	}
	return names
}

// buildSecuritySchemes collects the security schemes used by any endpoint.
func buildSecuritySchemes(f *ast.File) map[string]any {
	schemes := map[string]any{}
	add := func(meta []*ast.EndpointMeta) {
		if name, scheme, ok := endpointAuthScheme(meta); ok {
			schemes[name] = scheme
		}
	}
	for _, block := range f.Blocks {
		switch b := block.(type) {
		case *ast.Endpoint:
			add(b.Meta)
		case *ast.StreamEndpoint:
			add(b.Meta)
		case *ast.WsEndpoint:
			add(b.Meta)
		}
	}
	return schemes
}

// buildResponseObject builds a single response object from an OutputStmt.
func buildResponseObject(output *ast.OutputStmt) map[string]any {
	resp := map[string]any{}

	status := output.Status
	if status == "" {
		status = "200"
	}
	resp["description"] = httpStatusDescription(status)

	if output.Value == nil {
		return resp
	}

	switch v := output.Value.(type) {
	case *ast.BlockExpr:
		schema := blockExprToSchema(v)
		resp["content"] = map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		}
	case *ast.StringLit:
		// e.g., -> 204 "deleted" — no content schema, just description
		resp["description"] = v.Value
	default:
		// For other expression types, emit a generic schema
		resp["content"] = map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{},
			},
		}
	}

	return resp
}

// blockExprToSchema converts a BlockExpr to a JSON Schema object.
func blockExprToSchema(b *ast.BlockExpr) map[string]any {
	props := map[string]any{}
	for _, kv := range b.Entries {
		props[kv.Key] = inferExprSchema(kv.Value)
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
	}
}

// inferExprSchema infers a simple JSON Schema from an expression value.
func inferExprSchema(e ast.Expr) map[string]any {
	if e == nil {
		return map[string]any{}
	}
	switch e.(type) {
	case *ast.IntLit:
		return map[string]any{"type": "integer"}
	case *ast.FloatLit:
		return map[string]any{"type": "number"}
	case *ast.BoolLit:
		return map[string]any{"type": "boolean"}
	case *ast.StringLit:
		return map[string]any{"type": "string"}
	case *ast.NullLit:
		return map[string]any{"type": "null"}
	case *ast.ListExpr:
		return map[string]any{"type": "array"}
	case *ast.BlockExpr:
		return map[string]any{"type": "object"}
	default:
		return map[string]any{}
	}
}

// buildComponents constructs the components/schemas object.
func buildComponents(f *ast.File) map[string]any {
	schemas := map[string]any{}

	for _, block := range f.Blocks {
		switch b := block.(type) {
		case *ast.Model:
			name := toPascalCase(b.Name)
			schemas[name] = modelToSchema(b)

		case *ast.Content:
			name := toPascalCase(b.Name)
			schemas[name] = modelToSchema(b.AsModel())

		case *ast.TypeDecl:
			name := toPascalCase(b.Name)
			schemas[name] = fieldsToSchema(b.Fields)

		case *ast.Alias:
			name := toPascalCase(b.Name)
			schemas[name] = typeExprToJSONSchema(b.Type, b.Constraints)

		case *ast.Enum:
			name := toPascalCase(b.Name)
			// Check if it's a simple enum (no struct body) or a rich enum
			isSimple := true
			for _, v := range b.Variants {
				if v.Body != nil && len(v.Body.Entries) > 0 {
					isSimple = false
					break
				}
			}
			if isSimple {
				variants := make([]any, len(b.Variants))
				for i, v := range b.Variants {
					variants[i] = v.Name
				}
				schemas[name] = map[string]any{
					"type": "string",
					"enum": variants,
				}
			} else {
				// Rich enum: emit as string enum with the variant names
				variants := make([]any, len(b.Variants))
				for i, v := range b.Variants {
					variants[i] = v.Name
				}
				schemas[name] = map[string]any{
					"type":        "string",
					"enum":        variants,
					"description": "Rich enum — see variant definitions for associated data",
				}
			}

		case *ast.StateMachine:
			name := toPascalCase(b.Name)
			variants := make([]any, len(b.States))
			for i, state := range b.States {
				variants[i] = state
			}
			schemas[name] = map[string]any{
				"type": "string",
				"enum": variants,
			}
		}
	}

	components := map[string]any{
		"schemas": schemas,
	}
	if schemes := buildSecuritySchemes(f); len(schemes) > 0 {
		components["securitySchemes"] = schemes
	}
	return components
}

// modelToSchema builds a JSON Schema object from persisted and read-only
// computed model fields.
func modelToSchema(model *ast.Model) map[string]any {
	result := fieldsToSchema(model.Fields)
	properties := result["properties"].(map[string]any)
	required, _ := result["required"].([]string)
	for _, field := range model.ComputedFields {
		schema := typeToJSONSchema(field.Type, nil)
		schema["readOnly"] = true
		properties[field.Name] = schema
		required = append(required, field.Name)
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

// fieldsToSchema builds a JSON Schema object from a list of fields.
func fieldsToSchema(fields []*ast.Field) map[string]any {
	props := map[string]any{}
	var required []string

	for _, field := range fields {
		schema := typeToJSONSchema(field.Type, field.Constraints)
		props[field.Name] = schema

		// A field is required unless it has the "optional" constraint.
		// primary fields are always required (they're the identifier).
		if !hasConstraint(field.Constraints, "optional") {
			required = append(required, field.Name)
		}
	}

	result := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

// typeToJSONSchema converts a Blueprint TypeExpr + constraints to a JSON Schema map.
func typeToJSONSchema(t ast.TypeExpr, constraints []*ast.Constraint_) map[string]any {
	schema := typeExprToJSONSchema(t, constraints)
	applyConstraints(schema, t, constraints)
	return schema
}

// typeExprToJSONSchema converts only the TypeExpr to a JSON Schema map (no constraints).
func typeExprToJSONSchema(t ast.TypeExpr, constraints []*ast.Constraint_) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	switch v := t.(type) {
	case *ast.PrimitiveType:
		return primitiveToSchema(v.Name, constraints)
	case *ast.TypedJSONType:
		return typeExprToJSONSchema(v.Inner, constraints)
	case *ast.TranslationKeyType:
		schema := map[string]any{"type": "string"}
		if len(v.Keys) > 0 {
			enumVals := make([]any, len(v.Keys))
			for i, key := range v.Keys {
				enumVals[i] = key
			}
			schema["enum"] = enumVals
		}
		return schema
	case *ast.NamedType:
		return map[string]any{
			"$ref": "#/components/schemas/" + toPascalCase(v.Name),
		}
	case *ast.ListType:
		return map[string]any{
			"type":  "array",
			"items": typeExprToJSONSchema(v.Element, nil),
		}
	case *ast.MapType:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": typeExprToJSONSchema(v.Value, nil),
		}
	case *ast.EnumInline:
		variants := make([]any, len(v.Variants))
		for i, variant := range v.Variants {
			variants[i] = variant
		}
		return map[string]any{
			"type": "string",
			"enum": variants,
		}
	case *ast.MimeTypeExpr:
		return map[string]any{
			"type":   "string",
			"format": "binary",
		}
	default:
		return map[string]any{}
	}
}

// primitiveToSchema maps Blueprint primitive type names to JSON Schema.
func primitiveToSchema(name string, constraints []*ast.Constraint_) map[string]any {
	// Check for format constraint (e.g., alias Email = string format(email))
	for _, c := range constraints {
		if c.Kind == "format" {
			if s := exprToString(c.Value); s != "" {
				switch s {
				case "email":
					return map[string]any{"type": "string", "format": "email"}
				case "url", "uri":
					return map[string]any{"type": "string", "format": "uri"}
				case "uuid":
					return map[string]any{"type": "string", "format": "uuid"}
				case "date":
					return map[string]any{"type": "string", "format": "date"}
				case "ip":
					return map[string]any{"type": "string", "anyOf": []any{
						map[string]any{"format": "ipv4"},
						map[string]any{"format": "ipv6"},
					}}
				}
			}
		}
	}

	switch name {
	case "string":
		return map[string]any{"type": "string"}
	case "int":
		return map[string]any{"type": "integer"}
	case "float":
		return map[string]any{"type": "number"}
	case "bool":
		return map[string]any{"type": "boolean"}
	case "uuid":
		return map[string]any{"type": "string", "format": "uuid"}
	case "timestamp":
		return map[string]any{"type": "string", "format": "date-time"}
	case "json":
		return map[string]any{}
	case "file":
		return map[string]any{"type": "string", "format": "binary"}
	case "money":
		return map[string]any{"type": "number"}
	default:
		return map[string]any{}
	}
}

// applyConstraints adds constraint-driven keywords to a JSON Schema map.
func applyConstraints(schema map[string]any, t ast.TypeExpr, constraints []*ast.Constraint_) {
	isNumeric := false
	isString := false
	if prim, ok := t.(*ast.PrimitiveType); ok {
		switch prim.Name {
		case "int", "float", "money":
			isNumeric = true
		case "string":
			isString = true
		}
	}

	for _, c := range constraints {
		switch c.Kind {
		case "min":
			if val := exprToNumber(c.Value); val != nil {
				if isNumeric {
					schema["minimum"] = *val
				} else if isString {
					if iv, ok := toInt(*val); ok {
						schema["minLength"] = iv
					}
				}
			}
		case "max":
			if val := exprToNumber(c.Value); val != nil {
				if isNumeric {
					schema["maximum"] = *val
				} else if isString {
					if iv, ok := toInt(*val); ok {
						schema["maxLength"] = iv
					}
				}
			}
		case "default":
			// default(now) is server-filled and has no literal JSON value to show.
			if v := exprToDefaultValue(c.Value); v != nil && !isNowLit(c.Value) {
				schema["default"] = v
			}
		}
	}
}

// isNowLit reports whether a constraint value is the `now` keyword.
func isNowLit(e ast.Expr) bool {
	_, ok := e.(*ast.NowLit)
	return ok
}

// hasConstraint returns true if the given constraint kind is in the list.
func hasConstraint(constraints []*ast.Constraint_, kind string) bool {
	for _, c := range constraints {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

// --- helpers ---

// toOpenAPIPath converts a Blueprint path (:param) to OpenAPI format ({param}).
func toOpenAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[i] = "{" + p[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}

// extractPathParams returns a set of path parameter names from a Blueprint path.
// e.g. "/api/jobs/:id" -> {"id": true}
func extractPathParams(path string) map[string]bool {
	params := map[string]bool{}
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, ":") {
			params[segment[1:]] = true
		}
	}
	return params
}

// buildOperationId generates an operationId from the HTTP method and path.
// e.g. POST /api/watermark -> postApiWatermark
// e.g. GET /api/jobs/:id -> getApiJobsId
func buildOperationId(method, path string) string {
	prefix := strings.ToLower(method)

	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var parts []string
	parts = append(parts, prefix)
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		// Strip leading : for path params
		seg = strings.TrimPrefix(seg, ":")
		// Split on - and _ for camelCase joining
		subParts := strings.FieldsFunc(seg, func(r rune) bool {
			return r == '-' || r == '_'
		})
		for i, sp := range subParts {
			if i == 0 && len(parts) == 1 {
				// First path segment after method prefix: capitalize first letter
				parts = append(parts, capitalize(sp))
			} else {
				parts = append(parts, capitalize(sp))
			}
		}
	}
	return strings.Join(parts, "")
}

// capitalize uppercases the first character of a string.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// toPascalCase converts snake_case or kebab-case to PascalCase.
func toPascalCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	for i := range parts {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// exprToString converts an expression to a plain string (no JS conversion).
func exprToString(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch v := e.(type) {
	case *ast.StringLit:
		return v.Value
	case *ast.IntLit:
		return v.Value
	case *ast.FloatLit:
		return v.Value
	case *ast.BoolLit:
		if v.Value {
			return "true"
		}
		return "false"
	case *ast.Ident:
		return v.Name
	case *ast.NullLit:
		return "null"
	default:
		return ""
	}
}

// exprToStringSlice converts a list expression or single string to []string.
func exprToStringSlice(e ast.Expr) []string {
	if e == nil {
		return nil
	}
	if list, ok := e.(*ast.ListExpr); ok {
		result := make([]string, 0, len(list.Elements))
		for _, el := range list.Elements {
			if s := exprToString(el); s != "" {
				result = append(result, s)
			}
		}
		return result
	}
	if s := exprToString(e); s != "" {
		return []string{s}
	}
	return nil
}

// exprToNumber converts an expression to a *float64 if it's numeric.
func exprToNumber(e ast.Expr) *float64 {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *ast.IntLit:
		n, err := strconv.ParseFloat(v.Value, 64)
		if err == nil {
			return &n
		}
	case *ast.FloatLit:
		n, err := strconv.ParseFloat(v.Value, 64)
		if err == nil {
			return &n
		}
	}
	return nil
}

// exprToDefaultValue converts a constraint value expression to a JSON-compatible Go value.
func exprToDefaultValue(e ast.Expr) any {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *ast.StringLit:
		return v.Value
	case *ast.IntLit:
		n, err := strconv.ParseInt(v.Value, 10, 64)
		if err == nil {
			return n
		}
		return v.Value
	case *ast.FloatLit:
		n, err := strconv.ParseFloat(v.Value, 64)
		if err == nil {
			return n
		}
		return v.Value
	case *ast.BoolLit:
		return v.Value
	case *ast.Ident:
		// enum variant or identifier default value — return as string
		return v.Name
	case *ast.NullLit:
		return nil
	case *ast.NowLit:
		return "now"
	default:
		return nil
	}
}

// toInt converts a float64 to int if it is a whole number.
func toInt(f float64) (int, bool) {
	i := int(f)
	if float64(i) == f {
		return i, true
	}
	return 0, false
}

// httpStatusDescription returns a human-readable description for common HTTP status codes.
func httpStatusDescription(status string) string {
	switch status {
	case "200":
		return "OK"
	case "201":
		return "Created"
	case "202":
		return "Accepted"
	case "204":
		return "No Content"
	case "400":
		return "Bad Request"
	case "401":
		return "Unauthorized"
	case "403":
		return "Forbidden"
	case "404":
		return "Not Found"
	case "409":
		return "Conflict"
	case "413":
		return "Payload Too Large"
	case "415":
		return "Unsupported Media Type"
	case "422":
		return "Unprocessable Entity"
	case "429":
		return "Too Many Requests"
	case "500":
		return "Internal Server Error"
	case "502":
		return "Bad Gateway"
	case "503":
		return "Service Unavailable"
	default:
		return "Response"
	}
}
