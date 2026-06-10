//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRUsesIndexReadHelperForRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def fold_common() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		out: int = node.span + node.span
		destroy scratch
		return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated packed common-field reads in index-soa mode to lower through ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if !strings.Contains(output, "call void @ctx_packed_store_record_prefix_words(") {
		t.Fatalf("expected index-soa constructor lowering to record fixed prefix words, got:\n%s", output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode_index(")
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for repeated packed common-field reads in index-soa mode, got %d decode calls:\n%s", decodeCalls, output)
	}
}
func TestGenerateLLVMIRUsesSideWordHelpersForRepeatedSideTableCommonFieldReadsByDefault(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

def fold_common() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		out: int = node.span + node.span
		destroy scratch
		return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_side_table_common_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call ptr @ctx_packed_store_state_new_variant_sparse_with_side_words(") {
		t.Fatalf("expected canonical packed lowering to allocate side columns for side-tabled common fields, got:\n%s", output)
	}
	if !strings.Contains(output, "call void @ctx_packed_store_record_side_words(") {
		t.Fatalf("expected canonical packed constructor lowering to record side-table common-field words, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_side_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated side-tabled common-field reads to lower through ctx_packed_store_read_side_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_variant_sparse_word(") {
		t.Fatalf("expected side-tabled common-field reads to bypass inline variant-sparse word helpers, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesSideWordHelpersForRepeatedSideTableCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

def fold_common() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		out: int = node.span + node.span
		destroy scratch
		return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_side_table_common_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call ptr @ctx_packed_store_state_new_with_side_words(") {
		t.Fatalf("expected index-soa packed lowering to allocate side columns for side-tabled common fields, got:\n%s", output)
	}
	if !strings.Contains(output, "call void @ctx_packed_store_record_side_words(") {
		t.Fatalf("expected index-soa packed constructor lowering to record side-table common-field words, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_side_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated side-tabled common-field reads in index-soa mode to lower through ctx_packed_store_read_side_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected side-tabled common-field reads in index-soa mode to bypass inline index-word helpers, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexWordReadForFrozenRepeatedCommonFieldReads(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache_frozen_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected repeated frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode_index(")
	if decodeCalls != 0 {
		t.Fatalf("expected repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got %d decode calls:\n%s", decodeCalls, output)
	}
}
func TestGenerateLLVMIRUsesCanonicalVariantSparseReadCacheForFrozenRepeatedCommonFieldReads(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache_frozen_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 1 {
		t.Fatalf("expected canonical frozen repeated packed common-field reads to reuse one variant-sparse payload read, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen repeated packed common-field reads to avoid eager variant-sparse decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") || strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected canonical frozen repeated packed common-field reads to stay on variant-sparse helpers, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesCanonicalVariantSparseReadCacheAcrossMatchedAccessorPatterns(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Wrap(child: Expr)

def fold_child_common_frozen() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	child: Expr = new[store] Expr.Int(span: 5, value: 7)
	node: Expr = new[store] Expr.Wrap(span: 9, child: child)
	frozen: Expr.Store[Frozen] = freeze(move store)
	if node in frozen is Expr.Wrap(child: child_alias):
		out: int = child_alias.span + child_alias.span
		destroy scratch
		return out
	destroy scratch
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_matched_field_cache_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(") {
		t.Fatalf("expected canonical frozen packed match to use variant-sparse tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 2 {
		t.Fatalf("expected canonical frozen matched accessor pattern to use two total variant-sparse word reads (child payload + cached repeated child common field), got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen matched accessor pattern to avoid eager variant-sparse decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesCanonicalDirectReadsForFrozenMatchedValueFieldAccess(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	match node in frozen:
		Expr.Lit(value):
			return node.span + node.span + value
		Expr.End:
			return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_matched_value_field_cache_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(") {
		t.Fatalf("expected canonical frozen matched-value field access to use variant-sparse tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 2 {
		t.Fatalf("expected canonical frozen matched-value field access to use two total variant-sparse payload reads (cached repeated common field + payload value), got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen matched-value field access to avoid eager variant-sparse decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRPreloadsCanonicalMatchedValueCommonFieldsAcrossArms(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
		weight: int
	Lit(value: int)
	End
	Stop

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	match node in frozen:
		Expr.Lit(value):
			return node.span + node.weight + value
		Expr.End:
			return node.span + node.weight
		Expr.Stop:
			return node.span + node.weight
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_matched_value_common_preload_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 3 {
		t.Fatalf("expected canonical frozen matched-value common-field preloading to reduce total variant-sparse word reads across arms to three (two common preloads + one payload), got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen matched-value common-field preloading to avoid eager variant-sparse decode, got:\n%s", output)
	}
	if !strings.Contains(output, "switch i32") {
		t.Fatalf("expected canonical frozen matched-value common-field preloading case to lower through a switch, got:\n%s", output)
	}
}
func TestGenerateLLVMIRPreloadsIndexSOAMatchedValueCommonFieldsAcrossArms(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
		weight: int
	Lit(value: int)
	End
	Stop

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	match node in frozen:
		Expr.Lit(value):
			return node.span + node.weight + value
		Expr.End:
			return node.span + node.weight
		Expr.Stop:
			return node.span + node.weight
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_matched_value_common_preload_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 3 {
		t.Fatalf("expected frozen index-soa matched-value common-field preloading to reduce total index word reads across arms to three (two common preloads + one payload), got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen index-soa matched-value common-field preloading to avoid eager decode, got:\n%s", output)
	}
	if !strings.Contains(output, "switch i32") {
		t.Fatalf("expected frozen index-soa matched-value common-field preloading case to lower through a switch, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesCanonicalDirectReadsForNestedMatchedValueFieldAccess(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Wrap(child: Expr)

def fold_child(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	match node in frozen:
		Expr.Lit(value):
			return node.span + node.span + value
		Expr.Wrap(child: inner):
			return fold_child(inner, frozen)

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	match node in frozen:
		Expr.Wrap(child: child):
			return fold_child(child, frozen)
		Expr.Lit(value):
			return node.span + value
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_nested_matched_value_field_cache_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical nested matched-value field access to avoid eager variant-sparse decode, got:\n%s", output)
	}
	tagCalls := strings.Count(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(")
	if tagCalls < 2 {
		t.Fatalf("expected canonical nested matched-value field access to use direct tag reads for both levels, got %d helper calls:\n%s", tagCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls < 3 {
		t.Fatalf("expected canonical nested matched-value field access to stay on variant-sparse payload reads, got %d helper calls:\n%s", readCalls, output)
	}
}
func TestGenerateLLVMIRUsesSwitchForCanonicalFrozenUniqueVariantMatch(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Wrap(child: Expr)
	End

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	match node in frozen:
		Expr.Lit(value):
			return node.span + value
		Expr.Wrap(child):
			return child.span
		Expr.End:
			return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_match_switch_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "switch i32") {
		t.Fatalf("expected canonical frozen unique-variant match to lower through an LLVM switch, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen unique-variant match to avoid eager variant-sparse decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRDoesNotMaterializeUndefFailPathForExhaustiveCanonicalMatchStmt(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)
	End

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	match node in frozen:
		Expr.Lit(value):
			return value
		Expr.End:
			return 0
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_match_stmt_exhaustive_no_undef_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if strings.Contains(output, "phi i64 [ undef") {
		t.Fatalf("expected exhaustive canonical packed match statement to avoid materializing an undef fail path, got:\n%s", output)
	}
	if !strings.Contains(output, "unreachable") {
		t.Fatalf("expected exhaustive canonical packed match statement to terminate the impossible fail path with unreachable, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexWordReadForFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def fold_common_frozen_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	out: int = node.span + node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_field_cache_direct_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexWordReadForFrozenHelperWrappedRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def fold_common_frozen_wrapped_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	boxed: Box = wrap_node(node)
	out: int = boxed.node.span + boxed.node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_wrapped_field_cache_direct_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexWordReadForHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: Expr

struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_common_frozen_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	out: int = wrapped.items[0u].node.span + wrapped.items[0u].node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_field_cache_direct_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesSideWordReadForFrozenHelperWrappedRepeatedSideTableCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def fold_side_common_frozen_wrapped_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	boxed: Box = wrap_node(node)
	out: int = boxed.node.span + boxed.node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_wrapped_side_field_cache_direct_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_side_word(")
	if readCalls != 1 {
		t.Fatalf("expected helper-wrapped repeated frozen side-tabled common-field reads in index-soa mode to reuse one ctx_packed_store_read_side_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected helper-wrapped repeated frozen side-tabled common-field reads in index-soa mode to bypass inline index-word helpers, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-wrapped repeated frozen side-tabled common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
