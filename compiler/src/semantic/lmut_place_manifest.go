package semantic

import "elisacore/src/ast"

// isLmutPlaceArgManifest recognizes the FIELD-PATH form of the docs/120 §8 arg-manifest:
// `report.cache <- bump(report.cache)`. It is the place analogue of isLmutArgManifest's
// identifier form, needed once a big struct is split into sub-structs that are mutated through
// `lmut` helpers. The RHS must be a void call (optionally under a `can` grant), the target a
// field path rooted at a mutable binding, and the call must pass that SAME rooted place (same
// root, same field path) as one of its arguments — possibly already auto-referenced (`&place`).
// A UFCS receiver also counts (`report.cache <- report.cache.bump()`). Like the identifier
// form, it erases to just the call, which writes through the field's address.
func (a *Analyzer) isLmutPlaceArgManifest(target, value ast.Expr, valueType Type) bool {
	if !isVoidType(valueType) {
		return false
	}
	troot, tfields, ok := exprPlacePath(target)
	if !ok || len(tfields) == 0 || !a.isMutableBinding(troot) {
		return false
	}
	call, ok := unwrapToCall(value)
	if !ok {
		return false
	}
	matches := func(arg ast.Expr) bool {
		if arg == nil {
			return false
		}
		if addr, ok := stripOptimizationParens(arg).(*ast.AddrOfExpr); ok && addr != nil {
			arg = addr.Operand
		}
		root, fields, ok := exprPlacePath(arg)
		if !ok || root != troot || len(fields) != len(tfields) {
			return false
		}
		for i := range fields {
			if fields[i] != tfields[i] {
				return false
			}
		}
		return true
	}
	for _, arg := range call.Args {
		if matches(arg) {
			return true
		}
	}
	for _, arg := range call.ResolvedArgs {
		if matches(arg) {
			return true
		}
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && matches(field.Object) {
		return true
	}
	return false
}

// lmutMutatedPlaceName renders the place a bare lmut-mutating call mutates, for the §10
// diagnostic: the dotted field path of a field argument (`report.cache`), else the root name.
func lmutMutatedPlaceName(arg ast.Expr, root string) string {
	if addr, ok := stripOptimizationParens(arg).(*ast.AddrOfExpr); ok && addr != nil {
		arg = addr.Operand
	}
	r, fields, ok := exprPlacePath(arg)
	if !ok || r != root || len(fields) == 0 {
		return root
	}
	place := r
	for _, f := range fields {
		place += "." + f
	}
	return place
}
