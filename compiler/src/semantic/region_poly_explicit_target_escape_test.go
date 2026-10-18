package semantic

import (
	"strings"
	"testing"
)

const regionPolyExplicitTargetEscapeSource = `def make_row(seed: i64) -> darray[i64]:
    row: mutable darray[i64] = []
    row.push(seed)
    return row
`

func TestRegionPolyHiddenArenaDoesNotAdoptUnrelatedInferredPushTarget(t *testing.T) {
	source := regionPolyExplicitTargetEscapeSource + `def append_row(rows: mutable darray[darray[i64]]&, other: mutable darray[darray[i64]]&, seed: i64) -> darray[i64]:
    row: darray[i64] = make_row(seed)
    rows.push(row)
    other.push(row)
    return row
`
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "region_poly_inferred_push_target.elisa", source)
	if !regionPolymorphicFuncType(t, result, "make_row").RegionPolymorphic || !regionPolymorphicFuncType(t, result, "append_row").RegionPolymorphic {
		t.Fatal("the fixture must exercise a hidden caller arena")
	}
	appendRow := testFuncDeclByName(t, file, "append_row")
	if len(appendRow.InferredRegionParams) < 2 || appendRow.AmbientGrownContainerRegion != "" {
		t.Fatalf("the fixture needs two independent inferred targets and no adopted ambient target, got inferred=%v ambient=%q", appendRow.InferredRegionParams, appendRow.AmbientGrownContainerRegion)
	}
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "region_poly_inferred_push_target.elisa:7:") ||
		!strings.Contains(errs, "region_poly_inferred_push_target.elisa:8:") ||
		strings.Count(errs, "stored into longer-lived region") != 2 {
		t.Fatalf("a hidden caller arena must not be assumed equal to an independent inferred target, got:\n%s", errs)
	}
}

func TestRegionPolyBuilderCanTargetTheSameExplicitRegion(t *testing.T) {
	source := regionPolyExplicitTargetEscapeSource + `def append_row[@owner](rows: mutable darray[darray[i64]]& @owner, seed: i64) -> void:
    row: darray[i64] @owner = make_row(seed)
    rows.push(row)
`
	result := analyzeFunctionAnalysisTestSource(t, "region_poly_explicit_target_same_region.elisa", source)
	if !regionPolymorphicFuncType(t, result, "make_row").RegionPolymorphic {
		t.Fatal("the positive control must exercise a region-polymorphic producer")
	}
}

func TestExplicitAutoPrefixedRegionStillGetsEscapeChecked(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "explicit_auto_prefixed_region_escape.elisa", `def main() -> i64:
    kept: mutable sview = ""
    region __auto_fake(4096):
        bytes: mutable darray[u8] = [65.u8()]
        kept <- bytes.as_sview()
    return kept[0].i64() - 65
`)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, `is stored into "kept", which outlives the region`) || !strings.Contains(errs, `region "__auto_fake"`) {
		t.Fatalf("an explicit region with a compiler-reserved prefix must still enforce its lexical lifetime, got:\n%s", errs)
	}
}

func TestExplicitAutoPrefixedRegionDoesNotAdoptGrownCallerTarget(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "explicit_auto_prefixed_region_grown_target.elisa", `def fill(out: mutable darray[darray[i64]]&) -> void:
    region __auto_fake(4096):
        row: mutable darray[i64] = [1]
        out.push(row)
`)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "explicit_auto_prefixed_region_grown_target.elisa:4:") ||
		!strings.Contains(errs, "stored into longer-lived region") {
		t.Fatalf("an explicit region whose name has the synthesized prefix must not adopt the caller target, got:\n%s", errs)
	}
}

func TestExplicitAutoPrefixedRegionDoesNotAdoptFreshAggregateInterior(t *testing.T) {
	const source = `struct Packet:
    body: mutable darray[i64]

def fill(out: mutable darray[Packet]&) -> void:
    region __auto_fake(4096):
        body: mutable darray[i64] = [1]
        out.push(Packet{body: body})
`
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "explicit_auto_prefixed_aggregate_target.elisa", source)
	fill := testFuncDeclByName(t, file, "fill")
	if fill.AmbientGrownContainerRegion == "" {
		t.Fatal("the fixture must exercise a single grown caller target")
	}
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "explicit_auto_prefixed_aggregate_target.elisa:7:") || !strings.Contains(errs, "longer-lived region") {
		t.Fatalf("a nested value from a user region must not inherit the target's ambient adoption, got:\n%s", errs)
	}
}
