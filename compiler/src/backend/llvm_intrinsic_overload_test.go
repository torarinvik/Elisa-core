//go:build cgo

package backend

import (
	"strings"
	"testing"
)

const intrinsicOverloadProbeSrc = `@intrinsic("llvm.umax")
extern umax_u64(a: u64, b: u64) -> u64

@intrinsic("llvm.umax.i64")
extern umax_u64_mangled(a: u64, b: u64) -> u64

@intrinsic("llvm.ctpop")
extern popcount_u32(a: u32) -> u32

@intrinsic("elisa.mul_hi.u64")
extern mul_hi_u64(a: u64, b: u64) -> u64

@intrinsic("elisa.div_wide.u64")
extern div_wide_u64(hi: u64, lo: u64, d: u64) -> u64

@intrinsic("elisa.f64_to_bits")
extern f64_bits(v: f64) -> u64

@intrinsic("elisa.f64_from_bits")
extern f64_of_bits(b: u64) -> f64

@inline(never)
def probe(a: u64, b: u64, c: u32) -> u64:
    return umax_u64(a, b) + umax_u64_mangled(b, a) + popcount_u32(c).u64() + mul_hi_u64(a, b) + div_wide_u64(1, a, b + 2) + f64_bits(f64_of_bits(a))

def main() -> int:
    return (probe(3, 4, 7) & 1).int()
`

// An overloaded LLVM intrinsic is instantiated on its overload types only
// (llvm.umax.i64, llvm.ctpop.i32), whether or not the @intrinsic spelling is
// already mangled; previously every parameter type was appended
// (llvm.umax.i64.i64), which failed verification. The elisa.mul_hi.u64 builtin is
// an internal always-inline i128 multiply that folds into the caller.
func TestIntrinsicOverloadsAndMulHiBuiltin(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "intrinsic_overload_probe.elisa", intrinsicOverloadProbeSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
	}
	for _, want := range []string{"declare i64 @llvm.umax.i64(", "declare i32 @llvm.ctpop.i32(", "@elisa.mul_hi.u64(i64", "mul nuw i128", "lshr i128", "udiv i128", "alwaysinline", "bitcast double", "bitcast i64"} {
		if !strings.Contains(ir, want) {
			t.Fatalf("IR missing %q:\n%s", want, ir)
		}
	}
	if strings.Contains(ir, "llvm.umax.i64.i64") || strings.Contains(ir, "llvm.ctpop.i32.i32") {
		t.Fatalf("over-mangled intrinsic:\n%s", ir)
	}
	optimized, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt O3 error: %v", err)
	}
	if strings.Contains(optimized, "@elisa.mul_hi.u64(") || strings.Contains(optimized, "@elisa.div_wide.u64(") || strings.Contains(optimized, "@elisa.f64_to_bits(") {
		t.Fatalf("mul_hi builtin was not inlined away at O3:\n%s", optimized)
	}
}
