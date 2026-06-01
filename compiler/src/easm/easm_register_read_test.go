package easm

// Behavioral coverage for the explicit register-read contract: every register an
// instruction reads must be established -- a declared input, written earlier, a preserved
// callee-saved value, or a structural machine register (rsp/rip) -- or the read is refused.
// Reading an undeclared register consumes whatever indeterminate value the caller left;
// the indirect-jump case (jmp *%rax to an unestablished rax) is a jump to a garbage
// address, the sharpest form of the footgun this check closes.
//
// Assertions target the register-read-uninitialized diagnostic specifically, so a case
// that fails some unrelated contract rule does not mask the property under test.

import "testing"

func TestEASMRegisterReadEnforcement(t *testing.T) {
	const code = "register-read-uninitialized"
	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{
			name:      "read of an uninitialized register is refused",
			wantError: true,
			src: `module t
target x86_64
export def f() -> void abi c:
    clobbers: rbx
    stack: unchanged
    control: returns
    body:
        movq %rax, %rbx
        ret
`,
		},
		{
			name:      "indirect jump through an unestablished register is refused",
			wantError: true,
			src: `module t
target x86_64
export def f() -> void abi c:
    clobbers: memory
    stack: noreturn
    control: noreturn, tail_jumps
    requires: control.indirect, control.target.untyped
    body:
        jmp *%rax
`,
		},
		{
			name:      "read after an earlier write is accepted",
			wantError: false,
			src: `module t
target x86_64
export def f() -> void abi c:
    clobbers: rax, rbx
    stack: unchanged
    control: returns
    body:
        movq $0, %rax
        movq %rax, %rbx
        ret
`,
		},
		{
			name:      "read of a declared input is accepted",
			wantError: false,
			src: `module t
target x86_64
export def f(x: u64) -> void abi c:
    inputs: x = rax
    clobbers: rbx
    stack: unchanged
    control: returns
    body:
        movq %rax, %rbx
        ret
`,
		},
		{
			name:      "read of the structural stack pointer is accepted",
			wantError: false,
			src: `module t
target x86_64
export def f() -> void abi c:
    clobbers: rbx
    stack: unchanged
    control: returns
    body:
        movq %rsp, %rbx
        ret
`,
		},
		{
			name:      "self-zeroing idiom establishes without a read",
			wantError: false,
			src: `module t
target x86_64
export def f() -> void abi c:
    clobbers: rax, rbx, cc
    stack: unchanged
    control: returns
    body:
        xorq %rax, %rax
        movq %rax, %rbx
        ret
`,
		},
		{
			name:      "read of a preserved callee-saved register is accepted",
			wantError: false,
			src: `module t
target x86_64
export def f() -> void abi c:
    clobbers: rbx, memory
    preserves: rbx
    stack: unchanged
    control: returns
    body:
        pushq %rbx
        movq $0, %rbx
        popq %rbx
        ret
`,
		},
		{
			name:      "concrete zero-extension mnemonic overwrites destination",
			wantError: false,
			src: `module t
target x86_64
export def f(x: u8) -> void abi c:
    inputs: x = rax
    clobbers: rcx
    stack: unchanged
    control: returns
    body:
        movzbl %al, %ecx
        ret
`,
		},
		{
			name:      "concrete sign-extension mnemonic overwrites destination",
			wantError: false,
			src: `module t
target x86_64
export def f(x: i8) -> void abi c:
    inputs: x = rax
    clobbers: rcx
    stack: unchanged
    control: returns
    body:
        movsbl %al, %ecx
        ret
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := Parse("t.easm", tc.src)
			if got := easmErrorCodeSet(issues)[code]; got != tc.wantError {
				t.Fatalf("%s present=%v, want %v; all error codes=%v", code, got, tc.wantError, easmErrorCodeSet(issues))
			}
		})
	}
}
