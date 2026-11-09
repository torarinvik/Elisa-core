package semantic

import (
	"elisacore/src/ast"
	"strings"
)

type segmentOwnerState int

const (
	segmentOwnerUnknown segmentOwnerState = iota
	segmentOwnerHost
	segmentOwnerGuest
)

func segmentOwnerName(owner segmentOwnerState) string {
	switch owner {
	case segmentOwnerHost:
		return "Host"
	case segmentOwnerGuest:
		return "Guest"
	default:
		return "Unknown"
	}
}

func normalizeSegmentTransitionAnnotationArg(value string) (FuncSegmentTransition, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "host", "segment.host":
		return FuncSegmentTransitionHost, true
	case "guest", "segment.guest":
		return FuncSegmentTransitionGuest, true
	default:
		return FuncSegmentTransitionNone, false
	}
}

func segmentOwnerFromFuncTransition(transition FuncSegmentTransition) segmentOwnerState {
	switch transition {
	case FuncSegmentTransitionHost:
		return segmentOwnerHost
	case FuncSegmentTransitionGuest:
		return segmentOwnerGuest
	default:
		return segmentOwnerUnknown
	}
}

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

func permissionRefsContainMember(refs []ast.PermissionRef, family string, member string) bool {
	for _, ref := range refs {
		if ref.Name == family && (member == "" || ref.Member == member || ref.Member == "") {
			return true
		}
	}
	return false
}

func permissionRefsTransitionOwner(refs []ast.PermissionRef) segmentOwnerState {
	if !permissionRefsContainMember(refs, "Unsafe", "SegmentMutation") {
		return segmentOwnerUnknown
	}
	if permissionRefsContainMember(refs, "Segment", "Guest") {
		return segmentOwnerGuest
	}
	if permissionRefsContainMember(refs, "Segment", "Host") {
		return segmentOwnerHost
	}
	return segmentOwnerUnknown
}

func permissionRefsRequiredOwner(refs []ast.PermissionRef) segmentOwnerState {
	if permissionRefsTransitionOwner(refs) != segmentOwnerUnknown {
		return segmentOwnerUnknown
	}
	if permissionRefsContainMember(refs, "Segment", "Guest") {
		return segmentOwnerGuest
	}
	if permissionRefsContainMember(refs, "Segment", "Host") {
		return segmentOwnerHost
	}
	return segmentOwnerUnknown
}

func funcTypeHasSegmentDependency(fnType *FuncType) bool {
	if fnType == nil {
		return false
	}
	return fnType.SegmentTransition != FuncSegmentTransitionNone || permissionRefsContainSegmentDependency(functionPermissionRefs(fnType))
}

func funcTypeSegmentTransitionOwner(fnType *FuncType) segmentOwnerState {
	if fnType == nil {
		return segmentOwnerUnknown
	}
	if owner := segmentOwnerFromFuncTransition(fnType.SegmentTransition); owner != segmentOwnerUnknown {
		return owner
	}
	return permissionRefsTransitionOwner(functionPermissionRefs(fnType))
}

func funcTypeRequiredSegmentOwner(fnType *FuncType) segmentOwnerState {
	if fnType == nil {
		return segmentOwnerUnknown
	}
	if funcTypeSegmentTransitionOwner(fnType) != segmentOwnerUnknown {
		return segmentOwnerUnknown
	}
	return permissionRefsRequiredOwner(functionPermissionRefs(fnType))
}

func (a *Analyzer) validateSegmentFlow(fn *ast.FuncDecl) {
	if fn == nil || funcHasAnnotation(fn, "segment_agnostic") {
		return
	}
	owner := segmentOwnerHost
	if funcHasAnnotation(fn, "async_entry") && !funcHasAnnotation(fn, "segment_establishing") {
		owner = segmentOwnerUnknown
	}
	a.validateSegmentFlowStmts(fn.Body, &owner)
}

func mergeSegmentOwner(aOwner segmentOwnerState, bOwner segmentOwnerState) segmentOwnerState {
	if aOwner == bOwner {
		return aOwner
	}
	return segmentOwnerUnknown
}

func (a *Analyzer) validateSegmentFlowStmts(stmts []ast.Stmt, owner *segmentOwnerState) {
	for _, stmt := range stmts {
		a.validateSegmentFlowStmt(stmt, owner)
	}
}

func (a *Analyzer) validateSegmentFlowStmt(stmt ast.Stmt, owner *segmentOwnerState) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.LetDestructureStmt:
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.TupleBindStmt:
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.MoveBindStmt:
		a.validateSegmentFlowExpr(n.Value, owner)
		a.validateSegmentFlowExpr(n.Store, owner)
	case *ast.DeferStmt:
		snapshot := *owner
		a.validateSegmentFlowStmts(n.Body, &snapshot)
	case *ast.AssignStmt:
		a.validateSegmentFlowExpr(n.Target, owner)
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.AugAssignStmt:
		a.validateSegmentFlowExpr(n.Target, owner)
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.AsRefAssignStmt:
		a.validateSegmentFlowExpr(n.Target, owner)
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.ReturnStmt:
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.IfStmt:
		a.validateSegmentFlowExpr(n.Cond, owner)
		base := *owner
		thenOwner := base
		a.validateSegmentFlowStmts(n.Then, &thenOwner)
		merged := thenOwner
		sawElse := len(n.Else) != 0
		for _, elif := range n.Elifs {
			elifOwner := base
			a.validateSegmentFlowExpr(elif.Cond, &elifOwner)
			a.validateSegmentFlowStmts(elif.Body, &elifOwner)
			merged = mergeSegmentOwner(merged, elifOwner)
		}
		if len(n.Else) != 0 {
			elseOwner := base
			a.validateSegmentFlowStmts(n.Else, &elseOwner)
			merged = mergeSegmentOwner(merged, elseOwner)
		}
		if !sawElse {
			merged = mergeSegmentOwner(merged, base)
		}
		*owner = merged
	case *ast.MatchStmt:
		a.validateSegmentFlowExpr(n.Value, owner)
		a.validateSegmentFlowExpr(n.Store, owner)
		base := *owner
		merged := segmentOwnerUnknown
		for i, arm := range n.Arms {
			armOwner := base
			a.validateSegmentFlowStmts(arm.Body, &armOwner)
			if i == 0 {
				merged = armOwner
			} else {
				merged = mergeSegmentOwner(merged, armOwner)
			}
		}
		*owner = merged
	case *ast.InStoreStmt:
		a.validateSegmentFlowExpr(n.Store, owner)
		a.validateSegmentFlowStmts(n.Body, owner)
	case *ast.CanStmt:
		a.validateSegmentFlowStmts(n.Body, owner)
	case *ast.RegionStmt:
		// Explicit `region` blocks and the auto-region the compiler wraps around an allocating
		// body run straight through: without this arm the whole body went unchecked.
		a.validateSegmentFlowStmts(n.Body, owner)
	case *ast.ScopeStmt:
		a.validateSegmentFlowExpr(n.Guard, owner)
		a.validateSegmentFlowStmts(n.Body, owner)
	case *ast.PoolStmt:
		a.validateSegmentFlowExpr(n.Workers, owner)
		a.validateSegmentFlowStmts(n.Body, owner)
	case *ast.LockStmt:
		a.validateSegmentFlowExpr(n.Mutex, owner)
		a.validateSegmentFlowStmts(n.Body, owner)
	case *ast.WhileStmt:
		a.validateSegmentFlowExpr(n.Cond, owner)
		bodyOwner := *owner
		a.validateSegmentFlowStmts(n.Body, &bodyOwner)
		*owner = mergeSegmentOwner(*owner, bodyOwner)
	case *ast.ForStmt:
		a.validateSegmentFlowExpr(n.Start, owner)
		a.validateSegmentFlowExpr(n.End, owner)
		a.validateSegmentFlowExpr(n.Step, owner)
		bodyOwner := *owner
		a.validateSegmentFlowStmts(n.Body, &bodyOwner)
		*owner = mergeSegmentOwner(*owner, bodyOwner)
	case *ast.IterForStmt:
		a.validateSegmentFlowExpr(n.Source, owner)
		a.validateSegmentFlowExpr(n.WhereFilter, owner)
		a.validateSegmentFlowExpr(n.Filter, owner)
		bodyOwner := *owner
		a.validateSegmentFlowStmts(n.Body, &bodyOwner)
		*owner = mergeSegmentOwner(*owner, bodyOwner)
	case *ast.ParallelForStmt:
		a.validateSegmentFlowExpr(n.Source, owner)
		bodyOwner := *owner
		a.validateSegmentFlowStmts(n.Body, &bodyOwner)
		*owner = mergeSegmentOwner(*owner, bodyOwner)
	case *ast.PanicStmt:
		a.validateSegmentFlowExpr(n.Message, owner)
	case *ast.ExprStmt:
		a.validateSegmentFlowExpr(n.Expr, owner)
	case *ast.StaticIfStmt:
		for _, active := range a.activeStmtBranch(n) {
			a.validateSegmentFlowStmt(active, owner)
		}
	case *ast.StaticErrorStmt:
		a.validateSegmentFlowExpr(n.Message, owner)
	case *ast.StaticAssertStmt:
		a.validateSegmentFlowExpr(n.Cond, owner)
		a.validateSegmentFlowExpr(n.Message, owner)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			a.validateSegmentFlowExpr(item.Cond, owner)
			a.validateSegmentFlowExpr(item.Message, owner)
		}
	case *ast.StaticBlockStmt:
		a.validateSegmentFlowStmts(n.Body, owner)
	case *ast.DiscardStmt:
		a.validateSegmentFlowExpr(n.Value, owner)
	}
}

func (a *Analyzer) validateSegmentFlowExpr(expr ast.Expr, owner *segmentOwnerState) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		a.validateSegmentFlowExpr(n.Left, owner)
		a.validateSegmentFlowExpr(n.Right, owner)
	case *ast.UnaryExpr:
		a.validateSegmentFlowExpr(n.Operand, owner)
	case *ast.CallExpr:
		a.validateSegmentFlowExpr(n.Func, owner)
		a.validateSegmentFlowExpr(n.SafeReceiver, owner)
		for _, arg := range n.Args {
			a.validateSegmentFlowExpr(arg, owner)
		}
		if fnType, ok := a.exprType(n.Func).(*FuncType); ok {
			required := funcTypeRequiredSegmentOwner(fnType)
			if required != segmentOwnerUnknown {
				if *owner == segmentOwnerUnknown {
					a.errorf(n.Pos(), "segment owner unknown: call to %q requires Segment.%s; establish Segment.%s before crossing this boundary", fnType.Name, segmentOwnerName(required), segmentOwnerName(required))
				} else if *owner != required {
					a.errorf(n.Pos(), "segment owner mismatch: call to %q requires Segment.%s but current ambient segment is Segment.%s", fnType.Name, segmentOwnerName(required), segmentOwnerName(*owner))
				}
			}
			if transition := funcTypeSegmentTransitionOwner(fnType); transition != segmentOwnerUnknown {
				*owner = transition
			}
		}
	case *ast.FieldExpr:
		a.validateSegmentFlowExpr(n.Object, owner)
	case *ast.IndexExpr:
		a.validateSegmentFlowExpr(n.Object, owner)
		a.validateSegmentFlowExpr(n.Index, owner)
		a.validateSegmentFlowExpr(n.Fallback, owner)
	case *ast.SliceExpr:
		a.validateSegmentFlowExpr(n.Object, owner)
		a.validateSegmentFlowExpr(n.Start, owner)
		a.validateSegmentFlowExpr(n.End, owner)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.validateSegmentFlowExpr(elem, owner)
		}
		a.validateSegmentFlowExpr(n.Owner, owner)
	case *ast.CastExpr:
		if call, ok := a.postfixShorthandCalls[n]; ok && call != nil {
			a.validateSegmentFlowExpr(call, owner)
			break
		}
		a.validateSegmentFlowExpr(n.Operand, owner)
	case *ast.TernaryExpr:
		a.validateSegmentFlowExpr(n.Value, owner)
		a.validateSegmentFlowExpr(n.Cond, owner)
		a.validateSegmentFlowExpr(n.Alt, owner)
	case *ast.AddrOfExpr:
		a.validateSegmentFlowExpr(n.Operand, owner)
	case *ast.MoveExpr:
		a.validateSegmentFlowExpr(n.Operand, owner)
	case *ast.SpecializeExpr:
		a.validateSegmentFlowExpr(n.Operand, owner)
	case *ast.StructLitExpr:
		if call, ok := a.loweredInitCalls[n]; ok && call != nil {
			a.validateSegmentFlowExpr(call, owner)
			break
		}
		for _, arg := range n.Args {
			a.validateSegmentFlowExpr(arg, owner)
		}
	case *ast.RecordUpdateExpr:
		a.validateSegmentFlowExpr(n.Base, owner)
		for _, arg := range n.Args {
			a.validateSegmentFlowExpr(arg, owner)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			a.validateSegmentFlowExpr(elem, owner)
		}
	case *ast.ParenExpr:
		a.validateSegmentFlowExpr(n.Inner, owner)
	case *ast.RaiseExpr:
		a.validateSegmentFlowExpr(n.Error, owner)
	case *ast.TryExpr:
		a.validateSegmentFlowExpr(n.Value, owner)
		a.validateSegmentFlowExpr(n.Fallback, owner)
	case *ast.UnwrapElseExpr:
		a.validateSegmentFlowExpr(n.Value, owner)
		a.validateSegmentFlowExpr(n.Fallback, owner)
	case *ast.GetExpr:
		a.validateSegmentFlowExpr(n.Value, owner)
		a.validateSegmentFlowExpr(n.Fallback, owner)
	case *ast.OptionalBindExpr:
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.AllocExpr:
		a.validateSegmentFlowExpr(n.Owner, owner)
		a.validateSegmentFlowExpr(n.Value, owner)
	case *ast.CanExpr:
		a.validateSegmentFlowExpr(n.Expr, owner)
	case *ast.MatchExpr:
		a.validateSegmentFlowExpr(n.Value, owner)
		a.validateSegmentFlowExpr(n.Store, owner)
		base := *owner
		merged := segmentOwnerUnknown
		for i, arm := range n.Arms {
			armOwner := base
			a.validateSegmentFlowStmts(arm.Body, &armOwner)
			if i == 0 {
				merged = armOwner
			} else {
				merged = mergeSegmentOwner(merged, armOwner)
			}
		}
		*owner = merged
	case *ast.FoldExpr:
		a.validateSegmentFlowExpr(n.Value, owner)
		for _, arm := range n.Arms {
			a.validateSegmentFlowExpr(arm.Guard, owner)
			a.validateSegmentFlowStmts(arm.Body, owner)
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			a.validateSegmentFlowExpr(target, owner)
		}
	case *ast.IsAliasExpr:
		a.validateSegmentFlowExpr(n.Target, owner)
	}
}
