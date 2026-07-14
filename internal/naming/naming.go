// Package naming contains target-neutral identifier naming helpers shared by
// semantic analysis and code generation.
package naming

import "strings"

// Pluralize returns a naive English plural. It handles a small set of
// irregulars, all-caps acronyms, and common English suffix rules.
func Pluralize(s string) string {
	irregulars := map[string]string{
		"person": "people",
		"man":    "men",
		"woman":  "women",
		"child":  "children",
		"tooth":  "teeth",
		"foot":   "feet",
		"mouse":  "mice",
		"goose":  "geese",
	}
	lower := strings.ToLower(s)
	if plural, ok := irregulars[lower]; ok {
		if len(s) > 0 && s[0] >= 'A' && s[0] <= 'Z' {
			return strings.ToUpper(plural[:1]) + plural[1:]
		}
		return plural
	}
	if strings.ToUpper(s) == s && len(s) > 1 {
		return s + "s"
	}
	if strings.HasSuffix(s, "s") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "z") ||
		strings.HasSuffix(s, "sh") || strings.HasSuffix(s, "ch") {
		return s + "es"
	}
	if strings.HasSuffix(s, "y") && len(s) > 1 {
		prev := s[len(s)-2]
		if prev != 'a' && prev != 'e' && prev != 'i' && prev != 'o' && prev != 'u' {
			return s[:len(s)-1] + "ies"
		}
	}
	return s + "s"
}
