package python_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/codegen/python"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

func TestPython_PathParamConstraintsEmitPathValidation(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
GET /api/items/:slug {
  <- slug string required max(5)
  -> 200 { slug: slug }
}
GET /api/pages/:slug/:n {
  <- slug string required min(2) max(40)
  <- n    int    required min(1)
  -> 200 { slug: slug, n: n }
}
GET /api/keys/:key {
  <- key string required format(uuid) max(36)
  -> 200 { key: key }
}
`
	outDir := buildPython(t, src)
	items := readPy(t, outDir, "src/routes/items.py")
	for _, want := range []string{
		"from fastapi import APIRouter, Path",
		"from typing import Annotated",
		"async def get_items_slug(slug: Annotated[str, Path(max_length=5)]):",
	} {
		if !strings.Contains(items, want) {
			t.Errorf("items.py missing %q, got:\n%s", want, items)
		}
	}
	pages := readPy(t, outDir, "src/routes/pages.py")
	for _, want := range []string{
		"slug: Annotated[str, Path(min_length=2, max_length=40)]",
		"n: Annotated[int, Path(ge=1)]",
	} {
		if !strings.Contains(pages, want) {
			t.Errorf("pages.py missing %q, got:\n%s", want, pages)
		}
	}
	keys := readPy(t, outDir, "src/routes/keys.py")
	if !strings.Contains(keys, "key: Annotated[formats.Uuid, Path(max_length=36)]") {
		t.Errorf("keys.py path format+length wrong, got:\n%s", keys)
	}
}

func TestPython_UnconstrainedPathParamKeepsPlainAnnotation(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	hello := readPy(t, outDir, "src/routes/hello.py")
	if !strings.Contains(hello, "name: str") || strings.Contains(hello, "Path(") {
		t.Errorf("unconstrained path param should stay plain, got:\n%s", hello)
	}
	if strings.Contains(hello, "Annotated") {
		t.Errorf("no Annotated import expected without constraints, got:\n%s", hello)
	}
}

func TestPython_FormatEmitsTypedInputsForEveryKind(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
POST /api/echo {
  <- email string required format(email)
  <- url   string required format(url)
  <- uuid  string required format(uuid)
  <- ip    string required format(ip)
  <- date  string required format(date)
  -> 200 { email: email, url: url, uuid: uuid, ip: ip, date: date }
}
`
	outDir := buildPython(t, src)
	echo := readPy(t, outDir, "src/routes/echo.py")
	for _, want := range []string{
		"from src.lib import formats",
		"email: formats.Email = Body(..., embed=True)",
		"url: formats.Url = Body(..., embed=True)",
		"uuid: formats.Uuid = Body(..., embed=True)",
		"ip: formats.IpAddress = Body(..., embed=True)",
		"date: formats.Date = Body(..., embed=True)",
	} {
		if !strings.Contains(echo, want) {
			t.Errorf("echo.py missing %q, got:\n%s", want, echo)
		}
	}
	if strings.Contains(echo, "Annotated") {
		t.Errorf("format-only inputs should not need Annotated, got:\n%s", echo)
	}
}

func TestPython_FormatOnOptionalAndQueryInputs(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
POST /api/notify {
  <- ip   string optional format(ip)
  <- mail string required format(email)
  -> 200 { ip: ip, mail: mail }
}
GET /api/days {
  <- day string optional format(date)
  -> 200 { day: day }
}
`
	outDir := buildPython(t, src)
	notify := readPy(t, outDir, "src/routes/notify.py")
	if !strings.Contains(notify, "ip: formats.IpAddress | None = Body(None, embed=True)") {
		t.Errorf("optional format input wrong, got:\n%s", notify)
	}
	days := readPy(t, outDir, "src/routes/days.py")
	if !strings.Contains(days, "day: formats.Date | None = Query(None)") {
		t.Errorf("optional query format input wrong, got:\n%s", days)
	}
}

func TestPython_FormatOnModelFieldsEmitsPydanticTypes(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
model contact {
  id    uuid   primary
  email string required format(email)
  site  string optional format(url)
  ref   string optional
}
GET /api/x { -> 200 "ok" }
`
	outDir := buildPython(t, src)
	body := readPy(t, outDir, "src/models/pydantic.py")
	for _, want := range []string{
		"from src.lib import formats",
		"email: formats.Email",
		"site: Optional[formats.Url]",
		"ref: Optional[str]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pydantic.py missing %q, got:\n%s", want, body)
		}
	}
	// SQLAlchemy columns are not affected by Pydantic-side formats.
	schema := readPy(t, outDir, "src/models/schema.py")
	if strings.Contains(schema, "formats") {
		t.Errorf("schema.py should not reference formats, got:\n%s", schema)
	}
}

func TestPython_FormatsModuleStdlibValidators(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
POST /api/x { <- ip string required format(ip)  -> 200 { ip: ip } }
`
	outDir := buildPython(t, src)
	mod := readPy(t, outDir, "src/lib/formats.py")
	for _, want := range []string{
		"def validate_email(value: str) -> str:",
		"def validate_url(value: str) -> str:",
		"def validate_uuid(value: str) -> str:",
		"def validate_ip(value: str) -> str:",
		"def validate_date(value: str) -> str:",
		"uuid.UUID(value)",
		"ipaddress.ip_address(value)",
		"date.fromisoformat(value)",
		"urllib.parse.urlsplit(value)",
		`_EMAIL_RE = re.compile(r"[^@\s]+@[^@\s]+\.[^@\s]+")`,
		"class Email(str):",
		"class Url(str):",
		"class Uuid(str):",
		"class IpAddress(str):",
		"class Date(str):",
	} {
		if !strings.Contains(mod, want) {
			t.Errorf("formats.py missing %q, got:\n%s", want, mod)
		}
	}
	if strings.Contains(mod, "email_validator") || strings.Contains(mod, "EmailStr") {
		t.Errorf("formats.py must not depend on email-validator")
	}
}

func TestPython_FormatsModuleOnlyWhenUsed(t *testing.T) {
	outDir := buildPython(t, helloWorldSrc)
	if _, err := os.Stat(filepath.Join(outDir, "src/lib/formats.py")); err == nil {
		t.Errorf("src/lib/formats.py should not be emitted without format constraints")
	}
}

func TestPython_UnsupportedFormatsAreRejected(t *testing.T) {
	cases := map[string]string{
		"unknown endpoint input format": `blueprint "x" { version "1.0" port 3000 runtime node }
POST /api/x { <- code string required format(isbn)  -> 200 { code: code } }
`,
		"unknown model field format": `blueprint "x" { version "1.0" port 3000 runtime node database postgres }
model contact { id uuid primary  code string required format(isbn) }
GET /api/x { -> 200 "ok" }
`,
		"format on int endpoint input": `blueprint "x" { version "1.0" port 3000 runtime node }
POST /api/x { <- n int required format(email)  -> 200 { n: n } }
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			file, errs := parser.ParseFile("t.bp", []byte(src))
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			err := python.New().Generate(file, t.TempDir())
			if err == nil {
				t.Fatalf("expected unsupported-feature error for %s", name)
			}
			if !strings.Contains(err.Error(), "python target does not yet support") {
				t.Errorf("error should explain the gate: %v", err)
			}
		})
	}
}

func TestPython_GenTestsSampleValuesSatisfyFormats(t *testing.T) {
	src := `blueprint "x" { version "1.0" port 3000 runtime node }
POST /api/notify {
  <- ip   string required format(ip)
  <- day  string required format(date)
  <- site string required format(url)
  -> 200 { ip: ip, day: day, site: site }
}
`
	outDir := buildPythonGenTests(t, src)
	var all strings.Builder
	entries, err := os.ReadDir(filepath.Join(outDir, "tests"))
	if err != nil {
		t.Fatalf("read tests dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".py") {
			all.WriteString(readPy(t, outDir, filepath.Join("tests", e.Name())))
		}
	}
	for _, want := range []string{`"127.0.0.1"`, `"2026-01-01"`, `"https://example.com"`} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("contract tests missing sample %s", want)
		}
	}
}
