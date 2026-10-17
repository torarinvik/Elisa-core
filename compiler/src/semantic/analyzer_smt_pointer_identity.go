package semantic

import "elisacore/src/ast"

// Resolve only literal components that the constructor evaluation-order rules
// allow us to read again. In particular, a later constructor argument may have
// changed a mutable pointer slot after the field captured its old value.
func (tr *smtTranslator) pointerLiteralOperand(expr ast.Expr, env map[string]string, depth int) ast.Expr {
	expr = stripOptimizationParens(expr)
	if expr == nil || depth > 32 || tr.a == nil || tr.a.shadowingActive() {
		return expr
	}
	field, ok := expr.(*ast.FieldExpr)
	if !ok {
		return expr
	}
	if tr.resultFields != nil && tr.calleeBodyDepth == 0 {
		root, _ := projectionChain(field)
		if id, ok := root.(*ast.Ident); ok && id.Name == "result" && !tr.envBindsRoot(id, env) {
			if _, direct := stripOptimizationParens(field.Object).(*ast.Ident); direct {
				if value := tr.resultFields[field.Field]; value != nil {
					return tr.pointerLiteralOperand(value, env, depth+1)
				}
			} else if rerooted, found := rerootBelowFirstField(field, tr.resultFields, tr.a); found {
				return tr.pointerLiteralOperand(rerooted, env, depth+1)
			}
		}
	}
	if value, found := tr.a.reevaluableLiteralComponent(field, 0); found && value != nil {
		return tr.pointerLiteralOperand(value, env, depth+1)
	}
	return expr
}

// pointerIdentityReadStable permits reflexivity of builtin element addresses.
// Computing an address reads the container header and index but not the element.
// Keep this local to pointer equality: it does not make an indexed value safe to
// re-evaluate after later constructor arguments have mutated its storage or index.
func (tr *smtTranslator) pointerIdentityReadStable(expr ast.Expr) bool {
	if sideEffectFreeExpr(expr, 0) {
		return true
	}
	address, ok := stripOptimizationParens(expr).(*ast.AddrOfExpr)
	if !ok || address == nil || tr.a == nil {
		return false
	}
	index, ok := stripOptimizationParens(address.Operand).(*ast.IndexExpr)
	if !ok || index == nil || index.Index2 != nil || index.Fallback != nil || index.AsOverlayCall != nil ||
		!sideEffectFreeExpr(index.Object, 0) || !sideEffectFreeExpr(index.Index, 0) {
		return false
	}
	typ := tr.a.exprTypes[index.Object]
	switch stripRefForBounds(typ).(type) {
	case *ArrayType, *DArrayType:
		return true
	}
	// Raw pointers to primitive integers use builtin indexing. A reference to a
	// user-defined indexable value must not acquire this rule by its syntax.
	if ref, ok := typ.(*RefType); ok && ref != nil {
		_, _, integer := smtIntWidthSign(ref.Elem)
		return integer
	}
	return false
}

func (tr *smtTranslator) samePointerIdentityExpr(left, right ast.Expr) bool {
	if tr.a.sameExprText(left, right) {
		return true
	}
	l, lok := stripOptimizationParens(left).(*ast.AddrOfExpr)
	r, rok := stripOptimizationParens(right).(*ast.AddrOfExpr)
	return lok && rok && l != nil && r != nil && tr.a.sameExprText(l.Operand, r.Operand)
}
