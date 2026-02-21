package ast

// Walk traverses an AST in depth-first order, calling the visitor methods.
// If a visitor method returns false, children of that node are not visited.
func Walk(node Node, v Visitor) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *File:
		if !v.VisitFile(n) {
			return
		}
		if n.Blueprint != nil {
			Walk(n.Blueprint, v)
		}
		for _, b := range n.Blocks {
			Walk(b, v)
		}

	case *Blueprint:
		if !v.VisitBlueprint(n) {
			return
		}
		for _, u := range n.Uses {
			walkUseStmt(u, v)
		}
	case *Secret:
		if !v.VisitSecret(n) {
			return
		}
		walkExpr(n.Default, v)
	case *Env:
		if !v.VisitEnv(n) {
			return
		}
		walkExpr(n.Value, v)
	case *Include:
		v.VisitInclude(n)
	case *TypeDecl:
		if !v.VisitTypeDecl(n) {
			return
		}
		walkFields(n.Fields, v)
	case *Alias:
		if !v.VisitAlias(n) {
			return
		}
		walkTypeExpr(n.Type, v)
		walkConstraints(n.Constraints, v)
	case *Enum:
		if !v.VisitEnum(n) {
			return
		}
		for _, variant := range n.Variants {
			if variant.Body != nil {
				walkBlockBody(variant.Body, v)
			}
		}
	case *Model:
		if !v.VisitModel(n) {
			return
		}
		walkFields(n.Fields, v)

	case *Fn:
		if !v.VisitFn(n) {
			return
		}
		for _, s := range n.Inputs {
			Walk(s, v)
		}
		for _, s := range n.Outputs {
			Walk(s, v)
		}
		if n.Logic != nil {
			for _, s := range n.Logic.Stmts {
				Walk(s, v)
			}
		}

	case *Pipe:
		if !v.VisitPipe(n) {
			return
		}
		walkArrowStmts(n.Stmts, v)

	case *Middleware:
		if !v.VisitMiddleware(n) {
			return
		}
		walkArrowStmts(n.Before, v)
		walkArrowStmts(n.After, v)

	case *Endpoint:
		if !v.VisitEndpoint(n) {
			return
		}
		walkArrowStmts(n.Stmts, v)

	case *StreamEndpoint:
		if !v.VisitStreamEndpoint(n) {
			return
		}
		walkArrowStmts(n.Stmts, v)
		for _, h := range n.Handlers {
			walkArrowStmts(h.Body, v)
		}

	case *WsEndpoint:
		if !v.VisitWsEndpoint(n) {
			return
		}
		walkArrowStmts(n.OnConnect, v)
		walkArrowStmts(n.OnMessage, v)
		walkArrowStmts(n.OnDisconnect, v)

	case *Worker:
		if !v.VisitWorker(n) {
			return
		}
		walkArrowStmts(n.Stmts, v)
		walkArrowStmts(n.OnFail, v)

	case *Schedule:
		if !v.VisitSchedule(n) {
			return
		}
		walkArrowStmts(n.Stmts, v)

	case *External:
		v.VisitExternal(n)

	case *Subscribe:
		if !v.VisitSubscribe(n) {
			return
		}
		walkArrowStmts(n.Stmts, v)

	case *Test:
		if !v.VisitTest(n) {
			return
		}
		walkArrowStmts(n.Setup, v)
		walkArrowStmts(n.Cleanup, v)

	case *TestGroup:
		v.VisitTestGroup(n)
	case *Fixture:
		v.VisitFixture(n)

	// Arrow statements
	case *InputStmt:
		if !v.VisitInputStmt(n) {
			return
		}
		walkTypeExpr(n.Type, v)
		walkConstraints(n.Constraints, v)
	case *StepStmt:
		if !v.VisitStepStmt(n) {
			return
		}
		walkExpr(n.Expr, v)
	case *GuardStmt:
		if !v.VisitGuardStmt(n) {
			return
		}
		walkExpr(n.Condition, v)
	case *WhenStmt:
		if !v.VisitWhenStmt(n) {
			return
		}
		walkExpr(n.Condition, v)
		walkExpr(n.Inline, v)
		walkArrowStmts(n.Body, v)
	case *OutputStmt:
		if !v.VisitOutputStmt(n) {
			return
		}
		walkExpr(n.Value, v)
	case *TryRecover:
		if !v.VisitTryRecover(n) {
			return
		}
		walkArrowStmts(n.Try, v)
		walkArrowStmts(n.Recover, v)
	case *IntentStep:
		v.VisitIntentStep(n)
	case *GenerateStep:
		v.VisitGenerateStep(n)

	// Expressions
	case *BinaryExpr:
		if !v.VisitBinaryExpr(n) {
			return
		}
		walkExpr(n.Left, v)
		walkExpr(n.Right, v)
	case *UnaryExpr:
		if !v.VisitUnaryExpr(n) {
			return
		}
		walkExpr(n.Operand, v)
	case *FnCall:
		if !v.VisitFnCall(n) {
			return
		}
		for _, a := range n.Args {
			walkExpr(a, v)
		}
	case *FieldAccess:
		if !v.VisitFieldAccess(n) {
			return
		}
		walkExpr(n.Base, v)
	case *IndexAccess:
		if !v.VisitIndexAccess(n) {
			return
		}
		walkExpr(n.Base, v)
		walkExpr(n.Index, v)
	case *Ident:
		v.VisitIdent(n)
	case *StringLit:
		v.VisitStringLit(n)
	case *IntLit:
		v.VisitIntLit(n)
	case *FloatLit:
		v.VisitFloatLit(n)
	case *BoolLit:
		v.VisitBoolLit(n)
	case *NullLit:
		v.VisitNullLit(n)
	case *NowLit:
		v.VisitNowLit(n)
	case *DurationLit:
		v.VisitDurationLit(n)
	case *SizeLit:
		v.VisitSizeLit(n)
	case *RateLit:
		v.VisitRateLit(n)
	case *ParenExpr:
		if !v.VisitParenExpr(n) {
			return
		}
		walkExpr(n.Expr, v)
	case *ListExpr:
		if !v.VisitListExpr(n) {
			return
		}
		for _, e := range n.Elements {
			walkExpr(e, v)
		}
	case *BlockExpr:
		if !v.VisitBlockExpr(n) {
			return
		}
		for _, kv := range n.Entries {
			walkExpr(kv.Value, v)
		}
	case *PathExpr:
		v.VisitPathExpr(n)

	// Type expressions
	case *PrimitiveType:
		v.VisitPrimitiveType(n)
	case *NamedType:
		v.VisitNamedType(n)
	case *ListType:
		if !v.VisitListType(n) {
			return
		}
		walkTypeExpr(n.Element, v)
	case *MapType:
		if !v.VisitMapType(n) {
			return
		}
		walkTypeExpr(n.Key, v)
		walkTypeExpr(n.Value, v)
	case *EnumInline:
		v.VisitEnumInline(n)
	case *MimeTypeExpr:
		v.VisitMimeTypeExpr(n)
	}
}

func walkArrowStmts(stmts []ArrowStmt, v Visitor) {
	for _, s := range stmts {
		Walk(s, v)
	}
}

func walkExpr(e Expr, v Visitor) {
	if e != nil {
		Walk(e, v)
	}
}

func walkTypeExpr(te TypeExpr, v Visitor) {
	if te != nil {
		Walk(te, v)
	}
}

func walkFields(fields []*Field, v Visitor) {
	for _, f := range fields {
		walkTypeExpr(f.Type, v)
		walkConstraints(f.Constraints, v)
	}
}

func walkConstraints(constraints []*Constraint_, v Visitor) {
	for _, c := range constraints {
		walkExpr(c.Value, v)
	}
}

func walkBlockBody(body *BlockBody, v Visitor) {
	for _, kv := range body.Entries {
		walkExpr(kv.Value, v)
	}
}

func walkUseStmt(u *UseStmt, v Visitor) {
	for _, arg := range u.Args {
		walkExpr(arg, v)
	}
	if u.Body != nil {
		walkBlockBody(u.Body, v)
	}
}
