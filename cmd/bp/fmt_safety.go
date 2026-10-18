package main

import (
	"fmt"
	"io"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// exitFormatterBug is returned when bp fmt refuses to use its own output. It
// means the formatter is wrong for this input, not that the input is invalid.
const exitFormatterBug = 3

// formatPrinter renders a parsed file for bp fmt. Tests replace it to model a
// formatter defect; the CLI always uses ast.Print.
var formatPrinter = ast.Print

// parseFormatSource parses a file the way bp fmt reads it. A file without a
// blueprint block is an include fragment and is parsed as a partial file.
func parseFormatSource(filename string, src []byte) (*ast.File, []parser.ParseError) {
	file, errs := parser.ParseFile(filename, src)
	for _, e := range errs {
		if e.Code == parser.CodeMissingBlueprint {
			return parser.ParsePartialFile(filename, src)
		}
	}
	return file, errs
}

// verifyFormatted checks formatted output before bp fmt uses it. The output
// must parse, must have the same syntax tree as the source it came from, and
// must keep every `#` comment and `@` intent (neither is fully in the tree).
func verifyFormatted(file *ast.File, filename, formatted string) error {
	reparsed, errs := parseFormatSource(filename, []byte(formatted))
	if len(errs) > 0 {
		return fmt.Errorf("formatted output does not parse: %s", errs[0].Message)
	}
	if same, where := ast.SameStructure(file, reparsed); !same {
		return fmt.Errorf("formatted output changes the syntax tree (%s)", where)
	}
	if got, want := len(reparsed.Comments), len(file.Comments); got != want {
		return fmt.Errorf("formatted output has %d comments, the source has %d", got, want)
	}
	if got, want := ast.IntentCount(reparsed), ast.IntentCount(file); got != want {
		return fmt.Errorf("formatted output has %d @ intents, the source has %d", got, want)
	}
	return nil
}

// reportFormatterBug explains a refused format. The source file has not been
// touched when this is printed.
func reportFormatterBug(w io.Writer, filename string, err error) {
	fmt.Fprintf(w, "error: bp fmt refused to format %s: %s\n", filename, err)
	fmt.Fprintln(w, "The file was left unchanged. This is a bug in the formatter, not in your source.")
	fmt.Fprintln(w, "Please report it at https://github.com/abdul-hamid-achik/blueprint/issues and include the file if you can share it.")
}
