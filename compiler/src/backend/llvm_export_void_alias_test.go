//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// A void-returning `export func NAME(...) = impl` alias used to SIGSEGV: the void path
// names the forwarded call "" and passed it through cStringFree (which returns a nil
// C string), and LLVMBuildCall2 dereferences the name into a Twine(const char*). The
// fix routes the call name through cString (always non-nil). Regression: this must
// compile to LLVM without crashing and emit the forwarding call. (The original report
// blamed reference/container params, but the real trigger is any void return — a
// void+value alias crashed too — so the fixture exercises a darray-ref param void alias.)
func TestExportFuncVoidAliasDoesNotCrash(t *testing.T) {
	src := `def axpy_impl(y: mutable darray[f64]&, x: mutable darray[f64]&, a: f64, n: i64) -> void:
	for i in 0..<n:
		y[i.usize()] <- y[i.usize()] + a * x[i.usize()]

export func axpy(y: mutable darray[f64]&, x: mutable darray[f64]&, a: f64, n: i64) -> void = axpy_impl
`
	result := parseAndAnalyzeBackendTest(t, "export_void_alias.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "define void @axpy(") {
		t.Fatalf("expected an @axpy export wrapper definition, got:\n%s", output)
	}
	if !strings.Contains(output, "call void @axpy_impl(") {
		t.Fatalf("expected the wrapper to forward to @axpy_impl, got:\n%s", output)
	}
}

// A void-returning export alias over plain value params also exercises the same nil-name
// path (the original crash was return-type driven, not param driven), so cover it too.
func TestExportFuncVoidAliasValueParamsDoesNotCrash(t *testing.T) {
	src := `def sink(a: i64, b: i64) -> void:
	_ = a + b

export func e(a: i64, b: i64) -> void = sink
`
	result := parseAndAnalyzeBackendTest(t, "export_void_value_alias.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "call void @sink(") {
		t.Fatalf("expected the wrapper to forward to @sink, got:\n%s", output)
	}
}
