package easm

import (
	"os"
	"testing"
)

func TestParseAndVerifyAcceptsSimpleReturningVoidFunction(t *testing.T) {
	src := `module smoke
target any
export def easm_pause() -> void abi c:
    clobbers: cc, memory
    stack: unchanged
    control: returns
    requires: x86_64.sse.pause
    body:
        pause
        ret
`
	module, issues := Parse("smoke.easm", src)
	if module == nil || module.Name != "smoke" || len(module.Functions) != 1 {
		t.Fatalf("unexpected module: %#v", module)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
}

func TestVerifyRejectsGuestEntryCallOnSyntheticStack(t *testing.T) {
	src := `module guest
target x86_64
export def guest_entry(params: uintptr) -> void abi ps4_sysv:
    inputs: params = rdi
    clobbers: rax, rdi, memory
    stack: synthetic
    control: noreturn
    body:
        pushq 0(%rdi)
        call *%rax
`
	_, issues := Parse("guest.easm", src)
	if !containsIssue(issues, "guest-entry-call-mangles-stack") {
		t.Fatalf("expected guest-entry-call-mangles-stack, got %#v", issues)
	}
}

func TestVerifyRequiresInstructionCapabilities(t *testing.T) {
	src := `module clock
target any
export def ticks() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, rdx
    stack: unchanged
    control: returns
    body:
        rdtsc
        ret
`
	_, issues := Parse("clock.easm", src)
	if !containsIssue(issues, "missing-capability") {
		t.Fatalf("expected missing-capability, got %#v", issues)
	}
}

func TestVerifyAcceptsShadPS4GuestEntryTailJumpTrampoline(t *testing.T) {
	src := `module shadps4_guest
target x86_64
export def shadps4_guest_entry(params: uintptr, exit_func: uintptr) -> void abi ps4_sysv:
    inputs: params = rdi, exit_func = rsi
    clobbers: rax, r9, r10, r11, rsi, rdi, cc, memory
    stack: synthetic, aligned 16, noreturn
    control: noreturn, tail_jumps
    body:
        movq 272(%rdi), %r11
        movq %rdi, %r10
        movq %rsi, %r9
        andq $-16, %rsp
        subq $8, %rsp
        pushq 8(%r10)
        pushq 0(%r10)
        movq %r10, %rdi
        movq %r9, %rsi
        jmp *%r11
`
	_, issues := Parse("guest_entry.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected shadPS4 guest-entry tail jump to verify, got %#v", issues)
	}
}

func TestVerifyAcceptsShadPS4RDTSCEquivalent(t *testing.T) {
	src := `module shadps4_clock
target x86_64
export def shadps4_fenced_rdtsc() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, rdx, memory
    stack: unchanged
    control: returns
    requires: x86_64.rdtsc
    body:
        lfence
        rdtsc
        lfence
        ret
`
	_, issues := Parse("rdtsc.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected RDTSC helper to verify, got %#v", issues)
	}
}

func TestVerifyAcceptsShadPS4StackSwitchEquivalent(t *testing.T) {
	src := `module shadps4_stack
target x86_64
export def run_on_another_stack(arg: uintptr, entry: uintptr, stack: uintptr) -> void abi ps4_sysv:
    inputs: arg = rdi, entry = rsi, stack = rdx
    clobbers: r12, r13, rsp, rbp, cc, memory
    preserves: r12, r13, callee_saved
    stack: switches
    control: returns
    body:
        pushq %r12
        pushq %r13
        movq %rsp, %r12
        movq %rbp, %r13
        movq %rdx, %rsp
        movq %rdx, %rbp
        callq *%rsi
        movq %r13, %rbp
        movq %r12, %rsp
        popq %r13
        popq %r12
        ret
`
	_, issues := Parse("stack_switch.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected stack switch helper to verify, got %#v", issues)
	}
}

func TestVerifyAcceptsShadPS4FiberContextInstructionSubset(t *testing.T) {
	src := `module shadps4_fiber
target x86_64
export def sce_fiber_setjmp(ctx: uintptr) -> i32 abi ps4_sysv:
    inputs: ctx = rdi
    outputs: ret = rax
    clobbers: rax, rdx, cc, memory
    stack: unchanged
    control: returns
    requires: x86_64.fpu_control
    body:
        movq %rax, 0(%rdi)
        movq (%rsp), %rdx
        movq %rdx, 16(%rdi)
        fnstcw 112(%rdi)
        stmxcsr 114(%rdi)
        xor %eax, %eax
        ret

export def sce_fiber_switch_entry(data: uintptr, set_fpu: i32) -> void abi ps4_sysv:
    inputs: data = rdi, set_fpu = rsi
    clobbers: rax, rbx, rcx, rdx, r8, r9, r10, r11, r12, r13, r14, r15, rsp, rbp, cc, memory
    preserves: callee_saved
    stack: switches
    control: noreturn, may_fault
    requires: x86_64.fpu_control, x86_64.simd_state, debug.trap
    body:
        movq %rdi, %r11
        movq 24(%r11), %rsp
        ldmxcsr 44(%r11)
        fldcw 40(%r11)
        xorq %r8, %r8
        emms
        vzeroall
        call *%r11
        trap
`
	_, issues := Parse("fiber.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected fiber context subset to verify, got %#v", issues)
	}
}

func TestVerifyRejectsUnsafeShadPS4GuestEntryCallVariant(t *testing.T) {
	src := `module guest
target x86_64
export def guest_entry(params: uintptr, exit_func: uintptr) -> void abi ps4_sysv:
    inputs: params = rdi, exit_func = rsi
    clobbers: rax, r9, r10, r11, rsi, rdi, cc, memory
    stack: synthetic, aligned 16
    control: noreturn
    body:
        andq $-16, %rsp
        subq $8, %rsp
        pushq 8(%rdi)
        pushq 0(%rdi)
        call *%rax
`
	_, issues := Parse("unsafe_guest_call.easm", src)
	if !containsIssue(issues, "guest-entry-call-mangles-stack") {
		t.Fatalf("expected guest-entry-call-mangles-stack, got %#v", issues)
	}
	if !containsIssue(issues, "noreturn-missing-terminal") {
		t.Fatalf("expected noreturn-missing-terminal, got %#v", issues)
	}
}

func TestVerifyRejectsUnsafeStackMutationWithoutMemoryClobber(t *testing.T) {
	src := `module stack
target x86_64
export def bad_stack() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        pushq %rax
        ret
`
	_, issues := Parse("bad_stack.easm", src)
	if !containsIssue(issues, "stack-without-memory-clobber") {
		t.Fatalf("expected stack-without-memory-clobber, got %#v", issues)
	}
	if !containsIssue(issues, "returning-stack-leak") {
		t.Fatalf("expected returning-stack-leak, got %#v", issues)
	}
}

func TestVerifyRejectsUnsafeUnsupportedSystemInstruction(t *testing.T) {
	src := `module sys
target x86_64
export def raw_syscall() -> i64 abi c:
    outputs: ret = rax
    clobbers: rax, rcx, r11, memory
    stack: unchanged
    control: returns
    body:
        syscall
        ret
`
	_, issues := Parse("syscall.easm", src)
	if !containsIssue(issues, "unsupported-instruction") {
		t.Fatalf("expected unsupported-instruction for raw syscall, got %#v", issues)
	}
}

func TestVerifyRejectsUnsafeCalleeSavedClobberWithoutPreserve(t *testing.T) {
	src := `module callee_saved
target x86_64
export def bad_callee_saved() -> void abi c:
    clobbers: r12, memory
    stack: unchanged
    control: returns
    body:
        movq %rax, %r12
        ret
`
	_, issues := Parse("callee_saved.easm", src)
	if !containsIssue(issues, "callee-saved-not-preserved") {
		t.Fatalf("expected callee-saved-not-preserved, got %#v", issues)
	}
}

func TestVerifyRejectsUnsafeTargetCapabilityMismatch(t *testing.T) {
	src := `module mixed_target
target x86_64
export def bad_counter() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, cc, memory
    stack: unchanged
    control: returns
    requires: aarch64.cntvct
    body:
        mrs %rax, cntvct_el0
        ret
`
	_, issues := Parse("mixed_target.easm", src)
	if !containsIssue(issues, "target-capability-mismatch") {
		t.Fatalf("expected target-capability-mismatch, got %#v", issues)
	}
}

func TestVerifyRejectsUnsafeReturningUnqualifiedJump(t *testing.T) {
	src := `module jump
target x86_64
export def bad_jump(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: cc, memory
    stack: unchanged
    control: returns
    body:
        jmp *%rdi
`
	_, issues := Parse("bad_jump.easm", src)
	if !containsIssue(issues, "returning-unqualified-jump") {
		t.Fatalf("expected returning-unqualified-jump, got %#v", issues)
	}
	if !containsIssue(issues, "returns-missing-ret") {
		t.Fatalf("expected returns-missing-ret, got %#v", issues)
	}
}

func TestVerifyRejectsMemoryWriteWithoutMemoryClobber(t *testing.T) {
	src := `module memory
target x86_64
export def bad_write(ptr: uintptr) -> void abi c:
    inputs: ptr = rdi
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq %rax, 0(%rdi)
        ret
`
	_, issues := Parse("memory.easm", src)
	if !containsIssue(issues, "memory-write-without-clobber") {
		t.Fatalf("expected memory-write-without-clobber, got %#v", issues)
	}
}

func TestVerifyRejectsCallWithoutStackContract(t *testing.T) {
	src := `module calls
target x86_64
export def bad_call(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: cc, memory
    stack: unchanged
    control: returns
    body:
        call *%rdi
        ret
`
	_, issues := Parse("call.easm", src)
	if !containsIssue(issues, "call-without-stack-contract") {
		t.Fatalf("expected call-without-stack-contract, got %#v", issues)
	}
}

func TestVerifyRejectsStaleFlagsBranch(t *testing.T) {
	src := `module flags
target x86_64
export def stale_flags(a: i64, b: i64) -> void abi c:
    inputs: a = rdi, b = rsi
    clobbers: rax, memory
    stack: unchanged
    control: returns
    requires: compare.signed
    body:
        cmpq %rsi, %rdi
        addq $1, %rax
        jl slow_path
        ret
`
	_, issues := Parse("flags.easm", src)
	if !containsIssue(issues, "stale-flags-branch") {
		t.Fatalf("expected stale-flags-branch, got %#v", issues)
	}
}

func TestVerifyRejectsSignedUnsignedBranchIntentMissing(t *testing.T) {
	signedSrc := `module signed
target x86_64
export def missing_signed(a: i64, b: i64) -> void abi c:
    inputs: a = rdi, b = rsi
    clobbers: cc, memory
    stack: unchanged
    control: returns
    body:
        cmpq %rsi, %rdi
        jl slow_path
        ret
`
	_, signedIssues := Parse("signed.easm", signedSrc)
	if !containsIssue(signedIssues, "signed-branch-intent-missing") {
		t.Fatalf("expected signed-branch-intent-missing, got %#v", signedIssues)
	}

	unsignedSrc := `module unsigned
target x86_64
export def missing_unsigned(a: u64, b: u64) -> void abi c:
    inputs: a = rdi, b = rsi
    clobbers: cc, memory
    stack: unchanged
    control: returns
    body:
        cmpq %rsi, %rdi
        jb slow_path
        ret
`
	_, unsignedIssues := Parse("unsigned.easm", unsignedSrc)
	if !containsIssue(unsignedIssues, "unsigned-branch-intent-missing") {
		t.Fatalf("expected unsigned-branch-intent-missing, got %#v", unsignedIssues)
	}
}

func TestVerifyAcceptsDeclaredSignedBranchIntent(t *testing.T) {
	src := `module signed
target x86_64
export def signed_branch(a: i64, b: i64) -> void abi c:
    inputs: a = rdi, b = rsi
    clobbers: cc, memory
    stack: unchanged
    control: returns
    requires: compare.signed
    body:
        cmpq %rsi, %rdi
        jl slow_path
        ret
`
	_, issues := Parse("signed_ok.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected signed branch intent to verify, got %#v", issues)
	}
}

func TestVerifyRejectsPartialRegisterWideReturn(t *testing.T) {
	src := `module partial
target x86_64
export def bad_return() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movb $1, %al
        ret
`
	_, issues := Parse("partial.easm", src)
	if !containsIssue(issues, "partial-register-return") {
		t.Fatalf("expected partial-register-return, got %#v", issues)
	}
}

func TestVerifyRejectsHardCodedAddressWithoutCapability(t *testing.T) {
	src := `module address
target x86_64
export def bad_address() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq 0x7ffbfc000, %rax
        ret
`
	_, issues := Parse("address.easm", src)
	if !containsIssue(issues, "hard-coded-address") {
		t.Fatalf("expected hard-coded-address, got %#v", issues)
	}
}

func TestVerifyRejectsConditionCodeClobberMissing(t *testing.T) {
	src := `module cc
target x86_64
export def bad_flags(a: i64, b: i64) -> void abi c:
    inputs: a = rdi, b = rsi
    clobbers: memory
    stack: unchanged
    control: returns
    requires: compare.signed
    body:
        cmpq %rsi, %rdi
        jl slow_path
        ret
`
	_, issues := Parse("cc.easm", src)
	if !containsIssue(issues, "cc-clobber-missing") {
		t.Fatalf("expected cc-clobber-missing, got %#v", issues)
	}
}

func TestVerifyRejectsImplicitRegisterClobberMissing(t *testing.T) {
	src := `module implicit
target x86_64
export def bad_rdtsc() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, memory
    stack: unchanged
    control: returns
    requires: x86_64.rdtsc
    body:
        rdtsc
        ret
`
	_, issues := Parse("implicit.easm", src)
	if !containsIssue(issues, "implicit-clobber-missing") {
		t.Fatalf("expected implicit-clobber-missing, got %#v", issues)
	}
}

func TestVerifyRejectsCallImplicitConditionCodeClobberMissing(t *testing.T) {
	src := `module call_cc
target x86_64
export def bad_call_cc(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: memory
    stack: aligned 16
    control: returns
    body:
        call *%rdi
        ret
`
	_, issues := Parse("call_cc.easm", src)
	if !containsIssue(issues, "implicit-clobber-missing") {
		t.Fatalf("expected implicit-clobber-missing, got %#v", issues)
	}
}

func TestVerifyRejectsDirectionFlagNotRestored(t *testing.T) {
	src := `module df
target x86_64
export def bad_df() -> void abi c:
    clobbers: cc, memory
    stack: unchanged
    control: returns
    body:
        std
        ret
`
	_, issues := Parse("df.easm", src)
	if !containsIssue(issues, "direction-flag-not-restored") {
		t.Fatalf("expected direction-flag-not-restored, got %#v", issues)
	}
}

func TestVerifyAcceptsRestoredDirectionFlag(t *testing.T) {
	src := `module df
target x86_64
export def good_df() -> void abi c:
    clobbers: cc, memory
    stack: unchanged
    control: returns
    body:
        std
        cld
        ret
`
	_, issues := Parse("df_ok.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected restored direction flag to verify, got %#v", issues)
	}
}

func TestVerifyRejectsHighByteRegisterWithoutCapability(t *testing.T) {
	src := `module high_byte
target x86_64
export def bad_high_byte() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movb $1, %ah
        ret
`
	_, issues := Parse("high_byte.easm", src)
	if !containsIssue(issues, "high-byte-register") {
		t.Fatalf("expected high-byte-register, got %#v", issues)
	}
}

func TestVerifyRejectsAmbiguousOperandSize(t *testing.T) {
	src := `module size
target x86_64
export def bad_size(ptr: uintptr) -> void abi c:
    inputs: ptr = rdi
    clobbers: cc, memory
    stack: unchanged
    control: returns
    body:
        mov $1, 0(%rdi)
        ret
`
	_, issues := Parse("size.easm", src)
	if !containsIssue(issues, "ambiguous-operand-size") {
		t.Fatalf("expected ambiguous-operand-size, got %#v", issues)
	}
}

func TestVerifyRejectsImmediateTruncationWithoutIntent(t *testing.T) {
	src := `module imm
target x86_64
export def bad_imm() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movb $0x1ff, %al
        ret
`
	_, issues := Parse("imm.easm", src)
	if !containsIssue(issues, "immediate-truncation") {
		t.Fatalf("expected immediate-truncation, got %#v", issues)
	}
}

func TestVerifyRejectsSymbolRelocationWithoutIntent(t *testing.T) {
	src := `module reloc
target x86_64
export def bad_symbol() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        lea global_counter(%rip), %rax
        ret
`
	_, issues := Parse("reloc.easm", src)
	if !containsIssue(issues, "symbol-relocation-intent-missing") {
		t.Fatalf("expected symbol-relocation-intent-missing, got %#v", issues)
	}
}

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
    requires: operand_size.inferred, immediate.truncation, relocation.symbol, x86_64.segment.fs
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

func TestVerifyRejectsBadInputBindings(t *testing.T) {
	src := `module bindings
target x86_64
export def bad_inputs(a: i64) -> void abi c:
    inputs: missing = rdi, a = nope, a = rsi
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("bad_inputs.easm", src)
	for _, code := range []string{"unknown-input-binding", "invalid-register-binding", "duplicate-input-binding"} {
		if !containsIssue(issues, code) {
			t.Fatalf("expected %s, got %#v", code, issues)
		}
	}
}

func TestVerifyRejectsMissingInputBinding(t *testing.T) {
	src := `module bindings
target x86_64
export def missing_input(a: i64, b: i64) -> void abi c:
    inputs: a = rdi
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("missing_input.easm", src)
	if !containsIssue(issues, "missing-input-binding") {
		t.Fatalf("expected missing-input-binding, got %#v", issues)
	}
}

func TestVerifyRejectsBadOutputBindings(t *testing.T) {
	src := `module bindings
target x86_64
export def bad_outputs() -> u64 abi c:
    outputs: value = rax, ret = nope, ret = rdx
    clobbers: rax, rdx
    stack: unchanged
    control: returns
    body:
        movq $1, %rax
        ret
`
	_, issues := Parse("bad_outputs.easm", src)
	for _, code := range []string{"unknown-output-binding", "invalid-register-binding", "duplicate-output-binding"} {
		if !containsIssue(issues, code) {
			t.Fatalf("expected %s, got %#v", code, issues)
		}
	}
}

func TestBuildReportRejectsDuplicateExportsAcrossFiles(t *testing.T) {
	first := t.TempDir()
	a := first + "/a.easm"
	b := first + "/b.easm"
	writeEASMTestFile(t, a, `module a
target x86_64
export def same_symbol() -> void abi c:
    stack: unchanged
    control: returns
    body:
        ret
`)
	writeEASMTestFile(t, b, `module b
target x86_64
export def same_symbol() -> void abi c:
    stack: unchanged
    control: returns
    body:
        ret
`)
	report, _ := BuildReport([]string{a, b}, "x86_64")
	if !containsIssue(report.Issues, "duplicate-export") {
		t.Fatalf("expected duplicate-export across files, got %#v", report.Issues)
	}
}

func containsIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func writeEASMTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
