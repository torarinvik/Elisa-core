//go:build cgo

package backend

import (
	"strings"
	"testing"
)

const watchdogTrustedIndexSrc = `def at(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        return xs[i]
`

// In debug (-O0) a trusted/unchecked user index is verified by the watchdog: a
// runtime bounds compare plus llvm.trap on violation. "Debug verifies what
// release assumes."
func TestIndexWatchdogTrapsInDebug(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_trusted_debug.elisa", watchdogTrustedIndexSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("expected debug watchdog bounds check on trusted index, got:\n%s", output)
	}
	if !strings.Contains(output, "@llvm.trap") {
		t.Fatalf("expected debug watchdog to emit llvm.trap on violation, got:\n%s", output)
	}
}

// In release the trusted/unchecked index is exactly as before — zero watchdog
// overhead.
func TestIndexWatchdogAbsentInRelease(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_trusted_release.elisa", watchdogTrustedIndexSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel2)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "wd.in_bounds") || strings.Contains(output, "wd.fail") {
		t.Fatalf("expected no watchdog instrumentation in release, got:\n%s", output)
	}
}

// A checked `get arr[i] else ...` performs its own semantic bounds check; the
// debug watchdog must not also instrument it (no duplicate check).
func TestCheckedGetIndexHasNoWatchdogDuplicate(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_get_no_dup.elisa", `def at(xs: darray[i32]&, i: usize) -> i32:
    return get xs[i] else -1
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("checked get index must not get a duplicate watchdog bounds check, got:\n%s", output)
	}
}

// Watchdog subsumption (docs/86 86-3): an index the analyzer proved in-bounds — here a
// `0..<xs.count` loop index (proven upper bound + non-negative) — emits NO debug watchdog,
// even at -O0. A proven access is never double-instrumented.
func TestIndexWatchdogSubsumedForProvenLoopIndex(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_subsumed_proven.elisa", `def sum(xs: darray[i32]&) -> i32:
    total: mutable i32 = 0
    for i in 0..<xs.count:
        total <- total + xs[i]
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("a proven loop index should be subsumed (no debug watchdog), got:\n%s", output)
	}
}

// Cross-variable coupling (docs/86 brick 86-6): a loop bound `n` known EQUAL to `xs.count` (via an
// immutable binding) lets `xs[i]` in `for i in 0..<n` discharge — the equality bridges the loop's
// `n` upper bound to the container's `xs.count` length expression. No debug watchdog at -O0.
func TestIndexWatchdogSubsumedViaBoundEquality(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_bound_equality.elisa", `def sum(xs: darray[i32]&) -> i32:
    total: mutable i32 = 0
    n: usize = xs.count
    for i in 0..<n:
        total <- total + xs[i]
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("a loop index bounded by n==xs.count should be subsumed, got:\n%s", output)
	}
}

// A `requires n == xs.count` precondition seeds the bound equality at function entry — the canonical
// Dafny pattern — so `for i in 0..<n: xs[i]` discharges with no body guard. No debug watchdog at -O0.
func TestIndexWatchdogSubsumedViaRequiresEquality(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_requires_equality.elisa", `def sum(xs: darray[i32]&, n: usize) -> i32:
    requires n == xs.count
    total: mutable i32 = 0
    for i in 0..<n:
        total <- total + xs[i]
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("a loop index bounded by a `requires n == xs.count` precondition should be subsumed, got:\n%s", output)
	}
}

// Shader-register frontier: a decoded/refined register index whose interval fits the fixed register
// file extent is marked IndexBoundsProven by semantic analysis, so debug codegen must not reinsert the
// runtime watchdog. This mirrors shadps4's GcnScalarRegisterIndex / GcnVectorRegisterPairIndex cases.
func TestIndexWatchdogSubsumedForRefinedRegisterIndex(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_refined_register_index.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi

type GcnScalarRegisterIndex = u32 is InRange[0, 127]
type GcnVectorRegisterIndex = u32 is InRange[0, 255]
type GcnVectorRegisterPairIndex = u32 is InRange[0, 254]

struct Vop2Inst:
    vdst: mutable GcnVectorRegisterIndex
    vsrc1: mutable GcnVectorRegisterIndex

def read_sgpr(idx: GcnScalarRegisterIndex, sgprs: array[u32, 128]&) -> u32:
    return sgprs[idx]

def read_vsrc1(d: Vop2Inst&, vgprs: array[u32, 256]&) -> u32:
    return vgprs[d.vsrc1]

def write_vdst_pair(idx: GcnVectorRegisterPairIndex, vgprs: mutable array[u32, 256]&, lo: u32, hi: u32) -> void:
    vgprs[idx] <- lo
    vgprs[idx + 1] <- hi
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "wd.in_bounds") || strings.Contains(output, "wd.fail") {
		t.Fatalf("refined shader register indices should be subsumed (no debug watchdog), got:\n%s", output)
	}
}

// Negative control: a loop bound `n` with NO known relation to `xs.count` must NOT be subsumed —
// the access keeps its debug watchdog. Confirms the equality layer doesn't over-prove.
func TestIndexWatchdogNotSubsumedWithoutEquality(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_no_equality.elisa", `def sum(xs: darray[i32]&, n: usize) -> i32:
    total: mutable i32 = 0
    for i in 0..<n:
        total <- total + xs[i]
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("an unrelated loop bound must NOT be subsumed (would be unsound), got:\n%s", output)
	}
}

// IR-level inbounds GEP: a statically-proven loop index (`for i in 0..<xs.count`)
// must emit `getelementptr inbounds` so LLVM can apply no-wrap optimizations. Verified
// at -O0 so no IR is elided by the optimizer.
func TestProvenIndexEmitsInboundsGEP(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "proven_inbounds_gep.elisa", `def sum(xs: darray[i32]&) -> i32:
    total: mutable i32 = 0
    for i in 0..<xs.count:
        total <- total + xs[i]
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	// The element GEP for xs[i] (idx.ptr) must carry inbounds: the analyzer proved the index
	// is in [0, xs.count), so LLVM is allowed to assume no pointer wrap.
	if !strings.Contains(output, "getelementptr inbounds") {
		t.Fatalf("proven loop index must emit getelementptr inbounds, got:\n%s", output)
	}
}

// IR-level inbounds GEP: a refined fixed-array index (register file, interval fits array
// size) must also emit `getelementptr inbounds` — the semantic proof carries to the GEP.
func TestRefinedIndexEmitsInboundsGEP(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "refined_inbounds_gep.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
type GcnScalarRegisterIndex = u32 is InRange[0, 127]

def read_sgpr(idx: GcnScalarRegisterIndex, sgprs: array[u32, 128]&) -> u32:
    return sgprs[idx]
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "getelementptr inbounds") {
		t.Fatalf("refined-interval index must emit getelementptr inbounds, got:\n%s", output)
	}
}

// IR-level inbounds GEP negative: an unproven index (loop bound unrelated to xs.count)
// must NOT emit `getelementptr inbounds` — claiming inbounds without a proof is unsound.
func TestUnprovenIndexDoesNotEmitInboundsGEP(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "unproven_no_inbounds_gep.elisa", `def sum(xs: darray[i32]&, n: usize) -> i32:
    total: mutable i32 = 0
    for i in 0..<n:
        total <- total + xs[i]
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	// Verify the element GEP (idx.ptr line) is plain getelementptr, not inbounds.
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "idx.ptr") && strings.Contains(line, "inbounds") {
			t.Fatalf("unproven index must NOT emit getelementptr inbounds, got line:\n%s\nfull IR:\n%s", line, output)
		}
	}
	// Also confirm the watchdog IS present (regression guard: unproven → guarded).
	if !strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("unproven index must keep debug watchdog, got:\n%s", output)
	}
}
