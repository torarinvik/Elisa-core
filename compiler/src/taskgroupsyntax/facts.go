package taskgroupsyntax

import "elisacore/src/ast"

type Facts struct {
	GroupRef, GroupAPI, UntrustedCall bool
	SafeSubmitCount                   int
	EarlyExit, GroupShadow, ApiShadow bool
}

func CollectFacts(node any, groupName string, walk func(any, func(any))) Facts {
	var facts Facts
	if node == nil {
		return facts
	}
	walk(node, func(value any) {
		switch n := value.(type) {
		case *ast.Ident:
			facts.GroupRef = facts.GroupRef || n != nil && n.Name == groupName
		case *ast.CallExpr:
			if n == nil {
				return
			}
			name := callName(n)
			facts.GroupAPI = facts.GroupAPI || IsAPIName(name)
			facts.UntrustedCall = true
			if n.SafeConcurrencySugar && name == "pool_submit1" {
				facts.SafeSubmitCount++
			}
		case *ast.GetExpr, *ast.TryExpr, *ast.UnwrapElseExpr, *ast.RaiseExpr:
			// These forms can propagate absence or failure out of the function before
			// the matching wait. Keep the window on the existing conservative path.
			facts.EarlyExit = true
		case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
			facts.EarlyExit = true
		case *ast.VarDeclStmt:
			if n != nil {
				facts.GroupShadow = facts.GroupShadow || groupName != "" && n.Name == groupName
				facts.ApiShadow = facts.ApiShadow || IsAPIName(n.Name) || n.Name == "pool_submit1"
			}
		case *ast.ForStmt:
			if n != nil {
				facts.GroupShadow = facts.GroupShadow || groupName != "" && n.Name == groupName
				facts.ApiShadow = facts.ApiShadow || IsAPIName(n.Name) || n.Name == "pool_submit1"
			}
		case *ast.PoolStmt:
			facts.ApiShadow = facts.ApiShadow || n != nil && IsAPIName(n.Name)
		case *ast.LockStmt:
			facts.ApiShadow = facts.ApiShadow || n != nil && IsAPIName(n.GuardName)
		case *ast.RegionStmt:
			facts.ApiShadow = facts.ApiShadow || n != nil && IsAPIName(n.Name)
		case *ast.TupleBindStmt:
			if n != nil {
				for _, name := range n.Names {
					facts.GroupShadow = facts.GroupShadow || groupName != "" && name.Name == groupName
					facts.ApiShadow = facts.ApiShadow || IsAPIName(name.Name)
				}
			}
		case *ast.IterForStmt:
			facts.ApiShadow = facts.ApiShadow || n != nil && MovePatternShadowsAPI(n.Pattern)
		case *ast.MoveBindStmt:
			facts.ApiShadow = facts.ApiShadow || n != nil && MovePatternShadowsAPI(n.Pattern)
		case *ast.LetDestructureStmt:
			facts.ApiShadow = facts.ApiShadow || n != nil && MovePatternShadowsAPI(n.Pattern)
		case *ast.MatchStmt:
			if n != nil {
				for _, arm := range n.Arms {
					facts.ApiShadow = facts.ApiShadow || MatchPatternShadowsAPI(arm.Pattern)
				}
			}
		case *ast.MatchBindPattern:
			if n != nil {
				facts.GroupShadow = facts.GroupShadow || groupName != "" && (n.Name == groupName || n.Binder == groupName)
				facts.ApiShadow = facts.ApiShadow || IsAPIName(n.Name) || IsAPIName(n.Binder)
			}
		case *ast.MatchRestPattern:
			if n != nil {
				facts.GroupShadow = facts.GroupShadow || groupName != "" && n.Name == groupName
				facts.ApiShadow = facts.ApiShadow || IsAPIName(n.Name)
			}
		case *ast.MatchStructPattern:
			if n != nil {
				facts.GroupShadow = facts.GroupShadow || groupName != "" && n.As == groupName
				facts.ApiShadow = facts.ApiShadow || IsAPIName(n.As)
				for _, arg := range n.Args {
					facts.ApiShadow = facts.ApiShadow || MatchPatternShadowsAPI(arg.Pattern)
				}
			}
		case *ast.MatchVariantPattern:
			if n != nil {
				facts.GroupShadow = facts.GroupShadow || groupName != "" && n.As == groupName
				facts.ApiShadow = facts.ApiShadow || IsAPIName(n.As)
				for _, arg := range n.Args {
					facts.ApiShadow = facts.ApiShadow || MatchPatternShadowsAPI(arg.Pattern)
				}
			}
		case *ast.MoveBindNamePattern:
			facts.GroupShadow = facts.GroupShadow || groupName != "" && n != nil && n.Name == groupName
			facts.ApiShadow = facts.ApiShadow || n != nil && IsAPIName(n.Name)
		case *ast.MoveBindStructPattern:
			if n != nil {
				for _, arg := range n.Args {
					facts.GroupShadow = facts.GroupShadow || groupName != "" && arg.Name == groupName
				}
			}
		case *ast.MoveBindTuplePattern:
			if n != nil {
				for _, arg := range n.Args {
					facts.GroupShadow = facts.GroupShadow || groupName != "" && arg.Name == groupName
				}
			}
		case *ast.MoveBindArg:
			facts.GroupShadow = facts.GroupShadow || groupName != "" && n != nil && n.Name == groupName
		case *ast.LambdaExpr:
			// A closure can retain a reference or hide a submission from this flat
			// window proof. Keep such windows on the conservative path.
			facts.GroupAPI = true
		}
	})
	return facts
}

func callName(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	if ident, ok := call.Func.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}
