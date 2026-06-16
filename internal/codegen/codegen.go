// Package codegen provides the interface for code generation from Blueprint AST.
package codegen

import "github.com/abdul-hamid-achik/blueprint/internal/ast"

// Generator is implemented by all code generation targets (JS, Python, ...).
//
// The target-agnostic contract is Files: a generator turns a parsed+checked
// AST into a set of in-memory OutputFiles without touching disk. Persistence
// (manifest tracking, UserOwned scaffolding, stale cleanup) is the shared
// responsibility of WriteOutputFiles, so every target gets identical on-disk
// behavior for free. Generate is a convenience that wires the two together.
//
// New targets should implement Files and embed the standard Generate via the
// pattern in the existing js/python generators; this keeps emit logic pure and
// unit-testable (assert on returned files, no temp dir required).
type Generator interface {
	// Files returns the generated output files for a parsed+checked AST.
	// It must not write to disk. A non-nil error means the target cannot
	// generate for this AST (e.g. an unsupported feature); callers surface it.
	Files(file *ast.File) ([]OutputFile, error)
	// Generate builds Files and persists them to outDir via WriteOutputFiles.
	Generate(file *ast.File, outDir string) error
}

// OutputFile represents a single generated file.
type OutputFile struct {
	Path      string // relative path within the output directory
	Content   []byte
	UserOwned bool // scaffold if missing, then leave untouched on later builds
}
