package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

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
				facts.groupShadow = facts.groupShadow || groupName != "" && n.Name == groupName
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.Name) || n.Name == "pool_submit1"
			}
		case *ast.ForStmt:
			if n != nil {
				facts.groupShadow = facts.groupShadow || groupName != "" && n.Name == groupName
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
					facts.groupShadow = facts.groupShadow || groupName != "" && name.Name == groupName
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
			if n != nil {
				facts.groupShadow = facts.groupShadow || groupName != "" && (n.Name == groupName || n.Binder == groupName)
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.Name) || taskGroupAPIName(n.Binder)
			}
		case *ast.MatchRestPattern:
			if n != nil {
				facts.groupShadow = facts.groupShadow || groupName != "" && n.Name == groupName
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.Name)
			}
		case *ast.MatchStructPattern:
			if n != nil {
				facts.groupShadow = facts.groupShadow || groupName != "" && n.As == groupName
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.As)
				for _, arg := range n.Args {
					facts.apiShadow = facts.apiShadow || taskGroupMatchPatternShadowsAPI(arg.Pattern)
				}
			}
		case *ast.MatchVariantPattern:
			if n != nil {
				facts.groupShadow = facts.groupShadow || groupName != "" && n.As == groupName
				facts.apiShadow = facts.apiShadow || taskGroupAPIName(n.As)
				for _, arg := range n.Args {
					facts.apiShadow = facts.apiShadow || taskGroupMatchPatternShadowsAPI(arg.Pattern)
				}
			}
		case *ast.MoveBindNamePattern:
			facts.groupShadow = facts.groupShadow || groupName != "" && n != nil && n.Name == groupName
			facts.apiShadow = facts.apiShadow || n != nil && taskGroupAPIName(n.Name)
		case *ast.MoveBindStructPattern:
			if n != nil {
				for _, arg := range n.Args {
					facts.groupShadow = facts.groupShadow || groupName != "" && arg.Name == groupName
				}
			}
		case *ast.MoveBindTuplePattern:
			if n != nil {
				for _, arg := range n.Args {
					facts.groupShadow = facts.groupShadow || groupName != "" && arg.Name == groupName
				}
			}
		case *ast.MoveBindArg:
			facts.groupShadow = facts.groupShadow || groupName != "" && n != nil && n.Name == groupName
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
			if taskGroupMatchPatternShadowsAPI(arg.Pattern) {
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
			if taskGroupMatchPatternShadowsAPI(arg.Pattern) {
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
			if taskGroupMatchPatternShadowsAPI(arg.Pattern) {
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
