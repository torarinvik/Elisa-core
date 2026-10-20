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
	if !tr.sviewEqualityOperand(n.Left) || !tr.sviewEqualityOperand(n.Right) {
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

// sviewEqualityOperand admits a typed view, or a ternary or pure-function call whose view
// parts sviewContentParts derives structurally (a merged return value carries no type).
func (tr *smtTranslator) sviewEqualityOperand(expr ast.Expr) bool {
	if _, ok := stripRefForBounds(tr.a.exprTypes[expr]).(*SViewType); ok {
		return true
	}
	if tr.a.exprTypes[expr] == nil {
		if _, ok := stripRefForBounds(tr.placeStaticType(expr)).(*SViewType); ok {
			return true
		}
	}
	switch e := stripOptimizationParens(expr).(type) {
	case *ast.TernaryExpr:
		return e != nil && tr.a.ternaryBranchType(e) != nil
	case *ast.CallExpr:
		decl, ok := tr.sviewEquationCallee(e)
		return ok && decl != nil
	}
	return false
}

// sviewEquationCallee resolves a direct call to a pure, total function returning a view: its
// single return expression is its value, so the call's parts are the body's parts.
func (tr *smtTranslator) sviewEquationCallee(call *ast.CallExpr) (*ast.FuncDecl, bool) {
	if call == nil || call.Safe || call.SafeReceiver != nil {
		return nil, false
	}
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	decl = tr.a.readDecl(decl)
	if !direct || decl == nil || len(proofCallArgs(call)) != len(decl.Params) {
		return nil, false
	}
	sym, ok := tr.a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return nil, false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil {
		return nil, false
	}
	if _, view := fn.Return.(*SViewType); !view {
		return nil, false
	}
	if !tr.a.viewEquationEligible(decl) {
		return nil, false
	}
	return decl, true
}

// sviewCallContentParts is the defining equation of a pure view-returning call, read as view
// parts: the callee body under the call's argument environment, bounded like defining equations.
func (tr *smtTranslator) sviewCallContentParts(call *ast.CallExpr, env map[string]string) (arr, lo, length string, ok bool) {
	decl, ok := tr.sviewEquationCallee(call)
	if !ok {
		return "", "", "", false
	}
	if tr.a.isRecursiveProofCall(decl) || tr.callEqDeclDepth[decl] >= tr.callEqMaxDeclDepth {
		return "", "", "", false
	}
	body, ok := pureReturnBody(decl)
	if !ok {
		return "", "", "", false
	}
	args := proofCallArgs(call)
	subst := make(map[string]ast.Expr, len(decl.Params))
	for i, param := range decl.Params {
		if args[i] == nil {
			return "", "", "", false
		}
		subst[param.Name] = args[i]
	}
	priorBinding := tr.bindingCallee
	tr.bindingCallee = decl
	calleeEnv, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, env)
	tr.bindingCallee = priorBinding
	if !ok {
		return "", "", "", false
	}
	// The body defines the call only where the callee's requires hold; elsewhere the call is an
	// opaque function of its arguments.
	guard := ""
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, req, calleeEnv)
		if !ok {
			return "", "", "", false
		}
		if guard == "" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	var opaqueArr, opaqueLo, opaqueLen string
	if guard != "" {
		if opaqueArr, opaqueLo, opaqueLen, ok = tr.sviewCallUFTerms(call, env); !ok {
			// Outside its requires the call is simply unknown: fresh, unconstrained parts.
			name := "@view_opaque_" + strconv.Itoa(tr.sviewArgCount)
			tr.sviewArgCount++
			opaqueArr, opaqueLo, opaqueLen = "|"+name+"$data|", "|"+name+"$lo|", "|"+name+"$len|"
			tr.auxDecls = append(tr.auxDecls,
				"(declare-const "+opaqueArr+" (Array Int Int))\n",
				"(declare-const "+opaqueLo+" Int)\n",
				"(declare-const "+opaqueLen+" Int)\n")
		}
	}
	priorDecl := tr.calleeBodyDecl
	tr.calleeBodyDecl = decl
	tr.calleeBodyDepth++
	if tr.callEqDeclDepth == nil {
		tr.callEqDeclDepth = map[*ast.FuncDecl]int{}
	}
	tr.callEqDeclDepth[decl]++
	defer func() {
		tr.callEqDeclDepth[decl]--
		tr.calleeBodyDepth--
		tr.calleeBodyDecl = priorDecl
	}()
	arr, lo, length, ok = tr.sviewContentParts(body, calleeEnv)
	if !ok || guard == "" {
		return arr, lo, length, ok
	}
	return "(ite " + guard + " " + arr + " " + opaqueArr + ")", "(ite " + guard + " " + lo + " " + opaqueLo + ")", "(ite " + guard + " " + length + " " + opaqueLen + ")", true
}

func (tr *smtTranslator) sviewContentParts(expr ast.Expr, env map[string]string) (arr, lo, length string, ok bool) {
	e := stripOptimizationParens(expr)
	switch n := e.(type) {
	case *ast.Ident:
		if n != nil {
			if data, offset, length, bound := smtIndexedSViewArgTermsFromEnv(n.Name, env); bound {
				return data, offset, length, true
			}
		}
	case *ast.TernaryExpr:
		if n == nil || n.Cond == nil || n.Value == nil || n.Alt == nil {
			return "", "", "", false
		}
		cond, ok := tr.boolTerm(n.Cond, env)
		if !ok {
			return "", "", "", false
		}
		a1, l1, n1, ok := tr.sviewContentParts(n.Value, env)
		if !ok {
			return "", "", "", false
		}
		a2, l2, n2, ok := tr.sviewContentParts(n.Alt, env)
		if !ok {
			return "", "", "", false
		}
		return "(ite " + cond + " " + a1 + " " + a2 + ")", "(ite " + cond + " " + l1 + " " + l2 + ")", "(ite " + cond + " " + n1 + " " + n2 + ")", true
	case *ast.StringLit:
		// The empty view has no bytes to compare; its backing array is irrelevant.
		if n != nil && n.Value == "" {
			return "((as const (Array Int Int)) 0)", "0", "0", true
		}
		return "", "", "", false
	case *ast.CallExpr:
		if arr, lo, length, ok := tr.sviewCallContentParts(n, env); ok {
			return arr, lo, length, true
		}
		// A call that cannot be unfolded is still a function of its arguments.
		return tr.sviewCallUFTerms(n, env)
	}
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
