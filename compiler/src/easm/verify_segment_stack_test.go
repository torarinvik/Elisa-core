package easm

import (
	"testing"
)

func TestVerifyRejectsSegmentAccessWithoutCapability(t *testing.T) {
	src := `module tls
target x86_64
export def bad_tls() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq %fs:0x30, %rax
        ret
`
	_, issues := Parse("tls.easm", src)
	if !containsIssue(issues, "segment-access-intent-missing") {
		t.Fatalf("expected segment-access-intent-missing, got %#v", issues)
	}
}

func TestVerifyRejectsSegmentRegisterWriteWithoutIntent(t *testing.T) {
	src := `module tls
target x86_64
export def bad_fs_write(selector: u16) -> void abi c:
    inputs: selector = rdi
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs
    body:
        movw %di, %fs
        ret
`
	_, issues := Parse("tls_write.easm", src)
	if !containsIssue(issues, "segment-register-write-intent-missing") {
		t.Fatalf("expected segment-register-write-intent-missing, got %#v", issues)
	}
}

func TestVerifyRejectsWrongSpecificSegmentCapability(t *testing.T) {
	src := `module tls
target x86_64
export def bad_gs_with_fs_cap() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs
    body:
        movq %gs:0x30, %rax
        ret
`
	_, issues := Parse("wrong_segment.easm", src)
	if !containsIssue(issues, "segment-access-intent-missing") {
		t.Fatalf("expected segment-access-intent-missing for gs with fs-only capability, got %#v", issues)
	}
}

func TestVerifyRejectsSegmentRegisterWriteWithoutMemoryClobber(t *testing.T) {
	src := `module tls
target x86_64
export def bad_fs_write(selector: u16) -> void abi c:
    inputs: selector = rdi
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs, x86_64.segment.write
    body:
        movw %di, %fs
        ret
`
	_, issues := Parse("tls_write_no_memory.easm", src)
	if !containsIssue(issues, "segment-register-write-without-memory-clobber") {
		t.Fatalf("expected segment-register-write-without-memory-clobber, got %#v", issues)
	}
}

func TestVerifyRejectsReturningSegmentRegisterWriteWithoutLifetimeContract(t *testing.T) {
	src := `module tls
target x86_64
export def bad_fs_write(selector: u16) -> void abi c:
    inputs: selector = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs, x86_64.segment.write
    body:
        movw %di, %fs
        ret
`
	_, issues := Parse("tls_write_lifetime.easm", src)
	if !containsIssue(issues, "segment-register-return-without-lifetime-contract") {
		t.Fatalf("expected segment-register-return-without-lifetime-contract, got %#v", issues)
	}
}

func TestVerifyAcceptsPersistentSegmentRegisterWriteContract(t *testing.T) {
	src := `module tls
target x86_64
export def load_fs(selector: GuestFsSelector) -> void abi c:
    inputs: selector = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent
    body:
        movw %di, %fs
        state fs: guest
        ret
`
	_, issues := Parse("tls_write_persistent.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected persistent segment write contract to verify, got %#v", issues)
	}
}

func TestVerifyRejectsStackTopHandoffWithoutOwnershipContract(t *testing.T) {
	src := `module stack_owner
target x86_64
export def jump_on_stack(entry: uintptr, stack_top: uintptr) -> void abi c:
    inputs: entry = rdi, stack_top = rsi
    clobbers: r11, rsp, memory
    stack: switches, aligned 16, noreturn
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        movq %rdi, %r11
        movq %rsi, %rsp
        jmp *%r11
`
	_, issues := Parse("stack_owner.easm", src)
	if !containsIssue(issues, "stack-switch-without-ownership-contract") {
		t.Fatalf("expected stack-switch-without-ownership-contract, got %#v", issues)
	}
}

func TestVerifyAcceptsStackTopHandoffWithOwnershipContract(t *testing.T) {
	src := `module stack_owner
target x86_64
export def jump_on_stack(entry: GuestEntryPoint, stack_top: GuestStackTop) -> void abi c:
    inputs: entry = rdi, stack_top = rsi
    clobbers: r11, rsp, memory
    stack: switches, owns, aligned 16, noreturn
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        movq %rdi, %r11
        movq %rsi, %rsp
        jmp *%r11
`
	_, issues := Parse("stack_owner_ok.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected owned stack handoff to verify, got %#v", issues)
	}
}

func TestVerifyRejectsTailJumpFramePointerClobberWithoutEscape(t *testing.T) {
	src := `module frame_handoff
target x86_64
export def jump_on_stack(entry: uintptr, stack_top: uintptr) -> void abi c:
    inputs: entry = rdi, stack_top = rsi
    clobbers: r11, rsp, rbp, memory
    preserves: callee_saved
    stack: switches, owns, aligned 16, noreturn
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        movq %rdi, %r11
        movq %rsi, %rsp
        movq %rsi, %rbp
        jmp *%r11
`
	_, issues := Parse("frame_handoff.easm", src)
	if !containsIssue(issues, "tail-jump-frame-pointer-clobber") {
		t.Fatalf("expected tail-jump-frame-pointer-clobber, got %#v", issues)
	}
}

func TestVerifyAcceptsTailJumpFramePointerHandoffEscape(t *testing.T) {
	src := `module frame_handoff
target x86_64
export def jump_on_stack(entry: GuestEntryPoint, stack_top: GuestStackTop) -> void abi c:
    inputs: entry = rdi, stack_top = rsi
    clobbers: r11, rsp, rbp, memory
    preserves: callee_saved
    stack: switches, owns, aligned 16, noreturn
    control: noreturn, tail_jumps
    requires: control.indirect, frame_pointer.handoff.unchecked
    body:
        movq %rdi, %r11
        movq %rsi, %rsp
        movq %rsi, %rbp
        jmp *%r11
`
	_, issues := Parse("frame_handoff_ok.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected explicit frame handoff escape to verify, got %#v", issues)
	}
}

func TestVerifyRejectsLargeStackAdjustWithoutProbe(t *testing.T) {
	src := `module stack_probe
target x86_64
export def bad_large_stack() -> void abi c:
    clobbers: rsp, cc, memory
    stack: unchanged
    control: returns
    body:
        subq $8192, %rsp
        addq $8192, %rsp
        ret
`
	_, issues := Parse("stack_probe.easm", src)
	if !containsIssue(issues, "large-stack-adjust-without-probe") {
		t.Fatalf("expected large-stack-adjust-without-probe, got %#v", issues)
	}
}

func TestVerifyRejectsAArch64ReservedRegisterWithoutCapability(t *testing.T) {
	src := `module reserved
target aarch64
export def bad_x18() -> void abi c:
    clobbers: x18
    stack: unchanged
    control: returns
    body:
        mov x0, x18
        ret
`
	_, issues := Parse("reserved.easm", src)
	if !containsIssue(issues, "reserved-register-use") {
		t.Fatalf("expected reserved-register-use, got %#v", issues)
	}
}

func TestVerifyAcceptsExplicitRelocationSegmentAndProbeIntent(t *testing.T) {
	src := `module explicit
target x86_64
export def explicit_contract(ptr: uintptr) -> void abi c:
    inputs: ptr = rdi
    clobbers: rax, rsp, cc, memory
    stack: unchanged, probed
    control: returns
    requires: operand_size.inferred, immediate.truncation, relocation.symbol, x86_64.segment.fs, memory.base.untyped
    body:
        mov $1, 0(%rdi)
        movb $0x1ff, %al
        lea global_counter(%rip), %rax
        movq %fs:0x30, %rax
        subq $8192, %rsp
        addq $8192, %rsp
        ret
`
	_, issues := Parse("explicit.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected explicit low-level intent to verify, got %#v", issues)
	}
}

func TestVerifyRejectsDuplicateExports(t *testing.T) {
	src := `module dup
target x86_64
export def same() -> void abi c:
    stack: unchanged
    control: returns
    body:
        ret

export def same() -> void abi c:
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("dup.easm", src)
	if !containsIssue(issues, "duplicate-export") {
		t.Fatalf("expected duplicate-export, got %#v", issues)
	}
}

func TestVerifyRejectsDuplicateContractAtoms(t *testing.T) {
	src := `module dup_contract
target x86_64
export def duplicate_contract() -> void abi c:
    clobbers: rax, rax
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("dup_contract.easm", src)
	if !containsIssue(issues, "duplicate-contract-atom") {
		t.Fatalf("expected duplicate-contract-atom, got %#v", issues)
	}
}

func TestVerifyRejectsPreserveWithoutClobber(t *testing.T) {
	src := `module preserve
target x86_64
export def preserve_without_clobber() -> void abi c:
    clobbers: memory
    preserves: r12
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("preserve.easm", src)
	if !containsIssue(issues, "preserve-without-clobber") {
		t.Fatalf("expected preserve-without-clobber, got %#v", issues)
	}
}

func TestVerifyRejectsNonVoidReturningFunctionMissingRet(t *testing.T) {
	src := `module ret
target x86_64
export def bad_ret() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq $1, %rax
`
	_, issues := Parse("ret.easm", src)
	if !containsIssue(issues, "returns-missing-ret") {
		t.Fatalf("expected returns-missing-ret, got %#v", issues)
	}
}

func TestVerifyDoesNotTreatSubstringSPAsStackRegister(t *testing.T) {
	src := `module names
target x86_64
export def dispatch_symbol() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: returns
    requires: relocation.symbol
    body:
        movq dispatch_table(%rip), %rax
        ret
`
	_, issues := Parse("names.easm", src)
	if containsIssue(issues, "stack-effect-undeclared") {
		t.Fatalf("did not expect stack-effect-undeclared from dispatch substring, got %#v", issues)
	}
}

func TestVerifyAcceptsNegativeImmediateWithinExplicitWidth(t *testing.T) {
	src := `module imm
target x86_64
export def good_negative_imm() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movb $-1, %al
        ret
`
	_, issues := Parse("negative_imm.easm", src)
	if containsIssue(issues, "immediate-truncation") {
		t.Fatalf("did not expect immediate-truncation for -1 byte immediate, got %#v", issues)
	}
}

func TestVerifyAcceptsEAXWriteForWideReturn(t *testing.T) {
	src := `module partial
target x86_64
export def good_zero_extended_return() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    body:
        xor %eax, %eax
        ret
`
	_, issues := Parse("eax_return.easm", src)
	if containsIssue(issues, "partial-register-return") {
		t.Fatalf("did not expect partial-register-return for eax zero-extension, got %#v", issues)
	}
}

func TestVerifyReservedRegisterUsesExactTokens(t *testing.T) {
	src := `module reserved
target aarch64
export def not_x18() -> void abi c:
    clobbers: x180
    stack: unchanged
    control: returns
    body:
        mov x0, x180
        ret
`
	_, issues := Parse("reserved_token.easm", src)
	if containsIssue(issues, "reserved-register-use") {
		t.Fatalf("did not expect reserved-register-use for x180 token, got %#v", issues)
	}
}

func TestParseRejectsDuplicateParameters(t *testing.T) {
	src := `module params
target x86_64
export def bad_params(a: i64, a: i64) -> void abi c:
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("params.easm", src)
	if !containsIssue(issues, "duplicate-param") {
		t.Fatalf("expected duplicate-param, got %#v", issues)
	}
}

func TestVerifyRejectsMissingContractsAndBody(t *testing.T) {
	src := `module contracts
target x86_64
export def no_contract() -> void abi c:
`
	_, issues := Parse("contracts.easm", src)
	for _, code := range []string{"missing-body", "missing-stack-contract", "missing-control-contract"} {
		if !containsIssue(issues, code) {
			t.Fatalf("expected %s, got %#v", code, issues)
		}
	}
}

func TestVerifyRejectsConflictingControlContract(t *testing.T) {
	src := `module control
target x86_64
export def bad_control() -> void abi c:
    stack: unchanged
    control: returns, noreturn
    body:
        ret
`
	_, issues := Parse("control.easm", src)
	if !containsIssue(issues, "conflicting-control-contract") {
		t.Fatalf("expected conflicting-control-contract, got %#v", issues)
	}
}

func TestVerifyRejectsMissingReturnOutput(t *testing.T) {
	src := `module output
target x86_64
export def bad_output() -> u64 abi c:
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq $1, %rax
        ret
`
	_, issues := Parse("output.easm", src)
	if !containsIssue(issues, "missing-return-output") {
		t.Fatalf("expected missing-return-output, got %#v", issues)
	}
}

func TestVerifyRejectsVoidReturnOutput(t *testing.T) {
	src := `module output
target x86_64
export def bad_void_output() -> void abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("void_output.easm", src)
	if !containsIssue(issues, "void-return-output") {
		t.Fatalf("expected void-return-output, got %#v", issues)
	}
}

func TestVerifyRejectsNonTerminalReturn(t *testing.T) {
	src := `module terminal
target x86_64
export def bad_terminal() -> void abi c:
    stack: unchanged
    control: returns
    body:
        ret
        movq %rax, %rax
`
	_, issues := Parse("terminal.easm", src)
	if !containsIssue(issues, "return-not-terminal") {
		t.Fatalf("expected return-not-terminal, got %#v", issues)
	}
}

func TestVerifyRejectsNonTerminalNoreturn(t *testing.T) {
	src := `module terminal
target x86_64
export def bad_noreturn(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: memory
    stack: unchanged
    control: noreturn
    body:
        jmp *%rdi
        movq %rax, %rax
`
	_, issues := Parse("noreturn_terminal.easm", src)
	if !containsIssue(issues, "noreturn-missing-terminal") {
		t.Fatalf("expected noreturn-missing-terminal, got %#v", issues)
	}
}

func TestVerifyRejectsHugeUnsignedImmediateTruncation(t *testing.T) {
	src := `module imm
target x86_64
export def bad_huge_imm() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movb $0xffffffffffffffff, %al
        ret
`
	_, issues := Parse("huge_imm.easm", src)
	if !containsIssue(issues, "immediate-truncation") {
		t.Fatalf("expected immediate-truncation, got %#v", issues)
	}
}

func TestVerifyRejectsUnknownABIAndContracts(t *testing.T) {
	src := `module contracts
target x86_64
export def bad_contract() -> void abi mystery:
    stack: unchanged, floating
    control: returns, teleports
    body:
        ret
`
	_, issues := Parse("bad_contract.easm", src)
	for _, code := range []string{"unknown-abi", "unknown-stack-contract", "unknown-control-contract"} {
		if !containsIssue(issues, code) {
			t.Fatalf("expected %s, got %#v", code, issues)
		}
	}
}
