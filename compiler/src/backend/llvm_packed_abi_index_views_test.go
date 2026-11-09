//go:build cgo

package backend

import (
	"regexp"
	"strings"
	"testing"
)

func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPackedIfPatternInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(inline)
		span: int
	Lit(value: int)
	End

def fold_if_pattern() -> int:
	region scratch(256)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	if node in frozen is Expr.Lit(value: value):
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
		@storage(inline)
		span: int
	Lit(value: int)
	End

def fold() -> int:
	region scratch(256)
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
		@storage(inline)
		span: int
	Lit(value: int)
	End

def fold_view() -> int:
	region scratch(256)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	if node in frozen is Expr.Lit(value: value):
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
	// Contracts-live builds trap signed `+`/`-`/`*` on overflow; those traps (in `signed.arith.*`
	// blocks) come from the fixture's ordinary `int` accumulation and are orthogonal to the packed
	// view. A trap NOT from that path would mean the view eagerly trapped on a variant mismatch
	// instead of falling through, which is what this test guards against.
	trapCount := strings.Count(output, "call void @llvm.trap()")
	signedTraps := len(regexp.MustCompile(`signed\.arith\.overflow\.trap\d*:`).FindAllString(output, -1))
	if trapCount > signedTraps {
		t.Fatalf("expected packed if variant view in index-soa mode to preserve ordinary if fallthrough instead of trapping on mismatch, got:\n%s", output)
	}
}
func TestGenerateLLVMIRUsesIndexTailViewFastPathForFrozenPackedIfVariantViewInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def fold_view() -> int:
	region scratch(256)
	store: Expr.Store[Local] = Expr.Store(scratch)
	source_items: array[int, 3] = [1, 2, 3]
	node: Expr = new[store] Expr.Block(count: 3, items: source_items[0:3])
	frozen: Expr.Store[Frozen] = freeze(move store)
	if node in frozen is Expr.Block:
		out: int = node.items[0] + node.items[2]
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
		@storage(inline)
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
	index: mutable usize = 0
	while index < frozen.count:
		node: Expr = frozen[index]
		if node in frozen is Expr.Int(value: value):
			total <- total + value + node.value + node.span
		index <- index + 1
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
		@storage(inline)
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
	chunk: view[Expr] = frozen[1:frozen.count]
	if chunk.len > 0:
		node: Expr = chunk[0]
		if node in frozen is Expr.Int(value: value):
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
	region scratch(256)
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
	region scratch(256)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		source_items: array[int, 3] = [1, 2, 3]
		node: Expr = new Expr.Block(count: 3, items: source_items[0:3])
		match node:
			Expr.Block(count: _, items: items):
				out: int = items[0] + items[2]
				destroy scratch
				return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tail_payload_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word(", "packed.payload.tail.data", "packed.payload.tail.len"} {
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
	region scratch(256)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		source_items: array[int, 3] = [1, 2, 3]
		node: Expr = new Expr.Block(items: source_items[0:3], count: 3)
		match node:
			Expr.Block(items: items, count: count):
				out: int = count.int() + items[0] + items[2]
				destroy scratch
				return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tail_payload_non_final_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word(", "packed.payload.tail.data", "packed.payload.tail.len"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected index-soa non-final tail payload match to avoid full decode on the fast path, got:\n%s", output)
	}
}
