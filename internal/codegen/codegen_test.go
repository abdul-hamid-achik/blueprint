package codegen

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

func TestRejectUnresolvedGenerateSteps(t *testing.T) {
	loc := lexer.Loc{File: "service.bp", Line: 8, Col: 3}
	file := &ast.File{
		Loc: loc,
		Blocks: []ast.TopLevel{
			&ast.Endpoint{Loc: loc, Stmts: []ast.ArrowStmt{
				&ast.GenerateStep{Loc: loc, Text: "add validation"},
			}},
			&ast.TestGroup{Loc: loc, SharedSetup: []ast.ArrowStmt{
				&ast.GenerateStep{Loc: lexer.Loc{File: "service.bp", Line: 20, Col: 5}, Text: "seed data"},
			}},
		},
	}

	err := RejectUnresolvedGenerateSteps(file)
	if err == nil {
		t.Fatal("expected unresolved generation slots to be rejected")
	}
	for _, want := range []string{"2 found", "service.bp:8:3", "bp generate service.bp --write"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q; got %q", want, err)
		}
	}
}

func TestRejectUnresolvedGenerateStepsAllowsResolvedFile(t *testing.T) {
	file := &ast.File{Blocks: []ast.TopLevel{
		&ast.Endpoint{Stmts: []ast.ArrowStmt{&ast.OutputStmt{}}},
	}}
	if err := RejectUnresolvedGenerateSteps(file); err != nil {
		t.Fatalf("resolved file should pass: %v", err)
	}
}
