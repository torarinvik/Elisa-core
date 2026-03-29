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
	result := parseAndAnalyzeBackendTest(t, "backend_reduce_sum_direct_dense_view.llcontext", `
def sum_one(value: i32) -> i32:
	return value

def kernel(buf: dview[i32]) -> i32:
	source: dview[i32] = readonly(buf[0u:16u])
	return reduce_sum(source, sum_one)
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
	result := parseAndAnalyzeBackendTest(t, "backend_zip_map_direct_dense_view.llcontext", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: dview[i32]) -> void:
	whole: dview[i32] = buf[0u:12u]
	ro: dview[i32] = readonly(whole)
	zip_map(whole[0u:4u], ro[4u:8u], ro[8u:12u], add)
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
	if !strings.Contains(output, "llctx.zip_map.") {
		t.Fatalf("expected zip_map lowering to emit named alias scopes, got:\n%s", output)
	}
	requireInstructionLineContainsAll(t, output, "zip_map.src1.elem = load", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, output, "zip_map.src2.elem = load", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, output, "store i32 %zip_map.call, ptr %zip_map.dst.ptr", "!alias.scope", "!noalias")
}
