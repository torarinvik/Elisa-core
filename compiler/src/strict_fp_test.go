package main

import (
	"strings"
	"testing"
)

// FP contraction is on by default (clang's -ffp-contract=on), so `a + b * c` may become a single
// fused multiply-add with one rounding instead of two. It is expression-local: a product that was
// bound to a local and added in a later statement keeps both roundings, as in C. That is invisible in most code and decisive
// in one place: reproducing another implementation's floating-point results bit-for-bit. Python's
// `random.uniform(a, b)` is `a + (b - a) * random()`, and for a negative `a` the add cancels, so the
// fused and unfused forms disagree in the last ulp for around a quarter of all draws.
//
// `-fstrict-fp` / ELISACORE_STRICT_FP is the opt-out: no contraction, no reciprocal, no fast-math.
func TestStrictFPDropsContraction(t *testing.T) {
	t.Run("default contracts", func(t *testing.T) {
		t.Setenv("ELISACORE_STRICT_FP", "")
		out := emitFoldProbeIR(t, "strict_fp_probe.elisa", "-O0")
		if !strings.Contains(out, "fmul contract") || !strings.Contains(out, "fadd contract") {
			t.Fatalf("expected the default build to allow contraction:\n%s", out)
		}
		if split := functionIR(out, "blend_split"); split == "" || strings.Contains(split, "contract") {
			t.Fatalf("a product added in a later statement must not be contracted:\n%s", split)
		}
		if compound := functionIR(out, "blend_compound"); !strings.Contains(compound, "fadd contract") {
			t.Fatalf("`total += a * b` is one expression and may contract:\n%s", compound)
		}
	})

	t.Run("strict-fp emits bare FP with no flags", func(t *testing.T) {
		t.Setenv("ELISACORE_STRICT_FP", "1")
		out := emitFoldProbeIR(t, "strict_fp_probe.elisa", "-O0")
		if strings.Contains(out, "fmul contract") || strings.Contains(out, "fadd contract") ||
			strings.Contains(out, "fsub contract") {
			t.Fatalf("strict FP must not stamp `contract` on any FP op:\n%s", out)
		}
		if !strings.Contains(out, "fmul double") || !strings.Contains(out, "fadd double") {
			t.Fatalf("expected bare fmul/fadd under strict FP:\n%s", out)
		}
	})

	// Strict FP beats every other tier, @fast_math included: a program asking for reproducible
	// IEEE results is asking for them everywhere.
	t.Run("strict-fp beats fast-math", func(t *testing.T) {
		t.Setenv("ELISACORE_FAST_MATH", "1")
		t.Setenv("ELISACORE_STRICT_FP", "1")
		out := emitFoldProbeIR(t, "strict_fp_probe.elisa", "-O0")
		if strings.Contains(out, "fmul fast") || strings.Contains(out, "fadd fast") {
			t.Fatalf("strict FP must override -ffast-math:\n%s", out)
		}
	})
}

// functionIR returns the body of the first defined function whose name contains name.
func functionIR(module, name string) string {
	for _, chunk := range strings.Split(module, "\ndefine ") {
		header, _, _ := strings.Cut(chunk, "\n")
		if strings.Contains(header, "@") && strings.Contains(header, name) {
			body, _, _ := strings.Cut(chunk, "\n}")
			return body
		}
	}
	return ""
}
