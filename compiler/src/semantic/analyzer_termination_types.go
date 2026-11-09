package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) smtEquationParamType(t Type) bool {
	if ref, isRef := StripAggregateStateType(t).(*RefType); isRef && (ref.Mutable || ref.Linear) {
		param := stripRefForBounds(t)
		return param != nil && a.smtEquationParamTypeWithoutContainerRecord(param)
	}
	return a.smtEquationParamTypeWithoutContainerRecord(stripRefForBounds(t)) || a.smtEquationContainerRecordType(stripRefForBounds(t))
}

func (a *Analyzer) smtEquationParamTypeWithoutContainerRecord(t Type) bool {
	if t == nil {
		return false
	}
	if (IsNumericType(t) && !IsFloatType(t)) || IsBoolType(t) || a.smtEquationStructType(t) {
		return true
	}
	// The SMT array theory models sview[u8] as a read-only integer array. The parameter itself is
	// accepted only when its indexed reads pass pureEquationSViewIndex and the surrounding function
	// is an immutable pure-return tree.
	if _, ok := t.(*SViewType); ok {
		return true
	}
	// A container parameter enters the equation through the container theory: its length and, for
	// modeled element types, its elements, read through a stable base.
	if _, ok := StripAggregateStateType(t).(*DArrayType); ok {
		return true
	}
	return false
}

// smtEquationContainerRecordType admits a record parameter read through an immutable binding.
// It enters an equation only through the reads the translator models (a container field is a
// stable base through recordFieldContainerBase, in a frame that writes no memory); any other
// read declines the translation, never the soundness of what is translated.
func (a *Analyzer) smtEquationContainerRecordType(t Type) bool {
	st, ok := StripAggregateStateType(t).(*StructType)
	return ok && st != nil && len(st.Fields) != 0
}

func (a *Analyzer) smtEquationStructType(t Type) bool {
	st, ok := stripRefForBounds(t).(*StructType)
	if !ok || st == nil || len(st.Fields) == 0 {
		return false
	}
	for _, field := range st.Fields {
		ft := stripRefForBounds(field.Type)
		if (IsNumericType(ft) && !IsFloatType(ft)) || IsBoolType(ft) {
			continue
		}
		if !plainFrameRecordType(field.Type, map[*StructType]bool{}) {
			return false
		}
	}
	return true
}

// measureBoundedBelow reports whether the measure cannot fall below 0. An unsigned-typed measure is
// non-negative by construction (the common, zero-friction case: `decreases n` with `n: usize`).
// Otherwise the measure's affine form must bound to a non-negative lower value under the current
// facts. Without a provable floor, a strictly-decreasing measure could run forever, so we decline.
func (a *Analyzer) measureBoundedBelow(measure ast.Expr) bool {
	bounded, _ := a.measureBoundedBelowWithTier(measure)
	return bounded
}

func (a *Analyzer) measureBoundedBelowWithTier(measure ast.Expr) (bool, bool) {
	if t := a.exprType(measure); t != nil && indexTypeGuaranteedNonNegative(t) {
		return true, false
	}
	form, ok := a.affineOf(measure, a.currentScope)
	if ok {
		r := a.boundAffine(form, a.currentScope)
		if r.loKnown && r.lo >= 0 {
			return true, false
		}
	}
	// Relational preconditions can establish a lower bound that interval arithmetic
	// cannot represent (for example, `index <= text.len` proves `text.len-index >= 0`).
	// The goal is checked against the function's actual requires/assert facts; a declined
	// or unavailable solver keeps the termination proof conservative.
	bounded := &ast.BinaryExpr{
		Position: measure.Pos(),
		Op:       lexer.TOKEN_GTEQ,
		Left:     measure,
		Right:    &ast.IntLit{Position: measure.Pos(), Value: "0"},
	}
	if proven, _ := a.trySMTProveRequires(bounded, nil); proven {
		return true, true
	}
	return false, false
}

func isSMTIntWidthType(t Type) bool {
	if t == nil {
		return false
	}
	_, _, ok := smtIntWidthSign(t)
	return ok
}

func contractConds(cs []*ast.ContractStmt) []ast.Expr {
	out := make([]ast.Expr, 0, len(cs))
	for _, c := range cs {
		if c != nil {
			out = append(out, c.Cond)
		}
	}
	return out
}

// pureEquationContainerIndex admits `xs[k]` on an immutable darray parameter of the owner; its
// element is read through the container theory (a read the theory cannot model declines there).
func (a *Analyzer) pureEquationContainerIndex(index *ast.IndexExpr, owner *ast.FuncDecl) bool {
	if a == nil || index == nil || owner == nil || index.Index2 != nil || index.Fallback != nil ||
		index.LegacyElseFallback || index.AsSpecialize != nil || index.AsOverlayCall != nil || index.AsBuiltinDictGet != nil {
		return false
	}
	target := stripOptimizationParens(index.Object)
	viaField := false
	for {
		field, isField := target.(*ast.FieldExpr)
		if !isField || field == nil {
			break
		}
		if field.Safe {
			return false
		}
		viaField = true
		target = stripOptimizationParens(field.Object)
	}
	object, ok := target.(*ast.Ident)
	if !ok || object == nil || !a.equationIdentifierIsStable(object, owner) {
		return false
	}
	if viaField {
		// A container field of an immutable parameter is a stable base (recordFieldContainerBase).
		if t := a.exprType(index.Object); t != nil {
			if _, isContainer := StripAggregateStateType(stripRefForBounds(t)).(*DArrayType); !isContainer {
				return false
			}
		}
	}
	sym, ok := a.symbolForFuncDecl(owner)
	if !ok || sym == nil {
		return false
	}
	fn, ok := sym.Type.(*FuncType)
	// Trailing implicit parameters (an injected packed store) are never spelled in the body.
	if !ok || fn == nil || funcTypeExplicitParamCount(fn) != len(owner.Params) || len(fn.Params) < len(owner.Params) {
		return false
	}
	for i, param := range owner.Params {
		if param.Name != object.Name {
			continue
		}
		pt := fn.Params[i]
		if ref, isRef := pt.(*RefType); isRef && (ref.Mutable || ref.Linear) {
			return false
		}
		if viaField {
			return a.smtEquationContainerRecordType(stripRefForBounds(pt))
		}
		_, isContainer := StripAggregateStateType(stripRefForBounds(pt)).(*DArrayType)
		return isContainer
	}
	return false
}
