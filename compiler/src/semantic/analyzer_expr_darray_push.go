package semantic

import (
	"elisacore/src/ast"
	"strconv"
)

func (a *Analyzer) analyzeBuiltinDarrayPushCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	// Preserve identity for provenance checks; spelling alone is not authority.
	a.recordCompilerBuiltinHelperCall(expr, "darray.push")
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray push expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray push does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray push requires a mutable darray receiver")
	}
	if a.staticContextDepth == 0 && a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "%s", NoGrowthRegionMessage("darray push"))
	}
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "push")
	// `buf.push(0)` NUL-terminates the buffer — remembered so a later `(&buf[0]).cast[cstr]`
	// is recognised as a bounded C-string rather than an unbounded scan.
	a.noteDarrayNulTerminated(fieldExpr.Object, expr.Args[0])
	// Growing a container that is a FIELD of a region-less struct local (`m.bits.push(..)`) allocates
	// the field backing in the ambient region; taint the struct so a later by-value copy of it into a
	// longer-lived container is caught (S4 Stage 2 — by-value region-struct element storage).
	a.recordGrownFieldInteriorTaint(fieldExpr.Object)
	pushArgType := darrayType.Elem
	bulkPush := false
	if list, ok := expr.Args[0].(*ast.ListLitExpr); ok && list != nil && !darrayElemPrefersListLiteralAsSingleValue(darrayType.Elem) {
		pushArgType = &ArrayType{Elem: darrayType.Elem, Size: strconv.FormatInt(int64(len(list.Elems)), 10), HasConstSize: true, ConstSize: int64(len(list.Elems))}
		bulkPush = true
	}
	argType := a.analyzeValueExpr(expr.Args[0], pushArgType)
	a.checkRetainedBorrowEscapesLocal(fieldExpr.Object, expr.Args[0], pushArgType, "darray push")
	if !bulkPush {
		if !AssignableTo(darrayType.Elem, argType) {
			if builtinDArrayExtendSourceCompatible(darrayType.Elem, argType) {
				bulkPush = true
			} else {
				a.errorf(expr.Args[0].Pos(), "darray push expects %s, got %s", darrayType.Elem, argType)
			}
		}
	}
	if bulkPush {
		if !builtinDArrayExtendSourceCompatible(darrayType.Elem, argType) {
			a.errorf(expr.Args[0].Pos(), "darray push expects %s or a compatible darray or array source of %s, got %s", darrayType.Elem, darrayType.Elem, argType)
		}
		if a.containsAffineHandleValues(darrayType.Elem, map[string]bool{}) {
			a.errorf(expr.Args[0].Pos(), "bulk darray push does not support affine element type %s; push elements individually with explicit move", darrayType.Elem)
		}
		// Nested-region escape: bulk-pushing the elements of an inner-region source
		// container into a longer-lived target leaves dangling element references
		// once the source region is freed.
		a.checkNestedRegionBulkStoreEscape(expr.Args[0], fieldExpr.Object, darrayType, darrayType.Elem, argType)
		a.recordLocalContainerElementPush(fieldExpr.Object, expr.Args[0], true)
		// A bulk source that is itself a FRESH producer (`xs.push([v])`) hides its interior region
		// behind its own (the container's) region; descend into it.
		a.checkFreshProducerElementEscape(fieldExpr.Object, darrayType, expr.Args[0])
	} else {
		// Nested-region escape: pushing an inner-@r value into a darray whose element
		// region outlives it would leave the longer-lived buffer holding a dangling
		// reference once the inner region is freed.
		a.checkNestedRegionElementStoreEscape(expr.Args[0], darrayType, darrayType.Elem, argType)
		a.recordLocalContainerElementPush(fieldExpr.Object, expr.Args[0], false)
		// A pushed FRESH producer (`ol.push([v])`, a ternary, a struct literal) hides its interior
		// region behind its own (the container's) region, so the element check above misses it; check
		// the pushed value's interior region against the container's lifetime.
		a.checkFreshProducerElementEscape(fieldExpr.Object, darrayType, expr.Args[0])
		// A by-VALUE copy of a region-less struct/local whose interior field was grown into a
		// shorter-lived region (interior taint) dangles when that region dies but the container
		// outlives it. The element/fresh checks above miss it (the arg's TYPE carries no region and it
		// is not a fresh producer), so consult the taint side-table directly (S4 Stage 2). A no-op for
		// fresh producers (already covered above) and for scalar/region-less args.
		if !isFreshContainerProducer(expr.Args[0]) {
			a.checkInteriorRegionAgainstTarget(fieldExpr.Object, containerRegion(darrayType), expr.Args[0], "by-value copy")
		}
		// Region-poly result element: its type is region-less but it lives in the ambient
		// region, so pushing it into a container whose storage outlives the function dangles
		// once that region is freed (same hole as the assignment path). Mirror the assignment
		// gate: only when the receiver container outlives the function.
		if a.lvalueStorageOutlivesFunction(fieldExpr.Object) {
			a.checkStoredRegionContainerEscapeInto(expr.Args[0], argType, containerRegion(darrayType))
		}
		a.consumeAffineValueExpr(expr.Args[0], darrayType.Elem, "move into darray push")
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.push",
		Params: []Type{resultType, argType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray push"))
	if len(expr.Args) == 1 {
		// The pushed element may itself be a view: the container now holds it.
		a.recordStorageViewPlaceStore(fieldExpr.Object, expr.Args[0])
	}
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
}

// analyzeBuiltinDarrayPopCall handles the value-producing `da.pop()` operation.
// It must be recognized before UFCS/generic resolution: otherwise the compiler's own
// aggregate containers instantiate `darray_pop[T]`, whose generic body cannot be lowered
// during the stage0 bootstrap for every aggregate element type.
func (a *Analyzer) analyzeBuiltinDarrayPopCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "pop" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray pop expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray pop does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray pop requires a mutable darray receiver")
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{Elem: darrayType, Mutable: true, State: RefStateNonNull, Storage: RefStorageAny, ExplicitStorage: true}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.pop",
		Params: []Type{resultType},
		Return: darrayType.Elem,
	}
	a.exprTypes[expr] = darrayType.Elem
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray pop"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return darrayType.Elem, true
}

// isPlainListComprehensionArg reports whether e (after unwrapping parens) is a list comprehension
// (not a dict/set comprehension, which infer their key/value/element types independently and don't
// want a DArrayType expected-type hint forced onto them).
func isPlainListComprehensionArg(e ast.Expr) bool {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok || p == nil {
			break
		}
		e = p.Inner
	}
	comp, ok := e.(*ast.ListComprehensionExpr)
	return ok && comp != nil && comp.Key == nil && !comp.Set
}

func (a *Analyzer) analyzeBuiltinDarrayExtendCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray extend expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray extend does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray extend requires a mutable darray receiver")
	}
	if a.staticContextDepth == 0 && a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "%s", NoGrowthRegionMessage("darray extend"))
	}
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "extend")
	var expectedSource Type
	if list, ok := expr.Args[0].(*ast.ListLitExpr); ok && list != nil {
		expectedSource = &ArrayType{Elem: darrayType.Elem, Size: strconv.FormatInt(int64(len(list.Elems)), 10), HasConstSize: true, ConstSize: int64(len(list.Elems))}
	} else if isPlainListComprehensionArg(expr.Args[0]) {
		// A list comprehension source has no inherent element type of its own — e.g. a range
		// comprehension's loop var defaults to `int` — so without a hint `dst: darray[i64]).extend([i
		// for i in range])` would infer `darray[int]` and be rejected by the source-compatibility
		// check below even though the comprehension COULD legally produce i64. Propagate dst's
		// element type as the expected result, exactly as a `dst: darray[T] = [...]` var decl would.
		expectedSource = &DArrayType{Elem: darrayType.Elem, Shape: &WildcardShape{}, SurfaceName: "darray"}
	} else if isStringLitExtendArg(expr.Args[0]) && isExtendU8Elem(darrayType.Elem) {
		// A string LITERAL has a compile-time-known length, so — unlike a bare `static u8&`
		// byte pointer (a NUL-terminated pointer whose length is NOT in its type) — it is a
		// bounded source safe to bulk-copy. Adapt it to an sview (the u8 byte view): the
		// contextual-string-literal path retypes the literal, then the existing view extend
		// path memcpies exactly its bytes. Mirrors the match-fold literal->sview adaptation.
		expectedSource = &SViewType{}
	}
	// A comprehension argument to `extend` FUSES — its elements are pushed straight
	// into the receiver, no standalone temp is allocated. So analyze it with the
	// receiver as the allocation context: the (fused) comprehension inherits the
	// receiver's growth region and doesn't independently demand an ambient `in <arena>:`
	// scope — which is what let a ref-param receiver's push loop compile but its
	// equivalent `.extend([… for …])` (the -Wperf-suggested form) fail. The extend's own
	// growth-region check (above) already validated the receiver.
	var sourceType Type
	if isPlainListComprehensionArg(expr.Args[0]) {
		savedAllocExpr := a.currentAllocExpr
		if a.currentAllocExpr == nil {
			a.currentAllocExpr = fieldExpr.Object
		}
		sourceType = a.analyzeValueExpr(expr.Args[0], expectedSource)
		a.currentAllocExpr = savedAllocExpr
	} else {
		sourceType = a.analyzeValueExpr(expr.Args[0], expectedSource)
	}
	if !builtinDArrayExtendSourceCompatible(darrayType.Elem, sourceType) {
		if isStaticStringLiteralRefType(sourceType) {
			// A bare `static u8&` byte-ref binding that ISN'T a literal (a literal adapts
			// above): its length is not in its type, so extend cannot bound the copy. Point
			// at the two length-carrying fixes instead of the opaque shape error.
			a.errorf(expr.Args[0].Pos(), "darray extend source is an unbounded `static u8&` byte pointer with no length; wrap it in a bounded view (`sview`/`view[u8]`) or build a length-carrying string such as an f-string `f\"...\"`")
		} else if !IsInvalidType(sourceType) {
			a.errorf(expr.Args[0].Pos(), "darray extend expects a compatible darray, array, or view source of %s, got %s", darrayType.Elem, sourceType)
		}
	}
	if a.containsAffineHandleValues(darrayType.Elem, map[string]bool{}) {
		a.errorf(expr.Args[0].Pos(), "darray extend does not support affine element type %s; push elements individually with explicit move", darrayType.Elem)
	}
	// Nested-region escape: extending from an inner-region source container into a
	// longer-lived target leaves dangling element references once the source
	// region is freed.
	a.checkNestedRegionBulkStoreEscape(expr.Args[0], fieldExpr.Object, darrayType, darrayType.Elem, sourceType)
	// A fresh-producer source (`ol.extend([v])`) hides its interior region behind its own; descend in.
	a.checkFreshProducerElementEscape(fieldExpr.Object, darrayType, expr.Args[0])
	a.recordLocalContainerElementPush(fieldExpr.Object, expr.Args[0], true)
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.extend",
		Params: []Type{resultType, sourceType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray extend"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
}
