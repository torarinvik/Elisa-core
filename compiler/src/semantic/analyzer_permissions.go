package semantic

import (
	"sort"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) collectPermissionDecls(decls []scopedDecl) {
	for _, scoped := range decls {
		permissionDecl, ok := scoped.Decl.(*ast.PermissionDecl)
		if !ok {
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, permissionDecl.Name)
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
		if existing, exists := a.permissions[qualifiedName]; exists {
			if existing.Builtin && permissionMembersMatch(existing, members) {
				continue
			}
			if existing.Builtin {
				a.errorf(permissionDecl.Pos(), "permission %q conflicts with the builtin members %q", qualifiedName, existing.Members)
				continue
			}
			a.errorf(permissionDecl.Pos(), "duplicate permission %q", qualifiedName)
			continue
		}
		a.permissions[qualifiedName] = &PermissionSet{Name: qualifiedName, Members: members, MemberSet: memberSet, Decl: permissionDecl}
	}
}

func permissionMembersMatch(existing *PermissionSet, members []string) bool {
	if existing == nil {
		return false
	}
	if len(existing.MemberSet) != len(members) {
		return false
	}
	for _, member := range members {
		if !existing.MemberSet[member] {
			return false
		}
	}
	return true
}

func permissionRefKey(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
}

func canonicalizePermissionRefs(refs []ast.PermissionRef) []ast.PermissionRef {
	if len(refs) == 0 {
		return nil
	}
	familyRefs := make(map[string]ast.PermissionRef)
	memberRefs := make(map[string]ast.PermissionRef)
	for _, ref := range refs {
		if ref.Name == "" {
			continue
		}
		if ref.Member == "" {
			familyRefs[ref.Name] = ast.PermissionRef{Position: ref.Position, Name: ref.Name}
			for key, existing := range memberRefs {
				if existing.Name == ref.Name {
					delete(memberRefs, key)
				}
			}
			continue
		}
		if _, ok := familyRefs[ref.Name]; ok {
			continue
		}
		memberRefs[permissionRefKey(ref)] = ast.PermissionRef{Position: ref.Position, Name: ref.Name, Member: ref.Member}
	}
	out := make([]ast.PermissionRef, 0, len(familyRefs)+len(memberRefs))
	for _, ref := range familyRefs {
		out = append(out, ref)
	}
	for _, ref := range memberRefs {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Member < out[j].Member
	})
	return out
}

func permissionFamiliesFromRefs(refs []ast.PermissionRef) []string {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name == "" || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		out = append(out, ref.Name)
	}
	sort.Strings(out)
	return out
}

func mergePermissionFamilies(left []string, right []string) []string {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(left)+len(right))
	for _, family := range left {
		if seen[family] {
			continue
		}
		seen[family] = true
		out = append(out, family)
	}
	for _, family := range right {
		if seen[family] {
			continue
		}
		seen[family] = true
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}

func mergePermissionRefs(left []ast.PermissionRef, right []ast.PermissionRef) []ast.PermissionRef {
	merged := make([]ast.PermissionRef, 0, len(left)+len(right))
	merged = append(merged, left...)
	merged = append(merged, right...)
	return canonicalizePermissionRefs(merged)
}

func functionPermissionRefs(fnType *FuncType) []ast.PermissionRef {
	if fnType == nil {
		return nil
	}
	if len(fnType.PermissionRefs) != 0 {
		return fnType.PermissionRefs
	}
	refs := make([]ast.PermissionRef, 0, len(fnType.Permissions))
	for _, family := range fnType.Permissions {
		refs = append(refs, ast.PermissionRef{Name: family})
	}
	return refs
}

func filterPermissionRefsByFamilies(refs []ast.PermissionRef, families []string) []ast.PermissionRef {
	if len(refs) == 0 || len(families) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(families))
	for _, family := range families {
		allowed[family] = true
	}
	out := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		if allowed[ref.Name] {
			out = append(out, ref)
		}
	}
	return canonicalizePermissionRefs(out)
}

func permissionDeclHint(refs []ast.PermissionRef, families []string) string {
	refs = filterPermissionRefsByFamilies(refs, families)
	if hint := PermissionRefsString(refs); hint != "" {
		return hint
	}
	return permissionFamiliesString(families)
}

func permissionGrantHint(refs []ast.PermissionRef, families []string) string {
	refs = filterPermissionRefsByFamilies(refs, families)
	if len(refs) == 0 {
		for _, family := range families {
			refs = append(refs, ast.PermissionRef{Name: family})
		}
	}
	refs = canonicalizePermissionRefs(refs)
	if len(refs) == 1 && refs[0].Member != "" {
		return "can " + PermissionRefString(refs[0])
	}
	return PermissionRefsString(refs)
}

func (a *Analyzer) resolvePermissionFamilies(refs []ast.PermissionRef, report bool) []string {
	return permissionFamiliesFromRefs(a.resolvePermissionRefs(refs, report))
}

func (a *Analyzer) resolvePermissionRefs(refs []ast.PermissionRef, report bool) []ast.PermissionRef {
	if len(refs) == 0 {
		return nil
	}
	valid := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		if a.lookupPermissionParam(ref.Name) {
			if ref.Member != "" {
				if report {
					a.errorf(ref.Position, "permission parameter %q does not support member access", ref.Name)
				}
				continue
			}
			valid = append(valid, ast.PermissionRef{Position: ref.Position, Name: ref.Name})
			continue
		}
		permission, _, ok := a.lookupVisiblePermission(ref.Name)
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
		valid = append(valid, ref)
	}
	return canonicalizePermissionRefs(valid)
}

func (a *Analyzer) recordFunctionPermissionFamilies(families []string) {
	if len(families) == 0 || a.currentFunctionUsedPermissions == nil {
		return
	}
	for _, family := range families {
		a.currentFunctionUsedPermissions[family] = true
	}
}

func (a *Analyzer) recordFunctionPermissionRefs(refs []ast.PermissionRef) {
	refs = canonicalizePermissionRefs(refs)
	if len(refs) == 0 {
		return
	}
	a.currentFunctionUsedPermissionRefs = append(a.currentFunctionUsedPermissionRefs, refs...)
	a.recordFunctionPermissionFamilies(permissionFamiliesFromRefs(refs))
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

func samePermissionRefs(left []ast.PermissionRef, right []ast.PermissionRef) bool {
	left = canonicalizePermissionRefs(left)
	right = canonicalizePermissionRefs(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Name != right[i].Name || left[i].Member != right[i].Member {
			return false
		}
	}
	return true
}

func sameStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (a *Analyzer) inferFunctionPermissionEffects(decls []scopedDecl) {
	for iter := 0; iter < len(decls)+4; iter++ {
		changed := false
		for _, scoped := range decls {
			fn, ok := scoped.Decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
				sym, ok := a.symbolForFuncDecl(fn)
				if !ok {
					return
				}
				fnType, ok := sym.Type.(*FuncType)
				if !ok || fnType == nil {
					return
				}
				usedRefs := a.collectFunctionPermissionRefs(fn)
				mergedRefs := mergePermissionRefs(fnType.DeclaredPermissionRefs, usedRefs)
				mergedFamilies := mergePermissionFamilies(fnType.DeclaredPermissions, permissionFamiliesFromRefs(mergedRefs))
				if !samePermissionRefs(fnType.PermissionRefs, mergedRefs) {
					fnType.PermissionRefs = mergedRefs
					changed = true
				}
				if !sameStringSlice(fnType.Permissions, mergedFamilies) {
					fnType.Permissions = mergedFamilies
					changed = true
				}
			})
		}
		if !changed {
			return
		}
	}
}

func (a *Analyzer) warnOnImplicitFunctionPermissions(decls []scopedDecl) {
	for _, scoped := range decls {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			sym, ok := a.symbolForFuncDecl(fn)
			if !ok {
				return
			}
			fnType, ok := sym.Type.(*FuncType)
			if !ok || fnType == nil {
				return
			}
			missing := missingPermissionFamilies(fnType.DeclaredPermissions, fnType.Permissions)
			if len(missing) == 0 {
				return
			}
			if !functionAnnotationsAllowDeclaredPermissionFamilies(fn, fnType, missing) {
				return
			}
			hint := permissionDeclHint(functionPermissionRefs(fnType), missing)
			if len(fnType.DeclaredPermissions) == 0 {
				a.warnf(fn.Pos(), "function %q infers%s from its body; add explicit%s to make the effect contract visible", fn.Name, permissionFamiliesString(missing), hint)
				return
			}
			a.warnf(fn.Pos(), "function %q declares%s but body also uses%s; add explicit%s to silence this warning", fn.Name, permissionFamiliesString(fnType.DeclaredPermissions), permissionFamiliesString(missing), hint)
		})
	}
}

func functionAnnotationsAllowDeclaredPermissionFamilies(fn *ast.FuncDecl, fnType *FuncType, families []string) bool {
	if fn == nil || fnType == nil || len(families) == 0 {
		return true
	}
	candidate := *fnType
	candidate.Permissions = mergePermissionFamilies(fnType.DeclaredPermissions, families)
	candidate.PermissionRefs = mergePermissionRefs(functionPermissionRefs(fnType), filterPermissionRefsByFamilies(functionPermissionRefs(fnType), families))
	for _, annotation := range fn.Annotations {
		if !isSupportedFunctionAnnotation(annotation.Name) {
			continue
		}
		if !annotationAllowsDeclaredPermissions(annotation.Name, &candidate) {
			return false
		}
	}
	return true
}

func (a *Analyzer) collectFunctionPermissionRefs(fn *ast.FuncDecl) []ast.PermissionRef {
	if fn == nil {
		return nil
	}
	collector := permissionEffectCollector{analyzer: a}
	collector.collectStmts(fn.Body)
	return collector.refs()
}

type permissionEffectCollector struct {
	analyzer *Analyzer
	seen     []ast.PermissionRef
}

func (c *permissionEffectCollector) refs() []ast.PermissionRef {
	return canonicalizePermissionRefs(c.seen)
}

func (c *permissionEffectCollector) addRefs(refs []ast.PermissionRef) {
	if len(refs) == 0 {
		return
	}
	c.seen = append(c.seen, refs...)
}

func (c *permissionEffectCollector) collectStmts(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		c.collectStmt(stmt)
	}
}

func (c *permissionEffectCollector) collectStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Value != nil {
			c.collectExpr(n.Value)
		}
	case *ast.TupleBindStmt:
		c.collectExpr(n.Value)
	case *ast.MoveBindStmt:
		c.collectExpr(n.Value)
		if n.Store != nil {
			c.collectExpr(n.Store)
		}
	case *ast.OpenStmt:
		c.collectExpr(n.Value)
		if n.Store != nil {
			c.collectExpr(n.Store)
		}
		c.collectStmts(n.Body)
	case *ast.ViewStmt:
		c.collectExpr(n.Value)
		if n.Store != nil {
			c.collectExpr(n.Store)
		}
		c.collectStmts(n.Body)
	case *ast.DeferStmt:
		c.collectStmts(n.Body)
	case *ast.AssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.AugAssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.AsRefAssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.ReturnStmt:
		c.collectExpr(n.Value)
	case *ast.IfStmt:
		c.collectExpr(n.Cond)
		c.collectStmts(n.Then)
		for _, elif := range n.Elifs {
			c.collectExpr(elif.Cond)
			c.collectStmts(elif.Body)
		}
		c.collectStmts(n.Else)
	case *ast.MatchStmt:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.InStoreStmt:
		c.collectExpr(n.Store)
		c.collectStmts(n.Body)
	case *ast.CanStmt:
		c.addRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false))
		c.collectStmts(n.Body)
	case *ast.PoolStmt:
		c.collectExpr(n.Workers)
		c.addRefs([]ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Create"}, {Position: n.Position, Name: "Pool", Member: "Shutdown"}})
		c.collectStmts(n.Body)
	case *ast.LockStmt:
		c.collectExpr(n.Mutex)
		c.addRefs([]ast.PermissionRef{{Position: n.Position, Name: "Sync", Member: "Lock"}, {Position: n.Position, Name: "Sync", Member: "Unlock"}})
		c.collectStmts(n.Body)
	case *ast.WhileStmt:
		c.collectExpr(n.Cond)
		c.collectStmts(n.Body)
	case *ast.ForStmt:
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
		if n.Step != nil {
			c.collectExpr(n.Step)
		}
		c.collectStmts(n.Body)
	case *ast.IterForStmt:
		c.collectExpr(n.Source)
		c.collectStmts(n.Body)
	case *ast.ParallelForStmt:
		c.collectExpr(n.Source)
		c.addRefs([]ast.PermissionRef{
			{Position: n.Position, Name: "Pool", Member: "Submit"},
			{Position: n.Position, Name: "Pool", Member: "WaitAll"},
			{Position: n.Position, Name: "Memory", Member: "Allocate"},
			{Position: n.Position, Name: "Memory", Member: "Release"},
			{Position: n.Position, Name: "Abort", Member: "Panic"},
			{Position: n.Position, Name: "Atomics", Member: "Load"},
			{Position: n.Position, Name: "Atomics", Member: "CompareExchange"},
		})
		c.collectStmts(n.Body)
	case *ast.PanicStmt:
		c.addRefs([]ast.PermissionRef{{Position: n.Position, Name: "Abort", Member: "Panic"}})
		c.collectExpr(n.Message)
	case *ast.ExprStmt:
		c.collectExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, active := range c.analyzer.activeStmtBranch(n) {
			c.collectStmt(active)
		}
	case *ast.StaticErrorStmt:
		c.collectExpr(n.Message)
	case *ast.DiscardStmt:
		c.collectExpr(n.Value)
	}
}

func (c *permissionEffectCollector) collectExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		c.collectExpr(n.Left)
		c.collectExpr(n.Right)
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand)
	case *ast.CallExpr:
		c.collectExpr(n.Func)
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
		if fnType, ok := c.analyzer.exprTypes[n.Func].(*FuncType); ok {
			c.addRefs(functionPermissionRefs(fnType))
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object)
	case *ast.IndexExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Index)
	case *ast.SliceExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem)
		}
	case *ast.CastExpr:
		c.collectExpr(n.Operand)
		if sym, ok := c.analyzer.resolvedCastHooks[n]; ok {
			if fnType, ok := sym.Type.(*FuncType); ok {
				c.addRefs(functionPermissionRefs(fnType))
			}
		}
	case *ast.TernaryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Cond)
		c.collectExpr(n.Alt)
	case *ast.AddrOfExpr:
		c.collectExpr(n.Operand)
	case *ast.MoveExpr:
		c.collectExpr(n.Operand)
	case *ast.SpecializeExpr:
		c.collectExpr(n.Operand)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem)
		}
	case *ast.ParenExpr:
		c.collectExpr(n.Inner)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error)
	case *ast.TryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
	case *ast.AllocExpr:
		c.collectExpr(n.Owner)
		c.collectExpr(n.Value)
	case *ast.CanExpr:
		c.addRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false))
		c.collectExpr(n.Expr)
	case *ast.MatchExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.VisitExpr:
		c.collectExpr(n.Value)
		for _, arm := range n.Arms {
			c.collectExpr(arm.Guard)
			c.collectStmts(arm.Body)
		}
	case *ast.FoldExpr:
		c.collectExpr(n.Value)
		for _, arm := range n.Arms {
			c.collectExpr(arm.Guard)
			c.collectStmts(arm.Body)
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			c.collectExpr(target)
		}
	}
}

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
	if sym, ok := a.symbolForFuncDecl(fn); ok {
		if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
			for _, family := range fnType.DeclaredPermissions {
				granted[family] = true
			}
		}
	}
	a.validatePermissionStmts(fn.Body, granted)
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
	case *ast.TupleBindStmt:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.MoveBindStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
	case *ast.OpenStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ViewStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
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
		families := a.resolvePermissionFamilies(n.Permissions, false)
		a.validatePermissionStmts(n.Body, extendGrantedPermissionFamilies(granted, families))
	case *ast.PoolStmt:
		a.validatePermissionExpr(n.Workers, granted)
		if !granted["Pool"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Create"}, {Position: n.Position, Name: "Pool", Member: "Shutdown"}}
			a.warnf(n.Pos(), "pool scope requires%s and has no explicit local effect grant; add %s or a surrounding can ...: block", permissionFamiliesString([]string{"Pool"}), permissionGrantHint(refs, []string{"Pool"}))
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.LockStmt:
		a.validatePermissionExpr(n.Mutex, granted)
		if !granted["Sync"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Sync", Member: "Lock"}, {Position: n.Position, Name: "Sync", Member: "Unlock"}}
			a.warnf(n.Pos(), "lock scope requires%s and has no explicit local effect grant; add %s or a surrounding can ...: block", permissionFamiliesString([]string{"Sync"}), permissionGrantHint(refs, []string{"Sync"}))
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
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ParallelForStmt:
		a.validatePermissionExpr(n.Source, granted)
		if !granted["Pool"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Submit"}, {Position: n.Position, Name: "Pool", Member: "WaitAll"}}
			a.warnf(n.Pos(), "parallel for requires%s and has no explicit local effect grant; add %s or a surrounding can ...: block", permissionFamiliesString([]string{"Pool"}), permissionGrantHint(refs, []string{"Pool"}))
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.PanicStmt:
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
	missing := make([]string, 0)
	for _, family := range fnType.Permissions {
		if !granted[family] {
			missing = append(missing, family)
		}
	}
	if len(missing) == 0 {
		return
	}
	a.warnf(pos, "call to %q requires%s and has no explicit local effect grant; add %s or a surrounding can ...: block", fnType.Name, permissionFamiliesString(missing), permissionGrantHint(functionPermissionRefs(fnType), missing))
}
