package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (a *Analyzer) validatePermissionUsage(decls []scopedDecl) {
	for _, scoped := range decls {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			a.validateFunctionPermissionUsage(fn)
		})
	}
}

func (a *Analyzer) validateFunctionPermissionUsage(fn *ast.FuncDecl) {
	granted := map[string]bool{}
	savedFuncType := a.currentFuncType
	defer func() { a.currentFuncType = savedFuncType }()
	if sym, ok := a.symbolForFuncDecl(fn); ok {
		if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
			a.currentFuncType = fnType
		}
	}
	a.validatePermissionStmts(fn.Body, granted)
}

func grantedPermissionFamilies(families []string) map[string]bool {
	granted := make(map[string]bool, len(families))
	for _, family := range families {
		granted[family] = true
	}
	return granted
}

func missingGrantedPermissionFamilies(refs []ast.PermissionRef, granted map[string]bool) []string {
	families := permissionFamiliesFromRefs(refs)
	if len(families) == 0 {
		return nil
	}
	missing := make([]string, 0, len(families))
	for _, family := range families {
		if !granted[family] {
			missing = append(missing, family)
		}
	}
	return missing
}

func (a *Analyzer) warnOnMissingLocalGrant(pos lexer.Pos, label string, refs []ast.PermissionRef, granted map[string]bool) {
	missing := missingGrantedPermissionFamilies(refs, granted)
	if len(missing) == 0 {
		return
	}
	a.warnf(pos, effectAuthorityGrantMessage(label, missing, permissionGrantHint(refs, missing)))
}

func (a *Analyzer) warnOnRedundantLocalGrant(pos lexer.Pos, label string, refs []ast.PermissionRef, granted map[string]bool) {
	families := permissionFamiliesFromRefs(refs)
	if len(families) == 0 {
		return
	}
	for _, family := range families {
		if !granted[family] {
			return
		}
	}
	hint := strings.TrimSpace(permissionGrantHint(refs, families))
	a.warnf(pos, "%s grants %s redundantly; a surrounding can ...: block already grants these effects", label, hint)
}

func (a *Analyzer) validatePermissionStmts(stmts []ast.Stmt, granted map[string]bool) {
	for _, stmt := range stmts {
		a.validatePermissionStmt(stmt, granted)
	}
}

func (a *Analyzer) validatePermissionStmt(stmt ast.Stmt, granted map[string]bool) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Value != nil {
			a.validatePermissionExpr(n.Value, granted)
		}
	case *ast.LetDestructureStmt:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.TupleBindStmt:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.MoveBindStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
	case *ast.ArgsScopeStmt:
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg.Value, granted)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				a.validatePermissionExpr(arg.Value, granted)
			}
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.DeferStmt:
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.AssignStmt:
		a.validatePermissionExpr(n.Target, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.AugAssignStmt:
		a.validatePermissionExpr(n.Target, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.AsRefAssignStmt:
		a.validatePermissionExpr(n.Target, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.ReturnStmt:
		if n.Value != nil {
			a.validatePermissionExpr(n.Value, granted)
		}
	case *ast.IfStmt:
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionStmts(n.Then, cloneGrantedPermissionFamilies(granted))
		for _, elif := range n.Elifs {
			a.validatePermissionExpr(elif.Cond, granted)
			a.validatePermissionStmts(elif.Body, cloneGrantedPermissionFamilies(granted))
		}
		a.validatePermissionStmts(n.Else, cloneGrantedPermissionFamilies(granted))
	case *ast.MatchStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
		for _, arm := range n.Arms {
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.InStoreStmt:
		a.validatePermissionExpr(n.Store, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.CanStmt:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		if !n.SuppressPermissionInference {
			a.warnOnRedundantLocalGrant(n.Pos(), "can block", refs, granted)
		}
		families := permissionFamiliesFromRefs(refs)
		a.validatePermissionStmts(n.Body, extendGrantedPermissionFamilies(granted, families))
	case *ast.SignalStmt:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		a.warnOnMissingLocalGrant(n.Pos(), "signal", refs, granted)
	case *ast.PoolStmt:
		a.validatePermissionExpr(n.Workers, granted)
		if !granted["Pool"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Create"}, {Position: n.Position, Name: "Pool", Member: "Shutdown"}}
			a.warnOnMissingLocalGrant(n.Pos(), "pool scope", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.LockStmt:
		a.validatePermissionExpr(n.Mutex, granted)
		if !granted["Sync"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Sync", Member: "Lock"}, {Position: n.Position, Name: "Sync", Member: "Unlock"}}
			a.warnOnMissingLocalGrant(n.Pos(), "lock scope", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.WhileStmt:
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ForStmt:
		a.validatePermissionExpr(n.Start, granted)
		a.validatePermissionExpr(n.End, granted)
		if n.Step != nil {
			a.validatePermissionExpr(n.Step, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.IterForStmt:
		a.validatePermissionExpr(n.Source, granted)
		a.validatePermissionExpr(n.Filter, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ParallelForStmt:
		a.validatePermissionExpr(n.Source, granted)
		if !granted["Pool"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Submit"}, {Position: n.Position, Name: "Pool", Member: "WaitAll"}}
			a.warnOnMissingLocalGrant(n.Pos(), "parallel for", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.PanicStmt:
		a.warnOnMissingLocalGrant(n.Pos(), "panic", []ast.PermissionRef{{Position: n.Position, Name: "Abort", Member: "Panic"}}, granted)
		a.validatePermissionExpr(n.Message, granted)
	case *ast.ExprStmt:
		a.validatePermissionExpr(n.Expr, granted)
	case *ast.StaticIfStmt:
		for _, active := range a.activeStmtBranch(n) {
			a.validatePermissionStmt(active, granted)
		}
	case *ast.StaticErrorStmt:
		a.validatePermissionExpr(n.Message, granted)
	case *ast.DiscardStmt:
		a.validatePermissionExpr(n.Value, granted)
	}
}

func (a *Analyzer) validatePermissionExpr(expr ast.Expr, granted map[string]bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		a.validatePermissionExpr(n.Left, granted)
		a.validatePermissionExpr(n.Right, granted)
	case *ast.UnaryExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.CallExpr:
		a.validatePermissionExpr(n.Func, granted)
		a.validatePermissionExpr(n.SafeReceiver, granted)
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg, granted)
		}
		a.validateCallPermissions(n.Position, n.Func, granted)
	case *ast.FieldExpr:
		a.validatePermissionExpr(n.Object, granted)
	case *ast.IndexExpr:
		a.validatePermissionExpr(n.Object, granted)
		a.validatePermissionExpr(n.Index, granted)
		a.validatePermissionExpr(n.Fallback, granted)
	case *ast.SliceExpr:
		a.validatePermissionExpr(n.Object, granted)
		a.validatePermissionExpr(n.Start, granted)
		a.validatePermissionExpr(n.End, granted)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.validatePermissionExpr(elem, granted)
		}
	case *ast.CastExpr:
		a.validatePermissionExpr(n.Operand, granted)
		if sym, ok := a.resolvedCastHooks[n]; ok {
			if fnType, ok := sym.Type.(*FuncType); ok {
				a.validateRequiredPermissions(n.Position, fnType, granted)
			}
		}
	case *ast.TernaryExpr:
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionExpr(n.Alt, granted)
	case *ast.AddrOfExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.MoveExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.SpecializeExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.StructLitExpr:
		if call, ok := a.loweredInitCalls[n]; ok && call != nil {
			a.validatePermissionExpr(call, granted)
			break
		}
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg, granted)
		}
	case *ast.RecordUpdateExpr:
		a.validatePermissionExpr(n.Base, granted)
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg, granted)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			a.validatePermissionExpr(elem, granted)
		}
	case *ast.ParenExpr:
		a.validatePermissionExpr(n.Inner, granted)
	case *ast.RaiseExpr:
		a.validatePermissionExpr(n.Error, granted)
	case *ast.TryExpr:
		a.validatePermissionExpr(n.Value, granted)
		if n.Fallback != nil {
			a.validatePermissionExpr(n.Fallback, granted)
		}
	case *ast.UnwrapElseExpr:
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionExpr(n.Fallback, granted)
	case *ast.OptionalBindExpr:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.AllocExpr:
		if n.Owner != nil {
			a.validatePermissionExpr(n.Owner, granted)
		}
		a.validatePermissionExpr(n.NodeSpan, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.CanExpr:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		if !n.SuppressPermissionInference {
			a.warnOnRedundantLocalGrant(n.Pos(), "inline can", refs, granted)
		}
		families := permissionFamiliesFromRefs(refs)
		a.validatePermissionExpr(n.Expr, extendGrantedPermissionFamilies(granted, families))
	case *ast.MatchExpr:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
		for _, arm := range n.Arms {
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.VisitExpr:
		a.validatePermissionExpr(n.Value, granted)
		for _, arm := range n.Arms {
			a.validatePermissionExpr(arm.Guard, granted)
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.FoldExpr:
		a.validatePermissionExpr(n.Value, granted)
		for _, arm := range n.Arms {
			a.validatePermissionExpr(arm.Guard, granted)
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			a.validatePermissionExpr(target, granted)
		}
	}
}

func (a *Analyzer) validateCallPermissions(pos lexer.Pos, fnExpr ast.Expr, granted map[string]bool) {
	fnType, ok := a.exprTypes[fnExpr].(*FuncType)
	if !ok {
		return
	}
	a.validateRequiredPermissions(pos, fnType, granted)
}

func (a *Analyzer) validateRequiredPermissions(pos lexer.Pos, fnType *FuncType, granted map[string]bool) {
	if fnType == nil || len(fnType.Permissions) == 0 {
		return
	}
	if a.permissionWarningsSuppressedByGenericContext(fnType, granted) {
		return
	}
	missing := make([]string, 0)
	for _, family := range fnType.Permissions {
		if !granted[family] {
			missing = append(missing, family)
		}
	}
	if len(missing) == 0 {
		return
	}
	a.warnf(pos, effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), missing, permissionGrantHint(functionPermissionRefs(fnType), missing)))
}
