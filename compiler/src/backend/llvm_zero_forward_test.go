//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestZeroForwardSafety(t *testing.T) {
	ir, err := os.ReadFile("testdata/zero_forward.ll")
	if err != nil {
		t.Fatal(err)
	}
	g, err := parseLLVMIRForSplitHalfTest(string(ir))
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := forwardZeroedLoads(g.module); n != 4 {
		t.Fatalf("forwarded %d loads, want 4", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	result := g.printModule()
	for _, name := range []string{"good", "good_disjoint_store", "marker_vector", "marker_scalar"} {
		body := splitHalfAddFunctionBody(result, name)
		if !strings.Contains(body, "ret i64 0") || strings.Contains(body, "load i64") {
			t.Errorf("%s not forwarded: %s", name, body)
		}
	}
	for _, name := range []string{"overlap_store", "unknown_store", "escaped", "unknown_call", "volatile_load", "atomic_load", "volatile_store", "fence_case", "volatile_memset", "nonzero_memset", "partial_memset", "out_of_bounds", "dynamic_offset", "not_inbounds", "bad_alignment", "range_metadata", "other_block", "debug_function", "poison_intermediate_gep", "dynamic_memset_size", "dynamic_alloca", "escaped_to_global", "escaped_as_integer"} {
		body := splitHalfAddFunctionBody(result, name)
		if !strings.Contains(body, "load ") {
			t.Errorf("%s unexpectedly forwarded: %s", name, body)
		}
	}
	for _, name := range []string{"marker_vector", "marker_scalar"} {
		if !strings.Contains(splitHalfAddFunctionBody(result, name), "!llvm.loop") {
			t.Errorf("%s lost loop metadata", name)
		}
	}
}

func TestZeroForwardCachePolicy(t *testing.T) {
	normal := optimizedModuleCacheKey(&llvmGenerator{}, OptimizationLevel3, "a", "b", "c")
	debug := optimizedModuleCacheKey(&llvmGenerator{emitDebugInfo: true}, OptimizationLevel3, "a", "b", "c")
	if !strings.Contains(normal, "zero-forward-v1") || normal == debug {
		t.Fatal("cache key does not distinguish zero forwarding policy")
	}
}

func TestZeroForwardDeclinesMissingLayoutAndScanBudget(t *testing.T) {
	input, err := os.ReadFile("testdata/zero_forward.ll")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(input), "\n", 2)
	g, err := parseLLVMIRForSplitHalfTest(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	if n := forwardZeroedLoads(g.module); n != 0 {
		t.Errorf("missing data layout forwarded %d", n)
	}
	g.dispose()

	var ir strings.Builder
	ir.WriteString(`target datalayout = "e-m:o-i64:64-n32:64-S128"
declare void @llvm.memset.p0.i64(ptr nocapture writeonly, i8, i64, i1 immarg)
define i64 @long_scan(i64 %x) {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
`)
	for i := 0; i < 1030; i++ {
		fmt.Fprintf(&ir, "  %%r%d = add i64 %%x, %d\n", i, i)
	}
	ir.WriteString("  %p = getelementptr inbounds i8, ptr %a, i64 16\n  %v = load i64, ptr %p, align 8\n  ret i64 %v\n}\n")
	g, err = parseLLVMIRForSplitHalfTest(ir.String())
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := forwardZeroedLoads(g.module); n != 0 {
		t.Errorf("scan limit forwarded %d", n)
	}
}

func TestZeroForwardWideConstantsAndGEPBudget(t *testing.T) {
	var ir strings.Builder
	ir.WriteString(`target datalayout = "e-m:o-i64:64-n32:64-S128"
declare void @llvm.memset.p0.i128(ptr nocapture writeonly, i8, i128, i1 immarg)
declare void @llvm.memset.p0.i64(ptr nocapture writeonly, i8, i64, i1 immarg)
define i64 @wide_length() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i128(ptr %a, i8 0, i128 32, i1 false)
  %v = load i64, ptr %a, align 8
  ret i64 %v
}
define i64 @wide_index() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i128 8
  %v = load i64, ptr %p, align 8
  ret i64 %v
}
define i64 @long_geps() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
`)
	previous := "%a"
	for i := 0; i < 1030; i++ {
		fmt.Fprintf(&ir, "  %%p%d = getelementptr inbounds i8, ptr %s, i64 0\n", i, previous)
		previous = fmt.Sprintf("%%p%d", i)
	}
	fmt.Fprintf(&ir, "  %%v = load i64, ptr %s, align 8\n  ret i64 %%v\n}\n", previous)
	g, err := parseLLVMIRForSplitHalfTest(ir.String())
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := forwardZeroedLoads(g.module); n != 2 {
		t.Fatalf("wide constants and long GEP chain forwarded %d loads, want 2", n)
	}
	if !strings.Contains(splitHalfAddFunctionBody(g.printModule(), "long_geps"), "load i64") {
		t.Fatal("long GEP chain must exhaust the traversal budget")
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
}

func TestZeroForwardRejectsTruncatedGEPIndex(t *testing.T) {
	const ir = `target datalayout = "e-p:64:64:64:32-i64:64-n32:64-S128"
declare void @llvm.memset.p0.i64(ptr nocapture writeonly, i8, i64, i1 immarg)
define i64 @truncated_index() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 4294967304
  %v = load i64, ptr %p, align 8
  ret i64 %v
}
`
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := forwardZeroedLoads(g.module); n != 0 {
		t.Fatalf("truncated GEP index forwarded %d loads", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
}
