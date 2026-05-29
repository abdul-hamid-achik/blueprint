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
	var nameLen int

	switch sym.Kind {
	case SymbolModel:
		if m, ok := findModel(idx.file, sym.Name); ok {
			loc := m.Loc
			target = &loc
			nameLen = len(sym.Name)
		}
	case SymbolFn:
		if fn := findFn(idx.file, sym.Name); fn != nil {
			loc := fn.Loc
			target = &loc
			nameLen = len(sym.Name)
		}
	case SymbolPipe:
		if p := findPipe(idx.file, sym.Name); p != nil {
			loc := p.Loc
			target = &loc
			nameLen = len(sym.Name)
		}
	case SymbolMiddleware:
		if m := findMiddleware(idx.file, sym.Name); m != nil {
			loc := m.Loc
			target = &loc
			nameLen = len(sym.Name)
		}
	case SymbolField:
		if m, ok := findModel(idx.file, sym.Parent); ok {
			if f, ok := findField(m, sym.Name); ok {
				loc := f.Loc
				target = &loc
				nameLen = len(sym.Name)
			}
		}
	}

	if target == nil {
		return nil
	}
	// Force-stamp Len so the highlight covers the declaration name.
	loc := *target
	if loc.Len <= 0 {
		loc.Len = nameLen
	}
	if loc.Len <= 0 {
		loc.Len = 1
	}
	return map[string]interface{}{
		"uri":   uri,
		"range": locToRange(loc),
	}
}
