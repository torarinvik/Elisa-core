//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A recursive ghost fold returning a named tuple is read through its body, and two calls of it
// with provably equal arguments agree, so a loop invariant that tracks the fold preserves.
func TestSMTNamedTupleEquationPreservesFoldInvariant(t *testing.T) {
	const fold = `
ghost def fold_from(text: sview, position: usize, total: i64) -> (valid: bool, value: i64):
    requires position <= text.len
    decreases text.len - position
    return (true, total) if position == text.len
    return (false, 0) if text[position] > 9 or total > 1000
    return fold_from(text, position + 1, total + text[position].i64())
`
	for _, tc := range []struct {
		name, step string
		proven     bool
	}{
		{"same_fold", "total + text[position].i64()", true},
		{"different_step", "total + text[position].i64() + 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := fold + `
def sum(text: sview) -> (valid: bool, value: i64):
    position: mutable usize = 0
    total: mutable i64 = 0
    while position < text.len |position, text, total|:
        decreases text.len - position
        invariant position <= text.len
        invariant total >= 0 and total <= 1009
        invariant fold_from(text, position, total).valid == fold_from(text, 0, 0).valid
        if text[position] > 9 or total > 1000:
            return (false, 0)
        total <- ` + tc.step + `
        position <- position + 1
    return (true, total)
`
			r := analyzeContractStrict(t, "tuple_equation_"+tc.name+".elisa", src)
			es := strings.Join(r.Errors(), "\n")
			if tc.proven && es != "" {
				t.Fatalf("expected the fold invariant to prove: %s", es)
			}
			if !tc.proven && es == "" {
				t.Fatal("an invariant over a different fold step was accepted")
			}
		})
	}
}

// A body-level assert whose quantifier the parser lowered to a runtime loop changes no state,
// so the sequential model keeps the body and the loop measure proves.
func TestLoopMeasureAdmitsQuantifiedBodyAssert(t *testing.T) {
	src := `
def ok(x: u8) -> bool:
    return x < 10

def scan(text: sview) -> bool:
    position: mutable usize = 0
    while position < text.len |position, text|:
        decreases text.len - position
        if not ok(text[position]):
            return false
        position <- position + 1
        assert position <= text.len or (forall k in 0..<position: ok(text[k]))
    return true
`
	r := analyzeContractStrict(t, "quantified_body_assert.elisa", src)
	if es := strings.Join(r.Errors(), "\n"); es != "" {
		t.Fatalf("expected the loop measure to prove: %s", es)
	}
}

// An element store keeps the array's length, so a loop that writes elements of a mutable
// array parameter preserves an invariant about its count.
func TestLoopElementStoreKeepsArrayLength(t *testing.T) {
	src := `
def clear(xs: mutable darray[i64]&) -> void can[Abort.Panic]:
    n: usize = xs.count
    i: mutable usize = 0
    while i < n |xs, n, i|:
        invariant xs.count == n
        xs[i] <- 0
        i <- i + 1
`
	r := analyzeContractStrict(t, "element_store_length.elisa", src)
	if es := strings.Join(r.Errors(), "\n"); es != "" {
		t.Fatalf("expected the count invariant to prove: %s", es)
	}
}

func TestSMTStoreBaseArray(t *testing.T) {
	for in, want := range map[string]string{
		"v_xs":               "v_xs",
		"(store v_xs v_i 0)": "v_xs",
		"(store (store |a b| (+ i 1) 2) i (f x))": "|a b|",
		"(select v_xs 0)":                         "(select v_xs 0)",
	} {
		if got := smtStoreBaseArray(in); got != want {
			t.Errorf("smtStoreBaseArray(%q) = %q, want %q", in, got, want)
		}
	}
}
