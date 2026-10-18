package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Content equality of two views (`text[a:b] == needle`). The runtime (string_views_eq) answers
// true exactly when both lengths are non-negative, the lengths are equal, and the bytes are equal.
// A view whose bytes the translator models as an array (an immutable sview parameter under the
// read-only gates, or a slice of one) is described by (array, first index, length): a parameter
// is (arr, 0, len), and a slice `v[s:e]` is (arr, lo, slice length) with lo the clamped start that
// string_view_slice computes, so byte k of the slice is arr[lo + k]. The equality is then
//
//	len1 >= 0 ∧ len2 >= 0 ∧ len1 = len2 ∧ ∀k ∈ [0, len1). arr1[lo1+k] = arr2[lo2+k]
//
// which is the runtime's answer on every input. Any operand outside this shape declines.
func (tr *smtTranslator) sviewContentEqualityTerm(n *ast.BinaryExpr, env map[string]string) (string, bool) {
	if n == nil || (n.Op != lexer.TOKEN_EQEQ && n.Op != lexer.TOKEN_BANGEQ) || n.LoweredCall != nil {
		return "", false
	}
	// The empty string has no byte comparisons: equality depends only on the current header.
	// In particular, this needs no byte-array model or immutable-backing assumption.
	value := n.Left
	empty := false
	if literal, ok := stripOptimizationParens(n.Right).(*ast.StringLit); ok && literal.Value == "" {
		empty = true
	} else if literal, ok := stripOptimizationParens(n.Left).(*ast.StringLit); ok && literal.Value == "" {
		value, empty = n.Right, true
	}
	_, viewValue := stripRefForBounds(tr.a.exprTypes[value]).(*SViewType)
	if empty && viewValue {
		length, ok := tr.termEnv(&ast.FieldExpr{Position: value.Pos(), Object: value, Field: "len"}, env)
		if ok {
			eq := "(= " + length + " 0)"
			// String dispatch uses literal identity terms. Keep its empty identity
			// consistent with this header-only equality when both models are available.
			// This is a top-level auxiliary fact, so never emit a quantified binder
			// from either term outside its quantifier. In that case, omitting the
			// optional correlation only loses precision.
			if identity, ok := tr.stringIDTerm(value, env); ok {
				emptyID := smtInt(tr.classifierStringLiteralInt(""))
				correlation := "(= (= " + identity + " " + emptyID + ") " + eq + ")"
				if !smtTermMentionsQuantifierBinder(correlation) {
					tr.auxDecls = append(tr.auxDecls, "(assert "+correlation+")\n")
				}
			}
			if n.Op == lexer.TOKEN_BANGEQ {
				eq = "(not " + eq + ")"
			}
			return eq, true
		}
	}
	if _, ok := stripRefForBounds(tr.a.exprTypes[n.Left]).(*SViewType); !ok {
		return "", false
	}
	if _, ok := stripRefForBounds(tr.a.exprTypes[n.Right]).(*SViewType); !ok {
		return "", false
	}
	arrL, loL, lenL, ok := tr.sviewContentParts(n.Left, env)
	if !ok {
		return "", false
	}
	arrR, loR, lenR, ok := tr.sviewContentParts(n.Right, env)
	if !ok {
		return "", false
	}
	k := "q_sveq_" + strconv.Itoa(tr.sviewArgCount)
	tr.sviewArgCount++
	selL := "(select " + arrL + " (+ " + loL + " " + k + "))"
	selR := "(select " + arrR + " (+ " + loR + " " + k + "))"
	bytes := "(forall ((" + k + " Int)) (! (=> (and (<= 0 " + k + ") (< " + k + " " + lenL + ")) (= " + selL + " " + selR + ")) :pattern (" + selL + ")))"
	eq := "(and (>= " + lenL + " 0) (>= " + lenR + " 0) (= " + lenL + " " + lenR + ") " + bytes + ")"
	if n.Op == lexer.TOKEN_BANGEQ {
		return "(not " + eq + ")", true
	}
	return eq, true
}

func (tr *smtTranslator) sviewContentParts(expr ast.Expr, env map[string]string) (arr, lo, length string, ok bool) {
	e := stripOptimizationParens(expr)
	if id, isIdent := e.(*ast.Ident); isIdent && id != nil {
		if !tr.envBindsViewRoot(id, env) && tr.calleeBodyDepth == 0 {
			if init, isSlice := tr.a.immutableSliceLocalInit(id); isSlice {
				e = init
			}
		}
	}
	if base, k, ok := tr.containerViewIndex(e, env); ok {
		arr, lo, length := tr.containerViewElement(base, k)
		return arr, lo, length, true
	}
	slice, isSlice := e.(*ast.SliceExpr)
	if !isSlice {
		switch e.(type) {
		case *ast.Ident, *ast.FieldExpr:
		default:
			return "", "", "", false
		}
		a, ok := tr.arrayTermEnv(e, env)
		if !ok {
			return "", "", "", false
		}
		l, ok := tr.termEnv(&ast.FieldExpr{Position: e.Pos(), Object: e, Field: "len"}, env)
		if !ok {
			return "", "", "", false
		}
		return a, "0", l, true
	}
	if slice == nil || slice.Start == nil || slice.End == nil {
		return "", "", "", false
	}
	if _, ok := stripRefForBounds(tr.a.exprTypes[slice.Object]).(*SViewType); !ok {
		return "", "", "", false
	}
	base := stripOptimizationParens(slice.Object)
	switch base.(type) {
	case *ast.Ident, *ast.FieldExpr:
	default:
		return "", "", "", false
	}
	a, ok := tr.arrayTermEnv(base, env)
	if !ok {
		return "", "", "", false
	}
	baseLen, ok := tr.termEnv(&ast.FieldExpr{Position: base.Pos(), Object: base, Field: "len"}, env)
	if !ok {
		return "", "", "", false
	}
	start, ok := tr.sviewSliceBoundI64(slice.Start, env)
	if !ok {
		return "", "", "", false
	}
	l, ok := tr.sviewSliceLengthTerm(slice, env)
	if !ok {
		return "", "", "", false
	}
	lo0 := "(ite (> " + start + " 0) " + start + " 0)"
	lo = "(ite (> " + lo0 + " " + baseLen + ") " + baseLen + " " + lo0 + ")"
	return a, lo, l, true
}
