package semantic

import (
	"elisacore/src/ast"
)

func summarizePackedStoreProvenance(state regionRefState) PackedStoreProvenance {
	if state.PackedStoreSummaryKnown {
		return state.PackedStoreSummary
	}
	var out PackedStoreProvenance
	summarizePackedStoreProvenanceIntoSeen(&out, state, map[uintptr]struct{}{})
	return out
}

func mergePackedStoreProvenanceInto(dst *PackedStoreProvenance, src PackedStoreProvenance) {
	if dst == nil {
		return
	}
	dst.HasPackedStoreDeps = dst.HasPackedStoreDeps || src.HasPackedStoreDeps
	dst.HasFrozenPackedStoreDeps = dst.HasFrozenPackedStoreDeps || src.HasFrozenPackedStoreDeps
	dst.HasNonFrozenPackedStoreDeps = dst.HasNonFrozenPackedStoreDeps || src.HasNonFrozenPackedStoreDeps
	dst.HasNonStoreProvenance = dst.HasNonStoreProvenance || src.HasNonStoreProvenance
}

func summarizePackedStoreProvenanceInto(out *PackedStoreProvenance, state regionRefState) {
	summarizePackedStoreProvenanceIntoSeen(out, state, map[uintptr]struct{}{})
}

func summarizePackedStoreProvenanceIntoSeen(out *PackedStoreProvenance, state regionRefState, seen map[uintptr]struct{}) {
	if out == nil {
		return
	}
	if state.PackedStoreSummaryKnown {
		mergePackedStoreProvenanceInto(out, state.PackedStoreSummary)
		return
	}
	if len(state.Deps) != 0 || hasRegionParamDependencies(state) {
		out.HasNonStoreProvenance = true
	}
	for _, dep := range state.StoreDeps {
		out.HasPackedStoreDeps = true
		if dep.Type != nil && IsFrozenPackedEnumStoreType(dep.Type) {
			out.HasFrozenPackedStoreDeps = true
			continue
		}
		out.HasNonFrozenPackedStoreDeps = true
	}
	fieldsID := regionRefFieldsIdentity(state.Fields)
	if fieldsID != 0 {
		if _, ok := seen[fieldsID]; ok {
			return
		}
		seen[fieldsID] = struct{}{}
		defer delete(seen, fieldsID)
	}
	for _, fieldState := range state.Fields {
		summarizePackedStoreProvenanceIntoSeen(out, fieldState, seen)
	}
}

func (a *Analyzer) exprPackedStoreProvenance(expr ast.Expr) (PackedStoreProvenance, bool) {
	if a == nil || expr == nil {
		return PackedStoreProvenance{}, false
	}
	state, ok := a.regionRefStateForExpr(expr)
	if !ok {
		return PackedStoreProvenance{}, false
	}
	return summarizePackedStoreProvenance(state), true
}

func (a *Analyzer) exprDependsOnlyOnFrozenPackedStores(expr ast.Expr) bool {
	provenance, ok := a.exprPackedStoreProvenance(expr)
	if !ok {
		return false
	}
	return provenance.DependsOnlyOnFrozenPackedStores()
}

func regionRefStateDependsOnlyOnFrozenPackedStores(state regionRefState) (bool, bool) {
	return regionRefStateDependsOnlyOnFrozenPackedStoresWithSeen(state, map[uintptr]struct{}{})
}

func regionRefStateDependsOnlyOnFrozenPackedStoresWithSeen(state regionRefState, seen map[uintptr]struct{}) (bool, bool) {
	if state.PackedStoreSummaryKnown {
		summary := state.PackedStoreSummary
		return summary.DependsOnlyOnFrozenPackedStores() || (!summary.HasAnyPackedStoreProvenance() && !summary.HasNonStoreProvenance), summary.DependsOnFrozenPackedStores()
	}
	if len(state.Deps) != 0 || hasRegionParamDependencies(state) {
		return false, false
	}
	hasFrozen := false
	for _, dep := range state.StoreDeps {
		if dep.Type == nil || !IsFrozenPackedEnumStoreType(dep.Type) {
			return false, false
		}
		hasFrozen = true
	}
	fieldsID := regionRefFieldsIdentity(state.Fields)
	if fieldsID != 0 {
		if _, ok := seen[fieldsID]; ok {
			return true, false
		}
		seen[fieldsID] = struct{}{}
		defer delete(seen, fieldsID)
	}
	for _, fieldState := range state.Fields {
		fieldOnlyFrozen, fieldHasFrozen := regionRefStateDependsOnlyOnFrozenPackedStoresWithSeen(fieldState, seen)
		if !fieldOnlyFrozen {
			return false, false
		}
		hasFrozen = hasFrozen || fieldHasFrozen
	}
	return true, hasFrozen
}

func (a *Analyzer) sliceFullSpanField(expr ast.Expr) string {
	if a == nil || expr == nil {
		return ""
	}
	t := a.exprTypes[expr]
	for {
		ref, ok := t.(*RefType)
		if !ok {
			break
		}
		t = ref.Elem
	}
	switch tt := t.(type) {
	case *DArrayType:
		return "count"
	case *ViewType:
		return "len"
	case *DStrType, *SViewType:
		return "len"
	case *StructType:
		if tt != nil {
			if _, ok := dynArrayViewRuntimeType(tt); ok {
				return "len"
			}
			if _, ok := stringViewRuntimeType(tt); ok {
				return "len"
			}
		}
	}
	return ""
}
