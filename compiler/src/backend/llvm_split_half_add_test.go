//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const splitHalfAddProbeSource = `@inline(never)
def split_half_add_impl(left: u64, right: u64) -> u64:
    low: u64 = (left & 0xFFFFFFFF) + (right & 0xFFFFFFFF)
    high: u64 = (left >> 32) + (right >> 32) + (low >> 32)
    ((high & 0xFFFFFFFF) << 32) | (low & 0xFFFFFFFF)

@inline(never)
def split_half_add_constants() -> u64:
    left: u64 = 7
    right: u64 = 9
    low: u64 = (left & 0xFFFFFFFF) + (right & 0xFFFFFFFF)
    high: u64 = (left >> 32) + (right >> 32) + (low >> 32)
    ((high & 0xFFFFFFFF) << 32) | (low & 0xFFFFFFFF)

@inline(never)
def split_half_add_near_miss(left: u64, right: u64) -> u64:
    low: u64 = (left & 0xFFFFFFFF) + (right & 0xFFFFFFFF)
    high: u64 = (left >> 32) + (right >> 32) + ((low >> 32) + 1)
    ((high & 0xFFFFFFFF) << 32) | (low & 0xFFFFFFFF)

export fn split_half_add(left: u64, right: u64) -> u64 = split_half_add_impl
`

func splitHalfAddFunctionBody(ir, name string) string {
	marker := "@" + name + "("
	index := strings.Index(ir, marker)
	if index < 0 {
		return ""
	}
	start := strings.LastIndex(ir[:index], "define ")
	if start < 0 {
		return ""
	}
	end := strings.Index(ir[index:], "\n}")
	if end < 0 {
		return ""
	}
	return ir[start : index+end+2]
}

func TestSplitHalfUnsignedAddCanonicalization(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "split_half_add.elisa", splitHalfAddProbeSource)
	O0IR, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("O0 LLVM lowering failed: %v", err)
	}
	O0Body := splitHalfAddFunctionBody(O0IR, "split_half_add_impl")
	if O0Body == "" || !strings.Contains(O0Body, "lshr i64") || !strings.Contains(O0Body, "4294967295") ||
		strings.Contains(O0Body, "add i64 %0, %1") {
		t.Fatalf("O0 lowering should retain the original split-half operations:\n%s", O0Body)
	}

	// Apply the production canonicalizer directly to unoptimized codegen. The
	// exact idiom must fold once; the +1 near miss must be left intact.
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compile raw LLVM module: %v", err)
	}
	defer g.dispose()
	if rewrites := canonicalizeSplitHalfUnsignedAdds(g.module); rewrites != 2 {
		t.Fatalf("canonicalizer rewrote %d roots, want the general and constant-operand helpers", rewrites)
	}
	if err := g.verify(); err != nil {
		t.Fatalf("canonicalized raw module failed verification: %v", err)
	}
	rawIR := g.printModule()
	goodBody := splitHalfAddFunctionBody(rawIR, "split_half_add_impl")
	constantBody := splitHalfAddFunctionBody(rawIR, "split_half_add_constants")
	badBody := splitHalfAddFunctionBody(rawIR, "split_half_add_near_miss")
	if !strings.Contains(goodBody, "add i64 %0, %1") || strings.Contains(goodBody, "ret i64 %ortmp") {
		t.Fatalf("matching split helper did not become one native-width add:\n%s", goodBody)
	}
	if !strings.Contains(constantBody, "add i64 7, 9") {
		t.Fatalf("constant-operand helper did not create a valid add instruction:\n%s", constantBody)
	}
	if !strings.Contains(badBody, "or i64") || strings.Contains(badBody, "add i64 %0, %1") {
		t.Fatalf("near-miss expression was rewritten:\n%s", badBody)
	}

	O3IR, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
	if err != nil {
		t.Fatalf("O3 LLVM optimization failed: %v", err)
	}
	O3Body := splitHalfAddFunctionBody(O3IR, "split_half_add_impl")
	if O3Body == "" || (!strings.Contains(O3Body, "add i64 %0, %1") &&
		!strings.Contains(O3Body, "add i64 %1, %0")) {
		t.Fatalf("full O3 output did not retain the canonical native-width addition:\n%s", O3Body)
	}
}

const splitHalfRawIRTemplate = `define i64 @split(i64 %%a, i64 %%b) {
entry:
  %%a.addr = alloca i64, align 8
  %%b.addr = alloca i64, align 8
  store i64 %%a, ptr %%a.addr, align 8
  store i64 %%b, ptr %%b.addr, align 8
  ; EXTRA_BEFORE_LOAD
  %%a0 = load i64, ptr %%a.addr, align 8
  %%b0 = load i64, ptr %%b.addr, align 8
  ; EXTRA_AFTER_LOAD
  %%alo = and i64 %%a0, 4294967295
  %%blo = and i64 %%b0, 4294967295
  %%low = add i64 %%alo, %%blo
  %%carry = lshr i64 %%low, 32
  %%ahi = lshr i64 %%a0, 32
  %%bhi = lshr i64 %%b0, 32
  %%h0 = add i64 %%ahi, %%bhi
  %%high = add i64 %%h0, %%carry
  %%hm = and i64 %%high, 4294967295
  %%hs = shl i64 %%hm, 32
  %%lm = and i64 %%low, 4294967295
  %%out = or i64 %%hs, %%lm
  ret i64 %%out
}
`

func TestSplitHalfRawLLVMMatcherSafetyBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		edit     func(string) string
		wantFold bool
	}{
		{name: "exact pattern", wantFold: true},
		{name: "freeze input", wantFold: true, edit: func(ir string) string {
			ir = strings.Replace(ir, "  store i64 %a, ptr %a.addr, align 8", "  %af = freeze i64 %a\n  store i64 %af, ptr %a.addr, align 8", 1)
			return ir
		}},
		{name: "poison input", wantFold: true, edit: func(ir string) string {
			return strings.Replace(ir, "  store i64 %a, ptr %a.addr, align 8", "  store i64 poison, ptr %a.addr, align 8", 1)
		}},
		{name: "exact right shift", edit: func(ir string) string {
			return strings.Replace(ir, "%carry = lshr i64", "%carry = lshr exact i64", 1)
		}},
		{name: "signed no-wrap left shift", edit: func(ir string) string {
			return strings.Replace(ir, "%hs = shl i64", "%hs = shl nsw i64", 1)
		}},
		{name: "multiple stores", edit: func(ir string) string {
			return strings.Replace(ir, "  ; EXTRA_BEFORE_LOAD", "  store i64 4, ptr %a.addr, align 8\n  ; EXTRA_BEFORE_LOAD", 1)
		}},
		{name: "intervening store", edit: func(ir string) string {
			return strings.Replace(ir, "  ; EXTRA_AFTER_LOAD", "  store i64 4, ptr %a.addr, align 8\n  ; EXTRA_AFTER_LOAD", 1)
		}},
		{name: "volatile load", edit: func(ir string) string {
			return strings.Replace(ir, "%a0 = load i64", "%a0 = load volatile i64", 1)
		}},
		{name: "atomic load", edit: func(ir string) string {
			return strings.Replace(ir, "%a0 = load i64, ptr %a.addr, align 8", "%a0 = load atomic i64, ptr %a.addr acquire, align 8", 1)
		}},
		{name: "volatile store", edit: func(ir string) string {
			return strings.Replace(ir, "store i64 %a, ptr %a.addr", "store volatile i64 %a, ptr %a.addr", 1)
		}},
		{name: "atomic store", edit: func(ir string) string {
			return strings.Replace(ir, "store i64 %a, ptr %a.addr, align 8", "store atomic i64 %a, ptr %a.addr release, align 8", 1)
		}},
		{name: "escaped alloca", edit: func(ir string) string {
			ir = strings.Replace(ir, "  ; EXTRA_BEFORE_LOAD", "  call void @sink(ptr %a.addr)\n  ; EXTRA_BEFORE_LOAD", 1)
			return ir + "\ndeclare void @sink(ptr)\n"
		}},
		{name: "cross-block load", edit: func(ir string) string {
			return strings.Replace(ir, "  ; EXTRA_BEFORE_LOAD", "  br label %body\nbody:\n  ; EXTRA_BEFORE_LOAD", 1)
		}},
		{name: "load before store", edit: func(ir string) string {
			ir = strings.Replace(ir, "  %a0 = load i64, ptr %a.addr, align 8\n", "", 1)
			ir = strings.Replace(ir, "  ; EXTRA_BEFORE_LOAD", "  %a0 = load i64, ptr %a.addr, align 8\n  store i64 %a, ptr %a.addr, align 8\n  ; EXTRA_BEFORE_LOAD", 1)
			return ir
		}},
		{name: "load range metadata", edit: func(ir string) string {
			ir = strings.Replace(ir, "%a0 = load i64, ptr %a.addr, align 8", "%a0 = load i64, ptr %a.addr, align 8, !range !0", 1)
			return ir + "\n!0 = !{i64 0, i64 10}\n"
		}},
		{name: "alloca use budget", edit: func(ir string) string {
			var uses strings.Builder
			for i := 0; i < 256; i++ {
				fmt.Fprintf(&uses, "  %%extra%d = load i64, ptr %%a.addr, align 8\n", i)
			}
			return strings.Replace(ir, "  ; EXTRA_BEFORE_LOAD", uses.String()+"  ; EXTRA_BEFORE_LOAD", 1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ir := strings.ReplaceAll(splitHalfRawIRTemplate, "%%", "%")
			if test.edit != nil {
				ir = test.edit(ir)
			}
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatalf("parse raw matcher fixture: %v\n%s", err, ir)
			}
			defer g.dispose()
			got := canonicalizeSplitHalfUnsignedAdds(g.module)
			if test.wantFold && got != 1 {
				t.Fatalf("rewrote %d roots, want one", got)
			}
			if !test.wantFold && got != 0 {
				t.Fatalf("rewrote %d roots, want fail-closed decline", got)
			}
			if err := g.verify(); err != nil {
				t.Fatalf("fixture after rewrite/decline failed verification: %v", err)
			}
		})
	}
}

// This isolates the matcher scan on a large module containing many ordinary,
// nonmatching ORs. It is a compile-time overhead probe; it does not run LLVM O3.
func BenchmarkSplitHalfMatcherManyNonmatchingORs(b *testing.B) {
	const count = 50000
	var ir strings.Builder
	ir.Grow(count * 48)
	ir.WriteString("define i64 @many(i64 %x) {\nentry:\n")
	previous := "%x"
	for i := 0; i < count; i++ {
		fmt.Fprintf(&ir, "  %%v%d = or i64 %s, %d\n", i, previous, i+1)
		previous = fmt.Sprintf("%%v%d", i)
	}
	fmt.Fprintf(&ir, "  ret i64 %s\n}\n", previous)
	g, err := parseLLVMIRForSplitHalfTest(ir.String())
	if err != nil {
		b.Fatalf("parse synthetic OR-heavy module: %v", err)
	}
	defer g.dispose()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rewrites := canonicalizeSplitHalfUnsignedAdds(g.module); rewrites != 0 {
			b.Fatalf("rewrote %d ordinary ORs", rewrites)
		}
	}
}

func TestOptimizedModuleCacheKeySeparatesSplitAddPolicy(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "split_half_add_cache_policy.elisa", splitHalfAddProbeSource)
	withPass, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compile split-add cache key input: %v", err)
	}
	defer withPass.dispose()
	withoutPass, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compile second split-add cache key input: %v", err)
	}
	defer withoutPass.dispose()
	if withPass.printModule() != withoutPass.printModule() {
		t.Fatal("test setup must give the two pass policies identical input IR")
	}
	withoutPass.emitDebugInfo = true // Models the existing fail-closed policy gate.
	if optimizedModuleCacheKey(withPass, OptimizationLevel3, "triple", "cpu", "features") ==
		optimizedModuleCacheKey(withoutPass, OptimizationLevel3, "triple", "cpu", "features") {
		t.Fatal("optimized-module cache key ignored the split-add pass policy for identical IR")
	}
}

func TestOptimizedModuleDiskCacheMissesAcrossSplitAddPolicies(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "split_half_add_disk_cache_policy.elisa", splitHalfAddProbeSource)
	root := t.TempDir()
	compileInput := func(debugPolicy bool) *llvmGenerator {
		t.Helper()
		g, err := compileLLVMModule(result, OptimizationLevel3, DefaultPackedLoweringProfile())
		if err != nil {
			t.Fatalf("compile split-add cache module: %v", err)
		}
		discardLLVMValueNames(g.context)
		g.emitDebugInfo = debugPolicy
		if err := g.ensureTargetMachine(); err != nil {
			g.dispose()
			t.Fatalf("initialize split-add target: %v", err)
		}
		return g
	}
	newSession := func() *IncrementalObjectSession {
		session := NewIncrementalObjectSession(2, 32<<20)
		session.EnableDiskCache(root, "split-add-policy-test", 64<<20)
		return session
	}

	plain := compileInput(false)
	plainInput := plain.printModule()
	plainSession := newSession()
	optimizedPlain, err := plainSession.optimizeObjectModule(plain, OptimizationLevel3)
	if err != nil {
		plain.dispose()
		t.Fatalf("optimize plain-policy module: %v", err)
	}
	if optimizedPlain != plain {
		defer optimizedPlain.dispose()
	}
	defer plain.dispose()

	// A fresh session first proves the exact artifact is present and replayable.
	warmSamePolicy := compileInput(false)
	if warmSamePolicy.printModule() != plainInput {
		warmSamePolicy.dispose()
		t.Fatal("same-policy module input IR changed")
	}
	samePolicySession := newSession()
	warmOptimized, err := samePolicySession.optimizeObjectModule(warmSamePolicy, OptimizationLevel3)
	if err != nil {
		warmSamePolicy.dispose()
		t.Fatalf("replay same-policy optimized module: %v", err)
	}
	if warmOptimized != warmSamePolicy {
		defer warmOptimized.dispose()
	}
	defer warmSamePolicy.dispose()
	if samePolicySession.Stats().OptimizedModulesReused != 1 {
		t.Skip("LLVM did not admit this fixture's optimized bitcode for exact disk replay")
	}

	// The second input is byte-for-byte the same IR but uses the debug policy,
	// which disables the split-add rewrite. It must miss and match its own cold
	// optimization result rather than replay the plain-policy artifact.
	debugPolicy := compileInput(true)
	defer debugPolicy.dispose()
	if debugPolicy.printModule() != plainInput {
		t.Fatal("debug-policy cache test must use byte-identical input IR")
	}
	debugSession := newSession()
	optimizedDebug, err := debugSession.optimizeObjectModule(debugPolicy, OptimizationLevel3)
	if err != nil {
		t.Fatalf("optimize debug-policy module: %v", err)
	}
	if optimizedDebug != debugPolicy {
		defer optimizedDebug.dispose()
	}
	if debugSession.Stats().OptimizedModulesReused != 0 {
		t.Fatal("different split-add policy reused a plain-policy optimized artifact")
	}

	debugCold := compileInput(true)
	defer debugCold.dispose()
	if err := debugCold.optimizeModule(OptimizationLevel3); err != nil {
		t.Fatalf("independently optimize debug-policy module: %v", err)
	}
	if optimizedDebug.printModule() != debugCold.printModule() {
		t.Fatal("debug-policy cache miss differs from independent optimization")
	}
}

func TestSplitHalfUnsignedAddCanonicalizationRuntimeParity(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is required for the native split-add parity check")
	}
	result := parseAndAnalyzeBackendTest(t, "split_half_add_runtime.elisa", splitHalfAddProbeSource)
	dir := t.TempDir()
	driver := filepath.Join(dir, "driver.c")
	if err := os.WriteFile(driver, []byte(`#include <stdint.h>
extern uint64_t split_half_add(uint64_t, uint64_t);
int main(void) {
    static const uint64_t pairs[][2] = {
        {0, 0}, {0, 1}, {UINT64_MAX, 1}, {UINT64_MAX, UINT64_MAX},
        {UINT64_C(0xffffffff), 1},
        {UINT64_C(0xffffffff00000000), UINT64_C(0xffffffff)},
        {UINT64_C(0x7fffffffffffffff), 1},
        {UINT64_C(0x8000000000000000), UINT64_C(0x8000000000000000)}
    };
    for (unsigned i = 0; i < sizeof(pairs) / sizeof(pairs[0]); ++i)
        if (split_half_add(pairs[i][0], pairs[i][1]) != pairs[i][0] + pairs[i][1]) return (int)i + 1;
    uint64_t x = UINT64_C(0x243f6a8885a308d3);
    for (unsigned i = 0; i < 5000; ++i) {
        x ^= x << 13; x ^= x >> 7; x ^= x << 17;
        uint64_t y = x * UINT64_C(0x9e3779b97f4a7c15) + i;
        if (split_half_add(x, y) != x + y) return 20;
    }
    return 0;
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
		t.Run("O"+strconv.Itoa(int(level)), func(t *testing.T) {
			object := filepath.Join(dir, "split-add-O"+strconv.Itoa(int(level))+".o")
			if err := WriteLLVMObjectFileWithOptions(result, object, LLVMObjectEmitOptions{OptLevel: level, Serial: true}); err != nil {
				t.Fatalf("emit O%d object: %v", level, err)
			}
			executable := filepath.Join(dir, "split-add")
			if output, err := exec.Command(clang, driver, object, "-o", executable).CombinedOutput(); err != nil {
				t.Fatalf("link O%d object: %v\n%s", level, err, output)
			}
			if output, err := exec.Command(executable).CombinedOutput(); err != nil {
				t.Fatalf("O%d split-add parity failed: %v\n%s", level, err, output)
			}
		})
	}
}
