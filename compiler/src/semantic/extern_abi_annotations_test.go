package semantic

import (
	"strings"
	"testing"
)

func TestExternABIAnnotationsPopulateFunctionType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extern_abi_annotations.elisa", `
@intrinsic(llvm.ctpop.i64)
extern popcount64(value: u64) -> u64

@callconv(winapi)
extern winapi(value: i32) -> i32

@c_abi(c)
extern c_api(value: i32) -> i32
`)

	popcountSym, ok := result.GlobalScope.Lookup("popcount64")
	if !ok {
		t.Fatal("expected popcount64 symbol")
	}
	popcountType, ok := popcountSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected popcount64 function type, got %T", popcountSym.Type)
	}
	if popcountType.IntrinsicName != "llvm.ctpop.i64" || popcountSym.LinkName != "llvm.ctpop.i64" {
		t.Fatalf("expected intrinsic metadata and link name, got intrinsic=%q link=%q", popcountType.IntrinsicName, popcountSym.LinkName)
	}

	winapiSym, ok := result.GlobalScope.Lookup("winapi")
	if !ok {
		t.Fatal("expected winapi symbol")
	}
	winapiType, ok := winapiSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected winapi function type, got %T", winapiSym.Type)
	}
	if winapiType.CallConv != "winapi" {
		t.Fatalf("expected winapi calling convention, got %q", winapiType.CallConv)
	}

	capiSym, ok := result.GlobalScope.Lookup("c_api")
	if !ok {
		t.Fatal("expected c_api symbol")
	}
	capiType, ok := capiSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected c_api function type, got %T", capiSym.Type)
	}
	if capiType.CallConv != "c" {
		t.Fatalf("expected c calling convention, got %q", capiType.CallConv)
	}
}

func TestExternABIAnnotationsRejectInvalidValues(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "extern_abi_annotations_invalid.elisa", `
@intrinsic(ctpop)
extern bad_intrinsic(value: u64) -> u64

@callconv(vectorcall)
extern bad_callconv(value: i32) -> i32
`)

	allErrors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(allErrors, "expects an LLVM intrinsic name starting with \"llvm.\"") {
		t.Fatalf("expected invalid intrinsic error, got:\n%s", allErrors)
	}
	if !strings.Contains(allErrors, "unsupported calling convention \"vectorcall\"") {
		t.Fatalf("expected invalid calling convention error, got:\n%s", allErrors)
	}
}

func TestPascalCaseFunctionCallCanFallbackFromStructLiteralSyntax(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "pascal_case_function_call.elisa", `
def DivCeil(value: usize, divisor: usize) -> usize:
    return (value + divisor - 1) / divisor

def main() -> usize:
    return DivCeil(9, 4)
`)

	sym, ok := result.GlobalScope.Lookup("main")
	if !ok {
		t.Fatal("expected main symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType.Return == nil {
		t.Fatalf("expected main function type, got %T", sym.Type)
	}
}
