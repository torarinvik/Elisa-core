package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// checkTupleReturnRegionEscapes checks tuple literals field-by-field. Contextual
// tuple typing records the declared field types on the tuple expression, so
// checking only the aggregate can mistake an input borrowed from @b for the
// declared @a lifetime. Each source expression must independently satisfy its
// field's return lifetime before the aggregate contract is trusted.
func (a *Analyzer) checkTupleReturnRegionEscapes(valueExpr ast.Expr, valueType, returnType Type) bool {
	returnTuple, ok := StripAggregateStateType(returnType).(*TupleType)
	if !ok || returnTuple == nil {
		return false
	}
	tupleExpr, isLiteral := stripParenExpr(valueExpr).(*ast.TupleExpr)
	var sourceTuple *TupleType
	if isLiteral {
		if len(returnTuple.Fields) != len(tupleExpr.Elems) {
			return false
		}
	} else {
		sourceTuple = a.sourceTupleType(valueExpr, valueType)
		if sourceTuple == nil || len(returnTuple.Fields) != len(sourceTuple.Fields) {
			return false
		}
	}
	for index, targetField := range returnTuple.Fields {
		var element ast.Expr
		actualType := Type(nil)
		if isLiteral {
			element = tupleExpr.Elems[index]
			actualType = a.exprType(element)
		} else {
			actualType = sourceTuple.Fields[index].Type
		}
		if actualType == nil {
			actualType = targetField.Type
		}
		if nestedTupleType(actualType) != nil && nestedTupleType(targetField.Type) != nil {
			if isLiteral && a.checkTupleReturnRegionEscapes(element, actualType, targetField.Type) {
				continue
			}
			if !isLiteral && a.checkTupleTypeReturnRegionEscapes(valueExpr, nestedTupleType(actualType), nestedTupleType(targetField.Type)) {
				continue
			}
		}
		a.checkTupleReturnFieldRegion(valueExpr, element, targetField.Name, actualType, targetField.Type)
	}
	return true
}

func (a *Analyzer) sourceTupleType(expr ast.Expr, analyzedType Type) *TupleType {
	if ident, ok := stripParenExpr(expr).(*ast.Ident); ok && a.currentScope != nil {
		if symbol, found := a.currentScope.Lookup(ident.Name); found && symbol != nil {
			if tuple := nestedTupleType(symbol.Type); tuple != nil {
				return tuple
			}
		}
	}
	return nestedTupleType(analyzedType)
}

func nestedTupleType(typ Type) *TupleType {
	if tuple, ok := StripAggregateStateType(typ).(*TupleType); ok {
		return tuple
	}
	return nil
}

func (a *Analyzer) checkTupleTypeReturnRegionEscapes(expr ast.Expr, source, target *TupleType) bool {
	if source == nil || target == nil || len(source.Fields) != len(target.Fields) {
		return false
	}
	for index, targetField := range target.Fields {
		sourceType := source.Fields[index].Type
		if a.checkTupleTypeReturnRegionEscapes(expr, nestedTupleType(sourceType), nestedTupleType(targetField.Type)) {
			continue
		}
		a.checkTupleReturnFieldRegion(expr, nil, targetField.Name, sourceType, targetField.Type)
	}
	return true
}

func (a *Analyzer) checkTupleReturnFieldRegion(aggregateExpr, fieldExpr ast.Expr, name string, actualType, expectedType Type) {
	expr := fieldExpr
	if expr == nil {
		expr = aggregateExpr
	}
	if expr == nil {
		return
	}
	actualRegion := regionParamReturnTypeRegion(actualType)
	if actualRegion == "" && fieldExpr != nil {
		actualRegion = a.returnBorrowedRegion(fieldExpr)
	}
	expectedRegion := regionParamReturnTypeRegion(expectedType)
	regionContractRejected := false
	if expectedRegion != "" && actualRegion != expectedRegion &&
		!a.regionOutlives(actualRegion, expectedRegion) && !isStaticStringLiteralExpr(fieldExpr) {
		if actualRegion == "" {
			a.errorf(expr.Pos(), "tuple return field %q has no proven backing region for the declared return lifetime %q", name, expectedRegion)
		} else {
			a.errorf(expr.Pos(), "tuple return field %q is tied to region %q, which does not prove the declared return lifetime %q", name, actualRegion, expectedRegion)
		}
		regionContractRejected = true
	}
	a.checkReturnRegionContainerEscape(expr, actualType)
	a.checkRegionAggregateReturnEscape(expr, actualType)
	if !regionContractRejected {
		a.checkRegionParamReturnEscape(expr, actualType)
	}
}

func isStaticStringLiteralExpr(expr ast.Expr) bool {
	_, ok := stripParenExpr(expr).(*ast.StringLit)
	return ok
}

// checkRegionAggregateReturnEscape rejects RETURNING a by-value aggregate that
// carries region-backed storage (an SoA `Store` struct, a container, or a struct
// transitively containing one) when that aggregate was built into a scope-owned
// local region — i.e. the value is produced by a call that takes the local
// `region NAME(...):` block's arena as an argument. The region is freed at the
// block's exit, so every container/store column inside the returned aggregate
// dangles (use-after-free at the caller's first read).
//
// This is the laundered-through-a-call analogue of checkReturnRegionContainerEscape:
// that check only sees a container's region when it is stamped directly on the
// value's TYPE (a `darray[T] @r` value). When the region is instead threaded as an
// `Arena` argument into a builder that returns a struct-of-stores by value, the
// region never reaches the return type, so the container check is blind to it. This
// closes that hole syntactically: scope-owned region passed to an Arena param of a
// call whose region-carrying result is returned.
func (a *Analyzer) checkRegionAggregateReturnEscape(value ast.Expr, valueType Type) {
	if a == nil || value == nil || !typeCarriesRegionStorage(valueType) {
		return
	}
	region := a.regionAggregateConstructionRegion(value)
	if region == "" {
		// A by-value aggregate whose interior field was grown into a scope-owned LOCAL region
		// (recorded as interior taint, e.g. `m.bits.push(..)` then `return m`) dangles once that
		// region frees at block exit — the same UAF as construction-into-a-local-region, but the
		// region rode in via field growth rather than a construction-call argument.
		taint := a.returnedAggregateTaintRegion(value)
		if taint == "" || a.isCompilerGeneratedAutoRegion(taint) {
			return
		}
		if sym, state := a.lookupRegionState(taint); sym == nil || state.Destroyed || state.DeclScope == nil {
			return // not a live scope-owned local region (e.g. a region param the caller owns)
		}
		region = taint
	}
	a.errorf(value.Pos(), "value backed by scope-owned region %q escapes via return; the region is freed at block exit, leaving its containers/stores dangling. Build it into a caller-owned region instead — take a region param (`def f[@r] ... @r`) or an `Arena&` the caller owns, rather than a local `region %s(...):` block", a.regionDisplayName(region), a.regionDisplayName(region))
}

// typeCarriesRegionStorage reports whether a value of type t can transitively hold
// pointers into an allocation region: a container/view (darray/dict/set/dstr/view/
// sview), an SoA `Store` struct (its columns are region-backed and relocatable), or
// a plain struct/aggregate any of whose fields transitively carries such storage.
// Scalars and pointer-free structs return false, so returning them out of a region
// block is never flagged.
func typeCarriesRegionStorage(t Type) bool {
	return typeCarriesRegionStorageRec(t, map[Type]bool{})
}

func typeCarriesRegionStorageRec(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *DArrayType, *DictType, *SetType, *CStrType, *SViewType, *ViewType:
		return true
	case *RefType:
		if tt == nil {
			return false
		}
		return typeCarriesRegionStorageRec(tt.Elem, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if typeCarriesRegionStorageRec(field.Type, seen) {
				return true
			}
		}
		return false
	case *OptionalType:
		return typeCarriesRegionStorageRec(tt.Value, seen)
	case *ErrorUnionType:
		return typeCarriesRegionStorageRec(tt.Value, seen)
	case *ArrayType:
		return typeCarriesRegionStorageRec(tt.Elem, seen)
	case *GenericInstanceType:
		if tt.Region != "" {
			return true
		}
		for _, argument := range tt.Args {
			if typeCarriesRegionStorageRec(argument, seen) {
				return true
			}
		}
		// The generic struct's own fields (`items: darray[T]` in `Vector[T]`) carry storage
		// regardless of the type arguments.
		if base, ok := tt.Base.(*StructType); ok {
			return typeCarriesRegionStorageRec(base, seen)
		}
		return false
	case *AggregateStateType:
		if tt == nil {
			return false
		}
		return typeCarriesRegionStorageRec(tt.Base, seen)
	case *StructType:
		if tt == nil {
			return false
		}
		if tt.Store {
			return true
		}
		for _, f := range tt.Fields {
			if typeCarriesRegionStorageRec(f.Type, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		if tt == nil {
			return false
		}
		// A PACKED enum is a handle into a region store — deliberately NOT treated as carrying
		// region storage here (typeIsRegionValued covers handles with its own, narrower intent).
		// An INLINE value enum, though, embeds its variant payloads directly, so a variant payload
		// holding a container (`Item.Row(vals: darray[i64])`) makes the enum VALUE carry the
		// container's region-backed header exactly like a struct field would.
		if tt.Packed || tt.Root().Packed {
			return false
		}
		for _, v := range tt.Variants {
			for _, p := range v.Payload {
				if typeCarriesRegionStorageRec(p, seen) {
					return true
				}
			}
		}
		return false
	}
	return false
}

// regionAggregateConstructionRegion returns the name of a live scope-owned local
// region that `value` was built into via a call argument, or "" if none. It walks
// every call in the (possibly try/paren/cast-wrapped) value expression and reports
// the first call argument that is a live scope-owned region identifier bound to an
// `Arena` parameter — the signal that the call allocated the returned aggregate in
// that region.
func (a *Analyzer) regionAggregateConstructionRegion(value ast.Expr) string {
	if a == nil || value == nil {
		return ""
	}
	found := ""
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found != "" || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if v.CanInterface() {
				if call, ok := v.Interface().(*ast.CallExpr); ok {
					if r := a.callConstructionRegion(call); r != "" {
						found = r
						return
					}
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if found != "" {
					return
				}
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				if found != "" {
					return
				}
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(value))
	return found
}

// callConstructionRegion reports the live scope-owned local region passed as an
// Arena argument to call, or "" if none. A region PARAMETER (caller-owned) is
// excluded: it outlives the call, so building into it is sound.
func (a *Analyzer) callConstructionRegion(call *ast.CallExpr) string {
	if a == nil || call == nil {
		return ""
	}
	ft, _ := a.exprType(call.Func).(*FuncType)
	if ft == nil {
		return ""
	}
	for i, arg := range call.Args {
		ident, ok := stripParenExpr(arg).(*ast.Ident)
		if !ok || ident == nil {
			continue
		}
		if a.lookupRegionParam(ident.Name) {
			continue // caller-owned region — outlives the call.
		}
		if sym, _ := a.lookupRegionState(ident.Name); sym == nil {
			continue // not a live region owner.
		}
		if a.argBindsArenaParam(ft, call, i) {
			return ident.Name
		}
	}
	return ""
}

// argBindsArenaParam reports whether argument argIndex of call binds to an
// Arena-typed parameter of ft (resolving named arguments through the parameter
// name list).
func (a *Analyzer) argBindsArenaParam(ft *FuncType, call *ast.CallExpr, argIndex int) bool {
	if ft == nil {
		return false
	}
	paramIndex := argIndex
	if argIndex < len(call.ArgNames) && call.ArgNames[argIndex] != "" {
		paramIndex = -1
		for pi, pn := range ft.ExplicitParamNames {
			if pn == call.ArgNames[argIndex] {
				paramIndex = pi
				break
			}
		}
	}
	if paramIndex < 0 || paramIndex >= len(ft.Params) {
		return false
	}
	return isArenaLikeType(ft.Params[paramIndex])
}

// isArenaLikeType reports whether t is the builtin Arena struct (peeling ref,
// mutable, and state wrappers).
func isArenaLikeType(t Type) bool {
	for {
		switch tt := t.(type) {
		case *RefType:
			if tt == nil {
				return false
			}
			t = tt.Elem
		case *AggregateStateType:
			if tt == nil {
				return false
			}
			t = tt.Base
		case *StructType:
			return tt != nil && tt.Name == "Arena"
		default:
			return false
		}
	}
}

// checkStoredRegionContainerEscape rejects storing a scope-owned-region container
// into storage that outlives the region (a global / function-outliving lvalue).
func (a *Analyzer) checkStoredRegionContainerEscape(valueExpr ast.Expr, valueType Type) {
	a.checkRegionContainerEscape(valueExpr, valueType, "store into longer-lived storage")
}

// checkStoredRegionContainerEscapeInto is checkStoredRegionContainerEscape for a store whose
// target place carries a known container region. When that region is the function's ambient
// grown-container region, the synthesized auto region adopts the caller's arena (the backend's
// ambientGrownContainerRegion binding), so a value allocated there lives in the target's region.
func (a *Analyzer) checkStoredRegionContainerEscapeInto(valueExpr ast.Expr, valueType Type, targetRegion string) {
	saved := a.storeTargetRegion
	a.storeTargetRegion = targetRegion
	defer func() { a.storeTargetRegion = saved }()
	a.checkStoredRegionContainerEscape(valueExpr, valueType)
}

// exprIsRegionPolyResultCarryingRegionData reports whether an expression is a call to a
// region-polymorphic function whose RETURN is proven to allocate in the caller's threaded arena
// and whose result carries region storage. Region-polymorphism can also be required solely by an
// out-parameter write; that does not transfer the hidden arena's ownership to an unrelated return
// value (which may instead borrow a caller parameter or be static).
func (a *Analyzer) exprIsRegionPolyResultCarryingRegionData(valueExpr ast.Expr, valueType Type) bool {
	if a == nil || valueExpr == nil {
		return false
	}
	call, ok := unwrapParenForRegionPoly(valueExpr).(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	ft := a.regionPolyCalleeFuncType(call)
	if ft == nil || !ft.RegionPolymorphic || !a.regionPolyReturnsCallerArena[ft] {
		return false
	}
	return typeCarriesRegionStorage(valueType)
}

func (a *Analyzer) checkRegionContainerEscape(valueExpr ast.Expr, valueType Type, via string) {
	if a == nil || valueExpr == nil {
		return
	}
	region := containerRegion(valueType)
	if region == "" && (isRegionBorrowedStringOrViewType(valueType) || isRegionBorrowedAggregateType(valueType)) {
		// Contextual checking against a region-less return/assignment type can erase the
		// stamped region from valueType. Recover it from the source expression instead;
		// in particular, a slice borrows the region of its underlying cstr/sview/view.
		region = a.returnBorrowedRegion(valueExpr)
	}
	if region == "" {
		region = a.declaredCallValueRegion(valueExpr)
	}
	if region == "" {
		// A region-polymorphic call's result is allocated in the AMBIENT region even though its
		// type carries no region (region-poly returns are region-less by convention — the region
		// rides the threaded arena, not the type). If the result transitively holds region-allocated
		// data (a container/ref field, possibly inside a returned struct), it escapes exactly like a
		// region-stamped container. Recover the region from the active alloc scope so the checks below
		// fire. Gated callers already restrict this to targets that outlive the function.
		if a.activeContainerRegionName() != "" && a.exprIsRegionPolyResultCarryingRegionData(valueExpr, valueType) {
			region = a.activeContainerRegionName()
		}
	}
	if region == "" && via == "store into longer-lived storage" && typeCarriesRegionStorage(valueType) {
		// A struct local (or a field path of one) whose container fields were allocated in a
		// recorded local region: storing it copies headers that still point into that region
		// (`o.inner <- local`, `o.inner.xs <- local.xs`).
		region = a.structLocalArgRegion(valueExpr)
	}
	if region == "" {
		return
	}
	if a.lookupRegionParam(region) {
		return // caller-owned region — fine.
	}
	if sym, _ := a.lookupRegionState(region); sym != nil {
		if a.isCompilerGeneratedAutoRegion(region) {
			// Build-local-return (region-return-inference Stage 1): in a region-polymorphic
			// function the synthesized `__auto_*` region is threaded from the caller (the hidden
			// `__region_auto` Arena&) and the backend adopts it (regionPolyAutoAdopts), so a value
			// RETURNED from it outlives the call — no escape. This MUST mirror regionPolyAutoAdopts
			// exactly (RegionPolymorphic + return + synthesized-auto-region), or a front-end
			// suppression the backend doesn't adopt would be a silent use-after-free. Only `return`
			// threads the region: a store into longer-lived storage (via != "return") still escapes,
			// because there is no caller region for the stored value to live in.
			if via == "return" && a.currentFuncType != nil && a.currentFuncType.RegionPolymorphic &&
				funcTypeHasImplicitParam(a.currentFuncType, regionPolymorphicImplicitParamName) {
				return
			}
			// A store into the place whose region the ambient allocation region is bound to
			// (the struct-field rehash shape `p.slots <- fresh`): the auto region allocates
			// into the caller's arena for exactly that target, so nothing dangles.
			if a.storeTargetRegion != "" && a.currentFuncDecl != nil && a.currentFuncDecl.AmbientGrownContainerRegion == a.storeTargetRegion {
				a.noteAmbientFillFresh()
				return
			}
			// Destination-driven allocation: the region-poly call's hidden region was bound to
			// the store target's own caller-owned region, so its result lives there.
			if a.destinationAllocatedInto(valueExpr, a.storeTargetRegion) {
				return
			}
			a.errorf(valueExpr.Pos(), "value escapes its `in auto:` scope via %s; the inferred region is freed at scope exit. Give it an explicit lifetime — copy it into a caller-provided region param (def f[@r] ... -> ... @r) or a longer-lived region", via)
		} else {
			a.errorf(valueExpr.Pos(), "value allocated in region %q escapes via %s; the region is freed at scope exit. Copy it into a caller-provided region param (def f[@r] ... -> ... @r) or a longer-lived region first", region, via)
		}
	}
}

func isRegionBorrowedStringOrViewType(t Type) bool {
	switch StripAggregateStateType(stripRefForBounds(t)).(type) {
	case *CStrType, *SViewType, *ViewType:
		return true
	default:
		return false
	}
}

func isRegionBorrowedAggregateType(t Type) bool {
	switch StripAggregateStateType(t).(type) {
	case *TupleType, *ArrayType, *OptionalType, *ErrorUnionType:
		return typeCarriesRegionStorage(t)
	}
	return false
}

// isSynthesizedAutoRegion reports whether a region name is one the compiler created
// for an `in auto:` block (see synthesizedAutoRegionName in the parser).
func isSynthesizedAutoRegion(name string) bool {
	const prefix = "__auto_"
	return len(name) > len(prefix) && name[:len(prefix)] == prefix
}

// isCompilerGeneratedAutoRegionStmt identifies the parser/semantic pass' lazy
// auto scopes. The reserved-looking name is not sufficient provenance: source
// code may declare a region with the same prefix.
func isCompilerGeneratedAutoRegionStmt(region *ast.RegionStmt) bool {
	return region != nil && region.Lazy && !region.UserAuto && isSynthesizedAutoRegion(region.Name)
}

func isCompilerGeneratedAutoRegionSymbol(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	region, ok := sym.Node.(*ast.RegionStmt)
	return ok && isCompilerGeneratedAutoRegionStmt(region)
}

// isCompilerGeneratedAutoRegion resolves a live region name back to its AST
// declaration before applying auto-region lifetime exemptions. Source-declared
// regions and source `in auto:` blocks never adopt a caller arena.
func (a *Analyzer) isCompilerGeneratedAutoRegion(name string) bool {
	if a == nil || a.currentScope == nil || !isSynthesizedAutoRegion(name) {
		return false
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || sym.Kind != SymbolRegion {
		return false
	}
	region, ok := sym.Node.(*ast.RegionStmt)
	return ok && isCompilerGeneratedAutoRegionStmt(region)
}
