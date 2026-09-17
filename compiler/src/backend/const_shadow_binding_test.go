//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// A parameter that SHADOWS a const is a runtime value. The backend's constant evaluator read the
// global const table without looking at the function's bindings, so `xs[N]` "was" the in-bounds
// constant 3 and its bounds check was dropped -- `f(9)` then read past the array, silently.
func TestShadowingParamKeepsIndexBoundsCheck(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "shadow_param_index.elisa", `const N = 3

def f(N: i64) -> i64:
    xs: array[i64, 4] = [1, 2, 3, 4]
    return xs[N]
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("an index by a parameter shadowing a const must keep its bounds check, got:\n%s", output)
	}
}

// The index emitter folds a whole `XS[1]` when it const-evaluates. With a parameter shadowing a
// const list, that fold returned the GLOBAL list's element -- `ret i64 20` whatever array the
// caller passed -- because the backend's const lookup ignored the function's bindings.
func TestShadowingParamIndexIsNotFoldedFromConst(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "shadow_param_const_list_index.elisa", `const XS = [10, 20, 30]

def f(XS: array[i64, 3]) -> i64:
    return XS[1]
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "ret i64 20") || !strings.Contains(output, "load i64") {
		t.Fatalf("`XS[1]` on a parameter shadowing a const list must read the parameter, got:\n%s", output)
	}
}

// A cstr's `.len` is strlen: the fold of a cstr const's `.len` must stop at an embedded NUL,
// exactly as the runtime `ctx_strlen` call on the same const does.
func TestCStrConstLenFoldsToStrlen(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "cstr_const_len.elisa", `const G: cstr = "ab\0cd"
const N = G.len

def f() -> i64:
    return N
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "ret i64 2") {
		t.Fatalf("`G.len` for cstr \"ab\\0cd\" is strlen = 2, got:\n%s", output)
	}
}
