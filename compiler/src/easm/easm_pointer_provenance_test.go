package easm

// Behavioral coverage for memory-base pointer provenance. A register dereferenced as a
// memory base carries an unstated "this is a valid pointer into some memory" claim. When
// the base comes directly from an input parameter, that parameter must use an address-space
// carrier (HostPtr[T], GuestVAddr[T], ...) that names the memory class, not a raw scalar
// integer -- or the function must require memory.base.untyped to opt out explicitly. Only
// the base register is typed; the scalar index of an addressing mode is not.

import "testing"

func TestEASMMemoryBaseProvenance(t *testing.T) {
	const code = "raw-memory-base"
	cases := []struct {
		name      string
		src       string
		wantError bool
	}{
		{
			name:      "dereferencing a raw uintptr pointer is refused",
			wantError: true,
			src: `module t
target x86_64
export def f(p: uintptr) -> void abi c:
    inputs: p = rdi
    clobbers: rax, memory.read
    stack: unchanged
    control: returns
    body:
        movq (%rdi), %rax
        ret
`,
		},
		{
			name: "a HostPtr carrier names the memory class and is accepted",
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax, memory.read
    stack: unchanged
    control: returns
    body:
        movq (%rdi), %rax
        ret
`,
		},
		{
			name:      "copying a raw pointer to another register does not erase provenance",
			wantError: true,
			src: `module t
target x86_64
export def f(p: uintptr) -> void abi c:
    inputs: p = rdi
    clobbers: rax, rcx, memory.read
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rcx
        movq (%rcx), %rax
        ret
`,
		},
		{
			name:      "lea-derived raw pointer base is refused",
			wantError: true,
			src: `module t
target x86_64
export def f(p: uintptr) -> void abi c:
    inputs: p = rdi
    clobbers: rax, rcx, memory.read
    stack: unchanged
    control: returns
    body:
        leaq 8(%rdi), %rcx
        movq (%rcx), %rax
        ret
`,
		},
		{
			name: "copying a typed pointer keeps the carrier proof",
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax, rcx, memory.read
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rcx
        movq (%rcx), %rax
        ret
`,
		},
		{
			name: "a GuestVAddr carrier is accepted",
			src: `module t
target x86_64
export def f(p: GuestVAddr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax, memory.read
    stack: unchanged
    control: returns
    body:
        movq (%rdi), %rax
        ret
`,
		},
		{
			name: "memory.base.untyped opts out explicitly",
			src: `module t
target x86_64
export def f(p: uintptr) -> void abi c:
    inputs: p = rdi
    clobbers: rax, memory.read
    stack: unchanged
    control: returns
    requires: memory.base.untyped
    body:
        movq (%rdi), %rax
        ret
`,
		},
		{
			name: "only the base is typed, a raw scalar index is fine",
			src: `module t
target x86_64
export def f(p: HostPtr[u64], i: u64) -> void abi c:
    inputs: p = rdi, i = rsi
    clobbers: rax, memory.read
    stack: unchanged
    control: returns
    body:
        movq (%rdi,%rsi,8), %rax
        ret
`,
		},
		{
			name: "the structural stack pointer as a base needs no carrier type",
			src: `module t
target x86_64
export def f() -> void abi c:
    clobbers: rax, memory.read
    stack: unchanged
    control: returns
    body:
        movq (%rsp), %rax
        ret
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := Parse("t.easm", tc.src)
			if got := easmErrorCodeSet(issues)[code]; got != tc.wantError {
				t.Fatalf("%s present=%v want %v; all=%v", code, got, tc.wantError, easmErrorCodeSet(issues))
			}
		})
	}
}
