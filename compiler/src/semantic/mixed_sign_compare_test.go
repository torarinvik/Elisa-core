package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS: codegen compares in the LEFT operand's type. `x <= t.len` with x: usize compares
// unsigned, so t.len == -1 reads as 2^64-1 and the comparison is TRUE at runtime although the
// plain-value comparison is false. The prover must not use the value comparison there.
func TestMixedSignComparisonDeclines(t *testing.T) {
	bad := map[string]string{
		// v == -1: codegen compares in the left (unsigned) type, so the runtime result is true and the
		// ensure is false there; value reasoning proves it. (selfhost-proofs states these over
		// sview.len == -1; main assumes sview.len >= 0, so they are stated over an i64 here.)
		"usize_le_i64_minus_one": "def f(v: i64) -> bool:\n    ensure v >= 0 or result == false\n    x: usize = 0\n    return x <= v\n",
		"usize_eq_i64_minus_one": "def f2(v: i64, x: usize) -> bool:\n    requires x == 18446744073709551615\n    ensure result == false\n    return x == v\n",
		// i32 < u64 truncates the u64 (a == 2^32 compares as 0).
		"i32_lt_u64": "def h(a: u64, b: i32) -> bool:\n    requires b == 0\n    ensure result == (a > 0)\n    return b < a\n",
	}
	for name, src := range bad {
		errs := strictSMTErrors(t, "mixed_"+name+".elisa", src)
		if !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
			t.Fatalf("%s: mixed-sign comparison must not be modeled as a value comparison; errs=%v", name, errs)
		}
	}
	// When the right operand provably fits the left type, the comparison is exact.
	ok := map[string]string{
		"guarded":   "def k(t: sview) -> bool:\n    requires t.len >= 0\n    ensure result\n    x: usize = 0\n    return x <= t.len\n",
		"same_type": "def m(a: i64, b: i64) -> bool:\n    requires a < b\n    ensure result\n    return a <= b\n",
	}
	for name, src := range ok {
		if errs := strictSMTErrors(t, "mixed_ok_"+name+".elisa", src); len(errs) != 0 {
			t.Fatalf("%s: should prove, got:\n%s", name, strings.Join(errs, "\n"))
		}
	}
}
