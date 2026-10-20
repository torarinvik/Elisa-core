package semantic

import "elisacore/src/ast"

// A projected conditional is re-read after its selected constructor has run.
// Its condition must therefore depend only on values no constructor call can
// change. In particular, pure callees may still read mutable reference arguments;
// their permission set alone is not enough to establish this property.
func (a *Analyzer) projectionConditionStable(expr ast.Expr, depth int) bool {
	if expr == nil || depth > 32 {
		return false
	}
	expr = stripOptimizationParens(expr)
	if a.constructorProjectionStable(expr) {
		return true
	}
	switch n := expr.(type) {
	case *ast.FieldExpr:
		return a.projectionConditionStable(n.Object, depth+1)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && a.projectionConditionStable(n.Operand, depth+1)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && a.projectionConditionStable(n.Left, depth+1) && a.projectionConditionStable(n.Right, depth+1)
	case *ast.CastExpr:
		return a.projectionConditionStable(n.Operand, depth+1)
	case *ast.CallExpr:
		callee, ok := a.resolveReadOnlyScanCallee(n)
		if !ok || !storeFlowCallFitsParams(callee, n) || len(returnBorrowCallArgs(n)) != len(callee.Params) || !a.callResultStateIndependent(callee) {
			return false
		}
		for _, arg := range returnBorrowCallArgs(n) {
			if !a.projectionConditionStable(arg, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

func (tr *smtTranslator) ternaryProjectionBool(n *ast.FieldExpr, tern *ast.TernaryExpr, env map[string]string) (string, bool) {
	// This stability check uses the caller's lexical bindings. An inlined callee
	// has a different formal namespace and needs its own argument-snapshot proof,
	// except a ghost body admitted by the aggregate equation gate: every call in it
	// is pure and its formals are immutable, so no arm can change what the condition reads.
	if tr.calleeBodyDepth != 0 {
		if !tr.inlinedPureGhostRecordBody() {
			return "", false
		}
	} else if !tr.a.projectionConditionStable(tern.Cond, 0) {
		return "", false
	}
	cond, ok := tr.boolTerm(tern.Cond, env)
	if !ok {
		return "", false
	}
	arm := func(value ast.Expr) (string, bool) {
		field := &ast.FieldExpr{Position: n.Position, Object: value, Field: n.Field}
		if typ := tr.a.exprTypes[n]; typ != nil {
			tr.a.exprTypes[field] = typ
		}
		return tr.boolTerm(field, env)
	}
	left, ok := arm(tern.Value)
	if !ok {
		return "", false
	}
	right, ok := arm(tern.Alt)
	if !ok {
		return "", false
	}
	return "(ite " + cond + " " + left + " " + right + ")", true
}

func (tr *smtTranslator) inlinedPureGhostRecordBody() bool {
	decl := tr.calleeBodyDecl
	return decl != nil && decl.IsGhost && tr.a.aggregateRecordDefiningEquationEligible(decl)
}
