//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRUsesIndexReadHelpersForNestedWildcardRebasedHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: i32&)
	Wrap(child: Expr)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_nested_wild_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	items: array[Box, 2] = [Box(new[store] Expr.Int(span: 2, value: 1)), Box(new[store] Expr.Wrap(span: 9, child: held))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match wrapped.meta.items[0u].node in frozen:
		Expr.Wrap(child: child_alias):
			out: int = child_alias.span + child_alias.span
			destroy scratch
			return out
		Expr.Int(value: _):
			destroy scratch
			return 0
		Expr.Hold(value: _):
			destroy scratch
			return 1
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_rebased_helper_indexed_matched_payload_field_cache_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 3 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPayloadlessPackedMatchWithMatchedValueFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Flag:
	common:
		span: int
	Yes
	No

def choose() -> int:
	region scratch(256u)
	store: Flag.Store[Local] = Flag.Store(scratch)
	node: Flag = new[store] Flag.Yes(span: 7)
	frozen: Flag.Store[Frozen] = freeze(move store)
	match node in frozen:
		Flag.Yes:
			out: int = node.span + node.span
			destroy scratch
			return out
		Flag.No:
			destroy scratch
			return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_payloadless_match_fields_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads in index-soa mode to reuse one direct index word read, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexTagReadHelperForPayloadlessPackedMatch(t *testing.T) {
	src := `packed enum Flag:
	Yes
	No

def choose() -> int:
	region scratch(256u)
	store: Flag.Store[Local] = Flag.Store(scratch)
	in store:
		node: Flag = new Flag.Yes
		match node:
			Flag.Yes:
				destroy scratch
				return 1
			Flag.No:
				destroy scratch
				return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tag_read_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected payloadless packed match in index-soa mode to read tag through ctx_packed_store_read_index_tag, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 0 {
		t.Fatalf("expected payloadless packed match in index-soa mode to avoid ctx_packed_store_read_index_word, got %d helper calls:\n%s", readCalls, output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode_index(")
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for payloadless packed match in index-soa mode, got %d decode calls:\n%s", decodeCalls, output)
	}
}
func TestGenerateOptimizedLLVMIRUsesDirectPrefixColumnLoadsForFrozenRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def fold_common_frozen() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	in frozen:
		out: int = node.span + node.span
		destroy scratch
		return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache_frozen_index_soa_opt.elisa", src)
	profile, err := ExplicitPackedLoweringProfile(PackedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("ExplicitPackedLoweringProfile returned error: %v", err)
	}
	output, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel3, profile)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
	}

	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected optimized frozen repeated packed common-field reads in index-soa mode to avoid ctx_packed_store_read_index_word, got:\n%s", output)
	}
	if strings.Contains(output, "call fastcc i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected optimized frozen repeated packed common-field reads in index-soa mode to avoid ctx_packed_store_read_word fallback calls, got:\n%s", output)
	}
}
func TestGenerateOptimizedLLVMIRUsesDirectDenseMetadataLoadsForFrozenPackedMatchInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold_match_frozen() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	in frozen:
		match node:
			Expr.Lit(value):
				out: int = value + node.span
				destroy scratch
				return out
			Expr.End:
				destroy scratch
				return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_match_frozen_index_soa_opt.elisa", src)
	profile, err := ExplicitPackedLoweringProfile(PackedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("ExplicitPackedLoweringProfile returned error: %v", err)
	}
	output, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel3, profile)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
	}

	if strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected optimized frozen packed match in index-soa mode to avoid ctx_packed_store_read_index_tag, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected optimized frozen packed match in index-soa mode to avoid ctx_packed_store_read_index_word, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected optimized frozen packed match in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateOptimizedLLVMIRUsesDirectAllWordsPrefixLoadsForRetainedReadsFrozenWidePayloadMatch(t *testing.T) {
	src := `@packed_profile(retained_reads)
packed enum Expr:
	common:
		span: int
		cost: int
	Leaf(value: int)
	Wide(first: int, second: int, third: int)
	End

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	match node in frozen:
		Expr.Wide(first: first, second: second, third: third):
			return node.span + node.cost + first + second + third
		Expr.Leaf(value: value):
			return node.span + node.cost + value
		Expr.End:
			return 0

def fold_export() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Wide(span: 7, cost: 11, first: 2, second: 3, third: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	out: int = fold(node, frozen)
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_retained_reads_wide_payload_opt.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}

	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected optimized retained-reads frozen wide payload match to avoid ctx_packed_store_read_index_word, got:\n%s", output)
	}
	if strings.Contains(output, "call fastcc i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected optimized retained-reads frozen wide payload match to avoid ctx_packed_store_read_word fallback calls, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected optimized retained-reads frozen wide payload match to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPackedIfPatternInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold_if_pattern() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	if node in frozen as Expr.Lit(value: value):
		out: int = value + node.span
		destroy scratch
		return out
	destroy scratch
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_if_pattern_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen packed if pattern in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected frozen packed if pattern in index-soa mode to use direct prefix word reads, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed if pattern in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRAvoidsEagerDecodeForFrozenMixedPackedMatchInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match node in frozen:
		Expr.Lit(value):
			out: int = value + node.span
			destroy scratch
			return out
		Expr.End:
			destroy scratch
			return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen mixed packed match in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected frozen mixed packed match in index-soa mode to use direct prefix word reads, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen mixed packed match in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPackedIfVariantViewInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold_view() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	if node in frozen as Expr.Lit(value: value):
		out: int = node.value + node.span + value
		destroy scratch
		return out
	destroy scratch
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_view_stmt_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected frozen packed if variant view in index-soa mode to reuse one repeated payload index word read plus one common-field read, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed if variant view in index-soa mode to avoid eager decode, got:\n%s", output)
	}
	if strings.Contains(output, "@llvm.trap") {
		t.Fatalf("expected packed if variant view in index-soa mode to preserve ordinary if fallthrough instead of trapping on mismatch, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexTailViewFastPathForFrozenPackedIfVariantViewInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def fold_view() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	source_items: array[int, 3] = [1, 2, 3]
	node: Expr = new[store] Expr.Block(count: 3u, items: source_items[0u:3u])
	frozen: Expr.Store[Frozen] = freeze(move store)
	if node in frozen as Expr.Block:
		out: int = node.items[0u] + node.items[2u]
		destroy scratch
		return out
	destroy scratch
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_view_stmt_tail_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"call i64 @ctx_packed_store_read_index_word(",
		"packed.payload.tail.data",
		"packed.payload.tail.len",
		"packed.payload.tail.elem_size",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected frozen packed tail if variant view in index-soa mode to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed tail if variant view in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersPackedStoreCountAndIndexForIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def walk(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 3)
		right: Expr = new Expr.Int(span: 2, value: 4)
		_ = new Expr.Add(span: 3, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	total: mutable int = 0
	index: mutable usize = 0u
	while index < frozen.count:
		node: Expr = frozen[index]
		if node in frozen as Expr.Int(value: value):
			total <- total + value + node.value + node.span
		index <- index + 1u
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_count_index_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"%PackedStoreIndexAllocResult = type { ptr, i32 }",
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_index_result(",
		"call i64 @ctx_packed_store_count(",
		"call i32 @ctx_packed_store_index_at(",
		"call i32 @ctx_packed_store_read_index_tag(",
		"call i64 @ctx_packed_store_read_index_word(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected output to avoid decode_index on the index-SOA fast path, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersPackedStoreSliceForIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def walk(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 3)
		right: Expr = new Expr.Int(span: 2, value: 4)
		_ = new Expr.Add(span: 3, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	chunk: dview[Expr] = frozen[1u:frozen.count]
	if chunk.len > 0u:
		node: Expr = chunk[0u]
		if node in frozen as Expr.Int(value: value):
			return value + node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_slice_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"call %DynArrayView @ctx_packed_store_indices_view(",
		"call i64 @ctx_packed_store_count(",
		"call i32 @ctx_packed_store_read_index_tag(",
		"call i64 @ctx_packed_store_read_index_word(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected output to avoid decode_index for sliced index-SOA reads, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexReadHelpersForMixedPackedMatchInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)
	End

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(value: 5)
		match node:
			Expr.Lit(value):
				out: int = value
				destroy scratch
				return out
			Expr.End:
				destroy scratch
				return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_index_soa_tag_read_mixed.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected mixed packed match in index-soa mode to avoid eager decode on the fast path, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexTailViewFastPathInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		source_items: array[int, 3] = [1, 2, 3]
		node: Expr = new Expr.Block(count: 3u, items: source_items[0u:3u])
		match node:
			Expr.Block(count: _, items: items):
				out: int = items[0u] + items[2u]
				destroy scratch
				return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tail_payload_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word(", "packed.payload.tail.data", "packed.payload.tail.len", "packed.payload.tail.elem_size"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected index-soa tail payload match to avoid full decode on the fast path, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexTailViewFastPathForNonFinalTailPayloadInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Block(items: tail int, count: usize)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		source_items: array[int, 3] = [1, 2, 3]
		node: Expr = new Expr.Block(items: source_items[0u:3u], count: 3u)
		match node:
			Expr.Block(items: items, count: count):
				out: int = count.int() + items[0u] + items[2u]
				destroy scratch
				return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tail_payload_non_final_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word(", "packed.payload.tail.data", "packed.payload.tail.len", "packed.payload.tail.elem_size"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected index-soa non-final tail payload match to avoid full decode on the fast path, got:\n%s", output)
	}
}
