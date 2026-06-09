package semantic

import (
	"os"

	"elisacore/src/ast"
)

var debugRegionContainers = os.Getenv("ELISA_REGION_DEBUG") != ""

// Region-parameterized containers — Phase 1 (carry the region; don't enforce).
// See REGION_CONTAINERS_DESIGN.md. This stamps a darray's allocation region from
// the enclosing `in <region>:` scope. SameType/AssignableTo/String/codegen do
// NOT yet consult Region, so this is observable but behaviorally inert.

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

// checkStoredRegionContainerEscape rejects storing a scope-owned-region container
// into storage that outlives the region (a global / function-outliving lvalue).
func (a *Analyzer) checkStoredRegionContainerEscape(valueExpr ast.Expr, valueType Type) {
	a.checkRegionContainerEscape(valueExpr, valueType, "store into longer-lived storage")
}

func (a *Analyzer) checkRegionContainerEscape(valueExpr ast.Expr, valueType Type, via string) {
	if a == nil || valueExpr == nil {
		return
	}
	region := containerRegion(valueType)
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
			a.errorf(valueExpr.Pos(), "value escapes its `in auto:` scope via %s; the inferred region is freed at scope exit. Give it an explicit lifetime — copy it into a caller-provided region param (def f[region r] ... -> ... @r) or a longer-lived region", via)
		} else {
			a.errorf(valueExpr.Pos(), "value allocated in region %q escapes via %s; the region is freed at scope exit. Copy it into a caller-provided region param (def f[region r] ... -> ... @r) or a longer-lived region first", region, via)
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
	targetRegion := containerOrEntryRegion(targetType)
	if valueRegion == "" || targetRegion == "" {
		return
	}
	if a.regionOutlives(targetRegion, valueRegion) {
		a.errorf(targetExpr.Pos(), "value in region %q is stored into longer-lived region %q; region %q is freed first, leaving a dangling reference. Copy it into region %q (or a region that outlives %q) before storing", valueRegion, targetRegion, valueRegion, targetRegion, targetRegion)
	}
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
	if a.currentTreeAllocOwner.Kind == treeAllocOwnerRegion && a.currentTreeAllocOwner.RegionName == region {
		return true
	}
	if sym, state := a.lookupRegionState(region); sym != nil && !state.Destroyed {
		return true
	}
	return false
}

// containerRegionParamInScope reports whether t is a container whose region is
// an in-scope region parameter (e.g. `def f[region r](out: darray[T] @r&)` or
// `def f[region r](d: dict[K,V] @r&)`). Such a container may be grown/inserted
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
	if a == nil {
		return ""
	}
	owner := a.currentTreeAllocOwner
	switch owner.Kind {
	case treeAllocOwnerRegion:
		// `region r(...):` — a named region.
		return owner.RegionName
	case treeAllocOwnerArena:
		// plain `in <arena>:` — use the arena variable's name as the region
		// identity (Phase 1 proxy; proper region identity comes in Phase 3).
		if ident, ok := stripTreeAllocOwnerExpr(a.currentAllocExpr).(*ast.Ident); ok && ident != nil {
			return ident.Name
		}
		return ""
	default:
		return ""
	}
}
