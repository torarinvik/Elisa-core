package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) analyzeDeferStmt(stmt *ast.DeferStmt) {
	if stmt == nil {
		return
	}
	if stmt.Mode == ast.DeferModeFunction && a.currentNonGlobalScopeDepth() != 1 {
		a.errorf(stmt.Pos(), "defer function is currently only supported in the outermost function scope")
	}
	a.validateDeferStmtBody(stmt.Body)
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	a.analyzeBlockWithAffineClone(stmt.Body, NewScope(a.currentScope))
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	collector := newDeferCaptureCollector(a, a.currentScope)
	collector.collectStmts(stmt.Body)
	if a.deferInfo == nil {
		a.deferInfo = map[*ast.DeferStmt]*DeferInfo{}
	}
	a.deferInfo[stmt] = &DeferInfo{Mode: stmt.Mode, Captures: append([]string(nil), collector.captureOrder...)}
}

func (a *Analyzer) analyzeScopeStmt(stmt *ast.ScopeStmt) {
	if stmt == nil {
		return
	}
	guardType := a.analyzeExpr(stmt.Guard)
	if !IsInvalidType(guardType) && len(CreateTypeBoundOps(guardType)) == 0 {
		a.errorf(stmt.Guard.Pos(), "scope guard requires a value with synthesized cleanup behavior, got %s", guardType)
	}
	a.analyzeBlockWithAffineClone(stmt.Body, NewScope(a.currentScope))
}

func checkpointDArraySourceType(t Type) (*DArrayType, bool) {
	switch tt := t.(type) {
	case *DArrayType:
		return tt, true
	case *RefType:
		if tt == nil || tt.Elem == nil || tt.State != RefStateNonNull {
			return nil, false
		}
		darrayType, ok := tt.Elem.(*DArrayType)
		return darrayType, ok
	default:
		return nil, false
	}
}

func (a *Analyzer) currentNonGlobalScopeDepth() int {
	depth := 0
	for scope := a.currentScope; scope != nil && scope != a.globalScope; scope = scope.Parent {
		depth++
	}
	return depth
}

func (a *Analyzer) validateDeferStmtBody(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		a.validateDeferStmtBodyStmt(stmt)
	}
}

func (a *Analyzer) validateDeferStmtBodyStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.MoveBindStmt:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Store)
	case *ast.DeferStmt:
		a.errorf(n.Pos(), "nested defer is not supported inside defer bodies")
		a.validateDeferStmtBody(n.Body)
	case *ast.AssignStmt:
		a.validateDeferStmtBodyExpr(n.Target)
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.AugAssignStmt:
		a.validateDeferStmtBodyExpr(n.Target)
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.AsRefAssignStmt:
		a.validateDeferStmtBodyExpr(n.Target)
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.ReturnStmt:
		a.errorf(n.Pos(), "defer body cannot return from the enclosing function")
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.IfStmt:
		a.validateDeferStmtBodyExpr(n.Cond)
		a.validateDeferStmtBody(n.Then)
		for _, elif := range n.Elifs {
			a.validateDeferStmtBodyExpr(elif.Cond)
			a.validateDeferStmtBody(elif.Body)
		}
		a.validateDeferStmtBody(n.Else)
	case *ast.MatchStmt:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Store)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	case *ast.InStoreStmt:
		a.validateDeferStmtBodyExpr(n.Store)
		a.validateDeferStmtBody(n.Body)
	case *ast.CanStmt:
		a.validateDeferStmtBody(n.Body)
	case *ast.PoolStmt:
		a.validateDeferStmtBodyExpr(n.Workers)
		a.validateDeferStmtBody(n.Body)
	case *ast.LockStmt:
		a.validateDeferStmtBodyExpr(n.Mutex)
		a.validateDeferStmtBody(n.Body)
	case *ast.WhileStmt:
		a.validateDeferStmtBodyExpr(n.Cond)
		a.validateDeferStmtBody(n.Body)
	case *ast.ForStmt:
		a.validateDeferStmtBodyExpr(n.Start)
		a.validateDeferStmtBodyExpr(n.End)
		a.validateDeferStmtBodyExpr(n.Step)
		a.validateDeferStmtBody(n.Body)
	case *ast.IterForStmt:
		a.validateDeferStmtBodyExpr(n.Source)
		a.validateDeferStmtBodyExpr(n.WhereFilter)
		a.validateDeferStmtBodyExpr(n.Filter)
		a.validateDeferStmtBody(n.Body)
	case *ast.ParallelForStmt:
		a.validateDeferStmtBodyExpr(n.Source)
		a.validateDeferStmtBody(n.Body)
	case *ast.PanicStmt:
		a.validateDeferStmtBodyExpr(n.Message)
	case *ast.ExprStmt:
		a.validateDeferStmtBodyExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, active := range a.activeStmtBranch(n) {
			a.validateDeferStmtBodyStmt(active)
		}
	case *ast.StaticErrorStmt:
		a.validateDeferStmtBodyExpr(n.Message)
	case *ast.DiscardStmt:
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.RegionStmt:
		a.validateDeferStmtBodyExpr(n.Capacity)
		for _, stmt := range n.Body {
			a.validateDeferStmtBodyStmt(stmt)
		}
	case *ast.PassStmt, *ast.SignalStmt, *ast.MarkStmt, *ast.RestoreStmt, *ast.ResetStmt, *ast.DestroyStmt:
		return
	}
}

func (a *Analyzer) validateDeferStmtBodyExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		a.validateDeferStmtBodyExpr(n.Left)
		a.validateDeferStmtBodyExpr(n.Right)
	case *ast.UnaryExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.CallExpr:
		a.validateDeferStmtBodyExpr(n.Func)
		a.validateDeferStmtBodyExpr(n.SafeReceiver)
		for _, arg := range n.Args {
			a.validateDeferStmtBodyExpr(arg)
		}
	case *ast.FieldExpr:
		a.validateDeferStmtBodyExpr(n.Object)
	case *ast.IndexExpr:
		a.validateDeferStmtBodyExpr(n.Object)
		a.validateDeferStmtBodyExpr(n.Index)
		a.validateDeferStmtBodyExpr(n.Fallback)
	case *ast.SliceExpr:
		a.validateDeferStmtBodyExpr(n.Object)
		a.validateDeferStmtBodyExpr(n.Start)
		a.validateDeferStmtBodyExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.validateDeferStmtBodyExpr(elem)
		}
	case *ast.CastExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.TernaryExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Cond)
		a.validateDeferStmtBodyExpr(n.Alt)
	case *ast.AddrOfExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.MoveExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.SpecializeExpr:
		a.validateDeferStmtBodyExpr(n.Operand)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			a.validateDeferStmtBodyExpr(arg)
		}
	case *ast.ParenExpr:
		a.validateDeferStmtBodyExpr(n.Inner)
	case *ast.RaiseExpr:
		a.errorf(n.Pos(), "defer body cannot raise from the enclosing function")
		a.validateDeferStmtBodyExpr(n.Error)
	case *ast.TryExpr:
		if n.Fallback == nil {
			a.errorf(n.Pos(), "defer body cannot use try propagation without an else fallback")
		}
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Fallback)
	case *ast.CatchExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBody(n.Success.Body)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	case *ast.UnwrapElseExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Fallback)
	case *ast.OptionalBindExpr:
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.AllocExpr:
		a.validateDeferStmtBodyExpr(n.Owner)
		a.validateDeferStmtBodyExpr(n.NodeSpan)
		a.validateDeferStmtBodyExpr(n.Value)
	case *ast.CanExpr:
		a.validateDeferStmtBodyExpr(n.Expr)
	case *ast.MatchExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		a.validateDeferStmtBodyExpr(n.Store)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	case *ast.VisitExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	case *ast.FoldExpr:
		a.validateDeferStmtBodyExpr(n.Value)
		for _, arm := range n.Arms {
			a.validateDeferStmtBody(arm.Body)
		}
	}
}

func (a *Analyzer) clonePackedVariantViewBindings() map[*Symbol]*PackedVariantViewType {
	if a.currentPackedVariantViews == nil {
		return nil
	}
	cloned := make(map[*Symbol]*PackedVariantViewType, len(a.currentPackedVariantViews))
	for sym, viewType := range a.currentPackedVariantViews {
		cloned[sym] = viewType
	}
	return cloned
}

func unwrapPackedVariantViewExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapPackedVariantViewExpr(n.Inner)
	case *ast.CastExpr:
		return unwrapPackedVariantViewExpr(n.Operand)
	case *ast.CanExpr:
		return unwrapPackedVariantViewExpr(n.Expr)
	default:
		return expr
	}
}

func directValueBindingAliasRoot(scope *Scope, expr ast.Expr) *Symbol {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return directValueBindingAliasRoot(scope, n.Inner)
	case *ast.Ident:
		if scope == nil {
			return nil
		}
		sym, ok := scope.Lookup(n.Name)
		if !ok || sym == nil {
			return nil
		}
		if root := symbolAliasRoot(sym); root != nil {
			return root
		}
		return sym
	default:
		return nil
	}
}

func (a *Analyzer) aliasRootRefinementKey(scope *Scope, expr ast.Expr) (string, bool) {
	if scope == nil {
		return "", false
	}
	ident, ok := unwrapPackedVariantViewExpr(expr).(*ast.Ident)
	if !ok || ident == nil {
		return "", false
	}
	sym, ok := scope.Lookup(ident.Name)
	if !ok || sym == nil {
		return "", false
	}
	root := symbolAliasRoot(sym)
	if root == nil || root == sym || root.Name == "" {
		return "", false
	}
	return root.Name, true
}

func (a *Analyzer) bindRefinedExprType(scope *Scope, expr ast.Expr, refined Type) {
	if scope == nil || refined == nil {
		return
	}
	key, ok := exprRefinementKey(unwrapPackedVariantViewExpr(expr))
	if aliasKey, aliasOK := a.aliasRootRefinementKey(scope, expr); aliasOK {
		key = aliasKey
		ok = true
	}
	if !ok {
		return
	}
	scope.Refinements[key] = refined
}

func (a *Analyzer) bindMatchedPackedVariantView(expr ast.Expr, viewType *PackedVariantViewType) {
	if viewType == nil || a.currentScope == nil {
		return
	}
	a.bindRefinedExprType(a.currentScope, expr, viewType)
	ident, ok := unwrapPackedVariantViewExpr(expr).(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return
	}
	if a.currentPackedVariantViews == nil {
		a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	}
	a.currentPackedVariantViews[sym] = viewType
}

func (a *Analyzer) lookupRefinedPackedVariantView(expr ast.Expr) (*PackedVariantViewType, bool) {
	if a.currentPackedVariantViews == nil || a.currentScope == nil {
		return nil, false
	}
	ident, ok := unwrapPackedVariantViewExpr(expr).(*ast.Ident)
	if !ok {
		return nil, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if ok && sym != nil {
		if viewType, ok := a.currentPackedVariantViews[sym]; ok && viewType != nil {
			return viewType, true
		}
	}
	for candidate, viewType := range a.currentPackedVariantViews {
		if candidate != nil && candidate.Name == ident.Name && viewType != nil {
			return viewType, true
		}
	}
	return nil, false
}

func (a *Analyzer) clearPackedVariantViewExpr(expr ast.Expr) {
	if a.currentScope == nil {
		return
	}
	if key, ok := exprRefinementKey(unwrapPackedVariantViewExpr(expr)); ok {
		for scope := a.currentScope; scope != nil; scope = scope.Parent {
			if refined, exists := scope.Refinements[key]; exists {
				if _, isPackedView := refined.(*PackedVariantViewType); isPackedView {
					delete(scope.Refinements, key)
				}
				break
			}
		}
	}
	if a.currentPackedVariantViews == nil {
		return
	}
	ident, ok := unwrapPackedVariantViewExpr(expr).(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return
	}
	delete(a.currentPackedVariantViews, sym)
}
