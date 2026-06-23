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
