package resolve

import "github.com/abdul-hamid-achik/blueprint/internal/ast"

// AsyncFunctions returns the .bp source names (raw, snake_case) of every fn
// and pipe declared in the file. Calls to these names from arrow-statement
// contexts should be awaited in async-capable target languages (JavaScript:
// `await foo()`; Python with async drivers: `await foo()`; sync targets:
// ignore).
//
// Target-specific built-ins (e.g. JS's `upload`, `deleteS3Object`) are NOT
// included — the code generator contributes those because the name itself
// is target-specific (`upload` vs `upload_file`, etc.).
func AsyncFunctions(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, b := range file.Blocks {
		switch n := b.(type) {
		case *ast.Fn:
			out[n.Name] = true
		case *ast.Pipe:
			out[n.Name] = true
		}
	}
	return out
}
