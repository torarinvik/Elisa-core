package easm

import "testing"

// docs/104 increment 2 (dataflow joins): at a control-flow merge (a label that is a jump target), a
// register read after the label must be established on EVERY incoming edge. The linear walk only
// carries the textual-predecessor state, so checkMergeConsistency verifies the join: a register the
// walk treats as live after the merge but which is undefined on a jump-in edge is unsound.

// rcx is set only on the fall-through path, then read after the merge -- on the je-skip edge it is
// undefined. The meet excludes rcx; reading it after `skip` is flagged.
func TestMergeRejectsRegisterUndefinedOnJumpEdge(t *testing.T) {
	src := `module merge
target x86_64
export def merge_bug(flag: u64, p: HostPtr[u32]) -> u64 abi c:
    inputs: flag = rdi, p = rsi
    outputs: ret = rax
    clobbers: rax, rcx, cc, memory
    stack: unchanged
    control: returns
    requires: compare.unsigned, control.direct
    body:
        cmpq $0, %rdi
        je skip
        movq (%rsi), %rcx
    skip:
        movq %rcx, %rax
        ret
`
	_, issues := Parse("merge.easm", src)
	if !containsIssue(issues, "merge-state-unsound") {
		t.Fatalf("expected merge-state-unsound, got %#v", issues)
	}
}

// rcx is written on BOTH paths before the merge, so it is established on every edge: no merge error.
func TestMergeAcceptsRegisterDefinedOnAllEdges(t *testing.T) {
	src := `module merge
target x86_64
export def merge_ok(flag: u64, p: HostPtr[u32]) -> u64 abi c:
    inputs: flag = rdi, p = rsi
    outputs: ret = rax
    clobbers: rax, rcx, cc, memory
    stack: unchanged
    control: returns
    requires: compare.unsigned, control.direct
    body:
        movq %rsi, %rcx
        cmpq $0, %rdi
        je skip
        movq (%rsi), %rcx
    skip:
        movq %rcx, %rax
        ret
`
	_, issues := Parse("merge.easm", src)
	if containsIssue(issues, "merge-state-unsound") {
		t.Fatalf("sound merge should not be flagged, got %#v", issues)
	}
}

// A register the walk carries past the merge but never reads is harmless: no false positive.
func TestMergeIgnoresCarriedButUnreadRegister(t *testing.T) {
	src := `module merge
target x86_64
export def merge_dead(flag: u64, p: HostPtr[u32]) -> u64 abi c:
    inputs: flag = rdi, p = rsi
    outputs: ret = rax
    clobbers: rax, rcx, cc, memory
    stack: unchanged
    control: returns
    requires: compare.unsigned, control.direct
    body:
        cmpq $0, %rdi
        je skip
        movq (%rsi), %rcx
    skip:
        movq %rdi, %rax
        ret
`
	_, issues := Parse("merge.easm", src)
	if containsIssue(issues, "merge-state-unsound") {
		t.Fatalf("carried-but-unread register should not be flagged, got %#v", issues)
	}
}

// A merge label WITH a declared contract is exempt: the contract types the merge and is enforced
// per-edge, so the demand-driven meet check stands down (the existing label-precondition machinery owns it).
func TestMergeContractLabelExempt(t *testing.T) {
	src := `module merge
target x86_64
export def merge_contract(flag: u64, p: HostPtr[u32]) -> u64 abi c:
    inputs: flag = rdi, p = rsi
    outputs: ret = rax
    labels:
        skip: rcx
    clobbers: rax, rcx, cc, memory
    stack: unchanged
    control: returns
    requires: compare.unsigned, control.direct
    body:
        cmpq $0, %rdi
        je skip
        movq (%rsi), %rcx
    skip:
        movq %rcx, %rax
        ret
`
	_, issues := Parse("merge.easm", src)
	if containsIssue(issues, "merge-state-unsound") {
		t.Fatalf("contract label must be exempt from the meet check, got %#v", issues)
	}
}

func TestMergeRejectsDivergentStackAlignmentForCall(t *testing.T) {
	src := `module merge
target x86_64
export def merge_stack_bad(flag: u64, target: HostCallable) -> void abi c:
    inputs: flag = rdi, target = rsi
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, cc, memory
    stack: unchanged, aligned 16
    control: returns
    requires: compare.unsigned, control.indirect
    body:
        cmpq $0, %rdi
        je callsite
        subq $8, %rsp
    callsite:
        call *%rsi
        addq $8, %rsp
        ret
`
	_, issues := Parse("merge_stack.easm", src)
	if !containsIssue(issues, "merge-stack-alignment-unsound") {
		t.Fatalf("expected merge-stack-alignment-unsound, got %#v", issues)
	}
}

func TestMergeAcceptsMatchingStackAlignmentForCall(t *testing.T) {
	src := `module merge
target x86_64
export def merge_stack_ok(flag: u64, target: HostCallable) -> void abi c:
    inputs: flag = rdi, target = rsi
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, cc, memory
    stack: unchanged, aligned 16
    control: returns
    requires: compare.unsigned, control.indirect
    body:
        subq $8, %rsp
        cmpq $0, %rdi
        je callsite
    callsite:
        call *%rsi
        addq $8, %rsp
        ret
`
	_, issues := Parse("merge_stack_ok.easm", src)
	if containsIssue(issues, "merge-stack-alignment-unsound") {
		t.Fatalf("matching stack alignment should not be flagged, got %#v", issues)
	}
}

func TestMergeRejectsDivergentKnownIndirectTarget(t *testing.T) {
	src := `module merge
target x86_64
export def merge_known_bad(flag: u64) -> void abi c:
    inputs: flag = rdi
    clobbers: rax, cc
    stack: noreturn
    control: noreturn, tail_jumps
    requires: compare.unsigned, control.indirect
    body:
        cmpq $0, %rdi
        je poison
        movq $0x10000, %rax
    target:
        jmp *%rax
    poison:
        movq $0xffff800000000000, %rax
        jmp target
`
	_, issues := Parse("merge_known.easm", src)
	if !containsIssue(issues, "merge-known-value-unsound") {
		t.Fatalf("expected merge-known-value-unsound, got %#v", issues)
	}
}

func TestMergeAcceptsMatchingKnownIndirectTarget(t *testing.T) {
	src := `module merge
target x86_64
export def merge_known_ok(flag: u64) -> void abi c:
    inputs: flag = rdi
    clobbers: rax, cc
    stack: noreturn
    control: noreturn, tail_jumps
    requires: compare.unsigned, control.indirect
    body:
        cmpq $0, %rdi
        je same
        movq $0x10000, %rax
    target:
        jmp *%rax
    same:
        movq $0x10000, %rax
        jmp target
`
	_, issues := Parse("merge_known_ok.easm", src)
	if containsIssue(issues, "merge-known-value-unsound") {
		t.Fatalf("matching known indirect target should not be flagged, got %#v", issues)
	}
}
