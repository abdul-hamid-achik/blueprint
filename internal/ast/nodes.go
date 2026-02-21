package ast

import "github.com/abdul-hamid-achik/blueprint/internal/lexer"

// Node is the common interface for all AST nodes.
type Node interface {
	nodeType() string
	Location() lexer.Loc
}

// TopLevel is implemented by all top-level block AST nodes.
type TopLevel interface {
	Node
	topLevel()
}

// ArrowStmt is implemented by all arrow statement nodes.
type ArrowStmt interface {
	Node
	arrowStmt()
}

// Expr is implemented by all expression nodes.
type Expr interface {
	Node
	expr()
}

// TypeExpr is implemented by all type expression nodes.
type TypeExpr interface {
	Node
	typeExpr()
}

// --- Root ---

// File represents an entire .bp file.
type File struct {
	Loc       lexer.Loc
	Blueprint *Blueprint
	Blocks    []TopLevel
}

func (n *File) nodeType() string    { return "File" }
func (n *File) Location() lexer.Loc { return n.Loc }

// --- Top-Level Blocks ---

type Blueprint struct {
	Loc     lexer.Loc
	Intent  *Intent
	Name    string
	Entries []KVPair
	Uses    []*UseStmt
}

func (n *Blueprint) nodeType() string    { return "Blueprint" }
func (n *Blueprint) Location() lexer.Loc { return n.Loc }
func (n *Blueprint) topLevel()           {}

type Secret struct {
	Loc      lexer.Loc
	Name     string
	Required bool
	Default  Expr
}

func (n *Secret) nodeType() string    { return "Secret" }
func (n *Secret) Location() lexer.Loc { return n.Loc }
func (n *Secret) topLevel()           {}

type Env struct {
	Loc   lexer.Loc
	Name  string
	Value Expr
}

func (n *Env) nodeType() string    { return "Env" }
func (n *Env) Location() lexer.Loc { return n.Loc }
func (n *Env) topLevel()           {}

type Include struct {
	Loc  lexer.Loc
	Path string
}

func (n *Include) nodeType() string    { return "Include" }
func (n *Include) Location() lexer.Loc { return n.Loc }
func (n *Include) topLevel()           {}

type TypeDecl struct {
	Loc    lexer.Loc
	Name   string
	Fields []*Field
}

func (n *TypeDecl) nodeType() string    { return "TypeDecl" }
func (n *TypeDecl) Location() lexer.Loc { return n.Loc }
func (n *TypeDecl) topLevel()           {}

type Alias struct {
	Loc         lexer.Loc
	Name        string
	Type        TypeExpr
	Constraints []*Constraint_
}

func (n *Alias) nodeType() string    { return "Alias" }
func (n *Alias) Location() lexer.Loc { return n.Loc }
func (n *Alias) topLevel()           {}

type Enum struct {
	Loc      lexer.Loc
	Intent   *Intent
	Name     string
	Variants []*EnumVariant
}

func (n *Enum) nodeType() string    { return "Enum" }
func (n *Enum) Location() lexer.Loc { return n.Loc }
func (n *Enum) topLevel()           {}

type EnumVariant struct {
	Loc  lexer.Loc
	Name string
	Body *BlockBody
}

type Model struct {
	Loc    lexer.Loc
	Intent *Intent
	Name   string
	Fields []*Field
}

func (n *Model) nodeType() string    { return "Model" }
func (n *Model) Location() lexer.Loc { return n.Loc }
func (n *Model) topLevel()           {}

type Fn struct {
	Loc     lexer.Loc
	Intent  *Intent
	Name    string
	Inputs  []*InputStmt
	Outputs []*OutputStmt
	Impl    *ImplBlock
	Logic   *LogicBlock
}

func (n *Fn) nodeType() string    { return "Fn" }
func (n *Fn) Location() lexer.Loc { return n.Loc }
func (n *Fn) topLevel()           {}

type Pipe struct {
	Loc    lexer.Loc
	Intent *Intent
	Name   string
	Stmts  []ArrowStmt
}

func (n *Pipe) nodeType() string    { return "Pipe" }
func (n *Pipe) Location() lexer.Loc { return n.Loc }
func (n *Pipe) topLevel()           {}

type Middleware struct {
	Loc     lexer.Loc
	Intent  *Intent
	Name    string
	Before  []ArrowStmt
	After   []ArrowStmt
	Entries []KVPair
}

func (n *Middleware) nodeType() string    { return "Middleware" }
func (n *Middleware) Location() lexer.Loc { return n.Loc }
func (n *Middleware) topLevel()           {}

type Endpoint struct {
	Loc     lexer.Loc
	Intent  *Intent
	Method  string
	Path    string
	Meta    []*EndpointMeta
	Stmts   []ArrowStmt
	OnError *OnError
}

func (n *Endpoint) nodeType() string    { return "Endpoint" }
func (n *Endpoint) Location() lexer.Loc { return n.Loc }
func (n *Endpoint) topLevel()           {}

type StreamEndpoint struct {
	Loc      lexer.Loc
	Intent   *Intent
	Path     string
	Meta     []*EndpointMeta
	Stmts    []ArrowStmt
	Handlers []*StreamHandler
}

func (n *StreamEndpoint) nodeType() string    { return "StreamEndpoint" }
func (n *StreamEndpoint) Location() lexer.Loc { return n.Loc }
func (n *StreamEndpoint) topLevel()           {}

type WsEndpoint struct {
	Loc          lexer.Loc
	Intent       *Intent
	Path         string
	Meta         []*EndpointMeta
	OnConnect    []ArrowStmt
	OnMessage    []ArrowStmt
	OnDisconnect []ArrowStmt
}

func (n *WsEndpoint) nodeType() string    { return "WsEndpoint" }
func (n *WsEndpoint) Location() lexer.Loc { return n.Loc }
func (n *WsEndpoint) topLevel()           {}

type Worker struct {
	Loc    lexer.Loc
	Intent *Intent
	Name   string
	Meta   []*WorkerMeta
	Stmts  []ArrowStmt
	OnFail []ArrowStmt
}

func (n *Worker) nodeType() string    { return "Worker" }
func (n *Worker) Location() lexer.Loc { return n.Loc }
func (n *Worker) topLevel()           {}

type Schedule struct {
	Loc    lexer.Loc
	Intent *Intent
	Name   string
	Cron   string
	Stmts  []ArrowStmt
}

func (n *Schedule) nodeType() string    { return "Schedule" }
func (n *Schedule) Location() lexer.Loc { return n.Loc }
func (n *Schedule) topLevel()           {}

type External struct {
	Loc     lexer.Loc
	Name    string
	Entries []KVPair
}

func (n *External) nodeType() string    { return "External" }
func (n *External) Location() lexer.Loc { return n.Loc }
func (n *External) topLevel()           {}

type Subscribe struct {
	Loc    lexer.Loc
	Intent *Intent
	Event  string
	From   string
	Stmts  []ArrowStmt
}

func (n *Subscribe) nodeType() string    { return "Subscribe" }
func (n *Subscribe) Location() lexer.Loc { return n.Loc }
func (n *Subscribe) topLevel()           {}

type Test struct {
	Loc     lexer.Loc
	Intent  *Intent
	Name    string
	Target  *TestTarget
	Setup   []ArrowStmt
	Request *TestRequest
	Expect  []*Assertion
	Cleanup []ArrowStmt
}

func (n *Test) nodeType() string    { return "Test" }
func (n *Test) Location() lexer.Loc { return n.Loc }
func (n *Test) topLevel()           {}

type TestGroup struct {
	Loc         lexer.Loc
	Intent      *Intent
	Name        string
	SharedSetup []ArrowStmt
	Tests       []string
}

func (n *TestGroup) nodeType() string    { return "TestGroup" }
func (n *TestGroup) Location() lexer.Loc { return n.Loc }
func (n *TestGroup) topLevel()           {}

type Fixture struct {
	Loc       lexer.Loc
	Name      string
	FromPath  string          // "from" variant
	Generated *BlockBody      // "generated" variant
	SeedModel string          // "seed" variant
	SeedBody  *BlockBody      // "seed" variant
}

func (n *Fixture) nodeType() string    { return "Fixture" }
func (n *Fixture) Location() lexer.Loc { return n.Loc }
func (n *Fixture) topLevel()           {}

// --- Arrow Statements ---

type InputStmt struct {
	Loc         lexer.Loc
	Name        string
	Type        TypeExpr
	Constraints []*Constraint_
}

func (n *InputStmt) nodeType() string    { return "InputStmt" }
func (n *InputStmt) Location() lexer.Loc { return n.Loc }
func (n *InputStmt) arrowStmt()          {}

type StepStmt struct {
	Loc      lexer.Loc
	Binding  string // variable name if "x = expr", else ""
	Expr     Expr
}

func (n *StepStmt) nodeType() string    { return "StepStmt" }
func (n *StepStmt) Location() lexer.Loc { return n.Loc }
func (n *StepStmt) arrowStmt()          {}

type GuardStmt struct {
	Loc       lexer.Loc
	Condition Expr
	Status    string
	Message   string
}

func (n *GuardStmt) nodeType() string    { return "GuardStmt" }
func (n *GuardStmt) Location() lexer.Loc { return n.Loc }
func (n *GuardStmt) arrowStmt()          {}

type WhenStmt struct {
	Loc       lexer.Loc
	Condition Expr
	Inline    Expr        // for inline: |> when cond: expr
	Body      []ArrowStmt // for block: |> when cond { ... }
}

func (n *WhenStmt) nodeType() string    { return "WhenStmt" }
func (n *WhenStmt) Location() lexer.Loc { return n.Loc }
func (n *WhenStmt) arrowStmt()          {}

type OutputStmt struct {
	Loc    lexer.Loc
	Status string // HTTP status code like "200", or ""
	Value  Expr   // the response body expression
}

func (n *OutputStmt) nodeType() string    { return "OutputStmt" }
func (n *OutputStmt) Location() lexer.Loc { return n.Loc }
func (n *OutputStmt) arrowStmt()          {}

type TryRecover struct {
	Loc     lexer.Loc
	Try     []ArrowStmt
	Recover []ArrowStmt
}

func (n *TryRecover) nodeType() string    { return "TryRecover" }
func (n *TryRecover) Location() lexer.Loc { return n.Loc }
func (n *TryRecover) arrowStmt()          {}

type IntentStep struct {
	Loc  lexer.Loc
	Text string
}

func (n *IntentStep) nodeType() string    { return "IntentStep" }
func (n *IntentStep) Location() lexer.Loc { return n.Loc }
func (n *IntentStep) arrowStmt()          {}

type GenerateStep struct {
	Loc   lexer.Loc
	Text  string
	Hints []Hint
}

func (n *GenerateStep) nodeType() string    { return "GenerateStep" }
func (n *GenerateStep) Location() lexer.Loc { return n.Loc }
func (n *GenerateStep) arrowStmt()          {}

// --- Expressions ---

type BinaryExpr struct {
	Loc   lexer.Loc
	Op    string
	Left  Expr
	Right Expr
}

func (n *BinaryExpr) nodeType() string    { return "BinaryExpr" }
func (n *BinaryExpr) Location() lexer.Loc { return n.Loc }
func (n *BinaryExpr) expr()               {}

type UnaryExpr struct {
	Loc     lexer.Loc
	Op      string
	Operand Expr
}

func (n *UnaryExpr) nodeType() string    { return "UnaryExpr" }
func (n *UnaryExpr) Location() lexer.Loc { return n.Loc }
func (n *UnaryExpr) expr()               {}

type FnCall struct {
	Loc  lexer.Loc
	Name string
	Args []Expr
}

func (n *FnCall) nodeType() string    { return "FnCall" }
func (n *FnCall) Location() lexer.Loc { return n.Loc }
func (n *FnCall) expr()               {}

type FieldAccess struct {
	Loc   lexer.Loc
	Base  Expr
	Field string
}

func (n *FieldAccess) nodeType() string    { return "FieldAccess" }
func (n *FieldAccess) Location() lexer.Loc { return n.Loc }
func (n *FieldAccess) expr()               {}

type IndexAccess struct {
	Loc   lexer.Loc
	Base  Expr
	Index Expr
}

func (n *IndexAccess) nodeType() string    { return "IndexAccess" }
func (n *IndexAccess) Location() lexer.Loc { return n.Loc }
func (n *IndexAccess) expr()               {}

type Ident struct {
	Loc  lexer.Loc
	Name string
}

func (n *Ident) nodeType() string    { return "Ident" }
func (n *Ident) Location() lexer.Loc { return n.Loc }
func (n *Ident) expr()               {}

type StringLit struct {
	Loc   lexer.Loc
	Value string
}

func (n *StringLit) nodeType() string    { return "StringLit" }
func (n *StringLit) Location() lexer.Loc { return n.Loc }
func (n *StringLit) expr()               {}

type IntLit struct {
	Loc   lexer.Loc
	Value string
}

func (n *IntLit) nodeType() string    { return "IntLit" }
func (n *IntLit) Location() lexer.Loc { return n.Loc }
func (n *IntLit) expr()               {}

type FloatLit struct {
	Loc   lexer.Loc
	Value string
}

func (n *FloatLit) nodeType() string    { return "FloatLit" }
func (n *FloatLit) Location() lexer.Loc { return n.Loc }
func (n *FloatLit) expr()               {}

type BoolLit struct {
	Loc   lexer.Loc
	Value bool
}

func (n *BoolLit) nodeType() string    { return "BoolLit" }
func (n *BoolLit) Location() lexer.Loc { return n.Loc }
func (n *BoolLit) expr()               {}

type NullLit struct {
	Loc lexer.Loc
}

func (n *NullLit) nodeType() string    { return "NullLit" }
func (n *NullLit) Location() lexer.Loc { return n.Loc }
func (n *NullLit) expr()               {}

type NowLit struct {
	Loc lexer.Loc
}

func (n *NowLit) nodeType() string    { return "NowLit" }
func (n *NowLit) Location() lexer.Loc { return n.Loc }
func (n *NowLit) expr()               {}

type DurationLit struct {
	Loc   lexer.Loc
	Value string // e.g. "5s", "10min"
}

func (n *DurationLit) nodeType() string    { return "DurationLit" }
func (n *DurationLit) Location() lexer.Loc { return n.Loc }
func (n *DurationLit) expr()               {}

type SizeLit struct {
	Loc   lexer.Loc
	Value string // e.g. "10mb"
}

func (n *SizeLit) nodeType() string    { return "SizeLit" }
func (n *SizeLit) Location() lexer.Loc { return n.Loc }
func (n *SizeLit) expr()               {}

type RateLit struct {
	Loc   lexer.Loc
	Value string // e.g. "60/min"
}

func (n *RateLit) nodeType() string    { return "RateLit" }
func (n *RateLit) Location() lexer.Loc { return n.Loc }
func (n *RateLit) expr()               {}

type ParenExpr struct {
	Loc  lexer.Loc
	Expr Expr
}

func (n *ParenExpr) nodeType() string    { return "ParenExpr" }
func (n *ParenExpr) Location() lexer.Loc { return n.Loc }
func (n *ParenExpr) expr()               {}

type ListExpr struct {
	Loc      lexer.Loc
	Elements []Expr
}

func (n *ListExpr) nodeType() string    { return "ListExpr" }
func (n *ListExpr) Location() lexer.Loc { return n.Loc }
func (n *ListExpr) expr()               {}

type BlockExpr struct {
	Loc     lexer.Loc
	Entries []KVPair
}

func (n *BlockExpr) nodeType() string    { return "BlockExpr" }
func (n *BlockExpr) Location() lexer.Loc { return n.Loc }
func (n *BlockExpr) expr()               {}

type PathExpr struct {
	Loc   lexer.Loc
	Value string
}

func (n *PathExpr) nodeType() string    { return "PathExpr" }
func (n *PathExpr) Location() lexer.Loc { return n.Loc }
func (n *PathExpr) expr()               {}

// --- Type Expressions ---

type PrimitiveType struct {
	Loc  lexer.Loc
	Name string // "string", "int", "float", etc.
}

func (n *PrimitiveType) nodeType() string    { return "PrimitiveType" }
func (n *PrimitiveType) Location() lexer.Loc { return n.Loc }
func (n *PrimitiveType) typeExpr()           {}

type NamedType struct {
	Loc  lexer.Loc
	Name string
}

func (n *NamedType) nodeType() string    { return "NamedType" }
func (n *NamedType) Location() lexer.Loc { return n.Loc }
func (n *NamedType) typeExpr()           {}

type ListType struct {
	Loc     lexer.Loc
	Element TypeExpr
}

func (n *ListType) nodeType() string    { return "ListType" }
func (n *ListType) Location() lexer.Loc { return n.Loc }
func (n *ListType) typeExpr()           {}

type MapType struct {
	Loc   lexer.Loc
	Key   TypeExpr
	Value TypeExpr
}

func (n *MapType) nodeType() string    { return "MapType" }
func (n *MapType) Location() lexer.Loc { return n.Loc }
func (n *MapType) typeExpr()           {}

type EnumInline struct {
	Loc      lexer.Loc
	Variants []string
}

func (n *EnumInline) nodeType() string    { return "EnumInline" }
func (n *EnumInline) Location() lexer.Loc { return n.Loc }
func (n *EnumInline) typeExpr()           {}

type MimeTypeExpr struct {
	Loc     lexer.Loc
	Type    string // e.g. "image"
	Subtype string // e.g. "*" or "png"
}

func (n *MimeTypeExpr) nodeType() string    { return "MimeTypeExpr" }
func (n *MimeTypeExpr) Location() lexer.Loc { return n.Loc }
func (n *MimeTypeExpr) typeExpr()           {}

// --- Supporting Types ---

type Intent struct {
	Loc  lexer.Loc
	Text string
}

func (n *Intent) nodeType() string    { return "Intent" }
func (n *Intent) Location() lexer.Loc { return n.Loc }

type Field struct {
	Loc         lexer.Loc
	Name        string
	Type        TypeExpr
	Constraints []*Constraint_
}

type Constraint_ struct {
	Loc   lexer.Loc
	Kind  string // "primary", "unique", "index", "required", "optional", "auto", "default", "ref", "min", "max", "format"
	Value Expr   // nil for bare constraints (primary, unique, etc.)
}

type KVPair struct {
	Loc   lexer.Loc
	Key   string
	Value Expr
}

type BlockBody struct {
	Loc     lexer.Loc
	Entries []KVPair
}

type UseStmt struct {
	Loc  lexer.Loc
	Name string
	Args []Expr
	Body *BlockBody
}

type EndpointMeta struct {
	Loc   lexer.Loc
	Kind  string // "use", "auth", "limit", "cache", "tags", "timeout"
	Value Expr
	Use   *UseStmt
}

type WorkerMeta struct {
	Loc   lexer.Loc
	Kind  string // "trigger", "retry", "timeout"
	Value Expr
	Extra []KVPair // for backoff params
}

type OnError struct {
	Loc     lexer.Loc
	Status  string
	Message string
}

type StreamHandler struct {
	Loc       lexer.Loc
	EventName string
	Condition Expr
	Timeout   string
	Body      []ArrowStmt
}

type TestTarget struct {
	Loc    lexer.Loc
	Method string
	Path   string
}

type TestRequest struct {
	Loc     lexer.Loc
	Repeat  int
	Entries []KVPair
}

type Assertion struct {
	Loc  lexer.Loc
	Raw  string // raw text of the assertion for now
	Kind string // "status", "body", "header", "duration", "model", "last_status"
	Expr Expr   // parsed expression if applicable
}

type Hint struct {
	Loc   lexer.Loc
	Name  string
	Value Expr
}

type ImplBlock struct {
	Loc      lexer.Loc
	Strategy string // "node", "exec", "http", "generate"
	Entries  []KVPair
}

type LogicBlock struct {
	Loc   lexer.Loc
	Stmts []ArrowStmt
}
