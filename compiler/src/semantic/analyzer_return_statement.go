package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// knownValueType is supplied only by the validated incremental primitive-body path.
// All return/escape/drop/provenance checks still run on the current invocation.
func (a *Analyzer) analyzeReturnStatement(n *ast.ReturnStmt, knownValueType Type) {
	// docs/85 brick 2 (B): check `ensures <param> is Law` postconditions on every return path,
	// including value-less returns in a void function, before the value-specific handling below.
	a.dischargeEnsuresRefinements(n)
	if n.Value == nil {
		// docs/126 §2: a bare `return` is an exit edge too. (The value-returning
		// path notes its edge further down, after the returned value has had its
		// own chance to consume — `return move f` must suppress f's drop.)
		a.noteImplicitDropEdge(DropEdgeReturn, n.Pos())
		a.reportUnconsumedProtocolValuesOnExit()
		if currentUnion, ok := a.currentReturn.(*ErrorUnionType); ok {
			if !SameType(currentUnion.Value, a.namedTypes["void"]) {
				a.errorf(n.Pos(), "return value required for %s", a.currentReturn)
			}
			a.validateCurrentFuncPoststates()
			return
		}
		if a.currentReturn != nil && !SameType(a.currentReturn, a.namedTypes["void"]) {
			a.errorf(n.Pos(), "return value required for %s", a.currentReturn)
		}
		a.validateCurrentFuncPoststates()
		return
	}
	a.noteReturnElementState(n)
	valueType := knownValueType
	if valueType == nil {
		valueType = a.analyzeValueExpr(n.Value, a.currentReturn)
	}
	if a.currentReturn == nil {
		a.errorf(n.Pos(), "unexpected return value")
		return
	}
	a.noteImplicitIntConversion(n.Value, valueType, a.currentReturn)
	a.dischargeReturnRefinements(n)
	a.dischargeReturnWhereRefinement(n)
	a.dischargeEnsureBooleans(n)
	a.checkLocalArenaEscape(n.Value, valueType, "return")
	a.checkReturnBorrowEscapesLocal(n.Value, valueType)
	a.checkGlobalStorageReturnEscape(n.Value)
	// docs/128: `-> T @source` is a store into the program-lived region.
	a.checkProgramRegionSlotStore(n.Value, a.currentReturn, valueType)
	if !a.checkTupleReturnRegionEscapes(n.Value, valueType, a.currentReturn) {
		a.checkReturnRegionContainerEscape(n.Value, valueType)
		a.checkRegionAggregateReturnEscape(n.Value, valueType)
		a.checkRegionParamReturnEscape(n.Value, valueType)
	}
	// Approach A: `return move <region>` transfers an owned region to the
	// caller. Consume it locally (discharging the must-consume obligation)
	// and mark the function as returning an owned region. All value-returns
	// must agree, or the caller's inherited obligation would be unsound.
	if ownerSym, isOwned := a.returnedRegionOwner(n.Value); isOwned {
		if a.currentFuncSawPlainValueReturn {
			a.errorf(n.Pos(), "this path returns an owned region but another returns a plain value; every value-returning path must transfer the region with `return move <region>` or none may")
		}
		a.recordAffineConsumption(affineValueKey{Root: ownerSym}, "return")
		if a.currentFuncType != nil {
			a.currentFuncType.ReturnsOwnedRegion = true
		}
	} else {
		if a.currentFuncType != nil && a.currentFuncType.ReturnsOwnedRegion {
			a.errorf(n.Pos(), "this path returns a plain value but another returns an owned region; every value-returning path must transfer the region with `return move <region>` or none may")
		}
		a.currentFuncSawPlainValueReturn = true
	}
	if refState, ok := a.regionRefStateForExpr(n.Value); ok {
		if region, _, ok := firstLiveRegionDependency(refState); ok && region != nil {
			// docs/75 step 1 (detection): a value built with `new[auto]` carries a
			// synthesized inferred region (`__auto_*`) local to this function. Returning
			// it is the signature of a *region-polymorphic* function — the region will be
			// threaded from the caller so the result outlives the call. Record that
			// classification now. The escape error still fires until threading (steps
			// 2-3) makes the return sound; an explicitly-named local region (`in
			// scratch:`) is never region-polymorphic and always errors.
			regionPoly := a.currentFuncType != nil && a.currentFuncType.RegionPolymorphic
			prepassPoly := regionPoly
			if a.isCompilerGeneratedAutoRegion(region.Name) && a.currentFuncType != nil {
				// Belt-and-suspenders: the pre-pass already set this from a syntactic scan, but
				// keep the flow-based confirmation so the classification never under-reports.
				a.currentFuncType.RegionPolymorphic = true
				// Only the pre-pass makes the backend thread the region. A return that only this
				// walk classifies stays unthreaded, so a REFERENCE it returns points into a frame
				// that is gone: `return firstp(xs)` over a local darray. A scalar carries no
				// reference out, so the suppression stands for it.
				if !prepassPoly && !returnTypeIsScalarValue(a.currentReturn) && a.typeCarriesBorrowedStorage(valueType, map[Type]bool{}) {
					regionPoly = false
				} else {
					regionPoly = true
				}
				// A returned closure captures its locals' container headers by value, but the
				// hidden region parameter is never threaded through a function-typed result:
				// the captured backing is freed at return no matter what the caller supplies.
				// The same holds for a closure nested in an aggregate result (`return (1, fn() => r[0])`).
				if valueTypeCarriesClosure(valueType, map[Type]bool{}) {
					regionPoly = false
				}
			}
			// docs/75: in a region-polymorphic function the synthesized `__auto_*` region is
			// threaded from the caller (the hidden `__region_auto` Arena& param), so the result
			// outlives the call — no escape. Suppress the error. Explicitly-named local regions
			// are never region-polymorphic and still error.
			//
			// That last sentence is the rule, and the suppression used to break it. `regionPoly`
			// is a property of the FUNCTION; the region being returned here is a property of THIS
			// return. A function earns the region-polymorphic flag from any `__auto_*` return, and
			// once it had the flag EVERY escaping return went quiet — including one that depends on
			// an explicitly-named `region scratch(...):`, whose storage is freed at block exit no
			// matter what the caller threads. `return box.values` from inside an `if`, with a plain
			// `return []` on the other path, compiled clean and handed the caller freed memory:
			// the empty-literal return set the flag, and the field return rode its suppression out.
			// Returning `values` directly only looked fine because a second, name-based check
			// caught it — reach through one field and nothing was left. Suppress only when the
			// region is one the CALLER owns.
			//
			// Caller-owned means one of two things, and NEITHER is a region this function
			// frees: a declared `@r` region param (`lookupRegionParam` — note that
			// `lookupRegionState` is NOT the test, it resolves a region param too), or the
			// synthesized `__auto_*` region, which docs/75 threads in through the hidden
			// `__region_auto` Arena&. Anything else that is a LIVE region owned by a scope of
			// ours — a `region NAME(...):` block, or a region taken over by `return move` —
			// is freed before the caller can read the result, and returning it dangles.
			escapesLocalRegionBlock := false
			if !a.lookupRegionParam(region.Name) && !a.isCompilerGeneratedAutoRegion(region.Name) {
				if sym, state := a.lookupRegionState(region.Name); sym != nil && !state.Destroyed && state.DeclScope != nil {
					escapesLocalRegionBlock = true
				}
			}
			// An explicit @r contract is also a caller-owned region, even when the
			// function is not using inferred __auto_* region polymorphism. `new[r]`
			// returns are safe here because the caller supplies the arena; named
			// block-local regions still fail closed above.
			if (!regionPoly && !a.lookupRegionParam(region.Name)) || escapesLocalRegionBlock {
				if _, isRef := valueType.(*RefType); isRef {
					a.errorf(n.Pos(), localRegionEscapeMessage("reference", region.Name))
				} else {
					a.errorf(n.Pos(), localRegionEscapeMessage("value", region.Name))
				}
			}
		}
		if summary, ok := abstractParamOnlyRegionRefState(refState); ok {
			if merged, ok := mergeRegionRefStates(a.currentReturnProvenance, summary); ok {
				a.currentReturnProvenance = merged
			} else if !hasRegionProvenance(a.currentReturnProvenance) {
				a.currentReturnProvenance = cloneRegionRefState(summary)
			}
		}
		// The first expression-fact pass runs as analyzeExpr returns, before this
		// return-provenance query can lazily infer a recursive callee's summary.
		// Refresh the returned expression child-first so cold analysis records the
		// same nested-call provenance facts as retained analysis.
		a.refreshReturnedExprOptimizationFacts(n.Value, valueType)
	}
	a.inReturnBorrowCapture = true
	ownerState, ownerStateOK := a.borrowedOwnerRefStateForExpr(n.Value)
	a.inReturnBorrowCapture = false
	if ownerStateOK {
		if summary, ok := abstractParamOnlyBorrowedOwnerRefSummary(ownerState); ok {
			if merged, ok := mergeBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs, summary); ok {
				a.currentReturnBorrowedOwnerRefs = merged
			} else if !hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
				a.currentReturnBorrowedOwnerRefs = summary
			}
		}
	}
	a.recordFreshReturnBindings(valueType)
	expectedReturn := a.matchReturnType(valueType)
	if !AssignableTo(expectedReturn, valueType) {
		a.errorf(n.Pos(), "return type expects %s, got %s", expectedReturn, valueType)
		a.reportShapeMismatchNotes(n.Pos(), expectedReturn, valueType)
	}
	a.validateCurrentFuncPoststatesForReturnValue(n.Value)
	a.consumeAffineValueExpr(n.Value, expectedReturn, "return")
	// docs/126 §2: an early `return` is an exit edge like any other — every drop
	// local still holding a value dies here. Recorded AFTER the return value's own
	// consumption so `return move f` correctly suppresses f's drop.
	a.noteImplicitDropEdge(DropEdgeReturn, n.Pos())
	// A `return` leaves every scope: a must-consume value still live here leaks on
	// this path even when the fall-through path consumes it.
	a.reportUnconsumedProtocolValuesOnExit()
}

// refreshReturnedExprOptimizationFacts re-records only already-typed expression
// nodes in a returned value, after provenance queries may have filled callee
// summaries. Child-first order lets aggregate children observe fresh facts before
// their parent. Non-expression syntax nodes are boundaries: their children may
// use arm, block, lambda, or declaration-local bindings that are no longer active.
func (a *Analyzer) refreshReturnedExprOptimizationFacts(root ast.Expr, rootType Type) {
	if a == nil || root == nil || a.suppressOptimizationFacts {
		return
	}
	if !typeMayCarryRegionProvenanceForOptimization(rootType) {
		a.recordExprOptimizationFacts(root, rootType)
		return
	}

	ordered := make([]ast.Expr, 0, 8)
	seenExpr := make(map[ast.Expr]bool)
	seenNode := make(map[uintptr]bool)
	var visitValue func(reflect.Value)
	var visitExpr func(ast.Expr)
	visitExpr = func(expr ast.Expr) {
		if expr == nil || seenExpr[expr] {
			return
		}
		seenExpr[expr] = true
		if returnedExprFactScopeBoundary(expr) {
			ordered = append(ordered, expr)
			return
		}
		value := reflect.ValueOf(expr)
		if value.Kind() == reflect.Ptr && !value.IsNil() {
			ptr := value.Pointer()
			if seenNode[ptr] {
				return
			}
			seenNode[ptr] = true
			visitValue(value.Elem())
		}
		ordered = append(ordered, expr)
	}
	visitValue = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		switch value.Kind() {
		case reflect.Interface:
			if value.IsNil() || !value.CanInterface() {
				return
			}
			if expr, ok := value.Interface().(ast.Expr); ok {
				visitExpr(expr)
			}
		case reflect.Ptr:
			if value.IsNil() {
				return
			}
			if expr, ok := value.Interface().(ast.Expr); ok {
				visitExpr(expr)
				return
			}
			if value.Type().Elem().PkgPath() != astPkgPath {
				return
			}
			if _, ok := value.Interface().(ast.Node); ok {
				return
			}
			ptr := value.Pointer()
			if seenNode[ptr] {
				return
			}
			seenNode[ptr] = true
			visitValue(value.Elem())
		case reflect.Struct:
			if value.Type().PkgPath() != astPkgPath {
				return
			}
			if value.CanAddr() {
				if node, ok := value.Addr().Interface().(ast.Node); ok {
					if _, isExpr := node.(ast.Expr); !isExpr {
						return
					}
				}
			}
			for _, index := range syntaxWalkFields(value.Type()) {
				field := value.Field(index)
				if field.CanInterface() {
					visitValue(field)
				}
			}
		case reflect.Slice, reflect.Array:
			for index := 0; index < value.Len(); index++ {
				visitValue(value.Index(index))
			}
		}
	}

	visitExpr(root)
	for _, expr := range ordered {
		result, ok := Type(nil), false
		if expr == root {
			result, ok = rootType, rootType != nil
		} else {
			result, ok = a.lookupExprType(expr)
		}
		if ok {
			a.recordExprOptimizationFacts(expr, result)
		}
	}
}

func returnedExprFactScopeBoundary(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.LambdaExpr, *ast.ExprBlock, *ast.ListComprehensionExpr, *ast.QueryExpr,
		*ast.FoldExpr, *ast.MatchExpr, *ast.CatchExpr, *ast.MachineFromExpr,
		*ast.QuantifierExpr, *ast.GetExpr, *ast.TryExpr, *ast.UnwrapElseExpr,
		*ast.OptionalBindExpr:
		return true
	default:
		return false
	}
}
