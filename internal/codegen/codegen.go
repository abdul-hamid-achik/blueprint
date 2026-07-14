// Package codegen provides the interface for code generation from Blueprint AST.
package codegen

import (
	"fmt"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

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

// RejectUnresolvedGenerateSteps prevents a successful build from silently
// dropping an @> slot. Generation slots are source-rewrite placeholders, not
// runtime statements; callers must resolve them with `bp generate --write`
// before asking any target generator for project files.
func RejectUnresolvedGenerateSteps(file *ast.File) error {
	if file == nil {
		return nil
	}
	visitor := &generateStepVisitor{}
	ast.Walk(file, visitor)
	if visitor.count == 0 {
		return nil
	}
	plural := "slot"
	if visitor.count != 1 {
		plural = "slots"
	}
	return fmt.Errorf(
		"unresolved @> generation %s (%d found; first at %s): run `bp generate %s --write`, review the source edit, then run `bp check` before building",
		plural, visitor.count, visitor.first.String(), visitor.first.File,
	)
}

type generateStepVisitor struct {
	ast.BaseVisitor
	count int
	first lexer.Loc
}

func (v *generateStepVisitor) VisitGenerateStep(node *ast.GenerateStep) bool {
	if v.count == 0 {
		v.first = node.Loc
	}
	v.count++
	return false
}
