package semantic

import "elisacore/src/ast"

// copy[array[T, N]](src) materializes a fixed-size, stack-owned array from a
// fixed-size array source. It is the "fixed owner" half of the copy/clone
// symmetry (see docs/26): copy produces a compile-time-sized owner on the stack,
// while clone produces a runtime-sized owner in a region.
//
// Because the result lives on the stack, the size MUST be statically known.
// Copying a runtime-length source (view / darray / sview / dstr) to the stack
// would reintroduce unbounded stack growth, so it is rejected with a diagnostic
// that points at clone (which owns its bytes in a region instead).
func (a *Analyzer) copyBuiltinTargetType(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil || callSpecializedIdentName(expr) != "copy" {
		return nil, false
	}
	_, specialize, ok := callSpecializedIdent(expr.Func)
	if !ok || specialize == nil || len(specialize.TypeArgs) != 1 {
		return nil, false
	}
	targetType := a.resolveType(specialize.TypeArgs[0])
	if targetType == nil || IsInvalidType(targetType) {
		return invalidType, true
	}
	return targetType, true
}

func (a *Analyzer) analyzeCopyBuiltinCall(expr *ast.CallExpr) (Type, bool) {
	if expr == nil || callSpecializedIdentName(expr) != "copy" {
		return nil, false
	}
	targetType, ok := a.copyBuiltinTargetType(expr)
	if !ok {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "copy expects 1 argument, got %d", len(expr.Args))
		return invalidType, true
	}
	sourceType := a.analyzeExpr(expr.Args[0])
	if targetType == nil || IsInvalidType(targetType) || IsInvalidType(sourceType) {
		return invalidType, true
	}
	arrayTarget, ok := StripAggregateStateType(targetType).(*ArrayType)
	if !ok || arrayTarget == nil || !arrayTarget.HasConstSize {
		a.errorf(expr.Pos(), "copy target must be a fixed-size array[T, N] stack owner with a compile-time size; got %s", targetType)
		return invalidType, true
	}
	if !a.copyBuiltinCompatible(arrayTarget, sourceType) {
		switch StripAggregateStateType(sourceType).(type) {
		case *DArrayType, *ViewType, *SViewType, *DStrType:
			a.errorf(expr.Pos(), "copy requires a statically known size; %s has a runtime length — use clone to copy it into a region-owned darray", sourceType)
		default:
			a.errorf(expr.Pos(), "copy cannot copy %s into %s in v1", sourceType, targetType)
		}
		return invalidType, true
	}
	a.recordBuiltinHelperFuncType(expr, "copy", targetType)
	return targetType, true
}

// copyBuiltinCompatible reports whether a fixed-size array source can be copied
// element-for-element into the fixed-size target on the stack. The element type
// must be pure value data: anything that needs a region owner (a nested darray,
// dynamic string, tree, etc.) cannot live on the stack and must go through clone.
func (a *Analyzer) copyBuiltinCompatible(target *ArrayType, source Type) bool {
	if target == nil || !target.HasConstSize {
		return false
	}
	src, ok := StripAggregateStateType(source).(*ArrayType)
	if !ok || src == nil || !arraySizesEqual(target, src) {
		return false
	}
	needsOwner, compatible := a.cloneBuiltinCompatible(target.Elem, src.Elem, map[string]bool{})
	return compatible && !needsOwner
}
