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
		a.errorf(valueExpr.Pos(), "value allocated in region %q escapes via %s; the region is freed at scope exit. Copy it into a caller-provided region param (def f[region r] ... -> ... @r) or a longer-lived region first", region, via)
	}
}

// containerRegionParamInScope reports whether t is a container whose region is
// an in-scope region parameter (e.g. `def f[region r](out: darray[T] @r&)`).
// Such a container may be grown without an ambient `in <arena>:` scope: the
// arena for r is supplied by the caller (region-aware codegen sources it from
// the region environment / hidden arena param).
func (a *Analyzer) containerRegionParamInScope(t Type) bool {
	if a == nil {
		return false
	}
	if da, ok := t.(*DArrayType); ok && da != nil && da.Region != "" {
		return a.lookupRegionParam(da.Region)
	}
	return false
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
