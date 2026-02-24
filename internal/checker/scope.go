package checker

import (
	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

// SymbolKind identifies what kind of declaration a symbol represents.
type SymbolKind int

const (
	SymModel SymbolKind = iota
	SymFn
	SymPipe
	SymMiddleware
	SymEnum
	SymType
	SymAlias
	SymExternal
	SymVariable
	SymInput
	SymSecret
	SymEnv
)

func (k SymbolKind) String() string {
	names := [...]string{
		"model", "fn", "pipe", "middleware", "enum", "type", "alias",
		"external", "variable", "input", "secret", "env",
	}
	if int(k) < len(names) {
		return names[k]
	}
	return "unknown"
}

// Symbol represents a named declaration in a scope.
type Symbol struct {
	Name string
	Kind SymbolKind
	Loc  lexer.Loc
	Node ast.Node
}

// Scope represents a lexical scope for name resolution.
type Scope struct {
	parent  *Scope
	symbols map[string]*Symbol
}

// NewScope creates a new scope with an optional parent.
func NewScope(parent *Scope) *Scope {
	return &Scope{
		parent:  parent,
		symbols: make(map[string]*Symbol),
	}
}

// Define adds a symbol to this scope. Returns the existing symbol if already defined.
func (s *Scope) Define(sym *Symbol) *Symbol {
	if existing, ok := s.symbols[sym.Name]; ok {
		return existing
	}
	s.symbols[sym.Name] = sym
	return nil
}

// Lookup searches for a symbol in this scope and parent scopes.
func (s *Scope) Lookup(name string) *Symbol {
	if sym, ok := s.symbols[name]; ok {
		return sym
	}
	if s.parent != nil {
		return s.parent.Lookup(name)
	}
	return nil
}

// NamesOfKind returns all symbol names of the given kind in this scope and parent scopes.
func (s *Scope) NamesOfKind(kind SymbolKind) []string {
	var names []string
	s.collectNamesOfKind(kind, &names, make(map[string]bool))
	return names
}

func (s *Scope) collectNamesOfKind(kind SymbolKind, names *[]string, seen map[string]bool) {
	for _, sym := range s.symbols {
		if sym.Kind == kind && !seen[sym.Name] {
			*names = append(*names, sym.Name)
			seen[sym.Name] = true
		}
	}
	if s.parent != nil {
		s.parent.collectNamesOfKind(kind, names, seen)
	}
}
