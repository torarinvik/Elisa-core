//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"
)

// The immutable-local slice keeps a local whose symbol the context mentions, and (transitively) the
// locals its definition mentions; it drops a local nothing else refers to.
func TestSliceImmutableLocalHypothesesKeepsOnlyReachableLocals(t *testing.T) {
	locals := "(assert (= v_a (+ v_b 1)))\n" +
		"(assert (= v_b (* v_p 2)))\n" +
		"(assert (= v_next (* v_state 1099511628211)))\n" +
		"(assert (= v_rem (mod v_p 8)))\n(assert (>= v_rem 0))\n(assert (< v_rem 8))\n"
	got := sliceImmutableLocalHypotheses(locals, "(assert (not (> v_a 0)))\n")
	want := "(assert (= v_a (+ v_b 1)))\n(assert (= v_b (* v_p 2)))\n"
	if got != want {
		t.Fatalf("slice:\n%s\nwant:\n%s", got, want)
	}
	// A modulo local keeps its bound lines together with its definition.
	got = sliceImmutableLocalHypotheses(locals, "(assert (not (< v_rem 8)))\n")
	if !strings.Contains(got, "(assert (< v_rem 8))") || !strings.Contains(got, "(assert (= v_rem (mod v_p 8)))") || strings.Contains(got, "v_next") {
		t.Fatalf("modulo local slice wrong:\n%s", got)
	}
	// Declarations and single-symbol bounds in the preamble do not make a local reachable; a
	// two-symbol preamble assertion does.
	if ctx := smtPreambleContext("(declare-const v_next Int)\n(assert (< v_next 256))\n"); strings.Contains(ctx, "v_next") {
		t.Fatalf("unary preamble lines must not connect symbols: %q", ctx)
	}
	if ctx := smtPreambleContext("(assert (= v_next v_goal))\n"); !strings.Contains(ctx, "v_next") {
		t.Fatalf("a relating preamble assertion must connect symbols: %q", ctx)
	}
}

// The linear-arithmetic same-width reinterpretation equals the bit-vector one for every Int.
func TestSMTSameWidthReinterpretMatchesBitVectorRead(t *testing.T) {
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH")
	}
	for _, signed := range []bool{false, true} {
		q := "(declare-const x Int)\n(assert (not (= " + smtSameWidthReinterpret("x", signed, 8) + " " +
			smtBitvectorRead("((_ int2bv 8) x)", signed, 8) + ")))\n(check-sat)\n"
		cmd := exec.Command("z3", "-in", "-T:60")
		cmd.Stdin = strings.NewReader(q)
		res, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(res)) != "unsat" {
			t.Fatalf("signed=%v: encodings differ (z3: %q, %v)", signed, res, err)
		}
	}
}

// CPU: a termination proof over a view length (`decreases value.len.usize() - index`) must fit the
// default per-query budget. With `.usize()` of the i64 length encoded through int2bv/bv2nat and the
// fold's 64-bit multiply in every hypothesis set, it needed more than 6M z3 units and was declined.
func TestSViewLengthTerminationFitsDefaultBudget(t *testing.T) {
	src := `
const PRIME: u64 = 0x100000001b3

ghost def fold_from(value: sview, index: usize, state: u64) -> u64:
    requires value.len > 0 and value.len.usize().i64() == value.len
    requires index <= value.len.usize()
    decreases value.len.usize() - index
    return state if index == value.len.usize()
    byte: u8 = value[index].u8()
    next_state: u64 = (state ^ byte.u64()) * PRIME
    return fold_from(value, index + 1.usize(), next_state)
`
	result := analyzeContractStrict(t, "fold_from.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "decreases") || strings.Contains(e, "recursive proof declined") {
			t.Fatalf("the decreasing measure is provable within the default budget; got: %v", result.Errors())
		}
	}
}
