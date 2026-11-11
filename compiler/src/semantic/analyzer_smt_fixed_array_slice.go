package semantic

import "elisacore/src/ast"

// fixedArraySliceLengthTerm mirrors the unsigned clamp used to lower slices
// of fixed arrays. Model only bounds already typed as usize, so a conversion
// at the slice boundary cannot silently change the translated value.
func (tr *smtTranslator) fixedArraySliceLengthTerm(expr ast.Expr, env map[string]string) (string, bool) {
	if tr.a == nil {
		return "", false
	}
	expr = stripOptimizationParens(expr)
	if id, ok := expr.(*ast.Ident); ok {
		if tr.envBindsRoot(id, env) || tr.calleeBodyDepth != 0 {
			return "", false
		}
		init, ok := tr.a.immutableLocalSliceInit(id, true)
		if !ok {
			return "", false
		}
		expr = init
	}
	slice, ok := expr.(*ast.SliceExpr)
	if !ok || slice == nil || slice.Start == nil || slice.End == nil {
		return "", false
	}
	array, ok := stripRefForBounds(tr.a.exprType(slice.Object)).(*ArrayType)
	if !ok || !array.HasConstSize || array.ConstSize < 0 {
		return "", false
	}
	if _, ok := stripRefForBounds(tr.a.exprType(slice)).(*ViewType); !ok {
		return "", false
	}
	for _, bound := range []ast.Expr{slice.Start, slice.End} {
		builtin, ok := stripRefForBounds(tr.a.exprType(bound)).(*BuiltinType)
		if !ok || builtin.Name != "usize" {
			return "", false
		}
	}
	start, ok := tr.termEnv(slice.Start, env)
	if !ok {
		return "", false
	}
	end, ok := tr.termEnv(slice.End, env)
	if !ok {
		return "", false
	}
	length := smtInt(array.ConstSize)
	clamp := func(bound string) string {
		return "(ite (> " + bound + " " + length + ") " + length + " " + bound + ")"
	}
	lo, hi := clamp(start), clamp(end)
	return "(ite (< " + hi + " " + lo + ") 0 (- " + hi + " " + lo + "))", true
}
