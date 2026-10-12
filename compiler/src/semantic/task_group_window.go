package semantic

import (
	"path/filepath"
	"reflect"

	"elisacore/src/ast"
)

// recordVerifiedTaskGroupSubmits proves the deliberately small manual TaskGroup
// window understood by Stage0: a local TaskGroup initialized by the canonical
// constructor, one matching wait later in the same statement list, and matching
// task_group_add(group, safe_submit(...)) calls nested only in ordinary counted
// loops and `can` blocks between them.
// Anything more complex keeps the existing conservative lifetime diagnostic.
func (a *Analyzer) recordVerifiedTaskGroupSubmits(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 || taskGroupFunctionShadowsAPI(fn.Body) || !a.taskGroupRuntimeAPIsTrusted() {
		return
	}
	if a.taskGroupSubmits == nil {
		a.taskGroupSubmits = map[*ast.CallExpr]TaskGroupSubmitInfo{}
	}

	walkTaskGroupStmtLists(fn.Body, func(body []ast.Stmt) {
		var declRow int = -1
		var groupName string
		for row, stmt := range body {
			decl, ok := stmt.(*ast.VarDeclStmt)
			if !ok || !taskGroupConstructorDecl(decl) {
				continue
			}
			if declRow >= 0 {
				return
			}
			declRow, groupName = row, decl.Name
		}
		if declRow < 0 || groupName == "" || taskGroupAPIName(groupName) {
			return
		}

		waitRow, waitCount := -1, 0
		for row := declRow + 1; row < len(body); row++ {
			if taskGroupWaitMatches(body[row], groupName) {
				waitRow, waitCount = row, waitCount+1
			}
		}
		if waitCount != 1 || waitRow <= declRow {
			return
		}
		if a.taskGroupAPIShadowedInCurrentScope() || taskGroupRangeShadowsAPI(body[:waitRow]) {
			return
		}
		if taskGroupRangeMentionsName(body[waitRow+1:], groupName) {
			return
		}

		var submits []taskGroupSubmitCandidate
		for _, stmt := range body[declRow+1 : waitRow] {
			if !taskGroupWindowStmtSafe(stmt, groupName, &submits) {
				return
			}
		}
		if len(submits) == 0 {
			return
		}
		added := false
		for _, submit := range submits {
			if _, exists := a.taskGroupSubmits[submit.call]; !exists {
				added = true
			}
			a.taskGroupSubmits[submit.call] = TaskGroupSubmitInfo{GroupName: groupName, AlreadyAdded: submit.alreadyAdded}
		}
		if added {
			a.invalidateStoreFlowSummaries()
		}
	})
}

// recordVerifiedTaskGroupSubmitsInDecls runs before body analysis so interprocedural
// store-flow summaries see every proved join, regardless of declaration order. The
// proof only needs the lexical value names visible at function entry; body-local
// shadowing is checked directly from the AST.
func (a *Analyzer) recordVerifiedTaskGroupSubmitsInDecls(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			savedFuncType := a.currentFuncType
			a.currentFuncType = nil
			defer func() { a.currentFuncType = savedFuncType }()
			record := func(fn *ast.FuncDecl) {
				if fn == nil {
					return
				}
				savedScope := a.currentScope
				a.currentScope = NewScope(a.globalScope)
				defer func() { a.currentScope = savedScope }()
				for _, param := range fn.Params {
					if param.Name != "" {
						a.currentScope.Symbols[param.Name] = &Symbol{Name: param.Name}
					}
				}
				for _, name := range fn.RegionParams {
					if name != "" {
						if _, exists := a.currentScope.Symbols[name]; !exists {
							a.currentScope.Symbols[name] = &Symbol{Name: name}
						}
					}
				}
				a.recordVerifiedTaskGroupSubmits(fn)
			}
			switch decl := scoped.Decl.(type) {
			case *ast.FuncDecl:
				record(decl)
			case *ast.ImplDecl:
				for _, member := range decl.Members {
					if fn, ok := member.(*ast.FuncDecl); ok {
						record(fn)
					}
				}
			}
		})
	}
}

func (a *Analyzer) taskGroupRuntimeAPIsTrusted() bool {
	if a == nil || a.globalScope == nil || a.currentScope == nil || a.taskGroupAPIShadowedInCurrentScope() {
		return false
	}
	lookup := func(name string) (*ast.FuncDecl, *FuncType, bool) {
		sym, ok := a.globalScope.Lookup(name)
		if !ok || sym == nil {
			return nil, nil, false
		}
		visible, resolvedName, isVisible := a.lookupVisibleGlobal(name)
		if !isVisible || visible != sym || resolvedName != name {
			return nil, nil, false
		}
		decl, declOK := sym.Node.(*ast.FuncDecl)
		fnType, typeOK := sym.Type.(*FuncType)
		return decl, fnType, declOK && typeOK && decl != nil && fnType != nil && decl.Name == name
	}
	newDecl, newType, ok := lookup("task_group_new")
	if !ok || !a.taskGroupRuntimeSourceDecl(newDecl) || len(newDecl.Params) != 0 || len(newType.Params) != 0 || len(newType.TypeParams) != 0 || len(newType.PermissionParams) != 0 || newType.Variadic || !isBuiltinProtocolOwnerType(newType.Return, "TaskGroup") {
		return false
	}
	waitDecl, waitType, ok := lookup("task_group_wait_all")
	if !ok || !a.taskGroupRuntimeSourceDecl(waitDecl) || len(waitDecl.Params) != 1 || len(waitType.Params) != 1 || len(waitType.TypeParams) != 0 || len(waitType.PermissionParams) != 0 || waitType.Variadic || !isTaskGroupMutableRef(waitType.Params[0]) || !SameType(waitType.Return, a.namedTypes["void"]) {
		return false
	}
	addDecl, addType, ok := lookup("task_group_add")
	if !ok || !a.taskGroupRuntimeSourceDecl(addDecl) || len(addDecl.Params) != 2 || len(addType.Params) != 2 || len(addType.TypeParams) != 1 || addType.TypeParams[0] != "R" || len(addType.PermissionParams) != 0 || addType.Variadic || !isTaskGroupMutableRef(addType.Params[0]) || !isPendingTaskGeneric(addType.Params[1], "R") || !SameType(addType.Return, a.namedTypes["void"]) {
		return false
	}
	submitDecl, submitType, ok := lookup("pool_submit1")
	if !ok || !a.taskGroupRuntimeSourceDecl(submitDecl) || len(submitDecl.Params) != 3 || len(submitType.Params) != 3 || len(submitType.TypeParams) != 2 || submitType.TypeParams[0] != "A" || submitType.TypeParams[1] != "R" || len(submitType.PermissionParams) != 1 || submitType.PermissionParams[0] != "P" || submitType.Variadic || !isThreadPoolMutableRef(submitType.Params[0]) || !isPendingTaskGeneric(submitType.Return, "R") || !isTypeParameterNamed(submitType.Params[2], "A") {
		return false
	}
	callback, ok := submitType.Params[1].(*FuncType)
	if !ok || callback == nil || len(callback.Params) != 1 || !isTypeParameterNamed(callback.Params[0], "A") || !isTypeParameterNamed(callback.Return, "R") {
		return false
	}
	return true
}

// Runtime implementation identity matters here: a source-level stub with the
// same spelling and signature cannot certify that a wait joins submitted work.
func trustedRuntimeSourceFileSet(paths []string) map[string]bool {
	if len(paths) == 0 {
		return nil
	}
	trusted := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		trusted[filepath.Clean(path)] = true
	}
	return trusted
}

func (a *Analyzer) taskGroupRuntimeSourceDecl(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || decl.Position.File == "" {
		return false
	}
	return a.trustedRuntimeSourceFiles[filepath.Clean(decl.Position.File)]
}

func isTaskGroupMutableRef(t Type) bool {
	ref, ok := t.(*RefType)
	return ok && ref != nil && ref.Mutable && isBuiltinProtocolOwnerType(ref.Elem, "TaskGroup")
}

func isThreadPoolMutableRef(t Type) bool {
	ref, ok := t.(*RefType)
	return ok && ref != nil && ref.Mutable && isBuiltinProtocolOwnerType(ref.Elem, "ThreadPool")
}

func isPendingTaskGeneric(t Type, typeParam string) bool {
	instance, ok := t.(*GenericInstanceType)
	if !ok || instance == nil || genericInstanceTypeName(instance) != "Task" || len(instance.Args) != 2 || !SameType(instance.Args[1], &BuiltinType{Name: "Pending"}) {
		return false
	}
	return isTypeParameterNamed(instance.Args[0], typeParam)
}

func isTypeParameterNamed(t Type, name string) bool {
	param, ok := t.(*TypeParamType)
	return ok && param != nil && param.Name == name
}

func genericInstanceTypeName(instance *GenericInstanceType) string {
	if instance == nil {
		return ""
	}
	if instance.Name != "" {
		return instance.Name
	}
	if base, ok := instance.Base.(*StructType); ok && base != nil {
		return base.Name
	}
	return ""
}

func taskGroupAPIName(name string) bool {
	switch name {
	case "task_group_new", "task_group_add", "task_group_wait_all", "pool_submit1":
		return true
	default:
		return false
	}
}

func (a *Analyzer) taskGroupAPIShadowedInCurrentScope() bool {
	if a == nil || a.currentScope == nil {
		return true
	}
	for _, name := range []string{"task_group_new", "task_group_add", "task_group_wait_all", "pool_submit1"} {
		if _, shadowed := a.currentScope.Symbols[name]; shadowed {
			return true
		}
	}
	return false
}

func taskGroupConstructorDecl(stmt *ast.VarDeclStmt) bool {
	if stmt == nil {
		return false
	}
	if !taskGroupTypeExpr(stmt.Type) || stmt.Name == "" {
		return false
	}
	call, ok := stmt.Value.(*ast.CallExpr)
	return ok && call != nil && callIdentName(call) == "task_group_new" && len(call.Args) == 0
}

func taskGroupTypeExpr(expr ast.TypeExpr) bool {
	switch t := expr.(type) {
	case *ast.NamedType:
		return t != nil && t.Name == "TaskGroup"
	case *ast.MutableType:
		return t != nil && taskGroupTypeExpr(t.Elem)
	default:
		return false
	}
}

func taskGroupWaitMatches(stmt ast.Stmt, groupName string) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok || exprStmt == nil {
		return false
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || call == nil || callIdentName(call) != "task_group_wait_all" || len(call.Args) != 1 {
		return false
	}
	arg := call.Args[0]
	for {
		switch n := arg.(type) {
		case *ast.ParenExpr:
			arg = n.Inner
		case *ast.CastExpr:
			arg = n.Operand
		default:
			goto unwrapped
		}
	}
unwrapped:
	address, ok := arg.(*ast.AddrOfExpr)
	if !ok || address == nil {
		return false
	}
	ident, ok := address.Operand.(*ast.Ident)
	return ok && ident != nil && ident.Name == groupName
}

type taskGroupNodeFacts struct {
	groupRef        bool
	groupAPI        bool
	untrustedCall   bool
	safeSubmitCount int
	earlyExit       bool
	groupShadow     bool
	apiShadow       bool
}

type taskGroupSubmitCandidate struct {
	call         *ast.CallExpr
	alreadyAdded bool
}

func taskGroupFacts(node any, groupName string) taskGroupNodeFacts {
	var facts taskGroupNodeFacts
	if node == nil {
		return facts
	}
	fillMayAdoptWalk(reflect.ValueOf(node), func(value any) {
		switch n := value.(type) {
		case *ast.Ident:
			facts.groupRef = facts.groupRef || n != nil && n.Name == groupName
		case *ast.CallExpr:
			if n == nil {
				return
			}
			name := callIdentName(n)
			facts.groupAPI = facts.groupAPI || taskGroupAPIName(name)
			facts.untrustedCall = true
			if n.SafeConcurrencySugar && name == "pool_submit1" {
				facts.safeSubmitCount++
			}
		case *ast.GetExpr, *ast.TryExpr, *ast.UnwrapElseExpr, *ast.RaiseExpr:
			// These forms can propagate absence or failure out of the function before
			// the matching wait. Keep the window on the existing conservative path.
			facts.earlyExit = true
		case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
			facts.earlyExit = true
		case *ast.VarDeclStmt:
			if n != nil {
				facts.groupShadow = facts.groupShadow || n.Name == groupName
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.Name) || n.Name == "pool_submit1"
			}
		case *ast.ForStmt:
			if n != nil {
				facts.groupShadow = facts.groupShadow || n.Name == groupName
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.Name) || n.Name == "pool_submit1"
			}
		case *ast.PoolStmt:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupAPIName(n.Name)
		case *ast.LockStmt:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupAPIName(n.GuardName)
		case *ast.RegionStmt:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupAPIName(n.Name)
		case *ast.TupleBindStmt:
			if n != nil {
				for _, name := range n.Names {
					facts.apiShadow = facts.apiShadow || taskGroupAPIName(name.Name)
				}
			}
		case *ast.IterForStmt:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupMovePatternShadowsAPI(n.Pattern)
		case *ast.MoveBindStmt:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupMovePatternShadowsAPI(n.Pattern)
		case *ast.LetDestructureStmt:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupMovePatternShadowsAPI(n.Pattern)
		case *ast.MatchStmt:
			if n != nil {
				for _, arm := range n.Arms {
					facts.apiShadow = facts.apiShadow || taskGroupMatchPatternShadowsAPI(arm.Pattern)
				}
			}
		case *ast.MatchBindPattern:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupAPIName(n.Name)
		case *ast.MatchRestPattern:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupAPIName(n.Name)
		case *ast.MatchStructPattern:
			if n != nil {
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.As)
				for _, arg := range n.Args {
					facts.apiShadow = facts.apiShadow || taskGroupAPIName(arg.Name)
				}
			}
		case *ast.MatchVariantPattern:
			if n != nil {
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.As)
				for _, arg := range n.Args {
					facts.apiShadow = facts.apiShadow || taskGroupAPIName(arg.Name)
				}
			}
		case *ast.MoveBindNamePattern:
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupAPIName(n.Name)
		case *ast.LambdaExpr:
			// A closure can retain a reference or hide a submission from this flat
			// window proof. Keep such windows on the conservative path.
			facts.groupAPI = true
		}
	})
	return facts
}

func taskGroupWindowStmtSafe(stmt ast.Stmt, groupName string, submits *[]taskGroupSubmitCandidate) bool {
	switch n := stmt.(type) {
	case *ast.ForStmt:
		if n == nil || n.Name == groupName || taskGroupAPIName(n.Name) || n.Name == "pool_submit1" || n.PreReserve != nil || len(n.PreReserves) != 0 {
			return false
		}
		for _, expr := range []ast.Expr{n.Start, n.End, n.Step} {
			facts := taskGroupFacts(expr, groupName)
			if facts.groupRef || facts.groupAPI || facts.untrustedCall || facts.safeSubmitCount != 0 || facts.earlyExit {
				return false
			}
		}
		for _, child := range n.Body {
			if !taskGroupWindowStmtSafe(child, groupName, submits) {
				return false
			}
		}
		return true
	case *ast.CanStmt:
		if n == nil || n.As != "" || n.HandlerName != "" || len(n.HandlerArgs) != 0 {
			return false
		}
		facts := taskGroupFacts(n.Permissions, groupName)
		if facts.groupRef || facts.groupAPI || facts.untrustedCall || facts.safeSubmitCount != 0 || facts.earlyExit {
			return false
		}
		for _, child := range n.Body {
			if !taskGroupWindowStmtSafe(child, groupName, submits) {
				return false
			}
		}
		return true
	case *ast.ExprStmt:
		if n == nil {
			return false
		}
		call, ok := n.Expr.(*ast.CallExpr)
		if ok && call != nil && callIdentName(call) == "task_group_add" && len(call.Args) == 2 && taskGroupGroupRefMatches(call.Args[0], groupName) {
			submit, isSubmit := call.Args[1].(*ast.CallExpr)
			if !isSubmit || submit == nil || !submit.SafeConcurrencySugar || callIdentName(submit) != "pool_submit1" || len(submit.Args) != 3 {
				return false
			}
			facts := taskGroupFacts(submit.Args, groupName)
			if facts.groupRef || facts.groupAPI || facts.untrustedCall || facts.safeSubmitCount != 0 || facts.earlyExit || facts.groupShadow || facts.apiShadow {
				return false
			}
			*submits = append(*submits, taskGroupSubmitCandidate{call: submit, alreadyAdded: true})
			return true
		}
		facts := taskGroupFacts(stmt, groupName)
		return !facts.groupRef && !facts.groupAPI && !facts.untrustedCall && facts.safeSubmitCount == 0 && !facts.earlyExit && !facts.groupShadow && !facts.apiShadow
	default:
		facts := taskGroupFacts(stmt, groupName)
		return !facts.groupRef && !facts.groupAPI && !facts.untrustedCall && facts.safeSubmitCount == 0 && !facts.earlyExit && !facts.groupShadow && !facts.apiShadow
	}
}

func taskGroupGroupRefMatches(expr ast.Expr, groupName string) bool {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.AddrOfExpr:
			ident, ok := n.Operand.(*ast.Ident)
			return ok && ident != nil && ident.Name == groupName
		default:
			return false
		}
	}
	return false
}

func taskGroupRangeMentionsName(stmts []ast.Stmt, name string) bool {
	for _, stmt := range stmts {
		facts := taskGroupFacts(stmt, name)
		if facts.groupRef || facts.groupShadow {
			return true
		}
	}
	return false
}

func taskGroupRangeShadowsAPI(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		facts := taskGroupFacts(stmt, "")
		if facts.apiShadow {
			return true
		}
	}
	return false
}

func taskGroupFunctionShadowsAPI(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		facts := taskGroupFacts(stmt, "")
		if facts.apiShadow {
			return true
		}
	}
	return false
}

func taskGroupMovePatternShadowsAPI(pattern ast.MoveBindPattern) bool {
	switch n := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return n != nil && taskGroupAPIName(n.Name)
	case *ast.MoveBindStructPattern:
		if n == nil {
			return false
		}
		for _, arg := range n.Args {
			if taskGroupAPIName(arg.Name) {
				return true
			}
		}
	case *ast.MoveBindTuplePattern:
		if n == nil {
			return false
		}
		for _, arg := range n.Args {
			if taskGroupAPIName(arg.Name) {
				return true
			}
		}
	case *ast.MoveBindVariantPattern:
		if n == nil {
			return false
		}
		for _, arg := range n.Args {
			if taskGroupAPIName(arg.Name) || taskGroupMatchPatternShadowsAPI(arg.Pattern) {
				return true
			}
		}
	}
	return false
}

func taskGroupMatchPatternShadowsAPI(pattern ast.MatchPattern) bool {
	switch n := pattern.(type) {
	case *ast.MatchBindPattern:
		return n != nil && (taskGroupAPIName(n.Name) || taskGroupAPIName(n.Binder))
	case *ast.MatchRestPattern:
		return n != nil && taskGroupAPIName(n.Name)
	case *ast.MatchTuplePattern:
		if n == nil {
			return false
		}
		for _, elem := range n.Elems {
			if taskGroupMatchPatternShadowsAPI(elem) {
				return true
			}
		}
	case *ast.MatchListPattern:
		if n == nil {
			return false
		}
		for _, elem := range n.Elems {
			if taskGroupMatchPatternShadowsAPI(elem) {
				return true
			}
		}
	case *ast.MatchOrPattern:
		if n == nil {
			return false
		}
		for _, option := range n.Options {
			if taskGroupMatchPatternShadowsAPI(option) {
				return true
			}
		}
	case *ast.MatchStructPattern:
		if n == nil {
			return false
		}
		if taskGroupAPIName(n.As) {
			return true
		}
		for _, arg := range n.Args {
			if taskGroupAPIName(arg.Name) || taskGroupMatchPatternShadowsAPI(arg.Pattern) {
				return true
			}
		}
	case *ast.MatchVariantPattern:
		if n == nil {
			return false
		}
		if taskGroupAPIName(n.As) {
			return true
		}
		for _, arg := range n.Args {
			if taskGroupAPIName(arg.Name) || taskGroupMatchPatternShadowsAPI(arg.Pattern) {
				return true
			}
		}
	}
	return false
}

func walkTaskGroupStmtLists(body []ast.Stmt, visit func([]ast.Stmt)) {
	if len(body) == 0 || visit == nil {
		return
	}
	visit(body)
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.IfStmt:
			walkTaskGroupStmtLists(n.Then, visit)
			for _, clause := range n.Elifs {
				walkTaskGroupStmtLists(clause.Body, visit)
			}
			walkTaskGroupStmtLists(n.Else, visit)
		case *ast.WhileStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.ForStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.IterForStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.ParallelForStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.CanStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.InStoreStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.ScopeStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.PoolStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.LockStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.RegionStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.DeferStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.StaticIfStmt:
			walkTaskGroupStmtLists(n.Then, visit)
			for _, clause := range n.Elifs {
				walkTaskGroupStmtLists(clause.Body, visit)
			}
			walkTaskGroupStmtLists(n.Else, visit)
		case *ast.StaticBlockStmt:
			walkTaskGroupStmtLists(n.Body, visit)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				walkTaskGroupStmtLists(arm.Body, visit)
			}
		}
	}
}
