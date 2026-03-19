package semantic

import (
	"sort"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) collectPermissionDecls(decls []ast.Decl) {
	for _, decl := range decls {
		permissionDecl, ok := decl.(*ast.PermissionDecl)
		if !ok {
			continue
		}
		if _, exists := a.permissions[permissionDecl.Name]; exists {
			a.errorf(permissionDecl.Pos(), "duplicate permission %q", permissionDecl.Name)
			continue
		}
		members := make([]string, 0, len(permissionDecl.Members))
		memberSet := make(map[string]bool, len(permissionDecl.Members))
		for _, member := range permissionDecl.Members {
			if memberSet[member] {
				a.errorf(permissionDecl.Pos(), "duplicate permission member %q in %q", member, permissionDecl.Name)
				continue
			}
			memberSet[member] = true
			members = append(members, member)
		}
		a.permissions[permissionDecl.Name] = &PermissionSet{Name: permissionDecl.Name, Members: members, MemberSet: memberSet, Decl: permissionDecl}
	}
}

func (a *Analyzer) resolvePermissionFamilies(refs []ast.PermissionRef, report bool) []string {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(refs))
	families := make([]string, 0, len(refs))
	for _, ref := range refs {
		permission, ok := a.permissions[ref.Name]
		if !ok {
			if report {
				a.errorf(ref.Position, "unknown permission %q", ref.Name)
			}
			continue
		}
		if ref.Member != "" && !permission.MemberSet[ref.Member] {
			if report {
				a.errorf(ref.Position, "permission %q has no member %q", ref.Name, ref.Member)
			}
			continue
		}
		if !seen[ref.Name] {
			seen[ref.Name] = true
			families = append(families, ref.Name)
		}
	}
	return families
}

func (a *Analyzer) recordFunctionPermissionFamilies(families []string) {
	if len(families) == 0 || a.currentFunctionUsedPermissions == nil {
		return
	}
	for _, family := range families {
		a.currentFunctionUsedPermissions[family] = true
	}
}

func sortedPermissionFamilies(families map[string]bool) []string {
	if len(families) == 0 {
		return nil
	}
	out := make([]string, 0, len(families))
	for family := range families {
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}

func missingPermissionFamilies(declared []string, used []string) []string {
	if len(used) == 0 {
		return nil
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, family := range declared {
		declaredSet[family] = true
	}
	missing := make([]string, 0)
	for _, family := range used {
		if !declaredSet[family] {
			missing = append(missing, family)
		}
	}
	return missing
}

func cloneGrantedPermissionFamilies(granted map[string]bool) map[string]bool {
	if len(granted) == 0 {
		return map[string]bool{}
	}
	cloned := make(map[string]bool, len(granted))
	for family := range granted {
		cloned[family] = true
	}
	return cloned
}

func extendGrantedPermissionFamilies(granted map[string]bool, families []string) map[string]bool {
	next := cloneGrantedPermissionFamilies(granted)
	for _, family := range families {
		next[family] = true
	}
	return next
}

func (a *Analyzer) validatePermissionUsage(decls []ast.Decl) {
	for _, decl := range decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		a.validateFunctionPermissionUsage(fn)
	}
}

func (a *Analyzer) validateFunctionPermissionUsage(fn *ast.FuncDecl) {
	a.validatePermissionStmts(fn.Body, map[string]bool{})
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
		families := a.resolvePermissionFamilies(n.Permissions, false)
		a.validatePermissionStmts(n.Body, extendGrantedPermissionFamilies(granted, families))
	case *ast.WhileStmt:
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.PanicStmt:
		a.validatePermissionExpr(n.Message, granted)
	case *ast.ExprStmt:
		a.validatePermissionExpr(n.Expr, granted)
	case *ast.StaticIfStmt:
		for _, stmt := range a.activeStmtBranch(n) {
			a.validatePermissionStmt(stmt, granted)
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
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg, granted)
		}
		a.validateCallPermissions(n.Position, n.Func, granted)
	case *ast.FieldExpr:
		a.validatePermissionExpr(n.Object, granted)
	case *ast.IndexExpr:
		a.validatePermissionExpr(n.Object, granted)
		a.validatePermissionExpr(n.Index, granted)
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
	case *ast.TernaryExpr:
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionExpr(n.Alt, granted)
	case *ast.AddrOfExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg, granted)
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
	case *ast.AllocExpr:
		if n.Owner != nil {
			a.validatePermissionExpr(n.Owner, granted)
		}
		a.validatePermissionExpr(n.Value, granted)
	case *ast.CanExpr:
		families := a.resolvePermissionFamilies(n.Permissions, false)
		a.validatePermissionExpr(n.Expr, extendGrantedPermissionFamilies(granted, families))
	case *ast.MatchExpr:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
		for _, arm := range n.Arms {
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	}
}

func (a *Analyzer) validateCallPermissions(pos lexer.Pos, fnExpr ast.Expr, granted map[string]bool) {
	fnType, ok := a.exprTypes[fnExpr].(*FuncType)
	if !ok || len(fnType.Permissions) == 0 {
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
	a.errorf(pos, "call to %q requires%s; wrap it in a can[...] annotation or can ...: block", fnType.Name, permissionFamiliesString(missing))
}
