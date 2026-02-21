package lexer

import (
	"testing"
)

func tok(kind TokenKind, value string) Token {
	return Token{Kind: kind, Value: value}
}

func assertTokens(t *testing.T, input string, expected []Token) {
	t.Helper()
	tokens, errs := Tokenize("test.bp", []byte(input))
	if len(errs) > 0 {
		t.Errorf("unexpected lex errors: %v", errs)
	}
	// Remove EOF for comparison
	if len(tokens) > 0 && tokens[len(tokens)-1].Kind == TokenEOF {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) != len(expected) {
		t.Errorf("expected %d tokens, got %d", len(expected), len(tokens))
		for i, tk := range tokens {
			t.Logf("  token[%d]: %s", i, tk)
		}
		return
	}
	for i, exp := range expected {
		got := tokens[i]
		if got.Kind != exp.Kind {
			t.Errorf("token[%d]: expected kind %s, got %s", i, exp.Kind, got.Kind)
		}
		if exp.Value != "" && got.Value != exp.Value {
			t.Errorf("token[%d]: expected value %q, got %q", i, exp.Value, got.Value)
		}
	}
}

func TestArrows(t *testing.T) {
	assertTokens(t, "<-", []Token{tok(TokenArrowIn, "<-")})
	assertTokens(t, "->", []Token{tok(TokenArrowOut, "->")})
	assertTokens(t, "|>", []Token{tok(TokenArrowPipe, "|>")})
}

func TestIntentAndGenerate(t *testing.T) {
	assertTokens(t, `@ "hello"`, []Token{
		tok(TokenIntent, "@"),
		tok(TokenString, "hello"),
	})
	assertTokens(t, `@> "generate this"`, []Token{
		tok(TokenGenerate, "@>"),
		tok(TokenString, "generate this"),
	})
}

func TestStrings(t *testing.T) {
	assertTokens(t, `"hello world"`, []Token{tok(TokenString, "hello world")})
	assertTokens(t, `"escaped \"quote\""`, []Token{tok(TokenString, `escaped "quote"`)})
	assertTokens(t, `"new\nline"`, []Token{tok(TokenString, "new\nline")})
	assertTokens(t, `"tab\there"`, []Token{tok(TokenString, "tab\there")})
	assertTokens(t, `"backslash\\"`, []Token{tok(TokenString, "backslash\\")})
}

func TestMultiLineString(t *testing.T) {
	assertTokens(t, "\"line1\nline2\nline3\"", []Token{
		tok(TokenString, "line1\nline2\nline3"),
	})
}

func TestIntegers(t *testing.T) {
	assertTokens(t, "42", []Token{tok(TokenInt, "42")})
	assertTokens(t, "0", []Token{tok(TokenInt, "0")})
	assertTokens(t, "8080", []Token{tok(TokenInt, "8080")})
}

func TestFloats(t *testing.T) {
	assertTokens(t, "3.14", []Token{tok(TokenFloat, "3.14")})
	assertTokens(t, "0.5", []Token{tok(TokenFloat, "0.5")})
	assertTokens(t, "100.0", []Token{tok(TokenFloat, "100.0")})
}

func TestBooleans(t *testing.T) {
	assertTokens(t, "true", []Token{tok(TokenTrue, "true")})
	assertTokens(t, "false", []Token{tok(TokenFalse, "false")})
}

func TestSpecialLiterals(t *testing.T) {
	assertTokens(t, "now", []Token{tok(TokenNow, "now")})
	assertTokens(t, "null", []Token{tok(TokenNull, "null")})
}

func TestDurations(t *testing.T) {
	assertTokens(t, "500ms", []Token{tok(TokenDuration, "500ms")})
	assertTokens(t, "5s", []Token{tok(TokenDuration, "5s")})
	assertTokens(t, "10min", []Token{tok(TokenDuration, "10min")})
	assertTokens(t, "1hour", []Token{tok(TokenDuration, "1hour")})
	assertTokens(t, "1day", []Token{tok(TokenDuration, "1day")})
	assertTokens(t, "30days", []Token{tok(TokenDuration, "30days")})
}

func TestSizes(t *testing.T) {
	assertTokens(t, "512b", []Token{tok(TokenSize, "512b")})
	assertTokens(t, "10kb", []Token{tok(TokenSize, "10kb")})
	assertTokens(t, "10mb", []Token{tok(TokenSize, "10mb")})
	assertTokens(t, "1gb", []Token{tok(TokenSize, "1gb")})
}

func TestRates(t *testing.T) {
	assertTokens(t, "10/min", []Token{tok(TokenRate, "10/min")})
	assertTokens(t, "100/hour", []Token{tok(TokenRate, "100/hour")})
	assertTokens(t, "1000/day", []Token{tok(TokenRate, "1000/day")})
}

func TestDelimiters(t *testing.T) {
	assertTokens(t, "{ } [ ] ( ) , : .", []Token{
		tok(TokenLBrace, "{"),
		tok(TokenRBrace, "}"),
		tok(TokenLBracket, "["),
		tok(TokenRBracket, "]"),
		tok(TokenLParen, "("),
		tok(TokenRParen, ")"),
		tok(TokenComma, ","),
		tok(TokenColon, ":"),
		tok(TokenDot, "."),
	})
}

func TestOperators(t *testing.T) {
	assertTokens(t, "== != < > <= >= + - * /", []Token{
		tok(TokenEq, "=="),
		tok(TokenNeq, "!="),
		tok(TokenLt, "<"),
		tok(TokenGt, ">"),
		tok(TokenLte, "<="),
		tok(TokenGte, ">="),
		tok(TokenPlus, "+"),
		tok(TokenMinus, "-"),
		tok(TokenStar, "*"),
		tok(TokenSlash, "/"),
	})
	assertTokens(t, "=", []Token{tok(TokenAssign, "=")})
}

func TestKeywords(t *testing.T) {
	assertTokens(t, "blueprint", []Token{tok(TokenBlueprint, "blueprint")})
	assertTokens(t, "model", []Token{tok(TokenModel, "model")})
	assertTokens(t, "fn", []Token{tok(TokenFn, "fn")})
	assertTokens(t, "pipe", []Token{tok(TokenPipe, "pipe")})
	assertTokens(t, "middleware", []Token{tok(TokenMiddleware, "middleware")})
	assertTokens(t, "secret", []Token{tok(TokenSecret, "secret")})
	assertTokens(t, "env", []Token{tok(TokenEnv, "env")})
	assertTokens(t, "include", []Token{tok(TokenInclude, "include")})
	assertTokens(t, "type", []Token{tok(TokenType, "type")})
	assertTokens(t, "alias", []Token{tok(TokenAlias, "alias")})
	assertTokens(t, "enum", []Token{tok(TokenEnum, "enum")})
	assertTokens(t, "test", []Token{tok(TokenTest, "test")})
	assertTokens(t, "test_group", []Token{tok(TokenTestGroup, "test_group")})
	assertTokens(t, "fixture", []Token{tok(TokenFixture, "fixture")})
	assertTokens(t, "worker", []Token{tok(TokenWorker, "worker")})
	assertTokens(t, "schedule", []Token{tok(TokenSchedule, "schedule")})
	assertTokens(t, "external", []Token{tok(TokenExternal, "external")})
	assertTokens(t, "subscribe", []Token{tok(TokenSubscribe, "subscribe")})
}

func TestHTTPMethods(t *testing.T) {
	assertTokens(t, "GET", []Token{tok(TokenGetMethod, "GET")})
	assertTokens(t, "POST", []Token{tok(TokenPostMethod, "POST")})
	assertTokens(t, "PUT", []Token{tok(TokenPutMethod, "PUT")})
	assertTokens(t, "PATCH", []Token{tok(TokenPatchMethod, "PATCH")})
	assertTokens(t, "DELETE", []Token{tok(TokenDeleteMethod, "DELETE")})
	assertTokens(t, "STREAM", []Token{tok(TokenStreamMethod, "STREAM")})
	assertTokens(t, "WS", []Token{tok(TokenWs, "WS")})
}

func TestIdentifiers(t *testing.T) {
	assertTokens(t, "my_var", []Token{tok(TokenIdent, "my_var")})
	assertTokens(t, "_private", []Token{tok(TokenIdent, "_private")})
	assertTokens(t, "camelCase", []Token{tok(TokenIdent, "camelCase")})
	assertTokens(t, "PascalCase", []Token{tok(TokenIdent, "PascalCase")})
	assertTokens(t, "SCREAMING_CASE", []Token{tok(TokenIdent, "SCREAMING_CASE")})
}

func TestPathsAfterMethod(t *testing.T) {
	assertTokens(t, "GET /api/health", []Token{
		tok(TokenGetMethod, "GET"),
		tok(TokenPath, "/api/health"),
	})
	assertTokens(t, "POST /api/users/:id", []Token{
		tok(TokenPostMethod, "POST"),
		tok(TokenPath, "/api/users/:id"),
	})
	assertTokens(t, "DELETE /api/v1/items/:item_id/comments/:comment_id", []Token{
		tok(TokenDeleteMethod, "DELETE"),
		tok(TokenPath, "/api/v1/items/:item_id/comments/:comment_id"),
	})
}

func TestComments(t *testing.T) {
	assertTokens(t, "# this is a comment", []Token{})
	assertTokens(t, "42 # inline comment", []Token{tok(TokenInt, "42")})
	assertTokens(t, "# line1\n# line2\n42", []Token{tok(TokenInt, "42")})
}

func TestWhitespace(t *testing.T) {
	assertTokens(t, "  42  ", []Token{tok(TokenInt, "42")})
	assertTokens(t, "\t\n\r 42", []Token{tok(TokenInt, "42")})
}

func TestArrowGreedy(t *testing.T) {
	// <- is always ArrowIn, not Lt + Minus
	assertTokens(t, "<-", []Token{tok(TokenArrowIn, "<-")})
	// -> is always ArrowOut
	assertTokens(t, "->", []Token{tok(TokenArrowOut, "->")})
	// |> is always ArrowPipe
	assertTokens(t, "|>", []Token{tok(TokenArrowPipe, "|>")})
}

func TestLtNotArrow(t *testing.T) {
	// < followed by space + something is Lt, not ArrowIn
	assertTokens(t, "x < y", []Token{
		tok(TokenIdent, "x"),
		tok(TokenLt, "<"),
		tok(TokenIdent, "y"),
	})
}

func TestComparisonOperators(t *testing.T) {
	assertTokens(t, "a <= b", []Token{
		tok(TokenIdent, "a"),
		tok(TokenLte, "<="),
		tok(TokenIdent, "b"),
	})
	assertTokens(t, "a >= b", []Token{
		tok(TokenIdent, "a"),
		tok(TokenGte, ">="),
		tok(TokenIdent, "b"),
	})
}

func TestLogicalKeywords(t *testing.T) {
	assertTokens(t, "and", []Token{tok(TokenAnd, "and")})
	assertTokens(t, "or", []Token{tok(TokenOr, "or")})
	assertTokens(t, "not", []Token{tok(TokenNot, "not")})
	assertTokens(t, "in", []Token{tok(TokenIn, "in")})
}

func TestCompoundStatement(t *testing.T) {
	input := `blueprint "test" {
  version "1.0"
  port    3000
}`
	assertTokens(t, input, []Token{
		tok(TokenBlueprint, "blueprint"),
		tok(TokenString, "test"),
		tok(TokenLBrace, "{"),
		tok(TokenIdent, "version"),
		tok(TokenString, "1.0"),
		tok(TokenIdent, "port"),
		tok(TokenInt, "3000"),
		tok(TokenRBrace, "}"),
	})
}

func TestSecretDeclaration(t *testing.T) {
	input := `secret API_KEY required`
	assertTokens(t, input, []Token{
		tok(TokenSecret, "secret"),
		tok(TokenIdent, "API_KEY"),
		tok(TokenRequired, "required"),
	})
}

func TestEnvWithSize(t *testing.T) {
	input := `env MAX_FILE_SIZE 10mb`
	assertTokens(t, input, []Token{
		tok(TokenEnv, "env"),
		tok(TokenIdent, "MAX_FILE_SIZE"),
		tok(TokenSize, "10mb"),
	})
}

func TestModelField(t *testing.T) {
	input := `id uuid primary`
	assertTokens(t, input, []Token{
		tok(TokenIdent, "id"),
		tok(TokenIdent, "uuid"),
		tok(TokenPrimary, "primary"),
	})
}

func TestArrowInputLine(t *testing.T) {
	input := `<- file image/*`
	tokens, errs := Tokenize("test.bp", []byte(input))
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if tokens[0].Kind != TokenArrowIn {
		t.Errorf("expected ArrowIn, got %s", tokens[0].Kind)
	}
}

func TestGuardLine(t *testing.T) {
	input := `|> guard file.size < 10mb -> 413 "File too large"`
	tokens, _ := Tokenize("test.bp", []byte(input))
	if tokens[0].Kind != TokenArrowPipe {
		t.Errorf("expected ArrowPipe, got %s", tokens[0].Kind)
	}
	if tokens[1].Kind != TokenGuard {
		t.Errorf("expected Guard, got %s", tokens[1].Kind)
	}
}

func TestListLiteral(t *testing.T) {
	input := `["a", "b", "c"]`
	assertTokens(t, input, []Token{
		tok(TokenLBracket, "["),
		tok(TokenString, "a"),
		tok(TokenComma, ","),
		tok(TokenString, "b"),
		tok(TokenComma, ","),
		tok(TokenString, "c"),
		tok(TokenRBracket, "]"),
	})
}

func TestTrailingComma(t *testing.T) {
	input := `["a", "b",]`
	assertTokens(t, input, []Token{
		tok(TokenLBracket, "["),
		tok(TokenString, "a"),
		tok(TokenComma, ","),
		tok(TokenString, "b"),
		tok(TokenComma, ","),
		tok(TokenRBracket, "]"),
	})
}

func TestBlockBody(t *testing.T) {
	input := `{ key: "value", num: 42 }`
	assertTokens(t, input, []Token{
		tok(TokenLBrace, "{"),
		tok(TokenIdent, "key"),
		tok(TokenColon, ":"),
		tok(TokenString, "value"),
		tok(TokenComma, ","),
		tok(TokenIdent, "num"),
		tok(TokenColon, ":"),
		tok(TokenInt, "42"),
		tok(TokenRBrace, "}"),
	})
}

func TestNegativeNumber(t *testing.T) {
	input := `-1`
	assertTokens(t, input, []Token{
		tok(TokenMinus, "-"),
		tok(TokenInt, "1"),
	})
}

func TestSourceLocations(t *testing.T) {
	input := "abc\ndef"
	tokens, _ := Tokenize("test.bp", []byte(input))
	// abc is on line 1, col 1
	if tokens[0].Loc.Line != 1 || tokens[0].Loc.Col != 1 {
		t.Errorf("expected line 1 col 1, got line %d col %d", tokens[0].Loc.Line, tokens[0].Loc.Col)
	}
	// def is on line 2, col 1
	if tokens[1].Loc.Line != 2 || tokens[1].Loc.Col != 1 {
		t.Errorf("expected line 2 col 1, got line %d col %d", tokens[1].Loc.Line, tokens[1].Loc.Col)
	}
}

func TestUnterminatedString(t *testing.T) {
	_, errs := Tokenize("test.bp", []byte(`"unterminated`))
	if len(errs) == 0 {
		t.Error("expected error for unterminated string")
	}
}

func TestInvalidPipe(t *testing.T) {
	_, errs := Tokenize("test.bp", []byte(`|`))
	if len(errs) == 0 {
		t.Error("expected error for lone |")
	}
}

func TestInvalidBang(t *testing.T) {
	_, errs := Tokenize("test.bp", []byte(`!`))
	if len(errs) == 0 {
		t.Error("expected error for lone !")
	}
}

func TestIllegalCharacter(t *testing.T) {
	_, errs := Tokenize("test.bp", []byte(`~`))
	if len(errs) == 0 {
		t.Error("expected error for ~")
	}
}

func TestEOF(t *testing.T) {
	tokens, _ := Tokenize("test.bp", []byte(""))
	if len(tokens) != 1 || tokens[0].Kind != TokenEOF {
		t.Error("expected single EOF token for empty input")
	}
}

func TestFieldAccess(t *testing.T) {
	input := "user.name"
	assertTokens(t, input, []Token{
		tok(TokenIdent, "user"),
		tok(TokenDot, "."),
		tok(TokenIdent, "name"),
	})
}

func TestStringInterpolationRaw(t *testing.T) {
	// String interpolation is kept raw — {expr} is just part of the string
	input := `"Hello {user.name}"`
	assertTokens(t, input, []Token{
		tok(TokenString, "Hello {user.name}"),
	})
}

func TestOnConnectKeyword(t *testing.T) {
	assertTokens(t, "on_connect", []Token{tok(TokenOnConnect, "on_connect")})
	assertTokens(t, "on_message", []Token{tok(TokenOnMessage, "on_message")})
	assertTokens(t, "on_disconnect", []Token{tok(TokenOnDisconnect, "on_disconnect")})
	assertTokens(t, "on_fail", []Token{tok(TokenOnFail, "on_fail")})
	assertTokens(t, "on_error", []Token{tok(TokenOnError, "on_error")})
}

func TestDotNotation(t *testing.T) {
	input := "90.days.ago"
	assertTokens(t, input, []Token{
		tok(TokenInt, "90"),
		tok(TokenDot, "."),
		tok(TokenIdent, "days"),
		tok(TokenDot, "."),
		tok(TokenIdent, "ago"),
	})
}

func TestFloatVsDotAccess(t *testing.T) {
	// 3.14 should be a float, not int.int
	input := "3.14"
	assertTokens(t, input, []Token{tok(TokenFloat, "3.14")})
}

func TestRateInContext(t *testing.T) {
	input := "limit 60/min"
	assertTokens(t, input, []Token{
		tok(TokenLimit, "limit"),
		tok(TokenRate, "60/min"),
	})
}

func TestPathAfterTarget(t *testing.T) {
	input := "target POST /api/watermark"
	assertTokens(t, input, []Token{
		tok(TokenTarget, "target"),
		tok(TokenPostMethod, "POST"),
		tok(TokenPath, "/api/watermark"),
	})
}

func TestExpressionArithmetic(t *testing.T) {
	input := "a + b * c"
	assertTokens(t, input, []Token{
		tok(TokenIdent, "a"),
		tok(TokenPlus, "+"),
		tok(TokenIdent, "b"),
		tok(TokenStar, "*"),
		tok(TokenIdent, "c"),
	})
}

func TestMixedTokens(t *testing.T) {
	input := `<- file image/* required`
	tokens, errs := Tokenize("test.bp", []byte(input))
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if tokens[0].Kind != TokenArrowIn {
		t.Errorf("token 0: expected ArrowIn, got %s", tokens[0].Kind)
	}
	if tokens[1].Kind != TokenIdent || tokens[1].Value != "file" {
		t.Errorf("token 1: expected Ident(file), got %s(%s)", tokens[1].Kind, tokens[1].Value)
	}
	// image/* — the lexer emits "image" then "/" then "*"
	// (MIME types are assembled in the parser)
}

func TestCronKeyword(t *testing.T) {
	input := `cron "0 4 * * 0"`
	assertTokens(t, input, []Token{
		tok(TokenCronKw, "cron"),
		tok(TokenString, "0 4 * * 0"),
	})
}
