package easm

import (
	"testing"
)

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
export def good_alignment(target: HostCallable) -> void abi c:
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

func TestVerifyRejectsUnwrittenReturnRegister(t *testing.T) {
	src := `module ret
target x86_64
export def bad_return() -> u64 abi c:
    outputs: ret = rax
    clobbers: rcx
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rcx
        ret
`
	_, issues := Parse("unwritten_ret.easm", src)
	if !containsIssue(issues, "return-register-not-written") {
		t.Fatalf("expected return-register-not-written, got %#v", issues)
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
