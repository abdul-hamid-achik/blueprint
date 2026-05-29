package common

import "strings"

// ExtractResource returns the first non-"api", non-param path segment of an
// endpoint path — the "resource" used to group endpoints into route files.
//
//	"/api/watermark"   -> "watermark"
//	"/api/jobs/:id"    -> "jobs"
//	"/api/cart/items"  -> "cart"
func ExtractResource(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, p := range parts {
		if p == "api" || p == "" {
			continue
		}
		if strings.HasPrefix(p, ":") {
			continue
		}
		return p
	}
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "root"
}

// IsPathParam reports whether name appears as a :name segment in path.
func IsPathParam(name, path string) bool {
	return strings.Contains(path, ":"+name)
}

// ExtractPathParams returns the ordered list of :name segments in path.
//
//	"/api/rooms/:id"  -> ["id"]
//	"/ws/:org/:room"  -> ["org", "room"]
func ExtractPathParams(path string) []string {
	var params []string
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, ":") {
			params = append(params, seg[1:])
		}
	}
	return params
}
