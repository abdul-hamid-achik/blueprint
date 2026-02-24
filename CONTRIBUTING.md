# Contributing to Blueprint

Thank you for your interest in contributing to Blueprint! This guide will help you get started.

## Development Setup

### Prerequisites

- **Go 1.25+** (no external Go dependencies are used)
- **Node.js 18+** (for testing generated output)
- **Git**

### Clone and Build

```bash
git clone https://github.com/abdul-hamid-achik/blueprint.git
cd blueprint
go build ./cmd/bp
```

### Run Tests

```bash
# Run all tests
go test ./... -count=1

# Run tests for a specific package
go test ./internal/checker/ -count=1

# Run with verbose output
go test ./... -count=1 -v

# Run fuzz tests (example: parser fuzzing)
go test ./internal/parser/ -fuzz=FuzzParser -fuzztime=30s
```

## Project Structure

```
cmd/bp/main.go           CLI entry point, all commands
internal/
  lexer/                  Hand-written tokenizer (~95 token kinds)
  parser/                 Recursive descent parser, Pratt expressions, panic-mode recovery
  ast/                    AST node types (Node, TopLevel, ArrowStmt, Expr, TypeExpr)
  checker/                Semantic analysis: scope management, name resolution, validation
  codegen/js/             JavaScript/TypeScript code generator (strings.Builder, no templates)
  linter/                 Best-practice linter (bp lint)
  docs/                   OpenAPI 3.1 JSON generator (bp docs)
  generate/               LLM slot resolution via Anthropic API (bp generate)
packages/runtime/         npm runtime package
testdata/
  valid/                  .bp fixtures that must parse and check without errors
  invalid/                .bp fixtures that must produce parser or checker errors
```

## How to Add a New Language Construct

Adding a new keyword or block type to Blueprint touches four layers. Here is the typical workflow:

### 1. Lexer (`internal/lexer/`)

- Add a new `Token` constant (e.g., `TokenMyKeyword`)
- Add the keyword string to the `keywords` map so the lexer recognizes it
- Add any new operators or punctuation if needed

### 2. Parser (`internal/parser/`)

- Add a parse function (e.g., `parseMyBlock()`) that builds an AST node
- Wire it into the top-level dispatch in `parseTopLevel()` or the appropriate statement parser
- Handle error recovery so one bad block does not cascade

### 3. AST (`internal/ast/`)

- Define the new node struct implementing the appropriate interface (`TopLevel`, `ArrowStmt`, `Expr`, etc.)
- Add it to `printer.go` so `bp fmt` can round-trip the node
- Ensure the node has a `Loc` field and implements `Location()`

### 4. Checker (`internal/checker/`)

- Add validation for the new node in `validateBlocks()`
- Check naming conventions, reference validity, structural rules
- Add the new symbol kind to `scope.go` if it introduces a named declaration

### 5. Code Generator (`internal/codegen/js/`)

- Add the generation logic in `generator.go` and/or `helpers.go`
- Produce the appropriate TypeScript output files
- Ensure deterministic output (sort maps before iterating)

### 6. Tests

- Add valid `.bp` fixtures in `testdata/valid/`
- Add invalid `.bp` fixtures in `testdata/invalid/`
- Add unit tests for the new parser, checker, and codegen behavior

## Code Style Guidelines

- **Format with `gofmt`** -- all Go code must be formatted with `gofmt`
- **No external Go dependencies** -- the entire toolchain uses only the Go standard library
- **Deterministic output** -- sort maps before iterating to ensure reproducible codegen
- **snake_case in .bp** -- Blueprint source uses `snake_case` for identifiers, `PascalCase` for types/enums, `SCREAMING_SNAKE_CASE` for secrets/env
- **Flat by force** -- max 1 level of `{}` nesting in .bp (except `try/recover`)
- **Use `strings.Builder`** -- for codegen, not `fmt.Sprintf` concatenation or templates

## Pull Request Workflow

1. **Fork** the repository and create a feature branch from `main`
2. **Make your changes** following the style guidelines above
3. **Add tests** for any new functionality
4. **Run the full test suite**: `go test ./... -count=1`
5. **Format your code**: `gofmt -w .`
6. **Open a pull request** against `main` with a clear description

### PR Checklist

- [ ] No external Go dependencies added
- [ ] Code formatted with `gofmt`
- [ ] All existing tests pass
- [ ] New tests added for new functionality
- [ ] Generated output is deterministic (sorted maps)

## Issue Reporting Guidelines

When reporting a bug, please include:

1. **Blueprint version** (`bp version`)
2. **The `.bp` source** that triggers the issue (minimal reproduction preferred)
3. **Expected behavior** vs **actual behavior**
4. **Error messages** if any
5. **Operating system** and Go version (if building from source)

For feature requests, describe the use case and proposed `.bp` syntax if applicable.

## Questions?

Open a GitHub issue or discussion. We are happy to help!
