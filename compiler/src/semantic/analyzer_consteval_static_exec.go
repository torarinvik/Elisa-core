package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) evalStaticFunctionCall(expr *ast.CallExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	if a.staticCallDepth >= StaticEvalCallDepthLimit {
		a.errorf(expr.Pos(), "static function evaluation exceeded %d calls", StaticEvalCallDepthLimit)
		return ConstValue{}, false
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || a.nameIsRuntimeBinding(ident.Name) {
		return ConstValue{}, false
	}
	sym, _, ok := a.lookupVisibleGlobal(ident.Name)
	if !ok || sym == nil {
		return ConstValue{}, false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return ConstValue{}, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil {
		return ConstValue{}, false
	}
	// Static functions and laws are both const-evaluable: a law is enforced pure (docs/85), so
	// evaluating its body on constant arguments is sound — this is how a refinement obligation on a
	// constant value is discharged (or refuted) at compile time.
	if !fnType.Static && !decl.IsLaw {
		return ConstValue{}, false
	}
	args := expr.Args
	if expr.ResolvedArgsValid {
		args = expr.ResolvedArgs
	}
	if len(decl.Params) != len(args) {
		return ConstValue{}, false
	}
	scope := make(map[string]ConstValue, len(decl.Params))
	for i, arg := range args {
		value, ok := a.evalConstExpr(arg)
		if !ok {
			return ConstValue{}, false
		}
		scope[decl.Params[i].Name] = value
	}
	a.staticCallDepth++
	a.constEvalScopes = append(a.constEvalScopes, scope)
	value, returned, ok := a.evalStaticStmtBlock(decl.Body, true)
	a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
	a.staticCallDepth--
	if !returned {
		if isVoidType(fnType.Return) {
			return ConstValue{Kind: ConstUnknown}, true
		}
		return ConstValue{}, false
	}
	return value, ok
}

func (a *Analyzer) evalStaticStmtBlock(stmts []ast.Stmt, allowReturn bool) (ConstValue, bool, bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.ReturnStmt:
			if !allowReturn {
				a.errorf(n.Pos(), "return is not allowed in a static block")
				return ConstValue{}, false, false
			}
			value, ok := a.evalConstExpr(n.Value)
			return value, true, ok
		case *ast.VarDeclStmt:
			if n.Value == nil {
				a.errorf(n.Pos(), "static local %q must have a compile-time initializer", n.Name)
				return ConstValue{}, false, false
			}
			value, ok := a.evalConstExpr(n.Value)
			if !ok {
				a.errorf(n.Pos(), "static local %q initializer must evaluate at compile time", n.Name)
				return ConstValue{}, false, false
			}
			a.setConstEvalValue(n.Name, value)
		case *ast.AugAssignStmt:
			if n.CollectionAppend == nil || !a.evalStaticExprStmt(n.CollectionAppend) {
				a.errorf(n.Pos(), "static augmented assignment must be a compile-time collection append")
				return ConstValue{}, false, false
			}
		case *ast.AssignStmt:
			ident, ok := n.Target.(*ast.Ident)
			if !ok {
				a.errorf(n.Pos(), "static assignment target must be a local name")
				return ConstValue{}, false, false
			}
			value, ok := a.evalConstExpr(n.Value)
			if !ok {
				a.errorf(n.Pos(), "static assignment value must evaluate at compile time")
				return ConstValue{}, false, false
			}
			if !a.updateConstEvalValue(ident.Name, value) {
				a.errorf(n.Pos(), "unknown static local %q", ident.Name)
				return ConstValue{}, false, false
			}
		case *ast.StaticAssertStmt:
			if cond, ok := a.evalConstBoolExpr(n.Cond); ok && !cond {
				a.reportStaticAssertFailure(n.Pos(), n.Message)
				return ConstValue{}, false, false
			}
		case *ast.StaticAssertBlockStmt:
			for _, item := range n.Assertions {
				if cond, ok := a.evalConstBoolExpr(item.Cond); ok && !cond {
					a.reportStaticAssertFailure(item.Position, item.Message)
					return ConstValue{}, false, false
				}
			}
		case *ast.StaticErrorStmt:
			if msg, ok := a.evalConstStringExpr(n.Message); ok {
				a.errorf(n.Pos(), "static error: %s", msg)
			} else {
				a.errorf(n.Pos(), "static error triggered")
			}
			return ConstValue{}, false, false
		case *ast.StaticIfStmt:
			value, returned, ok := a.evalStaticStmtBlock(a.activeStmtBranch(n), allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.StaticBlockStmt:
			a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{})
			value, returned, ok := a.evalStaticStmtBlock(n.Body, allowReturn)
			a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.IfStmt:
			cond, ok := a.evalConstBoolExpr(n.Cond)
			if !ok {
				a.errorf(n.Pos(), "static if condition must evaluate to a compile-time bool")
				return ConstValue{}, false, false
			}
			branch := n.Else
			if cond {
				branch = n.Then
			} else {
				for _, elif := range n.Elifs {
					elifCond, ok := a.evalConstBoolExpr(elif.Cond)
					if !ok {
						a.errorf(elif.Position, "static elif condition must evaluate to a compile-time bool")
						return ConstValue{}, false, false
					}
					if elifCond {
						branch = elif.Body
						break
					}
				}
			}
			value, returned, ok := a.evalStaticStmtBlock(branch, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.WhileStmt:
			for i := 0; i < staticEvalLoopIterationLimit; i++ {
				cond, ok := a.evalConstBoolExpr(n.Cond)
				if !ok {
					a.errorf(n.Pos(), "static while condition must evaluate to a compile-time bool")
					return ConstValue{}, false, false
				}
				if !cond {
					break
				}
				value, returned, ok := a.evalStaticStmtBlock(n.Body, allowReturn)
				if !ok || returned {
					return value, returned, ok
				}
				if i == staticEvalLoopIterationLimit-1 {
					a.errorf(n.Pos(), "static while exceeded %d iterations", staticEvalLoopIterationLimit)
					return ConstValue{}, false, false
				}
			}
		case *ast.ForStmt:
			value, returned, ok := a.evalStaticForStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.IterForStmt:
			value, returned, ok := a.evalStaticIterForStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.MatchStmt:
			value, returned, ok := a.evalStaticMatchStmt(n, allowReturn)
			if !ok || returned {
				return value, returned, ok
			}
		case *ast.PassStmt:
		case *ast.ExprStmt:
			if ok := a.evalStaticExprStmt(n.Expr); !ok {
				a.errorf(n.Pos(), "static expression statement must evaluate at compile time")
				return ConstValue{}, false, false
			}
		default:
			a.errorf(stmt.Pos(), "static block only allows compile-time assertions, errors, local bindings, assignments, conditionals, returns, nested static blocks, and expression statements")
			return ConstValue{}, false, false
		}
	}
	return ConstValue{}, false, true
}

func (a *Analyzer) evalStaticExprStmt(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if a.evalStaticDArrayPushExpr(expr) {
		return true
	}
	if a.evalStaticDArrayExtendExpr(expr) {
		return true
	}
	_, ok := a.evalConstExpr(expr)
	return ok
}

func (a *Analyzer) evalStaticDArrayPushExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" {
		return false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || ident == nil || len(call.Args) != 1 || call.NamedArgCount() != 0 {
		return false
	}
	current, scopeIndex, ok := a.constEvalValueScope(ident.Name)
	if !ok || current.Kind != ConstList {
		return false
	}
	value, ok := a.evalConstExpr(call.Args[0])
	if !ok {
		return false
	}
	updated := cloneConstValue(current)
	updated.Elems = append(updated.Elems, cloneConstValue(value))
	a.constEvalScopes[scopeIndex][ident.Name] = updated
	return true
}

func (a *Analyzer) evalStaticDArrayExtendExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" {
		return false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || ident == nil || len(call.Args) != 1 || call.NamedArgCount() != 0 {
		return false
	}
	current, scopeIndex, ok := a.constEvalValueScope(ident.Name)
	if !ok || current.Kind != ConstList {
		return false
	}
	source, ok := a.evalConstExpr(call.Args[0])
	if !ok || source.Kind != ConstList {
		return false
	}
	updated := cloneConstValue(current)
	for _, elem := range source.Elems {
		updated.Elems = append(updated.Elems, cloneConstValue(elem))
	}
	a.constEvalScopes[scopeIndex][ident.Name] = updated
	return true
}

func (a *Analyzer) evalStaticMatchStmt(stmt *ast.MatchStmt, allowReturn bool) (ConstValue, bool, bool) {
	if stmt.Store != nil {
		a.errorf(stmt.Store.Pos(), "static match does not support in-store clauses")
		return ConstValue{}, false, false
	}
	value, ok := a.evalConstExpr(stmt.Value)
	if !ok {
		a.errorf(stmt.Value.Pos(), "static match value must evaluate at compile time")
		return ConstValue{}, false, false
	}
	for _, arm := range stmt.Arms {
		matched, bindings, ok := a.evalStaticMatchPattern(arm.Pattern, value)
		if !ok {
			a.errorf(arm.Pattern.Pos(), "static match only supports compile-time literal patterns, `_`, name binds, and `|` patterns")
			return ConstValue{}, false, false
		}
		if !matched {
			continue
		}
		scope := map[string]ConstValue{}
		for name, bindingValue := range bindings {
			scope[name] = bindingValue
		}
		a.constEvalScopes = append(a.constEvalScopes, scope)
		result, returned, ok := a.evalStaticStmtBlock(arm.Body, allowReturn)
		a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
		return result, returned, ok
	}
	return ConstValue{}, false, true
}

func (a *Analyzer) evalStaticMatchPattern(pattern ast.MatchPattern, value ConstValue) (bool, map[string]ConstValue, bool) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return true, nil, true
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" {
			return true, nil, true
		}
		return true, map[string]ConstValue{p.Name: value}, true
	case *ast.MatchLiteralPattern:
		patternValue, ok := a.evalConstExpr(p.Value)
		if !ok {
			return false, nil, false
		}
		equal, ok := a.evalConstEquality(value, patternValue, true)
		if !ok || equal.Kind != ConstBool {
			return false, nil, false
		}
		return equal.Bool, nil, true
	case *ast.MatchStringLiteralPattern:
		if value.Kind != ConstString {
			return false, nil, true
		}
		return value.String == p.Value, nil, true
	case *ast.MatchTuplePattern:
		if value.Kind != ConstTuple && value.Kind != ConstList {
			return false, nil, true
		}
		return a.evalStaticSequenceMatchPatterns(p.Elems, value.Elems)
	case *ast.MatchListPattern:
		if value.Kind != ConstList {
			return false, nil, true
		}
		return a.evalStaticSequenceMatchPatterns(p.Elems, value.Elems)
	case *ast.MatchStructPattern:
		if value.Kind != ConstRecord {
			return false, nil, true
		}
		return a.evalStaticStructMatchPatterns(matchStructPatternArgs(p), value)
	case *ast.MatchVariantPattern:
		if len(p.Args) != 0 {
			return false, nil, false
		}
		patternValue, ok := a.lookupVisibleConst(p.EnumName + "." + p.Variant)
		if !ok {
			return false, nil, false
		}
		equal, ok := a.evalConstEquality(value, patternValue, true)
		if !ok || equal.Kind != ConstBool {
			return false, nil, false
		}
		return equal.Bool, nil, true
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			matched, bindings, ok := a.evalStaticMatchPattern(option, value)
			if !ok {
				return false, nil, false
			}
			if matched {
				return true, bindings, true
			}
		}
		return false, nil, true
	default:
		return false, nil, false
	}
}

func (a *Analyzer) evalStaticSequenceMatchPatterns(patterns []ast.MatchPattern, elems []ConstValue) (bool, map[string]ConstValue, bool) {
	if len(patterns) == 0 {
		return len(elems) == 0, nil, true
	}
	if _, ok := patterns[len(patterns)-1].(*ast.MatchRestPattern); ok {
		if len(elems) < len(patterns)-1 {
			return false, nil, true
		}
	} else if len(elems) != len(patterns) {
		return false, nil, true
	}
	bindings := map[string]ConstValue{}
	limit := len(patterns)
	if _, ok := patterns[len(patterns)-1].(*ast.MatchRestPattern); ok {
		limit--
	}
	for i := 0; i < limit; i++ {
		matched, itemBindings, ok := a.evalStaticMatchPattern(patterns[i], elems[i])
		if !ok {
			return false, nil, false
		}
		if !matched {
			return false, nil, true
		}
		for name, value := range itemBindings {
			bindings[name] = value
		}
	}
	return true, bindings, true
}

func matchStructPatternArgs(pattern *ast.MatchStructPattern) []*ast.MatchPatternArg {
	if pattern == nil {
		return nil
	}
	if len(pattern.ResolvedArgs) != 0 {
		return pattern.ResolvedArgs
	}
	args := make([]*ast.MatchPatternArg, 0, len(pattern.Args))
	for i := range pattern.Args {
		arg := pattern.Args[i]
		args = append(args, &ast.MatchPatternArg{Position: arg.Position, Name: arg.Name, Pattern: arg.Pattern})
	}
	return args
}

func (a *Analyzer) evalStaticStructMatchPatterns(args []*ast.MatchPatternArg, value ConstValue) (bool, map[string]ConstValue, bool) {
	bindings := map[string]ConstValue{}
	for _, arg := range args {
		if arg == nil || arg.Name == "" {
			continue
		}
		fieldValue, ok := ConstReflectionRecordField(value, arg.Name)
		if !ok {
			return false, nil, true
		}
		if arg.Pattern == nil {
			continue
		}
		matched, itemBindings, ok := a.evalStaticMatchPattern(arg.Pattern, fieldValue)
		if !ok {
			return false, nil, false
		}
		if !matched {
			return false, nil, true
		}
		for name, value := range itemBindings {
			bindings[name] = value
		}
	}
	return true, bindings, true
}
