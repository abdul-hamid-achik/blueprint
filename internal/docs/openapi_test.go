package docs_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/docs"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// openAPIOperation returns the operation object at spec.paths[path][method].
func openAPIOperation(t *testing.T, spec map[string]any, path, method string) map[string]any {
	t.Helper()
	paths := spec["paths"].(map[string]any)
	item, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("missing path %s in %v", path, paths)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("missing %s operation on %s in %v", method, path, item)
	}
	return op
}

// openAPIResponseSchema returns the application/json schema of a response.
func openAPIResponseSchema(t *testing.T, op map[string]any, status string) map[string]any {
	t.Helper()
	resp, ok := op["responses"].(map[string]any)[status].(map[string]any)
	if !ok {
		t.Fatalf("missing response %s in %v", status, op["responses"])
	}
	content, ok := resp["content"].(map[string]any)
	if !ok {
		t.Fatalf("response %s has no content: %v", status, resp)
	}
	media := content["application/json"].(map[string]any)
	return media["schema"].(map[string]any)
}

// generateOpenAPIBytes parses src and returns the generated OpenAPI bytes.
func generateOpenAPIBytes(t *testing.T, src string) []byte {
	t.Helper()
	file, errs := parser.ParseFile("test.bp", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	data, err := docs.GenerateOpenAPI(file)
	if err != nil {
		t.Fatalf("GenerateOpenAPI error: %v", err)
	}
	return data
}

const openAPIHeader = `blueprint "test" {
  version "1.0.0"
  port    8080
  runtime node
}
`

func TestGenerateOpenAPI_ServersFromPort(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader)
	servers := spec["servers"].([]any)
	if got := servers[0].(map[string]any)["url"]; got != "http://localhost:8080" {
		t.Errorf("servers url = %v, want http://localhost:8080", got)
	}

	noPort := parseAndGenOpenAPI(t, `blueprint "test" { version "1.0.0" runtime node }`)
	servers = noPort["servers"].([]any)
	if got := servers[0].(map[string]any)["url"]; got != "http://localhost:3000" {
		t.Errorf("default servers url = %v, want http://localhost:3000", got)
	}
}

func TestGenerateOpenAPI_StreamAndWsPathParams(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
STREAM /api/rooms/:id/live {
  <- id uuid required
}

WS /ws/rooms/:id {
  <- id int required
}

WS /ws/lobby/:name {
}
`)
	// Declared `<-` inputs type the path parameter; undeclared ones stay strings.
	wantSchema := map[string]map[string]any{
		"/api/rooms/{id}/live": {"type": "string", "format": "uuid"},
		"/ws/rooms/{id}":       {"type": "integer"},
		"/ws/lobby/{name}":     {"type": "string"},
	}
	for path, want := range wantSchema {
		op := openAPIOperation(t, spec, path, "get")
		params, _ := op["parameters"].([]any)
		if len(params) != 1 {
			t.Fatalf("%s: expected one path parameter, got %v", path, params)
		}
		p := params[0].(map[string]any)
		if p["in"] != "path" || p["required"] != true {
			t.Errorf("%s: path parameter = %v", path, p)
		}
		schema, _ := p["schema"].(map[string]any)
		for key, value := range want {
			if schema[key] != value {
				t.Errorf("%s: schema[%q] = %v, want %v (schema %v)", path, key, schema[key], value, schema)
			}
		}
	}
}

func TestGenerateOpenAPI_OperationIDsAreUnique(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
GET /api/a-b {
  -> 200 "ok"
}

GET /api/a_b {
  -> 200 "ok"
}
`)
	first := openAPIOperation(t, spec, "/api/a-b", "get")["operationId"]
	second := openAPIOperation(t, spec, "/api/a_b", "get")["operationId"]
	if first != "getApiAB" || second != "getApiAB2" {
		t.Errorf("operationIds = %v, %v; want getApiAB, getApiAB2", first, second)
	}
}

func TestGenerateOpenAPI_SameMethodAndPathIsError(t *testing.T) {
	file, errs := parser.ParseFile("test.bp", []byte(openAPIHeader+`
GET /api/rooms {
  -> 200 "ok"
}

STREAM /api/rooms {
}
`))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if _, err := docs.GenerateOpenAPI(file); err == nil {
		t.Fatal("expected an error when a GET endpoint and a STREAM share a path")
	}
}

func TestGenerateOpenAPI_SummaryFallsBackToMethodAndPath(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
GET /api/items {
  -> 200 "ok"
}

@ "Create an item"
POST /api/items {
  -> 201 "ok"
}
`)
	if got := openAPIOperation(t, spec, "/api/items", "get")["summary"]; got != "GET /api/items" {
		t.Errorf("fallback summary = %v", got)
	}
	if got := openAPIOperation(t, spec, "/api/items", "post")["summary"]; got != "Create an item" {
		t.Errorf("intent summary = %v", got)
	}
}

func TestGenerateOpenAPI_SecuritySchemesAndRequirements(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
secret STRIPE_KEY required

GET /api/private {
  auth bearer
  -> 200 "ok"
}

GET /api/keyed {
  auth api_key
  -> 200 "ok"
}

POST /webhooks/stripe {
  auth webhook_sig using(secret.STRIPE_KEY)
  -> 200 "ok"
}

GET /api/public {
  -> 200 "ok"
}

GET /api/wrapped {
  use require_auth
  -> 200 "ok"
}
`)
	schemes := spec["components"].(map[string]any)["securitySchemes"].(map[string]any)
	for _, name := range []string{"bearerAuth", "apiKeyAuth", "webhookSignature"} {
		if _, ok := schemes[name]; !ok {
			t.Errorf("missing security scheme %s in %v", name, schemes)
		}
	}
	if len(schemes) != 3 {
		t.Errorf("expected exactly the three used schemes, got %v", schemes)
	}
	bearer := schemes["bearerAuth"].(map[string]any)
	if bearer["type"] != "http" || bearer["scheme"] != "bearer" {
		t.Errorf("bearerAuth = %v", bearer)
	}

	requirement := func(path, method string) []any {
		return openAPIOperation(t, spec, path, method)["security"].([]any)
	}
	if got := requirement("/api/private", "get"); len(got) != 1 || got[0].(map[string]any)["bearerAuth"] == nil {
		t.Errorf("/api/private security = %v", got)
	}
	if got := requirement("/webhooks/stripe", "post"); len(got) != 1 || got[0].(map[string]any)["webhookSignature"] == nil {
		t.Errorf("/webhooks/stripe security = %v", got)
	}
	if got := requirement("/api/public", "get"); len(got) != 0 {
		t.Errorf("/api/public should declare no requirement, got %v", got)
	}

	wrapped := openAPIOperation(t, spec, "/api/wrapped", "get")
	if len(wrapped["security"].([]any)) != 0 {
		t.Errorf("middleware-only endpoint must not list a requirement: %v", wrapped["security"])
	}
	if desc, _ := wrapped["description"].(string); !strings.Contains(desc, "require_auth") {
		t.Errorf("middleware-only endpoint should say middleware is not modeled, got %q", desc)
	}
}

func TestGenerateOpenAPI_GuardAndRuntimeErrorResponses(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
GET /api/items/:id {
  <- id uuid required
  |> guard id -> 404 "Item not found"
  -> 200 "ok"
}

POST /api/items {
  limit 60/min
  <- name string required
  -> 201 "ok"
}

STREAM /api/items/:id/live {
  <- id uuid required
  |> guard id -> 409 "Item locked"
}
`)
	get := openAPIOperation(t, spec, "/api/items/{id}", "get")
	notFound := get["responses"].(map[string]any)["404"].(map[string]any)
	if notFound["description"] != "Item not found" {
		t.Errorf("404 description = %v", notFound["description"])
	}
	errSchema := openAPIResponseSchema(t, get, "404")
	if errSchema["properties"].(map[string]any)["error"] == nil {
		t.Errorf("guard 404 body should be {error: string}, got %v", errSchema)
	}
	if _, ok := get["responses"].(map[string]any)["500"]; !ok {
		t.Error("REST operations should document the app-wide 500 error body")
	}

	post := openAPIOperation(t, spec, "/api/items", "post")
	if _, ok := post["responses"].(map[string]any)["429"]; !ok {
		t.Error("limit metadata should document 429")
	}
	validation := openAPIResponseSchema(t, post, "400")
	if validation["properties"].(map[string]any)["success"] == nil {
		t.Errorf("validated input should document the zod 400 body, got %v", validation)
	}

	stream := openAPIOperation(t, spec, "/api/items/{id}/live", "get")
	if _, ok := stream["responses"].(map[string]any)["409"]; !ok {
		t.Error("stream guards should document their status")
	}
}

func TestGenerateOpenAPI_PathOnlyEndpointHasNoValidationResponse(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
GET /api/items/:id {
  <- id uuid required
  -> 200 "ok"
}
`)
	op := openAPIOperation(t, spec, "/api/items/{id}", "get")
	if _, ok := op["responses"].(map[string]any)["400"]; ok {
		t.Error("path-only inputs are not validated, so no 400 should be documented")
	}
}

func TestGenerateOpenAPI_RequiredInputsFollowRuntimeValidation(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
POST /api/items {
  <- a int
  <- b int default(1)
  <- c int optional
  <- d int required
  -> 201 "ok"
}
`)
	body := openAPIOperation(t, spec, "/api/items", "post")["requestBody"].(map[string]any)
	schema := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	required, _ := schema["required"].([]any)
	if len(required) != 2 || required[0] != "a" || required[1] != "d" {
		t.Errorf("required = %v, want [a d]", required)
	}
}

func TestGenerateOpenAPI_NoRequestBodyWhenOnlyPathInputs(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
POST /api/items/:id/publish {
  <- id uuid required
  -> 200 "ok"
}
`)
	op := openAPIOperation(t, spec, "/api/items/{id}/publish", "post")
	if _, ok := op["requestBody"]; ok {
		t.Errorf("no body inputs, so no requestBody expected: %v", op["requestBody"])
	}
}

func TestGenerateOpenAPI_NullAndNowFollowOpenAPI31(t *testing.T) {
	spec := parseAndGenOpenAPI(t, openAPIHeader+`
model event {
  id      uuid      primary
  created timestamp default(now)
}

GET /api/nothing {
  -> 200 { value: null }
}
`)
	created := spec["components"].(map[string]any)["schemas"].(map[string]any)["Event"].(map[string]any)["properties"].(map[string]any)["created"].(map[string]any)
	if _, ok := created["default"]; ok {
		t.Errorf("default(now) has no literal JSON value, got default=%v", created["default"])
	}
	value := openAPIResponseSchema(t, openAPIOperation(t, spec, "/api/nothing", "get"), "200")["properties"].(map[string]any)["value"].(map[string]any)
	if value["type"] != "null" {
		t.Errorf("null literal schema = %v, want type null", value)
	}
	if _, ok := value["nullable"]; ok {
		t.Error("nullable is not an OpenAPI 3.1 keyword")
	}
}

func TestGenerateOpenAPI_OutputIsDeterministic(t *testing.T) {
	src := openAPIHeader + `
secret STRIPE_KEY required

GET /api/x/:a/:b/:c {
  auth api_key
  <- a string required
  <- b string required
  <- c string required
  |> guard a -> 404 "a missing"
  |> guard b -> 404 "b missing"
  -> 200 { a: a, b: b, c: c, flag: null }
}

POST /api/y/:id {
  auth webhook_sig using(secret.STRIPE_KEY)
  limit 10/min
  <- id uuid required
  <- name string required
  <- count int default(0)
  -> 200 { ok: true }
}

STREAM /api/z/:p/:q {
  <- p uuid required
  <- q uuid required
}
`
	first := generateOpenAPIBytes(t, src)
	for i := 0; i < 20; i++ {
		if again := generateOpenAPIBytes(t, src); string(again) != string(first) {
			t.Fatalf("run %d produced different bytes", i)
		}
	}
}
