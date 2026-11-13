package semantic

import (
	"elisacore/src/ast"
)

func captureFunctionTypeGraph(root Type, env *FunctionTypeEnvironment) (*functionTypeGraph, error) {
	graph, _, err := captureFunctionTypeGraphs([]Type{root}, env)
	return graph, err
}

func functionNominalTypeFingerprint(t Type) ([32]byte, error) {
	return functionNominalTypeFingerprintWithResult(t, nil)
}

func storageViewScanBody(decl *ast.FuncDecl) storageViewSolveBody {
	return storageViewScanBodyWithNodes(decl, nil)
}

func summarizePackedStoreProvenanceInto(out *PackedStoreProvenance, state regionRefState) {
	summarizePackedStoreProvenanceIntoSeen(out, state, map[uintptr]struct{}{})
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
