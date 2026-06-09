//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersPackedStoreTagsViewForIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def first_tag(owner: Arena) -> Expr.Tag:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(value: 1)
		right: Expr = new Expr.Int(value: 2)
		_ = new Expr.Add(left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	tags: view[Expr.Tag] = frozen.tags
	return tags[0u]
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_tags_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"ctx_packed_store_tags_view", "call i64 @ctx_packed_store_count("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"call ptr @ctx_packed_store_decode(", "call i32 @ctx_packed_store_read_tag("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected packed store tag view lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}
func TestGenerateLLVMIRLowersParallelForOverFrozenPackedStoreForIndexSOA(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Int(span: 1, value: 3)
		_ = new Expr.Int(span: 2, value: 4)
	frozen: Expr.Store[Frozen] = freeze(move store)
	pool workers(2u):
		parallel for node in frozen:
			if node in frozen as Expr.Int(value: value):
				_ = value + node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_parallel_for_index_soa.elisa", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"@__parallel_for_0_worker", "@pool_submit1__", "@task_group_add__", "@task_group_wait_all(", "call i64 @ctx_packed_store_count(", "call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected index-soa parallel-for over frozen packed store to avoid eager decode on the fast path, got:\n%s", output)
	}
}
