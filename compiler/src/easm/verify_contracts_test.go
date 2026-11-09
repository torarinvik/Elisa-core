package easm

import (
	"strings"
	"testing"
)

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

func TestVerifyRejectsUnusedInputRegister(t *testing.T) {
	src := `module bindings
target x86_64
export def unused_input(a: i64) -> void abi c:
    inputs: a = rdi
    stack: unchanged
    control: returns
    body:
        ret
`
	_, issues := Parse("unused_input.easm", src)
	if !containsIssue(issues, "input-register-unused") {
		t.Fatalf("expected input-register-unused, got %#v", issues)
	}
}

func TestVerifyAcceptsExplicitUnusedInputRegister(t *testing.T) {
	src := `module bindings
target x86_64
export def unused_input(a: i64) -> void abi c:
    inputs: a = rdi
    stack: unchanged
    control: returns
    requires: input.unused
    body:
        ret
`
	_, issues := Parse("unused_input_ok.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected explicit unused input to verify, got %#v", issues)
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
    requires: return.register.preinitialized
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
    requires: return.register.preinitialized
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

func TestBuildReportIncludesTypedLabelContracts(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/typed.easm"
	writeEASMTestFile(t, path, `module typed
target x86_64
export def loop(value: uintptr) -> uintptr abi c:
    inputs: value = rdi
    outputs: ret = rax
    labels:
        again: rax
    clobbers: rax, cc
    stack: unchanged
    control: returns
    requires: compare.unsigned
    body:
        movq %rdi, %rax
    again:
        cmpq $0, %rax
        je done
        decq %rax
        jmp again
    done:
        ret
`)
	report, _ := BuildReport([]string{path}, "x86_64")
	if len(report.Modules) != 1 || len(report.Modules[0].Exports) != 1 {
		t.Fatalf("expected one EASM export, got %#v", report.Modules)
	}
	if got := strings.Join(report.Modules[0].Exports[0].Labels, ","); got != "again: rax" {
		t.Fatalf("expected label contract in report, got %q", got)
	}
	formatted, err := FormatReport(report, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formatted, "label again: rax") {
		t.Fatalf("expected formatted report to include label contract, got:\n%s", formatted)
	}
}

func TestVerifyAcceptsSegmentLabelPrecondition(t *testing.T) {
	src := `module segment
target x86_64
export def enter_host(selector: HostFsSelector, entry: GuestEntryPoint) -> void abi c:
    facts:
        fs: guest
    inputs: selector = rdi, entry = rax
    labels:
        host_entry: fs: host, rax
    clobbers: memory
    stack: unchanged
    control: noreturn, tail_jumps
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent, control.indirect
    body:
        movw %di, %fs
        state fs: host
        jmp host_entry
    host_entry:
        jmp *%rax
`
	_, issues := Parse("segment_label.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected segment label precondition to verify, got %#v", issues)
	}
}
