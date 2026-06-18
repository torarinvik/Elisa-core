//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func requireInstructionLineContainsAll(t *testing.T, output string, needle string, want ...string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		for _, item := range want {
			if !strings.Contains(line, item) {
				t.Fatalf("expected line containing %q to also contain %q, got:\n%s\n\nFull IR:\n%s", needle, item, line, output)
			}
		}
		return
	}
	t.Fatalf("expected instruction line containing %q, got:\n%s", needle, output)
}

func TestGenerateLLVMIRReduceSumUsesDirectDenseViewData(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_reduce_sum_direct_dense_view.elisa", `
def sum_one(value: i32) -> i32:
	return value

def kernel(buf: view[i32]) -> i32:
	source: view[i32] = readonly(buf[0:16])
	return source.reduce_sum(sum_one)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "reduce_sum.src = alloca") {
		t.Fatalf("expected reduce_sum lowering to avoid stack-temp carrier allocas, got:\n%s", output)
	}
	for _, forbidden := range []string{
		"reduce_sum.index = alloca",
		"reduce_sum.acc = alloca",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("expected reduce_sum lowering to avoid scalar loop allocas %q, got:\n%s", forbidden, output)
		}
	}
	if !strings.Contains(output, "reduce_sum.src.data = extractvalue") {
		t.Fatalf("expected reduce_sum lowering to extract the dense-view data pointer directly, got:\n%s", output)
	}
	if !strings.Contains(output, "reduce_sum.src.ptr = getelementptr") {
		t.Fatalf("expected reduce_sum lowering to index directly from the extracted data pointer, got:\n%s", output)
	}
	for _, required := range []string{
		"reduce_sum.index = phi",
		"reduce_sum.acc = phi",
	} {
		if !strings.Contains(output, required) {
			t.Fatalf("expected reduce_sum lowering to contain %q, got:\n%s", required, output)
		}
	}
}

func TestGenerateLLVMIRZipMapUsesDirectDenseViewData(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_zip_map_direct_dense_view.elisa", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: view[i32]) -> void:
	whole: view[i32] = buf[0:12]
	ro: view[i32] = readonly(whole)
	zip_map(whole[0:4], ro[4:8], ro[8:12], add)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, forbidden := range []string{
		"zip_map.dst = alloca",
		"zip_map.src1 = alloca",
		"zip_map.src2 = alloca",
		"zip_map.index = alloca",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("expected zip_map lowering to avoid carrier stack-temp %q, got:\n%s", forbidden, output)
		}
	}
	for _, required := range []string{
		"zip_map.dst.data = extractvalue",
		"zip_map.src1.data = extractvalue",
		"zip_map.src2.data = extractvalue",
		"zip_map.dst.ptr = getelementptr",
		"zip_map.src1.ptr = getelementptr",
		"zip_map.src2.ptr = getelementptr",
	} {
		if !strings.Contains(output, required) {
			t.Fatalf("expected zip_map lowering to contain %q, got:\n%s", required, output)
		}
	}
	if !strings.Contains(output, "zip_map.index = phi") {
		t.Fatalf("expected zip_map lowering to keep its induction variable in SSA, got:\n%s", output)
	}
	if !strings.Contains(output, "elisa_core.zip_map.") {
		t.Fatalf("expected zip_map lowering to emit named alias scopes, got:\n%s", output)
	}
	requireInstructionLineContainsAll(t, output, "zip_map.src1.elem = load", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, output, "zip_map.src2.elem = load", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, output, "store i32 %zip_map.call, ptr %zip_map.dst.ptr", "!alias.scope", "!noalias")
}

func TestGenerateLLVMIRArenaViewCopyUsesUnrolledDisjointExactFastPath(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_dview_copy_unrolled_disjoint_exact.elisa", `
def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	return

def kernel(buf: view[i32]) -> void:
	whole: view[i32] = buf[0:8]
	ro: view[i32] = readonly(whole)
	arena_da_copy_exact(whole[0:4], ro[4:8])
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "call ptr @arena_memcpy(") {
		t.Fatalf("expected tiny disjoint exact view copy to avoid arena_memcpy, got:\n%s", output)
	}
	if !strings.Contains(output, "elisa_core.view.copy.") {
		t.Fatalf("expected disjoint exact view copy to emit named alias scopes, got:\n%s", output)
	}
	requireInstructionLineContainsAll(t, output, "load i32, ptr %view.copy.src.elem.ptr", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, output, "store i32 %view.copy.elem, ptr %view.copy.dst.elem.ptr", "!alias.scope", "!noalias")
}

func TestGenerateLLVMIRArenaViewEqUsesUnrolledDisjointExactFastPath(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_dview_eq_unrolled_disjoint_exact.elisa", `
def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	return false

def kernel(buf: view[i32]) -> bool:
	whole: view[i32] = buf[0:8]
	ro: view[i32] = readonly(whole)
	return arena_da_eq_exact(whole[0:4], ro[4:8])
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "call i64 @memcmp(") || strings.Contains(output, "call i32 @memcmp(") {
		t.Fatalf("expected tiny disjoint exact view equality to avoid memcmp, got:\n%s", output)
	}
	if !strings.Contains(output, "elisa_core.view.eq.") {
		t.Fatalf("expected disjoint exact view equality to emit named alias scopes, got:\n%s", output)
	}
	requireInstructionLineContainsAll(t, output, "view.eq.left.byte = load i8", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, output, "view.eq.right.byte = load i8", "!alias.scope", "!noalias")
	if !strings.Contains(output, "view.eq.byte.eq = icmp eq i8") {
		t.Fatalf("expected tiny exact view equality to compare bytes directly, got:\n%s", output)
	}
}

func TestGenerateLLVMIRArenaFromViewUsesUnrolledTinyExactFastPath(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_dview_materialize_unrolled_exact.elisa", `
def arena_da_from_view[T](a: Arena&, view: view[T]) -> darray[T]:
	return zeroed

def kernel(buf: view[i32]) -> darray[i32]:
	arena: Arena = zeroed
	whole: view[i32] = buf[0:8]
	ro: view[i32] = readonly(whole)
	return arena_da_from_view((&arena).cast[Arena&], ro[4:8])
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "call ptr @arena_memcpy(") {
		t.Fatalf("expected tiny exact arena_da_from_view to avoid arena_memcpy, got:\n%s", output)
	}
	if !strings.Contains(output, "elisa_core.view.materialize.") {
		t.Fatalf("expected tiny exact arena_da_from_view to emit named alias scopes, got:\n%s", output)
	}
	requireInstructionLineContainsAll(t, output, "load i32, ptr %view.materialize.src.elem.ptr", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, output, "store i32 %view.materialize.elem, ptr %view.materialize.dst.elem.ptr", "!alias.scope", "!noalias")
	if !strings.Contains(output, "view.materialize.items") {
		t.Fatalf("expected tiny exact arena_da_from_view to still materialize the darray result, got:\n%s", output)
	}
}

func TestGenerateLLVMIRArenaViewFillUsesUnrolledTinyExactByteFastPath(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_dview_fill_unrolled_exact_byte.elisa", `
def arena_da_fill[T](dst: view[T], value: T):
	return

def kernel(buf: view[u8]) -> void:
	whole: view[u8] = buf[0:8]
	arena_da_fill(whole[0:4], 7)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "call ptr @memset(") {
		t.Fatalf("expected tiny exact byte fill to avoid memset, got:\n%s", output)
	}
	if !strings.Contains(output, "view.fill.elem.ptr = getelementptr") {
		t.Fatalf("expected tiny exact byte fill to compute element addresses directly, got:\n%s", output)
	}
	if !strings.Contains(output, "store i8 7, ptr %view.fill.elem.ptr") {
		t.Fatalf("expected tiny exact byte fill to lower to direct byte stores, got:\n%s", output)
	}
}
