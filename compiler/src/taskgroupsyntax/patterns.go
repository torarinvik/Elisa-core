package taskgroupsyntax

import "elisacore/src/ast"

func MovePatternShadowsAPI(pattern ast.MoveBindPattern) bool {
	switch n := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return n != nil && IsAPIName(n.Name)
	case *ast.MoveBindStructPattern:
		if n == nil {
			return false
		}
		for _, arg := range n.Args {
			if IsAPIName(arg.Name) {
				return true
			}
		}
	case *ast.MoveBindTuplePattern:
		if n == nil {
			return false
		}
		for _, arg := range n.Args {
			if IsAPIName(arg.Name) {
				return true
			}
		}
	case *ast.MoveBindVariantPattern:
		if n == nil {
			return false
		}
		for _, arg := range n.Args {
			if MatchPatternShadowsAPI(arg.Pattern) {
				return true
			}
		}
	}
	return false
}

func MatchPatternShadowsAPI(pattern ast.MatchPattern) bool {
	switch n := pattern.(type) {
	case *ast.MatchBindPattern:
		return n != nil && (IsAPIName(n.Name) || IsAPIName(n.Binder))
	case *ast.MatchRestPattern:
		return n != nil && IsAPIName(n.Name)
	case *ast.MatchTuplePattern:
		if n == nil {
			return false
		}
		for _, elem := range n.Elems {
			if MatchPatternShadowsAPI(elem) {
				return true
			}
		}
	case *ast.MatchListPattern:
		if n == nil {
			return false
		}
		for _, elem := range n.Elems {
			if MatchPatternShadowsAPI(elem) {
				return true
			}
		}
	case *ast.MatchOrPattern:
		if n == nil {
			return false
		}
		for _, option := range n.Options {
			if MatchPatternShadowsAPI(option) {
				return true
			}
		}
	case *ast.MatchStructPattern:
		if n == nil {
			return false
		}
		if IsAPIName(n.As) {
			return true
		}
		for _, arg := range n.Args {
			if MatchPatternShadowsAPI(arg.Pattern) {
				return true
			}
		}
	case *ast.MatchVariantPattern:
		if n == nil {
			return false
		}
		if IsAPIName(n.As) {
			return true
		}
		for _, arg := range n.Args {
			if MatchPatternShadowsAPI(arg.Pattern) {
				return true
			}
		}
	}
	return false
}

func WalkStmtLists(body []ast.Stmt, visit func([]ast.Stmt)) {
	if len(body) == 0 || visit == nil {
		return
	}
	visit(body)
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.IfStmt:
			WalkStmtLists(n.Then, visit)
			for _, clause := range n.Elifs {
				WalkStmtLists(clause.Body, visit)
			}
			WalkStmtLists(n.Else, visit)
		case *ast.WhileStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.ForStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.IterForStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.ParallelForStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.CanStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.InStoreStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.ScopeStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.PoolStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.LockStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.RegionStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.DeferStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.StaticIfStmt:
			WalkStmtLists(n.Then, visit)
			for _, clause := range n.Elifs {
				WalkStmtLists(clause.Body, visit)
			}
			WalkStmtLists(n.Else, visit)
		case *ast.StaticBlockStmt:
			WalkStmtLists(n.Body, visit)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				WalkStmtLists(arm.Body, visit)
			}
		}
	}
}

func IsAPIName(name string) bool {
	switch name {
	case "task_group_new", "task_group_add", "task_group_wait_all", "pool_submit1":
		return true
	default:
		return false
	}
}
