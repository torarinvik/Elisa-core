package semantic

import (
	"reflect"
	"sync"
	"unsafe"
)

func parallelBodyWorkerRebuildMap(field string) bool {
	switch field {
	case "storeScanIndexCache", "storeFlowBinderIndexes", "storeFlowInProgress", "typeParamMemo", "storeFlowFrozenStructLookup",
		"storeFlowActive", "regionPointeesMemo":
		return true
	default:
		return false
	}
}

// Completed summaries never change their connectivity. Edge-kind solving does
// mutate retKind/reachKind, so give workers private summary objects and kind
// slices while retaining the settled (potentially large) connectivity matrices.
func parallelBodyWorkerLocalOnlyMap(field string) bool {
	switch field {
	case "callEdgeCache", "storeFlowSCC", "storeFlowInProgress", "storeFlowActive", "storageViewCalleeWriteCache", "regionPointeesMemo", "typeParamMemo", "storeFlowFrozenStructLookup":
		return true
	// Equation eligibility rests on callees' verified termination, which the exporting
	// worker may not have known yet. Each worker recomputes it from the termination
	// facts it holds instead of importing a possibly stale verdict.
	case "definingEquationCache", "viewEquationCache", "definingEquationInProgress":
		return true
	default:
		return false
	}
}

func parallelBodyMemoMap(field string) bool {
	switch field {
	case "affineHandleMemo", "borrowedOwnerRefMemo", "regionRefsMemo", "mutableBorrowCapabilityMemo", "storeScanAddressMemo",
		"storeScanPlainValueMemo", "storeScanProgramLivedMemo",
		"programLivedTypes", "smtLocalDefDeps", "resolvedTypeNames", "abstractParamRegionRefMemo", "abstractRefRestartMemo":
		return true
	default:
		return false
	}
}

// Declaration indexes are complete before body workers start. Sharing their map
// headers avoids reflecting over large, nested indexes once per worker; mutable
// analysis caches and per-body facts still receive isolated maps.
func parallelDeclarationMapIsReadOnly(field string) bool {
	switch field {
	case "namedTypes", "staticInterfaces", "staticImpls", "effectHandlers", "permissions", "capabilityAliases",
		"extensionMethodsByName", "ufcsFunctionsByName", "ufcsByLastSegment", "functionTypes", "constValues",
		"ambientGrownCalleeParamIndex", "regionParamCalleeParamIndex", "declVisibility", "declaredFuncNames", "callDeclsByName",
		"declaredModules", "trustedRuntimeSourceFiles", "storageViewWriteDeclsByName", "aliasRefinements",
		"parametricAliasRefinements", "externLinkNames", "privateTypeNames", "privateModules", "castHooksByName",
		"initHooksByName", "dropHooks", "packedStoreBuilders", "regionPolyCandidateFnTypes",
		"regionPolyReturnsCallerArena":
		return true
	default:
		return false
	}
}

// settable makes an unexported field writable through reflection.
func settable(v reflect.Value) reflect.Value {
	if v.CanSet() || !v.CanAddr() {
		return v
	}
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

// deepCloneValue copies maps at every depth reachable through structs, arrays and
// map values, so a worker's in-place writes never reach another worker. Slices
// keep their elements but get capacity == length, so an append reallocates.
// Pointers, AST nodes and types included, stay shared.
func deepCloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		if v.Len() == 0 {
			return out
		}
		iter := v.MapRange()
		if v.Len() < 2 && !typeHoldsMapInline(v.Type().Elem()) {
			for iter.Next() {
				out.SetMapIndex(iter.Key(), deepCloneValue(iter.Value()))
			}
			return out
		}
		// Iterator Key/Value calls box indirect values. Reuse addressable slots;
		// SetMapIndex copies each slot's contents into the destination entry.
		var keySlot, valueSlot, structSlot reflect.Value
		if !parallelCloneDirectReflectKind(v.Type().Key().Kind()) {
			keySlot = reflect.New(v.Type().Key()).Elem()
		}
		if !parallelCloneDirectReflectKind(v.Type().Elem().Kind()) {
			valueSlot = reflect.New(v.Type().Elem()).Elem()
		}
		cloneStruct := v.Type().Elem().Kind() == reflect.Struct && typeHoldsMapInline(v.Type().Elem())
		if cloneStruct {
			structSlot = reflect.New(v.Type().Elem()).Elem()
		}
		for iter.Next() {
			var key, value reflect.Value
			if keySlot.IsValid() {
				keySlot.SetIterKey(iter)
				key = keySlot
			} else {
				key = iter.Key()
			}
			if valueSlot.IsValid() {
				valueSlot.SetIterValue(iter)
				value = valueSlot
			} else {
				value = iter.Value()
			}
			if cloneStruct {
				cloneParallelStructInto(structSlot, value)
				out.SetMapIndex(key, structSlot)
			} else {
				out.SetMapIndex(key, deepCloneValue(value))
			}
		}
		return out
	case reflect.Struct:
		if !typeHoldsMapInline(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		cloneParallelStructInto(out, v)
		return out
	case reflect.Array:
		if !typeHoldsMapInline(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.Len(); i++ {
			target := settable(out.Index(i))
			target.Set(deepCloneValue(target))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		return v.Slice3(0, v.Len(), v.Len())
	}
	return v
}

// These kinds fit directly in reflection values, so iterators do not box them.
func parallelCloneDirectReflectKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Chan, reflect.Map, reflect.Func:
		return true
	default:
		return false
	}
}

func cloneParallelStructInto(out, source reflect.Value) {
	out.Set(source)
	for _, i := range parallelCloneStructFields(source.Type()) {
		target := settable(out.Field(i))
		if target.Kind() == reflect.Struct && typeHoldsMapInline(target.Type()) {
			cloneParallelStructInto(target, target)
		} else {
			target.Set(deepCloneValue(target))
		}
	}
}

var parallelCloneStructFieldCache sync.Map // reflect.Type -> []int

// The initial struct copy already handles fields with no inline maps or
// slices. Cache only fields that need a recursive isolation copy.
func parallelCloneStructFields(t reflect.Type) []int {
	if fields, ok := parallelCloneStructFieldCache.Load(t); ok {
		return fields.([]int)
	}
	var fields []int
	for i := 0; i < t.NumField(); i++ {
		if typeHoldsMapInline(t.Field(i).Type) {
			fields = append(fields, i)
		}
	}
	actual, _ := parallelCloneStructFieldCache.LoadOrStore(t, fields)
	return actual.([]int)
}

var mapInlineCache sync.Map

// typeHoldsMapInline reports whether a value of t carries a map or slice without
// going through a pointer.
func typeHoldsMapInline(t reflect.Type) bool {
	if cached, ok := mapInlineCache.Load(t); ok {
		return cached.(bool)
	}
	holds := false
	switch t.Kind() {
	case reflect.Map, reflect.Slice:
		holds = true
	case reflect.Array:
		holds = typeHoldsMapInline(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField() && !holds; i++ {
			holds = typeHoldsMapInline(t.Field(i).Type)
		}
	}
	mapInlineCache.Store(t, holds)
	return holds
}

// mergeValue folds what a worker changed relative to base into dst: new or
// replaced map entries (nested maps merged entry by entry), slice tails appended
// past the base length, and changed scalars and pointers.
func mergeValue(dst, w, base reflect.Value, p *parallelWorkerState) {
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("optimizationSymbolIdentityOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectOptimizationSymbolIdentityOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}

	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("storeScanBinderOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectStoreScanBinderOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("storeScanParamFactsOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectStoreScanParamFactsOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("noStorageOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectNoStorageOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("iterBinderElementOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectIterBinderElementOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("exprFactsOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectExprFactsOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("exprTypeOverlay")).IsNil() {
		// A construction view cannot be merged into canonical analysis. Its
		// base is immutable and private tombstones are not canonical deltas.
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectExprTypeOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	switch w.Kind() {
	case reflect.Struct:
		for i := 0; i < w.NumField(); i++ {
			if w.Type() == reflect.TypeOf(Analyzer{}) && (w.Type().Field(i).Name == "proofLifecycle" || w.Type().Field(i).Name == "proofReport") {
				continue
			}
			if p != nil {
				name := w.Type().Field(i).Name
				if name == "exprTypeOverlay" || name == "exprFactsOverlay" || name == "iterBinderElementOverlay" || name == "noStorageOverlay" || name == "optimizationSymbolIdentityOverlay" || name == "storeScanBinderOverlay" || name == "storeScanParamFactsOverlay" || parallelBodyWorkerLocalOnlyMap(name) || name == "smtSolver" || name == "smtStats" || name == "smtUnavailable" {
					continue
				}
			}
			mergeValue(settable(dst.Field(i)), settable(w.Field(i)), settable(base.Field(i)), p)
		}
	case reflect.Map:
		if w.IsNil() {
			return
		}
		if dst.IsNil() {
			dst.Set(reflect.MakeMap(w.Type()))
		}
		iter := w.MapRange()
		for iter.Next() {
			k, v := iter.Key(), iter.Value()
			if !p.ownsKey(k) {
				continue
			}
			var bv reflect.Value
			if !base.IsNil() {
				bv = base.MapIndex(k)
			}
			if v.Kind() == reflect.Map && !v.IsNil() {
				if dv := dst.MapIndex(k); dv.IsValid() && !dv.IsNil() {
					if !bv.IsValid() {
						bv = reflect.Zero(v.Type())
					}
					mergeValue(dv, v, bv, p)
					continue
				}
			}
			if bv.IsValid() && sameValue(bv, v) {
				continue
			}
			dst.SetMapIndex(k, v)
		}
	case reflect.Slice:
		if w.Len() > base.Len() && (base.Len() == 0 || w.Pointer() == base.Pointer()) {
			dst.Set(reflect.AppendSlice(dst, w.Slice(base.Len(), w.Len())))
		} else if w.Len() > base.Len() || (w.Pointer() != base.Pointer() && w.Len() != base.Len()) {
			dst.Set(w)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Counters: every worker's increments survive.
		dst.SetInt(dst.Int() + w.Int() - base.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		dst.SetUint(dst.Uint() + w.Uint() - base.Uint())
	default:
		if !sameValue(base, w) {
			dst.Set(w)
		}
	}
}

// mergeRequiresReport folds a worker's requires-report aggregation into a's: the
// entries are per-worker pointers keyed by clause, so equal keys are summed. The
// worker's copy is reset to the base so the generic merge leaves it alone.
func mergeRequiresReport(a, w *Analyzer, base reflect.Value) {
	for _, key := range w.requiresReportOrder {
		entry := w.requiresReportData[key]
		if entry == nil {
			continue
		}
		if a.requiresReportData == nil {
			a.requiresReportData = map[requiresReportKey]*RequiresReportEntry{}
		}
		have := a.requiresReportData[key]
		switch {
		case have == nil:
			copied := *entry
			a.requiresReportData[key] = &copied
			a.requiresReportOrder = append(a.requiresReportOrder, key)
		case have != entry:
			have.Provable += entry.Provable
			have.Unprovable += entry.Unprovable
			have.UnprovableSites = append(have.UnprovableSites, entry.UnprovableSites...)
		}
	}
	b := base.Interface().(Analyzer)
	w.requiresReportData, w.requiresReportOrder = b.requiresReportData, b.requiresReportOrder
}

// sameValue reports whether a worker left a value unchanged: equal for
// comparable values, same identity for maps, slices and funcs.
func sameValue(x, y reflect.Value) (same bool) {
	switch x.Kind() {
	case reflect.Map, reflect.Slice:
		return x.Pointer() == y.Pointer() && x.Len() == y.Len()
	case reflect.Func, reflect.UnsafePointer, reflect.Chan:
		return x.Pointer() == y.Pointer()
	}
	if !x.Type().Comparable() {
		return false
	}
	// An interface holding an incomparable dynamic value panics on ==.
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return x.Interface() == y.Interface()
}
