package easm

import (
	"strings"
	"testing"
)

func TestLockstepOracleMatchingReferenceAndTargetPasses(t *testing.T) {
	requireLockstepOracleTools(t)
	t.Setenv(lockstepOracleEnv, "1")
	src := `module ls_oracle
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
        ret
`
	_, issues := Parse("ls_oracle.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected matching lockstep bodies to pass, got %#v", issues)
	}
}

func TestLockstepOracleDivergentTargetReportsDivergence(t *testing.T) {
	requireLockstepOracleTools(t)
	t.Setenv(lockstepOracleEnv, "1")
	src := `module ls_oracle
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
        subq %rsi, %rax
        ret
`
	_, issues := Parse("ls_oracle.easm", src)
	if !containsIssue(issues, "lockstep-divergence") {
		t.Fatalf("expected lockstep-divergence, got %#v", issues)
	}
}

func TestLockstepOracleSkipsNonLeafCallGate(t *testing.T) {
	t.Setenv(lockstepOracleEnv, "1")
	src := `module ls_oracle
target x86_64
export def call_leaf(target: HostCallable) -> void abi c:
    inputs: target = rdi
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, rsp, cc, memory
    stack: unchanged, aligned 16
    control: returns
    requires: control.indirect
    reference:
        subq $8, %rsp
        call *%rdi
        addq $8, %rsp
        ret
    target x86_64 lockstep reference:
        subq $8, %rsp
        call *%rdi
        addq $8, %rsp
        ret
`
	_, issues := Parse("ls_oracle.easm", src)
	if containsIssue(issues, "lockstep-divergence") {
		t.Fatalf("non-leaf routine must be skipped, not run: %#v", issues)
	}
	if !containsIssue(issues, "lockstep-oracle-skip") {
		t.Fatalf("expected explicit lockstep-oracle-skip, got %#v", issues)
	}
}

func TestLockstepOracleToolchainAbsentPathSkipsCleanly(t *testing.T) {
	t.Setenv(lockstepOracleEnv, "1")
	t.Setenv("ELISA_EASM_LOCKSTEP_ORACLE_LLVM_MC", "")
	t.Setenv("ELISA_EASM_LOCKSTEP_ORACLE_CLANG", "")
	t.Setenv("ELISA_EASM_LOCKSTEP_ORACLE_CLANG__", "")
	src := `module ls_oracle
target x86_64
export def ret_a(a: u64) -> u64 abi c:
    inputs: a = rdi
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    reference:
        movq %rdi, %rax
        ret
    target x86_64 lockstep reference:
        movq %rdi, %rax
        ret
`
	_, issues := Parse("ls_oracle.easm", src)
	if !containsIssue(issues, "lockstep-oracle-skip") {
		t.Fatalf("expected toolchain skip, got %#v", issues)
	}
	for _, issue := range issues {
		if issue.Code == "lockstep-oracle-skip" && strings.Contains(issue.Message, "not available") {
			return
		}
	}
	t.Fatalf("expected unavailable-toolchain skip reason, got %#v", issues)
}

func requireLockstepOracleTools(t *testing.T) {
	t.Helper()
	if findToolForLockstepOracle("llvm-mc") == "" || (findToolForLockstepOracle("clang") == "" && findToolForLockstepOracle("clang++") == "") {
		t.Skip("llvm-mc and clang are required for lockstep oracle execution")
	}
}

// TestLockstepOracleRealAeroLibStub exercises the oracle against the EXACT lockstep routine shipped
// in the emulator (easm/guest_exec_x86_64.easm: ElisaGuestExec_AeroLibFallbackUnknownStubAsm). This
// is the "turn it on for real" check (docs/103 stage 3c): production assembly, proven observationally
// equivalent, not just a synthetic fixture. The body is inlined (not read cross-repo) so the test is
// self-contained; keep it byte-identical to the emulator routine.
func TestLockstepOracleRealAeroLibStub(t *testing.T) {
	requireLockstepOracleTools(t)
	t.Setenv(lockstepOracleEnv, "1")
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
	// The oracle must have RUN (no skip) and found the bodies equivalent (no divergence).
	if containsIssue(issues, "lockstep-oracle-skip") {
		t.Fatalf("oracle skipped the real stub instead of proving it; issues=%#v", issues)
	}
	if containsIssue(issues, "lockstep-divergence") {
		t.Fatalf("real AeroLib stub reported divergence; issues=%#v", issues)
	}
	if len(issues) != 0 {
		t.Fatalf("expected the real lockstep stub to verify clean, got %#v", issues)
	}
}
