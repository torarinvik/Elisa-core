//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// GUARD-`if` loop-termination modeling — adversarial soundness battery.
//
// SOUNDNESS DOMINATES: every NEGATIVE here is a potential soundness hole (a non-terminating loop the
// prover must NOT "prove"). The all-paths rule must reject the loop whenever ANY non-exiting path
// (including the implicit no-op `else`) fails to strictly decrease the measure.
// ---------------------------------------------------------------------------

func guardIfRejected(t *testing.T, name, src string) *Result {
	t.Helper()
	result := analyzeContractStrict(t, name, src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("%s: loop MUST be rejected (could-not-be-proven error), got errors: %v\nproof: %+v", name, result.Errors(), result.ProofReport)
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 0 {
		t.Fatalf("%s: loop must NOT be recorded as SMT-proven, got %d proven", name, got)
	}
	return result
}

func guardIfProved(t *testing.T, name, src string) *Result {
	t.Helper()
	result := analyzeContractStrict(t, name, src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") || strings.Contains(e, "could not be proven") {
			t.Fatalf("%s: loop must PROVE termination, got error: %v", name, e)
		}
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("%s: expected exactly one SMT-proven loop termination, got %d: %+v", name, got, result.ProofReport)
	}
	return result
}

// ===================== NEGATIVES (must stay rejected) =====================

// progress only in `if`, empty else — the implicit no-op else path does NOT decrease.
func TestGuardIf_ProgressOnlyInIfEmptyElse_Rejected(t *testing.T) {
	src := `def loop(n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        if j < n:
            j <- j + 1
`
	guardIfRejected(t, "progress_only_if_empty_else.elisa", src)
}

// else INCREASES the measure (n-j): else path fails.
func TestGuardIf_ElseIncreasesMeasure_Rejected(t *testing.T) {
	src := `def loop(n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        if j < n - 1:
            j <- j + 1
        else:
            j <- j - 1
`
	guardIfRejected(t, "else_increases_measure.elisa", src)
}

// else makes no progress on the measure variable (modifies a different variable).
func TestGuardIf_ElseNoProgressOnMeasureVar_Rejected(t *testing.T) {
	src := `def loop(n: i64):
    requires n >= 0
    j: mutable i64 = 0
    k: mutable i64 = 0
    while j < n:
        decreases n - j
        if j < n - 1:
            j <- j + 1
        else:
            k <- k + 1
`
	guardIfRejected(t, "else_no_progress.elisa", src)
}

// a CALL in one branch poisons capture for that branch → reject the whole loop.
func TestGuardIf_CallInBranch_Rejected(t *testing.T) {
	src := `def f(x: i64) -> i64:
    return x

def loop(n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        if j < n - 1:
            j <- f(j)
        else:
            j <- j + 1
`
	guardIfRejected(t, "call_in_branch.elisa", src)
}

func TestGuardIf_OpaqueExitGuardFallthroughProgress_Proves(t *testing.T) {
	src := `def hit(x: usize) -> bool:
    return x == 5

def loop(n: usize):
    j: mutable usize = 0
    while j < n:
        decreases n - j
        if hit(j):
            return
        j <- j + 1
`
	guardIfProved(t, "opaque_exit_guard_fallthrough_progress.elisa", src)
}

func TestGuardIf_IgnoredBookkeepingWithCapacityMeasure_Proves(t *testing.T) {
	src := `def hit(x: usize) -> bool:
    return x == 5

def loop(capacity: usize):
    slot: mutable usize = 0
    insert_slot: mutable usize = capacity
    probed: mutable usize = 0
    while probed < capacity:
        decreases capacity - probed
        if slot == 0:
            return
        if hit(slot):
            return
        insert_slot <- slot if slot == 2 and insert_slot == capacity else insert_slot
        slot <- slot + 1
        probed <- probed + 1
`
	guardIfProved(t, "ignored_bookkeeping_capacity_measure.elisa", src)
}

// nested `if` where one deep path does not decrease (the inner empty-else no-op path).
func TestGuardIf_NestedDeepPathNoDecrease_Rejected(t *testing.T) {
	src := `def loop(n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        if j < n - 1:
            if j < n - 2:
                j <- j + 1
        else:
            j <- j + 1
`
	guardIfRejected(t, "nested_deep_no_decrease.elisa", src)
}

// `while true` with if/else both non-decreasing.
func TestGuardIf_WhileTrueBothNonDecreasing_Rejected(t *testing.T) {
	src := `def loop(n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while true:
        decreases n - j
        if j < n:
            j <- j - 1
        else:
            j <- j - 1
`
	guardIfRejected(t, "while_true_both_nondecreasing.elisa", src)
}

// a loop containing `continue` is rejected by the conservative choice.
func TestGuardIf_ContinueRejected(t *testing.T) {
	src := `def loop(n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        if j < 0:
            continue
        j <- j + 1
`
	guardIfRejected(t, "continue_rejected.elisa", src)
}

// a branch whose "progress" is a field-target WRITE (not a modeled scalar) → reject.
func TestGuardIf_FieldTargetWriteBranch_Rejected(t *testing.T) {
	src := `struct Box:
    j: i64

def loop(b: mutable Box&, n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        decreases n - j
        if j < n - 1:
            b.j <- j + 1
        else:
            j <- j + 1
`
	guardIfRejected(t, "field_target_write_branch.elisa", src)
}

// ===================== POSITIVES (should now PROVE) =====================

// both branches decrease: `if c: i<-i+2 else: i<-i+1`, measure `n-i`.
func TestGuardIf_BothBranchesDecrease_Proved(t *testing.T) {
	src := `def loop(n: i64):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        invariant i >= 0
        decreases n - i
        if i < n - 2:
            i <- i + 2
        else:
            i <- i + 1
`
	guardIfProved(t, "both_branches_decrease.elisa", src)
}

// early-exit: `if done: break else: i<-i+1` — the break path is obligation-free.
func TestGuardIf_EarlyBreak_Proved(t *testing.T) {
	src := `def loop(n: i64, done: bool):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        invariant i >= 0
        decreases n - i
        if done:
            break
        else:
            i <- i + 1
`
	guardIfProved(t, "early_break.elisa", src)
}

// break with no else (implicit no-op else still decreases via the trailing step).
func TestGuardIf_BreakThenStep_Proved(t *testing.T) {
	src := `def loop(n: i64, done: bool):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        invariant i >= 0
        decreases n - i
        if done:
            break
        i <- i + 1
`
	guardIfProved(t, "break_then_step.elisa", src)
}

// guarded array shift + scalar step where both paths progress.
func TestGuardIf_GuardedArrayShiftPlusStep_Proved(t *testing.T) {
	src := `def loop(arr: darray[i64], n: i64):
    requires n >= 0
    j: mutable i64 = 0
    while j < n:
        invariant j >= 0
        decreases n - j
        if j < n - 1:
            arr[j] <- arr[j + 1]
            j <- j + 1
        else:
            j <- j + 1
`
	guardIfProved(t, "guarded_array_shift.elisa", src)
}

// a return in a branch is also an exit path (obligation-free), with the else stepping.
func TestGuardIf_EarlyReturn_Proved(t *testing.T) {
	src := `def loop(n: i64, stop: bool):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        invariant i >= 0
        decreases n - i
        if stop:
            return
        else:
            i <- i + 1
`
	guardIfProved(t, "early_return.elisa", src)
}
