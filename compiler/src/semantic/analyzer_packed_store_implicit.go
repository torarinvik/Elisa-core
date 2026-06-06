package semantic

import (
	"reflect"
	"sort"
	"strings"

	"elisacore/src/ast"
)

// collectRegionBackedPackedEnums returns the set of packed enums that are allocated via `new[auto]`
// somewhere in the program. ONLY these are region-backed (implicit-store) enums. An enum used purely
// with explicit stores (`new[store]`, `match in store`, frozen `.Store`) is never new[auto]-built,
// so it must NOT receive implicit store threading — that would break the legacy explicit-store code
// (e.g. the JSON parser's JsonNode). This is the precise gate on implicit packed-store injection.
func (a *Analyzer) collectRegionBackedPackedEnums(funcs []*ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if alloc, ok := v.Interface().(*ast.AllocExpr); ok && alloc != nil && alloc.AutoRegion {
				if enumType, _, ok := a.packedAllocConstructorInfo(alloc.Value); ok && enumType != nil && enumType.Packed {
					out[enumType.Name] = true
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	for _, fn := range funcs {
		if fn != nil {
			rec(reflect.ValueOf(fn.Body))
		}
	}
	return out
}

// packedStoreImplicitParamName is the synthetic name of the hidden store parameter threaded into a
// function that exposes a region-backed packed enum in its signature (docs/74). It carries the
// implicit per-region PackedStoreState so the function can build (`new[auto]`) or match the bare
// handle without an explicit `Store` value written by hand.
func packedStoreImplicitParamName(enumName string) string {
	return "__packed_store_" + sanitizeImplicitTempBase(enumName)
}

// injectInferredPackedStoreParams threads an implicit `E.Store[Local]` parameter into a function that
// exposes a packed enum E in its signature (an explicit parameter or its return type). A bare packed
// handle is a u32 index that is only meaningful against one PackedStoreState, so any function that
// builds or consumes such a handle must share that store — this injects it as a hidden param,
// threaded at call sites (the root call creates the region-backed store; the rest reuse it).
//
// It deliberately does NOT fire when the function already threads an EXPLICIT `E.Store` param: that
// is the legacy explicit-store style, which must keep working unchanged. Idempotent.
func (a *Analyzer) injectInferredPackedStoreParams(fnType *FuncType, regionBacked map[string]bool) {
	if fnType == nil || len(regionBacked) == 0 {
		return
	}
	enums := map[string]*EnumType{}
	explicitCount := funcTypeExplicitParamCount(fnType)
	for i := 0; i < explicitCount && i < len(fnType.Params); i++ {
		collectPackedEnumsInType(fnType.Params[i], enums)
	}
	collectPackedEnumsInType(fnType.Return, enums)
	if len(enums) == 0 {
		return
	}
	names := make([]string, 0, len(enums))
	for name := range enums {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, enumName := range names {
		if !regionBacked[enumName] {
			continue // explicit-store enum — never thread an implicit store (would regress legacy code)
		}
		enumType := enums[enumName]
		if enumType == nil || enumType.StoreType == nil {
			continue
		}
		paramName := packedStoreImplicitParamName(enumName)
		if funcTypeHasImplicitParam(fnType, paramName) || funcTypeHasExplicitPackedStoreParam(fnType, enumName) {
			continue
		}
		storeType := PackedEnumStoreWithState(enumType.StoreType, a.namedTypes["Local"])
		fnType.Params = append(fnType.Params, storeType)
		fnType.ImplicitParamNames = append(fnType.ImplicitParamNames, paramName)
	}
}

// funcTypeHasExplicitPackedStoreParam reports whether an explicit parameter is already an `E.Store`
// (any state). When true the function threads its store by hand — the legacy explicit style — and
// must not also receive an implicit store.
func funcTypeHasExplicitPackedStoreParam(fnType *FuncType, enumName string) bool {
	if fnType == nil {
		return false
	}
	explicitCount := funcTypeExplicitParamCount(fnType)
	for i := 0; i < explicitCount && i < len(fnType.Params); i++ {
		if storeType, ok := StripAggregateStateType(fnType.Params[i]).(*PackedEnumStoreType); ok && storeType != nil && storeType.Enum != nil && storeType.Enum.Name == enumName {
			return true
		}
	}
	return false
}

// defineImplicitPackedStoreParamSymbols binds each implicit `__packed_store_E` parameter as a local
// symbol in the function body and registers it as the active store for its enum, so `new[auto] E.V`
// and storeless `match` resolve to the threaded store, and a recursive call threads it onward.
func (a *Analyzer) defineImplicitPackedStoreParamSymbols(fn *ast.FuncDecl, fnType *FuncType) {
	if fnType == nil {
		return
	}
	explicitCount := funcTypeExplicitParamCount(fnType)
	for i, name := range fnType.ImplicitParamNames {
		if !strings.HasPrefix(name, "__packed_store_") {
			continue
		}
		idx := explicitCount + i
		if idx >= len(fnType.Params) {
			continue
		}
		storeType, ok := fnType.Params[idx].(*PackedEnumStoreType)
		if !ok || storeType == nil {
			continue
		}
		if _, exists := a.currentScope.Lookup(name); !exists {
			sym := &Symbol{Name: name, Kind: SymbolParam, Type: storeType, Node: fn, ParamIndex: idx}
			a.defineLocal(sym, fn.Pos())
		}
		a.bindActivePackedStoreType(storeType)
	}
}

// packedStoreImplicitArgExpr is the synthetic argument threaded for an implicit packed store param.
// In a caller that already has the store (its own implicit param), the same-name lookup resolves it;
// otherwise the backend creates the region-backed store on demand at this call site.
func packedStoreImplicitArgExpr(storeType *PackedEnumStoreType) ast.Expr {
	if storeType == nil || storeType.Enum == nil {
		return &ast.Ident{Name: "__packed_store_"}
	}
	return &ast.Ident{Name: packedStoreImplicitParamName(storeType.Enum.Name)}
}

func collectPackedEnumsInType(t Type, out map[string]*EnumType) {
	switch tt := StripAggregateStateType(t).(type) {
	case *RefType:
		collectPackedEnumsInType(tt.Elem, out)
	case *OptionalType:
		collectPackedEnumsInType(tt.Value, out)
	case *ErrorUnionType:
		collectPackedEnumsInType(tt.Value, out)
	case *EnumType:
		if tt != nil && tt.Packed {
			out[tt.Name] = tt
		}
	case *PackedVariantViewType:
		if tt != nil && tt.Enum != nil && tt.Enum.Packed {
			out[tt.Enum.Name] = tt.Enum
		}
	}
}
