//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// WP over AUG-ASSIGNMENT: `y += 1` is transported as `y := y + 1`, threading the postcondition back to
// the parameter. Before this, an aug-assignment made WP decline the whole body (the var stayed free),
// so a strict build could not discharge `ensure result >= 2`.
func TestWPProvesThroughAugAssign(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result >= 2
    y: mutable i64 = x
    y += 1
    return y
`
	if errs := analyzeContractStrict(t, "wp_augassign.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("`y += 1` with x>0 gives y=x+1>=2; WP should prove it, got: %v", errs)
	}
}

// SOUNDNESS: WP through aug-assign must not prove a FALSE postcondition. `y -= 1` from x>0 yields
// y=x-1>=0, which does NOT establish `result >= 1` (x=1 -> y=0), so it must error under -strict.
func TestWPAugAssignDoesNotProveFalse(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result >= 1
    y: mutable i64 = x
    y -= 1
    return y
`
	errs := strings.Join(analyzeContractStrict(t, "wp_augassign_false.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("`y -= 1` from x>0 can reach 0; `result >= 1` must NOT prove, got: %v", errs)
	}
}

// WP over a single-level IF/ELSE merge: the postcondition must hold on BOTH branches, combined as
// (c -> wp(then)) and (not c -> wp(else)). Here then=x (x>5 so >=1) and else=1, both satisfy
// `result >= 1`, so the merge discharges.
func TestWPProvesThroughIfMerge(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result >= 1
    y: mutable i64 = 0
    if x > 5:
        y <- x
    else:
        y <- 1
    return y
`
	if errs := analyzeContractStrict(t, "wp_ifmerge.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("both branches satisfy result>=1; the if-merge WP should prove it, got: %v", errs)
	}
}

// SOUNDNESS: an if-merge where ONE branch violates the postcondition must not prove. The else-branch
// assigns 0, so `result >= 1` fails there — the merge's `(not c -> wp(else))` conjunct is false.
func TestWPIfMergeDoesNotProveWhenOneBranchFails(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result >= 1
    y: mutable i64 = 0
    if x > 5:
        y <- x
    else:
        y <- 0
    return y
`
	errs := strings.Join(analyzeContractStrict(t, "wp_ifmerge_false.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("the else branch assigns 0, violating result>=1; the merge must NOT prove, got: %v", errs)
	}
}

// Combined: an aug-assignment INSIDE a merged branch is transported too (`y += 2` in the then-branch).
func TestWPIfMergeWithAugAssignBranch(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result >= 1
    y: mutable i64 = 1
    if x > 5:
        y += 2
    else:
        y <- 1
    return y
`
	if errs := analyzeContractStrict(t, "wp_ifmerge_aug.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("then: y=1+2=3>=1, else: y=1>=1; the merge should prove, got: %v", errs)
	}
}

// SOUNDNESS (audit, cluster A): WP transport of an aug-assignment must MODEL unsigned wraparound. The
// synthetic `x := x OP e` node has no exprTypes entry, so the wrap width was lost and `y += 100` on a u8
// (200 -> 44) wrongly proved `result >= x`. The fix stamps the target's type onto the synthetic node so
// the `(mod 2^W)` wrap model engages and the false postcondition correctly declines.
func TestWPAugAssignModelsUnsignedWrap(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"add", "    y: mutable u8 = x\n    y += 100\n    return y"},
		{"mul", "    y: mutable u8 = x\n    y *= 2\n    return y"},
		{"ifmerge", "    y: mutable u8 = x\n    if x >= 200:\n        y += 100\n    else:\n        y += 0\n    return y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "def f(x: u8) -> u8:\n    requires x >= 200\n    ensure result >= x\n" + tc.body + "\n"
			errs := strings.Join(analyzeContractStrict(t, "wp_wrap_"+tc.name+".elisa", src).Errors(), "\n")
			if !strings.Contains(errs, "could not be proven statically") {
				t.Fatalf("u8 %s wraps (200->small), so `result >= x` is false and must NOT prove; got: %v", tc.name, errs)
			}
		})
	}
}

// The non-wrapping counterpart still proves: a WIDE accumulator that provably cannot overflow keeps the
// legitimate WP aug-assign proof (the fix only adds the wrap model, it does not blanket-decline).
func TestWPAugAssignNonWrappingStillProves(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    requires x < 1000
    ensure result >= x
    y: mutable i64 = x
    y += 5
    return y
`
	if errs := analyzeContractStrict(t, "wp_nowrap.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("a bounded i64 `y += 5` cannot overflow; `result >= x` should still prove, got: %v", errs)
	}
}
