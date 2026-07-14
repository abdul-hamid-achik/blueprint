package lexer

// Tokenize lexes the source bytes and returns a list of tokens and any lexer errors.
func Tokenize(filename string, src []byte) ([]Token, []LexError) {
	tokens, _, errs := TokenizeWithTrivia(filename, src)
	return tokens, errs
}

// TokenizeWithTrivia lexes source while also returning comments as structured
// trivia. Comments do not appear in the token stream, so existing parsers and
// token consumers retain their current behavior.
func TokenizeWithTrivia(filename string, src []byte) ([]Token, []Comment, []LexError) {
	l := &lexer{
		file: filename,
		src:  src,
		line: 1,
		col:  1,
	}
	l.run()
	l.anchorPendingComments(len(l.tokens))
	l.tokens = append(l.tokens, Token{Kind: TokenEOF, Loc: l.loc()})
	return l.tokens, l.comments, l.errors
}

type lexer struct {
	file            string
	src             []byte
	pos             int
	line            int
	col             int
	tokens          []Token
	comments        []Comment
	pendingComments []int
	errors          []LexError
	lineHasCode     bool
}

func (l *lexer) loc() Loc {
	return Loc{File: l.file, Line: l.line, Col: l.col, Offset: l.pos}
}

func (l *lexer) atEnd() bool {
	return l.pos >= len(l.src)
}

func (l *lexer) peek() byte {
	if l.atEnd() {
		return 0
	}
	return l.src[l.pos]
}

func (l *lexer) peekAt(offset int) byte {
	i := l.pos + offset
	if i >= len(l.src) {
		return 0
	}
	return l.src[i]
}

func (l *lexer) advance() byte {
	ch := l.src[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
		l.lineHasCode = false
	} else {
		l.col++
	}
	return ch
}

func (l *lexer) emit(kind TokenKind, value string, loc Loc) {
	loc.Len = l.pos - loc.Offset
	l.anchorPendingComments(len(l.tokens))
	l.tokens = append(l.tokens, Token{Kind: kind, Value: value, Loc: loc})
	l.lineHasCode = true
}

func (l *lexer) anchorPendingComments(tokenIndex int) {
	for _, commentIndex := range l.pendingComments {
		l.comments[commentIndex].AnchorToken = tokenIndex
	}
	l.pendingComments = l.pendingComments[:0]
}

func (l *lexer) addError(loc Loc, msg string) {
	l.errors = append(l.errors, LexError{Loc: loc, Message: msg})
}

// addErrorCode is addError plus a structured error code (e.g. "L001") and an
// optional Hint. Use this for sites documented in docs/error-codes.md so users
// can `bp explain <code>` to view the long-form explanation.
func (l *lexer) addErrorCode(loc Loc, code, msg, hint string) {
	l.errors = append(l.errors, LexError{Loc: loc, Message: msg, Hint: hint, Code: code})
}

// Lexer error codes — keep in sync with docs/error-codes.md and
// internal/diag/error-codes.md (the drift test enforces match).
const (
	// CodeLonePipe = `|` not followed by `>` (the only legal use is the
	// pipeline arrow `|>`).
	CodeLonePipe = "L001"
)

func (l *lexer) lastTokenKind() TokenKind {
	if len(l.tokens) == 0 {
		return TokenEOF
	}
	return l.tokens[len(l.tokens)-1].Kind
}

func (l *lexer) run() {
	for !l.atEnd() {
		l.skipWhitespace()
		if l.atEnd() {
			break
		}
		ch := l.peek()
		switch {
		case ch == '#':
			l.skipComment()
		case ch == '"':
			l.scanString()
		case ch == '<':
			l.scanLAngle()
		case ch == '-':
			l.scanDash()
		case ch == '|':
			l.scanPipe()
		case ch == '@':
			l.scanAt()
		case ch == '/':
			l.scanSlash()
		case ch == '{':
			loc := l.loc()
			l.advance()
			l.emit(TokenLBrace, "{", loc)
		case ch == '}':
			loc := l.loc()
			l.advance()
			l.emit(TokenRBrace, "}", loc)
		case ch == '[':
			loc := l.loc()
			l.advance()
			l.emit(TokenLBracket, "[", loc)
		case ch == ']':
			loc := l.loc()
			l.advance()
			l.emit(TokenRBracket, "]", loc)
		case ch == '(':
			loc := l.loc()
			l.advance()
			l.emit(TokenLParen, "(", loc)
		case ch == ')':
			loc := l.loc()
			l.advance()
			l.emit(TokenRParen, ")", loc)
		case ch == ',':
			loc := l.loc()
			l.advance()
			l.emit(TokenComma, ",", loc)
		case ch == ':':
			loc := l.loc()
			l.advance()
			l.emit(TokenColon, ":", loc)
		case ch == '.':
			loc := l.loc()
			l.advance()
			l.emit(TokenDot, ".", loc)
		case ch == '+':
			loc := l.loc()
			l.advance()
			l.emit(TokenPlus, "+", loc)
		case ch == '*':
			loc := l.loc()
			l.advance()
			l.emit(TokenStar, "*", loc)
		case ch == '=':
			l.scanEquals()
		case ch == '!':
			l.scanBang()
		case ch == '>':
			l.scanGt()
		case isDigit(ch):
			l.scanNumber()
		case isLetter(ch) || ch == '_':
			l.scanIdentOrKeyword()
		default:
			loc := l.loc()
			l.advance()
			l.emit(TokenIllegal, string(ch), loc)
			l.addError(loc, "unexpected character '"+string(ch)+"'")
		}
	}
}

func (l *lexer) skipWhitespace() {
	for !l.atEnd() {
		ch := l.peek()
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			l.advance()
		} else {
			break
		}
	}
}

func (l *lexer) skipComment() {
	loc := l.loc()
	start := l.pos
	inline := l.lineHasCode

	// Capture # and everything until end of line. String scanning consumes its
	// own contents, so a # inside a quoted string never reaches this path.
	for !l.atEnd() && l.peek() != '\n' {
		l.advance()
	}
	loc.Len = l.pos - loc.Offset
	comment := Comment{
		Loc:         loc,
		Text:        string(l.src[start:l.pos]),
		Inline:      inline,
		AnchorToken: -1,
	}
	if inline {
		comment.AnchorToken = len(l.tokens) - 1
	}
	l.comments = append(l.comments, comment)
	if !inline {
		l.pendingComments = append(l.pendingComments, len(l.comments)-1)
	}
}

func (l *lexer) scanString() {
	loc := l.loc()
	l.advance() // skip opening "
	var s []byte
	for !l.atEnd() {
		ch := l.peek()
		if ch == '"' {
			l.advance() // skip closing "
			l.emit(TokenString, string(s), loc)
			return
		}
		if ch == '\\' {
			l.advance()
			if l.atEnd() {
				l.addError(loc, "unterminated string literal")
				l.emit(TokenString, string(s), loc)
				return
			}
			esc := l.advance()
			switch esc {
			case '"':
				s = append(s, '"')
			case '\\':
				s = append(s, '\\')
			case 'n':
				s = append(s, '\n')
			case 't':
				s = append(s, '\t')
			default:
				s = append(s, '\\', esc)
			}
			continue
		}
		s = append(s, l.advance())
	}
	l.addError(loc, "unterminated string literal")
	l.emit(TokenString, string(s), loc)
}

func (l *lexer) scanLAngle() {
	loc := l.loc()
	l.advance() // consume <
	if !l.atEnd() && l.peek() == '-' {
		l.advance() // consume -
		l.emit(TokenArrowIn, "<-", loc)
		return
	}
	if !l.atEnd() && l.peek() == '=' {
		l.advance()
		l.emit(TokenLte, "<=", loc)
		return
	}
	l.emit(TokenLt, "<", loc)
}

func (l *lexer) scanDash() {
	loc := l.loc()
	l.advance() // consume -
	if !l.atEnd() && l.peek() == '>' {
		l.advance()
		l.emit(TokenArrowOut, "->", loc)
		return
	}
	l.emit(TokenMinus, "-", loc)
}

func (l *lexer) scanPipe() {
	loc := l.loc()
	l.advance() // consume |
	if !l.atEnd() && l.peek() == '>' {
		l.advance()
		l.emit(TokenArrowPipe, "|>", loc)
		return
	}
	l.emit(TokenIllegal, "|", loc)
	l.addErrorCode(loc, CodeLonePipe,
		"'|' is not valid alone",
		"Did you mean '|>' (the pipeline arrow)?",
	)
}

func (l *lexer) scanAt() {
	loc := l.loc()
	l.advance() // consume @
	if !l.atEnd() && l.peek() == '>' {
		l.advance()
		l.emit(TokenGenerate, "@>", loc)
		return
	}
	l.emit(TokenIntent, "@", loc)
}

func (l *lexer) scanSlash() {
	loc := l.loc()
	// Check if this is a path: after a method keyword, or at start of line context
	if l.isPathContext() {
		l.scanPath()
		return
	}
	l.advance() // consume /
	l.emit(TokenSlash, "/", loc)
}

func (l *lexer) isPathContext() bool {
	prev := l.lastTokenKind()
	if IsEndpointMethod(prev) {
		return true
	}
	if prev == TokenTarget {
		return true
	}
	return false
}

func (l *lexer) scanPath() {
	loc := l.loc()
	start := l.pos
	for !l.atEnd() {
		ch := l.peek()
		if ch == '/' || ch == ':' || ch == '-' || isLetter(ch) || isDigit(ch) || ch == '_' || ch == '.' {
			l.advance()
		} else {
			break
		}
	}
	l.emit(TokenPath, string(l.src[start:l.pos]), loc)
}

func (l *lexer) scanEquals() {
	loc := l.loc()
	l.advance() // consume =
	if !l.atEnd() && l.peek() == '=' {
		l.advance()
		l.emit(TokenEq, "==", loc)
		return
	}
	l.emit(TokenAssign, "=", loc)
}

func (l *lexer) scanBang() {
	loc := l.loc()
	l.advance() // consume !
	if !l.atEnd() && l.peek() == '=' {
		l.advance()
		l.emit(TokenNeq, "!=", loc)
		return
	}
	l.emit(TokenIllegal, "!", loc)
	l.addError(loc, "'!' is not valid alone. Did you mean '!='?")
}

func (l *lexer) scanGt() {
	loc := l.loc()
	l.advance() // consume >
	if !l.atEnd() && l.peek() == '=' {
		l.advance()
		l.emit(TokenGte, ">=", loc)
		return
	}
	l.emit(TokenGt, ">", loc)
}

func (l *lexer) scanNumber() {
	loc := l.loc()
	start := l.pos

	// consume digits
	for !l.atEnd() && isDigit(l.peek()) {
		l.advance()
	}

	isFloat := false
	// check for float: digit.digit (not digit.identifier like 90.days.ago)
	if !l.atEnd() && l.peek() == '.' && l.pos+1 < len(l.src) && isDigit(l.peekAt(1)) {
		isFloat = true
		l.advance() // consume .
		for !l.atEnd() && isDigit(l.peek()) {
			l.advance()
		}
	}

	numStr := string(l.src[start:l.pos])

	// Check for rate literal: number/unit
	if !l.atEnd() && l.peek() == '/' {
		// Look ahead for rate unit
		saved := l.pos
		savedLine := l.line
		savedCol := l.col
		l.advance() // consume /
		unitStart := l.pos
		for !l.atEnd() && isLetter(l.peek()) {
			l.advance()
		}
		unit := string(l.src[unitStart:l.pos])
		if isRateUnit(unit) {
			l.emit(TokenRate, numStr+"/"+unit, loc)
			return
		}
		// Not a rate, restore position
		l.pos = saved
		l.line = savedLine
		l.col = savedCol
	}

	// Check for unit suffix: duration or size
	if !l.atEnd() && isLetter(l.peek()) {
		unitStart := l.pos
		unitLine := l.line
		unitCol := l.col
		for !l.atEnd() && isLetter(l.peek()) {
			l.advance()
		}
		unit := string(l.src[unitStart:l.pos])
		if isSizeUnit(unit) {
			l.emit(TokenSize, numStr+unit, loc)
			return
		}
		if isDurationUnit(unit) {
			l.emit(TokenDuration, numStr+unit, loc)
			return
		}
		// Not a recognized unit — backtrack and emit number + identifier separately
		l.pos = unitStart
		l.line = unitLine
		l.col = unitCol
	}

	if isFloat {
		l.emit(TokenFloat, numStr, loc)
	} else {
		l.emit(TokenInt, numStr, loc)
	}
}

func (l *lexer) scanIdentOrKeyword() {
	loc := l.loc()
	start := l.pos
	for !l.atEnd() && isIdentChar(l.peek()) {
		l.advance()
	}
	word := string(l.src[start:l.pos])

	// Special: check for compound keywords with underscore
	// e.g., on_connect, on_disconnect, on_message, on_fail, on_error, test_group
	// These are handled by the keyword lookup since they contain underscores

	// Check for .days.ago suffix on numbers (special form like 90.days.ago)
	// This is handled differently - the number is already parsed, and this would be
	// field access in the parser (90 . days . ago)

	kind := LookupKeyword(word)
	l.emit(kind, word, loc)
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isLetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isIdentChar(ch byte) bool {
	return isLetter(ch) || isDigit(ch) || ch == '_'
}

func isDurationUnit(s string) bool {
	switch s {
	case "ms", "s", "h", "min", "hour", "hours", "d", "day", "days":
		return true
	}
	return false
}

func isSizeUnit(s string) bool {
	switch s {
	case "b", "kb", "mb", "gb":
		return true
	}
	return false
}

func isRateUnit(s string) bool {
	switch s {
	case "min", "hour", "day":
		return true
	}
	return false
}
