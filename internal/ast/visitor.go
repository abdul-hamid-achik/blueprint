package ast

// Visitor defines methods for visiting each AST node type.
type Visitor interface {
	VisitFile(node *File) bool
	VisitBlueprint(node *Blueprint) bool
	VisitSecret(node *Secret) bool
	VisitEnv(node *Env) bool
	VisitInclude(node *Include) bool
	VisitTypeDecl(node *TypeDecl) bool
	VisitAlias(node *Alias) bool
	VisitEnum(node *Enum) bool
	VisitModel(node *Model) bool
	VisitFn(node *Fn) bool
	VisitPipe(node *Pipe) bool
	VisitMiddleware(node *Middleware) bool
	VisitEndpoint(node *Endpoint) bool
	VisitStreamEndpoint(node *StreamEndpoint) bool
	VisitWsEndpoint(node *WsEndpoint) bool
	VisitWorker(node *Worker) bool
	VisitSchedule(node *Schedule) bool
	VisitExternal(node *External) bool
	VisitSubscribe(node *Subscribe) bool
	VisitTest(node *Test) bool
	VisitTestGroup(node *TestGroup) bool
	VisitFixture(node *Fixture) bool
	VisitInputStmt(node *InputStmt) bool
	VisitStepStmt(node *StepStmt) bool
	VisitGuardStmt(node *GuardStmt) bool
	VisitWhenStmt(node *WhenStmt) bool
	VisitOutputStmt(node *OutputStmt) bool
	VisitTryRecover(node *TryRecover) bool
	VisitIntentStep(node *IntentStep) bool
	VisitGenerateStep(node *GenerateStep) bool
	VisitBinaryExpr(node *BinaryExpr) bool
	VisitUnaryExpr(node *UnaryExpr) bool
	VisitFnCall(node *FnCall) bool
	VisitFieldAccess(node *FieldAccess) bool
	VisitIndexAccess(node *IndexAccess) bool
	VisitIdent(node *Ident) bool
	VisitStringLit(node *StringLit) bool
	VisitIntLit(node *IntLit) bool
	VisitFloatLit(node *FloatLit) bool
	VisitBoolLit(node *BoolLit) bool
	VisitNullLit(node *NullLit) bool
	VisitNowLit(node *NowLit) bool
	VisitDurationLit(node *DurationLit) bool
	VisitSizeLit(node *SizeLit) bool
	VisitRateLit(node *RateLit) bool
	VisitParenExpr(node *ParenExpr) bool
	VisitListExpr(node *ListExpr) bool
	VisitBlockExpr(node *BlockExpr) bool
	VisitPathExpr(node *PathExpr) bool

	// Type expressions
	VisitPrimitiveType(node *PrimitiveType) bool
	VisitNamedType(node *NamedType) bool
	VisitListType(node *ListType) bool
	VisitMapType(node *MapType) bool
	VisitEnumInline(node *EnumInline) bool
	VisitMimeTypeExpr(node *MimeTypeExpr) bool
}

// BaseVisitor provides default no-op implementations for all visitor methods.
// Embed this in your visitor to only override the methods you care about.
type BaseVisitor struct{}

func (v *BaseVisitor) VisitFile(node *File) bool                   { return true }
func (v *BaseVisitor) VisitBlueprint(node *Blueprint) bool         { return true }
func (v *BaseVisitor) VisitSecret(node *Secret) bool               { return true }
func (v *BaseVisitor) VisitEnv(node *Env) bool                     { return true }
func (v *BaseVisitor) VisitInclude(node *Include) bool             { return true }
func (v *BaseVisitor) VisitTypeDecl(node *TypeDecl) bool           { return true }
func (v *BaseVisitor) VisitAlias(node *Alias) bool                 { return true }
func (v *BaseVisitor) VisitEnum(node *Enum) bool                   { return true }
func (v *BaseVisitor) VisitModel(node *Model) bool                 { return true }
func (v *BaseVisitor) VisitFn(node *Fn) bool                       { return true }
func (v *BaseVisitor) VisitPipe(node *Pipe) bool                   { return true }
func (v *BaseVisitor) VisitMiddleware(node *Middleware) bool        { return true }
func (v *BaseVisitor) VisitEndpoint(node *Endpoint) bool           { return true }
func (v *BaseVisitor) VisitStreamEndpoint(node *StreamEndpoint) bool { return true }
func (v *BaseVisitor) VisitWsEndpoint(node *WsEndpoint) bool       { return true }
func (v *BaseVisitor) VisitWorker(node *Worker) bool               { return true }
func (v *BaseVisitor) VisitSchedule(node *Schedule) bool           { return true }
func (v *BaseVisitor) VisitExternal(node *External) bool           { return true }
func (v *BaseVisitor) VisitSubscribe(node *Subscribe) bool         { return true }
func (v *BaseVisitor) VisitTest(node *Test) bool                   { return true }
func (v *BaseVisitor) VisitTestGroup(node *TestGroup) bool         { return true }
func (v *BaseVisitor) VisitFixture(node *Fixture) bool             { return true }
func (v *BaseVisitor) VisitInputStmt(node *InputStmt) bool         { return true }
func (v *BaseVisitor) VisitStepStmt(node *StepStmt) bool           { return true }
func (v *BaseVisitor) VisitGuardStmt(node *GuardStmt) bool         { return true }
func (v *BaseVisitor) VisitWhenStmt(node *WhenStmt) bool           { return true }
func (v *BaseVisitor) VisitOutputStmt(node *OutputStmt) bool       { return true }
func (v *BaseVisitor) VisitTryRecover(node *TryRecover) bool       { return true }
func (v *BaseVisitor) VisitIntentStep(node *IntentStep) bool       { return true }
func (v *BaseVisitor) VisitGenerateStep(node *GenerateStep) bool   { return true }
func (v *BaseVisitor) VisitBinaryExpr(node *BinaryExpr) bool       { return true }
func (v *BaseVisitor) VisitUnaryExpr(node *UnaryExpr) bool         { return true }
func (v *BaseVisitor) VisitFnCall(node *FnCall) bool               { return true }
func (v *BaseVisitor) VisitFieldAccess(node *FieldAccess) bool     { return true }
func (v *BaseVisitor) VisitIndexAccess(node *IndexAccess) bool     { return true }
func (v *BaseVisitor) VisitIdent(node *Ident) bool                 { return true }
func (v *BaseVisitor) VisitStringLit(node *StringLit) bool         { return true }
func (v *BaseVisitor) VisitIntLit(node *IntLit) bool               { return true }
func (v *BaseVisitor) VisitFloatLit(node *FloatLit) bool           { return true }
func (v *BaseVisitor) VisitBoolLit(node *BoolLit) bool             { return true }
func (v *BaseVisitor) VisitNullLit(node *NullLit) bool             { return true }
func (v *BaseVisitor) VisitNowLit(node *NowLit) bool               { return true }
func (v *BaseVisitor) VisitDurationLit(node *DurationLit) bool     { return true }
func (v *BaseVisitor) VisitSizeLit(node *SizeLit) bool             { return true }
func (v *BaseVisitor) VisitRateLit(node *RateLit) bool             { return true }
func (v *BaseVisitor) VisitParenExpr(node *ParenExpr) bool         { return true }
func (v *BaseVisitor) VisitListExpr(node *ListExpr) bool           { return true }
func (v *BaseVisitor) VisitBlockExpr(node *BlockExpr) bool         { return true }
func (v *BaseVisitor) VisitPathExpr(node *PathExpr) bool           { return true }
func (v *BaseVisitor) VisitPrimitiveType(node *PrimitiveType) bool { return true }
func (v *BaseVisitor) VisitNamedType(node *NamedType) bool         { return true }
func (v *BaseVisitor) VisitListType(node *ListType) bool           { return true }
func (v *BaseVisitor) VisitMapType(node *MapType) bool             { return true }
func (v *BaseVisitor) VisitEnumInline(node *EnumInline) bool       { return true }
func (v *BaseVisitor) VisitMimeTypeExpr(node *MimeTypeExpr) bool   { return true }
