package semantic

import (
	"os"
	"reflect"

	"elisacore/src/ast"
)

var debugRegionContainers = os.Getenv("ELISA_REGION_DEBUG") != ""

// Region-parameterized containers. See REGION_CONTAINERS_DESIGN.md. This stamps
// a container's allocation region from the enclosing `in <region>:` scope. The
// stamped Region is load-bearing: escape checks (return/store/nested-store),
// flow destruction-invalidation, region-param binding, and backend arena
// routing (regionArenaOwner) all consult it.

// stampContainerRegion returns t with its container region inferred from the
// active allocation scope, if it is a region-less darray and a region scope is
// active. Returns a copy (never mutates the input type) so shared/annotation
// types are not polluted.
func (a *Analyzer) stampContainerRegion(t Type) Type {
	if a == nil || t == nil {
		return t
	}
	region := a.activeContainerRegionName()
	if region == "" {
		return t
	}
	switch c := t.(type) {
	case *DArrayType:
		if c != nil && c.Region == "" {
			cp := *c
			cp.Region = region
			if debugRegionContainers {
				println("REGIONSTAMP darray ->", region)
			}
			return &cp
		}
	case *DictType:
		if c != nil && c.Region == "" {
			cp := *c
			cp.Region = region
			return &cp
		}
	case *SetType:
		if c != nil && c.Region == "" {
			cp := *c
			cp.Region = region
			return &cp
		}
	}
	return t
}

// (semanticContainerDArray removed — superseded by containerRegion.)

// containerRegion peels ref wrappers and returns the allocation region of the
// underlying container (darray or dict), or "" if t is not a region-carrying
// container.
func containerRegion(t Type) string {
	for {
		switch tt := t.(type) {
		case *DArrayType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *DictType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *SetType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *DStrType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *SViewType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *ViewType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *RefType:
			if tt == nil {
				return ""
			}
			t = tt.Elem
		default:
			return ""
		}
	}
}

// checkReturnRegionContainerEscape rejects returning a container allocated in a
// *scope-owned* region (`region NAME(...):`), which would dangle once the region
// is freed at scope exit. Borrowed arenas (`in <arena>:`, region == arena var,
// not a tracked region) and caller-provided region params are fine: those
// outlive the call. This is the bucket-granular analogue of Rust's
// "reference does not outlive its referent".
func (a *Analyzer) checkReturnRegionContainerEscape(valueExpr ast.Expr, valueType Type) {
	a.checkRegionContainerEscape(valueExpr, valueType, "return")
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
		return
	}
	a.errorf(value.Pos(), "value backed by scope-owned region %q escapes via return; the region is freed at block exit, leaving its containers/stores dangling. Build it into a caller-owned region instead — take a region param (`def f[@r] ... @r`) or an `Arena&` the caller owns, rather than a local `region %s(...):` block", region, region)
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
	case *DArrayType, *DictType, *SetType, *DStrType, *SViewType, *ViewType:
		return true
	case *RefType:
		if tt == nil {
			return false
		}
		return typeCarriesRegionStorageRec(tt.Elem, seen)
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
	ft, _ := a.exprTypes[call.Func].(*FuncType)
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

// exprIsRegionPolyResultCarryingRegionData reports whether an expression is a call to a
// region-polymorphic function whose result transitively holds region-allocated data. Such a
// result lives in the ambient (threaded) region even though its type is region-less, so a store
// of it into longer-lived storage dangles once that region is freed (the ref-param / global-store
// soundness case). A pure-scalar result (no container/ref/nested region) can never dangle.
func (a *Analyzer) exprIsRegionPolyResultCarryingRegionData(valueExpr ast.Expr, valueType Type) bool {
	if a == nil || valueExpr == nil {
		return false
	}
	call, ok := unwrapParenForRegionPoly(valueExpr).(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	ft := a.regionPolyCalleeFuncType(call)
	if ft == nil || !ft.RegionPolymorphic {
		return false
	}
	return typeCarriesRegionStorage(valueType)
}

func (a *Analyzer) checkRegionContainerEscape(valueExpr ast.Expr, valueType Type, via string) {
	if a == nil || valueExpr == nil {
		return
	}
	region := containerRegion(valueType)
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
	if region == "" {
		return
	}
	if a.lookupRegionParam(region) {
		return // caller-owned region — fine.
	}
	if sym, _ := a.lookupRegionState(region); sym != nil {
		if isSynthesizedAutoRegion(region) {
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
			a.errorf(valueExpr.Pos(), "value escapes its `in auto:` scope via %s; the inferred region is freed at scope exit. Give it an explicit lifetime — copy it into a caller-provided region param (def f[@r] ... -> ... @r) or a longer-lived region", via)
		} else {
			a.errorf(valueExpr.Pos(), "value allocated in region %q escapes via %s; the region is freed at scope exit. Copy it into a caller-provided region param (def f[@r] ... -> ... @r) or a longer-lived region first", region, via)
		}
	}
}

// isSynthesizedAutoRegion reports whether a region name is one the compiler created
// for an `in auto:` block (see synthesizedAutoRegionName in the parser).
func isSynthesizedAutoRegion(name string) bool {
	const prefix = "__auto_"
	return len(name) > len(prefix) && name[:len(prefix)] == prefix
}

// regionLifetimeOrdinal returns a region's position in the outlives-lattice: a
// LOWER ordinal means a longer-lived region. Region params are caller-owned and
// outlive every local region (ordinal 0). Local regions get their declaration
// ordinal (1+). Any other region identity ('heap, a borrowed `in <arena>:`, or
// an unknown name) is treated as outermost (0) — borrowed/process arenas outlive
// local regions. The second return reports whether the region is *comparable*
// (a known region param or tracked local region); incomparable regions must not
// drive a rejection.
func (a *Analyzer) regionLifetimeOrdinal(region string) (int, bool) {
	if a == nil || region == "" {
		return 0, false
	}
	if a.lookupRegionParam(region) {
		return 0, true
	}
	if ord, ok := a.regionLifetimeOrdinals[region]; ok {
		return ord, true
	}
	return 0, false
}

// regionOutlives reports whether region `outer` strictly outlives region `inner`
// — i.e. `inner` is freed first, so a pointer into `inner` stored in `outer`'s
// bucket would dangle. Only decides when both regions are comparable.
func (a *Analyzer) regionOutlives(outer, inner string) bool {
	if outer == inner {
		return false
	}
	oo, ok1 := a.regionLifetimeOrdinal(outer)
	io, ok2 := a.regionLifetimeOrdinal(inner)
	if !ok1 || !ok2 {
		return false
	}
	return oo < io
}

// checkNestedRegionStoreEscape rejects storing a value whose region is freed
// before the target slot's region — e.g. an inner-`@r` container/ref written
// into an outer-region object (`darray[T] @outer`'s element typed `@inner`, a
// field `@inner` of an `@outer` struct, etc.). Once the inner region is freed,
// the still-live outer storage holds a dangling pointer. This is the nested-
// region case of the escape property, decided by the region outlives-lattice.
// The existing function-outliving store check (checkStoredRegionContainerEscape)
// covers the global / process-lifetime target; this covers the local outer ->
// inner case the lattice makes precise.
func (a *Analyzer) checkNestedRegionStoreEscape(targetExpr ast.Expr, targetType, valueType Type) {
	if a == nil || targetExpr == nil {
		return
	}
	valueRegion := containerOrEntryRegion(valueType)
	if valueRegion == "" {
		return
	}
	targetRegion := containerOrEntryRegion(targetType)
	if targetRegion == "" {
		// Region-less target slot (a plain local, or a field of a region-less
		// struct). The slot still dangles if its backing storage outlives the
		// value's region block: a binding declared in a scope that strictly
		// encloses the region's declaration scope survives the region's exit.
		a.checkRegionlessTargetStoreEscape(targetExpr, valueRegion)
		return
	}
	if a.regionOutlives(targetRegion, valueRegion) {
		a.errorf(targetExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", valueRegion, targetRegion, valueRegion, targetRegion, targetRegion)
	}
}

// checkNestedRegionElementStoreEscape is the container-mutation form of the
// nested-region store check (push / dict.put / set.add). Element types are
// never region-stamped, so when the slot's own type carries no region the
// element's true lifetime is the CONTAINER's region — fall back to it.
func (a *Analyzer) checkNestedRegionElementStoreEscape(argExpr ast.Expr, containerType, elemType, valueType Type) {
	if a == nil || argExpr == nil {
		return
	}
	valueRegion := containerOrEntryRegion(valueType)
	if valueRegion == "" {
		return
	}
	targetRegion := containerOrEntryRegion(elemType)
	if targetRegion == "" {
		targetRegion = containerRegion(containerType)
	}
	if targetRegion == "" {
		return
	}
	if a.regionOutlives(targetRegion, valueRegion) {
		a.errorf(argExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", valueRegion, targetRegion, valueRegion, targetRegion, targetRegion)
	}
}

// checkNestedRegionBulkStoreEscape is the bulk form of the element-store check
// (darray.extend, bulk darray.push from a darray/view/array source). The values
// copied in are the SOURCE container's elements; when the element type itself
// carries region storage (a nested container/view/ref), those elements live in
// the source container's region, so copying them into a longer-lived target
// container leaves dangling references once the source region is freed. Scalar
// (pointer-free) elements can never dangle, so they are exempt — a per-element
// region stamp is never available for a bulk source, so the source container's
// own region is the proxy for its elements' lifetime.
func (a *Analyzer) checkNestedRegionBulkStoreEscape(sourceExpr, receiverExpr ast.Expr, containerType, elemType, sourceType Type) {
	if a == nil || sourceExpr == nil {
		return
	}
	if !typeCarriesRegionStorage(elemType) {
		return // scalar / pointer-free elements can't dangle.
	}
	valueRegion := containerRegion(sourceType)
	if valueRegion == "" {
		return
	}
	targetRegion := containerOrEntryRegion(elemType)
	if targetRegion == "" {
		targetRegion = containerRegion(containerType)
	}
	if targetRegion == "" {
		// Region-less target container (a plain local declared outside the
		// source's region block): dangles if its scope outlives the region.
		if receiverExpr != nil {
			a.checkRegionlessTargetStoreEscape(receiverExpr, valueRegion)
		}
		return
	}
	if a.regionOutlives(targetRegion, valueRegion) {
		a.errorf(sourceExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", valueRegion, targetRegion, valueRegion, targetRegion, targetRegion)
	}
}

// structInteriorTaintRegion returns the innermost region a region-less aggregate
// local was tainted with (an interior container/view field stored an inner-region
// value), peeling parens/casts/moves down to the root identifier. "" when the
// expression is not a tainted local.
func (a *Analyzer) structInteriorTaintRegion(expr ast.Expr) string {
	if a == nil || a.currentScope == nil || a.currentStructInteriorRegionTaint == nil {
		return ""
	}
	ident, ok := stripParenExpr(expr).(*ast.Ident)
	if !ok || ident == nil {
		return ""
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return ""
	}
	return a.currentStructInteriorRegionTaint[sym]
}

// valueStoreRegion returns the innermost (shortest-lived) tracked region a stored
// value carries: a container/view's own stamped region, or — for a region-less
// aggregate — the interior-field taint recorded for its source local. Only
// lattice-comparable (tracked-local) regions are reported; "" otherwise.
func (a *Analyzer) valueStoreRegion(expr ast.Expr, valueType Type) string {
	if r := containerRegion(valueType); r != "" {
		if _, ok := a.regionLifetimeOrdinal(r); ok {
			return r
		}
	}
	if r := a.structInteriorTaintRegion(expr); r != "" {
		return r
	}
	return ""
}

// innerRegion returns whichever of two regions is shorter-lived (higher lifetime
// ordinal). Incomparable regions defer to the comparable one.
func (a *Analyzer) innerRegion(r1, r2 string) string {
	if r1 == "" {
		return r2
	}
	if r2 == "" || r1 == r2 {
		return r1
	}
	o1, ok1 := a.regionLifetimeOrdinal(r1)
	o2, ok2 := a.regionLifetimeOrdinal(r2)
	if !ok1 {
		return r2
	}
	if !ok2 {
		return r1
	}
	if o2 > o1 {
		return r2
	}
	return r1
}

// recordStructInteriorRegionTaint maintains the interior-field taint side-table on
// an assignment/binding. A field store `base.field <- v` (or any deeper path with a
// local root) taints the root aggregate with v's region; a whole-aggregate copy
// `dest <- src`/`let dest = src` of a tainted region-less struct propagates the
// taint so the dangling interior reference is tracked through copy chains. This is
// what lets checkStructCopyInteriorRegionEscape reject laundering a region-less
// struct's dangling interior field out past the region's death.
func (a *Analyzer) recordStructInteriorRegionTaint(target, value ast.Expr, valueType Type) {
	if a == nil || a.currentStructInteriorRegionTaint == nil || a.currentScope == nil {
		return
	}
	root := rootIdentExpr(target)
	if root == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(root.Name)
	if !ok || sym == nil {
		return
	}
	if _, isField := stripParenExpr(target).(*ast.FieldExpr); isField {
		// Field store into an aggregate: taint the root with the value's region.
		region := a.valueStoreRegion(value, valueType)
		if region == "" {
			return
		}
		a.currentStructInteriorRegionTaint[sym] = a.innerRegion(a.currentStructInteriorRegionTaint[sym], region)
		return
	}
	// Whole-aggregate assignment to a plain local: propagate the source's taint.
	if _, isIdent := stripParenExpr(target).(*ast.Ident); !isIdent {
		return
	}
	if !typeCarriesRegionStorage(valueType) || containerRegion(valueType) != "" {
		return
	}
	if region := a.structInteriorTaintRegion(value); region != "" {
		a.currentStructInteriorRegionTaint[sym] = a.innerRegion(a.currentStructInteriorRegionTaint[sym], region)
	}
}

// checkStructCopyInteriorRegionEscape rejects copying a region-less aggregate that
// holds an interior reference into an inner region (recorded via interior taint)
// into storage that outlives that region. A struct's TYPE never carries its
// fields' regions, so checkNestedRegionStoreEscape (which keys off the value type's
// region) is blind to this laundering; the taint side-table supplies the region.
func (a *Analyzer) checkStructCopyInteriorRegionEscape(targetExpr ast.Expr, targetType Type, valueExpr ast.Expr, valueType Type) {
	if a == nil || targetExpr == nil {
		return
	}
	// Only aggregates: a stamped container value's region is on its type and is
	// already handled by checkNestedRegionStoreEscape.
	if !typeCarriesRegionStorage(valueType) || containerRegion(valueType) != "" {
		return
	}
	valueRegion := a.structInteriorTaintRegion(valueExpr)
	if valueRegion == "" {
		return
	}
	targetRegion := containerOrEntryRegion(targetType)
	if targetRegion == "" {
		a.checkRegionlessTargetStoreEscape(targetExpr, valueRegion)
		return
	}
	if a.regionOutlives(targetRegion, valueRegion) {
		a.errorf(targetExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", valueRegion, targetRegion, valueRegion, targetRegion, targetRegion)
	}
}

// checkRegionlessTargetStoreEscape rejects storing region-allocated data into a
// region-less binding that outlives the region's block. The target outlives the
// region exactly when its defining scope strictly encloses the region's
// declaration scope (it was declared before the region block opened, so it is
// still live after the region is freed). Synthesized `in auto:` regions are
// excluded: their lifetimes are governed by the region-return/adoption
// machinery, not the explicit-region lattice.
func (a *Analyzer) checkRegionlessTargetStoreEscape(targetExpr ast.Expr, valueRegion string) {
	if isSynthesizedAutoRegion(valueRegion) {
		return
	}
	regionSym, state := a.lookupRegionState(valueRegion)
	if regionSym == nil || state.Destroyed || state.DeclScope == nil {
		return
	}
	root := rootIdentExpr(targetExpr)
	if root == nil {
		return
	}
	targetScope := a.definingScope(root.Name)
	if targetScope == nil || targetScope == state.DeclScope {
		return
	}
	if !scopeStrictlyEncloses(targetScope, state.DeclScope) {
		return
	}
	a.errorf(targetExpr.Pos(), "value in region %q is stored into %q, which outlives the region; region %q is freed first, leaving a dangling reference. Copy the value out of region %q (or declare %q inside the region block) instead", valueRegion, root.Name, valueRegion, valueRegion, root.Name)
}

// isFreshContainerProducer reports whether an expression CONSTRUCTS a fresh value whose interior may
// carry a shorter-lived region than where the value is stored: a list literal, comprehension,
// ternary (a runtime choice between branches), or struct literal. These are the forms whose OWN region
// (the target's) hides their elements'/fields' region, so they need the interior-region escape check.
func isFreshContainerProducer(expr ast.Expr) bool {
	switch stripParenExpr(expr).(type) {
	case *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.TernaryExpr, *ast.StructLitExpr:
		return true
	}
	return false
}

// valueInteriorRegion returns the innermost (shortest-lived) tracked region reachable through a
// value-producing expression's interior: the (non-spread) elements of a list literal or
// comprehension, BOTH branches of a ternary (either may be chosen at runtime), and the fields of a
// struct literal, recursing through these producer forms. A leaf is the expression's own
// container/entry region or recorded interior-field taint. "" when nothing region-carrying is
// reachable (scalars, region-less data).
func (a *Analyzer) valueInteriorRegion(expr ast.Expr) string {
	if a == nil || expr == nil {
		return ""
	}
	stripped := stripParenExpr(expr)
	switch n := stripped.(type) {
	case *ast.ListLitExpr:
		r := ""
		for i, elem := range n.Elems {
			if i < len(n.Spreads) && n.Spreads[i] {
				continue // spreads are bulk-checked at the store site (with the scalar-elem exemption).
			}
			r = a.innerRegion(r, a.valueInteriorRegion(elem))
		}
		return r
	case *ast.ListComprehensionExpr:
		r := a.valueInteriorRegion(n.Value)
		if n.Key != nil {
			r = a.innerRegion(r, a.valueInteriorRegion(n.Key))
		}
		return r
	case *ast.TernaryExpr:
		return a.innerRegion(a.valueInteriorRegion(n.Value), a.valueInteriorRegion(n.Alt))
	case *ast.StructLitExpr:
		r := ""
		for _, arg := range n.Args {
			r = a.innerRegion(r, a.valueInteriorRegion(arg))
		}
		for _, spread := range n.Spreads {
			r = a.innerRegion(r, a.valueInteriorRegion(spread))
		}
		return r
	default:
		return a.valueStoreRegion(stripped, a.exprTypes[stripped])
	}
}

// checkFreshContainerStoreEscape rejects storing a freshly-built container/struct whose INTERIOR
// carries a shorter-lived region than where it is stored. A list literal `[v]`, comprehension
// `[mk() for ...]`, ternary `[v if c else w]`, or struct literal `Holder(xs: v)` copies each element's
// /field's header; an interior typed `@inner` then dangles once the inner region is freed. The
// store-site check (checkNestedRegionStoreEscape) only sees the fresh value's OWN region - which is
// the target's - because element/field types are never region-stamped. This is the
// construction-expression analogue of the push/dict.put element-store check (which only fires on
// explicit container-mutation calls). Spread elements (`[...src]`) copy a whole source container, so
// they route through the bulk-store check (which exempts scalar elements).
func (a *Analyzer) checkFreshContainerStoreEscape(targetExpr ast.Expr, targetType Type, valueExpr ast.Expr) {
	if a == nil || valueExpr == nil {
		return
	}
	if lit, ok := stripParenExpr(valueExpr).(*ast.ListLitExpr); ok {
		elemType := dArrayLikeElemType(targetType)
		for i, elem := range lit.Elems {
			if elem != nil && i < len(lit.Spreads) && lit.Spreads[i] {
				a.checkNestedRegionBulkStoreEscape(elem, targetExpr, targetType, elemType, a.exprTypes[elem])
			}
		}
	}
	if !isFreshContainerProducer(valueExpr) {
		return
	}
	a.checkInteriorRegionAgainstTarget(targetExpr, containerRegion(targetType), valueExpr, "fresh container")
}

// checkInteriorRegionAgainstTarget rejects a value whose interior region is freed before the target
// storage: a longer-lived target region (or a region-less target that outlives the value's region)
// leaves the stored interior dangling.
func (a *Analyzer) checkInteriorRegionAgainstTarget(targetExpr ast.Expr, targetRegion string, valueExpr ast.Expr, via string) {
	valueRegion := a.valueInteriorRegion(valueExpr)
	if valueRegion == "" {
		return // scalar / region-less interior can't dangle.
	}
	if targetRegion == "" {
		a.checkRegionlessTargetStoreEscape(targetExpr, valueRegion)
		return
	}
	if a.regionOutlives(targetRegion, valueRegion) {
		a.errorf(valueExpr.Pos(), "value in region %q is stored into longer-lived region %q via a %s; region %q is freed first, leaving a dangling element. Copy it into region %q (or a region that outlives %q) before storing", valueRegion, targetRegion, via, valueRegion, targetRegion, targetRegion)
	}
}

// checkFreshProducerElementEscape rejects passing a FRESH producer value (list literal, comprehension,
// ternary, struct literal) whose interior carries a shorter-lived region than the container it is
// stored into, at a container-mutation site (push / extend / dict.put / dict-entry insert). The
// element-store checks (checkNestedRegionElementStoreEscape / ...Bulk...) see only the producer's OWN
// region — which is the container's — so they miss its interior; this descends into it. A no-op for a
// non-producer argument, which the element/bulk store checks already cover.
func (a *Analyzer) checkFreshProducerElementEscape(receiverExpr ast.Expr, containerType Type, argExpr ast.Expr) {
	if argExpr == nil || !isFreshContainerProducer(argExpr) {
		return
	}
	a.checkInteriorRegionAgainstTarget(receiverExpr, containerRegion(containerType), argExpr, "fresh container")
}

// dArrayLikeElemType returns the element type of a darray/array/deque-like container, or nil.
func dArrayLikeElemType(t Type) Type {
	switch ct := stripRefForBounds(t).(type) {
	case *DArrayType:
		if ct != nil {
			return ct.Elem
		}
	case *ArrayType:
		if ct != nil {
			return ct.Elem
		}
	}
	return nil
}

// rootIdentExpr peels field accesses and indexing down to the base identifier
// of an lvalue expression, or nil when the root is not a plain identifier.
func rootIdentExpr(expr ast.Expr) *ast.Ident {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e
		case *ast.FieldExpr:
			expr = e.Object
		case *ast.IndexExpr:
			expr = e.Object
		default:
			return nil
		}
	}
}

// definingScope returns the scope that defines name as seen from the current
// scope (respecting shadowing), or nil if the name is not in scope.
func (a *Analyzer) definingScope(name string) *Scope {
	for s := a.currentScope; s != nil; s = s.Parent {
		if _, ok := s.Symbols[name]; ok {
			return s
		}
	}
	return nil
}

// scopeStrictlyEncloses reports whether outer is a strict ancestor of inner.
func scopeStrictlyEncloses(outer, inner *Scope) bool {
	if outer == nil || inner == nil {
		return false
	}
	for s := inner.Parent; s != nil; s = s.Parent {
		if s == outer {
			return true
		}
	}
	return false
}

// regionAvailableForContainer reports whether a container's allocation region is
// LIVE at the current point, so a grow/push may source that region's arena. The
// design's rule: "push is legal iff the container's region r is live." A region
// is live when it is (a) an in-scope region parameter (caller supplies the
// arena), (b) the active `region r(...):` scope, or (c) any tracked, non-
// destroyed local region in scope. Codegen mirrors this via regionArenaOwner,
// which sources the arena from exactly that region's binding — so accepting these
// here is sound (the previous check rejected the design's canonical
// `region a: v.push(x)`, which codegen already handled).
func (a *Analyzer) regionAvailableForContainer(t Type) bool {
	if a == nil {
		return false
	}
	region := containerOrEntryRegion(t)
	if region == "" {
		return false
	}
	if a.lookupRegionParam(region) {
		return true
	}
	if sym, state := a.lookupRegionState(region); sym != nil && !state.Destroyed {
		return true
	}
	// An Arena value or Arena& ref in scope also satisfies the region requirement.
	// This covers `darray[T] @alloc` when `alloc: mutable Arena&` is in scope.
	if a.currentScope != nil && a.namedTypes["Arena"] != nil {
		if sym, ok := a.currentScope.Lookup(region); ok && sym != nil {
			arenaType := a.namedTypes["Arena"]
			st := sym.Type
			if SameType(st, arenaType) {
				return true
			}
			if refT, ok2 := st.(*RefType); ok2 && refT != nil && SameType(refT.Elem, arenaType) {
				return true
			}
		}
	}
	return false
}

// containerRegionParamInScope reports whether t is a container whose region is
// an in-scope region parameter (e.g. `def f[@r](out: darray[T] @r&)` or
// `def f[@r](d: dict[K,V] @r&)`). Such a container may be grown/inserted
// without an ambient `in <arena>:` scope: the arena for r is supplied by the
// caller (region-aware codegen sources it from the region environment / hidden
// arena param). Handles darray, dict, dict-entry receivers, peeling refs.
func (a *Analyzer) containerRegionParamInScope(t Type) bool {
	if a == nil {
		return false
	}
	if region := containerOrEntryRegion(t); region != "" {
		return a.lookupRegionParam(region)
	}
	return false
}

// containerOrEntryRegion peels ref wrappers and returns the allocation region of
// a darray, dict, or dict-entry (the entry's underlying dict), or "".
func containerOrEntryRegion(t Type) string {
	for {
		switch tt := t.(type) {
		case *DArrayType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *DictType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *SetType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *DictEntryType:
			if tt == nil || tt.Dict == nil {
				return ""
			}
			return tt.Dict.Region
		case *DStrType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *SViewType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *ViewType:
			if tt == nil {
				return ""
			}
			return tt.Region
		case *RefType:
			if tt == nil {
				return ""
			}
			t = tt.Elem
		default:
			return ""
		}
	}
}

// activeContainerRegionName is the name of the region of the innermost active
// `in <arena/region>:` scope, or "" if none.
func (a *Analyzer) activeContainerRegionName() string {
	if a == nil || a.currentAllocExpr == nil {
		return ""
	}
	if ident, ok := stripParenExpr(a.currentAllocExpr).(*ast.Ident); ok && ident != nil {
		return ident.Name
	}
	return ""
}
