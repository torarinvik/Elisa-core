package easm

import (
	"os/exec"
	"testing"
)

// docs/104 rigor rung 3: symbolic equivalence proves reference ≡ target for ALL inputs via SMT,
// strictly stronger than the fuzz oracle's sampled agreement.

func requireZ3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; symbolic equivalence requires an SMT solver")
	}
}

// Two textually-different but semantically-identical bodies: reference computes a+b via mov+add;
// target computes b+a (addition commutes). The fuzz oracle would sample; SMT proves it for all inputs.
func TestSymbolicProvesCommutativeEquivalence(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def add_pair(a: u64, b: u64) -> u64 abi c:
    inputs: a = rdi, b = rsi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movq %rdi, %rax
        addq %rsi, %rax
        ret
    target x86_64 lockstep reference:
        movq %rsi, %rax
        addq %rdi, %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if containsIssue(issues, "lockstep-symbolic-skip") {
		t.Fatalf("expected the body to be modelable, got skip: %#v", issues)
	}
	if len(issues) != 0 {
		t.Fatalf("expected symbolic equivalence to PROVE a+b == b+a, got %#v", issues)
	}
}

// The real emulator AeroLib stub: xorq %rax,%rax on both sides. Proven equivalent (rax == 0 always).
func TestSymbolicProvesRealAeroLibStub(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module guest_exec_x86_64
target x86_64
export def ElisaGuestExec_AeroLibFallbackUnknownStubAsm() -> u64 abi c:
    outputs: ret = rax
    clobbers: cc
    stack: unchanged
    control: returns
    reference:
        xorq %rax, %rax
        ret
    target x86_64 lockstep reference:
        xorq %rax, %rax
        ret
`
	_, issues := Parse("guest_exec_x86_64.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected real stub to be proven equivalent, got %#v", issues)
	}
}

// A genuinely-divergent target (adds an extra increment) must be DISPROVED, not passed.
func TestSymbolicDisprovesDivergentTarget(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def add_pair(a: u64, b: u64) -> u64 abi c:
    inputs: a = rdi, b = rsi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movq %rdi, %rax
        addq %rsi, %rax
        ret
    target x86_64 lockstep reference:
        movq %rdi, %rax
        addq %rsi, %rax
        incq %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if !containsIssue(issues, "lockstep-symbolic-divergence") {
		t.Fatalf("expected lockstep-symbolic-divergence, got %#v", issues)
	}
}

// A body outside the modelable subset (memory operand) is skipped, never silently passed.
func TestSymbolicSkipsUnmodelableBody(t *testing.T) {
	requireZ3(t)
	t.Setenv(lockstepSymbolicEnv, "1")
	src := `module sym
target x86_64
export def load_it(p: HostPtr[u64]) -> u64 abi c:
    inputs: p = rdi
    outputs: ret = rax
    clobbers: rax, memory
    stack: unchanged
    control: returns
    requires: relocation.symbol
    reference:
        movq (%rdi), %rax
        ret
    target x86_64 lockstep reference:
        movq (%rdi), %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if !containsIssue(issues, "lockstep-symbolic-skip") {
		t.Fatalf("expected lockstep-symbolic-skip for a memory-operand body, got %#v", issues)
	}
}

// Off by default: with the env unset, no symbolic issues appear even on a divergent pair.
func TestSymbolicOffByDefault(t *testing.T) {
	src := `module sym
target x86_64
export def add_pair(a: u64, b: u64) -> u64 abi c:
    inputs: a = rdi, b = rsi
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    reference:
        movq %rdi, %rax
        addq %rsi, %rax
        ret
    target x86_64 lockstep reference:
        movq %rdi, %rax
        addq %rsi, %rax
        incq %rax
        ret
`
	_, issues := Parse("sym.easm", src)
	if containsIssue(issues, "lockstep-symbolic-divergence") || containsIssue(issues, "lockstep-symbolic-skip") {
		t.Fatalf("symbolic checks must be off by default, got %#v", issues)
	}
}
