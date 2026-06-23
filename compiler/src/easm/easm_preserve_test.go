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

// rsp-relative stack memory: r12 is saved to its pushed slot, then RELOADED from the same slot via a
// movq load before pop. The slot-precise stack model proves the round-trip -> preserved.
func TestPreserveAcceptsRspStackReload(t *testing.T) {
	requireZ3Preserve(t)
	t.Setenv(preserveParametricEnv, "1")
	src := `module pres
target x86_64
export def stk(a: u64) -> u64 abi c:
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
        movq (%rsp), %r12
        popq %r12
        ret
`
	_, issues := Parse("pres.easm", src)
	if containsIssue(issues, "callee-saved-value-unrestored") || containsIssue(issues, "preserve-parametric-skip") {
		t.Fatalf("honest rsp-stack reload must be preserved, got %#v", issues)
	}
}

// Direct-encoder test: a store of r12 to a stack slot followed by a load from the SAME offset returns
// the entry value; a load from a different offset (holding rdi) does not.
func TestPreserveRspStackSlotEncoding(t *testing.T) {
	good, reason := preserveEncodeBody([]Instruction{
		{Op: "subq", Text: "subq $16, %rsp"},
		{Op: "movq", Text: "movq %r12, 8(%rsp)"},
		{Op: "movq", Text: "movq 8(%rsp), %r12"},
		{Op: "addq", Text: "addq $16, %rsp"},
	})
	if reason != "" {
		t.Fatalf("expected rsp-stack body to be modelable, got %q", reason)
	}
	if good["r12"] != "in_r12" {
		t.Fatalf("r12 should round-trip to its entry symbol, got %q", good["r12"])
	}
	bad, reason2 := preserveEncodeBody([]Instruction{
		{Op: "subq", Text: "subq $16, %rsp"},
		{Op: "movq", Text: "movq %r12, 8(%rsp)"},
		{Op: "movq", Text: "movq %rdi, 0(%rsp)"},
		{Op: "movq", Text: "movq 0(%rsp), %r12"},
		{Op: "addq", Text: "addq $16, %rsp"},
	})
	if reason2 != "" {
		t.Fatalf("expected wrong-slot body to be modelable, got %q", reason2)
	}
	if bad["r12"] == "in_r12" {
		t.Fatalf("wrong-slot reload must NOT restore the entry value, got %q", bad["r12"])
	}
}

// A non-%rsp memory base stays outside the modeled subset (skip, never a false proof).
func TestPreserveNonRspMemorySkips(t *testing.T) {
	_, reason := preserveEncodeBody([]Instruction{
		{Op: "movq", Text: "movq %r12, 8(%rbp)"},
	})
	if reason == "" {
		t.Fatalf("expected non-%%rsp memory to be skipped, was modeled")
	}
}

// Direct-encoder: the newly-modeled ops (lea/shift/or/not/neg/xchg/movl) compute the right register
// terms. r12 is saved via push and restored via pop; the surrounding churn must not disturb it.
func TestPreserveNewOpcodesEncoding(t *testing.T) {
	regs, reason := preserveEncodeBody([]Instruction{
		{Op: "pushq", Text: "pushq %r12"},
		{Op: "leaq", Text: "leaq 8(%rdi), %rax"},
		{Op: "shlq", Text: "shlq $2, %rax"},
		{Op: "orq", Text: "orq %rdi, %rax"},
		{Op: "notq", Text: "notq %rax"},
		{Op: "negq", Text: "negq %rax"},
		{Op: "xchgq", Text: "xchgq %rax, %r12"},
		{Op: "xchgq", Text: "xchgq %rax, %r12"},
		{Op: "movl", Text: "movl $7, %eax"},
		{Op: "popq", Text: "popq %r12"},
	})
	if reason != "" {
		t.Fatalf("expected new-opcode churn to be modelable, got %q", reason)
	}
	if regs["r12"] != "in_r12" {
		t.Fatalf("r12 must be restored to entry across the churn, got %q", regs["r12"])
	}
	if regs["rax"] != "(concat (_ bv0 32) ((_ extract 31 0) (_ bv7 64)))" {
		t.Fatalf("movl $7,%%eax must zero-extend, got %q", regs["rax"])
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
