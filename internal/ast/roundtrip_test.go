package ast_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/checker"
	"github.com/abdul-hamid-achik/blueprint/internal/parser"
)

// parseFormatInput parses src the way `bp fmt` does: a file without a
// blueprint block is retried as a partial include fragment.
func parseFormatInput(filename string, src []byte) (*ast.File, []parser.ParseError) {
	file, errs := parser.ParseFile(filename, src)
	for _, e := range errs {
		if e.Code == parser.CodeMissingBlueprint {
			return parser.ParsePartialFile(filename, src)
		}
	}
	return file, errs
}

func commentTexts(file *ast.File) []string {
	texts := make([]string, 0, len(file.Comments))
	for _, c := range file.Comments {
		texts = append(texts, c.Text)
	}
	return texts
}

// checkFormatRoundTrip asserts the formatter contract for one source file:
// the output parses, keeps the AST and every comment, is a fixed point of
// Print, and does not turn a clean check into a failing one.
func checkFormatRoundTrip(t *testing.T, filename string, src []byte) {
	t.Helper()
	orig, errs := parseFormatInput(filename, src)
	if len(errs) > 0 {
		t.Fatalf("input does not parse: %s", errs[0].Message)
	}
	formatted := ast.Print(orig)

	reparsed, errs := parseFormatInput(filename, []byte(formatted))
	if len(errs) > 0 {
		t.Fatalf("formatted output does not parse: %s\n%s", errs[0].Message, formatted)
	}
	if same, where := ast.SameStructure(orig, reparsed); !same {
		t.Fatalf("formatting changed the AST (%s)\n%s", where, formatted)
	}
	// Every comment must survive with its text. Their relative order can change
	// when two comments end up on one canonical line, so compare as multisets.
	got, want := commentTexts(reparsed), commentTexts(orig)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("comments changed\ngot:  %q\nwant: %q\n%s", got, want, formatted)
	}
	if second := ast.Print(reparsed); second != formatted {
		t.Fatalf("formatting is not idempotent\nfirst:\n%s\nsecond:\n%s", formatted, second)
	}
	if len(checker.Check(orig)) == 0 && len(checker.Check(reparsed)) != 0 {
		t.Fatalf("formatted output fails the checker: %v\n%s", checker.Check(reparsed), formatted)
	}
}

func fixtureFiles(t testing.TB) []string {
	t.Helper()
	var files []string
	for _, pattern := range []string{
		filepath.Join("..", "..", "examples", "*.bp"),
		filepath.Join("..", "..", "testdata", "valid", "*.bp"),
		filepath.Join("..", "..", "testdata", "valid", "includes", "*.bp"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) < 40 {
		t.Fatalf("expected the example and fixture corpus, found %d files", len(files))
	}
	return files
}

func TestFormatRoundTripCorpus(t *testing.T) {
	for _, path := range fixtureFiles(t) {
		path := path
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			checkFormatRoundTrip(t, path, src)
		})
	}
}

func FuzzFormatRoundTrip(f *testing.F) {
	for _, path := range fixtureFiles(f) {
		src, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if _, errs := parseFormatInput("fuzz.bp", src); len(errs) > 0 {
			t.Skip()
		}
		checkFormatRoundTrip(t, "fuzz.bp", src)
	})
}
