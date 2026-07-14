package docs_test

import "testing"

func TestOpenAPIComputedFieldsAreReadOnlyAndRequired(t *testing.T) {
	spec := parseAndGenOpenAPI(t, `blueprint "test" { version "1.0" runtime node }
model person {
  id uuid primary
  first_name string required
  last_name string required
  computed full_name string = first_name + " " + last_name
}`)
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	person := schemas["Person"].(map[string]any)
	properties := person["properties"].(map[string]any)
	fullName := properties["full_name"].(map[string]any)
	if fullName["type"] != "string" || fullName["readOnly"] != true {
		t.Fatalf("full_name schema=%v", fullName)
	}
	required := person["required"].([]any)
	found := false
	for _, field := range required {
		found = found || field == "full_name"
	}
	if !found {
		t.Fatalf("computed field missing from required: %v", required)
	}
}
