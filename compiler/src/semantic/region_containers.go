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
	if da, ok := t.(*DArrayType); ok && da != nil && da.Region == "" {
		cp := *da
		cp.Region = region
		if debugRegionContainers {
			println("REGIONSTAMP darray ->", region)
		}
		return &cp
	}
	return t
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
