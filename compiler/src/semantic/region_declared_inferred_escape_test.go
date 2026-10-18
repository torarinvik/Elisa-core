package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestRegionStoreEscapesAcrossDistinctDeclaredParams(t *testing.T) {
	a := &Analyzer{
		currentFuncDecl: &ast.FuncDecl{
			RegionParams: []string{"source", "target"},
		},
		regionParamScopes: []map[string]bool{{"source": true, "target": true}},
	}

	for _, tc := range []struct {
		target string
		value  string
	}{
		{target: "target", value: "source"},
		{target: "source", value: "target"},
	} {
		if !a.regionStoreEscapes(tc.target, tc.value) {
			t.Errorf("independent caller-owned regions target=%q value=%q must be conservatively rejected", tc.target, tc.value)
		}
	}
	if a.regionStoreEscapes("source", "source") {
		t.Fatal("storing within the same declared region must remain safe")
	}
}

func TestRegionStoreEscapesLeavesInferredOnlyRelationsToInference(t *testing.T) {
	a := &Analyzer{
		currentFuncDecl: &ast.FuncDecl{
			RegionParams:         []string{"__rg_value", "__rg_target"},
			InferredRegionParams: []string{"__rg_value", "__rg_target"},
		},
		regionParamScopes: []map[string]bool{{"__rg_value": true, "__rg_target": true}},
	}
	if a.regionStoreEscapes("__rg_target", "__rg_value") {
		t.Fatal("inferred-only region relations belong to call-site inference, not the source-declared check")
	}
}
