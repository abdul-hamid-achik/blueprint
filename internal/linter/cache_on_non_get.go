package linter

import (
	"fmt"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
)

// checkCacheOnNonGet flags `cache` metadata on non-GET endpoints. The Node
// target only sets Cache-Control on GET responses, so the directive has no
// effect there. The build still succeeds, so this is a warning, not an error.
func checkCacheOnNonGet(f *ast.File) []Issue {
	var issues []Issue

	for _, block := range f.Blocks {
		ep, ok := block.(*ast.Endpoint)
		if !ok || ep.Method == "GET" {
			continue
		}
		for _, meta := range ep.Meta {
			if meta.Kind != "cache" {
				continue
			}
			issues = append(issues, Issue{
				File:    meta.Loc.File,
				Line:    meta.Loc.Line,
				Col:     meta.Loc.Col,
				Level:   "warning",
				Rule:    "cache-on-non-get",
				Message: fmt.Sprintf("cache is ignored on %s %s: Cache-Control is only set on GET responses", ep.Method, ep.Path),
				Hint:    "Move `cache` to a GET endpoint, or remove it",
			})
		}
	}

	return issues
}
