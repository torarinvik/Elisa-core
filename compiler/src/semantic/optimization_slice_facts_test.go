package semantic

import (
	"strings"
	"testing"
)

func TestOptimizationFactsSupportAffineSplitChunkComposition(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "affine_split_chunks_exact_offsets.elisa", `
def kernel(buf: view[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (4 * chunk)
	whole: view[i32] = buf[start:limit]
	parts: SplitView[i32] = split_at(whole, (2 * chunk))
	left_chunks: ChunksExactView[i32] = chunks_exact(parts.left, chunk)
	right_ro: view[i32] = readonly(parts.right)
	right_chunks: ChunksExactView[i32] = chunks_exact(right_ro, chunk)
	left0: view[i32] = left_chunks[0]
	right0: view[i32] = right_chunks[0]
	right1: view[i32] = right_chunks[1]
	pass
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	left0 := mustVarDeclValueExpr(t, fn.Body[6], "left0")
	right0 := mustVarDeclValueExpr(t, fn.Body[7], "right0")
	right1 := mustVarDeclValueExpr(t, fn.Body[8], "right1")

	leftFacts, ok := result.ExprOptimizationFacts(left0)
	if !ok || leftFacts.Extent == nil {
		t.Fatalf("expected left0 chunk item facts")
	}
	mustAffineExprTerms(t, leftFacts.Extent.Begin, 0, map[string]int64{"start": 1})
	mustAffineExprTerms(t, leftFacts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 1})

	right0Facts, ok := result.ExprOptimizationFacts(right0)
	if !ok || right0Facts.Extent == nil {
		t.Fatalf("expected right0 chunk item facts")
	}
	mustAffineExprTerms(t, right0Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 2})
	mustAffineExprTerms(t, right0Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 3})

	right1Facts, ok := result.ExprOptimizationFacts(right1)
	if !ok || right1Facts.Extent == nil {
		t.Fatalf("expected right1 chunk item facts")
	}
	mustAffineExprTerms(t, right1Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 3})
	mustAffineExprTerms(t, right1Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 4})

	if !result.ExprsHaveEqualExtentSize(left0, right0) {
		t.Fatalf("expected affine chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(left0, right1) {
		t.Fatalf("expected affine chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(left0, right0) {
		t.Fatalf("expected affine left0 and right0 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(left0, right1) {
		t.Fatalf("expected affine left0 and right1 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(right0, right1) {
		t.Fatalf("expected affine right0 and right1 chunk items to be disjoint")
	}
}

func TestAnalyzeZipMapAcceptsAffineSplitChunkComposition(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_affine_split_chunks_exact.elisa", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: view[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (4 * chunk)
	whole: view[i32] = buf[start:limit]
	parts: SplitView[i32] = split_at(whole, (2 * chunk))
	left_chunks: ChunksExactView[i32] = chunks_exact(parts.left, chunk)
	right_ro: view[i32] = readonly(parts.right)
	right_chunks: ChunksExactView[i32] = chunks_exact(right_ro, chunk)
	zip_map(left_chunks[0], right_chunks[0], right_chunks[1], add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected affine split/chunk zip_map to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected affine zip_map destination and source 1 chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected affine zip_map destination and source 2 chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected affine zip_map destination and source 1 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected affine zip_map destination and source 2 chunk items to be disjoint")
	}
}

func TestAnalyzeZipMapAcceptsReadonlyDirectSliceComposition(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_affine_direct_slices.elisa", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: view[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (3 * chunk)
	whole: view[i32] = buf[start:limit]
	ro: view[i32] = readonly(whole)
	dst: view[i32] = whole[0:chunk]
	src1: view[i32] = ro[chunk:(2 * chunk)]
	src2: view[i32] = ro[(2 * chunk):(3 * chunk)]
	zip_map(dst, src1, src2, add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected readonly direct-slice zip_map to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	src1 := mustVarDeclValueExpr(t, fn.Body[4], "src1")
	src2 := mustVarDeclValueExpr(t, fn.Body[5], "src2")
	src1Facts, ok := result.ExprOptimizationFacts(src1)
	if !ok || src1Facts.Extent == nil {
		t.Fatalf("expected src1 slice facts")
	}
	if !src1Facts.ReadOnly {
		t.Fatalf("expected src1 slice of readonly view to remain readonly")
	}
	mustAffineExprTerms(t, src1Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 1})
	mustAffineExprTerms(t, src1Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 2})
	src2Facts, ok := result.ExprOptimizationFacts(src2)
	if !ok || src2Facts.Extent == nil {
		t.Fatalf("expected src2 slice facts")
	}
	if !src2Facts.ReadOnly {
		t.Fatalf("expected src2 slice of readonly view to remain readonly")
	}
	mustAffineExprTerms(t, src2Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 2})
	mustAffineExprTerms(t, src2Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 3})
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected readonly direct-slice zip_map destination and source 1 to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected readonly direct-slice zip_map destination and source 2 to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected readonly direct-slice zip_map destination and source 1 to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected readonly direct-slice zip_map destination and source 2 to be disjoint")
	}
}

func TestOptimizationFactsComposeDirectViewSlicesWithAffineBase(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "helper_view_slice_affine.elisa", `
def kernel(buf: view[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (3 * chunk)
	whole: view[i32] = buf[start:limit]
	rest_view: view[i32] = whole[chunk:whole.len]
	first: view[i32] = rest_view[0:chunk]
	second: view[i32] = rest_view[chunk:(2 * chunk)]
	pass
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected helper slice composition to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	first := mustVarDeclValueExpr(t, fn.Body[3], "first")
	second := mustVarDeclValueExpr(t, fn.Body[4], "second")
	firstFacts, ok := result.ExprOptimizationFacts(first)
	if !ok || firstFacts.Extent == nil {
		t.Fatalf("expected first helper slice facts")
	}
	mustAffineExprTerms(t, firstFacts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 1})
	mustAffineExprTerms(t, firstFacts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 2})
	secondFacts, ok := result.ExprOptimizationFacts(second)
	if !ok || secondFacts.Extent == nil {
		t.Fatalf("expected second helper slice facts")
	}
	mustAffineExprTerms(t, secondFacts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 2})
	mustAffineExprTerms(t, secondFacts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 3})
	if !result.ExprsHaveEqualExtentSize(first, second) {
		t.Fatalf("expected helper view slices to have equal extent size")
	}
	if !result.ExprsAreDisjoint(first, second) {
		t.Fatalf("expected helper view slices to be disjoint")
	}
}

func TestAnalyzeZipMapAcceptsReadonlyDirectViewSlices(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_readonly_helper_slices.elisa", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: view[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (3 * chunk)
	whole: view[i32] = buf[start:limit]
	ro: view[i32] = readonly(whole)
	dst: view[i32] = whole[0:chunk]
	src1: view[i32] = ro[chunk:(2 * chunk)]
	src2: view[i32] = ro[(2 * chunk):(3 * chunk)]
	zip_map(dst, src1, src2, add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected readonly helper-slice zip_map to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	src1 := mustVarDeclValueExpr(t, fn.Body[4], "src1")
	src2 := mustVarDeclValueExpr(t, fn.Body[5], "src2")
	src1Facts, ok := result.ExprOptimizationFacts(src1)
	if !ok || src1Facts.Extent == nil {
		t.Fatalf("expected src1 helper slice facts")
	}
	if !src1Facts.ReadOnly {
		t.Fatalf("expected src1 helper slice of readonly view to remain readonly")
	}
	mustAffineExprTerms(t, src1Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 1})
	mustAffineExprTerms(t, src1Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 2})
	src2Facts, ok := result.ExprOptimizationFacts(src2)
	if !ok || src2Facts.Extent == nil {
		t.Fatalf("expected src2 helper slice facts")
	}
	if !src2Facts.ReadOnly {
		t.Fatalf("expected src2 helper slice of readonly view to remain readonly")
	}
	mustAffineExprTerms(t, src2Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 2})
	mustAffineExprTerms(t, src2Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 3})
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected readonly helper-slice zip_map destination and source 1 to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected readonly helper-slice zip_map destination and source 2 to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected readonly helper-slice zip_map destination and source 1 to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected readonly helper-slice zip_map destination and source 2 to be disjoint")
	}
}

func TestAnalyzeReduceSumAcceptsReadonlyDirectSuffix(t *testing.T) {
	_, result := parseAndAnalyzeOptimizationFactsTest(t, "reduce_sum_readonly_helper_suffix.elisa", `
def sum_one(value: i32) -> i32:
	return value

def kernel(buf: view[i32], start: usize, chunk: usize) -> i32:
	limit: usize = start + (2 * chunk)
	whole: view[i32] = buf[start:limit]
	ro: view[i32] = readonly(whole)
	rest_view: view[i32] = ro[chunk:ro.len]
	return reduce_sum(rest_view, sum_one)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected readonly helper-suffix reduce_sum to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeReduceSumAcceptsReadonlyFixedDArraySlice(t *testing.T) {
	_, result := parseAndAnalyzeOptimizationFactsTest(t, "reduce_sum_readonly_fixed_darray_slice.elisa", `
def sum_with_bias(value: i64, bias: i64) -> i64:
	return value + bias

def kernel(values: darray[i64, 4], bias: i64) -> i64:
	base: view[i64] = values[0:4]
	return reduce_sum(readonly(base), sum_with_bias, bias)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected a readonly fixed-darray slice to satisfy reduce_sum, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeReduceSumAcceptsReadonlyEqualSizeTernarySlices(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "reduce_sum_readonly_ternary_slices.elisa", `
def sum_one(value: i32) -> i32:
	return value

def kernel(buf: view[i32], cond: bool, chunk: usize) -> i32:
	whole: view[i32] = readonly(buf[0:(2 * chunk)])
	picked: view[i32] = whole[0:chunk] if cond else whole[chunk:(2 * chunk)]
	return reduce_sum(picked, sum_one)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected readonly equal-size ternary slices to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	picked := mustVarDeclValueExpr(t, fn.Body[1], "picked")
	facts, ok := result.ExprOptimizationFacts(picked)
	if !ok || facts.Extent == nil {
		t.Fatalf("expected ternary-picked view facts")
	}
	if !facts.ReadOnly || !facts.Contiguous || !facts.UnitStride {
		t.Fatalf("expected ternary-picked view to remain readonly dense, got %+v", facts)
	}
	if facts.Extent.Kind != OptimizationExtentArraySize {
		t.Fatalf("expected ternary-picked view to keep size-only exact extent, got %v", facts.Extent.Kind)
	}
	mustAffineExprTerms(t, facts.Extent.Size, 0, map[string]int64{"chunk": 1})
}

func TestAnalyzeReduceSumAcceptsReadonlyEqualSizeTernaryDirectSlices(t *testing.T) {
	_, result := parseAndAnalyzeOptimizationFactsTest(t, "reduce_sum_readonly_ternary_direct_slices.elisa", `
def sum_one(value: i32) -> i32:
	return value

def kernel(buf: view[i32], cond: bool, chunk: usize) -> i32:
	whole: view[i32] = readonly(buf[0:(2 * chunk)])
	picked: view[i32] = whole[0:chunk] if cond else whole[chunk:(2 * chunk)]
	return reduce_sum(picked, sum_one)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected readonly equal-size ternary helper slices to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}
