package semantic

import (
	"elisacore/src/ast"
	"path/filepath"
)

// recordVerifiedTaskGroupSubmits proves the deliberately small manual TaskGroup
// window understood by Stage0: a local TaskGroup initialized by the canonical
// constructor, one matching wait later in the same statement list, and matching
// task_group_add(group, safe_submit(...)) calls nested only in ordinary counted
// loops and `can` blocks between them.
// Anything more complex keeps the existing conservative lifetime diagnostic.
func (a *Analyzer) recordVerifiedTaskGroupSubmits(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	type verifiedSubmit struct {
		call *ast.CallExpr
		info TaskGroupSubmitInfo
	}
	var verified []verifiedSubmit

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
		// The constructor is the first API use in this list. Check only the prefix
		// that can shadow that declaration; the candidate window is checked while
		// it is already traversed below.
		if taskGroupRangeShadowsAPI(body[:declRow+1]) {
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
		for _, submit := range submits {
			verified = append(verified, verifiedSubmit{
				call: submit.call,
				info: TaskGroupSubmitInfo{GroupName: groupName, AlreadyAdded: submit.alreadyAdded},
			})
		}
	})
	// The vast majority of functions have no candidate window. Defer the trusted
	// runtime's four signature/source checks until syntax has found at least one
	// otherwise-safe submit, avoiding that work for ordinary functions.
	if len(verified) == 0 || taskGroupFunctionShadowsAPI(fn) || !a.taskGroupRuntimeAPIsTrusted() {
		return
	}
	if a.taskGroupSubmits == nil {
		a.taskGroupSubmits = map[*ast.CallExpr]TaskGroupSubmitInfo{}
	}
	added := false
	for _, proof := range verified {
		if _, exists := a.taskGroupSubmits[proof.call]; !exists {
			added = true
		}
		a.taskGroupSubmits[proof.call] = proof.info
	}
	if added && !a.retainedProofsScoped {
		a.invalidateStoreFlowSummaries()
	}
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
			switch decl := scoped.Decl.(type) {
			case *ast.FuncDecl:
				a.recordVerifiedTaskGroupSubmits(decl)
			case *ast.ImplDecl:
				for _, member := range decl.Members {
					if fn, ok := member.(*ast.FuncDecl); ok {
						a.recordVerifiedTaskGroupSubmits(fn)
					}
				}
			}
		})
	}
}

func (a *Analyzer) taskGroupRuntimeAPIsTrusted() bool {
	if a == nil || a.globalScope == nil {
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

func taskGroupFunctionShadowsAPI(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	for _, param := range fn.Params {
		if taskGroupAPIName(param.Name) {
			return true
		}
	}
	for _, name := range fn.RegionParams {
		if taskGroupAPIName(name) {
			return true
		}
	}
	// Most functions have no TaskGroup candidate and avoid this reflective walk.
	// For candidate functions, scan the whole body once so binders introduced by
	// enclosing match/iteration scopes cannot shadow the runtime APIs outside the
	// candidate's immediate statement list.
	for _, stmt := range fn.Body {
		if taskGroupFacts(stmt, "").apiShadow {
			return true
		}
	}
	return false
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
