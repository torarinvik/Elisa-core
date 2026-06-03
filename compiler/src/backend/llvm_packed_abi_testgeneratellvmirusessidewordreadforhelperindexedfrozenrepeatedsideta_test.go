//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRUsesSideWordReadForHelperIndexedFrozenRepeatedSideTableCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

struct Box:
	node: Expr

struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_side_common_frozen_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	out: int = wrapped.items[0u].node.span + wrapped.items[0u].node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_side_field_cache_direct_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_side_word(")
	if readCalls != 1 {
		t.Fatalf("expected helper-indexed repeated frozen side-tabled common-field reads in index-soa mode to reuse one ctx_packed_store_read_side_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected helper-indexed repeated frozen side-tabled common-field reads in index-soa mode to bypass inline index-word helpers, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed repeated frozen side-tabled common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRAvoidsDecodeForFrozenHelperWrappedCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

def fold_common_frozen_wrapped_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	boxed: Box = Box(new[store] Expr.Lit(span: 7, value: 5))
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = boxed.node.span
	boxed.node <- other
	_ = frozen
	out: int = first + boxed.node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_wrapped_field_cache_reassign_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads after reassignment in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads after reassignment in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRAvoidsDecodeForFrozenHelperIndexedCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_common_frozen_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	wrapped: BoxHolder = wrap_indexed_node(node)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.items[0u].node.span
	wrapped.items[0u].node <- other
	_ = frozen
	out: int = first + wrapped.items[0u].node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_field_cache_reassign_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected helper-indexed frozen packed common-field reads after reassignment in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed frozen packed common-field reads after reassignment in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexWordReadForNestedRebasedHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	out: int = wrapped.meta.items[0u].node.span + wrapped.meta.items[0u].node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_helper_indexed_field_cache_direct_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected nested rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexWordReadForNestedWildcardRebasedHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_wild_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	out: int = wrapped.meta.items[0u].node.span + wrapped.meta.items[0u].node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_helper_indexed_field_cache_direct_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRAvoidsDecodeForNestedWildcardRebasedHelperIndexedFrozenCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_wild_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.meta.items[0u].node.span
	wrapped.meta.items[0u].node <- other
	_ = frozen
	out: int = first + wrapped.meta.items[0u].node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_helper_indexed_field_cache_reassign_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads after reassignment in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads after reassignment in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRAvoidsDecodeForNestedRebasedHelperIndexedFrozenCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.meta.items[0u].node.span
	wrapped.meta.items[0u].node <- other
	_ = frozen
	out: int = first + wrapped.meta.items[0u].node.span
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_helper_indexed_field_cache_reassign_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads after reassignment in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads after reassignment in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersDenseNodeKeysInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Add(left: Expr, right: Expr)

def read(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		_ = new Expr.Add(span: 5, left: left, right: right)

	frozen: Expr.Store[Frozen] = freeze(move store)
	node: Expr = frozen[2u]
	key: NodeKey[Expr] = dense_key(node, frozen)
	again: Expr = frozen[key]
	return again.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_dense_node_key_abi.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}
	indexCalls := strings.Count(output, "call i32 @ctx_packed_store_index_at(")
	if indexCalls != 1 {
		t.Fatalf("expected dense-key lowering to reuse the original numeric frozen lookup and avoid an extra index_at for frozen[key], got %d calls:\n%s", indexCalls, output)
	}
	for _, bad := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index(", "call i64 @ctx_packed_store_encode(", "call ptr @ctx_packed_store_decode("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected dense-key lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}
func TestGenerateLLVMIRLowersDenseNodeKeysFromHiddenFrozenStoreFieldRootsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Add(left: Expr, right: Expr)

struct FrozenBox:
	store: Expr.Store[Frozen]

def make_box(owner: Arena) -> FrozenBox:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		_ = new Expr.Add(span: 5, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return FrozenBox(frozen)

def read(owner: Arena) -> int:
	box: FrozenBox = make_box(owner)
	node: Expr = box.store[2u]
	key: NodeKey[Expr] = dense_key(node, box.store)
	again: Expr = box.store[key]
	_ = again
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_dense_node_key_hidden_field_root_abi.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}
	indexCalls := strings.Count(output, "call i32 @ctx_packed_store_index_at(")
	if indexCalls != 1 {
		t.Fatalf("expected hidden-root dense-key lowering to reuse the original numeric frozen lookup and avoid an extra index_at for box.store[key], got %d calls:\n%s", indexCalls, output)
	}
	for _, bad := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index(", "call i64 @ctx_packed_store_encode(", "call ptr @ctx_packed_store_decode("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected hidden-root dense-key lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}
func TestGenerateLLVMIRUsesIndexReadHelpersForMixedFrozenRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Hold(value: i32&)
	End

def fold_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: i32& @scratch = new[scratch] 7i32
	node: Expr = new[store] Expr.Hold(span: 7, value: local_ref)
	frozen: Expr.Store[Frozen] = freeze(move store)
	in frozen:
		out: int = node.span + node.span
		destroy scratch
		return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_field_cache_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected repeated mixed frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected repeated mixed frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexReadHelpersForMixedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: i32&)
	Wrap(child: Expr)

def fold_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: i32& @scratch = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	node: Expr = new[store] Expr.Wrap(span: 9, child: held)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match node in frozen:
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_matched_payload_field_cache_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 3 {
		t.Fatalf("expected frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen outer match plus repeated mixed child common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexReadHelpersForHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: i32&)
	Wrap(child: Expr)

struct Box:
	node: Expr

struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: i32& @scratch = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	node: Expr = new[store] Expr.Wrap(span: 9, child: held)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match wrapped.items[0u].node in frozen:
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_matched_payload_field_cache_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 3 {
		t.Fatalf("expected helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexReadHelpersForNestedRebasedHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
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

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_nested_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: i32& @scratch = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	items: array[Box, 2] = [Box(new[store] Expr.Int(span: 2, value: 1)), Box(new[store] Expr.Wrap(span: 9, child: held))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_rebased_helper_indexed_matched_payload_field_cache_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected nested rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 3 {
		t.Fatalf("expected nested rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}
