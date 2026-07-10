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
               | "enqueue"
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
ComparisonOp   ::= "==" | "!=" | "<" | ">" | "<=" | ">=" | "in"
FieldPath      ::= Identifier ("." Identifier)*
Backoff        ::= "backoff" "(" Identifier ("," KvPair)* ")"
```

`in` tests membership: `left in right` is true when `right` is a list-typed
input/field or a fetched collection, and `left` matches one of its elements or
one of a field projected from its elements (e.g. `id in tags.tag_id`). It is
lexed as its own keyword, parsed at the same precedence as the other
comparison operators, and translated by both the node target (Drizzle
`inArray`) and the python target (SQLAlchemy `.in_`).

`where(...)` accepts one `Expr` per the grammar above, but only a restricted
subset is actually emitted by the generators today: a single `Condition`, or
several comma-separated `Condition`s, which are combined with **AND** (e.g.
`where(status == "open", owner_id == user.id)`). Passing an explicit `or`
between two conditions inside a single `where(...)` argument is not a defined
composite predicate — the checker does not reject it, but codegen does not
special-case it, so the emitted code is not guaranteed to behave like a SQL
`OR`. Prefer `query`-then-filter in application code (or a `pipe`) until `or`
composition is specified. Substring/fuzzy matching (a `like` operator) is not
part of the grammar or lexer; the node target's ILIKE search behavior is a
naming-convention extension (parameters named `q`, `search`, `query`,
`keyword`, `term`, or `filter` are matched against text columns), not a
language keyword, pending a spec decision on first-class text search.

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
| JavaScript/TypeScript (`--target node`, default) | ✅ Complete — mature, the reference target | Hono + Drizzle + Zod |
| Python (`--target python`) | 🚧 Advanced — all 5 canonical examples (`examples/*.bp`) compile end-to-end; long-tail constructs are rejected with a specific error rather than silently mis-emitted (see BACKLOG.md, "Python target") | FastAPI + SQLAlchemy 2.0 + Pydantic v2 + Alembic |
| TypeScript on Effect (`--target effect`) | 🧱 Experimental scaffold, opt-in — emits the project shell and a `Config` secrets module; endpoint/model emission is not yet implemented | `@effect/platform` HttpApi + `@effect/schema` + `@effect/sql` |
| Go/Chi | 🗺️ Planned, not started | Chi + sqlc + validator |

See [docs/multi-target-codegen.md](https://github.com/abdul-hamid-achik/blueprint/blob/main/docs/multi-target-codegen.md)
for the full generator contract, per-command `--target` dispatch table, and
type/naming mappings — this table is a summary, not the source of truth for
target capability.

### Generated vs User-Owned Files

Code generators must distinguish managed output from user implementation code:

- Generated files are tracked in `.blueprint/manifest.json` and may be rewritten
  on each build.
- User-owned files are scaffolded only when missing and must not be overwritten.
- For JavaScript/TypeScript, `impl node { module: "./internal/X" }` maps to a
  generated wrapper under `src/functions/` and a user-owned implementation
  scaffold under `src/impl/functions/internal/X.ts`.

---

# Appendix D: CLI exit codes

Every `bp` subcommand returns one of these four codes (verified against
`cmd/bp/main.go`'s command dispatch); a new command must keep reusing them
rather than introducing new ones:

| Code | Meaning | Example |
|------|---------|---------|
| `0` | Success | `bp check` found no errors; `bp build`/`Generate` wrote output |
| `1` | Validation error | Lexer/parser syntax errors; `checker.Check` semantic errors; `include` resolution errors |
| `2` | Environment/file error | Unreadable input file; unknown/malformed `--target`; an output directory that fails the safety check; a flag combination the target doesn't support (e.g. `--react-query` with `--target python`) |
| `4` | Codegen error | The target's `Generate`/`Files` returned a non-nil error — most commonly `unsupportedFeatures()` rejecting a construct the target doesn't emit yet |

An AST that fails to check never reaches a generator, so `1` and `4` are
mutually exclusive in practice: everything a generator itself rejects is a
`4`, everything the parser/checker rejects is a `1`.

---

*This specification is maintained by the Blueprint project. For implementation details, see the AGENTS.md file in the source repository.*
