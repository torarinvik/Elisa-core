package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Guard-prefix returns of an aggregate-valued callee. A function body that starts with
//
//	return (false, 0) if text.len <= 0
//	return (true, value) if position == text.len and consumed
//	...
//
// returns the first literal whose guard holds, before any other statement runs. For a call
// projected as a token (analyzer_smt_call_projection.go) this asserts, for each leading guarded
// return, `requires ∧ ¬C1 ∧ … ∧ ¬C(k-1) ∧ Ck ⟹ token.f == L_k.f` for every field of the literal.
//
// SOUNDNESS: these are the body's own first steps, read in the callee's frame (formals bound to
// the argument terms; any callee local or non-constant global declines). Nothing executes before
// the first guard and nothing between guards, so when the guards up to Ck are as stated the call
// returns exactly L_k. No recursion is unfolded (a recursive call in a literal declines like any
// other non-literal component), so termination is not needed. The walk stops at the first
// statement that is not a guarded literal return, and every translation failure only withholds a
// hypothesis.
const guardPrefixMaxReturns = 8

func (tr *smtTranslator) assumeGuardPrefixReturns(decl *ast.FuncDecl, subst map[string]ast.Expr, argEnv map[string]string, token string) {
	if tr == nil || tr.a == nil || decl == nil || len(decl.Body) == 0 {
		return
	}
	if tr.callClauseDepth >= smtCallClauseMaxDepth || tr.callClauseExpansions >= smtCallClauseMaxExpansions || tr.callClauseInFlight[decl] {
		return
	}
	fields, ok := tr.a.aggregateReturnFieldNames(decl)
	if !ok {
		return
	}
	body := decl.Body
	if len(body) == 1 {
		if can, isCan := body[0].(*ast.CanStmt); isCan && can != nil && can.HandlerName == "" && len(can.HandlerArgs) == 0 && can.As == "" {
			body = can.Body
		}
	}
	tr.callClauseExpansions++
	tr.callClauseDepth++
	tr.callClauseInFlight[decl] = true
	defer func() {
		tr.callClauseDepth--
		delete(tr.callClauseInFlight, decl)
	}()
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	if !ok {
		return
	}
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, env)
		if !ok {
			return
		}
		guard = smtAnd(guard, h)
	}
	resultEnv := make(map[string]string, len(env)+1)
	for name, term := range env {
		resultEnv[name] = term
	}
	resultEnv["result"] = dtToken(token)
	for i, stmt := range body {
		if i >= guardPrefixMaxReturns {
			return
		}
		switch n := stmt.(type) {
		case *ast.IfStmt:
			if n == nil || len(n.Elifs) != 0 || len(n.Else) != 0 || len(n.Then) != 1 {
				return
			}
			ret, ok := n.Then[0].(*ast.ReturnStmt)
			if !ok || ret == nil || ret.Value == nil {
				return
			}
			if !tr.a.matchArmValueReadOnly(n.Cond, 0) {
				return
			}
			cond, ok := tr.calleeBoolTerm(decl, n.Cond, env)
			if !ok {
				return
			}
			tr.assumeGuardedLiteralReturn(decl, smtAnd(guard, cond), ret.Value, fields, resultEnv)
			guard = smtAnd(guard, "(not "+cond+")")
		case *ast.ReturnStmt:
			if n != nil && n.Value != nil {
				tr.assumeGuardedLiteralReturn(decl, guard, n.Value, fields, resultEnv)
			}
			return
		default:
			return
		}
	}
}

func smtAnd(a, b string) string {
	if a == "true" {
		return b
	}
	if b == "true" {
		return a
	}
	return "(and " + a + " " + b + ")"
}

// aggregateReturnFieldNames lists the return type's fields in construction order.
func (a *Analyzer) aggregateReturnFieldNames(decl *ast.FuncDecl) ([]TupleField, bool) {
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return nil, false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil {
		return nil, false
	}
	tuple, ok := stripRefForBounds(fn.Return).(*TupleType)
	if !ok || tuple == nil || len(tuple.Fields) == 0 {
		return nil, false
	}
	for _, f := range tuple.Fields {
		if f.Name == "" {
			return nil, false
		}
	}
	return tuple.Fields, true
}

// assumeGuardedLiteralReturn asserts guard ⟹ result.f == component for each scalar component of a
// returned tuple literal. Components that do not translate in the callee frame are skipped.
func (tr *smtTranslator) assumeGuardedLiteralReturn(decl *ast.FuncDecl, guard string, value ast.Expr, fields []TupleField, resultEnv map[string]string) {
	tuple, ok := stripOptimizationParens(value).(*ast.TupleExpr)
	if !ok || tuple == nil || len(tuple.Elems) != len(fields) {
		return
	}
	for i, field := range fields {
		elem := tuple.Elems[i]
		if elem == nil || !tr.a.matchArmValueReadOnly(elem, 0) {
			continue
		}
		ft := stripRefForBounds(field.Type)
		if !(IsBoolType(ft) || isSMTExactAssignmentType(ft)) {
			continue
		}
		read := &ast.FieldExpr{Position: elem.Pos(), Object: &ast.Ident{Position: elem.Pos(), Name: "result"}, Field: field.Name}
		tr.a.exprTypes[read] = field.Type
		eq := &ast.BinaryExpr{Position: elem.Pos(), Op: lexer.TOKEN_EQEQ, Left: read, Right: elem}
		tr.a.exprTypes[eq] = &BuiltinType{Name: "bool"}
		h, ok := tr.calleeBoolTerm(decl, eq, resultEnv)
		if !ok {
			continue
		}
		if guard == "true" {
			tr.auxDecls = append(tr.auxDecls, "(assert "+h+")\n")
		} else {
			tr.auxDecls = append(tr.auxDecls, "(assert (=> "+guard+" "+h+"))\n")
		}
	}
}
