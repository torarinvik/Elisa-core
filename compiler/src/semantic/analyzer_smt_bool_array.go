package semantic

import "elisacore/src/ast"

// Boolean arrays share the existing Int-valued array identity/substitution model.
// Actual false/true elements have encodings 0/1; reading nonzero as true allows
// additional models without adding facts, so UNSAT remains sound. Stores whose
// values cannot translate as integer terms continue to decline.
func (tr *smtTranslator) booleanArrayIndexTerm(n *ast.IndexExpr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || n == nil || n.Index2 != nil || n.Fallback != nil || n.LegacyElseFallback || n.AsSpecialize != nil || n.AsOverlayCall != nil || n.AsBuiltinDictGet != nil {
		return "", false
	}
	typ := tr.a.exprTypes[n.Object]
	if typ == nil {
		typ = tr.a.exprTypes[stripOptimizationParens(n.Object)]
	}
	// A synthetic callee projection has no authority to borrow a same-named
	// caller binding's type. Original analyzed callee nodes retain their types.
	if typ == nil && tr.calleeBodyDepth > 0 {
		return "", false
	}
	if typ == nil {
		typ = tr.a.smtOperandType(n.Object)
	}
	typ = stripRefForBounds(typ)
	var elem Type
	switch t := typ.(type) {
	case *ArrayType:
		if t != nil {
			elem = t.Elem
		}
	case *DArrayType:
		if t != nil {
			elem = t.Elem
		}
	}
	if !IsBoolType(elem) {
		return "", false
	}
	array, ok := tr.arrayTermEnv(n.Object, env)
	if !ok {
		return "", false
	}
	index, ok := tr.termEnv(n.Index, env)
	if !ok {
		return "", false
	}
	return "(not (= (select " + array + " " + index + ") 0))", true
}
