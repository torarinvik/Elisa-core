package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) evalStaticForStmt(stmt *ast.ForStmt, allowReturn bool) (ConstValue, bool, bool) {
	start, ok := a.evalConstExpr(stmt.Start)
	if !ok || start.Kind != ConstInt {
		a.errorf(stmt.Pos(), "static for start must evaluate to a compile-time integer")
		return ConstValue{}, false, false
	}
	end, ok := a.evalConstExpr(stmt.End)
	if !ok || end.Kind != ConstInt {
		a.errorf(stmt.Pos(), "static for end must evaluate to a compile-time integer")
		return ConstValue{}, false, false
	}
	step := int64(1)
	if stmt.Step != nil {
		stepValue, ok := a.evalConstExpr(stmt.Step)
		if !ok || stepValue.Kind != ConstInt {
			a.errorf(stmt.Pos(), "static for step must evaluate to a compile-time integer")
			return ConstValue{}, false, false
		}
		step = stepValue.Int
		if step < 0 {
			step = -step
		}
	}
	if step == 0 {
		a.errorf(stmt.Pos(), "static for step must not be zero")
		return ConstValue{}, false, false
	}
	ascending := start.Int <= end.Int
	if stmt.Op == lexer.TOKEN_RANGE_LT {
		ascending = true
	} else if stmt.Op == lexer.TOKEN_RANGE_GT {
		ascending = false
	}
	current := start.Int
	for i := 0; i < staticEvalLoopIterationLimit; i++ {
		if !staticForLoopContinue(stmt.Op, current, end.Int, ascending) {
			return ConstValue{}, false, true
		}
		a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{stmt.Name: ConstValue{Kind: ConstInt, Int: current}})
		value, returned, ok := a.evalStaticStmtBlock(stmt.Body, allowReturn)
		a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
		if !ok || returned {
			return value, returned, ok
		}
		if ascending {
			current += step
		} else {
			current -= step
		}
	}
	a.errorf(stmt.Pos(), "static for exceeded %d iterations", staticEvalLoopIterationLimit)
	return ConstValue{}, false, false
}

func (a *Analyzer) evalStaticIterForStmt(stmt *ast.IterForStmt, allowReturn bool) (ConstValue, bool, bool) {
	if stmt == nil {
		return ConstValue{}, false, false
	}
	if stmt.Mode != ast.IterBindValue {
		a.errorf(stmt.Pos(), "static iterable for currently supports value binding only")
		return ConstValue{}, false, false
	}
	source, ok := a.evalConstExpr(stmt.Source)
	if !ok || (source.Kind != ConstList && source.Kind != ConstTuple) {
		a.errorf(stmt.Pos(), "static iterable for source must evaluate to a compile-time list or tuple")
		return ConstValue{}, false, false
	}
	pattern := stmt.Pattern
	if pattern == nil {
		pattern = &ast.MoveBindNamePattern{Position: stmt.Pos(), Name: "_"}
	}
	elems := source.Elems
	for i := 0; i < len(elems); i++ {
		item := elems[i]
		if stmt.Reverse {
			item = elems[len(elems)-1-i]
		}
		scope, ok := a.bindConstMovePattern(pattern, item)
		if !ok {
			a.errorf(stmt.Pos(), "static iterable for pattern does not match compile-time item")
			return ConstValue{}, false, false
		}
		a.constEvalScopes = append(a.constEvalScopes, scope)
		if stmt.PatternFilter != nil {
			matched, bindings, ok := a.evalStaticIterPatternFilter(stmt)
			if !ok {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				a.errorf(stmt.PatternFilter.Pos(), "static iterable for pattern filter must be a compile-time matchable pattern")
				return ConstValue{}, false, false
			}
			if !matched {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				continue
			}
			for name, value := range bindings {
				scope[name] = value
			}
			a.constEvalScopes[len(a.constEvalScopes)-1] = scope
		}
		include := true
		if stmt.WhereFilter != nil {
			filter, ok := a.evalConstExpr(stmt.WhereFilter)
			if !ok || filter.Kind != ConstBool {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				a.errorf(stmt.WhereFilter.Pos(), "static iterable for where filter must evaluate to a compile-time bool")
				return ConstValue{}, false, false
			}
			include = include && filter.Bool
		}
		if stmt.Filter != nil {
			filter, ok := a.evalConstExpr(stmt.Filter)
			if !ok || filter.Kind != ConstBool {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				a.errorf(stmt.Filter.Pos(), "static iterable for filter must evaluate to a compile-time bool")
				return ConstValue{}, false, false
			}
			include = include && filter.Bool
		}
		if include {
			value, returned, ok := a.evalStaticStmtBlock(stmt.Body, allowReturn)
			if !ok || returned {
				a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
				return value, returned, ok
			}
		}
		a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
	}
	return ConstValue{}, false, true
}

func (a *Analyzer) evalStaticIterPatternFilter(stmt *ast.IterForStmt) (bool, map[string]ConstValue, bool) {
	if stmt == nil || stmt.PatternFilter == nil {
		return true, nil, true
	}
	subjectName := stmt.PatternFilterSubject
	if subjectName == "" {
		if bind, ok := stmt.Pattern.(*ast.MoveBindNamePattern); ok && bind != nil && bind.Name != "" && bind.Name != "_" {
			subjectName = bind.Name
		}
	}
	subject, _, ok := a.constEvalValueScope(subjectName)
	if !ok {
		if subjectName == "" {
			return false, nil, true
		}
		return false, nil, false
	}
	matched, bindings, ok := a.evalStaticMatchPattern(stmt.PatternFilter, subject)
	if !ok {
		return false, nil, false
	}
	if !matched {
		return false, nil, true
	}
	return true, bindings, true
}

func staticForLoopContinue(op lexer.TokenKind, current int64, end int64, ascending bool) bool {
	switch op {
	case lexer.TOKEN_RANGE:
		if ascending {
			return current <= end
		}
		return current >= end
	case lexer.TOKEN_RANGE_LT:
		return current < end
	case lexer.TOKEN_RANGE_GT:
		return current > end
	default:
		return false
	}
}

func (a *Analyzer) validateStaticFunctionTotality(fn *ast.FuncDecl) {
	if fn == nil || !fn.Static {
		return
	}
	if !a.staticFunctionReturnsVoid(fn) && !staticStmtBlockAlwaysTerminates(fn.Body) {
		if pos, detail, ok := staticStmtBlockTerminationIssue(fn.Body); ok {
			a.errorf(pos, "static function %q must return on all paths: %s", fn.Name, detail)
		} else {
			a.errorf(fn.Pos(), "static function %q must return on all paths", fn.Name)
		}
	}
	a.validateStaticFunctionRecursion(fn)
}

func (a *Analyzer) staticFunctionReturnsVoid(fn *ast.FuncDecl) bool {
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	return ok && fnType != nil && isVoidType(fnType.Return)
}

func staticStmtBlockAlwaysTerminates(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		if staticStmtAlwaysTerminates(stmt) {
			return true
		}
	}
	return false
}

func staticStmtBlockTerminationIssue(stmts []ast.Stmt) (lexer.Pos, string, bool) {
	if len(stmts) == 0 {
		return lexer.Pos{}, "the function body can fall through", true
	}
	for _, stmt := range stmts {
		if staticStmtAlwaysTerminates(stmt) {
			return lexer.Pos{}, "", false
		}
		if pos, detail, ok := staticStmtTerminationIssue(stmt); ok {
			return pos, detail, true
		}
	}
	last := stmts[len(stmts)-1]
	return last.Pos(), "control can fall through after this statement", true
}

func staticStmtAlwaysTerminates(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.StaticErrorStmt:
		return true
	case *ast.StaticBlockStmt:
		return staticStmtBlockAlwaysTerminates(n.Body)
	case *ast.IfStmt:
		return staticIfStmtAlwaysTerminates(n.Then, n.Elifs, n.Else)
	case *ast.StaticIfStmt:
		return staticStaticIfStmtAlwaysTerminates(n.Then, n.Elifs, n.Else)
	case *ast.MatchStmt:
		return staticMatchStmtAlwaysTerminates(n)
	default:
		return false
	}
}

func staticStmtTerminationIssue(stmt ast.Stmt) (lexer.Pos, string, bool) {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		if len(n.Else) == 0 {
			return n.Pos(), "this if statement has no else branch", true
		}
		if !staticStmtBlockAlwaysTerminates(n.Then) {
			if pos, detail, ok := staticStmtBlockTerminationIssue(n.Then); ok {
				return pos, "the then branch does not terminate: " + detail, true
			}
			return n.Pos(), "the then branch does not terminate", true
		}
		for _, elif := range n.Elifs {
			if !staticStmtBlockAlwaysTerminates(elif.Body) {
				if pos, detail, ok := staticStmtBlockTerminationIssue(elif.Body); ok {
					return pos, "an elif branch does not terminate: " + detail, true
				}
				return elif.Position, "an elif branch does not terminate", true
			}
		}
		if !staticStmtBlockAlwaysTerminates(n.Else) {
			if pos, detail, ok := staticStmtBlockTerminationIssue(n.Else); ok {
				return pos, "the else branch does not terminate: " + detail, true
			}
			return n.Pos(), "the else branch does not terminate", true
		}
	case *ast.StaticIfStmt:
		if len(n.Else) == 0 {
			return n.Pos(), "this static if statement has no else branch", true
		}
		if !staticStmtBlockAlwaysTerminates(n.Then) {
			if pos, detail, ok := staticStmtBlockTerminationIssue(n.Then); ok {
				return pos, "the then branch does not terminate: " + detail, true
			}
			return n.Pos(), "the then branch does not terminate", true
		}
		for _, elif := range n.Elifs {
			if !staticStmtBlockAlwaysTerminates(elif.Body) {
				if pos, detail, ok := staticStmtBlockTerminationIssue(elif.Body); ok {
					return pos, "a static elif branch does not terminate: " + detail, true
				}
				return elif.Position, "a static elif branch does not terminate", true
			}
		}
		if !staticStmtBlockAlwaysTerminates(n.Else) {
			if pos, detail, ok := staticStmtBlockTerminationIssue(n.Else); ok {
				return pos, "the else branch does not terminate: " + detail, true
			}
			return n.Pos(), "the else branch does not terminate", true
		}
	case *ast.MatchStmt:
		hasCatchAll := false
		for _, arm := range n.Arms {
			switch arm.Pattern.(type) {
			case *ast.MatchWildcardPattern, *ast.MatchBindPattern:
				hasCatchAll = true
			}
			if !staticStmtBlockAlwaysTerminates(arm.Body) {
				if pos, detail, ok := staticStmtBlockTerminationIssue(arm.Body); ok {
					return pos, "a match arm does not terminate: " + detail, true
				}
				return arm.Position, "a match arm does not terminate", true
			}
		}
		if !hasCatchAll {
			return n.Pos(), "this match statement has no catch-all arm", true
		}
	case *ast.StaticBlockStmt:
		return staticStmtBlockTerminationIssue(n.Body)
	}
	return stmt.Pos(), "this statement can fall through", true
}

func staticMatchStmtAlwaysTerminates(stmt *ast.MatchStmt) bool {
	if stmt == nil || len(stmt.Arms) == 0 {
		return false
	}
	hasWildcard := false
	for _, arm := range stmt.Arms {
		switch arm.Pattern.(type) {
		case *ast.MatchWildcardPattern, *ast.MatchBindPattern:
			// docs/122 §5.1: a guarded catch-all can fail, so it does not guarantee
			// the match terminates.
			if arm.Guard == nil {
				hasWildcard = true
			}
		}
		if !staticStmtBlockAlwaysTerminates(arm.Body) {
			return false
		}
	}
	return hasWildcard
}

func staticIfStmtAlwaysTerminates(then []ast.Stmt, elifs []ast.ElifClause, elseStmts []ast.Stmt) bool {
	if len(elseStmts) == 0 {
		return false
	}
	if !staticStmtBlockAlwaysTerminates(then) || !staticStmtBlockAlwaysTerminates(elseStmts) {
		return false
	}
	for _, elif := range elifs {
		if !staticStmtBlockAlwaysTerminates(elif.Body) {
			return false
		}
	}
	return true
}

func staticStaticIfStmtAlwaysTerminates(then []ast.Stmt, elifs []ast.StaticElifClause, elseStmts []ast.Stmt) bool {
	if len(elseStmts) == 0 {
		return false
	}
	if !staticStmtBlockAlwaysTerminates(then) || !staticStmtBlockAlwaysTerminates(elseStmts) {
		return false
	}
	for _, elif := range elifs {
		if !staticStmtBlockAlwaysTerminates(elif.Body) {
			return false
		}
	}
	return true
}
