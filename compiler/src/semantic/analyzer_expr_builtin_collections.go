package semantic

import (
	"elisacore/src/ast"
	"strconv"
)

// checkDarrayGrowthRegionEscape rejects growing a darray whose storage outlives
// the current function while the ambient allocation arena is a function-local
// `Arena` value. A darray records no "home" arena: each push/extend/reserve
// grows the backing buffer from whatever `in <arena>:` scope is active. So
// growing a longer-lived darray (a struct field reached through a reference, a
// global, or a `mutable darray&` parameter) from a local `arena: Arena = zeroed`
// allocates the new buffer in that local arena, which is freed when the function
// returns — leaving the longer-lived darray pointing at freed memory. This was a
// silent, ASLR-dependent use-after-free source. The fix is to allocate from a
// persistent arena (a global `Arena`, or an `Arena&` parameter the caller owns).
func (a *Analyzer) checkDarrayGrowthRegionEscape(receiver ast.Expr, op string) {
	if a == nil || receiver == nil || a.staticContextDepth != 0 {
		return
	}
	arenaName, ok := a.ambientArenaLocalValueName()
	if !ok {
		return
	}
	if a.lvalueStorageOutlivesFunction(receiver) {
		a.errorf(receiver.Pos(), "darray %s grows a non-local darray from local arena %q; its backing buffer is freed when %q goes out of scope (use-after-free). Allocate from a persistent arena instead (a global Arena, or an Arena& parameter owned by the caller)", op, arenaName, arenaName)
		return
	}
	// The receiver is a function-local collection grown in a function-local
	// arena: its backing now lives in that arena. Record it so that returning it
	// (or a pointer into it) or storing it into a longer-lived location is caught
	// downstream as a use-after-free.
	if sym := a.rootLocalValueSymbol(receiver); sym != nil {
		if a.localArenaEscapeLocals == nil {
			a.localArenaEscapeLocals = map[*Symbol]string{}
		}
		a.localArenaEscapeLocals[sym] = arenaName
	}
}

// rootLocalValueSymbol walks an lvalue/path expression down to its root
// identifier and returns that symbol when it is a by-value local of the current
// function (the storage we own). It returns nil for parameters, globals, local
// references (which point at storage owned elsewhere), or unresolved roots.
func (a *Analyzer) rootLocalValueSymbol(expr ast.Expr) *Symbol {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			expr = n.Object
		case *ast.IndexExpr:
			expr = n.Object
		case *ast.AddrOfExpr:
			expr = n.Operand
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.Ident:
			if a.currentScope == nil {
				return nil
			}
			sym, ok := a.currentScope.Lookup(n.Name)
			if !ok || sym == nil {
				return nil
			}
			sym = symbolAliasRoot(sym)
			if sym.Kind != SymbolLocal {
				return nil
			}
			if _, isRef := sym.Type.(*RefType); isRef {
				return nil
			}
			return sym
		default:
			return nil
		}
	}
	return nil
}

// checkLocalArenaEscape flags returning or storing a value whose collection
// backing was grown in a function-local arena (recorded in localArenaEscapeLocals).
// Only values that actually alias the backing are flagged: a reference into it,
// or the collection header itself. Scalar fields (e.g. `.count`) and by-value
// element reads (`xs[i]`) are copies and cannot alias the freed buffer.
func (a *Analyzer) checkLocalArenaEscape(value ast.Expr, valueType Type, verb string) {
	if a == nil || value == nil || len(a.localArenaEscapeLocals) == 0 {
		return
	}
	var sym *Symbol
	switch valueType.(type) {
	case *RefType:
		// A reference result aliases storage: if it roots at a marked local it
		// points into a buffer that the local arena will free.
		sym = a.rootLocalValueSymbol(value)
	case *DArrayType:
		// A collection value carries the backing pointer; it escapes only when it
		// IS the marked collection (reached via field/ident, never via an index
		// which copies an element into a fresh value).
		sym = a.collectionValueRoot(value)
	default:
		return
	}
	if sym == nil {
		return
	}
	arenaName, ok := a.localArenaEscapeLocals[sym]
	if !ok {
		return
	}
	a.errorf(value.Pos(), "cannot %s %q: its backing was allocated in local arena %q, which is freed when the function returns (use-after-free). Build it in a persistent arena instead (a global Arena, or an Arena& parameter owned by the caller)", verb, sym.Name, arenaName)
}

// collectionValueRoot walks a collection-typed expression to the local it names,
// following only field/paren steps (which preserve the collection-header alias)
// and stopping at indexing (which yields an element copy) or other forms.
func (a *Analyzer) collectionValueRoot(expr ast.Expr) *Symbol {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			expr = n.Object
		case *ast.Ident:
			if a.currentScope == nil {
				return nil
			}
			sym, ok := a.currentScope.Lookup(n.Name)
			if !ok || sym == nil {
				return nil
			}
			sym = symbolAliasRoot(sym)
			if sym.Kind != SymbolLocal {
				return nil
			}
			if _, isRef := sym.Type.(*RefType); isRef {
				return nil
			}
			return sym
		default:
			return nil
		}
	}
	return nil
}

// ambientArenaLocalValueName returns the name of the active allocation arena
// when it is a function-local `Arena` value (the transient case). It returns
// false for a global Arena, an Arena& reference local/parameter (which points at
// a longer-lived arena), or any non-trivial owner expression — i.e. cases that
// are persistent or that we cannot prove transient, to avoid false positives.
func (a *Analyzer) ambientArenaLocalValueName() (string, bool) {
	if a.currentAllocExpr == nil {
		return "", false
	}
	ident, ok := stripParenExpr(a.currentAllocExpr).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return "", false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return "", false
	}
	sym = symbolAliasRoot(sym)
	if sym.Kind != SymbolLocal {
		return "", false
	}
	if _, isRef := sym.Type.(*RefType); isRef {
		return "", false
	}
	return ident.Name, true
}

// lvalueStorageOutlivesFunction reports whether the storage referenced by an
// lvalue expression lives beyond the current function: a global, a parameter,
// a local reference (which points elsewhere), or any field/element reached
// through a reference. A field/element of a by-value local is considered local.
func (a *Analyzer) lvalueStorageOutlivesFunction(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.lvalueStorageOutlivesFunction(n.Inner)
	case *ast.FieldExpr:
		if a.exprObjectIsReference(n.Object) {
			return true
		}
		return a.lvalueStorageOutlivesFunction(n.Object)
	case *ast.IndexExpr:
		if a.exprObjectIsReference(n.Object) {
			return true
		}
		return a.lvalueStorageOutlivesFunction(n.Object)
	case *ast.Ident:
		if a.currentScope == nil {
			return false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return false
		}
		sym = symbolAliasRoot(sym)
		switch sym.Kind {
		case SymbolGlobal, SymbolParam:
			return true
		case SymbolLocal:
			_, isRef := sym.Type.(*RefType)
			return isRef
		}
	}
	return false
}

func (a *Analyzer) exprObjectIsReference(obj ast.Expr) bool {
	if obj == nil {
		return false
	}
	t := a.exprTypes[obj]
	if t == nil {
		t = a.analyzeExpr(obj)
	}
	_, ok := t.(*RefType)
	return ok
}

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
		a.errorf(expr.Pos(), "darray push requires an active in <arena>: scope")
	}
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "push")
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
		// A bulk source that is itself a FRESH producer (`xs.push([v])`) hides its interior region
		// behind its own (the container's) region; descend into it.
		a.checkFreshProducerElementEscape(fieldExpr.Object, darrayType, expr.Args[0])
	} else {
		// Nested-region escape: pushing an inner-@r value into a darray whose element
		// region outlives it would leave the longer-lived buffer holding a dangling
		// reference once the inner region is freed.
		a.checkNestedRegionElementStoreEscape(expr.Args[0], darrayType, darrayType.Elem, argType)
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
			a.checkStoredRegionContainerEscape(expr.Args[0], argType)
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
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
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
		a.errorf(expr.Pos(), "darray extend requires an active in <arena>: scope")
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

func (a *Analyzer) analyzeBuiltinDarrayReserveCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
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
		a.errorf(expr.Pos(), "darray reserve expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray reserve does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray reserve requires a mutable darray receiver")
	}
	if a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "darray reserve requires an active in <arena>: scope")
	}
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "reserve")
	usizeType := a.namedTypes["usize"]
	argType := a.analyzeValueExpr(expr.Args[0], usizeType)
	if !AssignableTo(usizeType, argType) {
		a.errorf(expr.Args[0].Pos(), "darray reserve expects %s, got %s", usizeType, argType)
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
		Name:   "darray.reserve",
		Params: []Type{resultType, usizeType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray reserve"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
}

// rejectAffineElementDrop reports an error when a container operation would discard
// must-consume (affine) elements without consuming them. Silently dropping such a value is
// exactly the leak the affine system exists to prevent; the only sound way to remove affine
// elements is a consuming move-drain (`for x in move c:`), which moves each element out and
// forces the body to consume it.
func (a *Analyzer) rejectAffineElementDrop(obj ast.Expr, elemType Type, op string) {
	if a == nil || obj == nil || elemType == nil {
		return
	}
	if !a.containsAffineHandleValues(elemType, map[string]bool{}) {
		return
	}
	a.errorf(obj.Pos(), "%s would drop must-consume element(s) of type %s without consuming them; drain the container with `for x in move c:` and consume each element instead", op, elemType)
}

// analyzeBuiltinDarrayResizeCall handles `da.resize(n)`: presize to exactly n live
// elements in one allocation (capacity >= n, count := n), so a fill loop writes by
// index with no per-element push/capacity-check. Mirrors reserve, then seals the length.
func (a *Analyzer) analyzeBuiltinDarrayResizeCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "resize" || fieldExpr.Object == nil {
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
		a.errorf(expr.Pos(), "darray resize expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray resize does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray resize requires a mutable darray receiver")
	}
	if a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "darray resize requires an active in <arena>: scope")
	}
	// resize can shrink (dropping the tail) and, when growing, exposes zero-filled slots as live
	// elements — both unsound for must-consume element types. Reject affine-element resize.
	a.rejectAffineElementDrop(fieldExpr.Object, darrayType.Elem, "darray resize")
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "resize")
	usizeType := a.namedTypes["usize"]
	argType := a.analyzeValueExpr(expr.Args[0], usizeType)
	if !AssignableTo(usizeType, argType) {
		a.errorf(expr.Args[0].Pos(), "darray resize expects %s, got %s", usizeType, argType)
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
		Name:   "darray.resize",
		Params: []Type{resultType, usizeType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray resize"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayClearCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
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
		a.errorf(expr.Pos(), "darray clear expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray clear requires a mutable darray receiver")
	}
	a.rejectAffineElementDrop(fieldExpr.Object, darrayType.Elem, "darray clear")
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
	a.exprTypes[expr.Func] = &FuncType{Name: "darray.clear", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray clear"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayTruncateCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
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
		a.errorf(expr.Pos(), "darray truncate expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray truncate requires a mutable darray receiver")
	}
	a.rejectAffineElementDrop(fieldExpr.Object, darrayType.Elem, "darray truncate")
	usizeType := a.namedTypes["usize"]
	argType := a.analyzeValueExpr(expr.Args[0], usizeType)
	if !AssignableTo(usizeType, argType) {
		a.errorf(expr.Args[0].Pos(), "darray truncate expects %s, got %s", usizeType, argType)
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
	a.exprTypes[expr.Func] = &FuncType{Name: "darray.truncate", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray truncate"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
}

func builtinStoreResultRefType(storeType *StructType, receiverRefType *RefType) *RefType {
	if receiverRefType != nil {
		return receiverRefType
	}
	return &RefType{Elem: storeType, Mutable: true, State: RefStateNonNull, Storage: RefStorageAny, ExplicitStorage: true}
}

func builtinStorePushResultType(storeType *StructType, receiverRefType *RefType, u32Type Type) Type {
	if storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		return &IDType{Tag: storeType, Storage: u32Type}
	}
	return builtinStoreResultRefType(storeType, receiverRefType)
}

func (a *Analyzer) analyzeBuiltinStorePushCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store push requires a mutable store receiver")
	}
	if a.currentAllocExpr == nil && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "store push requires an active in <arena>: scope")
	}
	if len(expr.Args) != len(storeType.StoreFieldOrder) {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store push expects %d arguments, got %d", len(storeType.StoreFieldOrder), len(expr.Args))
		resultType := builtinStorePushResultType(storeType, receiverRefType, a.namedTypes["u32"])
		a.exprTypes[expr] = resultType
		return resultType, true
	}
	for i, name := range storeType.StoreFieldOrder {
		field := storeType.Fields[name]
		darrayField, ok := field.Type.(*DArrayType)
		if !ok || darrayField == nil {
			continue
		}
		argType := a.analyzeValueExpr(expr.Args[i], darrayField.Elem)
		if !AssignableTo(darrayField.Elem, argType) {
			a.errorf(expr.Args[i].Pos(), "store push argument %d (%s) expects %s, got %s", i+1, name, darrayField.Elem, argType)
		}
		a.consumeAffineValueExpr(expr.Args[i], darrayField.Elem, "move into store push")
	}
	resultType := builtinStorePushResultType(storeType, receiverRefType, a.namedTypes["u32"])
	a.exprTypes[expr.Func] = &FuncType{Name: "store.push", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreReserveCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store reserve requires a mutable store receiver")
	}
	if a.currentAllocExpr == nil && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "store reserve requires an active in <arena>: scope")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store reserve expects 1 argument, got %d", len(expr.Args))
	}
	usizeType := a.namedTypes["usize"]
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], usizeType)
		if !AssignableTo(usizeType, argType) {
			a.errorf(expr.Args[0].Pos(), "store reserve expects %s, got %s", usizeType, argType)
		}
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.reserve", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreClearCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store clear requires a mutable store receiver")
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store clear expects 0 arguments, got %d", len(expr.Args))
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.clear", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreTruncateCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store truncate requires a mutable store receiver")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store truncate expects 1 argument, got %d", len(expr.Args))
	}
	usizeType := a.namedTypes["usize"]
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], usizeType)
		if !AssignableTo(usizeType, argType) {
			a.errorf(expr.Args[0].Pos(), "store truncate expects %s, got %s", usizeType, argType)
		}
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.truncate", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreRowsCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "rows" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, _, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store rows expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "store rows does not support named arguments")
	}
	resultType := &StoreRowsViewType{Store: storeType}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "store.rows",
		Params: []Type{receiverType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinSOAValidCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "valid" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, _, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil || storeType.StoreDecl == nil || !storeType.StoreDecl.Soa {
		return nil, false
	}
	rowType := &IDType{Tag: storeType, Storage: a.namedTypes["u32"]}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "soa valid expects 1 argument, got %d", len(expr.Args))
	} else {
		argType := a.analyzeValueExpr(expr.Args[0], rowType)
		if !AssignableTo(rowType, argType) {
			a.errorf(expr.Args[0].Pos(), "soa valid expects %s, got %s", rowType, argType)
		}
	}
	resultType := a.namedTypes["bool"]
	a.exprTypes[expr.Func] = &FuncType{Name: "soa.valid", Params: []Type{receiverType, rowType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func builtinDArrayPushReceiverType(t Type) (*DArrayType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if darrayType, ok := t.(*DArrayType); ok && darrayType != nil {
		return darrayType, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil {
		return nil, nil, false
	}
	darrayType, ok := refType.Elem.(*DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, false
	}
	return darrayType, refType, true
}

func builtinDArrayPushReceiverWritable(a *Analyzer, receiver ast.Expr, receiverType Type, receiverRefType *RefType) bool {
	if receiverRefType != nil {
		return receiverRefType.Mutable
	}
	return a != nil && a.exprCanYieldWritableRef(receiver)
}

func builtinDArrayExtendSourceCompatible(elemType Type, sourceType Type) bool {
	if elemType == nil || sourceType == nil {
		return false
	}
	// Valid extend SOURCES are contiguous element sequences: an owning darray, a fixed
	// array, or a view (`view[T]`/`sview`). A view is a read-only BORROW, so it can never
	// be the extend TARGET — but reading its bytes into `dst` is a plain value copy
	// (`dst` owns the result, the view is untouched), and a contiguous view lowers to the
	// same `arena_memcpy` a darray source does. Element region-escape and affine-move
	// safety are enforced separately by the caller (checkNestedRegionBulkStoreEscape /
	// the affine-element check), so this only governs shape compatibility.
	switch tt := sourceType.(type) {
	case *DArrayType:
		return SameType(elemType, tt.Elem)
	case *ArrayType:
		return SameType(elemType, tt.Elem)
	case *ViewType:
		return SameType(elemType, tt.Elem)
	case *SViewType:
		// sview is a contiguous u8 byte view; its element is always u8.
		return isExtendU8Elem(elemType)
	case *RefType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		switch inner := tt.Elem.(type) {
		case *DArrayType:
			return SameType(elemType, inner.Elem)
		case *ArrayType:
			return SameType(elemType, inner.Elem)
		case *ViewType:
			return SameType(elemType, inner.Elem)
		case *SViewType:
			return isExtendU8Elem(elemType)
		}
	}
	return false
}

func isExtendU8Elem(elemType Type) bool {
	b, ok := StripAggregateStateType(elemType).(*BuiltinType)
	return ok && b != nil && b.Name == "u8"
}

// isStringLitExtendArg reports whether an extend argument is a bare string literal
// (peeling parentheses) — the only static-u8& shape whose length is a compile-time
// constant, hence the only one safe to adapt to a bounded view source.
func isStringLitExtendArg(e ast.Expr) bool {
	for {
		switch n := e.(type) {
		case *ast.StringLit:
			return true
		case *ast.ParenExpr:
			if n == nil {
				return false
			}
			e = n.Inner
		default:
			return false
		}
	}
}

func darrayElemPrefersListLiteralAsSingleValue(elemType Type) bool {
	switch StripAggregateStateType(elemType).(type) {
	case *ArrayType, *DArrayType:
		return true
	default:
		return false
	}
}

func builtinStoreReceiverType(t Type) (*StructType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if st, ok := StripAggregateStateType(t).(*StructType); ok && st != nil && st.Store {
		return st, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	st, ok := StripAggregateStateType(refType.Elem).(*StructType)
	if !ok || st == nil || !st.Store {
		return nil, nil, false
	}
	return st, refType, true
}

func storeRowViewField(t Type, fieldName string) (Field, bool) {
	rowType, ok := t.(*StoreRowViewType)
	if !ok || rowType == nil || rowType.Store == nil {
		return Field{}, false
	}
	field, ok := rowType.Store.Fields[fieldName]
	if !ok {
		return Field{}, false
	}
	darrayType, ok := field.Type.(*DArrayType)
	if !ok || darrayType == nil {
		return Field{}, false
	}
	return Field{Name: fieldName, Type: darrayType.Elem, Mutable: true}, true
}
