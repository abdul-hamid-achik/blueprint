package python_test

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

const declsHeader = `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
`

func TestPython_NamedEnumEmitsEnumModuleAndPostgresName(t *testing.T) {
	outDir := buildPython(t, declsHeader+`enum OrderStatus {
  pending
  shipped
}

model order {
  id     uuid   primary
  status OrderStatus default(pending)
}

GET /api/orders/:status {
  <- status OrderStatus required
  -> 200 { status: status }
}
`)
	enums := readPy(t, outDir, "src/models/enums.py")
	for _, want := range []string{
		"class OrderStatus(str, enum.Enum):",
		`pending = "pending"`,
		`shipped = "shipped"`,
	} {
		if !strings.Contains(enums, want) {
			t.Errorf("enums.py missing %q:\n%s", want, enums)
		}
	}
	schema := readPy(t, outDir, "src/models/schema.py")
	for _, want := range []string{
		"from src.models.enums import OrderStatus",
		`Enum(OrderStatus, name="orderstatus", values_callable=lambda e: [m.value for m in e])`,
		"status: Mapped[OrderStatus] = mapped_column(",
		"default=OrderStatus.pending",
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema.py missing %q:\n%s", want, schema)
		}
	}
	pyd := readPy(t, outDir, "src/models/pydantic.py")
	if !strings.Contains(pyd, "status: OrderStatus") {
		t.Errorf("pydantic.py should type the read model with the enum:\n%s", pyd)
	}
	routes := readPy(t, outDir, "src/routes/orders.py")
	for _, want := range []string{
		"from src.models.enums import OrderStatus",
		"async def get_orders_status(status: OrderStatus)",
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("route missing %q:\n%s", want, routes)
		}
	}
}

func TestPython_EnumOnlySpecGetsPackageInit(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
enum Mode { fast slow }

GET /api/mode {
  <- mode Mode default(fast)
  -> 200 { mode: mode }
}
`)
	readPy(t, outDir, "src/models/__init__.py")
	readPy(t, outDir, "src/models/enums.py")
	routes := readPy(t, outDir, "src/routes/mode.py")
	if !strings.Contains(routes, `mode: Mode = Query(Mode.fast)`) {
		t.Errorf("enum default should render as the member:\n%s", routes)
	}
}

func TestPython_InlineEnumEndpointInputsUseLiteral(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/items {
  <- level enum(low, high) default(low)
  -> 200 { level: level }
}

POST /api/items {
  <- kind enum(a, b) required
  -> 200 { kind: kind }
}
`)
	routes := readPy(t, outDir, "src/routes/items.py")
	for _, want := range []string{
		"from typing import Literal",
		`level: Literal["low", "high"] = Query("low")`,
		`kind: Literal["a", "b"] = Body(..., embed=True)`,
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("items.py missing %q:\n%s", want, routes)
		}
	}
}

func TestPython_TypeAndAliasDeclarations(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
alias Code = string min(3) max(8)
alias Email = string format(email)
enum Tier { free pro }

type Address {
  street string required
  zip    Code optional
  tier   Tier required
}

POST /api/address {
  <- address Address required
  <- code Code required
  -> 200 { street: address.street }
}
`)
	types := readPy(t, outDir, "src/models/types.py")
	for _, want := range []string{
		"from src.models.enums import Tier",
		"Code = Annotated[str, Field(min_length=3, max_length=8)]",
		"Email = formats.Email",
		"class Address(BaseModel):",
		"    street: str",
		"    zip: Optional[Code] = None",
		"    tier: Tier",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.py missing %q:\n%s", want, types)
		}
	}
	if strings.Index(types, "Code = ") > strings.Index(types, "class Address") {
		t.Errorf("aliases must be defined before the types that use them:\n%s", types)
	}
	if !strings.Contains(types, "from src.lib import formats") {
		t.Errorf("types.py should import formats for the email alias:\n%s", types)
	}
	routes := readPy(t, outDir, "src/routes/address.py")
	for _, want := range []string{
		"from src.models.types import Address, Code",
		"address: Address = Body(..., embed=True)",
		"code: Code = Body(..., embed=True, min_length=3, max_length=8)",
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("address.py missing %q:\n%s", want, routes)
		}
	}
	if !strings.Contains(types, "from pydantic import BaseModel, Field") {
		t.Errorf("Field import missing:\n%s", types)
	}
	if !strings.Contains(types, "from typing import Annotated, Optional") {
		t.Errorf("typing import should list Annotated and Optional:\n%s", types)
	}
}

func TestPython_AliasBoundsMergeIntoQueryDefault(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
alias Code = string min(3)
alias Short = Code max(5) min(4)

GET /api/codes {
  <- code Short required
  -> 200 { code: code }
}
`)
	routes := readPy(t, outDir, "src/routes/codes.py")
	// FastAPI ignores Annotated metadata when the parameter has a Query default,
	// so the bounds must appear in the Query call, merged without duplicates.
	if !strings.Contains(routes, "code: Short = Query(..., max_length=5, min_length=4)") {
		t.Errorf("alias bounds should be merged into Query(...):\n%s", routes)
	}
}

func TestPython_DeclarationsStillFailClosed(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"struct enum": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
enum Plan {
  free { limit: 10 }
  pro  { limit: 100 }
}
GET /api/x { -> 200 "ok" }
`,
			want: "struct `enum Plan` with variant bodies",
		},
		"alias of type declaration": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
type Point { x int required }
alias P = Point
GET /api/x { -> 200 "ok" }
`,
			want: `alias of type declaration "Point"`,
		},
		"recursive type": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
type Node { child Node optional }
GET /api/x { -> 200 "ok" }
`,
			want: "recursive type declarations",
		},
		"mutually recursive aliases": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
alias A = B
alias B = A
GET /api/x { -> 200 "ok" }
`,
			want: "recursive type declarations",
		},
		"type field referencing a model": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
model thing { id uuid primary }
type Holder { thing thing required }
GET /api/x { -> 200 "ok" }
`,
			want: `undeclared or model type "thing"`,
		},
		"type field with default": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
type Opts { retries int default(3) }
GET /api/x { -> 200 "ok" }
`,
			want: `constraint "default"`,
		},
		"alias over a list model field": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
alias Tags = string
model thing { id uuid primary tags list(Tags) required }
GET /api/x { -> 200 "ok" }
`,
			want: "enum or named types inside list/map model fields",
		},
		"primary key typed by an alias": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
alias Key = uuid
model thing { id Key primary }
GET /api/x { -> 200 "ok" }
`,
			want: `primary key typed by alias "Key"`,
		},
		"type model field": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
type Addr { street string required }
model thing { id uuid primary addr Addr required }
GET /api/x { -> 200 "ok" }
`,
			want: `type-typed model field "Addr"`,
		},
		"json over a declared type on a model": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
type Addr { street string required }
model thing { id uuid primary addr json<Addr> required }
GET /api/x { -> 200 "ok" }
`,
			want: "typed `json<T>` model fields",
		},
		"enum default that is not a variant": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
enum Mode { fast slow }
GET /api/x {
  <- mode Mode default(turbo)
  -> 200 { mode: mode }
}
`,
			want: "endpoint input default expression",
		},
		"typed json over a primitive": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node }
POST /api/x {
  <- payload json<string> required
  -> 200 "ok"
}
`,
			want: "typed `json<T>` endpoint inputs",
		},
		"inline enum model default not a variant": {
			src: `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
model thing { id uuid primary kind enum(a, b) default(c) }
GET /api/x { -> 200 "ok" }
`,
			want: "is not a variant of the field's enum",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(tc.src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected a rejection containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestPython_AliasModelFieldUsesScalarColumnAndAliasAnnotation(t *testing.T) {
	outDir := buildPython(t, declsHeader+`alias Code = string min(3)
model thing {
  id   uuid primary
  code Code required
}
GET /api/things {
  -> 200 "ok"
}
`)
	schema := readPy(t, outDir, "src/models/schema.py")
	if !strings.Contains(schema, "code: Mapped[str] = mapped_column(String(), nullable=False)") {
		t.Errorf("alias model field should store as its scalar:\n%s", schema)
	}
	if strings.Contains(schema, "src.models.types") {
		t.Errorf("schema.py does not need the alias import:\n%s", schema)
	}
	pyd := readPy(t, outDir, "src/models/pydantic.py")
	if !strings.Contains(pyd, "from src.models.types import Code") || !strings.Contains(pyd, "    code: Code\n") {
		t.Errorf("read model should use the alias annotation:\n%s", pyd)
	}
	readPy(t, outDir, "src/models/types.py")
}

func TestPython_TypedJSONOverDeclaredTypeIsAClassInput(t *testing.T) {
	outDir := buildPython(t, `blueprint "x" { version "1.0" port 3000 runtime node }
type Address {
  street string required
}
POST /api/address/json {
  <- payload json<Address> required
  -> 200 { street: payload.street }
}
`)
	routes := readPy(t, outDir, "src/routes/address.py")
	for _, want := range []string{
		"from src.models.types import Address",
		"payload: Address = Body(..., embed=True)",
		`jsonable_encoder({"street": payload.street})`,
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("route missing %q:\n%s", want, routes)
		}
	}
}

func TestPython_ScalarAliasModelFieldsImportTheirTypes(t *testing.T) {
	outDir := buildPython(t, declsHeader+`alias Stamp = timestamp
alias Owner = uuid
model thing {
  id      uuid primary
  owner   Owner required
  created Stamp required
}
GET /api/things {
  -> 200 "ok"
}
`)
	schema := readPy(t, outDir, "src/models/schema.py")
	for _, want := range []string{"import uuid", "from datetime import datetime", "owner: Mapped[uuid.UUID]", "created: Mapped[datetime]"} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema.py missing %q:\n%s", want, schema)
		}
	}
}

func TestPython_InlineEnumModelColumnIsNamed(t *testing.T) {
	outDir := buildPython(t, declsHeader+`model job {
  id   uuid primary
  kind enum(a, b) default(a)
}
GET /api/jobs {
  -> 200 "ok"
}
`)
	schema := readPy(t, outDir, "src/models/schema.py")
	// Postgres cannot compile an unnamed ENUM; the name follows Node's pgEnum rule.
	if !strings.Contains(schema, `Enum("a", "b", name="jobkind")`) {
		t.Errorf("inline enum column must be named:\n%s", schema)
	}
}

func TestPython_MemberNamesThatBreakAtRuntimeAreRejected(t *testing.T) {
	cases := map[string]string{
		"enum member mro":            `blueprint "x" { version "1.0" port 3000 runtime node }` + "\nenum Mode { mro fast }\nGET /api/x { -> 200 \"ok\" }\n",
		"type field with underscore": `blueprint "x" { version "1.0" port 3000 runtime node }` + "\ntype T { _secret string required }\nGET /api/x { -> 200 \"ok\" }\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			if err := python.New().Generate(file, t.TempDir()); err == nil {
				t.Fatalf("expected a rejection")
			}
		})
	}
}

func TestPython_ContractTestsSampleEnumAndAliasValues(t *testing.T) {
	file, errs := parser.ParseFile("t.bp", []byte(declsHeader+`enum Mode { fast slow }
alias Code = string min(3)
model thing {
  id   uuid primary
  mode Mode required
}
POST /api/things {
  <- mode Mode required
  <- code Code required
  -> 201 { mode: mode }
}
`))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	outDir := t.TempDir()
	if err := python.New().WithGenTests(true).Generate(file, outDir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	body := readPy(t, outDir, "tests/test_things.py")
	if !strings.Contains(body, `"mode": "fast"`) {
		t.Errorf("contract tests should send the first enum variant:\n%s", body)
	}
	if !strings.Contains(body, `"code": "test"`) {
		t.Errorf("alias sample should satisfy min(3) through the wrapped string sample:\n%s", body)
	}
}
