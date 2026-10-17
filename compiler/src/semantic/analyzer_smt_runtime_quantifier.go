package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Runtime range quantifiers are lowered to a local Boolean flag and an early-exit loop.
// Recognize the entire shape, independent of generated spelling; arbitrary value blocks
// and loops must not acquire a logical equation from a similar variable name.
func runtimeRangeQuantifierParts(block *ast.ExprBlock) (*ast.ForStmt, ast.Expr, bool, bool) {
	if block == nil || len(block.Captures) != 0 || len(block.Stmts) != 2 {
		return nil, nil, false, false
	}
	decl, ok := block.Stmts[0].(*ast.VarDeclStmt)
	if !ok || !decl.Mutable || decl.Ghost || decl.Type != nil || decl.Owner != nil {
		return nil, nil, false, false
	}
	initial, ok := decl.Value.(*ast.BoolLit)
	if !ok {
		return nil, nil, false, false
	}
	value, ok := block.Value.(*ast.Ident)
	if !ok || value.Name != decl.Name {
		return nil, nil, false, false
	}
	loop, ok := block.Stmts[1].(*ast.ForStmt)
	if !ok || loop.Name == decl.Name || loop.Start == nil || loop.End == nil || loop.Step != nil || loop.Reverse ||
		loop.Op != lexer.TOKEN_RANGE_LT || loop.PreReserve != nil || len(loop.PreReserves) != 0 || len(loop.Body) != 1 {
		return nil, nil, false, false
	}
	branch, ok := loop.Body[0].(*ast.IfStmt)
	if !ok || len(branch.Elifs) != 0 || len(branch.Else) != 0 || len(branch.Then) != 2 {
		return nil, nil, false, false
	}
	assign, ok := branch.Then[0].(*ast.AssignStmt)
	if !ok || assign.Optional || assign.AsOverlayCall != nil || assign.ArgManifest || assign.WriteThrough {
		return nil, nil, false, false
	}
	target, ok := assign.Target.(*ast.Ident)
	if !ok || target.Name != decl.Name {
		return nil, nil, false, false
	}
	assigned, ok := assign.Value.(*ast.BoolLit)
	if !ok || assigned.Value == initial.Value {
		return nil, nil, false, false
	}
	if _, ok := branch.Then[1].(*ast.BreakStmt); !ok {
		return nil, nil, false, false
	}
	exists := assigned.Value
	predicate := branch.Cond
	if !exists {
		not, ok := predicate.(*ast.UnaryExpr)
		if !ok || not.Op != lexer.TOKEN_NOT || not.LoweredCall != nil {
			return nil, nil, false, false
		}
		predicate = not.Operand
	}
	// A predicate that reads its own flag is a stateful loop rather than a quantifier.
	if staticExprContainsIdent(predicate, decl.Name) || staticExprContainsIdent(loop.Start, decl.Name) || staticExprContainsIdent(loop.End, decl.Name) {
		return nil, nil, false, false
	}
	return loop, predicate, exists, true
}

func staticExprContainsIdent(expr ast.Expr, name string) bool {
	found := false
	walkStoreScanNodes([]ast.Stmt{&ast.ExprStmt{Expr: expr}}, func(node any) {
		if id, ok := node.(*ast.Ident); ok && id.Name == name {
			found = true
		}
	})
	return found
}

func (tr *smtTranslator) runtimeRangeQuantifierTerm(block *ast.ExprBlock, env map[string]string) (string, bool) {
	loop, predicate, exists, ok := runtimeRangeQuantifierParts(block)
	if !ok || tr.calleeBodyDepth != 0 {
		return "", false
	}
	binders := map[string]bool{}
	for name, value := range env {
		if smtTermMentionsQuantifierBinder(value) {
			binders[name] = true
		}
	}
	nodes := 0
	if !tr.a.smtBoolInitializerStableWithBinders(block, &nodes, map[string]bool{}, 0, binders) {
		return "", false
	}
	startType, endType := tr.a.exprTypes[loop.Start], tr.a.exprTypes[loop.End]
	if startType == nil || endType == nil {
		return "", false
	}
	loopType := CommonNumericType(startType, endType)
	_, bits, integral := smtIntWidthSign(loopType)
	if !integral || bits <= 0 || bits > 64 || startType == nil || endType == nil {
		return "", false
	}
	// Bounds are evaluated in the OUTER scope, then converted exactly as the runtime loop.
	lo, ok := tr.runtimeRangeEndpointTerm(loop.Start, loopType, env)
	if !ok {
		return "", false
	}
	hi, ok := tr.runtimeRangeEndpointTerm(loop.End, loopType, env)
	if !ok {
		return "", false
	}
	name := "q_@runtime$" + strconv.Itoa(tr.runtimeQuantifierCount)
	tr.runtimeQuantifierCount++
	scoped := make(map[string]string, len(env)+1)
	for key, value := range env {
		scoped[key] = value
	}
	scoped[loop.Name] = name
	body, ok := tr.boolTerm(predicate, scoped)
	if !ok {
		return "", false
	}
	guard := "(and (<= " + lo + " " + name + ") (< " + name + " " + hi + "))"
	if exists {
		return "(exists ((" + name + " Int)) (and " + guard + " " + body + "))", true
	}
	return "(forall ((" + name + " Int)) (or (not " + guard + ") " + body + "))", true
}

// Do not use caller-scope range shortcuts for endpoints: an outer quantifier
// can shadow a caller local whose known range does not describe this value.
func (tr *smtTranslator) runtimeRangeEndpointTerm(value ast.Expr, target Type, env map[string]string) (string, bool) {
	cast := &ast.CastExpr{Position: value.Pos(), Operand: value}
	tr.a.exprTypes[cast] = target
	defer delete(tr.a.exprTypes, cast)
	return tr.termEnv(cast, env)
}
