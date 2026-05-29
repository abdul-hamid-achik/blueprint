package lexer

import "fmt"

// TokenKind represents the type of a lexical token.
type TokenKind int

const (
	// Arrows
	TokenArrowIn   TokenKind = iota // <-
	TokenArrowPipe                  // |>
	TokenArrowOut                   // ->
	TokenIntent                     // @ (followed by string)
	TokenGenerate                   // @>

	// Literals
	TokenString // "hello"
	TokenInt    // 42
	TokenFloat  // 3.14
	TokenTrue   // true
	TokenFalse  // false
	TokenNow    // now
	TokenNull   // null

	// Compound literals
	TokenDuration // 5s, 10min, 1hour, 30days
	TokenSize     // 10mb, 1gb
	TokenRate     // 60/min, 100/hour
	TokenPath     // /api/watermark

	// Delimiters
	TokenLBrace   // {
	TokenRBrace   // }
	TokenLBracket // [
	TokenRBracket // ]
	TokenLParen   // (
	TokenRParen   // )
	TokenComma    // ,
	TokenColon    // :
	TokenDot      // .

	// Operators
	TokenEq     // ==
	TokenNeq    // !=
	TokenLt     // <
	TokenGt     // >
	TokenLte    // <=
	TokenGte    // >=
	TokenPlus   // +
	TokenMinus  // -
	TokenStar   // *
	TokenSlash  // /
	TokenAssign // =

	// Keywords (alphabetical)
	TokenAfter
	TokenAnalytics
	TokenAlias
	TokenAnd
	TokenAs
	TokenAuth
	TokenAuto
	TokenBefore
	TokenBlueprint
	TokenBroadcast
	TokenCache
	TokenCall
	TokenCleanup
	TokenClose
	TokenContent
	TokenCount
	TokenCronKw // cron keyword
	TokenDefault
	TokenDelete       // delete operation
	TokenDeleteMethod // DELETE HTTP method
	TokenDownload
	TokenEmit
	TokenEnum
	TokenEnv
	TokenExists
	TokenExpect
	TokenExternal
	TokenFetch
	TokenFirst
	TokenFixture
	TokenFn
	TokenFormat
	TokenFrom
	TokenGenerated
	TokenGetMethod // GET
	TokenGuard
	TokenImpl
	TokenIn
	TokenInclude
	TokenIndex
	TokenInject
	TokenIs
	TokenJoin
	TokenLeave
	TokenLimit
	TokenLocale
	TokenLog
	TokenLogic
	TokenMap
	TokenMatches
	TokenMiddleware
	TokenModel
	TokenNot
	TokenOn
	TokenOnConnect
	TokenOnDisconnect
	TokenOnError
	TokenOnFail
	TokenOnMessage
	TokenOptional
	TokenOr
	TokenOrder
	TokenPaginate
	TokenPatchMethod // PATCH
	TokenPipe
	TokenPostMethod // POST
	TokenPrimary
	TokenPutMethod // PUT
	TokenQuery
	TokenRecover
	TokenRef
	TokenRepeat
	TokenRequired
	TokenRequest
	TokenRetry
	TokenSave
	TokenSchedule
	TokenSecret
	TokenSeed
	TokenSetup
	TokenSleep
	TokenState
	TokenStream
	TokenStreamMethod // STREAM
	TokenSubscribe
	TokenTags
	TokenTarget
	TokenTest
	TokenTestGroup
	TokenTimeout
	TokenTranslation
	TokenTo
	TokenTrigger
	TokenTry
	TokenType
	TokenUnique
	TokenUpdate
	TokenUpload
	TokenUse
	TokenUsing
	TokenWhen
	TokenWhere
	TokenWhisper
	TokenWorker
	TokenWs // WS

	// Identifiers
	TokenIdent

	// Meta
	TokenEOF
	TokenIllegal
)

var tokenNames = map[TokenKind]string{
	TokenArrowIn:      "ArrowIn",
	TokenArrowPipe:    "ArrowPipe",
	TokenArrowOut:     "ArrowOut",
	TokenIntent:       "Intent",
	TokenGenerate:     "Generate",
	TokenString:       "String",
	TokenInt:          "Int",
	TokenFloat:        "Float",
	TokenTrue:         "true",
	TokenFalse:        "false",
	TokenNow:          "now",
	TokenNull:         "null",
	TokenDuration:     "Duration",
	TokenSize:         "Size",
	TokenRate:         "Rate",
	TokenPath:         "Path",
	TokenLBrace:       "{",
	TokenRBrace:       "}",
	TokenLBracket:     "[",
	TokenRBracket:     "]",
	TokenLParen:       "(",
	TokenRParen:       ")",
	TokenComma:        ",",
	TokenColon:        ":",
	TokenDot:          ".",
	TokenEq:           "==",
	TokenNeq:          "!=",
	TokenLt:           "<",
	TokenGt:           ">",
	TokenLte:          "<=",
	TokenGte:          ">=",
	TokenPlus:         "+",
	TokenMinus:        "-",
	TokenStar:         "*",
	TokenSlash:        "/",
	TokenAssign:       "=",
	TokenAfter:        "after",
	TokenAnalytics:    "analytics",
	TokenAlias:        "alias",
	TokenAnd:          "and",
	TokenAs:           "as",
	TokenAuth:         "auth",
	TokenAuto:         "auto",
	TokenBefore:       "before",
	TokenBlueprint:    "blueprint",
	TokenBroadcast:    "broadcast",
	TokenCache:        "cache",
	TokenCall:         "call",
	TokenCleanup:      "cleanup",
	TokenClose:        "close",
	TokenContent:      "content",
	TokenCount:        "count",
	TokenCronKw:       "cron",
	TokenDefault:      "default",
	TokenDelete:       "delete",
	TokenDeleteMethod: "DELETE",
	TokenDownload:     "download",
	TokenEmit:         "emit",
	TokenEnum:         "enum",
	TokenEnv:          "env",
	TokenExists:       "exists",
	TokenExpect:       "expect",
	TokenExternal:     "external",
	TokenFetch:        "fetch",
	TokenFirst:        "first",
	TokenFixture:      "fixture",
	TokenFn:           "fn",
	TokenFormat:       "format",
	TokenFrom:         "from",
	TokenGenerated:    "generated",
	TokenGetMethod:    "GET",
	TokenGuard:        "guard",
	TokenImpl:         "impl",
	TokenIn:           "in",
	TokenInclude:      "include",
	TokenIndex:        "index",
	TokenInject:       "inject",
	TokenIs:           "is",
	TokenJoin:         "join",
	TokenLeave:        "leave",
	TokenLimit:        "limit",
	TokenLocale:       "locale",
	TokenLog:          "log",
	TokenLogic:        "logic",
	TokenMap:          "map",
	TokenMatches:      "matches",
	TokenMiddleware:   "middleware",
	TokenModel:        "model",
	TokenNot:          "not",
	TokenOn:           "on",
	TokenOnConnect:    "on_connect",
	TokenOnDisconnect: "on_disconnect",
	TokenOnError:      "on_error",
	TokenOnFail:       "on_fail",
	TokenOnMessage:    "on_message",
	TokenOptional:     "optional",
	TokenOr:           "or",
	TokenOrder:        "order",
	TokenPaginate:     "paginate",
	TokenPatchMethod:  "PATCH",
	TokenPipe:         "pipe",
	TokenPostMethod:   "POST",
	TokenPrimary:      "primary",
	TokenPutMethod:    "PUT",
	TokenQuery:        "query",
	TokenRecover:      "recover",
	TokenRef:          "ref",
	TokenRepeat:       "repeat",
	TokenRequired:     "required",
	TokenRequest:      "request",
	TokenRetry:        "retry",
	TokenSave:         "save",
	TokenSchedule:     "schedule",
	TokenSecret:       "secret",
	TokenSeed:         "seed",
	TokenSetup:        "setup",
	TokenSleep:        "sleep",
	TokenState:        "state",
	TokenStream:       "stream",
	TokenStreamMethod: "STREAM",
	TokenSubscribe:    "subscribe",
	TokenTags:         "tags",
	TokenTarget:       "target",
	TokenTest:         "test",
	TokenTestGroup:    "test_group",
	TokenTimeout:      "timeout",
	TokenTranslation:  "translation",
	TokenTo:           "to",
	TokenTrigger:      "trigger",
	TokenTry:          "try",
	TokenType:         "type",
	TokenUnique:       "unique",
	TokenUpdate:       "update",
	TokenUpload:       "upload",
	TokenUse:          "use",
	TokenUsing:        "using",
	TokenWhen:         "when",
	TokenWhere:        "where",
	TokenWhisper:      "whisper",
	TokenWorker:       "worker",
	TokenWs:           "WS",
	TokenIdent:        "Ident",
	TokenEOF:          "EOF",
	TokenIllegal:      "Illegal",
}

func (k TokenKind) String() string {
	if name, ok := tokenNames[k]; ok {
		return name
	}
	return fmt.Sprintf("Token(%d)", int(k))
}

// keywords maps keyword strings to their token kinds.
var keywords = map[string]TokenKind{
	"after":         TokenAfter,
	"analytics":     TokenAnalytics,
	"alias":         TokenAlias,
	"and":           TokenAnd,
	"as":            TokenAs,
	"auth":          TokenAuth,
	"auto":          TokenAuto,
	"before":        TokenBefore,
	"blueprint":     TokenBlueprint,
	"broadcast":     TokenBroadcast,
	"cache":         TokenCache,
	"call":          TokenCall,
	"cleanup":       TokenCleanup,
	"close":         TokenClose,
	"content":       TokenContent,
	"count":         TokenCount,
	"cron":          TokenCronKw,
	"default":       TokenDefault,
	"delete":        TokenDelete,
	"DELETE":        TokenDeleteMethod,
	"download":      TokenDownload,
	"emit":          TokenEmit,
	"enum":          TokenEnum,
	"env":           TokenEnv,
	"exists":        TokenExists,
	"expect":        TokenExpect,
	"external":      TokenExternal,
	"fetch":         TokenFetch,
	"first":         TokenFirst,
	"fixture":       TokenFixture,
	"fn":            TokenFn,
	"format":        TokenFormat,
	"from":          TokenFrom,
	"generated":     TokenGenerated,
	"GET":           TokenGetMethod,
	"guard":         TokenGuard,
	"impl":          TokenImpl,
	"in":            TokenIn,
	"include":       TokenInclude,
	"index":         TokenIndex,
	"inject":        TokenInject,
	"is":            TokenIs,
	"join":          TokenJoin,
	"leave":         TokenLeave,
	"limit":         TokenLimit,
	"locale":        TokenLocale,
	"log":           TokenLog,
	"logic":         TokenLogic,
	"map":           TokenMap,
	"matches":       TokenMatches,
	"middleware":    TokenMiddleware,
	"model":         TokenModel,
	"not":           TokenNot,
	"now":           TokenNow,
	"null":          TokenNull,
	"on":            TokenOn,
	"on_connect":    TokenOnConnect,
	"on_disconnect": TokenOnDisconnect,
	"on_error":      TokenOnError,
	"on_fail":       TokenOnFail,
	"on_message":    TokenOnMessage,
	"optional":      TokenOptional,
	"or":            TokenOr,
	"order":         TokenOrder,
	"paginate":      TokenPaginate,
	"PATCH":         TokenPatchMethod,
	"pipe":          TokenPipe,
	"POST":          TokenPostMethod,
	"primary":       TokenPrimary,
	"PUT":           TokenPutMethod,
	"query":         TokenQuery,
	"recover":       TokenRecover,
	"ref":           TokenRef,
	"repeat":        TokenRepeat,
	"required":      TokenRequired,
	"request":       TokenRequest,
	"retry":         TokenRetry,
	"save":          TokenSave,
	"schedule":      TokenSchedule,
	"secret":        TokenSecret,
	"seed":          TokenSeed,
	"setup":         TokenSetup,
	"sleep":         TokenSleep,
	"state":         TokenState,
	"stream":        TokenStream,
	"STREAM":        TokenStreamMethod,
	"subscribe":     TokenSubscribe,
	"tags":          TokenTags,
	"target":        TokenTarget,
	"test":          TokenTest,
	"test_group":    TokenTestGroup,
	"timeout":       TokenTimeout,
	"translation":   TokenTranslation,
	"to":            TokenTo,
	"trigger":       TokenTrigger,
	"true":          TokenTrue,
	"false":         TokenFalse,
	"try":           TokenTry,
	"type":          TokenType,
	"unique":        TokenUnique,
	"update":        TokenUpdate,
	"upload":        TokenUpload,
	"use":           TokenUse,
	"using":         TokenUsing,
	"when":          TokenWhen,
	"where":         TokenWhere,
	"whisper":       TokenWhisper,
	"worker":        TokenWorker,
	"WS":            TokenWs,
}

// LookupKeyword returns the keyword token kind for an identifier, or TokenIdent if not a keyword.
func LookupKeyword(ident string) TokenKind {
	if kind, ok := keywords[ident]; ok {
		return kind
	}
	return TokenIdent
}

// IsHTTPMethod returns true if the token kind is an HTTP method keyword.
func IsHTTPMethod(kind TokenKind) bool {
	switch kind {
	case TokenGetMethod, TokenPostMethod, TokenPutMethod, TokenPatchMethod, TokenDeleteMethod:
		return true
	}
	return false
}

// IsEndpointMethod returns true if the token kind starts an endpoint block.
func IsEndpointMethod(kind TokenKind) bool {
	return IsHTTPMethod(kind) || kind == TokenStreamMethod || kind == TokenWs
}

// Token represents a lexical token with its kind, value, and source location.
type Token struct {
	Kind  TokenKind
	Value string
	Loc   Loc
}

func (t Token) String() string {
	if t.Value != "" {
		return fmt.Sprintf("%s(%q)", t.Kind, t.Value)
	}
	return t.Kind.String()
}

// Loc represents a source location.
type Loc struct {
	File   string
	Line   int // 1-indexed
	Col    int // 1-indexed
	Offset int // byte offset in file
	Len    int // length in bytes
}

func (l Loc) String() string {
	return fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Col)
}

// LexError represents an error found during lexing.
//
// Code is an optional structured error code (e.g. "L001") used by
// `bp explain <code>` to surface long-form documentation. Hint, when
// non-empty, is rendered after the error message by the shared diagnostic
// formatter (see internal/diag).
type LexError struct {
	Loc     Loc
	Message string
	Hint    string
	Code    string
}

func (e LexError) Error() string {
	s := fmt.Sprintf("%s: %s", e.Loc, e.Message)
	if e.Hint != "" {
		s += "\n  Hint: " + e.Hint
	}
	return s
}
