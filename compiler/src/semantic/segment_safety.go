package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func permissionRefsContainSegmentDependency(refs []ast.PermissionRef) bool {
	for _, ref := range refs {
		if ref.Name == "Segment" && (ref.Member == "" || ref.Member == "Host" || ref.Member == "Guest") {
			return true
		}
		if ref.Name == "Unsafe" && ref.Member == "SegmentMutation" {
			return true
		}
	}
	return false
}

func funcTypeHasSegmentDependency(fnType *FuncType) bool {
	if fnType == nil {
		return false
	}
	return permissionRefsContainSegmentDependency(functionPermissionRefs(fnType))
}

func (a *Analyzer) validateSegmentAgnosticStmts(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		a.validateSegmentAgnosticStmt(stmt)
	}
}

func (a *Analyzer) validateSegmentAgnosticStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.LetDestructureStmt:
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.TupleBindStmt:
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.MoveBindStmt:
		a.validateSegmentAgnosticExpr(n.Value)
		a.validateSegmentAgnosticExpr(n.Store)
	case *ast.ArgsScopeStmt:
		for _, arg := range n.Args {
			a.validateSegmentAgnosticExpr(arg.Value)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				a.validateSegmentAgnosticExpr(arg.Value)
			}
		}
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.DeferStmt:
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.AssignStmt:
		a.validateSegmentAgnosticExpr(n.Target)
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.AugAssignStmt:
		a.validateSegmentAgnosticExpr(n.Target)
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.AsRefAssignStmt:
		a.validateSegmentAgnosticExpr(n.Target)
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.ReturnStmt:
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.IfStmt:
		a.validateSegmentAgnosticExpr(n.Cond)
		a.validateSegmentAgnosticStmts(n.Then)
		for _, elif := range n.Elifs {
			a.validateSegmentAgnosticExpr(elif.Cond)
			a.validateSegmentAgnosticStmts(elif.Body)
		}
		a.validateSegmentAgnosticStmts(n.Else)
	case *ast.MatchStmt:
		a.validateSegmentAgnosticExpr(n.Value)
		a.validateSegmentAgnosticExpr(n.Store)
		for _, arm := range n.Arms {
			a.validateSegmentAgnosticStmts(arm.Body)
		}
	case *ast.InStoreStmt:
		a.validateSegmentAgnosticExpr(n.Store)
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.CanStmt:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		if permissionRefsContainSegmentDependency(refs) {
			a.errorf(n.Pos(), "@segment_agnostic code cannot grant Segment.Host, Segment.Guest, or Unsafe.SegmentMutation; route segment establishment through a @segment_establishing entry thunk")
		}
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.SignalStmt:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		if permissionRefsContainSegmentDependency(refs) {
			a.errorf(n.Pos(), "@segment_agnostic code cannot signal Segment.Host, Segment.Guest, or Unsafe.SegmentMutation")
		}
	case *ast.PoolStmt:
		a.validateSegmentAgnosticExpr(n.Workers)
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.LockStmt:
		a.validateSegmentAgnosticExpr(n.Mutex)
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.WhileStmt:
		a.validateSegmentAgnosticExpr(n.Cond)
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.ForStmt:
		a.validateSegmentAgnosticExpr(n.Start)
		a.validateSegmentAgnosticExpr(n.End)
		a.validateSegmentAgnosticExpr(n.Step)
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.IterForStmt:
		a.validateSegmentAgnosticExpr(n.Source)
		a.validateSegmentAgnosticExpr(n.WhereFilter)
		a.validateSegmentAgnosticExpr(n.Filter)
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.ParallelForStmt:
		a.validateSegmentAgnosticExpr(n.Source)
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.PanicStmt:
		a.validateSegmentAgnosticExpr(n.Message)
	case *ast.ExprStmt:
		a.validateSegmentAgnosticExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, active := range a.activeStmtBranch(n) {
			a.validateSegmentAgnosticStmt(active)
		}
	case *ast.StaticErrorStmt:
		a.validateSegmentAgnosticExpr(n.Message)
	case *ast.StaticAssertStmt:
		a.validateSegmentAgnosticExpr(n.Cond)
		a.validateSegmentAgnosticExpr(n.Message)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			a.validateSegmentAgnosticExpr(item.Cond)
			a.validateSegmentAgnosticExpr(item.Message)
		}
	case *ast.StaticBlockStmt:
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.DiscardStmt:
		a.validateSegmentAgnosticExpr(n.Value)
	}
}

func (a *Analyzer) validateSegmentAgnosticExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		a.validateSegmentAgnosticExpr(n.Left)
		a.validateSegmentAgnosticExpr(n.Right)
	case *ast.UnaryExpr:
		a.validateSegmentAgnosticExpr(n.Operand)
	case *ast.CallExpr:
		if fnType, ok := a.exprTypes[n.Func].(*FuncType); ok && funcTypeHasSegmentDependency(fnType) {
			a.errorf(n.Pos(), "@segment_agnostic code cannot call %q because it requires Segment.Host, Segment.Guest, or Unsafe.SegmentMutation", fnType.Name)
		}
		a.validateSegmentAgnosticExpr(n.Func)
		a.validateSegmentAgnosticExpr(n.SafeReceiver)
		for _, arg := range n.Args {
			a.validateSegmentAgnosticExpr(arg)
		}
	case *ast.FieldExpr:
		a.validateSegmentAgnosticExpr(n.Object)
	case *ast.IndexExpr:
		a.validateSegmentAgnosticExpr(n.Object)
		a.validateSegmentAgnosticExpr(n.Index)
		a.validateSegmentAgnosticExpr(n.Fallback)
	case *ast.SliceExpr:
		a.validateSegmentAgnosticExpr(n.Object)
		a.validateSegmentAgnosticExpr(n.Start)
		a.validateSegmentAgnosticExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.validateSegmentAgnosticExpr(elem)
		}
		a.validateSegmentAgnosticExpr(n.Owner)
	case *ast.CastExpr:
		a.validateSegmentAgnosticExpr(n.Operand)
	case *ast.TernaryExpr:
		a.validateSegmentAgnosticExpr(n.Value)
		a.validateSegmentAgnosticExpr(n.Cond)
		a.validateSegmentAgnosticExpr(n.Alt)
	case *ast.AddrOfExpr:
		a.validateSegmentAgnosticExpr(n.Operand)
	case *ast.MoveExpr:
		a.validateSegmentAgnosticExpr(n.Operand)
	case *ast.SpecializeExpr:
		a.validateSegmentAgnosticExpr(n.Operand)
	case *ast.StructLitExpr:
		if call, ok := a.loweredInitCalls[n]; ok && call != nil {
			a.validateSegmentAgnosticExpr(call)
			break
		}
		for _, arg := range n.Args {
			a.validateSegmentAgnosticExpr(arg)
		}
	case *ast.RecordUpdateExpr:
		a.validateSegmentAgnosticExpr(n.Base)
		for _, arg := range n.Args {
			a.validateSegmentAgnosticExpr(arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			a.validateSegmentAgnosticExpr(elem)
		}
	case *ast.ParenExpr:
		a.validateSegmentAgnosticExpr(n.Inner)
	case *ast.RaiseExpr:
		a.validateSegmentAgnosticExpr(n.Error)
	case *ast.TryExpr:
		a.validateSegmentAgnosticExpr(n.Value)
		a.validateSegmentAgnosticExpr(n.Fallback)
	case *ast.UnwrapElseExpr:
		a.validateSegmentAgnosticExpr(n.Value)
		a.validateSegmentAgnosticExpr(n.Fallback)
	case *ast.OptionalBindExpr:
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.AllocExpr:
		a.validateSegmentAgnosticExpr(n.Owner)
		a.validateSegmentAgnosticExpr(n.NodeSpan)
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.CanExpr:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		if permissionRefsContainSegmentDependency(refs) {
			a.errorf(n.Pos(), "@segment_agnostic code cannot grant Segment.Host, Segment.Guest, or Unsafe.SegmentMutation")
		}
		a.validateSegmentAgnosticExpr(n.Expr)
	case *ast.MatchExpr:
		a.validateSegmentAgnosticExpr(n.Value)
		a.validateSegmentAgnosticExpr(n.Store)
		for _, arm := range n.Arms {
			a.validateSegmentAgnosticStmts(arm.Body)
		}
	case *ast.VisitExpr:
		a.validateSegmentAgnosticExpr(n.Value)
		for _, arm := range n.Arms {
			a.validateSegmentAgnosticExpr(arm.Guard)
			a.validateSegmentAgnosticStmts(arm.Body)
		}
	case *ast.FoldExpr:
		a.validateSegmentAgnosticExpr(n.Value)
		for _, arm := range n.Arms {
			a.validateSegmentAgnosticExpr(arm.Guard)
			a.validateSegmentAgnosticStmts(arm.Body)
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			a.validateSegmentAgnosticExpr(target)
		}
	case *ast.IsAliasExpr:
		a.validateSegmentAgnosticExpr(n.Target)
	}
}

func (a *Analyzer) validateSegmentAgnosticBackendDependency(fn *ast.FuncDecl, pos lexer.Pos) {
	if fn == nil || !funcHasAnnotation(fn, "segment_agnostic") {
		return
	}
	a.errorf(pos, "@segment_agnostic function %q has a backend-derived Segment.Host dependency; disable %%fs canary/TLS lowering or remove @segment_agnostic", fn.Name)
}
