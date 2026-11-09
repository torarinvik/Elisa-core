package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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
	case *ast.RegionStmt:
		a.validateSegmentAgnosticStmts(n.Body)
	case *ast.ScopeStmt:
		a.validateSegmentAgnosticExpr(n.Guard)
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
		if fnType, ok := a.exprType(n.Func).(*FuncType); ok && funcTypeHasSegmentDependency(fnType) {
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
	case *ast.GetExpr:
		a.validateSegmentAgnosticExpr(n.Value)
		a.validateSegmentAgnosticExpr(n.Fallback)
	case *ast.OptionalBindExpr:
		a.validateSegmentAgnosticExpr(n.Value)
	case *ast.AllocExpr:
		a.validateSegmentAgnosticExpr(n.Owner)
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

func (a *Analyzer) validateReentrantSafeStmts(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		a.validateReentrantSafeStmt(stmt)
	}
}

func (a *Analyzer) validateReentrantSafeStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.LockStmt:
		a.errorf(n.Pos(), "@reentrant_safe code cannot enter lock blocks; async paths must be lock-free")
		a.validateReentrantSafeExpr(n.Mutex)
		a.validateReentrantSafeStmts(n.Body)
	case *ast.PoolStmt:
		a.errorf(n.Pos(), "@reentrant_safe code cannot spawn pool work; async paths must not schedule blocking or reentrant work")
		a.validateReentrantSafeExpr(n.Workers)
		a.validateReentrantSafeStmts(n.Body)
	case *ast.ParallelForStmt:
		a.errorf(n.Pos(), "@reentrant_safe code cannot use parallel for; async paths must not schedule blocking or reentrant work")
		a.validateReentrantSafeExpr(n.Source)
		a.validateReentrantSafeStmts(n.Body)
	case *ast.VarDeclStmt:
		a.validateReentrantSafeExpr(n.Value)
	case *ast.LetDestructureStmt:
		a.validateReentrantSafeExpr(n.Value)
	case *ast.TupleBindStmt:
		a.validateReentrantSafeExpr(n.Value)
	case *ast.MoveBindStmt:
		a.validateReentrantSafeExpr(n.Value)
		a.validateReentrantSafeExpr(n.Store)
	case *ast.DeferStmt:
		a.validateReentrantSafeStmts(n.Body)
	case *ast.AssignStmt:
		a.validateReentrantSafeExpr(n.Target)
		a.validateReentrantSafeExpr(n.Value)
	case *ast.AugAssignStmt:
		a.validateReentrantSafeExpr(n.Target)
		a.validateReentrantSafeExpr(n.Value)
	case *ast.AsRefAssignStmt:
		a.validateReentrantSafeExpr(n.Target)
		a.validateReentrantSafeExpr(n.Value)
	case *ast.ReturnStmt:
		a.validateReentrantSafeExpr(n.Value)
	case *ast.IfStmt:
		a.validateReentrantSafeExpr(n.Cond)
		a.validateReentrantSafeStmts(n.Then)
		for _, elif := range n.Elifs {
			a.validateReentrantSafeExpr(elif.Cond)
			a.validateReentrantSafeStmts(elif.Body)
		}
		a.validateReentrantSafeStmts(n.Else)
	case *ast.MatchStmt:
		a.validateReentrantSafeExpr(n.Value)
		a.validateReentrantSafeExpr(n.Store)
		for _, arm := range n.Arms {
			a.validateReentrantSafeStmts(arm.Body)
		}
	case *ast.InStoreStmt:
		a.validateReentrantSafeExpr(n.Store)
		a.validateReentrantSafeStmts(n.Body)
	case *ast.CanStmt:
		a.validateReentrantSafeStmts(n.Body)
	case *ast.RegionStmt:
		a.validateReentrantSafeStmts(n.Body)
	case *ast.ScopeStmt:
		a.validateReentrantSafeExpr(n.Guard)
		a.validateReentrantSafeStmts(n.Body)
	case *ast.WhileStmt:
		a.validateReentrantSafeExpr(n.Cond)
		a.validateReentrantSafeStmts(n.Body)
	case *ast.ForStmt:
		a.validateReentrantSafeExpr(n.Start)
		a.validateReentrantSafeExpr(n.End)
		a.validateReentrantSafeExpr(n.Step)
		a.validateReentrantSafeStmts(n.Body)
	case *ast.IterForStmt:
		a.validateReentrantSafeExpr(n.Source)
		a.validateReentrantSafeExpr(n.WhereFilter)
		a.validateReentrantSafeExpr(n.Filter)
		a.validateReentrantSafeStmts(n.Body)
	case *ast.PanicStmt:
		a.validateReentrantSafeExpr(n.Message)
	case *ast.ExprStmt:
		a.validateReentrantSafeExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, active := range a.activeStmtBranch(n) {
			a.validateReentrantSafeStmt(active)
		}
	case *ast.StaticErrorStmt:
		a.validateReentrantSafeExpr(n.Message)
	case *ast.StaticAssertStmt:
		a.validateReentrantSafeExpr(n.Cond)
		a.validateReentrantSafeExpr(n.Message)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			a.validateReentrantSafeExpr(item.Cond)
			a.validateReentrantSafeExpr(item.Message)
		}
	case *ast.StaticBlockStmt:
		a.validateReentrantSafeStmts(n.Body)
	case *ast.DiscardStmt:
		a.validateReentrantSafeExpr(n.Value)
	}
}

func (a *Analyzer) validateReentrantSafeExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.CallExpr:
		if fnType, ok := a.exprType(n.Func).(*FuncType); ok && fnType != nil && !fnType.HasReentrantSafe {
			a.errorf(n.Pos(), "@reentrant_safe code cannot call %q because it is not marked @reentrant_safe", fnType.Name)
		}
		a.validateReentrantSafeExpr(n.Func)
		a.validateReentrantSafeExpr(n.SafeReceiver)
		for _, arg := range n.Args {
			a.validateReentrantSafeExpr(arg)
		}
	case *ast.BinaryExpr:
		a.validateReentrantSafeExpr(n.Left)
		a.validateReentrantSafeExpr(n.Right)
	case *ast.UnaryExpr:
		a.validateReentrantSafeExpr(n.Operand)
	case *ast.FieldExpr:
		a.validateReentrantSafeExpr(n.Object)
	case *ast.IndexExpr:
		a.validateReentrantSafeExpr(n.Object)
		a.validateReentrantSafeExpr(n.Index)
		a.validateReentrantSafeExpr(n.Fallback)
	case *ast.SliceExpr:
		a.validateReentrantSafeExpr(n.Object)
		a.validateReentrantSafeExpr(n.Start)
		a.validateReentrantSafeExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.validateReentrantSafeExpr(elem)
		}
		a.validateReentrantSafeExpr(n.Owner)
	case *ast.CastExpr:
		a.validateReentrantSafeExpr(n.Operand)
	case *ast.TernaryExpr:
		a.validateReentrantSafeExpr(n.Value)
		a.validateReentrantSafeExpr(n.Cond)
		a.validateReentrantSafeExpr(n.Alt)
	case *ast.AddrOfExpr:
		a.validateReentrantSafeExpr(n.Operand)
	case *ast.MoveExpr:
		a.validateReentrantSafeExpr(n.Operand)
	case *ast.SpecializeExpr:
		a.validateReentrantSafeExpr(n.Operand)
	case *ast.StructLitExpr:
		if call, ok := a.loweredInitCalls[n]; ok && call != nil {
			a.validateReentrantSafeExpr(call)
			break
		}
		for _, arg := range n.Args {
			a.validateReentrantSafeExpr(arg)
		}
	case *ast.RecordUpdateExpr:
		a.validateReentrantSafeExpr(n.Base)
		for _, arg := range n.Args {
			a.validateReentrantSafeExpr(arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			a.validateReentrantSafeExpr(elem)
		}
	case *ast.ParenExpr:
		a.validateReentrantSafeExpr(n.Inner)
	case *ast.RaiseExpr:
		a.validateReentrantSafeExpr(n.Error)
	case *ast.TryExpr:
		a.validateReentrantSafeExpr(n.Value)
		a.validateReentrantSafeExpr(n.Fallback)
	case *ast.UnwrapElseExpr:
		a.validateReentrantSafeExpr(n.Value)
		a.validateReentrantSafeExpr(n.Fallback)
	case *ast.GetExpr:
		a.validateReentrantSafeExpr(n.Value)
		a.validateReentrantSafeExpr(n.Fallback)
	case *ast.OptionalBindExpr:
		a.validateReentrantSafeExpr(n.Value)
	case *ast.AllocExpr:
		a.validateReentrantSafeExpr(n.Owner)
		a.validateReentrantSafeExpr(n.Value)
	case *ast.CanExpr:
		a.validateReentrantSafeExpr(n.Expr)
	case *ast.MatchExpr:
		a.validateReentrantSafeExpr(n.Value)
		a.validateReentrantSafeExpr(n.Store)
		for _, arm := range n.Arms {
			a.validateReentrantSafeStmts(arm.Body)
		}
	case *ast.FoldExpr:
		a.validateReentrantSafeExpr(n.Value)
		for _, arm := range n.Arms {
			a.validateReentrantSafeExpr(arm.Guard)
			a.validateReentrantSafeStmts(arm.Body)
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			a.validateReentrantSafeExpr(target)
		}
	case *ast.IsAliasExpr:
		a.validateReentrantSafeExpr(n.Target)
	}
}
