package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestFuncTypeIdentityIncludesInferredRegionAndReturnRegion(t *testing.T) {
	base := &FuncType{
		RegionParams:         []string{"region"},
		InferredRegionParams: []string{"region"},
		Params:               []Type{&BuiltinType{Name: "i64"}},
		Return:               &BuiltinType{Name: "i64"},
		ReturnRegion:         "region",
	}

	withoutInferredRegion := *base
	withoutInferredRegion.InferredRegionParams = nil
	assertFuncTypeIdentityDiffers(t, base, &withoutInferredRegion, "inferred-region metadata")

	withDifferentReturnRegion := *base
	withDifferentReturnRegion.ReturnRegion = "other"
	assertFuncTypeIdentityDiffers(t, base, &withDifferentReturnRegion, "return region")
}

func assertFuncTypeIdentityDiffers(t *testing.T, left, right *FuncType, difference string) {
	t.Helper()
	if SameType(left, right) {
		t.Fatalf("function types differing in %s compare equal", difference)
	}
	leftID := requireCanonicalTypeID(t, left)
	rightID := requireCanonicalTypeID(t, right)
	if leftID == rightID {
		t.Fatalf("function types differing in %s share canonical type id %d", difference, leftID)
	}
}

func TestInferredRegionNameAvoidsDeclaredRegionCollision(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "inferred_region_name_collision.elisa", `struct Cache:
    items: mutable darray[i64]

def fill[@__rg_self](self: mutable Cache&, n: usize) -> void:
    out: mutable darray[i64] = []
    i: mutable usize = 0
    while i < n:
        out.push(i.i64())
        i <- i + 1
    self.items <- out
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected region-name collision fixture to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "fill")
	if len(fn.InferredRegionParams) != 1 || fn.InferredRegionParams[0] != "__rg_self_1" {
		t.Fatalf("expected inferred region to avoid the declared __rg_self binder, got %v", fn.InferredRegionParams)
	}
	if fn.AmbientGrownContainerRegion != fn.InferredRegionParams[0] {
		t.Fatalf("ambient grown-container region %q does not match inferred binder %q", fn.AmbientGrownContainerRegion, fn.InferredRegionParams[0])
	}
}

func TestWritableParamSummaryUsesInferredRegionMetadataNotNamePrefix(t *testing.T) {
	decl := &ast.FuncDecl{
		RegionParams:         []string{"__rg_user", "__rg_out"},
		InferredRegionParams: []string{"__rg_out"},
	}
	if functionHasOnlyInferredRegionParams(decl) {
		t.Fatal("a source-declared __rg_* binder must not be mistaken for an inferred region")
	}
	decl.RegionParams = []string{"__rg_out"}
	if !functionHasOnlyInferredRegionParams(decl) {
		t.Fatal("a function with only metadata-marked inferred region binders should pass")
	}
}
