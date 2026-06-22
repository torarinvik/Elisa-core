//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// AlignUp is the nonlinear alignment shape from the shadPS4 port:
//   result = ((value + alignment - 1) / alignment) * alignment
// relating `/`, `%`, and `*` over the SAME divisor. When the operands are bounded so the intermediate
// `value + alignment - 1` and the product `q*alignment` cannot overflow the type, BOTH postconditions
// discharge:
//   - `result >= value`           (rounding up never goes below the input)
//   - `(result % alignment) == 0` (the result is a true multiple of the divisor)
// The ground Euclidean lemmas (q*r+rem==l, 0<=rem<r, (q*r)%r==0, l-r < q*r <= l) supply the closed
// form of the nonlinear product q*r so the solver reasons linearly about the rounding bracket.
func TestSMTAlignUpBoundedDischarges(t *testing.T) {
	src := `
law Bounded(self: u64, lo: u64, hi: u64) = self >= lo and self <= hi
type AlignValue = u64 is Bounded[0, 1000000]
type AlignAmount = u64 is Bounded[1, 4096]

def Module_AlignUp(value: AlignValue, alignment: AlignAmount) -> u64:
    ensure result >= value
    ensure (result % alignment) == 0
    return ((value + alignment - 1) / alignment) * alignment
`
	result := analyzeContractStrict(t, "align_up_bounded.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("bounded (no-overflow) AlignUp postconditions must discharge; got: %v", errs)
	}
}

// The ALIGN-DOWN companion (`(value / alignment) * alignment`) is the other multiple-of-divisor shape:
// the product cannot overflow (it is <= value), so divisibility AND `result <= value` discharge. This
// exercises the `(q*r) % r == 0` and `q*r <= l` lemmas directly on an unbounded u64.
func TestSMTAlignDownDischarges(t *testing.T) {
	src := `
def KernelMemory_AlignDown(value: u64, alignment: u64) -> u64:
    ensure result <= value
    ensure alignment == 0 or (result % alignment) == 0
    if alignment == 0:
        return value
    return (value / alignment) * alignment
`
	result := analyzeContractStrict(t, "align_down.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("AlignDown postconditions must discharge; got: %v", errs)
	}
}

// SOUNDNESS (negative polarity): the STRICT `>` postcondition is FALSE when `value` is already a
// multiple of alignment (result == value). Even with the Euclidean lemmas asserted, the goal must be
// REFUTED with a counterexample — the lemmas are theorems, so they remove no model the negated goal
// needs and can never fabricate a proof. If this ever passes the lemmas have over-asserted.
func TestSMTAlignUpStrictGTRefuted(t *testing.T) {
	src := `
law Bounded(self: u64, lo: u64, hi: u64) = self >= lo and self <= hi
type AlignValue = u64 is Bounded[0, 1000000]
type AlignAmount = u64 is Bounded[1, 4096]

def AlignUpStrict(value: AlignValue, alignment: AlignAmount) -> u64:
    ensure result > value
    return ((value + alignment - 1) / alignment) * alignment
`
	result := analyzeContractStrict(t, "align_up_strict_bad.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("`result > value` is false when value is already aligned; it must be refuted, got: %v", result.Errors())
	}
}

// SOUNDNESS (overflow polarity): the UNBOUNDED u64 AlignUp is NOT universally valid — at value=u64::MAX
// the intermediate `value + alignment - 1` wraps, so `result >= value` genuinely fails. The wrap model
// must keep that real counterexample; the lemmas must not paper over it. This pins that a wrap-violating
// postcondition stays refuted (the prover's exact-wrap arithmetic, not the lemmas, decides here).
func TestSMTUnboundedAlignUpOverflowRefuted(t *testing.T) {
	src := `
def Module_AlignUp(value: u64, alignment: u64) -> u64:
    ensure result >= value
    if alignment == 0:
        return value
    return ((value + alignment - 1) / alignment) * alignment
`
	result := analyzeContractStrict(t, "align_up_overflow.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("unbounded u64 AlignUp overflows at value=u64::MAX; `result >= value` must stay refuted, got: %v", result.Errors())
	}
}
