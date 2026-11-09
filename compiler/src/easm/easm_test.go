package easm

import (
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

func TestVerifyAcceptsTypedLabelPreconditions(t *testing.T) {
	src := `module typed_labels
target x86_64-apple-darwin

export def count_down(value: uintptr) -> uintptr abi c:
    inputs: value = rdi
    outputs: ret = rax
    labels:
        loop: rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    requires: compare.unsigned
    body:
        movq %rdi, %rax
    loop:
        cmpq $0, %rax
        je done
        decq %rax
        jmp loop
    done:
        ret
`
	_, issues := Parse("typed_labels.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected typed label preconditions to verify, got %#v", issues)
	}
}

func TestVerifyRejectsUnsatisfiedTypedLabelPrecondition(t *testing.T) {
	src := `module typed_labels
target x86_64-apple-darwin

export def bad_jump(value: uintptr) -> uintptr abi c:
    inputs: value = rdi
    outputs: ret = rax
    labels:
        loop: rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    requires: compare.unsigned
    body:
        jmp loop
    loop:
        cmpq $0, %rax
        je done
        movq %rdi, %rax
    done:
        ret
`
	_, issues := Parse("typed_labels_bad.easm", src)
	if !containsIssue(issues, "label-precondition-unsatisfied") {
		t.Fatalf("expected label-precondition-unsatisfied, got %#v", issues)
	}
}

func TestVerifyRejectsUnknownRequireCapability(t *testing.T) {
	src := `module typo
target x86_64
export def typo_capability() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.segment.wirte
    body:
        ret
`
	_, issues := Parse("typo.easm", src)
	if !containsIssue(issues, "unknown-require-capability") {
		t.Fatalf("expected unknown-require-capability, got %#v", issues)
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
export def swap32(ptr: HostPtr[u32], value: u32) -> u32 abi c:
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

func TestVerifyRejectsXCHGRegisterWriteWithoutClobber(t *testing.T) {
	src := `module xchg
target x86_64
export def swap_regs(left: u64, right: u64) -> void abi c:
    inputs: left = rax, right = rbx
    clobbers: rbx
    stack: unchanged
    control: returns
    requires: x86_64.atomic.rmw
    body:
        xchgq %rax, %rbx
        ret
`
	_, issues := Parse("xchg_reg_clobber.easm", src)
	if !containsIssue(issues, "register-write-without-clobber") {
		t.Fatalf("expected register-write-without-clobber for xchg source register, got %#v", issues)
	}
}

func TestVerifyRejectsXCHGMemoryWritebackRegisterWithoutClobber(t *testing.T) {
	src := `module xchg
target x86_64
export def swap_mem(ptr: HostPtr[u64], value: u64) -> void abi c:
    inputs: ptr = rdi, value = rax
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.atomic.rmw
    body:
        xchgq %rax, (%rdi)
        ret
`
	_, issues := Parse("xchg_mem_clobber.easm", src)
	if !containsIssue(issues, "register-write-without-clobber") {
		t.Fatalf("expected register-write-without-clobber for xchg memory writeback register, got %#v", issues)
	}
}

func TestVerifyAcceptsExplicitQControlAliases(t *testing.T) {
	src := `module q_aliases
target x86_64
export def returns_q() -> void abi c:
    stack: unchanged
    control: returns
    body:
        retq

export def jumps_q(target: GuestEntryPoint) -> void abi c:
    inputs: target = rdi
    clobbers: memory
    stack: noreturn
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        jmpq *%rdi
`
	_, issues := Parse("q_aliases.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected retq/jmpq aliases to verify, got %#v", issues)
	}
}

func TestVerifyAcceptsShadPS4GuestEntryTailJumpTrampoline(t *testing.T) {
	src := `module shadps4_guest
target x86_64
export def shadps4_guest_entry(params: HostPtr[void], exit_func: uintptr) -> void abi ps4_sysv:
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
export def run_on_another_stack(arg: uintptr, entry: HostCallable, stack: GuestStackTop) -> void abi ps4_sysv:
    inputs: arg = rdi, entry = rsi, stack = rdx
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, r12, r13, rsp, rbp, cc, memory
    preserves: r12, r13, callee_saved
    stack: switches, owns
    control: returns
    requires: control.indirect, stack.call_alignment.unchecked, callee_saved.preservation.unchecked, input.unused
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
export def sce_fiber_setjmp(ctx: HostPtr[void], incoming_rax: u64) -> i32 abi ps4_sysv:
    inputs: ctx = rdi, incoming_rax = rax
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

export def sce_fiber_switch_entry(data: HostPtr[void], set_fpu: i32) -> void abi ps4_sysv:
    inputs: data = rdi, set_fpu = rsi
    clobbers: rax, rbx, rcx, rdx, rsi, rdi, r8, r9, r10, r11, r12, r13, r14, r15, rsp, rbp, cc, memory
    preserves: callee_saved
    stack: switches
    control: noreturn, may_fault
    requires: x86_64.fpu_control, x86_64.simd_state, debug.trap, control.indirect, stack.call_alignment.unchecked, input.unused
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
        movq $0, %r12
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
