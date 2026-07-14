// Package common holds pure, target-agnostic helpers shared by every code
// generator (today JS, soon Python). Anything in this package must:
//
//   - depend only on the standard library and internal/ast
//   - hold no per-target conventions (e.g. file extensions, framework names)
//   - be testable as a pure function
//
// Generators import this package; nothing in common imports a generator.
package common

import (
	"strings"

	"github.com/abdul-hamid-achik/blueprint/internal/naming"
)

// CamelCase converts snake_case (or kebab-case) to camelCase.
//
//	"my_field"   -> "myField"
//	"rooms-stream" -> "roomsStream"
func CamelCase(s string) string {
	s = strings.ReplaceAll(s, "-", "_")
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// PascalCase converts snake_case or kebab-case to PascalCase.
//
//	"my_model"     -> "MyModel"
//	"user-profile" -> "UserProfile"
func PascalCase(s string) string {
	s = strings.ReplaceAll(s, "-", "_")
	parts := strings.Split(s, "_")
	for i := range parts {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// KebabCase converts snake_case to kebab-case.
//
//	"my_service" -> "my-service"
func KebabCase(s string) string {
	return strings.ReplaceAll(s, "_", "-")
}

// SnakeCase converts camelCase or PascalCase to snake_case. Idempotent on
// already-snake input. Useful for Python codegen where the .bp source name
// must be lowercased and underscore-joined.
//
//	"myField"  -> "my_field"
//	"MyModel"  -> "my_model"
//	"my_field" -> "my_field"
func SnakeCase(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Pluralize returns a naive English plural. Handles a small set of
// irregulars (person/people, man/men, ...), all-caps acronyms (API/APIs),
// and the common suffix rules (s/x/z/sh/ch → +es; consonant+y → +ies).
func Pluralize(s string) string {
	return naming.Pluralize(s)
}
