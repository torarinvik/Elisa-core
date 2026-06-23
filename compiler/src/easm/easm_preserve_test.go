package easm

import (
	"os/exec"
	"testing"
)

// docs/104 upgrade-ladder item 3: callee-saved preservation as parametricity. The structural check
// verifies push/pop ordering; this proves the restored VALUE equals the entry value, catching
// save-but-restore-wrong-value bugs a presence checklist misses.

func requireZ3Preserve(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH")
	}
}

// Correct save/restore: push r12 ... pop r12. Value provably preserved -> no issue.
func TestPreserveAcceptsHonestSaveRestore(t *testing.T) {
	requireZ3Preserve(t)
	t.Setenv(preserveParametricEnv, "1")
	src := `module pres
target x86_64
export def honest(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax, r12, cc, memory
    preserves: r12, callee_saved
    stack: synthetic, aligned 16
    control: returns
    body:
        pushq %r12
        movq %rdi, %r12
        addq %r12, %r12
        movq %r12, %rax
        popq %r12
        ret
`
	_, issues := Parse("pres.easm", src)
	if containsIssue(issues, "callee-saved-value-unrestored") {
		t.Fatalf("honest save/restore must be proven preserved, got %#v", issues)
	}
}

// Interleaved stack misalignment: push r12; push rax; pop r12 restores rax's value into r12, NOT the
// entry value. The structural ordering check passes this; parametricity must catch it.
func TestPreserveCatchesInterleavedStackMisalignment(t *testing.T) {
	requireZ3Preserve(t)
	t.Setenv(preserveParametricEnv, "1")
	src := `module pres
target x86_64
export def sneaky(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax, rdi, r12, cc, memory
    preserves: r12, callee_saved
    stack: synthetic, aligned 16
    control: returns
    body:
        pushq %r12
        pushq %rdi
        popq %r12
        popq %rdi
        movq %r12, %rax
        ret
`
	// Balanced stack, but pop order is swapped: r12 <- saved rdi, rdi <- saved r12. r12 ends holding
	// the wrong value. Structural ordering (push r12 ... pop r12) passes; parametricity catches it.
	_, issues := Parse("pres.easm", src)
	if !containsIssue(issues, "callee-saved-value-unrestored") {
		t.Fatalf("expected callee-saved-value-unrestored for interleaved stack, got %#v", issues)
	}
}

// Wrong-register restore: save r12, restore into r12 from a slot that held r13's saved value.
func TestPreserveCatchesWrongSlotRestore(t *testing.T) {
	requireZ3Preserve(t)
	t.Setenv(preserveParametricEnv, "1")
	src := `module pres
target x86_64
export def swap_save(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax, r12, r13, cc, memory
    preserves: r12, r13, callee_saved
    stack: synthetic, aligned 16
    control: returns
    body:
        pushq %r12
        pushq %r13
        movq %rdi, %rax
        popq %r12
        popq %r13
        ret
`
	_, issues := Parse("pres.easm", src)
	// r12 receives r13's saved value and r13 receives r12's: both unrestored.
	if !containsIssue(issues, "callee-saved-value-unrestored") {
		t.Fatalf("expected callee-saved-value-unrestored for swapped restore, got %#v", issues)
	}
}

// Off by default: env unset -> no parametric issues even on the sneaky body.
func TestPreserveOffByDefault(t *testing.T) {
	src := `module pres
target x86_64
export def sneaky(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax, r12, cc, memory
    preserves: r12, callee_saved
    stack: synthetic, aligned 16
    control: returns
    body:
        pushq %r12
        movq %rdi, %rax
        pushq %rax
        popq %r12
        popq %r12
        ret
`
	_, issues := Parse("pres.easm", src)
	if containsIssue(issues, "callee-saved-value-unrestored") || containsIssue(issues, "preserve-parametric-skip") {
		t.Fatalf("parametric preservation must be off by default, got %#v", issues)
	}
}
