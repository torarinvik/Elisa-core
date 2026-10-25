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
	if !smtBooleanArrayType(tr.arrayReadOperandType(n.Object)) {
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

// arrayReadOperandType keeps pre-analysis caller lookup separate from analyzed
// callee types. Synthetic callee reads never borrow caller lexical bindings.
func (tr *smtTranslator) arrayReadOperandType(object ast.Expr) Type {
	if tr == nil || tr.a == nil || object == nil {
		return nil
	}
	typ := tr.a.exprType(object)
	if typ == nil {
		typ = tr.a.exprType(stripOptimizationParens(object))
	}
	if typ == nil && tr.calleeBodyDepth > 0 {
		return nil
	}
	if typ == nil {
		typ = tr.a.smtOperandType(object)
	}
	return typ
}

func smtBooleanArrayType(typ Type) bool {
	switch t := stripRefForBounds(typ).(type) {
	case *ArrayType:
		return t != nil && IsBoolType(t.Elem)
	case *DArrayType:
		return t != nil && IsBoolType(t.Elem)
	}
	return false
}
