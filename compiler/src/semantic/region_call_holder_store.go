package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

// checkCallArgumentHolderStoreEscape: a call whose `mutable H&` parameter receives a NON-container
// holder that outlives the function (a parameter's referent, a global) may store another argument
// into one of the holder's fields. `set(h, b.as_sview())` with set doing `h.s <- s` leaves the
// caller's h holding a view of b's bytes, which live in this function's `in auto:` region and are
// freed on return. The direct form `h.s <- b.as_sview()` is rejected by the assignment's
// checkStoredRegionContainerEscape; the callee's own checks accept the store (both are its
// parameters), so the caller checks every argument the call may store, with the same rule.
// Container holders (`mutable darray[sview]&`) are checkCallArgumentRegionStoreEscape's.
func (a *Analyzer) checkCallArgumentHolderStoreEscape(call *ast.CallExpr) {
	if a == nil || call == nil || a.suppressDiagnostics || a.staticContextDepth != 0 || a.currentFuncDecl == nil {
		return
	}
	fnType := a.returnBorrowCalleeSignature(call)
	if fnType == nil {
		return
	}
	args := returnBorrowCallArgs(call)
	for index, paramType := range fnType.Params {
		if index >= len(args) || !a.returnBorrowWritableParam(paramType) {
			continue
		}
		ref, isRef := paramType.(*RefType)
		if !isRef || ref == nil || ref.Elem == nil || containsTypeParam(ref.Elem) {
			continue
		}
		if _, isStruct := StripAggregateStateType(ref.Elem).(*StructType); !isStruct {
			continue
		}
		holderType := stripRefForBounds(a.exprTypes[args[index]])
		if a.containerRegionOf(holderType) != "" || !a.typeMayHoldFrameBorrow(ref.Elem, map[Type]bool{}) {
			continue
		}
		place := returnBorrowStripAddr(args[index])
		if place == nil || !a.lvalueStorageOutlivesFunction(place) {
			continue
		}
		for other, arg := range args {
			if other == index || arg == nil {
				continue
			}
			if a.callArgNeverReaches(call, other, index) || a.callArgsKeptApart(call, index, other) {
				continue
			}
			// Only a VIEW argument (sview, cstr, view[T]) is checked: it is what a holder's view
			// field keeps pointing at the bytes. A container passed by value is a header copy whose
			// storage the callee's own checks already govern.
			argType := a.exprTypes[arg]
			if argType == nil || !isRegionBorrowedStringOrViewType(argType) {
				continue
			}
			a.checkRegionContainerEscape(arg, argType, fmt.Sprintf("store into longer-lived storage through argument %d", index+1))
		}
	}
}
