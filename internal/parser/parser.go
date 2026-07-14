package parser

import (
	"fmt"
	"strconv"

	"github.com/abdul-hamid-achik/blueprint/internal/ast"
	"github.com/abdul-hamid-achik/blueprint/internal/lexer"
)

const maxExprDepth = 256
const maxErrors = 50

// Parser error codes — keep in sync with docs/error-codes.md and
// internal/diag/error-codes.md (the drift test enforces match).
const (
	// CodeMissingBlueprint mirrors checker.CodeMissingBlueprint for the case
	// where the parser sees a non-blueprint top-level construct before any
	// blueprint block. The same code is reused so `bp explain C001`-equivalent
	// guidance shows up regardless of which pass first flagged the problem.
	// (The parser-specific code is P001; the checker keeps C001 for the
	// "blueprint block exists somewhere but is invalid" cases.)
	CodeMissingBlueprint = "P001"

	// CodeControlFlowKeyword flags an if/else/for/while/switch keyword found
	// in statement/step position. Blueprint has neither if/else nor loops — use
	// guard/when for branching and map for iteration instead. See P002 in
	// docs/error-codes.md.
	CodeControlFlowKeyword = "P002"
)

// controlFlowKeywords are step-position identifiers that don't exist in
// Blueprint but are the #1 mistake for anyone coming from JS/Python/etc.
// Left unhandled, `|> if cond { ... }` degrades into a generic "Expected
// '}'" panic whose panic-mode recovery can misparse everything that follows
// (see reportControlFlowStmt).
var controlFlowKeywords = map[string]bool{
	"if": true, "else": true, "for": true, "while": true, "switch": true,
}

// Parser parses a token stream into an AST.
type Parser struct {
	tokens []lexer.Token
	pos    int
	errors []ParseError
	file   string
	depth  int
	// lexOffsets holds the byte offsets of all lex errors so that follow-on
	// parser errors at the same offset (e.g. a "expected '}', got 'Illegal'"
	// triggered by a lexer-rejected character) can be suppressed.
	lexOffsets map[int]bool
}

// ParseFile tokenizes and parses a .bp source file, returning the AST and any errors.
func ParseFile(filename string, src []byte) (*ast.File, []ParseError) {
	tokens, comments, lexErrors := lexer.TokenizeWithTrivia(filename, src)

	p := &Parser{
		tokens:     tokens,
		file:       filename,
		lexOffsets: make(map[int]bool, len(lexErrors)),
	}

	// Convert lex errors to parse errors, preserving any structured Code/Hint
	// the lexer attached (see internal/lexer.addErrorCode).
	for _, le := range lexErrors {
		p.errors = append(p.errors, ParseError{
			Loc:     le.Loc,
			Message: le.Message,
			Hint:    le.Hint,
			Code:    le.Code,
		})
		p.lexOffsets[le.Loc.Offset] = true
	}

	file := p.parseFile()
	file.Comments = comments
	file.SourceTokens = tokens
	return file, p.errors
}

// ParsePartialFile tokenizes and parses a .bp fragment (e.g., an included file)
// that does not require a leading blueprint block. Returns only the top-level blocks.
func ParsePartialFile(filename string, src []byte) (*ast.File, []ParseError) {
	tokens, comments, lexErrors := lexer.TokenizeWithTrivia(filename, src)

	p := &Parser{
		tokens:     tokens,
		file:       filename,
		lexOffsets: make(map[int]bool, len(lexErrors)),
	}

	for _, le := range lexErrors {
		p.errors = append(p.errors, ParseError{
			Loc:     le.Loc,
			Message: le.Message,
			Hint:    le.Hint,
			Code:    le.Code,
		})
		p.lexOffsets[le.Loc.Offset] = true
	}

	f := &ast.File{Loc: p.peek().Loc, Comments: comments, SourceTokens: tokens}

	// If included file starts with a blueprint block, parse and attach it
	// but don't require it (unlike ParseFile).
	if p.check(lexer.TokenBlueprint) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(ParseError); ok {
						p.appendRecovered(err)
					} else {
						panic(r)
					}
					p.recoverToNextBlock()
				}
			}()
			f.Blueprint = p.parseBlueprintBlock(nil)
		}()
	}

	for !p.atEnd() {
		block := p.parseTopLevelBlock()
		if block != nil {
			f.Blocks = append(f.Blocks, block)
		}
	}
	return f, p.errors
}

func (p *Parser) parseFile() *ast.File {
	f := &ast.File{Loc: p.peek().Loc}

	// Parse optional intent before blueprint
	intent := p.maybeParseIntent()

	// Expect blueprint block first
	if p.check(lexer.TokenBlueprint) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(ParseError); ok {
						p.appendRecovered(err)
					} else {
						panic(r) // re-panic programming errors
					}
					p.recoverToNextBlock()
				}
			}()
			f.Blueprint = p.parseBlueprintBlock(intent)
		}()
	} else {
		p.addErrorCode(p.peek().Loc, CodeMissingBlueprint,
			"Expected 'blueprint' declaration as first block",
			"Every .bp file must start with: blueprint \"name\" { ... }")
	}

	// Parse remaining top-level blocks
	for !p.atEnd() {
		block := p.parseTopLevelBlock()
		if block != nil {
			f.Blocks = append(f.Blocks, block)
		}
	}

	return f
}

func (p *Parser) parseTopLevelBlock() (block ast.TopLevel) {
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(ParseError); ok {
				p.appendRecovered(err)
			} else {
				panic(r) // re-panic programming errors
			}
			p.recoverToNextBlock()
			block = nil
		}
	}()

	intent := p.maybeParseIntent()

	switch p.peek().Kind {
	case lexer.TokenSecret:
		return p.parseSecret()
	case lexer.TokenEnv:
		return p.parseEnv()
	case lexer.TokenLocale:
		return p.parseLocale()
	case lexer.TokenTranslation:
		return p.parseTranslation()
	case lexer.TokenState:
		return p.parseStateMachine(intent)
	case lexer.TokenAnalytics:
		return p.parseAnalytics(intent)
	case lexer.TokenSave:
		return p.parseSaveSchema(intent)
	case lexer.TokenInclude:
		return p.parseInclude()
	case lexer.TokenType:
		return p.parseTypeDecl()
	case lexer.TokenAlias:
		return p.parseAlias()
	case lexer.TokenEnum:
		return p.parseEnum(intent)
	case lexer.TokenModel:
		return p.parseModel(intent)
	case lexer.TokenContent:
		return p.parseContent(intent)
	case lexer.TokenFn:
		return p.parseFn(intent)
	case lexer.TokenPipe:
		return p.parsePipe(intent)
	case lexer.TokenMiddleware:
		return p.parseMiddleware(intent)
	case lexer.TokenGetMethod, lexer.TokenPostMethod, lexer.TokenPutMethod,
		lexer.TokenPatchMethod, lexer.TokenDeleteMethod:
		return p.parseEndpoint(intent)
	case lexer.TokenStreamMethod:
		return p.parseStreamEndpoint(intent)
	case lexer.TokenWs:
		return p.parseWsEndpoint(intent)
	case lexer.TokenWorker:
		return p.parseWorker(intent)
	case lexer.TokenSchedule:
		return p.parseSchedule(intent)
	case lexer.TokenExternal:
		return p.parseExternal()
	case lexer.TokenSubscribe:
		return p.parseSubscribe(intent)
	case lexer.TokenTest:
		return p.parseTest(intent)
	case lexer.TokenTestGroup:
		return p.parseTestGroup(intent)
	case lexer.TokenFixture:
		return p.parseFixture()
	case lexer.TokenGenerate:
		return p.parseGenerateTopLevel()
	case lexer.TokenEOF:
		return nil
	default:
		p.addError(p.peek().Loc,
			"Unexpected token '"+p.peek().Value+"'",
			"Expected a top-level block (blueprint, model, fn, endpoint, etc.)")
		p.advance()
		return nil
	}
}

// --- Helper Methods ---

func (p *Parser) peek() lexer.Token {
	if p.pos >= len(p.tokens) {
		return lexer.Token{Kind: lexer.TokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) peekAt(offset int) lexer.Token {
	i := p.pos + offset
	if i >= len(p.tokens) {
		return lexer.Token{Kind: lexer.TokenEOF}
	}
	return p.tokens[i]
}

func (p *Parser) advance() lexer.Token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *Parser) check(kind lexer.TokenKind) bool {
	return p.peek().Kind == kind
}

func (p *Parser) expect(kind lexer.TokenKind) lexer.Token {
	tok := p.peek()
	if tok.Kind != kind {
		panic(ParseError{
			Loc:     tok.Loc,
			Message: "Expected '" + kind.String() + "', got '" + tok.Kind.String() + "'",
		})
	}
	return p.advance()
}

func (p *Parser) atEnd() bool {
	return p.peek().Kind == lexer.TokenEOF
}

func (p *Parser) addError(loc lexer.Loc, msg, hint string) {
	// Suppress follow-on parser noise at the exact byte offset where the
	// lexer already reported a problem. Otherwise the user sees both
	// `L001: '|' is not valid alone` and a downstream `Expected '}', got
	// 'Illegal'` pointing at the same character.
	if p.lexOffsets != nil && p.lexOffsets[loc.Offset] {
		return
	}
	p.errors = append(p.errors, ParseError{Loc: loc, Message: msg, Hint: hint})
	if len(p.errors) >= maxErrors {
		panic(ParseError{Loc: loc, Message: "too many errors, stopping"})
	}
}

// addErrorCode is addError with a structured error code (e.g. "P001") so
// `bp explain <code>` can surface the long-form explanation. Sites covered
// today: missing blueprint block (P001).
func (p *Parser) addErrorCode(loc lexer.Loc, code, msg, hint string) {
	if p.lexOffsets != nil && p.lexOffsets[loc.Offset] {
		return
	}
	p.errors = append(p.errors, ParseError{Loc: loc, Message: msg, Hint: hint, Code: code})
	if len(p.errors) >= maxErrors {
		panic(ParseError{Loc: loc, Message: "too many errors, stopping"})
	}
}

// appendRecovered is the single funnel for ParseErrors produced via panic +
// recover (e.g. by `expect`). It applies the same lex-offset suppression as
// addError so that a downstream "Expected '{', got 'Illegal'" raised at the
// exact byte the lexer already flagged doesn't double up.
func (p *Parser) appendRecovered(err ParseError) {
	if p.lexOffsets != nil && p.lexOffsets[err.Loc.Offset] {
		return
	}
	p.errors = append(p.errors, err)
}

func (p *Parser) recoverToNextBlock() {
	for !p.atEnd() {
		if p.isTopLevelStart() {
			return
		}
		p.advance()
	}
}

func (p *Parser) isTopLevelStart() bool {
	kind := p.peek().Kind
	switch kind {
	case lexer.TokenBlueprint, lexer.TokenSecret, lexer.TokenEnv,
		lexer.TokenLocale, lexer.TokenTranslation, lexer.TokenState,
		lexer.TokenAnalytics, lexer.TokenSave,
		lexer.TokenModel, lexer.TokenContent, lexer.TokenFn, lexer.TokenPipe,
		lexer.TokenMiddleware, lexer.TokenWorker, lexer.TokenSchedule,
		lexer.TokenExternal, lexer.TokenSubscribe, lexer.TokenTest,
		lexer.TokenTestGroup, lexer.TokenFixture, lexer.TokenType,
		lexer.TokenAlias, lexer.TokenEnum, lexer.TokenInclude,
		lexer.TokenGetMethod, lexer.TokenPostMethod, lexer.TokenPutMethod,
		lexer.TokenPatchMethod, lexer.TokenDeleteMethod,
		lexer.TokenStreamMethod, lexer.TokenWs,
		lexer.TokenIntent, lexer.TokenGenerate:
		return true
	}
	return false
}

// --- Intent ---

func (p *Parser) maybeParseIntent() *ast.Intent {
	if !p.check(lexer.TokenIntent) {
		return nil
	}
	loc := p.advance().Loc // consume @
	if !p.check(lexer.TokenString) {
		return nil
	}
	text := p.advance()
	return &ast.Intent{Loc: loc, Text: text.Value}
}

// --- Blueprint ---

func (p *Parser) parseBlueprintBlock(intent *ast.Intent) *ast.Blueprint {
	loc := p.expect(lexer.TokenBlueprint).Loc
	name := p.expect(lexer.TokenString)
	p.expect(lexer.TokenLBrace)

	bp := &ast.Blueprint{
		Loc:    loc,
		Intent: intent,
		Name:   name.Value,
	}

	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		if p.check(lexer.TokenUse) {
			bp.Uses = append(bp.Uses, p.parseUseStmt())
		} else if p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent() {
			kv := p.parseKVPair()
			bp.Entries = append(bp.Entries, kv)
		} else {
			break
		}
	}

	p.expect(lexer.TokenRBrace)
	return bp
}

// --- Declarations ---

func (p *Parser) parseSecret() *ast.Secret {
	loc := p.expect(lexer.TokenSecret).Loc
	name := p.expectIdent()

	s := &ast.Secret{Loc: loc, Name: name}

	if p.check(lexer.TokenRequired) {
		p.advance()
		s.Required = true
	} else if p.check(lexer.TokenOptional) {
		p.advance()
		s.Required = false
	}

	// default(value)
	if p.check(lexer.TokenDefault) {
		p.advance()
		p.expect(lexer.TokenLParen)
		s.Default = p.parseExpr()
		p.expect(lexer.TokenRParen)
	}

	return s
}

func (p *Parser) parseEnv() *ast.Env {
	loc := p.expect(lexer.TokenEnv).Loc
	name := p.expectIdent()
	value := p.parseExpr()
	return &ast.Env{Loc: loc, Name: name, Value: value}
}

func (p *Parser) parseLocale() *ast.Locale {
	loc := p.expect(lexer.TokenLocale).Loc
	code := p.parseLocaleCode()
	l := &ast.Locale{Loc: loc, Code: code}
	for !p.atEnd() {
		if p.check(lexer.TokenDefault) {
			p.advance()
			l.Default = true
			continue
		}
		if (p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent()) && p.peek().Value == "fallback" {
			p.advance()
			p.expect(lexer.TokenLParen)
			l.Fallback = p.parseLocaleCode()
			p.expect(lexer.TokenRParen)
			continue
		}
		break
	}
	return l
}

func (p *Parser) parseTranslation() *ast.Translation {
	loc := p.expect(lexer.TokenTranslation).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)
	t := &ast.Translation{Loc: loc, Name: name}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		if (p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent()) && p.peek().Value == "key" {
			p.advance()
			key := p.expect(lexer.TokenString)
			t.Keys = append(t.Keys, key.Value)
			continue
		}
		if p.check(lexer.TokenLocale) {
			t.Bundles = append(t.Bundles, p.parseTranslationBundle())
			continue
		}
		panic(ParseError{Loc: p.peek().Loc, Message: fmt.Sprintf("unexpected token %q in translation block", p.peek().Value), Hint: `Use lines like: key "mission.start"`})
	}
	p.expect(lexer.TokenRBrace)
	return t
}

func (p *Parser) parseStateMachine(intent *ast.Intent) *ast.StateMachine {
	loc := p.expect(lexer.TokenState).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)
	sm := &ast.StateMachine{Loc: loc, Intent: intent, Name: name}
	seenStates := map[string]bool{}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		from := p.expectIdent()
		p.expect(lexer.TokenArrowOut)
		to := p.expectIdent()
		sm.Transitions = append(sm.Transitions, &ast.StateTransition{Loc: p.peek().Loc, From: from, To: to})
		if !seenStates[from] {
			sm.States = append(sm.States, from)
			seenStates[from] = true
		}
		if !seenStates[to] {
			sm.States = append(sm.States, to)
			seenStates[to] = true
		}
	}
	p.expect(lexer.TokenRBrace)
	return sm
}

func (p *Parser) parseAnalytics(intent *ast.Intent) *ast.Analytics {
	loc := p.expect(lexer.TokenAnalytics).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)
	a := &ast.Analytics{Loc: loc, Intent: intent, Name: name}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		if (p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent()) && p.peek().Value == "event" {
			p.advance()
			a.Events = append(a.Events, p.expectIdent())
			continue
		}
		if (p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent()) && p.peek().Value == "sink" {
			p.advance()
			kindLoc := p.peek().Loc
			kind := p.expectIdent()
			sink := &ast.AnalyticsSink{Loc: kindLoc, Kind: kind}
			if p.check(lexer.TokenLParen) {
				p.advance()
				sink.Target = p.parseExpr()
				p.expect(lexer.TokenRParen)
			}
			a.Sinks = append(a.Sinks, sink)
			continue
		}
		panic(ParseError{Loc: p.peek().Loc, Message: fmt.Sprintf("unexpected token %q in analytics block", p.peek().Value), Hint: `Use lines like: event mission_started or sink http("https://...")`})
	}
	p.expect(lexer.TokenRBrace)
	return a
}

func (p *Parser) parseSaveSchema(intent *ast.Intent) *ast.SaveSchema {
	loc := p.expect(lexer.TokenSave).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)
	s := &ast.SaveSchema{Loc: loc, Intent: intent, Name: name}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		key := p.expectIdent()
		switch key {
		case "model":
			s.Model = p.expectIdent()
		case "version_field":
			s.VersionField = p.expectIdent()
		case "latest":
			tok := p.expect(lexer.TokenInt)
			n, err := strconv.Atoi(tok.Value)
			if err != nil {
				panic(ParseError{Loc: tok.Loc, Message: fmt.Sprintf("invalid latest version %q", tok.Value), Hint: "latest expects an integer"})
			}
			s.Latest = n
		case "migrate":
			mig := &ast.SaveMigration{Loc: p.peek().Loc}
			fromTok := p.expect(lexer.TokenInt)
			from, err := strconv.Atoi(fromTok.Value)
			if err != nil {
				panic(ParseError{Loc: fromTok.Loc, Message: fmt.Sprintf("invalid migration from version %q", fromTok.Value), Hint: "migrate expects integer versions"})
			}
			mig.From = from
			p.expect(lexer.TokenArrowOut)
			toTok := p.expect(lexer.TokenInt)
			to, err := strconv.Atoi(toTok.Value)
			if err != nil {
				panic(ParseError{Loc: toTok.Loc, Message: fmt.Sprintf("invalid migration to version %q", toTok.Value), Hint: "migrate expects integer versions"})
			}
			mig.To = to
			if p.check(lexer.TokenUsing) {
				p.advance()
				mig.Module = p.expect(lexer.TokenString).Value
			}
			s.Migrations = append(s.Migrations, mig)
		default:
			panic(ParseError{Loc: p.peek().Loc, Message: fmt.Sprintf("unexpected entry %q in save block", key), Hint: "Use model <name>, version_field <field>, latest <int>, migrate <from> -> <to> [using \"./module\"]"})
		}
	}
	p.expect(lexer.TokenRBrace)
	return s
}

func (p *Parser) parseTranslationBundle() *ast.TranslationBundle {
	loc := p.expect(lexer.TokenLocale).Loc
	locale := p.parseLocaleCode()
	p.expect(lexer.TokenLBrace)
	b := &ast.TranslationBundle{Loc: loc, Locale: locale}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		kv := p.parseStringKVPair()
		b.Values = append(b.Values, kv)
	}
	p.expect(lexer.TokenRBrace)
	return b
}

func (p *Parser) parseLocaleCode() string {
	if p.check(lexer.TokenString) {
		return p.advance().Value
	}
	return p.expectIdent()
}

func (p *Parser) parseInclude() *ast.Include {
	loc := p.expect(lexer.TokenInclude).Loc
	path := p.expect(lexer.TokenString)
	return &ast.Include{Loc: loc, Path: path.Value}
}

func (p *Parser) parseTypeDecl() *ast.TypeDecl {
	loc := p.expect(lexer.TokenType).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	td := &ast.TypeDecl{Loc: loc, Name: name}

	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		f := p.parseField()
		td.Fields = append(td.Fields, f)
	}

	p.expect(lexer.TokenRBrace)
	return td
}

func (p *Parser) parseAlias() *ast.Alias {
	loc := p.expect(lexer.TokenAlias).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenAssign)
	typ := p.parseTypeExpr()

	a := &ast.Alias{Loc: loc, Name: name, Type: typ}

	for p.isConstraintStart() {
		c := p.parseConstraint()
		a.Constraints = append(a.Constraints, c)
	}

	return a
}

func (p *Parser) parseEnum(intent *ast.Intent) *ast.Enum {
	loc := p.expect(lexer.TokenEnum).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	e := &ast.Enum{Loc: loc, Intent: intent, Name: name}

	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		vLoc := p.peek().Loc
		vName := p.expectIdent()
		var body *ast.BlockBody
		if p.check(lexer.TokenLBrace) {
			body = p.parseBlockBody()
		}
		e.Variants = append(e.Variants, &ast.EnumVariant{Loc: vLoc, Name: vName, Body: body})
	}

	p.expect(lexer.TokenRBrace)
	return e
}

// --- Model ---

func (p *Parser) parseModel(intent *ast.Intent) *ast.Model {
	loc := p.expect(lexer.TokenModel).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	m := &ast.Model{Loc: loc, Intent: intent, Name: name}

	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		if p.check(lexer.TokenIdent) && p.peek().Value == "computed" {
			if !p.computedFieldHasAssignment() {
				panic(ParseError{
					Loc:     p.peek().Loc,
					Message: "model field name 'computed' is reserved for computed declarations",
					Hint:    "Rename the persisted field, or use: computed <name> <type> = <expression>",
				})
			}
			m.ComputedFields = append(m.ComputedFields, p.parseComputedField())
			continue
		}
		f := p.parseField()
		m.Fields = append(m.Fields, f)
	}

	p.expect(lexer.TokenRBrace)
	return m
}

func (p *Parser) computedFieldHasAssignment() bool {
	line := p.peek().Loc.Line
	for offset := 1; ; offset++ {
		token := p.peekAt(offset)
		if token.Kind == lexer.TokenEOF || token.Kind == lexer.TokenRBrace || token.Loc.Line != line {
			return false
		}
		if token.Kind == lexer.TokenAssign {
			return true
		}
	}
}

func (p *Parser) parseContent(intent *ast.Intent) *ast.Content {
	loc := p.expect(lexer.TokenContent).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	c := &ast.Content{Loc: loc, Intent: intent, Name: name}

	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		f := p.parseField()
		c.Fields = append(c.Fields, f)
	}

	p.expect(lexer.TokenRBrace)
	return c
}

// --- Function ---

func (p *Parser) parseFn(intent *ast.Intent) *ast.Fn {
	loc := p.expect(lexer.TokenFn).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	fn := &ast.Fn{Loc: loc, Intent: intent, Name: name}

	// Parse inputs
	for p.check(lexer.TokenArrowIn) {
		fn.Inputs = append(fn.Inputs, p.parseInputStmt())
	}

	// Parse output(s) — fn outputs are type expressions, not status+value
	for p.check(lexer.TokenArrowOut) {
		fn.Outputs = append(fn.Outputs, p.parseFnOutputStmt())
	}

	// Parse impl or logic
	if p.check(lexer.TokenImpl) {
		fn.Impl = p.parseImplBlock()
	} else if p.check(lexer.TokenLogic) {
		fn.Logic = p.parseLogicBlock()
	}

	p.expect(lexer.TokenRBrace)
	return fn
}

func (p *Parser) parseFnOutputStmt() *ast.OutputStmt {
	loc := p.expect(lexer.TokenArrowOut).Loc
	out := &ast.OutputStmt{Loc: loc}

	// fn outputs: "-> name type" or "-> type"
	// e.g., "-> file image/*" or "-> bool" or "-> string"
	if !p.atEnd() && !p.check(lexer.TokenRBrace) && !p.check(lexer.TokenImpl) &&
		!p.check(lexer.TokenLogic) && !p.check(lexer.TokenArrowIn) {
		// Check for "name type" pattern: non-primitive ident followed by a type-ish token
		if p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent() {
			next := p.peekAt(1)
			// If next token looks like a type (ident/slash for MIME), it's "name type"
			if next.Kind == lexer.TokenSlash ||
				(next.Kind == lexer.TokenIdent && p.isTypeValueAt(1)) ||
				next.Kind == lexer.TokenEnum {
				p.advance() // skip name
				typ := p.parseTypeExpr()
				out.Value = typeExprToExpr(typ)
				return out
			}
		}
		// Single type or expression
		if p.isTypeStart() {
			typ := p.parseTypeExpr()
			out.Value = typeExprToExpr(typ)
		}
	}

	return out
}

func (p *Parser) isTypeValueAt(offset int) bool {
	v := p.peekAt(offset).Value
	switch v {
	case "string", "int", "float", "bool", "uuid", "timestamp", "json", "file", "money",
		"image", "application", "video", "text", "audio":
		return true
	}
	return false
}

func typeExprToExpr(typ ast.TypeExpr) ast.Expr {
	switch t := typ.(type) {
	case *ast.PrimitiveType:
		return &ast.Ident{Loc: t.Loc, Name: t.Name}
	case *ast.TypedJSONType:
		return &ast.Ident{Loc: t.Loc, Name: "json"}
	case *ast.TranslationKeyType:
		return &ast.Ident{Loc: t.Loc, Name: t.Namespace}
	case *ast.NamedType:
		return &ast.Ident{Loc: t.Loc, Name: t.Name}
	case *ast.MimeTypeExpr:
		return &ast.Ident{Loc: t.Loc, Name: t.Type + "/" + t.Subtype}
	default:
		return &ast.Ident{Loc: typ.Location(), Name: "unknown"}
	}
}

func (p *Parser) parseImplBlock() *ast.ImplBlock {
	loc := p.expect(lexer.TokenImpl).Loc
	strategy := p.expectIdent()
	body := p.parseBlockBody()
	return &ast.ImplBlock{Loc: loc, Strategy: strategy, Entries: body.Entries}
}

func (p *Parser) parseLogicBlock() *ast.LogicBlock {
	loc := p.expect(lexer.TokenLogic).Loc
	p.expect(lexer.TokenLBrace)

	lb := &ast.LogicBlock{Loc: loc}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		s := p.parseArrowStmt()
		if s != nil {
			lb.Stmts = append(lb.Stmts, s)
		}
	}

	p.expect(lexer.TokenRBrace)
	return lb
}

// --- Pipe ---

func (p *Parser) parsePipe(intent *ast.Intent) *ast.Pipe {
	loc := p.expect(lexer.TokenPipe).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	pipe := &ast.Pipe{Loc: loc, Intent: intent, Name: name}

	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		s := p.parseArrowStmt()
		if s != nil {
			pipe.Stmts = append(pipe.Stmts, s)
		}
	}

	p.expect(lexer.TokenRBrace)
	return pipe
}

// --- Middleware ---

func (p *Parser) parseMiddleware(intent *ast.Intent) *ast.Middleware {
	loc := p.expect(lexer.TokenMiddleware).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	mw := &ast.Middleware{Loc: loc, Intent: intent, Name: name}

	if p.check(lexer.TokenBefore) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				mw.Before = append(mw.Before, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	if p.check(lexer.TokenAfter) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				mw.After = append(mw.After, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	// Remaining entries (for config-style middleware like cors)
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		if p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent() {
			kv := p.parseKVPair()
			mw.Entries = append(mw.Entries, kv)
		} else {
			break
		}
	}

	p.expect(lexer.TokenRBrace)
	return mw
}

// --- Endpoints ---

func (p *Parser) parseEndpoint(intent *ast.Intent) *ast.Endpoint {
	methodTok := p.advance()
	method := methodTok.Value
	pathTok := p.expect(lexer.TokenPath)
	p.expect(lexer.TokenLBrace)

	ep := &ast.Endpoint{
		Loc:    methodTok.Loc,
		Intent: intent,
		Method: method,
		Path:   pathTok.Value,
	}

	// Parse metadata (use, auth, limit, cache, tags, timeout)
	for p.isEndpointMetaStart() {
		meta := p.parseEndpointMeta()
		ep.Meta = append(ep.Meta, meta)
	}

	// Parse arrow statements
	for p.isArrowStart() {
		s := p.parseArrowStmt()
		if s != nil {
			ep.Stmts = append(ep.Stmts, s)
		}
	}

	// Parse on_error
	if p.check(lexer.TokenOnError) {
		ep.OnError = p.parseOnError()
	}

	p.expect(lexer.TokenRBrace)
	return ep
}

func (p *Parser) parseStreamEndpoint(intent *ast.Intent) *ast.StreamEndpoint {
	loc := p.expect(lexer.TokenStreamMethod).Loc
	pathTok := p.expect(lexer.TokenPath)
	p.expect(lexer.TokenLBrace)

	ep := &ast.StreamEndpoint{
		Loc:    loc,
		Intent: intent,
		Path:   pathTok.Value,
	}

	for p.isEndpointMetaStart() {
		meta := p.parseEndpointMeta()
		ep.Meta = append(ep.Meta, meta)
	}

	for p.isArrowStart() && !p.check(lexer.TokenStream) {
		s := p.parseArrowStmt()
		if s != nil {
			ep.Stmts = append(ep.Stmts, s)
		}
	}

	// Parse stream block
	if p.check(lexer.TokenStream) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			h := p.parseStreamHandler()
			if h != nil {
				ep.Handlers = append(ep.Handlers, h)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	p.expect(lexer.TokenRBrace)
	return ep
}

func (p *Parser) parseWsEndpoint(intent *ast.Intent) *ast.WsEndpoint {
	loc := p.expect(lexer.TokenWs).Loc
	pathTok := p.expect(lexer.TokenPath)
	p.expect(lexer.TokenLBrace)

	ep := &ast.WsEndpoint{
		Loc:    loc,
		Intent: intent,
		Path:   pathTok.Value,
	}

	for p.isEndpointMetaStart() {
		meta := p.parseEndpointMeta()
		ep.Meta = append(ep.Meta, meta)
	}

	if p.check(lexer.TokenOnConnect) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				ep.OnConnect = append(ep.OnConnect, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	if p.check(lexer.TokenOnMessage) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				ep.OnMessage = append(ep.OnMessage, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	if p.check(lexer.TokenOnDisconnect) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				ep.OnDisconnect = append(ep.OnDisconnect, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	p.expect(lexer.TokenRBrace)
	return ep
}

// --- Worker ---

func (p *Parser) parseWorker(intent *ast.Intent) *ast.Worker {
	loc := p.expect(lexer.TokenWorker).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	w := &ast.Worker{Loc: loc, Intent: intent, Name: name}

	// Parse worker meta (trigger, retry, timeout)
	for p.isWorkerMetaStart() {
		meta := p.parseWorkerMeta()
		w.Meta = append(w.Meta, meta)
	}

	// Parse arrow statements
	for p.isArrowStart() && !p.check(lexer.TokenOnFail) {
		s := p.parseArrowStmt()
		if s != nil {
			w.Stmts = append(w.Stmts, s)
		}
	}

	// Parse on_fail
	if p.check(lexer.TokenOnFail) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				w.OnFail = append(w.OnFail, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	p.expect(lexer.TokenRBrace)
	return w
}

// --- Schedule ---

func (p *Parser) parseSchedule(intent *ast.Intent) *ast.Schedule {
	loc := p.expect(lexer.TokenSchedule).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	s := &ast.Schedule{Loc: loc, Intent: intent, Name: name}

	// Parse cron
	if p.check(lexer.TokenCronKw) {
		p.advance()
		cronStr := p.expect(lexer.TokenString)
		s.Cron = cronStr.Value
	}

	// Parse arrow stmts
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		stmt := p.parseArrowStmt()
		if stmt != nil {
			s.Stmts = append(s.Stmts, stmt)
		}
	}

	p.expect(lexer.TokenRBrace)
	return s
}

// --- External ---

func (p *Parser) parseExternal() *ast.External {
	loc := p.expect(lexer.TokenExternal).Loc
	name := p.expect(lexer.TokenString)
	body := p.parseBlockBody()
	return &ast.External{Loc: loc, Name: name.Value, Entries: body.Entries}
}

// --- Subscribe ---

func (p *Parser) parseSubscribe(intent *ast.Intent) *ast.Subscribe {
	loc := p.expect(lexer.TokenSubscribe).Loc
	event := p.expect(lexer.TokenString)

	var from string
	if p.check(lexer.TokenFrom) {
		p.advance()
		p.expect(lexer.TokenLParen)
		from = p.expectIdent()
		p.expect(lexer.TokenRParen)
	}

	p.expect(lexer.TokenLBrace)

	sub := &ast.Subscribe{Loc: loc, Intent: intent, Event: event.Value, From: from}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		s := p.parseArrowStmt()
		if s != nil {
			sub.Stmts = append(sub.Stmts, s)
		}
	}

	p.expect(lexer.TokenRBrace)
	return sub
}

// --- Test ---

func (p *Parser) parseTest(intent *ast.Intent) *ast.Test {
	loc := p.expect(lexer.TokenTest).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	t := &ast.Test{Loc: loc, Intent: intent, Name: name}

	// target
	if p.check(lexer.TokenTarget) {
		p.advance()
		tLoc := p.peek().Loc
		method := p.advance() // METHOD token
		path := p.expect(lexer.TokenPath)
		t.Target = &ast.TestTarget{Loc: tLoc, Method: method.Value, Path: path.Value}
	}

	// setup
	if p.check(lexer.TokenSetup) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				t.Setup = append(t.Setup, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	// request
	if p.check(lexer.TokenRequest) {
		rLoc := p.advance().Loc
		var repeat int
		if p.check(lexer.TokenRepeat) {
			p.advance()
			p.expect(lexer.TokenLParen)
			rpt := p.expect(lexer.TokenInt)
			p.expect(lexer.TokenRParen)
			var err error
			repeat, err = strconv.Atoi(rpt.Value)
			if err != nil {
				panic(ParseError{
					Loc:     rpt.Loc,
					Message: fmt.Sprintf("invalid integer for repeat(): %s", rpt.Value),
					Hint:    "repeat() expects a positive integer",
				})
			}
		}
		p.expect(lexer.TokenLBrace)
		var entries []ast.KVPair
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			kv := p.parseKVPair()
			entries = append(entries, kv)
		}
		p.expect(lexer.TokenRBrace)
		t.Request = &ast.TestRequest{Loc: rLoc, Repeat: repeat, Entries: entries}
	}

	// expect
	if p.check(lexer.TokenExpect) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			a := p.parseAssertion()
			if a != nil {
				t.Expect = append(t.Expect, a)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	// cleanup
	if p.check(lexer.TokenCleanup) {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				t.Cleanup = append(t.Cleanup, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	p.expect(lexer.TokenRBrace)
	return t
}

func (p *Parser) parseTestGroup(intent *ast.Intent) *ast.TestGroup {
	loc := p.expect(lexer.TokenTestGroup).Loc
	name := p.expectIdent()
	p.expect(lexer.TokenLBrace)

	tg := &ast.TestGroup{Loc: loc, Intent: intent, Name: name}

	// shared_setup
	if p.check(lexer.TokenSetup) || (p.check(lexer.TokenIdent) && p.peek().Value == "shared_setup") {
		p.advance()
		p.expect(lexer.TokenLBrace)
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				tg.SharedSetup = append(tg.SharedSetup, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	// tests [name1, name2, ...]
	if p.check(lexer.TokenIdent) && p.peek().Value == "tests" {
		p.advance()
		p.expect(lexer.TokenLBracket)
		for !p.check(lexer.TokenRBracket) && !p.atEnd() {
			name := p.expectIdent()
			tg.Tests = append(tg.Tests, name)
			if p.check(lexer.TokenComma) {
				p.advance()
			}
		}
		p.expect(lexer.TokenRBracket)
	}

	p.expect(lexer.TokenRBrace)
	return tg
}

func (p *Parser) parseFixture() *ast.Fixture {
	loc := p.expect(lexer.TokenFixture).Loc
	name := p.expect(lexer.TokenString)

	f := &ast.Fixture{Loc: loc, Name: name.Value}

	if p.check(lexer.TokenFrom) {
		p.advance()
		path := p.expect(lexer.TokenString)
		f.FromPath = path.Value
	} else if p.check(lexer.TokenGenerated) {
		p.advance()
		f.Generated = p.parseBlockBody()
	} else if p.check(lexer.TokenSeed) {
		p.advance()
		modelName := p.expectIdent()
		f.SeedModel = modelName
		f.SeedBody = p.parseBlockBody()
	}

	return f
}

func (p *Parser) parseGenerateTopLevel() ast.TopLevel {
	loc := p.peek().Loc
	p.addError(loc,
		"@> generate steps can only appear inside endpoint or function bodies",
		"Move this @> step inside a route, function, or pipe block",
	)
	p.advance() // consume @>
	// Skip the string and any hint arguments to recover gracefully
	if p.check(lexer.TokenString) {
		p.advance()
	}
	for p.check(lexer.TokenIdent) {
		p.advance()
		if p.check(lexer.TokenLParen) {
			p.advance()
			p.parseExpr()
			if p.check(lexer.TokenRParen) {
				p.advance()
			}
		}
	}
	return nil
}

// --- Arrow Statements ---

func (p *Parser) parseArrowStmt() ast.ArrowStmt {
	switch p.peek().Kind {
	case lexer.TokenArrowIn:
		return p.parseInputStmt()
	case lexer.TokenArrowPipe:
		return p.parseStepOrGuardOrWhenOrTry()
	case lexer.TokenArrowOut:
		return p.parseOutputStmt()
	case lexer.TokenGenerate:
		return p.parseGenerateStepStmt()
	default:
		if p.isControlFlowKeyword() {
			p.reportControlFlowStmt()
			return nil
		}
		p.addError(p.peek().Loc,
			"Expected arrow statement (<-, |>, ->, or @>)",
			"Arrow statements start with <-, |>, ->, or @>")
		p.advance()
		return nil
	}
}

// isControlFlowKeyword reports whether the current token is one of Blueprint's
// forbidden control-flow keywords (if/else/for/while/switch) appearing where
// a step is expected. These aren't reserved words in the lexer — they're
// plain identifiers — so this check is scoped to statement/step position
// only (here and in parseStepOrGuardOrWhenOrTry), not to identifiers in
// general.
func (p *Parser) isControlFlowKeyword() bool {
	return p.peek().Kind == lexer.TokenIdent && controlFlowKeywords[p.peek().Value]
}

// reportControlFlowStmt emits the dedicated "Blueprint has no if/else or
// loops" diagnostic for a stray control-flow keyword in step position, then
// consumes the whole malformed construct (condition/iterable + balanced
// `{ ... }` body, plus any chained `else`/`else if`) itself rather than
// panicking. This bounds the blast radius: panic-mode recovery elsewhere
// (recoverToNextBlock) skips forward to the next *top-level* construct
// start, which can swallow a following `|> save x { ... }` step and
// misparse it as a top-level `save` schema block. Consuming the construct
// here means parsing resumes at the very next real step/output/closing
// brace instead.
func (p *Parser) reportControlFlowStmt() {
	tok := p.peek()
	p.addErrorCode(tok.Loc, CodeControlFlowKeyword,
		fmt.Sprintf("Blueprint has no '%s' — it has no if/else or loops", tok.Value),
		`Use '|> guard <cond> -> <status> "msg"' for early return, `+
			"'|> when <cond>: <step>' (or '|> when <cond> { ... }') for a conditional step, "+
			"and 'map' for iteration.")

	for {
		p.skipControlFlowConstruct()
		if p.peek().Kind == lexer.TokenIdent && p.peek().Value == "else" {
			p.advance() // consume "else"; a following "if" is just more condition
			// tokens to skipControlFlowConstruct, which stops at the next '{'.
			continue
		}
		break
	}
}

// skipControlFlowConstruct consumes one `<condition/iterable> { <body> }`
// unit starting at the current position (assumed to be right after a
// control-flow keyword or "else"). Braces inside the body are balanced, so
// arbitrarily nested blocks (including legitimate nested steps) are skipped
// as a whole. If no '{' is found before an arrow-statement start, '}', or
// EOF, it stops there instead of running away.
func (p *Parser) skipControlFlowConstruct() {
	for !p.atEnd() && !p.check(lexer.TokenLBrace) {
		if p.isArrowStart() || p.check(lexer.TokenRBrace) {
			return
		}
		p.advance()
	}
	if !p.check(lexer.TokenLBrace) {
		return
	}
	depth := 0
	for !p.atEnd() {
		switch p.peek().Kind {
		case lexer.TokenLBrace:
			depth++
			p.advance()
		case lexer.TokenRBrace:
			depth--
			p.advance()
			if depth == 0 {
				return
			}
		default:
			p.advance()
		}
	}
}

func (p *Parser) parseInputStmt() *ast.InputStmt {
	loc := p.expect(lexer.TokenArrowIn).Loc
	name := p.expectIdent()

	var typ ast.TypeExpr
	if p.isTypeStart() {
		typ = p.parseTypeExpr()
	}

	input := &ast.InputStmt{Loc: loc, Name: name, Type: typ}

	for p.isConstraintStart() {
		c := p.parseConstraint()
		input.Constraints = append(input.Constraints, c)
	}

	return input
}

func (p *Parser) parseStepOrGuardOrWhenOrTry() ast.ArrowStmt {
	loc := p.expect(lexer.TokenArrowPipe).Loc

	switch p.peek().Kind {
	case lexer.TokenGuard:
		return p.parseGuardStmt(loc)
	case lexer.TokenWhen:
		return p.parseWhenStmt(loc)
	case lexer.TokenTry:
		return p.parseTryRecover(loc)
	case lexer.TokenIntent:
		// |> @ "intent text"
		p.advance() // @
		text := p.expect(lexer.TokenString)
		return &ast.IntentStep{Loc: loc, Text: text.Value}
	case lexer.TokenArrowOut:
		// |> -> expr (return in logic blocks)
		return p.parseOutputStmt()
	default:
		if p.isControlFlowKeyword() {
			p.reportControlFlowStmt()
			return nil
		}
		return p.parseStepStmt(loc)
	}
}

func (p *Parser) parseStepStmt(loc lexer.Loc) *ast.StepStmt {
	stmt := &ast.StepStmt{Loc: loc}

	// Check for binding: ident = expr
	if (p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent()) && p.peekAt(1).Kind == lexer.TokenAssign {
		stmt.Binding = p.advance().Value
		p.advance() // consume =
	}

	stmt.Expr = p.parseStepExpr()
	return stmt
}

// parseStepExpr parses an expression in step context, which can include
// Blueprint operation keywords like query, save, update, delete, fetch, etc.
// It also handles field assignments like "filters.status = status".
func (p *Parser) parseStepExpr() ast.Expr {
	tok := p.peek()

	// Handle Blueprint operations that have special multi-token syntax
	switch tok.Kind {
	case lexer.TokenQuery, lexer.TokenFetch, lexer.TokenSave,
		lexer.TokenUpdate, lexer.TokenDelete, lexer.TokenCount,
		lexer.TokenSeed:
		return p.parseDataOperation()
	case lexer.TokenCall:
		return p.parseCallOperation()
	case lexer.TokenEmit:
		return p.parseEmitOperation()
	case lexer.TokenEnqueue:
		return p.parseEnqueueOperation()
	case lexer.TokenLog:
		return p.parseLogOperation()
	case lexer.TokenSleep:
		return p.parseSleepOperation()
	case lexer.TokenUpload, lexer.TokenDownload:
		return p.parseExpr() // These look like function calls: upload(file, bucket)
	case lexer.TokenMap:
		return p.parseMapOperation()
	case lexer.TokenInject:
		return p.parseInjectOperation()
	case lexer.TokenPipe:
		return p.parsePipeCall()
	case lexer.TokenJoin, lexer.TokenLeave:
		return p.parseRoomTargetOp()
	case lexer.TokenBroadcast, lexer.TokenWhisper:
		return p.parseBroadcastOp()
	case lexer.TokenClose:
		loc := p.advance().Loc
		return &ast.Ident{Loc: loc, Name: "close"}
	default:
		expr := p.parseExpr()
		// Check for field assignment: expr = value (e.g., filters.status = status)
		if p.check(lexer.TokenAssign) {
			p.advance()
			val := p.parseStepExpr()
			return &ast.BinaryExpr{Loc: expr.Location(), Op: "=", Left: expr, Right: val}
		}
		return expr
	}
}

// parseRoomTargetOp parses: join room(id)  or  leave room(id)
func (p *Parser) parseRoomTargetOp() ast.Expr {
	loc := p.peek().Loc
	op := p.advance().Value   // "join" or "leave"
	target := p.expectIdent() // "room" or "connection"
	var targetArgs []ast.Expr
	if p.check(lexer.TokenLParen) {
		p.advance()
		targetArgs = append(targetArgs, p.parseExpr())
		p.expect(lexer.TokenRParen)
	}
	return &ast.FnCall{Loc: loc, Name: op, Args: []ast.Expr{
		&ast.FnCall{Loc: loc, Name: target, Args: targetArgs},
	}}
}

// parseBroadcastOp parses: broadcast room(id) { data }  or  whisper connection(id) { data }
func (p *Parser) parseBroadcastOp() ast.Expr {
	loc := p.peek().Loc
	op := p.advance().Value   // "broadcast" or "whisper"
	target := p.expectIdent() // "room" or "connection"
	var targetArgs []ast.Expr
	if p.check(lexer.TokenLParen) {
		p.advance()
		targetArgs = append(targetArgs, p.parseExpr())
		p.expect(lexer.TokenRParen)
	}
	args := []ast.Expr{&ast.FnCall{Loc: loc, Name: target, Args: targetArgs}}
	if p.check(lexer.TokenLBrace) {
		args = append(args, p.parseBlockExpr())
	}
	return &ast.FnCall{Loc: loc, Name: op, Args: args}
}

func (p *Parser) parseDataOperation() ast.Expr {
	loc := p.peek().Loc
	op := p.advance().Value // query, fetch, save, update, delete, count

	args := []ast.Expr{&ast.Ident{Loc: loc, Name: op}}

	// Model name
	if p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent() {
		modelLoc := p.peek().Loc
		modelName := p.expectIdent()
		args = append(args, &ast.Ident{Loc: modelLoc, Name: modelName})
	}

	// Optional: (id) for fetch
	if p.check(lexer.TokenLParen) && op == "fetch" {
		p.advance()
		args = append(args, p.parseExpr())
		p.expect(lexer.TokenRParen)
	}

	// Optional block body { field: value }
	if p.check(lexer.TokenLBrace) {
		block := p.parseBlockExpr()
		args = append(args, block)
	}

	// Data-operation modifiers are order-independent. The checker owns
	// duplicate/operation-specific validation; the parser preserves source
	// order so formatting and downstream resolution remain deterministic.
	for {
		switch {
		case p.check(lexer.TokenWhere):
			args = append(args, p.parseDataOperationMarker("where"))
		case p.check(lexer.TokenOrder):
			args = append(args, p.parseDataOperationMarker("order"))
		case p.check(lexer.TokenPaginate):
			args = append(args, p.parseDataOperationMarker("paginate"))
		case p.check(lexer.TokenIdent) && p.peek().Value == "with" && p.peekAt(1).Kind == lexer.TokenLParen:
			args = append(args, p.parseDataOperationMarker("with"))
		case p.check(lexer.TokenFirst):
			firstLoc := p.advance().Loc
			args = append(args, &ast.Ident{Loc: firstLoc, Name: "first"})
		default:
			return &ast.FnCall{Loc: loc, Name: op, Args: args[1:]}
		}
	}
}

func (p *Parser) parseDataOperationMarker(name string) *ast.FnCall {
	loc := p.advance().Loc
	p.expect(lexer.TokenLParen)
	var markerArgs []ast.Expr
	for !p.check(lexer.TokenRParen) && !p.atEnd() {
		markerArgs = append(markerArgs, p.parseExpr())
		if p.check(lexer.TokenComma) {
			p.advance()
		}
	}
	p.expect(lexer.TokenRParen)
	return &ast.FnCall{Loc: loc, Name: name, Args: markerArgs}
}

func (p *Parser) parseCallOperation() ast.Expr {
	loc := p.expect(lexer.TokenCall).Loc
	var args []ast.Expr

	// Service name (may contain hyphens — parsed as ident)
	serviceLoc := p.peek().Loc
	service := p.expectIdent()
	args = append(args, &ast.Ident{Loc: serviceLoc, Name: service})

	// METHOD
	if lexer.IsHTTPMethod(p.peek().Kind) {
		methodLoc := p.peek().Loc
		method := p.advance().Value
		args = append(args, &ast.Ident{Loc: methodLoc, Name: method})
	}

	// PATH
	if p.check(lexer.TokenPath) {
		pathLoc := p.peek().Loc
		path := p.advance().Value
		args = append(args, &ast.PathExpr{Loc: pathLoc, Value: path})
	}

	// Optional body
	if p.check(lexer.TokenLBrace) {
		args = append(args, p.parseBlockExpr())
	}

	return &ast.FnCall{Loc: loc, Name: "call", Args: args}
}

func (p *Parser) parseEmitOperation() ast.Expr {
	loc := p.expect(lexer.TokenEmit).Loc
	var args []ast.Expr

	// Event name
	if p.check(lexer.TokenString) {
		args = append(args, &ast.StringLit{Loc: p.peek().Loc, Value: p.advance().Value})
	} else {
		eventLoc := p.peek().Loc
		event := p.expectIdent()
		args = append(args, &ast.Ident{Loc: eventLoc, Name: event})
	}

	// Optional to(service)
	if p.check(lexer.TokenTo) {
		p.advance()
		p.expect(lexer.TokenLParen)
		toLoc := p.peek().Loc
		to := p.expectIdent()
		args = append(args, &ast.Ident{Loc: toLoc, Name: to})
		p.expect(lexer.TokenRParen)
	}

	// Optional body { data }
	if p.check(lexer.TokenLBrace) {
		args = append(args, p.parseBlockExpr())
	}

	return &ast.FnCall{Loc: loc, Name: "emit", Args: args}
}

// parseEnqueueOperation parses `enqueue queue_name { data }` — enqueues a
// job to a BullMQ queue. The queue name can be a string literal or an ident
// matching a worker's trigger queue("name"). The body { data } is required
// and becomes the job payload.
func (p *Parser) parseEnqueueOperation() ast.Expr {
	loc := p.expect(lexer.TokenEnqueue).Loc
	var args []ast.Expr

	// Queue name (string literal or ident)
	if p.check(lexer.TokenString) {
		args = append(args, &ast.StringLit{Loc: p.peek().Loc, Value: p.advance().Value})
	} else {
		nameLoc := p.peek().Loc
		name := p.expectIdent()
		args = append(args, &ast.Ident{Loc: nameLoc, Name: name})
	}

	// Required body { data } — the job payload
	if p.check(lexer.TokenLBrace) {
		args = append(args, p.parseBlockExpr())
	}

	return &ast.FnCall{Loc: loc, Name: "enqueue", Args: args}
}

func (p *Parser) parseLogOperation() ast.Expr {
	loc := p.expect(lexer.TokenLog).Loc
	var args []ast.Expr

	// Message (string)
	if p.check(lexer.TokenString) {
		args = append(args, &ast.StringLit{Loc: p.peek().Loc, Value: p.advance().Value})
	} else {
		args = append(args, p.parseExpr())
	}

	// Optional level(info|warn|error)
	if p.check(lexer.TokenIdent) && p.peek().Value == "level" {
		p.advance()
		p.expect(lexer.TokenLParen)
		lvlLoc := p.peek().Loc
		lvl := p.expectIdent()
		args = append(args, &ast.Ident{Loc: lvlLoc, Name: lvl})
		p.expect(lexer.TokenRParen)
	}

	return &ast.FnCall{Loc: loc, Name: "log", Args: args}
}

func (p *Parser) parseSleepOperation() ast.Expr {
	loc := p.expect(lexer.TokenSleep).Loc
	dur := p.parseExpr()
	return &ast.FnCall{Loc: loc, Name: "sleep", Args: []ast.Expr{dur}}
}

func (p *Parser) parseMapOperation() ast.Expr {
	loc := p.expect(lexer.TokenMap).Loc
	collection := p.parseExpr()
	// map collection: expr
	if p.check(lexer.TokenColon) {
		p.advance()
		body := p.parseStepExpr()
		return &ast.FnCall{Loc: loc, Name: "map", Args: []ast.Expr{collection, body}}
	}
	return &ast.FnCall{Loc: loc, Name: "map", Args: []ast.Expr{collection}}
}

func (p *Parser) parseInjectOperation() ast.Expr {
	loc := p.expect(lexer.TokenInject).Loc
	val := p.parseExpr()
	var alias ast.Expr
	if p.check(lexer.TokenAs) {
		p.advance()
		aliasLoc := p.peek().Loc
		aliasName := p.expectIdent()
		alias = &ast.Ident{Loc: aliasLoc, Name: aliasName}
	}
	args := []ast.Expr{val}
	if alias != nil {
		args = append(args, alias)
	}
	return &ast.FnCall{Loc: loc, Name: "inject", Args: args}
}

func (p *Parser) parsePipeCall() ast.Expr {
	loc := p.expect(lexer.TokenPipe).Loc
	name := p.expectIdent()
	// pipe name(args)
	if p.check(lexer.TokenLParen) {
		return p.parseFnCall(name, loc)
	}
	return &ast.Ident{Loc: loc, Name: "pipe_" + name}
}

func (p *Parser) parseGuardStmt(loc lexer.Loc) *ast.GuardStmt {
	p.expect(lexer.TokenGuard)
	cond := p.parseExpr()
	p.expect(lexer.TokenArrowOut)

	statusTok := p.expect(lexer.TokenInt)
	var msg string
	if p.check(lexer.TokenString) {
		msg = p.advance().Value
	}

	return &ast.GuardStmt{
		Loc:       loc,
		Condition: cond,
		Status:    statusTok.Value,
		Message:   msg,
	}
}

func (p *Parser) parseWhenStmt(loc lexer.Loc) *ast.WhenStmt {
	p.expect(lexer.TokenWhen)
	cond := p.parseExpr()

	w := &ast.WhenStmt{Loc: loc, Condition: cond}

	if p.check(lexer.TokenColon) {
		p.advance()
		w.Inline = p.parseStepExpr()
	} else if p.check(lexer.TokenLBrace) {
		p.advance()
		for !p.check(lexer.TokenRBrace) && !p.atEnd() {
			s := p.parseArrowStmt()
			if s != nil {
				w.Body = append(w.Body, s)
			}
		}
		p.expect(lexer.TokenRBrace)
	}

	return w
}

func (p *Parser) parseOutputStmt() *ast.OutputStmt {
	loc := p.expect(lexer.TokenArrowOut).Loc
	out := &ast.OutputStmt{Loc: loc}

	// Check for status code
	if p.check(lexer.TokenInt) {
		out.Status = p.advance().Value
	}

	// Parse value
	if !p.atEnd() && !p.check(lexer.TokenRBrace) && !p.isArrowStart() && !p.isTopLevelStart() {
		out.Value = p.parseExpr()
	}

	return out
}

func (p *Parser) parseTryRecover(loc lexer.Loc) *ast.TryRecover {
	p.expect(lexer.TokenTry)
	p.expect(lexer.TokenLBrace)

	tr := &ast.TryRecover{Loc: loc}

	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		s := p.parseArrowStmt()
		if s != nil {
			tr.Try = append(tr.Try, s)
		}
	}
	p.expect(lexer.TokenRBrace)

	p.expect(lexer.TokenRecover)
	p.expect(lexer.TokenLBrace)

	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		s := p.parseArrowStmt()
		if s != nil {
			tr.Recover = append(tr.Recover, s)
		}
	}
	p.expect(lexer.TokenRBrace)

	return tr
}

func (p *Parser) parseGenerateStepStmt() *ast.GenerateStep {
	loc := p.advance().Loc // consume @>
	text := p.expect(lexer.TokenString)
	step := &ast.GenerateStep{Loc: loc, Text: text.Value}

	for p.check(lexer.TokenIdent) || p.check(lexer.TokenUsing) {
		hLoc := p.peek().Loc
		hName := p.advance().Value
		p.expect(lexer.TokenLParen)
		hVal := p.parseExpr()
		p.expect(lexer.TokenRParen)
		step.Hints = append(step.Hints, ast.Hint{Loc: hLoc, Name: hName, Value: hVal})
	}

	return step
}

// --- Expressions (Pratt parsing) ---

func (p *Parser) parseExpr() ast.Expr {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxExprDepth {
		panic(ParseError{
			Loc:     p.peek().Loc,
			Message: "expression nesting too deep",
		})
	}
	return p.parseBinaryExpr(1) // start at lowest precedence
}

func (p *Parser) parseBinaryExpr(minPrec int) ast.Expr {
	left := p.parseUnaryExpr()

	for {
		prec, op := p.binaryOpPrecedence()
		if prec < minPrec {
			break
		}
		p.advance() // consume operator
		right := p.parseBinaryExpr(prec + 1)
		left = &ast.BinaryExpr{
			Loc:   left.Location(),
			Op:    op,
			Left:  left,
			Right: right,
		}
	}

	return left
}

func (p *Parser) binaryOpPrecedence() (int, string) {
	switch p.peek().Kind {
	case lexer.TokenOr:
		return 1, "or"
	case lexer.TokenAnd:
		return 2, "and"
	case lexer.TokenEq:
		return 4, "=="
	case lexer.TokenNeq:
		return 4, "!="
	case lexer.TokenLt:
		return 4, "<"
	case lexer.TokenGt:
		return 4, ">"
	case lexer.TokenLte:
		return 4, "<="
	case lexer.TokenGte:
		return 4, ">="
	case lexer.TokenIn:
		return 4, "in"
	case lexer.TokenPlus:
		return 5, "+"
	case lexer.TokenMinus:
		return 5, "-"
	case lexer.TokenStar:
		return 6, "*"
	case lexer.TokenSlash:
		return 6, "/"
	default:
		return 0, ""
	}
}

func (p *Parser) parseUnaryExpr() ast.Expr {
	if p.check(lexer.TokenNot) {
		loc := p.advance().Loc
		operand := p.parseUnaryExpr()
		return &ast.UnaryExpr{Loc: loc, Op: "not", Operand: operand}
	}
	if p.check(lexer.TokenMinus) {
		loc := p.advance().Loc
		operand := p.parseUnaryExpr()
		return &ast.UnaryExpr{Loc: loc, Op: "-", Operand: operand}
	}
	return p.parsePostfixExpr()
}

func (p *Parser) parsePostfixExpr() ast.Expr {
	expr := p.parsePrimaryExpr()
	for {
		switch {
		case p.check(lexer.TokenDot):
			p.advance()
			field := p.expectIdent()
			// Special case: header.X-API-Key — reassemble hyphenated header names.
			// The lexer tokenizes "X-API-Key" as X, -, API, -, Key; we reassemble here.
			if ident, ok := expr.(*ast.Ident); ok && ident.Name == "header" {
				for p.peekAt(0).Kind == lexer.TokenMinus && p.peekAt(1).Kind == lexer.TokenIdent {
					p.advance() // consume '-'
					seg := p.expectIdent()
					field += "-" + seg
				}
			}
			expr = &ast.FieldAccess{Loc: expr.Location(), Base: expr, Field: field}
		case p.check(lexer.TokenLBracket):
			p.advance()
			index := p.parseExpr()
			p.expect(lexer.TokenRBracket)
			expr = &ast.IndexAccess{Loc: expr.Location(), Base: expr, Index: index}
		case p.check(lexer.TokenLParen):
			// Function call: only if base is an Ident
			if ident, ok := expr.(*ast.Ident); ok {
				expr = p.parseFnCall(ident.Name, ident.Loc)
			} else {
				return expr
			}
		default:
			return expr
		}
	}
}

func (p *Parser) parsePrimaryExpr() ast.Expr {
	tok := p.peek()
	switch tok.Kind {
	case lexer.TokenString:
		p.advance()
		return &ast.StringLit{Loc: tok.Loc, Value: tok.Value}
	case lexer.TokenInt:
		p.advance()
		return &ast.IntLit{Loc: tok.Loc, Value: tok.Value}
	case lexer.TokenFloat:
		p.advance()
		return &ast.FloatLit{Loc: tok.Loc, Value: tok.Value}
	case lexer.TokenTrue:
		p.advance()
		return &ast.BoolLit{Loc: tok.Loc, Value: true}
	case lexer.TokenFalse:
		p.advance()
		return &ast.BoolLit{Loc: tok.Loc, Value: false}
	case lexer.TokenNull:
		p.advance()
		return &ast.NullLit{Loc: tok.Loc}
	case lexer.TokenNow:
		p.advance()
		return &ast.NowLit{Loc: tok.Loc}
	case lexer.TokenDuration:
		p.advance()
		return &ast.DurationLit{Loc: tok.Loc, Value: tok.Value}
	case lexer.TokenSize:
		p.advance()
		return &ast.SizeLit{Loc: tok.Loc, Value: tok.Value}
	case lexer.TokenRate:
		p.advance()
		return &ast.RateLit{Loc: tok.Loc, Value: tok.Value}
	case lexer.TokenPath:
		p.advance()
		return &ast.PathExpr{Loc: tok.Loc, Value: tok.Value}
	case lexer.TokenLParen:
		p.advance()
		expr := p.parseExpr()
		p.expect(lexer.TokenRParen)
		return &ast.ParenExpr{Loc: tok.Loc, Expr: expr}
	case lexer.TokenLBracket:
		return p.parseListExpr()
	case lexer.TokenLBrace:
		return p.parseBlockExpr()
	case lexer.TokenIdent:
		p.advance()
		return &ast.Ident{Loc: tok.Loc, Name: tok.Value}
	default:
		// Try to handle keywords used as identifiers in expression context
		if p.isKeywordUsableAsIdent() {
			p.advance()
			return &ast.Ident{Loc: tok.Loc, Name: tok.Value}
		}
		panic(ParseError{
			Loc:     tok.Loc,
			Message: "Unexpected token '" + tok.Value + "' in expression",
		})
	}
}

func (p *Parser) parseFnCall(name string, loc lexer.Loc) *ast.FnCall {
	p.expect(lexer.TokenLParen)
	var args []ast.Expr
	for !p.check(lexer.TokenRParen) && !p.atEnd() {
		args = append(args, p.parseExpr())
		if p.check(lexer.TokenComma) {
			p.advance()
		}
	}
	p.expect(lexer.TokenRParen)
	return &ast.FnCall{Loc: loc, Name: name, Args: args}
}

func (p *Parser) parseListExpr() *ast.ListExpr {
	loc := p.expect(lexer.TokenLBracket).Loc
	list := &ast.ListExpr{Loc: loc}
	for !p.check(lexer.TokenRBracket) && !p.atEnd() {
		list.Elements = append(list.Elements, p.parseExpr())
		if p.check(lexer.TokenComma) {
			p.advance()
		}
	}
	p.expect(lexer.TokenRBracket)
	return list
}

func (p *Parser) parseBlockExpr() *ast.BlockExpr {
	loc := p.expect(lexer.TokenLBrace).Loc
	block := &ast.BlockExpr{Loc: loc}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		kv := p.parseKVPair()
		block.Entries = append(block.Entries, kv)
	}
	p.expect(lexer.TokenRBrace)
	return block
}

// --- Type Expressions ---

func (p *Parser) parseTypeExpr() ast.TypeExpr {
	tok := p.peek()

	// Check for enum inline: enum(...)
	if tok.Kind == lexer.TokenEnum && p.peekAt(1).Kind == lexer.TokenLParen {
		return p.parseEnumInline()
	}

	// Check for list type: list(...)
	if (tok.Kind == lexer.TokenIdent && tok.Value == "list") || (tok.Kind == lexer.TokenMap && p.peekAt(1).Kind == lexer.TokenLParen) {
		if tok.Value == "list" || tok.Kind == lexer.TokenIdent {
			return p.parseListType()
		}
	}

	// Check for map type: map(...)
	if tok.Kind == lexer.TokenMap && p.peekAt(1).Kind == lexer.TokenLParen {
		return p.parseMapType()
	}

	// Check for primitive types
	if tok.Value == "json" && p.peekAt(1).Kind == lexer.TokenLt {
		return p.parseTypedJSONType()
	}
	if tok.Value == "tkey" && p.peekAt(1).Kind == lexer.TokenLParen {
		return p.parseTranslationKeyType()
	}

	// Check for primitive types
	if p.isPrimitiveType() {
		p.advance()
		return &ast.PrimitiveType{Loc: tok.Loc, Name: tok.Value}
	}

	// Check for MIME type: ident/ident or ident/*
	if (tok.Kind == lexer.TokenIdent || p.isKeywordUsableAsIdent()) && p.peekAt(1).Kind == lexer.TokenSlash {
		return p.parseMimeType()
	}

	// Named type (identifier)
	if tok.Kind == lexer.TokenIdent || p.isKeywordUsableAsIdent() {
		p.advance()
		return &ast.NamedType{Loc: tok.Loc, Name: tok.Value}
	}

	// Fallback
	p.advance()
	return &ast.PrimitiveType{Loc: tok.Loc, Name: tok.Value}
}

func (p *Parser) parseEnumInline() *ast.EnumInline {
	loc := p.expect(lexer.TokenEnum).Loc
	p.expect(lexer.TokenLParen)
	var variants []string
	for !p.check(lexer.TokenRParen) && !p.atEnd() {
		variants = append(variants, p.expectIdent())
		if p.check(lexer.TokenComma) {
			p.advance()
		}
	}
	p.expect(lexer.TokenRParen)
	return &ast.EnumInline{Loc: loc, Variants: variants}
}

func (p *Parser) parseTypedJSONType() *ast.TypedJSONType {
	loc := p.expect(lexer.TokenIdent).Loc
	p.expect(lexer.TokenLt)
	inner := p.parseTypeExpr()
	p.expect(lexer.TokenGt)
	return &ast.TypedJSONType{Loc: loc, Inner: inner}
}

func (p *Parser) parseTranslationKeyType() *ast.TranslationKeyType {
	loc := p.expect(lexer.TokenIdent).Loc
	p.expect(lexer.TokenLParen)
	namespace := p.expectIdent()
	p.expect(lexer.TokenRParen)
	return &ast.TranslationKeyType{Loc: loc, Namespace: namespace}
}

func (p *Parser) parseListType() *ast.ListType {
	loc := p.peek().Loc
	p.advance() // consume "list"
	p.expect(lexer.TokenLParen)
	elem := p.parseTypeExpr()
	p.expect(lexer.TokenRParen)
	return &ast.ListType{Loc: loc, Element: elem}
}

func (p *Parser) parseMapType() *ast.MapType {
	loc := p.peek().Loc
	p.advance() // consume "map"
	p.expect(lexer.TokenLParen)
	key := p.parseTypeExpr()
	p.expect(lexer.TokenComma)
	val := p.parseTypeExpr()
	p.expect(lexer.TokenRParen)
	return &ast.MapType{Loc: loc, Key: key, Value: val}
}

func (p *Parser) parseMimeType() *ast.MimeTypeExpr {
	loc := p.peek().Loc
	typePart := p.advance().Value
	p.expect(lexer.TokenSlash)
	var subtype string
	if p.check(lexer.TokenStar) {
		p.advance()
		subtype = "*"
	} else {
		subtype = p.expectIdent()
	}
	return &ast.MimeTypeExpr{Loc: loc, Type: typePart, Subtype: subtype}
}

// --- Field & Constraint ---

func (p *Parser) parseField() *ast.Field {
	loc := p.peek().Loc
	name := p.expectIdent()
	typ := p.parseTypeExpr()

	f := &ast.Field{Loc: loc, Name: name, Type: typ}

	for p.isConstraintStart() {
		c := p.parseConstraint()
		f.Constraints = append(f.Constraints, c)
	}

	return f
}

func (p *Parser) parseComputedField() *ast.ComputedField {
	loc := p.advance().Loc // contextual `computed` keyword
	name := p.expectIdent()
	typ := p.parseTypeExpr()
	p.expect(lexer.TokenAssign)
	return &ast.ComputedField{Loc: loc, Name: name, Type: typ, Expr: p.parseExpr()}
}

func (p *Parser) parseConstraint() *ast.Constraint_ {
	loc := p.peek().Loc
	tok := p.peek()

	switch tok.Kind {
	case lexer.TokenPrimary:
		p.advance()
		return &ast.Constraint_{Loc: loc, Kind: "primary"}
	case lexer.TokenUnique:
		p.advance()
		return &ast.Constraint_{Loc: loc, Kind: "unique"}
	case lexer.TokenIndex:
		p.advance()
		return &ast.Constraint_{Loc: loc, Kind: "index"}
	case lexer.TokenRequired:
		p.advance()
		return &ast.Constraint_{Loc: loc, Kind: "required"}
	case lexer.TokenOptional:
		p.advance()
		return &ast.Constraint_{Loc: loc, Kind: "optional"}
	case lexer.TokenAuto:
		p.advance()
		return &ast.Constraint_{Loc: loc, Kind: "auto"}
	case lexer.TokenDefault:
		p.advance()
		p.expect(lexer.TokenLParen)
		val := p.parseExpr()
		p.expect(lexer.TokenRParen)
		return &ast.Constraint_{Loc: loc, Kind: "default", Value: val}
	case lexer.TokenRef:
		p.advance()
		p.expect(lexer.TokenLParen)
		ref := p.expectIdent()
		p.expect(lexer.TokenRParen)
		return &ast.Constraint_{Loc: loc, Kind: "ref", Value: &ast.Ident{Loc: p.peek().Loc, Name: ref}}
	case lexer.TokenFormat:
		p.advance()
		p.expect(lexer.TokenLParen)
		format := p.expectIdent()
		p.expect(lexer.TokenRParen)
		return &ast.Constraint_{Loc: loc, Kind: "format", Value: &ast.Ident{Loc: p.peek().Loc, Name: format}}
	default:
		// min, max as identifiers
		if tok.Kind == lexer.TokenIdent && (tok.Value == "min" || tok.Value == "max") {
			kind := tok.Value
			p.advance()
			p.expect(lexer.TokenLParen)
			val := p.parseExpr()
			p.expect(lexer.TokenRParen)
			return &ast.Constraint_{Loc: loc, Kind: kind, Value: val}
		}
		// Shouldn't reach here if isConstraintStart is correct
		p.advance()
		return &ast.Constraint_{Loc: loc, Kind: tok.Value}
	}
}

// --- Supporting Parsers ---

func (p *Parser) parseUseStmt() *ast.UseStmt {
	loc := p.expect(lexer.TokenUse).Loc
	name := p.expectIdent()
	u := &ast.UseStmt{Loc: loc, Name: name}

	if p.check(lexer.TokenLParen) {
		p.advance()
		for !p.check(lexer.TokenRParen) && !p.atEnd() {
			u.Args = append(u.Args, p.parseExpr())
			if p.check(lexer.TokenComma) {
				p.advance()
			}
		}
		p.expect(lexer.TokenRParen)
	} else if p.check(lexer.TokenLBrace) {
		u.Body = p.parseBlockBody()
	}

	return u
}

func (p *Parser) parseKVPair() ast.KVPair {
	loc := p.peek().Loc
	key := p.expectIdent()

	// Value can follow a colon or just be adjacent
	if p.check(lexer.TokenColon) {
		p.advance()
	}

	val := p.parseExpr()

	// Optional trailing comma
	if p.check(lexer.TokenComma) {
		p.advance()
	}

	return ast.KVPair{Loc: loc, Key: key, Value: val}
}

func (p *Parser) parseStringKVPair() ast.KVPair {
	loc := p.peek().Loc
	key := p.expect(lexer.TokenString)
	if p.check(lexer.TokenColon) {
		p.advance()
	}
	val := p.parseExpr()
	if p.check(lexer.TokenComma) {
		p.advance()
	}
	return ast.KVPair{Loc: loc, Key: key.Value, Value: val}
}

func (p *Parser) parseBlockBody() *ast.BlockBody {
	loc := p.expect(lexer.TokenLBrace).Loc
	body := &ast.BlockBody{Loc: loc}
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		kv := p.parseKVPair()
		body.Entries = append(body.Entries, kv)
	}
	p.expect(lexer.TokenRBrace)
	return body
}

func (p *Parser) parseEndpointMeta() *ast.EndpointMeta {
	loc := p.peek().Loc

	switch p.peek().Kind {
	case lexer.TokenUse:
		use := p.parseUseStmt()
		return &ast.EndpointMeta{Loc: loc, Kind: "use", Use: use}
	case lexer.TokenAuth:
		p.advance()
		val := p.parseExpr()
		// Handle optional: auth webhook_sig using(secret.KEY)
		if p.check(lexer.TokenUsing) {
			p.advance() // consume 'using'
			p.expect(lexer.TokenLParen)
			secretExpr := p.parseExpr()
			p.expect(lexer.TokenRParen)
			// Encode as FnCall{Name:authType, Args:[FnCall{Name:"using", Args:[secretExpr]}]}
			if ident, ok := val.(*ast.Ident); ok {
				usingFn := &ast.FnCall{Loc: ident.Loc, Name: "using", Args: []ast.Expr{secretExpr}}
				val = &ast.FnCall{Loc: ident.Loc, Name: ident.Name, Args: []ast.Expr{usingFn}}
			}
		}
		return &ast.EndpointMeta{Loc: loc, Kind: "auth", Value: val}
	case lexer.TokenLimit:
		p.advance()
		val := p.parseExpr()
		return &ast.EndpointMeta{Loc: loc, Kind: "limit", Value: val}
	case lexer.TokenCache:
		p.advance()
		val := p.parseExpr()
		return &ast.EndpointMeta{Loc: loc, Kind: "cache", Value: val}
	case lexer.TokenTags:
		p.advance()
		val := p.parseExpr()
		return &ast.EndpointMeta{Loc: loc, Kind: "tags", Value: val}
	case lexer.TokenTimeout:
		p.advance()
		val := p.parseExpr()
		return &ast.EndpointMeta{Loc: loc, Kind: "timeout", Value: val}
	default:
		// Shouldn't get here
		p.advance()
		return &ast.EndpointMeta{Loc: loc, Kind: "unknown"}
	}
}

func (p *Parser) parseOnError() *ast.OnError {
	loc := p.expect(lexer.TokenOnError).Loc
	p.expect(lexer.TokenArrowOut)
	status := p.expect(lexer.TokenInt)
	msg := p.expect(lexer.TokenString)
	return &ast.OnError{Loc: loc, Status: status.Value, Message: msg.Value}
}

func (p *Parser) parseWorkerMeta() *ast.WorkerMeta {
	loc := p.peek().Loc

	switch p.peek().Kind {
	case lexer.TokenTrigger:
		p.advance()
		val := p.parseExpr()
		return &ast.WorkerMeta{Loc: loc, Kind: "trigger", Value: val}
	case lexer.TokenRetry:
		p.advance()
		val := p.parseExpr()
		meta := &ast.WorkerMeta{Loc: loc, Kind: "retry", Value: val}
		// Parse optional backoff(...)
		if p.check(lexer.TokenIdent) && p.peek().Value == "backoff" {
			p.advance()
			p.expect(lexer.TokenLParen)
			for !p.check(lexer.TokenRParen) && !p.atEnd() {
				if (p.check(lexer.TokenIdent) || p.isKeywordUsableAsIdent()) && p.peekAt(1).Kind == lexer.TokenColon {
					kv := p.parseKVPair()
					meta.Extra = append(meta.Extra, kv)
				} else {
					// First arg is strategy name (not a KV pair)
					stratLoc := p.peek().Loc
					strat := p.expectIdent()
					meta.Extra = append(meta.Extra, ast.KVPair{
						Loc: stratLoc, Key: "strategy",
						Value: &ast.Ident{Loc: stratLoc, Name: strat},
					})
					if p.check(lexer.TokenComma) {
						p.advance()
					}
				}
			}
			p.expect(lexer.TokenRParen)
		}
		return meta
	case lexer.TokenTimeout:
		p.advance()
		val := p.parseExpr()
		return &ast.WorkerMeta{Loc: loc, Kind: "timeout", Value: val}
	default:
		p.advance()
		return &ast.WorkerMeta{Loc: loc, Kind: "unknown"}
	}
}

func (p *Parser) parseStreamHandler() *ast.StreamHandler {
	loc := p.peek().Loc

	if !p.check(lexer.TokenArrowPipe) {
		p.advance()
		return nil
	}
	p.advance() // |>

	if !p.check(lexer.TokenOn) {
		p.advance()
		return nil
	}
	p.advance() // on

	h := &ast.StreamHandler{Loc: loc}

	if p.check(lexer.TokenIdent) && p.peek().Value == "event" {
		p.advance()
		p.expect(lexer.TokenLParen)
		h.EventName = p.expectIdent()
		p.expect(lexer.TokenRParen)

		if p.check(lexer.TokenWhere) {
			p.advance()
			p.expect(lexer.TokenLParen)
			h.Condition = p.parseExpr()
			p.expect(lexer.TokenRParen)
		}
	} else if p.check(lexer.TokenTimeout) {
		p.advance()
		p.expect(lexer.TokenLParen)
		dur := p.expect(lexer.TokenDuration)
		h.Timeout = dur.Value
		p.expect(lexer.TokenRParen)
	}

	p.expect(lexer.TokenLBrace)
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		s := p.parseArrowStmt()
		if s != nil {
			h.Body = append(h.Body, s)
		}
	}
	p.expect(lexer.TokenRBrace)

	return h
}

func (p *Parser) parseAssertion() *ast.Assertion {
	loc := p.peek().Loc

	// Consume one assertion per source line. A new assertion begins when we
	// encounter an assertion-start keyword on a different line and we are not
	// inside parentheses (e.g. where(...) clauses can span keywords).
	var raw []string
	startLine := 0
	parenDepth := 0
	for !p.check(lexer.TokenRBrace) && !p.atEnd() {
		tok := p.peek()
		if len(raw) == 0 {
			startLine = tok.Loc.Line
		}
		if tok.Kind == lexer.TokenLParen {
			parenDepth++
		} else if tok.Kind == lexer.TokenRParen && parenDepth > 0 {
			parenDepth--
		}
		// Break when a new assertion keyword appears on a different source line
		// and we are not inside a parenthesised expression.
		if len(raw) > 0 && parenDepth == 0 && p.isAssertionStart() && tok.Loc.Line != startLine {
			break
		}
		// Preserve string token type by wrapping in quotes so codegen can distinguish
		// string values from identifier names (e.g. body.status == "done").
		if tok.Kind == lexer.TokenString {
			raw = append(raw, `"`+tok.Value+`"`)
		} else {
			raw = append(raw, tok.Value)
		}
		p.advance()
	}

	if len(raw) == 0 {
		return nil
	}

	kind := raw[0]
	return &ast.Assertion{Loc: loc, Kind: kind, Raw: joinStrings(raw)}
}

// --- Predicate Helpers ---

func (p *Parser) isArrowStart() bool {
	k := p.peek().Kind
	return k == lexer.TokenArrowIn || k == lexer.TokenArrowPipe ||
		k == lexer.TokenArrowOut || k == lexer.TokenGenerate
}

func (p *Parser) isEndpointMetaStart() bool {
	switch p.peek().Kind {
	case lexer.TokenUse, lexer.TokenAuth, lexer.TokenLimit,
		lexer.TokenCache, lexer.TokenTags, lexer.TokenTimeout:
		return true
	}
	return false
}

func (p *Parser) isWorkerMetaStart() bool {
	switch p.peek().Kind {
	case lexer.TokenTrigger, lexer.TokenRetry, lexer.TokenTimeout:
		return true
	}
	return false
}

func (p *Parser) isConstraintStart() bool {
	switch p.peek().Kind {
	case lexer.TokenPrimary, lexer.TokenUnique, lexer.TokenIndex,
		lexer.TokenRequired, lexer.TokenOptional, lexer.TokenAuto:
		return true
	case lexer.TokenDefault, lexer.TokenRef, lexer.TokenFormat:
		// These require ( after them to be constraints
		return p.peekAt(1).Kind == lexer.TokenLParen
	}
	// Also check for "min" and "max" as identifiers with (
	if p.peek().Kind == lexer.TokenIdent && (p.peek().Value == "min" || p.peek().Value == "max") {
		return p.peekAt(1).Kind == lexer.TokenLParen
	}
	return false
}

func (p *Parser) isTypeStart() bool {
	if p.isPrimitiveType() {
		return true
	}
	switch p.peek().Kind {
	case lexer.TokenEnum, lexer.TokenIdent:
		return true
	}
	// Also accept keywords that can be types
	if p.isKeywordUsableAsIdent() {
		return true
	}
	return false
}

func (p *Parser) isPrimitiveType() bool {
	tok := p.peek()
	if tok.Kind != lexer.TokenIdent && !p.isKeywordUsableAsIdent() {
		return false
	}
	switch tok.Value {
	case "string", "text", "int", "float", "bool", "uuid", "timestamp", "json", "file", "money":
		return true
	}
	return false
}

func (p *Parser) isKeywordUsableAsIdent() bool {
	// Many keywords can appear as identifiers in certain contexts
	// (field names, block names, etc.)
	switch p.peek().Kind {
	case lexer.TokenAnalytics, lexer.TokenAuth, lexer.TokenCache, lexer.TokenCall,
		lexer.TokenClose, lexer.TokenCount, lexer.TokenCronKw,
		lexer.TokenDefault, lexer.TokenDelete, lexer.TokenDownload,
		lexer.TokenEmit, lexer.TokenEnqueue, lexer.TokenEnv, lexer.TokenFetch, lexer.TokenFirst,
		lexer.TokenFormat, lexer.TokenFrom, lexer.TokenGenerated,
		lexer.TokenInject, lexer.TokenIs, lexer.TokenJoin,
		lexer.TokenLeave, lexer.TokenLimit, lexer.TokenLog,
		lexer.TokenLogic, lexer.TokenMap, lexer.TokenMatches, lexer.TokenNot,
		lexer.TokenOn, lexer.TokenOrder, lexer.TokenPaginate,
		lexer.TokenQuery, lexer.TokenSave, lexer.TokenSeed,
		lexer.TokenSleep, lexer.TokenStream, lexer.TokenTarget,
		lexer.TokenTimeout, lexer.TokenTo, lexer.TokenTrigger,
		lexer.TokenUpdate, lexer.TokenUpload, lexer.TokenUsing,
		lexer.TokenWhere, lexer.TokenWhisper, lexer.TokenBroadcast,
		lexer.TokenExists, lexer.TokenRequest, lexer.TokenRepeat,
		lexer.TokenSetup, lexer.TokenCleanup, lexer.TokenExpect,
		lexer.TokenAs, lexer.TokenBefore, lexer.TokenAfter,
		lexer.TokenSecret, lexer.TokenRetry, lexer.TokenPrimary,
		lexer.TokenUnique, lexer.TokenIndex, lexer.TokenRequired,
		lexer.TokenOptional, lexer.TokenAuto, lexer.TokenRef,
		lexer.TokenGuard, lexer.TokenPipe, lexer.TokenModel,
		lexer.TokenFn, lexer.TokenMiddleware, lexer.TokenWorker,
		lexer.TokenSchedule, lexer.TokenExternal, lexer.TokenSubscribe,
		lexer.TokenState,
		lexer.TokenTest, lexer.TokenTestGroup, lexer.TokenFixture,
		lexer.TokenType, lexer.TokenAlias, lexer.TokenEnum,
		lexer.TokenInclude, lexer.TokenImpl,
		lexer.TokenTags,
		lexer.TokenIdent:
		return true
	}
	return false
}

func (p *Parser) isAssertionStart() bool {
	v := p.peek().Value
	switch v {
	case "status", "body", "header", "duration", "model", "last_status":
		return true
	}
	return false
}

func (p *Parser) expectIdent() string {
	tok := p.peek()
	if tok.Kind == lexer.TokenIdent {
		p.advance()
		return tok.Value
	}
	// Allow keywords as identifiers in name positions
	if p.isKeywordUsableAsIdent() {
		p.advance()
		return tok.Value
	}
	panic(ParseError{
		Loc:     tok.Loc,
		Message: "Expected identifier, got '" + tok.Kind.String() + "'",
	})
}

// --- Utilities ---

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += " "
		}
		result += s
	}
	return result
}
