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
