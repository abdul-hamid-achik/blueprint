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

	// --- paths ---
	paths := buildPaths(f)
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

// buildPaths constructs the paths object from all Endpoint top-level blocks.
func buildPaths(f *ast.File) map[string]any {
	paths := map[string]any{}

	for _, block := range f.Blocks {
		switch b := block.(type) {
		case *ast.Endpoint:
			oaPath := toOpenAPIPath(b.Path)
			pathItem, exists := paths[oaPath]
			if !exists {
				pathItem = map[string]any{}
				paths[oaPath] = pathItem
			}
			pathItemMap := pathItem.(map[string]any)
			method := strings.ToLower(b.Method)
			pathItemMap[method] = buildOperation(b)

		case *ast.StreamEndpoint:
			// SSE endpoints are exposed as GET with text/event-stream response
			oaPath := toOpenAPIPath(b.Path)
			pathItem, exists := paths[oaPath]
			if !exists {
				pathItem = map[string]any{}
				paths[oaPath] = pathItem
			}
			pathItemMap := pathItem.(map[string]any)
			pathItemMap["get"] = buildStreamOperation(b)

		case *ast.WsEndpoint:
			// WebSocket endpoints are exposed as GET with 101 upgrade response
			oaPath := toOpenAPIPath(b.Path)
			pathItem, exists := paths[oaPath]
			if !exists {
				pathItem = map[string]any{}
				paths[oaPath] = pathItem
			}
			pathItemMap := pathItem.(map[string]any)
			pathItemMap["get"] = buildWsOperation(b)
		}
	}

	return paths
}

// buildStreamOperation builds an OpenAPI operation for a STREAM (SSE) endpoint.
func buildStreamOperation(ep *ast.StreamEndpoint) map[string]any {
	op := map[string]any{}
	if ep.Intent != nil && ep.Intent.Text != "" {
		op["summary"] = ep.Intent.Text
	}
	op["operationId"] = buildOperationId("stream", ep.Path)
	for _, meta := range ep.Meta {
		if meta.Kind == "tags" {
			if tags := exprToStringSlice(meta.Value); len(tags) > 0 {
				op["tags"] = tags
			}
		}
	}
	op["responses"] = map[string]any{
		"200": map[string]any{
			"description": "Server-Sent Events stream",
			"content": map[string]any{
				"text/event-stream": map[string]any{
					"schema": map[string]any{"type": "string"},
				},
			},
		},
	}
	return op
}

// buildWsOperation builds an OpenAPI operation for a WebSocket endpoint.
func buildWsOperation(ep *ast.WsEndpoint) map[string]any {
	op := map[string]any{}
	if ep.Intent != nil && ep.Intent.Text != "" {
		op["summary"] = ep.Intent.Text
	}
	op["operationId"] = buildOperationId("ws", ep.Path)
	for _, meta := range ep.Meta {
		if meta.Kind == "tags" {
			if tags := exprToStringSlice(meta.Value); len(tags) > 0 {
				op["tags"] = tags
			}
		}
	}
	op["responses"] = map[string]any{
		"101": map[string]any{
			"description": "Switching Protocols — WebSocket upgrade",
		},
	}
	// x-websocket extension for tooling that supports it
	op["x-websocket"] = true
	return op
}

// buildOperation constructs the operation object for a single endpoint.
func buildOperation(ep *ast.Endpoint) map[string]any {
	op := map[string]any{}

	// summary from intent
	if ep.Intent != nil && ep.Intent.Text != "" {
		op["summary"] = ep.Intent.Text
	}

	// operationId: method + path segments in camelCase
	op["operationId"] = buildOperationId(ep.Method, ep.Path)

	// tags from EndpointMeta
	for _, meta := range ep.Meta {
		if meta.Kind == "tags" {
			tags := exprToStringSlice(meta.Value)
			if len(tags) > 0 {
				op["tags"] = tags
			}
		}
	}

	// Collect inputs from Stmts
	var inputs []*ast.InputStmt
	for _, stmt := range ep.Stmts {
		if input, ok := stmt.(*ast.InputStmt); ok {
			inputs = append(inputs, input)
		}
	}

	// path parameters from URL pattern
	pathParams := extractPathParams(ep.Path)

	// parameters array
	params := buildParameters(ep.Method, ep.Path, inputs, pathParams)
	if len(params) > 0 {
		op["parameters"] = params
	}

	// requestBody for POST/PUT/PATCH
	method := strings.ToUpper(ep.Method)
	if (method == "POST" || method == "PUT" || method == "PATCH") && len(inputs) > 0 {
		op["requestBody"] = buildRequestBody(inputs, pathParams)
	}

	// responses from OutputStmt nodes
	op["responses"] = buildResponses(ep)

	return op
}

// buildParameters returns OpenAPI parameter objects for an endpoint.
func buildParameters(method, path string, inputs []*ast.InputStmt, pathParams map[string]bool) []any {
	var params []any

	// Add path parameters first (from URL template)
	for paramName := range pathParams {
		p := map[string]any{
			"in":       "path",
			"name":     paramName,
			"required": true,
			"schema":   map[string]any{"type": "string"},
		}
		// If there's a matching input with a type, use it
		for _, input := range inputs {
			if input.Name == paramName {
				p["schema"] = typeToJSONSchema(input.Type, input.Constraints)
				break
			}
		}
		params = append(params, p)
	}

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
			// required if it has the required constraint
			if hasConstraint(input.Constraints, "required") {
				p["required"] = true
			}
			params = append(params, p)
		}
	}

	return params
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
		if hasConstraint(input.Constraints, "required") {
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

// buildResponses returns the responses object for an endpoint.
func buildResponses(ep *ast.Endpoint) map[string]any {
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

	return responses
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
		return map[string]any{"nullable": true}
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
			schemas[name] = modelToSchema(b.Fields)

		case *ast.Content:
			name := toPascalCase(b.Name)
			schemas[name] = modelToSchema(b.AsModel().Fields)

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

	return map[string]any{
		"schemas": schemas,
	}
}

// modelToSchema builds a JSON Schema object from model fields.
func modelToSchema(fields []*ast.Field) map[string]any {
	return fieldsToSchema(fields)
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
			schema["default"] = exprToDefaultValue(c.Value)
		}
	}
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
