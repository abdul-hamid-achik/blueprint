package python

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/codegen"
)

// pythonFormatTypes maps each Blueprint `format(...)` kind the Python target
// enforces to the str subclass in src/lib/formats.py that validates it. Every
// kind the checker accepts (C052) has an entry, so an unknown kind is rejected
// by the generator gate instead of being silently dropped.
//
// The subclass, not typing.Annotated[str, AfterValidator(...)], carries the
// check: FastAPI keeps only the base type of an annotation when the parameter
// default is a Body(...)/Query(...) FieldInfo, which would discard Annotated
// validators. A subclass survives that path, and its runtime values are plain
// str because the validator returns its input unchanged.
var pythonFormatTypes = map[string]string{
	"email": "Email",
	"url":   "Url",
	"uuid":  "Uuid",
	"ip":    "IpAddress",
	"date":  "Date",
}

// pythonFormatsModule is the import alias for src/lib/formats.py in generated
// routes and models, so the format type names never collide with user names.
const pythonFormatsModule = "formats"

// formatKind returns the name of a format(...) constraint value. The parser
// produces an Ident (`format(email)`); a quoted form is accepted defensively.
func formatKind(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StringLit:
		return v.Value
	}
	return ""
}

// formatTypeName returns the formats.py type for one format constraint on a
// value of type t. Only string and text values carry formats; anything else,
// or an unknown kind, reports ok=false.
func formatTypeName(t ast.TypeExpr, c *ast.Constraint_) (string, bool) {
	if c == nil || c.Kind != "format" {
		return "", false
	}
	p, ok := t.(*ast.PrimitiveType)
	if !ok || (p.Name != "string" && p.Name != "text") {
		return "", false
	}
	name, ok := pythonFormatTypes[formatKind(c.Value)]
	return name, ok
}

// formatTypeFor returns the formats.py type name for the first format
// constraint in cs that applies to t, or "" when none does.
func formatTypeFor(t ast.TypeExpr, cs []*ast.Constraint_) string {
	for _, c := range cs {
		if name, ok := formatTypeName(t, c); ok {
			return name
		}
	}
	return ""
}

// annotatedType renders the Python annotation for a value of type t. A format
// replaces the base type with the formats.py subclass; extra metadata (for
// example FastAPI's Path(...)) wraps the result in typing.Annotated.
func annotatedType(base string, t ast.TypeExpr, cs []*ast.Constraint_, extra ...string) string {
	if name := formatTypeFor(t, cs); name != "" {
		base = pythonFormatsModule + "." + name
	}
	if len(extra) == 0 {
		return base
	}
	return fmt.Sprintf("Annotated[%s, %s]", base, strings.Join(extra, ", "))
}

// formatFeature returns the reason a format constraint cannot be emitted, or
// "" when it is supported on t.
func formatFeature(t ast.TypeExpr, c *ast.Constraint_) string {
	if _, ok := formatTypeName(t, c); ok {
		return ""
	}
	return fmt.Sprintf("format(%s) constraint on %T", formatKind(c.Value), t)
}

// pythonUsesFormats reports whether any endpoint input or model field carries
// a format constraint, which is when src/lib/formats.py must be emitted.
func pythonUsesFormats(models []*ast.Model, endpoints []*ast.Endpoint) bool {
	for _, m := range models {
		for _, f := range m.Fields {
			if formatTypeFor(f.Type, f.Constraints) != "" {
				return true
			}
		}
	}
	for _, ep := range endpoints {
		for _, s := range ep.Stmts {
			if in, ok := s.(*ast.InputStmt); ok && formatTypeFor(in.Type, in.Constraints) != "" {
				return true
			}
		}
	}
	return false
}

// genFormatsPy emits src/lib/formats.py. Validators are stdlib only and return
// the value unchanged, so handlers still receive a plain str. A violation
// raises ValueError, which Pydantic and FastAPI report as a 422.
func (g *Generator) genFormatsPy() codegen.OutputFile {
	content := fileHeader(g.sourceFile) + `"""Validators for Blueprint format(...) constraints. Stdlib only."""

import ipaddress
import re
import urllib.parse
import uuid
from datetime import date

from pydantic_core import core_schema

_EMAIL_RE = re.compile(r"[^@\s]+@[^@\s]+\.[^@\s]+")
_UUID_RE = re.compile(
    r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
)
_DATE_RE = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}")


def validate_email(value: str) -> str:
    if not _EMAIL_RE.fullmatch(value):
        raise ValueError("must be a valid email address")
    return value


def validate_url(value: str) -> str:
    if any(ch.isspace() for ch in value):
        raise ValueError("must be a valid http or https URL")
    try:
        parts = urllib.parse.urlsplit(value)
    except ValueError as exc:
        raise ValueError("must be a valid http or https URL") from exc
    if parts.scheme not in ("http", "https") or not parts.hostname:
        raise ValueError("must be a valid http or https URL")
    return value


def validate_uuid(value: str) -> str:
    # The shape check keeps the canonical hyphenated form only; uuid.UUID alone
    # also accepts braced, urn: and unhyphenated spellings.
    if not _UUID_RE.fullmatch(value):
        raise ValueError("must be a valid UUID")
    try:
        uuid.UUID(value)
    except ValueError as exc:
        raise ValueError("must be a valid UUID") from exc
    return value


def validate_ip(value: str) -> str:
    try:
        ipaddress.ip_address(value)
    except ValueError as exc:
        raise ValueError("must be a valid IP address") from exc
    return value


def validate_date(value: str) -> str:
    if not _DATE_RE.fullmatch(value):
        raise ValueError("must be a date in YYYY-MM-DD form")
    try:
        date.fromisoformat(value)
    except ValueError as exc:
        raise ValueError("must be a date in YYYY-MM-DD form") from exc
    return value


def _validated_str(validator):
    return core_schema.no_info_after_validator_function(validator, core_schema.str_schema())


class Email(str):
    """A str that must be an email address."""

    @classmethod
    def __get_pydantic_core_schema__(cls, source_type, handler):
        return _validated_str(validate_email)


class Url(str):
    """A str that must be an absolute http or https URL."""

    @classmethod
    def __get_pydantic_core_schema__(cls, source_type, handler):
        return _validated_str(validate_url)


class Uuid(str):
    """A str that must be a canonical hyphenated UUID."""

    @classmethod
    def __get_pydantic_core_schema__(cls, source_type, handler):
        return _validated_str(validate_uuid)


class IpAddress(str):
    """A str that must be an IPv4 or IPv6 address."""

    @classmethod
    def __get_pydantic_core_schema__(cls, source_type, handler):
        return _validated_str(validate_ip)


class Date(str):
    """A str that must be a calendar date in YYYY-MM-DD form."""

    @classmethod
    def __get_pydantic_core_schema__(cls, source_type, handler):
        return _validated_str(validate_date)
`
	return codegen.OutputFile{Path: "src/lib/formats.py", Content: []byte(content)}
}
