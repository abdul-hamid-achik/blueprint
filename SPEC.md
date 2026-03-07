# Appendix A: Formal Grammar

This appendix provides a formal grammar for the Blueprint language.

## Lexical Structure

```ebnf
Whitespace   ::= [ \t\n\r]+
Comment      ::= "#" [^\n]*
               | "@\"" [^\"]* "\""

Identifier   ::= [a-z_][a-z0-9_]*
TypeName     ::= [A-Z][a-zA-Z0-9]*
StringLit    ::= "\"" ([^"\\] | "\\" .)* "\""
IntLit       ::= [0-9]+
FloatLit     ::= [0-9]+ "." [0-9]+
BoolLit      ::= "true" | "false"
DurationLit  ::= IntLit ("ms" | "s" | "min" | "h" | "d" | "days")
SizeLit      ::= IntLit ("b" | "kb" | "mb" | "gb")
RateLit      ::= IntLit "/" ("min" | "hour" | "day")
MimeType     ::= [a-z]+ "/" ("*" | [a-z]+)
PathLit      ::= "/" [^\s{}]+ | "./" [^\s{}]+

Keywords     ::= "blueprint" | "secret" | "env" | "model" | "type" | "alias"
               | "enum" | "fn" | "pipe" | "middleware" | "GET" | "POST" | "PUT"
               | "DELETE" | "PATCH" | "STREAM" | "WS" | "worker" | "schedule"
               | "external" | "subscribe" | "test" | "fixture" | "include"
               | "impl" | "logic" | "before" | "after" | "use" | "auth"
               | "limit" | "cache" | "tags" | "timeout" | "stream" | "on_connect"
               | "on_message" | "on_disconnect" | "trigger" | "retry" | "cron"
               | "from" | "target" | "setup" | "request" | "expect" | "cleanup"
               | "on" | "where" | "order" | "paginate" | "first" | "save"
               | "update" | "delete" | "count" | "fetch" | "query" | "upload"
               | "download" | "pipe" | "map" | "call" | "emit" | "log"
               | "sleep" | "clock" | "hash" | "guard" | "when" | "try"
               | "recover" | "on_fail" | "on_error" | "inject" | "as" | "join"
               | "leave" | "broadcast" | "whisper" | "close" | "seed"
               | "exists" | "not_exists" | "is" | "repeat" | "body" | "header"
               | "status" | "duration" | "model" | "last_status" | "fixture"
               | "required" | "optional" | "primary" | "unique" | "index"
               | "auto" | "default" | "ref" | "min" | "max" | "format"
               | "node" | "exec" | "http" | "generate"
               | "now" | "null" | "and" | "or" | "not" | "in"

Operators    ::= "==" | "!=" | "<" | ">" | "<=" | ">=" | "+" | "-" | "*" | "/" | "%"
Arrows       ::= "<-" | "|>" | "->"
```

## Syntactic Grammar

```ebnf
File           ::= BlueprintDecl (TopLevel)*

BlueprintDecl  ::= "blueprint" StringLit "{" BlueprintEntry* "}"
BlueprintEntry ::= Identifier Expr
                 | "use" Identifier (BlockBody)?

TopLevel       ::= SecretDecl
                 | EnvDecl
                 | ModelDecl
                 | TypeDecl
                 | AliasDecl
                 | EnumDecl
                 | FnDecl
                 | PipeDecl
                 | MiddlewareDecl
                 | EndpointDecl
                 | StreamEndpointDecl
                 | WsEndpointDecl
                 | WorkerDecl
                 | ScheduleDecl
                 | ExternalDecl
                 | SubscribeDecl
                 | TestDecl
                 | FixtureDecl
                 | IncludeDecl

SecretDecl     ::= "secret" Identifier ("required" | "optional" DefaultValue?)
EnvDecl        ::= "env" Identifier Expr
ModelDecl      ::= Intent? "model" Identifier "{" Field* "}"
Field          ::= Identifier TypeExpr Constraint*
Constraint     ::= "primary" | "unique" | "index" | "required" | "optional"
                 | "auto" | "default" "(" Expr ")" | "ref" "(" Identifier ")"
                 | "min" "(" Expr ")" | "max" "(" Expr ")" | "format" "(" StringLit ")"

TypeDecl       ::= "type" Identifier "{" Field* "}"
AliasDecl      ::= "alias" Identifier "=" TypeExpr Constraint*
EnumDecl       ::= Intent? "enum" Identifier "{" EnumVariant* "}"
EnumVariant    ::= Identifier ("{" EnumField* "}")?
EnumField      ::= Identifier ":" Expr

FnDecl         ::= Intent? "fn" Identifier "{" FnBody "}"
FnBody         ::= InputStmt* OutputStmt* (ImplBlock | LogicBlock)
ImplBlock      ::= "impl" Strategy "{" KvPair* "}"
Strategy       ::= "node" | "exec" | "http" | "generate"
LogicBlock     ::= "logic" "{" ArrowStmt* "}"

PipeDecl       ::= Intent? "pipe" Identifier "{" PipeBody "}"
PipeBody       ::= InputStmt* ArrowStmt* OutputStmt?

MiddlewareDecl ::= Intent? "middleware" Identifier "{" MiddlewareBody "}"
MiddlewareBody ::= ("before" "{" ArrowStmt* "}")? ("after" "{" ArrowStmt* "}")? KvPair*

EndpointDecl   ::= Intent? HttpMethod Path "{" EndpointBody "}"
HttpMethod     ::= "GET" | "POST" | "PUT" | "DELETE" | "PATCH"
Path           ::= "/" PathSegment ("/" PathSegment)*
PathSegment    ::= Identifier | ":" Identifier
EndpointBody   ::= EndpointMeta* ArrowStmt*
EndpointMeta   ::= "use" Identifier
                 | "auth" Identifier
                 | "limit" Expr
                 | "cache" Expr
                 | "tags" ListExpr
                 | "timeout" Expr

StreamEndpointDecl ::= Intent? "STREAM" Path "{" StreamBody "}"
StreamBody     ::= EndpointMeta* ArrowStmt* StreamBlock?
StreamBlock    ::= "stream" "{" StreamHandler* "}"
StreamHandler  ::= "on" "event" "(" Identifier ")" (WhereClause)? "{" ArrowStmt* "}"
                 | "on" "timeout" "(" DurationLit ")" "{" ArrowStmt* "}"

WsEndpointDecl ::= Intent? "WS" Path "{" WsBody "}"
WsBody         ::= EndpointMeta* WsHandler*
WsHandler      ::= "on_connect" "{" ArrowStmt* "}"
                 | "on_message" "{" ArrowStmt* "}"
                 | "on_disconnect" "{" ArrowStmt* "}"

WorkerDecl     ::= Intent? "worker" Identifier "{" WorkerBody "}"
WorkerBody     ::= WorkerMeta* InputStmt? ArrowStmt* OnFail?
WorkerMeta     ::= "trigger" "queue" "(" StringLit ")"
                 | "retry" IntLit (Backoff)?
                 | "timeout" Expr
OnFail         ::= "on_fail" "{" ArrowStmt* "}"

ScheduleDecl   ::= Intent? "schedule" Identifier "{" ScheduleBody "}"
ScheduleBody   ::= "cron" StringLit ArrowStmt*

ExternalDecl   ::= "external" StringLit "{" KvPair* "}"
SubscribeDecl  ::= Intent? "subscribe" StringLit "from" "(" Identifier ")" "{" ArrowStmt* "}"

TestDecl       ::= Intent? "test" Identifier "{" TestBody "}"
TestBody       ::= "target" HttpMethod Path Setup? Request Expect Cleanup?
Setup          ::= "setup" "{" ArrowStmt* "}"
Request        ::= "request" (Repeat)? "{" KvPair* "}"
Repeat         ::= "repeat" "(" IntLit ")"
Expect         ::= "expect" "{" Assertion* "}"
Assertion      ::= "status" IntLit
                 | "body" "." FieldPath ("is" TypeName | "==" Expr | "!=" Expr | "exists" | "not_exists")
                 | "header" "." Identifier "==" Expr
                 | "duration" ComparisonOp Expr
                 | "model" Identifier "where" "(" Condition ("," Condition)* ")" ("exists" | "not_exists")
                 | "last_status" IntLit
Cleanup        ::= "cleanup" "{" ArrowStmt* "}"

FixtureDecl    ::= "fixture" StringLit "from" StringLit
                 | "fixture" StringLit "generated" BlockBody
                 | "fixture" StringLit "seed" Identifier BlockBody

IncludeDecl    ::= "include" StringLit

ArrowStmt      ::= InputStmt
                 | StepStmt
                 | GuardStmt
                 | WhenStmt
                 | OutputStmt
                 | TryRecover
                 | GenerateStep
                 | IntentStep

InputStmt      ::= "<-" Identifier TypeExpr Constraint*
StepStmt       ::= "|>" (Identifier "=")? Expr
GuardStmt      ::= "|>" "guard" Expr "->" IntLit StringLit
WhenStmt       ::= "|>" "when" Expr (":" Expr | "{" ArrowStmt* "}")
OutputStmt     ::= "->" IntLit Expr?
                 | "->" Expr
TryRecover     ::= "|>" "try" "{" ArrowStmt* "}" "recover" "{" ArrowStmt* "}"
GenerateStep   ::= "|>" "@>" StringLit Hint*
IntentStep     ::= "|>" "@" StringLit

Expr           ::= BinaryExpr
                 | UnaryExpr
                 | FnCall
                 | FieldAccess
                 | IndexAccess
                 | Ident
                 | StringLit
                 | IntLit
                 | FloatLit
                 | BoolLit
                 | NullLit
                 | NowLit
                 | DurationLit
                 | SizeLit
                 | RateLit
                 | ParenExpr
                 | ListExpr
                 | BlockExpr
                 | PathLit

BinaryExpr     ::= Expr BinOp Expr
BinOp          ::= "==" | "!=" | "<" | ">" | "<=" | ">=" | "+" | "-" | "*" | "/" | "%" | "and" | "or" | "in"
UnaryExpr      ::= UnOp Expr
UnOp           ::= "not" | "-"
FnCall         ::= Identifier "(" (Expr ("," Expr)*)? ")"
FieldAccess    ::= Expr "." Identifier
IndexAccess    ::= Expr "[" Expr "]"
Ident          ::= Identifier
ParenExpr      ::= "(" Expr ")"
ListExpr       ::= "[" (Expr ("," Expr)*)? "]"
BlockExpr      ::= "{" KvPair* "}"
KvPair         ::= Identifier ":" Expr

TypeExpr       ::= PrimitiveType
                 | NamedType
                 | ListType
                 | MapType
                 | EnumInline
                 | MimeTypeExpr

PrimitiveType  ::= "string" | "int" | "float" | "bool" | "uuid" | "timestamp" | "json" | "money" | "file"
NamedType      ::= Identifier
ListType       ::= "[" TypeExpr "]"
MapType        ::= "map" "[" TypeExpr "]" TypeExpr
EnumInline     ::= "enum" "(" Identifier ("," Identifier)* ")"
MimeTypeExpr   ::= MimeType

Intent         ::= "@" StringLit
Hint           ::= Identifier "(" Expr ")"
DefaultValue   ::= "=" Expr
WhereClause    ::= "where" "(" Expr ")"
Condition      ::= Expr ComparisonOp Expr
ComparisonOp   ::= "==" | "!=" | "<" | ">" | "<=" | ">="
FieldPath      ::= Identifier ("." Identifier)*
Backoff        ::= "backoff" "(" Identifier ("," KvPair)* ")"
```

---

# Appendix B: Built-in Functions Reference

## Data Operations

| Function | Signature | Description |
|----------|-----------|-------------|
| `fetch` | `fetch model(id)` | Get record by primary key |
| `query` | `query model where(...)` | Filter records |
| `query` | `query model where(...) order(field asc\|desc)` | Filter with ordering |
| `query` | `query model where(...) paginate(page, per_page)` | Filter with pagination |
| `query` | `query model where(...) first` | Get first match |
| `save` | `save model { fields }` | Insert record |
| `update` | `update model { fields }` | Update record |
| `delete` | `delete ref` | Delete record(s) |
| `count` | `count model where(...)` | Count matching records |

## Storage Operations

| Function | Signature | Description |
|----------|-----------|-------------|
| `upload` | `upload(file, bucket)` | Upload to S3/GCS |
| `download` | `download(url)` | Download file |
| `delete_s3_object` | `delete_s3_object(url)` | Delete S3 object |

## Utility Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `log` | `log "message" level(info\|warn\|error)` | Structured logging |
| `sleep` | `sleep duration` | Delay execution |
| `clock` | `clock()` | Current timestamp (ms) |
| `hash` | `hash(value)` | SHA-256 hash |
| `join` | `join room(name)` | Join WebSocket room |
| `leave` | `leave room(name)` | Leave WebSocket room |
| `broadcast` | `broadcast room(name) { data }` | Broadcast to room |
| `whisper` | `whisper connection(id) { data }` | Send to connection |
| `close` | `close` | Close WebSocket/SSE |

---

# Appendix C: Implementation Notes

## Compiler Pipeline

```
.bp source
    ↓
Lexer → Tokens
    ↓
Parser → AST
    ↓
Checker → Validated AST
    ↓
Codegen → TypeScript/Node.js project
```

## Error Handling Strategy

- **Lexer**: Character-level errors with position
- **Parser**: Panic-mode recovery to next block boundary
- **Checker**: Semantic errors with "did you mean?" suggestions
- **Codegen**: Should not fail on validated AST

## Code Generation Targets

| Target | Status | Output |
|--------|--------|--------|
| JavaScript/TypeScript | ✅ Complete | Hono + Drizzle + Zod |
| Python/FastAPI | 🚧 Planned | FastAPI + SQLAlchemy + Pydantic |
| Go/Chi | 🚧 Planned | Chi + sqlc + validator |

---

*This specification is maintained by the Blueprint project. For implementation details, see the AGENTS.md file in the source repository.*
