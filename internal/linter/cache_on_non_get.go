package linter

import (
	"fmt"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// checkCacheOnNonGet flags `cache` metadata on non-GET endpoints. The Node
// target only sets Cache-Control on REST GET responses, so the directive has
// no effect on other methods or on STREAM and WS endpoints, whose responses
// are long-lived. The build still succeeds, so this is a warning, not an error.
func checkCacheOnNonGet(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		var method, path string
		var metas []*ast.EndpointMeta
		switch ep := block.(type) {
		case *ast.Endpoint:
			if ep.Method == "GET" {
				continue
			}
			method, path, metas = ep.Method, ep.Path, ep.Meta
		case *ast.StreamEndpoint:
			method, path, metas = "STREAM", ep.Path, ep.Meta
		case *ast.WsEndpoint:
			method, path, metas = "WS", ep.Path, ep.Meta
		default:
			continue
		}
		for _, meta := range metas {
			if meta.Kind != "cache" {
				continue
			}
			issues = append(issues, Issue{
				File:    meta.Loc.File,
				Line:    meta.Loc.Line,
				Col:     meta.Loc.Col,
				Level:   "warning",
				Rule:    "cache-on-non-get",
				Message: fmt.Sprintf("cache is ignored on %s %s: Cache-Control is only set on REST GET responses", method, path),
				Hint:    "Move `cache` to a GET endpoint, or remove it",
			})
		}
	}

	return issues
}
