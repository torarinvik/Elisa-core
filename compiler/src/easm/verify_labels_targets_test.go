package easm

import (
	"os"
	"testing"
)

func TestVerifyRejectsUnsatisfiedSegmentLabelPrecondition(t *testing.T) {
	src := `module segment
target x86_64
export def enter_host(selector: u16) -> void abi c:
    facts:
        fs: guest
    inputs: selector = rdi
    labels:
        host_entry: fs: host
    clobbers: memory
    stack: unchanged
    control: noreturn, tail_jumps
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent, control.indirect
    body:
        movw %di, %fs
        jmp host_entry
    host_entry:
        jmp *%rax
`
	_, issues := Parse("segment_label_bad.easm", src)
	if !containsIssue(issues, "label-precondition-unsatisfied") {
		t.Fatalf("expected label-precondition-unsatisfied for unknown fs state, got %#v", issues)
	}
}

func TestVerifyRejectsControlTransferAfterSegmentWriteWithoutStateAssertion(t *testing.T) {
	src := `module segment
target x86_64
export def enter_guest(selector: u16, target: uintptr) -> void abi c:
    inputs: selector = rdi, target = rsi
    clobbers: memory
    stack: unchanged
    control: noreturn, tail_jumps
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent, control.indirect
    body:
        movw %di, %fs
        jmp *%rsi
`
	_, issues := Parse("segment_transfer_unknown.easm", src)
	if !containsIssue(issues, "segment-transfer-state-unknown") {
		t.Fatalf("expected segment-transfer-state-unknown, got %#v", issues)
	}
}

func TestVerifyAcceptsControlTransferAfterSegmentWriteWithStateAssertion(t *testing.T) {
	src := `module segment
target x86_64
export def enter_guest(selector: GuestFsSelector, target: GuestEntryPoint) -> void abi c:
    inputs: selector = rdi, target = rsi
    clobbers: memory
    stack: unchanged
    control: noreturn, tail_jumps
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent, control.indirect
    body:
        movw %di, %fs
        state fs: guest
        jmp *%rsi
`
	_, issues := Parse("segment_transfer_known.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected state assertion to satisfy segment transfer check, got %#v", issues)
	}
}

func TestVerifyAcceptsStackAlignmentLabelPrecondition(t *testing.T) {
	src := `module stackfacts
target x86_64
export def jump_when_call_aligned(value: uintptr, entry: GuestEntryPoint) -> void abi c:
    inputs: value = rdi, entry = rax
    labels:
        call_ready: rsp: mod16=0, rax
    clobbers: memory
    stack: synthetic, aligned 16
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        pushq %rdi
        jmp call_ready
    call_ready:
        jmp *%rax
`
	_, issues := Parse("stack_label.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected stack alignment label precondition to verify, got %#v", issues)
	}
}

func TestVerifyRejectsUnsatisfiedStackAlignmentLabelPrecondition(t *testing.T) {
	src := `module stackfacts
target x86_64
export def jump_when_call_aligned(value: uintptr) -> void abi c:
    inputs: value = rdi
    labels:
        call_ready: rsp: mod16=0
    clobbers: memory
    stack: synthetic, aligned 16
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        jmp call_ready
    call_ready:
        jmp *%rax
`
	_, issues := Parse("stack_label_bad.easm", src)
	if !containsIssue(issues, "label-precondition-unsatisfied") {
		t.Fatalf("expected label-precondition-unsatisfied for stack alignment, got %#v", issues)
	}
}

func TestVerifyRejectsKnownTinyIndirectControlTarget(t *testing.T) {
	src := `module tiny
target x86_64
export def bad_target() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        movq $0x1f4, %rax
        jmp *%rax
`
	_, issues := Parse("tiny_target.easm", src)
	if !containsIssue(issues, "tiny-indirect-control-target") {
		t.Fatalf("expected tiny-indirect-control-target, got %#v", issues)
	}
}

func TestVerifyAllowsIntentionalTinyIndirectControlTarget(t *testing.T) {
	src := `module tiny
target x86_64
export def sentinel_target() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: noreturn, tail_jumps
    requires: control.indirect, control.tiny_target.unchecked
    body:
        movq $0x1f4, %rax
        jmp *%rax
`
	_, issues := Parse("tiny_target_allowed.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected intentional tiny target to verify, got %#v", issues)
	}
}

func TestVerifyRejectsKnownPoisonIndirectControlTarget(t *testing.T) {
	src := `module poison
target x86_64
export def bad_poison_target() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        movq $0xdead0000dead0000, %rax
        jmp *%rax
`
	_, issues := Parse("poison_target.easm", src)
	if !containsIssue(issues, "poison-indirect-control-target") {
		t.Fatalf("expected poison-indirect-control-target, got %#v", issues)
	}
}

func TestVerifyAllowsIntentionalPoisonIndirectControlTarget(t *testing.T) {
	src := `module poison
target x86_64
export def poison_target_test() -> void abi c:
    clobbers: rax
    stack: unchanged
    control: noreturn, tail_jumps
    requires: control.indirect, control.poison_target.unchecked
    body:
        movq $0xdead0000dead0000, %rax
        jmp *%rax
`
	_, issues := Parse("poison_target_allowed.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected intentional poison target to verify, got %#v", issues)
	}
}

func TestVerifyRejectsKnownTinyReturnTarget(t *testing.T) {
	src := `module tinyret
target x86_64
export def bad_ret() -> void abi c:
    stack: unchanged
    control: returns
    body:
        pushq $1
        ret
`
	_, issues := Parse("tiny_ret.easm", src)
	if !containsIssue(issues, "tiny-return-target") {
		t.Fatalf("expected tiny-return-target, got %#v", issues)
	}
}

func TestVerifyRejectsKnownPoisonReturnTarget(t *testing.T) {
	src := `module poisonret
target x86_64
export def bad_ret() -> void abi c:
    stack: unchanged
    control: returns
    body:
        movq $0xdead0000dead0000, %rax
        pushq %rax
        ret
`
	_, issues := Parse("poison_ret.easm", src)
	if !containsIssue(issues, "poison-return-target") {
		t.Fatalf("expected poison-return-target, got %#v", issues)
	}
}

func TestVerifyRejectsRawSegmentSelectorInSegmentMutatingEASM(t *testing.T) {
	src := `module segment_roles
target x86_64
export def load_guest(selector: u16) -> void abi c:
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
	_, issues := Parse("segment_raw_selector.easm", src)
	if !containsIssue(issues, "raw-segment-selector") {
		t.Fatalf("expected raw-segment-selector, got %#v", issues)
	}
}

func TestVerifyAcceptsTypedSegmentSelectorInSegmentMutatingEASM(t *testing.T) {
	src := `module segment_roles
target x86_64
export def load_guest(selector: GuestFsSelector) -> void abi c:
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
	_, issues := Parse("segment_typed_selector.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected typed segment selector to verify, got %#v", issues)
	}
}

func TestVerifyRejectsRawIndirectTailJumpTarget(t *testing.T) {
	src := `module control_roles
target x86_64
export def jump(entry: uintptr, params: uintptr) -> void abi c:
    inputs: entry = rdi, params = rsi
    clobbers: r11
    stack: unchanged
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        movq %rdi, %r11
        jmp *%r11
`
	_, issues := Parse("control_raw_target.easm", src)
	if !containsIssue(issues, "raw-indirect-control-target") {
		t.Fatalf("expected raw-indirect-control-target, got %#v", issues)
	}
}

func TestVerifyAcceptsTypedIndirectTailJumpTarget(t *testing.T) {
	src := `module control_roles
target x86_64
export def jump(entry: GuestEntryPoint, params: HostPtr[void]) -> void abi c:
    inputs: entry = rdi, params = rsi
    clobbers: r11
    stack: unchanged
    control: noreturn, tail_jumps
    requires: control.indirect, input.unused
    body:
        movq %rdi, %r11
        jmp *%r11
`
	_, issues := Parse("control_typed_target.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected typed indirect target to verify, got %#v", issues)
	}
}

func TestVerifyAcceptsPointerWidthMachineRoleReturns(t *testing.T) {
	src := `module machine_roles
target x86_64
export def current_stack() -> HostStackPointer abi c:
    outputs: ret = rax
    clobbers: 
    stack: unchanged
    control: returns
    body:
        movq %rsp, %rax
        ret

export def read_fs0() -> SegmentSelfPointer abi c:
    outputs: ret = rax
    clobbers: memory.read
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs
    body:
        movq %fs:0, %rax
        ret
`
	_, issues := Parse("machine_role_returns.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected pointer-width role returns to verify, got %#v", issues)
	}
}

func TestVerifyRejectsRawStackHandoffPointer(t *testing.T) {
	src := `module stack_roles
target x86_64
export def jump(entry: GuestEntryPoint, stack_top: uintptr) -> void abi c:
    inputs: entry = rdi, stack_top = rsi
    clobbers: r11, rsp, memory
    stack: switches, owns, aligned 16, noreturn
    control: noreturn, tail_jumps
    requires: control.indirect
    body:
        movq %rsi, %rsp
        movq %rdi, %r11
        jmp *%r11
`
	_, issues := Parse("stack_raw_handoff.easm", src)
	if !containsIssue(issues, "raw-stack-handoff") {
		t.Fatalf("expected raw-stack-handoff, got %#v", issues)
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
