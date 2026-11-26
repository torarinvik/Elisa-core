package semantic

import "elisacore/src/ast"

// stampBindingRegion gives a container binding the region its value was allocated in: the
// active `region`/`in` scope for an ambient allocation, or the caller's threaded arena for a
// region-polymorphic call. Escape checks and arena routing read the region off the type, so a
// binding that skips this looks caller-owned and may be returned out of a freed region.
func (a *Analyzer) stampBindingRegion(bindingType Type, value ast.Expr, valueType Type) Type {
	bindingType = a.stampContainerRegion(bindingType)
	// A region-polymorphic call's result is allocated in the caller's threaded
	// arena even though its declared return type is regionless. Preserve that
	// owner on a local container binding so a later store through the local does
	// not lose the hidden `__region_auto` dependency.
	if value != nil && containerOrEntryRegion(bindingType) == "" && a.exprIsRegionPolyResultCarryingRegionData(value, valueType) {
		region := ""
		if call, ok := unwrapParenForRegionPoly(value).(*ast.CallExpr); ok && call != nil && a.destinationAllocated != nil {
			region = a.destinationAllocated[call]
		}
		if region == "" {
			if regionArg, found := a.regionPolymorphicCallerRegionArg(); found {
				if ident, ok := stripParenExpr(regionArg).(*ast.Ident); ok && ident != nil {
					region = ident.Name
				}
			}
		}
		if stamped, applied := stampContainerRegion(bindingType, region); applied {
			bindingType = stamped
		}
	}
	return bindingType
}

// stampConditionBinderRegion is stampBindingRegion for `if make() is found:`. The binder holds
// the call's own value, so it lives where that call allocated, exactly as
// `found = get make() else ...` does. Only a fresh call result is stamped: narrowing an existing
// place (`if param is p:`) borrows that place's storage, which the region does not own.
func (a *Analyzer) stampConditionBinderRegion(boundType Type, value ast.Expr, valueType Type) Type {
	if _, isCall := stripParenExpr(value).(*ast.CallExpr); !isCall {
		return boundType
	}
	return a.stampBindingRegion(boundType, value, valueType)
}
