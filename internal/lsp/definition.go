package lsp

import (
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// computeDefinition returns the LSP Location (or nil if none found) for a
// definition jump at (line, char). The returned location is a single map ready
// to be marshalled as the JSON-RPC result.
func computeDefinition(uri string, idx *docIndex, line, char int) map[string]interface{} {
	if idx == nil {
		return nil
	}
	sym := findSymbolAt(idx, line, char)
	var target *lexer.Loc

	switch sym.Kind {
	case SymbolModel:
		if m, ok := findModel(idx.file, sym.Name); ok {
			loc := m.Loc
			target = &loc
		}
	case SymbolFn:
		if fn := findFn(idx.file, sym.Name); fn != nil {
			loc := fn.Loc
			target = &loc
		}
	case SymbolPipe:
		if p := findPipe(idx.file, sym.Name); p != nil {
			loc := p.Loc
			target = &loc
		}
	case SymbolMiddleware:
		if m := findMiddleware(idx.file, sym.Name); m != nil {
			loc := m.Loc
			target = &loc
		}
	case SymbolField:
		if m, ok := findModel(idx.file, sym.Parent); ok {
			if f, ok := findField(m, sym.Name); ok {
				loc := f.Loc
				target = &loc
			} else if f, ok := findComputedField(m, sym.Name); ok {
				loc := f.Loc
				target = &loc
			}
		}
	}

	if target == nil {
		return nil
	}
	return map[string]interface{}{
		"uri":   uri,
		"range": declarationRange(idx.text, *target, sym.Name),
	}
}
