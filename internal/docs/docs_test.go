package docs_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/docs"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// parseAndGenOpenAPI parses the blueprint source and generates OpenAPI, fataling on any error.
func parseAndGenOpenAPI(t *testing.T, src string) map[string]any {
	t.Helper()
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	data, err := docs.GenerateOpenAPI(file)
	if err != nil {
		t.Fatalf("GenerateOpenAPI error: %v", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("invalid JSON from GenerateOpenAPI: %v\noutput:\n%s", err, data)
	}
	return spec
}

func TestGenerateOpenAPI_ValidJSON(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	data, err := docs.GenerateOpenAPI(file)
	if err != nil {
		t.Fatalf("GenerateOpenAPI error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("GenerateOpenAPI returned empty output")
	}
	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("GenerateOpenAPI output is not valid JSON: %v\noutput:\n%s", err, data)
	}
}

func TestGenerateOpenAPI_OpenAPIVersion(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}
`
	spec := parseAndGenOpenAPI(t, src)
	if spec["openapi"] != "3.1.0" {
		t.Errorf("expected openapi version 3.1.0, got %v", spec["openapi"])
	}
}

func TestGenerateOpenAPI_Info(t *testing.T) {
	src := `blueprint "my-api" {
  version "2.5.0"
  port    3000
  runtime node
}
`
	spec := parseAndGenOpenAPI(t, src)

	info, ok := spec["info"].(map[string]any)
	if !ok {
		t.Fatalf("expected info to be a map, got %T", spec["info"])
	}
	if info["title"] != "my-api" {
		t.Errorf("expected info.title = 'my-api', got %v", info["title"])
	}
	if info["version"] != "2.5.0" {
		t.Errorf("expected info.version = '2.5.0', got %v", info["version"])
	}
}

func TestGenerateOpenAPI_Info_DefaultVersion(t *testing.T) {
	// No version entry in blueprint → should default to "0.1.0"
	src := `blueprint "bare" {
  port    3000
  runtime node
}
`
	spec := parseAndGenOpenAPI(t, src)
	info, ok := spec["info"].(map[string]any)
	if !ok {
		t.Fatalf("expected info to be a map, got %T", spec["info"])
	}
	if info["version"] != "0.1.0" {
		t.Errorf("expected default version '0.1.0', got %v", info["version"])
	}
}

func TestGenerateOpenAPI_Paths(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "List items"
GET /api/items {
  -> 200 "ok"
}

@ "Create item"
POST /api/items {
  <- name string required
  -> 201 "created"
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected paths to be a map, got %T", spec["paths"])
	}

	pathItem, exists := paths["/api/items"]
	if !exists {
		t.Fatalf("expected path /api/items in spec, paths = %v", paths)
	}
	pathMap, ok := pathItem.(map[string]any)
	if !ok {
		t.Fatalf("expected path item to be a map, got %T", pathItem)
	}
	if _, hasGet := pathMap["get"]; !hasGet {
		t.Error("expected GET operation in /api/items path item")
	}
	if _, hasPost := pathMap["post"]; !hasPost {
		t.Error("expected POST operation in /api/items path item")
	}
}

func TestGenerateOpenAPI_PathParams(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Get item by ID"
GET /api/items/:id {
  <- id uuid required
  -> 200 { id: id }
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected paths to be a map, got %T", spec["paths"])
	}

	// Blueprint :id becomes OpenAPI {id}
	pathItem, exists := paths["/api/items/{id}"]
	if !exists {
		t.Fatalf("expected path /api/items/{id} in spec, got paths: %v", paths)
	}

	pathMap := pathItem.(map[string]any)
	getOp, ok := pathMap["get"].(map[string]any)
	if !ok {
		t.Fatal("expected GET operation in /api/items/{id}")
	}

	params, ok := getOp["parameters"].([]any)
	if !ok || len(params) == 0 {
		t.Fatal("expected parameters in GET /api/items/{id} operation")
	}

	// Find the path parameter
	var found bool
	for _, p := range params {
		pm := p.(map[string]any)
		if pm["name"] == "id" && pm["in"] == "path" {
			found = true
			if pm["required"] != true {
				t.Error("path parameter 'id' should be required")
			}
		}
	}
	if !found {
		t.Errorf("expected path parameter 'id' in parameters: %v", params)
	}
}

func TestGenerateOpenAPI_QueryParams(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "List items with pagination"
GET /api/items {
  <- page int default(1)
  <- limit int default(20)
  -> 200 "ok"
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths := spec["paths"].(map[string]any)
	pathItem := paths["/api/items"].(map[string]any)
	getOp := pathItem["get"].(map[string]any)

	params, ok := getOp["parameters"].([]any)
	if !ok || len(params) == 0 {
		t.Fatal("expected parameters in GET /api/items operation")
	}

	paramNames := map[string]bool{}
	for _, p := range params {
		pm := p.(map[string]any)
		if pm["in"] == "query" {
			paramNames[pm["name"].(string)] = true
		}
	}
	if !paramNames["page"] {
		t.Error("expected query parameter 'page'")
	}
	if !paramNames["limit"] {
		t.Error("expected query parameter 'limit'")
	}
}

func TestGenerateOpenAPI_RequestBody(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Create a new item"
POST /api/items {
  <- name  string required
  <- price int    min(0)
  -> 201 "created"
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths := spec["paths"].(map[string]any)
	pathItem := paths["/api/items"].(map[string]any)
	postOp, ok := pathItem["post"].(map[string]any)
	if !ok {
		t.Fatal("expected POST operation in /api/items")
	}

	requestBody, ok := postOp["requestBody"].(map[string]any)
	if !ok {
		t.Fatal("expected requestBody in POST /api/items operation")
	}
	if requestBody["required"] != true {
		t.Error("requestBody should be required")
	}

	content, ok := requestBody["content"].(map[string]any)
	if !ok {
		t.Fatal("expected content in requestBody")
	}
	jsonContent, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatal("expected application/json in requestBody content")
	}
	schema, ok := jsonContent["schema"].(map[string]any)
	if !ok {
		t.Fatal("expected schema in requestBody content")
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in requestBody schema")
	}
	if _, hasName := props["name"]; !hasName {
		t.Error("requestBody schema should have 'name' property")
	}
	if _, hasPrice := props["price"]; !hasPrice {
		t.Error("requestBody schema should have 'price' property")
	}
}

func TestGenerateOpenAPI_RequestBody_RequiredFields(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Create"
POST /api/widgets {
  <- title    string required
  <- subtitle string optional
  -> 201 "ok"
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths := spec["paths"].(map[string]any)
	pathItem := paths["/api/widgets"].(map[string]any)
	postOp := pathItem["post"].(map[string]any)
	requestBody := postOp["requestBody"].(map[string]any)
	content := requestBody["content"].(map[string]any)
	jsonContent := content["application/json"].(map[string]any)
	schema := jsonContent["schema"].(map[string]any)

	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("expected required array in schema")
	}
	found := false
	for _, r := range required {
		if r == "title" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'title' in required fields, got: %v", required)
	}
}

func TestGenerateOpenAPI_Components(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

model product {
  id    uuid   primary
  name  string required
  price int    optional
}

@ "List products"
GET /api/products {
  -> 200 "ok"
}
`
	spec := parseAndGenOpenAPI(t, src)

	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatalf("expected components to be a map, got %T", spec["components"])
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("expected components.schemas to be a map, got %T", components["schemas"])
	}

	// model 'product' → PascalCase 'Product'
	productSchema, exists := schemas["Product"]
	if !exists {
		t.Fatalf("expected 'Product' in components.schemas, got: %v", schemas)
	}

	productMap, ok := productSchema.(map[string]any)
	if !ok {
		t.Fatalf("expected Product schema to be a map, got %T", productSchema)
	}
	if productMap["type"] != "object" {
		t.Errorf("expected Product schema type to be 'object', got %v", productMap["type"])
	}

	props, ok := productMap["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected Product schema to have properties, got %T", productMap["properties"])
	}
	if _, hasID := props["id"]; !hasID {
		t.Error("Product schema should have 'id' property")
	}
	if _, hasName := props["name"]; !hasName {
		t.Error("Product schema should have 'name' property")
	}
}

func TestGenerateOpenAPI_Components_EnumSchema(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

enum status {
  active
  inactive
  pending
}
`
	spec := parseAndGenOpenAPI(t, src)

	components := spec["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)

	statusSchema, exists := schemas["Status"]
	if !exists {
		t.Fatalf("expected 'Status' in components.schemas, got: %v", schemas)
	}
	statusMap := statusSchema.(map[string]any)
	if statusMap["type"] != "string" {
		t.Errorf("expected enum schema type 'string', got %v", statusMap["type"])
	}
	enumVals, ok := statusMap["enum"].([]any)
	if !ok {
		t.Fatalf("expected enum values array, got %T", statusMap["enum"])
	}
	if len(enumVals) != 3 {
		t.Errorf("expected 3 enum variants, got %d: %v", len(enumVals), enumVals)
	}
}

func TestGenerateOpenAPI_Responses(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Health check"
GET /api/health {
  -> 200 "ok"
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths := spec["paths"].(map[string]any)
	pathItem := paths["/api/health"].(map[string]any)
	getOp := pathItem["get"].(map[string]any)

	responses, ok := getOp["responses"].(map[string]any)
	if !ok {
		t.Fatalf("expected responses in GET /api/health, got %T", getOp["responses"])
	}
	if _, has200 := responses["200"]; !has200 {
		t.Errorf("expected 200 response in GET /api/health, got: %v", responses)
	}
}

func TestGenerateOpenAPI_OperationId(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "List items"
GET /api/items {
  -> 200 "ok"
}

@ "Create item"
POST /api/items {
  <- name string required
  -> 201 "ok"
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths := spec["paths"].(map[string]any)
	pathItem := paths["/api/items"].(map[string]any)

	getOp := pathItem["get"].(map[string]any)
	if getOp["operationId"] != "getApiItems" {
		t.Errorf("expected operationId 'getApiItems', got %v", getOp["operationId"])
	}

	postOp := pathItem["post"].(map[string]any)
	if postOp["operationId"] != "postApiItems" {
		t.Errorf("expected operationId 'postApiItems', got %v", postOp["operationId"])
	}
}

func TestGenerateOpenAPI_Summary(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Returns the health status of the service"
GET /api/health {
  -> 200 "ok"
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths := spec["paths"].(map[string]any)
	pathItem := paths["/api/health"].(map[string]any)
	getOp := pathItem["get"].(map[string]any)

	if getOp["summary"] != "Returns the health status of the service" {
		t.Errorf("expected summary from intent annotation, got %v", getOp["summary"])
	}
}

func TestGenerateOpenAPI_NoEndpoints_EmptyPaths(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected paths to be a map, got %T", spec["paths"])
	}
	if len(paths) != 0 {
		t.Errorf("expected empty paths for blueprint with no endpoints, got %d paths", len(paths))
	}
}

func TestGenerateOpenAPI_JSON_IsIndented(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}
`
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	data, err := docs.GenerateOpenAPI(file)
	if err != nil {
		t.Fatalf("GenerateOpenAPI error: %v", err)
	}
	// Indented JSON contains newlines
	if !strings.Contains(string(data), "\n") {
		t.Error("expected indented JSON output (with newlines)")
	}
}

func TestGenerateOpenAPI_PathParam_ConvertedSyntax(t *testing.T) {
	// Blueprint uses :param, OpenAPI uses {param} — verify conversion.
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Get by id"
GET /api/users/:id {
  <- id uuid required
  -> 200 { id: id }
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths := spec["paths"].(map[string]any)
	if _, colonExists := paths["/api/users/:id"]; colonExists {
		t.Error("paths should use {id} not :id syntax")
	}
	if _, braceExists := paths["/api/users/{id}"]; !braceExists {
		t.Errorf("expected /api/users/{id} in paths, got: %v", paths)
	}
}

func TestGenerateOpenAPI_StreamEndpoint(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Job progress stream"
STREAM /api/jobs/:id/progress {
  auth api_key
  tags ["jobs", "streaming"]

  <- id uuid required

  stream {
    |> on event(job_done) where(job_id == id) {
      -> { percent: 100, stage: "done" }
      |> close
    }
  }
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected paths map, got %T", spec["paths"])
	}

	pathItem, exists := paths["/api/jobs/{id}/progress"]
	if !exists {
		t.Fatalf("expected /api/jobs/{id}/progress in paths, got %v", paths)
	}
	pathMap := pathItem.(map[string]any)
	getOp, hasGet := pathMap["get"]
	if !hasGet {
		t.Fatal("expected GET operation for STREAM endpoint")
	}
	op := getOp.(map[string]any)
	if op["summary"] != "Job progress stream" {
		t.Errorf("expected summary 'Job progress stream', got %v", op["summary"])
	}
	// Should have tags
	tags, _ := op["tags"].([]any)
	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %v", tags)
	}
	// Should have text/event-stream response
	responses := op["responses"].(map[string]any)
	resp200, has200 := responses["200"]
	if !has200 {
		t.Fatal("STREAM operation should have 200 response")
	}
	respMap := resp200.(map[string]any)
	content := respMap["content"].(map[string]any)
	if _, hasSSE := content["text/event-stream"]; !hasSSE {
		t.Errorf("expected text/event-stream content type, got %v", content)
	}
}

func TestGenerateOpenAPI_WsEndpoint(t *testing.T) {
	src := `blueprint "test" {
  version "1.0.0"
  port    3000
  runtime node
}

@ "Real-time updates"
WS /ws/updates {
  auth bearer
  tags ["realtime"]

  on_connect {
    -> { type: "connected" }
  }

  on_message {
    -> { type: "echo" }
  }

  on_disconnect {
    |> log "disconnected"
  }
}
`
	spec := parseAndGenOpenAPI(t, src)

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected paths map, got %T", spec["paths"])
	}

	pathItem, exists := paths["/ws/updates"]
	if !exists {
		t.Fatalf("expected /ws/updates in paths, got %v", paths)
	}
	pathMap := pathItem.(map[string]any)
	getOp, hasGet := pathMap["get"]
	if !hasGet {
		t.Fatal("expected GET operation for WS endpoint")
	}
	op := getOp.(map[string]any)
	if op["summary"] != "Real-time updates" {
		t.Errorf("expected summary 'Real-time updates', got %v", op["summary"])
	}
	// Should have x-websocket extension
	if op["x-websocket"] != true {
		t.Errorf("expected x-websocket:true, got %v", op["x-websocket"])
	}
	// Should have 101 response
	responses := op["responses"].(map[string]any)
	resp101, has101 := responses["101"]
	if !has101 {
		t.Fatal("WS operation should have 101 response")
	}
	respMap := resp101.(map[string]any)
	if respMap["description"] == "" {
		t.Error("101 response should have description")
	}
}
