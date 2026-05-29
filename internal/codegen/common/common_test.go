package common_test

import (
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/common"
)

func TestCamelCase(t *testing.T) {
	cases := map[string]string{
		"my_field":     "myField",
		"rooms-stream": "roomsStream",
		"single":       "single",
		"":             "",
	}
	for in, want := range cases {
		if got := common.CamelCase(in); got != want {
			t.Errorf("CamelCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPascalCase(t *testing.T) {
	cases := map[string]string{
		"my_model":     "MyModel",
		"user-profile": "UserProfile",
		"foo":          "Foo",
	}
	for in, want := range cases {
		if got := common.PascalCase(in); got != want {
			t.Errorf("PascalCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKebabCase(t *testing.T) {
	if got := common.KebabCase("my_service"); got != "my-service" {
		t.Errorf("KebabCase = %q", got)
	}
}

func TestSnakeCase(t *testing.T) {
	cases := map[string]string{
		"myField":  "my_field",
		"MyModel":  "my_model",
		"my_field": "my_field",
		"":         "",
		"X":        "x",
	}
	for in, want := range cases {
		if got := common.SnakeCase(in); got != want {
			t.Errorf("SnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPluralize(t *testing.T) {
	cases := map[string]string{
		"todo":   "todos",
		"box":    "boxes",
		"city":   "cities",
		"boy":    "boys",
		"person": "people",
		"API":    "APIs",
	}
	for in, want := range cases {
		if got := common.Pluralize(in); got != want {
			t.Errorf("Pluralize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractResource(t *testing.T) {
	cases := map[string]string{
		"/api/watermark":  "watermark",
		"/api/jobs/:id":   "jobs",
		"/api/cart/items": "cart",
		"/":               "", // "/" trims to "" → [""] → empty
	}
	for in, want := range cases {
		if got := common.ExtractResource(in); got != want {
			t.Errorf("ExtractResource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPathParam(t *testing.T) {
	if !common.IsPathParam("id", "/api/x/:id") {
		t.Errorf("id should be a path param of /api/x/:id")
	}
	if common.IsPathParam("nope", "/api/x/:id") {
		t.Errorf("nope is not a path param of /api/x/:id")
	}
}

func TestExtractPathParams(t *testing.T) {
	cases := map[string][]string{
		"/api/rooms/:id": {"id"},
		"/ws/:org/:room": {"org", "room"},
		"/api/todos":     nil,
		"":               nil,
	}
	for in, want := range cases {
		got := common.ExtractPathParams(in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ExtractPathParams(%q) = %v, want %v", in, got, want)
		}
	}
}
