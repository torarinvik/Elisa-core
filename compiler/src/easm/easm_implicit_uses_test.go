package easm

// Behavioral coverage for the implicit-read enforcement added to the validator: an
// instruction that reads a register invisibly (cpuid's leaf in eax / subleaf in ecx) must
// have that register established -- as a declared input or an earlier write -- or the
// function is reading an indeterminate caller-left value.
//
// Assertions target the implicit-read-uninitialized diagnostic specifically rather than
// "validates clean", so they isolate this feature from unrelated contract rules (e.g.
// cpuid also clobbers the callee-saved rbx, which has its own preservation requirement).

import "testing"

func easmErrorCodeSet(issues []Issue) map[string]bool {
	out := map[string]bool{}
	for _, is := range issues {
		if is.Severity == "error" {
			out[is.Code] = true
		}
	}
	return out
}

func TestEASMImplicitReadEnforcement(t *testing.T) {
	const code = "implicit-read-uninitialized"
	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{
			name:      "uninitialized eax/ecx is flagged",
			wantError: true,
			src: `module t
target x86_64
export def cpuid_bad() -> void abi c:
    clobbers: rax, rbx, rcx, rdx
    stack: unchanged
    control: returns
    requires: x86_64.cpuid
    body:
        cpuid
        ret
`,
		},
		{
			name:      "eax written but ecx not is still flagged",
			wantError: true,
			src: `module t
target x86_64
export def cpuid_half() -> void abi c:
    clobbers: rax, rbx, rcx, rdx
    stack: unchanged
    control: returns
    requires: x86_64.cpuid
    body:
        movq $1, %rax
        cpuid
        ret
`,
		},
		{
			name:      "eax and ecx written before cpuid is accepted",
			wantError: false,
			src: `module t
target x86_64
export def cpuid_write_first() -> void abi c:
    clobbers: rax, rbx, rcx, rdx
    stack: unchanged
    control: returns
    requires: x86_64.cpuid
    body:
        movq $1, %rax
        movq $0, %rcx
        cpuid
        ret
`,
		},
		{
			name:      "eax and ecx declared as inputs is accepted",
			wantError: false,
			src: `module t
target x86_64
export def cpuid_inputs(leaf: u64, sub: u64) -> void abi c:
    inputs: leaf = rax, sub = rcx
    clobbers: rax, rbx, rcx, rdx
    stack: unchanged
    control: returns
    requires: x86_64.cpuid
    body:
        cpuid
        ret
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := Parse("t.easm", tc.src)
			codes := easmErrorCodeSet(issues)
			if got := codes[code]; got != tc.wantError {
				t.Fatalf("%s present=%v, want %v; all error codes=%v", code, got, tc.wantError, codes)
			}
		})
	}
}
