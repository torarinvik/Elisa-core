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
	if a.staticContextDepth == 0 && a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) {
		a.errorf(expr.Pos(), "darray push requires an active in <arena>: scope")
	}
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "push")
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
			a.errorf(expr.Args[0].Pos(), "darray push expects %s or a compatible darray, view, or array source of %s, got %s", darrayType.Elem, darrayType.Elem, argType)
		}
		if a.containsAffineHandleValues(darrayType.Elem, map[string]bool{}) {
			a.errorf(expr.Args[0].Pos(), "bulk darray push does not support affine element type %s; push elements individually with explicit move", darrayType.Elem)
		}
	} else {
		// Nested-region escape: pushing an inner-@r value into a darray whose element
		// region outlives it would leave the longer-lived buffer holding a dangling
		// reference once the inner region is freed.
		a.checkNestedRegionElementStoreEscape(expr.Args[0], darrayType, darrayType.Elem, argType)
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
	if a.staticContextDepth == 0 && a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) {
		a.errorf(expr.Pos(), "darray extend requires an active in <arena>: scope")
	}
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "extend")
	var expectedSource Type
	if list, ok := expr.Args[0].(*ast.ListLitExpr); ok && list != nil {
		expectedSource = &ArrayType{Elem: darrayType.Elem, Size: strconv.FormatInt(int64(len(list.Elems)), 10), HasConstSize: true, ConstSize: int64(len(list.Elems))}
	}
	sourceType := a.analyzeValueExpr(expr.Args[0], expectedSource)
	if !builtinDArrayExtendSourceCompatible(darrayType.Elem, sourceType) {
		a.errorf(expr.Args[0].Pos(), "darray extend expects a compatible darray, view, or array source of %s, got %s", darrayType.Elem, sourceType)
	}
	if a.containsAffineHandleValues(darrayType.Elem, map[string]bool{}) {
		a.errorf(expr.Args[0].Pos(), "darray extend does not support affine element type %s; push elements individually with explicit move", darrayType.Elem)
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
	if a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) {
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
	if a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) {
		a.errorf(expr.Pos(), "darray resize requires an active in <arena>: scope")
	}
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
	if a.currentAllocExpr == nil {
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
	if a.currentAllocExpr == nil {
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
	switch tt := sourceType.(type) {
	case *DArrayType:
		return SameType(elemType, tt.Elem)
	case *ViewType:
		return SameType(elemType, tt.Elem)
	case *ArrayType:
		return SameType(elemType, tt.Elem)
	case *RefType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		switch inner := tt.Elem.(type) {
		case *DArrayType:
			return SameType(elemType, inner.Elem)
		case *ViewType:
			return SameType(elemType, inner.Elem)
		case *ArrayType:
			return SameType(elemType, inner.Elem)
		}
	}
	return false
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
