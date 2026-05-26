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

func TestVerifyRequiresAtomicRMWCapability(t *testing.T) {
	src := `module atomics
target x86_64
export def swap32(ptr: uintptr, value: u32) -> u32 abi c:
    inputs: ptr = rdi, value = rsi
    outputs: ret = rax
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movl %esi, %eax
        xchgl %eax, (%rdi)
        ret
`
	_, issues := Parse("atomic.easm", src)
	if !containsIssue(issues, "missing-capability") {
		t.Fatalf("expected missing-capability for atomic xchg, got %#v", issues)
	}
}

func TestVerifyAcceptsAtomicRMWWithIntent(t *testing.T) {
	src := `module atomics
target x86_64
export def swap32(ptr: uintptr, value: u32) -> u32 abi c:
    inputs: ptr = rdi, value = rsi
    outputs: ret = rax
    clobbers: rax, memory
    stack: unchanged
    control: returns
    requires: x86_64.atomic.rmw
    body:
        movl %esi, %eax
        xchgl %eax, (%rdi)
        ret
`
	_, issues := Parse("atomic.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected atomic xchg with explicit intent to verify, got %#v", issues)
	}
}

func TestVerifyRejectsAtomicRMWMemoryFirstWithoutMemoryClobber(t *testing.T) {
	src := `module atomics
target x86_64
export def swap32(ptr: uintptr, value: u32) -> u32 abi c:
    inputs: ptr = rdi, value = rsi
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    requires: x86_64.atomic.rmw
    body:
        movl %esi, %eax
        xchgl (%rdi), %eax
        ret
`
	_, issues := Parse("atomic_memory_first.easm", src)
	if !containsIssue(issues, "memory-write-without-clobber") {
		t.Fatalf("expected memory-write-without-clobber for memory-first xchg, got %#v", issues)
	}
}

func TestVerifyAcceptsShadPS4GuestEntryTailJumpTrampoline(t *testing.T) {
	src := `module shadps4_guest
target x86_64
export def shadps4_guest_entry(params: uintptr, exit_func: uintptr) -> void abi ps4_sysv:
    inputs: params = rdi, exit_func = rsi
    clobbers: rax, r9, r10, r11, rsi, rdi, rsp, cc, memory
    stack: synthetic, aligned 16, noreturn
    control: noreturn, tail_jumps
    requires: control.indirect
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
    requires: x86_64.rdtsc, x86_64.sse.lfence
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
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, r12, r13, rsp, rbp, cc, memory
    preserves: r12, r13, callee_saved
    stack: switches
    control: returns
    requires: control.indirect, stack.call_alignment.unchecked, callee_saved.preservation.unchecked
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
    clobbers: rax, rbx, rcx, rdx, rsi, rdi, r8, r9, r10, r11, r12, r13, r14, r15, rsp, rbp, cc, memory
    preserves: callee_saved
    stack: switches
    control: noreturn, may_fault
    requires: x86_64.fpu_control, x86_64.simd_state, debug.trap, control.indirect, stack.call_alignment.unchecked
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

func TestVerifyRejectsEntryStackPopEvenWhenRebalanced(t *testing.T) {
	src := `module stack
target x86_64
export def bad_entry_pop() -> void abi c:
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        popq %rax
        pushq %rax
        ret
`
	_, issues := Parse("entry_pop.easm", src)
	if !containsIssue(issues, "entry-stack-pop") {
		t.Fatalf("expected entry-stack-pop, got %#v", issues)
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

func TestVerifyRejectsUnprovenCalleeSavedPreservation(t *testing.T) {
	src := `module callee_saved
target x86_64
export def bad_callee_saved_proof() -> void abi c:
    clobbers: r12, memory
    preserves: r12
    stack: unchanged
    control: returns
    body:
        movq %rax, %r12
        ret
`
	_, issues := Parse("callee_saved_proof.easm", src)
	if !containsIssue(issues, "callee-saved-preservation-unproven") {
		t.Fatalf("expected callee-saved-preservation-unproven, got %#v", issues)
	}
}

func TestVerifyAcceptsProvenCalleeSavedPreservation(t *testing.T) {
	src := `module callee_saved
target x86_64
export def good_callee_saved_proof() -> void abi c:
    clobbers: r12, memory
    preserves: r12
    stack: unchanged
    control: returns
    body:
        pushq %r12
        movq %rax, %r12
        popq %r12
        ret
`
	_, issues := Parse("callee_saved_proof_ok.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected proven callee-saved preservation to verify, got %#v", issues)
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
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, rsp, cc, memory
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

func TestVerifyRejectsRegisterWriteWithoutClobber(t *testing.T) {
	src := `module regs
target x86_64
export def bad_reg_write() -> void abi c:
    stack: unchanged
    control: returns
    body:
        movq %rax, %r10
        ret
`
	_, issues := Parse("regs.easm", src)
	if !containsIssue(issues, "register-write-without-clobber") {
		t.Fatalf("expected register-write-without-clobber, got %#v", issues)
	}
}

func TestVerifyRejectsIndexedMemoryWriteWithoutMemoryClobber(t *testing.T) {
	src := `module memory
target x86_64
export def bad_indexed_write(ptr: uintptr, index: uintptr) -> void abi c:
    inputs: ptr = rdi, index = rsi
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq %rax, 0(%rdi,%rsi,8)
        ret
`
	_, issues := Parse("indexed_memory.easm", src)
	if !containsIssue(issues, "memory-write-without-clobber") {
		t.Fatalf("expected memory-write-without-clobber for indexed address, got %#v", issues)
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

func TestVerifyRejectsCallImmediatelyBeforeRetWithoutIntent(t *testing.T) {
	src := `module calls
target x86_64
export def bad_call_ret(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, cc, memory
    stack: aligned 16
    control: returns
    requires: control.indirect, stack.call_alignment.unchecked
    body:
        call *%rdi
        ret
`
	_, issues := Parse("call_ret.easm", src)
	if !containsIssue(issues, "call-immediately-before-ret") {
		t.Fatalf("expected call-immediately-before-ret, got %#v", issues)
	}
}

func TestVerifyRejectsIndirectControlWithoutIntent(t *testing.T) {
	src := `module indirect
target x86_64
export def bad_indirect(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: cc, memory
    stack: unchanged, aligned 16
    control: returns
    requires: stack.call_alignment.unchecked
    body:
        call *%rdi
        ret
`
	_, issues := Parse("indirect.easm", src)
	if !containsIssue(issues, "indirect-control-intent-missing") {
		t.Fatalf("expected indirect-control-intent-missing, got %#v", issues)
	}
}

func TestVerifyRejectsDirectSymbolControlWithoutIntent(t *testing.T) {
	src := `module direct
target x86_64
export def bad_direct_call() -> void abi c:
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, cc, memory
    stack: aligned 16
    control: returns
    requires: stack.call_alignment.unchecked
    body:
        call helper_symbol
        ret
`
	_, issues := Parse("direct.easm", src)
	if !containsIssue(issues, "direct-control-intent-missing") {
		t.Fatalf("expected direct-control-intent-missing, got %#v", issues)
	}
}

func TestVerifyRejectsMisalignedCall(t *testing.T) {
	src := `module align
target x86_64
export def bad_alignment(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: cc, memory
    stack: unchanged, aligned 16
    control: returns
    requires: control.indirect
    body:
        call *%rdi
        ret
`
	_, issues := Parse("align.easm", src)
	if !containsIssue(issues, "call-stack-misaligned") {
		t.Fatalf("expected call-stack-misaligned, got %#v", issues)
	}
}

func TestVerifyAcceptsProvenAlignedIndirectCall(t *testing.T) {
	src := `module align
target x86_64
export def good_alignment(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, rsp, cc, memory
    stack: unchanged, aligned 16
    control: returns
    requires: control.indirect
    body:
        subq $8, %rsp
        call *%rdi
        addq $8, %rsp
        ret
`
	_, issues := Parse("align_ok.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected proven call alignment to verify, got %#v", issues)
	}
}

func TestVerifyRejectsCallerSavedUseAfterCall(t *testing.T) {
	src := `module live
target x86_64
export def bad_live_after_call(target: uintptr, value: uintptr) -> uintptr abi c:
    inputs: target = rdi, value = rsi
    outputs: ret = rax
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, cc, memory
    stack: unchanged, aligned 16
    control: returns
    requires: control.indirect
    body:
        movq %rsi, %r8
        subq $8, %rsp
        call *%rdi
        addq $8, %rsp
        movq %r8, %rax
        ret
`
	_, issues := Parse("live_after_call.easm", src)
	if !containsIssue(issues, "caller-saved-use-after-call") {
		t.Fatalf("expected caller-saved-use-after-call, got %#v", issues)
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

func TestVerifyRejectsRDTSCWithoutFence(t *testing.T) {
	src := `module implicit
target x86_64
export def bad_unfenced_rdtsc() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, rdx
    stack: unchanged
    control: returns
    requires: x86_64.rdtsc
    body:
        rdtsc
        ret
`
	_, issues := Parse("unfenced_rdtsc.easm", src)
	if !containsIssue(issues, "rdtsc-without-fence") {
		t.Fatalf("expected rdtsc-without-fence, got %#v", issues)
	}
}

func TestVerifyRejectsLFENCEWithoutCapability(t *testing.T) {
	src := `module fence
target x86_64
export def bad_lfence() -> void abi c:
    stack: unchanged
    control: returns
    body:
        lfence
        ret
`
	_, issues := Parse("lfence.easm", src)
	if !containsIssue(issues, "missing-capability") {
		t.Fatalf("expected missing-capability for lfence, got %#v", issues)
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

func TestVerifyDoesNotTreatAArch64SystemRegisterAsSymbol(t *testing.T) {
	src := `module cnt
target aarch64
export def counter() -> u64 abi c:
    outputs: ret = x0
    clobbers: x0, memory
    stack: unchanged
    control: returns
    requires: aarch64.cntvct
    body:
        isb
        mrs %x0, cntvct_el0
        isb
        ret
`
	_, issues := Parse("cnt.easm", src)
	if containsIssue(issues, "symbol-relocation-intent-missing") {
		t.Fatalf("did not expect symbol-relocation-intent-missing for mrs system register, got %#v", issues)
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

func TestVerifyRejectsInvalidSignatureTypes(t *testing.T) {
	src := `module sig
target x86_64
export def bad_sig(ptr: SomeStruct&) -> Vector128 abi c:
    inputs: ptr = rdi
    outputs: ret = rax
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("bad_sig.easm", src)
	if !containsIssue(issues, "invalid-signature-type") {
		t.Fatalf("expected invalid-signature-type, got %#v", issues)
	}
}

func TestVerifyRejectsVoidParameterType(t *testing.T) {
	src := `module sig
target x86_64
export def bad_void_param(value: void) -> void abi c:
    inputs: value = rdi
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("bad_void_param.easm", src)
	if !containsIssue(issues, "invalid-signature-type") {
		t.Fatalf("expected invalid-signature-type, got %#v", issues)
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

func TestVerifyRejectsRegisterTargetMismatch(t *testing.T) {
	x86Src := `module regs
target x86_64
export def bad_x86(value: i64) -> i64 abi c:
    inputs: value = x0
    outputs: ret = rax
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rax
        ret
`
	_, x86Issues := Parse("bad_x86_regs.easm", x86Src)
	if !containsIssue(x86Issues, "register-target-mismatch") {
		t.Fatalf("expected register-target-mismatch for x86 binding, got %#v", x86Issues)
	}

	aarch64Src := `module regs
target aarch64
export def bad_arm(value: i64) -> i64 abi c:
    inputs: value = rdi
    outputs: ret = x0
    stack: unchanged
    control: returns
    body:
        mov x0, x0
        ret
`
	_, armIssues := Parse("bad_arm_regs.easm", aarch64Src)
	if !containsIssue(armIssues, "register-target-mismatch") {
		t.Fatalf("expected register-target-mismatch for aarch64 binding, got %#v", armIssues)
	}
}

func TestVerifyRejectsReturnRegisterMismatch(t *testing.T) {
	src := `module regs
target x86_64
export def bad_ret_reg() -> u64 abi c:
    outputs: ret = rdx
    clobbers: rdx
    stack: unchanged
    control: returns
    body:
        movq $1, %rdx
        ret
`
	_, issues := Parse("bad_ret_reg.easm", src)
	if !containsIssue(issues, "return-register-mismatch") {
		t.Fatalf("expected return-register-mismatch, got %#v", issues)
	}
}

func TestVerifyRejectsInvalidClobberAndPreserveRegisters(t *testing.T) {
	src := `module regs
target x86_64
export def bad_lists() -> void abi c:
    clobbers: nope, x0
    preserves: mystery, x1
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("bad_lists.easm", src)
	for _, code := range []string{"invalid-clobber-register", "invalid-preserve-register", "register-target-mismatch"} {
		if !containsIssue(issues, code) {
			t.Fatalf("expected %s, got %#v", code, issues)
		}
	}
}

func TestVerifyAcceptsFloatReturnRegisters(t *testing.T) {
	x86Src := `module floatret
target x86_64
export def ret_f64() -> f64 abi c:
    outputs: ret = xmm0
    clobbers: xmm0
    stack: unchanged
    control: returns
    body:
        ret
`
	_, x86Issues := Parse("float_x86.easm", x86Src)
	if len(x86Issues) != 0 {
		t.Fatalf("expected x86 float return register to verify, got %#v", x86Issues)
	}

	armSrc := `module floatret
target aarch64
export def ret_f32() -> f32 abi c:
    outputs: ret = s0
    clobbers: s0
    stack: unchanged
    control: returns
    body:
        ret
`
	_, armIssues := Parse("float_arm.easm", armSrc)
	if len(armIssues) != 0 {
		t.Fatalf("expected aarch64 float return register to verify, got %#v", armIssues)
	}
}

func TestVerifyRejectsFloatReturnIntegerRegister(t *testing.T) {
	src := `module floatret
target x86_64
export def bad_float_ret() -> f64 abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("bad_float_ret.easm", src)
	if !containsIssue(issues, "return-register-mismatch") {
		t.Fatalf("expected return-register-mismatch, got %#v", issues)
	}
}

func TestVerifyRejectsTailJumpsWithoutJump(t *testing.T) {
	src := `module control
target x86_64
export def bad_tail_contract() -> void abi c:
    stack: unchanged
    control: returns, tail_jumps
    body:
        ret
`
	_, issues := Parse("tail_contract.easm", src)
	if !containsIssue(issues, "tail-jumps-without-jump") {
		t.Fatalf("expected tail-jumps-without-jump, got %#v", issues)
	}
}

func TestVerifyRejectsMayFaultWithoutFaultingOperation(t *testing.T) {
	src := `module control
target x86_64
export def bad_may_fault() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: returns, may_fault
    body:
        movq %rax, %rax
        ret
`
	_, issues := Parse("may_fault.easm", src)
	if !containsIssue(issues, "may-fault-without-faulting-op") {
		t.Fatalf("expected may-fault-without-faulting-op, got %#v", issues)
	}
}

func TestVerifyRejectsNoreturnJumpWithoutTailContract(t *testing.T) {
	src := `module control
target x86_64
export def bad_noreturn_tail(target: uintptr) -> void abi c:
    inputs: target = rdi
    clobbers: memory
    stack: unchanged
    control: noreturn
    body:
        jmp *%rdi
`
	_, issues := Parse("noreturn_tail.easm", src)
	if !containsIssue(issues, "noreturn-jump-without-tail-contract") {
		t.Fatalf("expected noreturn-jump-without-tail-contract, got %#v", issues)
	}
}

func TestVerifyRejectsDirectStackPointerWriteUnderUnchangedStack(t *testing.T) {
	src := `module stack
target x86_64
export def bad_sp_write(value: uintptr) -> void abi c:
    inputs: value = rdi
    clobbers: rsp, memory
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rsp
        ret
`
	_, issues := Parse("sp_write.easm", src)
	if !containsIssue(issues, "stack-pointer-write-unchanged") {
		t.Fatalf("expected stack-pointer-write-unchanged, got %#v", issues)
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
