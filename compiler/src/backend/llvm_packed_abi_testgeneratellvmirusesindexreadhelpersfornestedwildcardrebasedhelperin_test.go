//go:build cgo

package backend

import (
	"strings"
	"testing"
)

const packedStoreStateLayoutTestPrelude = `struct PackedStoreState:
	head_region: uintptr
	alloc_region: uintptr
	alloc_region_index: usize
	decode_region: uintptr
	decode_region_index: usize
	tail_region: uintptr
	regions: darray[uintptr]
	handles: darray[uintptr]
	row_word_counts: darray[usize]
	indices: darray[u32]
	tags: darray[u32]
	variant_slots: darray[u32]
	variant_rows: darray[uintptr]
	prefix_words: usize
	prefix_columns: darray[uintptr]
	prefix_column_capacity: usize
	side_words: usize
	side_columns: darray[uintptr]
	side_column_capacity: usize
	row_words: usize
	preferred_region_capacity: usize
`

func TestGenerateLLVMIRUsesIndexReadHelpersForNestedWildcardRebasedHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(inline)
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

@borrows_return(field, rebased, meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_nested_wild_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: i32& @scratch = new[scratch] 7
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	items: array[Box, 2] = [Box{node: new[store] Expr.Int(span: 2, value: 1)}, Box{node: new[store] Expr.Wrap(span: 9, child: held)}]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1:2], 0, 1)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match wrapped.meta.items[0].node in frozen:
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
		@storage(inline)
		span: int
	Yes
	No

def choose() -> int:
	region scratch(256)
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
	region scratch(256)
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
	src := packedStoreStateLayoutTestPrelude + `
packed enum Expr:
	common:
		@storage(inline)
		span: int
	Lit(value: int)

def fold_common_frozen() -> int:
	region scratch(256)
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
	src := packedStoreStateLayoutTestPrelude + `
packed enum Expr:
	common:
		@storage(inline)
		span: int
	Lit(value: int)
	End

def fold_match_frozen() -> int:
	region scratch(256)
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
	src := packedStoreStateLayoutTestPrelude + `
@packed_profile(retained_reads)
packed enum Expr:
	common:
		@storage(inline)
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
	region scratch(256)
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
