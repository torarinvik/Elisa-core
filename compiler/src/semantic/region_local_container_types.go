package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
	"strings"
)

// isSviewDarrayType reports `darray[sview]`, through a reference.
func isSviewDarrayType(t Type) bool {
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	darray, ok := t.(*DArrayType)
	if !ok || darray == nil {
		return false
	}
	_, isView := darray.Elem.(*SViewType)
	return isView
}

// containerElementsHoldOnlyViews reports a darray (through a reference) whose elements borrow
// only through sviews: every reachable component is an sview, a scalar, or an inline aggregate of
// them (no reference, view, cstr, nested container or opaque type). Such an element can view
// bytes, but cannot point at another container's storage or share its buffer, so a
// `darray[sview]` or non-byte scalar container can hand it only its ELEMENTS.
func containerElementsHoldOnlyViews(t Type) bool {
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	darray, ok := t.(*DArrayType)
	if !ok || darray == nil || darray.Elem == nil {
		return false
	}
	return typeComponentsViewOnly(darray.Elem)
}

// typeComponentsViewOnly reports a type whose every component is a view or a plain scalar —
// nothing that owns region storage. Anything opaque is not view-only.
func typeComponentsViewOnly(t Type) bool {
	if t == nil {
		return false
	}
	components, opaque := []Type{}, false
	collectReturnBorrowComponents(StripAggregateStateType(t), false, &components, &opaque, map[Type]bool{})
	if opaque {
		return false
	}
	for _, component := range components {
		switch c := component.(type) {
		case *BuiltinType:
			switch c.Name {
			case "u8", "u16", "u32", "u64", "usize", "i8", "i16", "i32", "i64", "isize", "f32", "f64", "bool", "char":
			default:
				return false
			}
		case *SViewType, *BitIntType, *ConstEnumType, *BitGroupType, *StructType, *EnumType, *TupleType, *ArrayType, *OptionalType, *ErrorUnionType:
		default:
			return false
		}
	}
	return true
}

// isNonByteScalarContainerType reports a darray or fixed array (through a reference) whose
// element type holds no byte anywhere (no u8/i8 scalar, array or nested darray of them): the
// container's own storage holds no bytes a `darray[sview]` element could view, so what a callee
// can hand a filled `darray[sview]` from it is its ELEMENTS, never its storage. Anything opaque
// (a type parameter, an unresolved type) is not exempt.
func isNonByteScalarContainerType(t Type) bool {
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	var elem Type
	switch c := t.(type) {
	case *DArrayType:
		if c == nil {
			return false
		}
		elem = c.Elem
	case *ArrayType:
		if c == nil {
			return false
		}
		elem = c.Elem
	default:
		return false
	}
	if elem == nil {
		return false
	}
	if enum, ok := StripAggregateStateType(elem).(*EnumType); ok && enum != nil && enum.Packed {
		// A packed (handle) enum element is an index into its tree store: the container's own
		// buffer holds only those indices, never bytes an sview could view.
		return true
	}
	components, opaque := []Type{}, false
	collectReturnBorrowComponents(StripAggregateStateType(elem), false, &components, &opaque, map[Type]bool{})
	if opaque {
		return false
	}
	for _, component := range components {
		switch c := component.(type) {
		case *BuiltinType:
			switch c.Name {
			case "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "bool":
			default:
				return false
			}
		case *CStrType:
			return false
		case *OptionalType, *ErrorUnionType:
		}
	}
	return true
}

// summariesOff reports whether the current body's fill summary must not be recorded: a quiet
// pass publishes summaries only when it is the on-demand pass for exactly this function.
func (a *Analyzer) summariesOff() bool {
	return a.suppressDiagnostics && a.provisionalSummaryFn != a.currentFuncDecl
}

// ensureProvisionalSummaries analyzes a callee that a caller reaches before the declaration-order
// pass does, quietly, so its fill and return-element summaries exist at the call site. Without
// it a later-declared callee is judged by the coarse fallbacks, which over-report. The real pass
// still runs later and republishes the same summaries with diagnostics on. A callee on a call
// cycle is solved by summaryConvergence instead (region_summary_convergence.go).
func (a *Analyzer) ensureProvisionalSummaries(decl *ast.FuncDecl) {
	if a == nil || decl == nil || a.ambientFillAnalyzed[decl] || (a.suppressDiagnostics && a.provisionalSummaryFn == nil) {
		return
	}
	if a.funcAnalysisActive[decl] {
		a.assumeSummaryForActive(decl)
		return
	}
	if !a.summaryAnalyzable(decl) {
		return
	}
	// Only a callee whose summary can feed a store or return check is worth a second analysis.
	if !a.summaryRelevant(decl) {
		return
	}
	if a.convergeDepth == 0 && a.funcIsCyclic(decl) {
		a.convergeSummaries(decl)
		return
	}
	a.analyzeQuietly(decl)
}

func (a *Analyzer) summaryAnalyzable(decl *ast.FuncDecl) bool {
	if len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 || decl.IsContract || decl.Body == nil {
		return false
	}
	if _, ok := a.symbolForFuncDecl(decl); !ok {
		return false
	}
	return a.funcDeclSymbols[decl] != nil
}

// analyzeQuietly runs decl's body analysis with diagnostics off, publishing its summaries.
func (a *Analyzer) analyzeQuietly(decl *ast.FuncDecl) {
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return
	}
	if a.convergeDepth > 0 {
		a.convMembers[decl] = true
	}
	savedNamespace, savedUsings := a.currentNamespace, a.currentUsings
	savedSuppress, savedOpt, savedProvisional := a.suppressDiagnostics, a.suppressOptimizationFacts, a.provisionalSummaryFn
	savedStatic := a.staticContextDepth
	if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
		if idx := strings.LastIndex(fnType.Name, "."); idx >= 0 {
			a.currentNamespace = fnType.Name[:idx]
		} else {
			a.currentNamespace = ""
		}
	}
	a.currentUsings = append([]string(nil), a.declarationUsings(decl)...)
	// Under parallel body analysis a declaration owned by another worker is analyzed
	// on this worker's private pristine copy; its summaries are published under decl.
	target, publish := a.quietAnalysisTarget(decl)
	a.suppressDiagnostics, a.suppressOptimizationFacts, a.provisionalSummaryFn = true, true, target
	a.staticContextDepth = 0
	a.addProofReportOrigin(target)
	restoreProofReport := a.beginProofReportProbe()
	defer restoreProofReport.restore()
	a.analyzeFuncWithTypeArgs(target, nil)
	restoreProofReport.restore()
	a.currentNamespace, a.currentUsings = savedNamespace, savedUsings
	a.suppressDiagnostics, a.suppressOptimizationFacts, a.provisionalSummaryFn = savedSuppress, savedOpt, savedProvisional
	a.staticContextDepth = savedStatic
	walkFunctionReturns(target.Body, func(ret *ast.ReturnStmt) {
		delete(a.returnElementStmtStates, ret)
		a.clearReturnElementStmtUnknown(ret)
	})
	publish()
}

// headerOnlyDarrayElemTypeName reports a `darray[E]` (through a reference) whose element E is
// `sview` or a non-byte scalar, returning E's name.
func headerOnlyDarrayElemTypeName(t Type) (string, bool) {
	elem, ok := darrayElemType(t)
	if !ok {
		return "", false
	}
	switch e := StripAggregateStateType(elem).(type) {
	case *SViewType:
		return "sview", true
	case *BuiltinType:
		switch e.Name {
		case "bool", "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "char":
			return e.Name, true
		}
	}
	return "", false
}

// darrayElemType returns the element type of a `darray[E]` (through a reference).
func darrayElemType(t Type) (Type, bool) {
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	darray, ok := t.(*DArrayType)
	if !ok || darray == nil || darray.Elem == nil {
		return nil, false
	}
	return darray.Elem, true
}

// predictedUFCSScanCall returns the scan's predictor for receiver-form calls: `recv.f(args)`
// whose name has exactly one free UFCS function is judged as `f(recv, args)`. Predictions are
// memoized per call so the synthetic call is built once.
func (a *Analyzer) predictedUFCSScanCall() func(call *ast.CallExpr) *ast.CallExpr {
	memo := map[*ast.CallExpr]*ast.CallExpr{}
	return func(call *ast.CallExpr) *ast.CallExpr {
		if call == nil {
			return nil
		}
		if rewritten, seen := memo[call]; seen {
			return rewritten
		}
		var rewritten *ast.CallExpr
		if fieldExpr, ok := call.Func.(*ast.FieldExpr); ok && fieldExpr != nil && fieldExpr.Object != nil && len(a.ufcsFunctionsByName[fieldExpr.Field]) == 1 {
			args := make([]ast.Expr, 0, len(call.Args)+1)
			args = append(args, fieldExpr.Object)
			args = append(args, call.Args...)
			rewritten = &ast.CallExpr{Position: call.Position, Func: &ast.Ident{Position: fieldExpr.Position, Name: fieldExpr.Field}, Args: args}
		}
		memo[call] = rewritten
		return rewritten
	}
}

var elemStateTrace = os.Getenv("ELISA_ELEMSTATE_TRACE") != ""

// traceElemState (trace only): a local's tracked element state gained a local-region dependency.
func (a *Analyzer) traceElemState(sym *Symbol, site int) {
	if !elemStateTrace || sym == nil || a.currentElementStates == nil {
		return
	}
	names := liveLocalRegionDependencyNames(a.currentElementStates[sym])
	if len(names) == 0 {
		return
	}
	fn := ""
	if a.currentFuncDecl != nil {
		fn = a.currentFuncDecl.Name
	}
	fmt.Fprintf(os.Stderr, "ELEMSTATE %s %s site=%d deps=%v\n", fn, sym.Name, site, names)
}
