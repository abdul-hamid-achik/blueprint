// Package codegen provides the interface for code generation from Blueprint AST.
package codegen

import "github.com/abdul-hamid-achik/blueprint/internal/ast"

// Generator is implemented by all code generation targets (JS, etc.).
type Generator interface {
	// Generate takes a parsed+checked AST and writes output files to outDir.
	Generate(file *ast.File, outDir string) error
}

// OutputFile represents a single generated file.
type OutputFile struct {
	Path    string // relative path within the output directory
	Content []byte
}
