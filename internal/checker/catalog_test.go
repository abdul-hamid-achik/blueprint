package checker

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/diag"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

var catalogHeading = regexp.MustCompile(`^### (C\d{3}) `)

// catalogExamples returns the first ```bp block of every C-code section in the
// error catalog. By convention that block is the minimal failing example.
func catalogExamples(t *testing.T, md string) map[string]string {
	t.Helper()
	examples := map[string]string{}
	lines := strings.Split(md, "\n")
	code := ""
	inBlock := false
	var block []string
	for _, line := range lines {
		if inBlock {
			if line == "```" {
				inBlock = false
				if code != "" {
					if _, seen := examples[code]; !seen {
						examples[code] = strings.Join(block, "\n") + "\n"
					}
				}
				continue
			}
			block = append(block, line)
			continue
		}
		if m := catalogHeading.FindStringSubmatch(line); m != nil {
			code = m[1]
			continue
		}
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "---") {
			code = ""
			continue
		}
		if line == "```bp" && code != "" {
			inBlock = true
			block = nil
		}
	}
	return examples
}

// checkerCodeConstants returns every `Code... = "Cnnn"` constant declared in
// checker.go, so the catalog is held to the full set of checker codes.
func checkerCodeConstants(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, "checker.go", nil, 0)
	if err != nil {
		t.Fatalf("parse checker.go: %v", err)
	}
	var codes []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Code") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				code, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", name.Name, err)
				}
				codes = append(codes, code)
			}
		}
	}
	sort.Strings(codes)
	return codes
}

// sourceUnreachableCodes are checker codes that no `.bp` source can trigger
// because the parser rejects the input first. Their catalog sections document
// the guard without a failing example. Keep this list minimal and justified.
var sourceUnreachableCodes = map[string]string{
	// The parser always reads an identifier after `call`, so a call node with a
	// missing or non-identifier service only comes from tooling-built ASTs.
	CodeInvalidCallTarget: "parser requires an identifier service after `call`",
}

// TestErrorCatalogExamplesProduceTheirCode is the drift guard for the error
// catalog: every checker code has a section, every section's first bp example
// really produces that code from the checker, and `bp explain` can find it.
func TestErrorCatalogExamplesProduceTheirCode(t *testing.T) {
	md, err := os.ReadFile("../diag/error-codes.md")
	if err != nil {
		t.Fatalf("read error catalog: %v", err)
	}
	examples := catalogExamples(t, string(md))

	for _, code := range checkerCodeConstants(t) {
		if _, found := diag.Lookup(code); !found {
			t.Errorf("bp explain cannot find %s", code)
		}
		src, ok := examples[code]
		if !ok {
			if _, unreachable := sourceUnreachableCodes[code]; unreachable {
				continue
			}
			t.Errorf("%s is declared in checker.go but has no failing ```bp example in internal/diag/error-codes.md", code)
			continue
		}

		// Every example except C001 must parse cleanly with the strict parser so
		// it cannot pass only because of recovery. C001 is the checker's code for
		// a file without a blueprint; the strict parser reports P001 for that
		// shape first, so its example goes through the tolerant entry point the
		// editor uses.
		if code != CodeMissingBlueprint {
			if _, errs := parser.ParseFile(code+".bp", []byte(src)); len(errs) > 0 {
				t.Errorf("%s example does not parse: %v\n%s", code, errs, src)
				continue
			}
		}
		file, _ := parser.ParsePartialFile(code+".bp", []byte(src))

		var got []string
		found := false
		for _, e := range Check(file) {
			got = append(got, e.Code+" "+e.Message)
			if e.Code == code {
				found = true
			}
		}
		if !found {
			t.Errorf("%s example did not produce %s; checker reported:\n%s\nexample:\n%s", code, code, strings.Join(got, "\n"), src)
		}
	}
}

// TestCheckerErrorsAlwaysCarryACode forbids uncoded checker diagnostics. Every
// error site must call addErrorCode with a named Code constant; a bare string
// or a missing code fails here before it can reach users without a catalog entry.
func TestCheckerErrorsAlwaysCarryACode(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read checker dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "addError":
				t.Errorf("%s: %s has an uncoded checker error; use addErrorCode with a Code constant", fset.Position(call.Pos()), name)
			case "addErrorCode":
				// The second argument must be a named Code constant, not a literal.
				if len(call.Args) < 2 {
					t.Errorf("%s: addErrorCode call without a code", fset.Position(call.Pos()))
					return true
				}
				if id, ok := call.Args[1].(*ast.Ident); !ok || !strings.HasPrefix(id.Name, "Code") {
					t.Errorf("%s: addErrorCode must use a named Code constant", fset.Position(call.Pos()))
				}
			}
			return true
		})
	}
}
