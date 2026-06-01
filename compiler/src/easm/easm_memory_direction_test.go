package easm

// Behavioral coverage for the memory-direction contract. Previously only stores required a
// declaration; a load through a pointer was silently accepted. Now a memory load requires a
// `memory` or `memory.read` clobber and a store requires `memory` or `memory.write`, so a
// function can no longer read or write memory without saying so. `memory` remains the broad
// both-directions declaration; lea computes an address without touching memory and needs
// neither.

import "testing"

func TestEASMMemoryDirectionEnforcement(t *testing.T) {
	const readCode = "memory-read-without-clobber"
	const writeCode = "memory-write-without-clobber"
	cases := []struct {
		name      string
		src       string
		wantRead  bool
		wantWrite bool
	}{
		{
			name:     "undeclared load is refused",
			wantRead: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq (%rdi), %rax
        ret
`,
		},
		{
			name: "load declared memory.read is accepted",
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
			name: "load declared broad memory is accepted",
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movq (%rdi), %rax
        ret
`,
		},
		{
			name:      "undeclared store is refused",
			wantWrite: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u64], v: u64) -> void abi c:
    inputs: p = rdi, v = rsi
    clobbers:
    stack: unchanged
    control: returns
    body:
        movq %rsi, (%rdi)
        ret
`,
		},
		{
			name: "store declared memory.write is accepted",
			src: `module t
target x86_64
export def f(p: HostPtr[u64], v: u64) -> void abi c:
    inputs: p = rdi, v = rsi
    clobbers: memory.write
    stack: unchanged
    control: returns
    body:
        movq %rsi, (%rdi)
        ret
`,
		},
		{
			name: "lea needs no memory clobber (address computation, not access)",
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        leaq (%rdi), %rax
        ret
`,
		},
		{
			name: "read-modify-write of memory is covered by broad memory",
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: cc, memory
    stack: unchanged
    control: returns
    body:
        addq $1, (%rdi)
        ret
`,
		},
		{
			name:     "single-operand fpu control load requires memory.read",
			wantRead: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u16]) -> void abi c:
    inputs: p = rdi
    clobbers:
    stack: unchanged
    control: returns
    requires: x86_64.fpu_control
    body:
        fldcw (%rdi)
        ret
`,
		},
		{
			name:      "single-operand fpu control store requires memory.write",
			wantWrite: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u16]) -> void abi c:
    inputs: p = rdi
    clobbers:
    stack: unchanged
    control: returns
    requires: x86_64.fpu_control
    body:
        fnstcw (%rdi)
        ret
`,
		},
		{
			name: "single-operand fpu control access accepts precise memory directions",
			src: `module t
target x86_64
export def f(p: HostPtr[u16], q: HostPtr[u32]) -> void abi c:
    inputs: p = rdi, q = rsi
    clobbers: memory.read, memory.write
    stack: unchanged
    control: returns
    requires: x86_64.fpu_control
    body:
        fldcw (%rdi)
        stmxcsr (%rsi)
        ret
`,
		},
		{
			name:     "read-modify-write declared write-only still flags the load",
			wantRead: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: cc, memory.write
    stack: unchanged
    control: returns
    body:
        addq $1, (%rdi)
        ret
`,
		},
		{
			name:      "single-operand memory increment requires read and write",
			wantRead:  true,
			wantWrite: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: cc
    stack: unchanged
    control: returns
    body:
        incq (%rdi)
        ret
`,
		},
		{
			name:     "push from memory requires read coverage beyond stack contract",
			wantRead: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax, memory.write
    stack: unchanged
    control: returns
    body:
        pushq (%rdi)
        popq %rax
        ret
`,
		},
		{
			name:      "pop to memory requires write coverage beyond stack contract",
			wantWrite: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax, memory.read
    stack: unchanged
    control: returns
    body:
        pushq %rax
        popq (%rdi)
        ret
`,
		},
		{
			name:     "memory-indirect call requires read coverage for target load",
			wantRead: true,
			src: `module t
target x86_64
export def f(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax, rcx, rdx, rsi, rdi, r8, r9, r10, r11, cc, memory.write
    stack: aligned 16
    control: returns
    requires: control.indirect
    body:
        call *(%rdi)
        ret
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := Parse("t.easm", tc.src)
			codes := easmErrorCodeSet(issues)
			if codes[readCode] != tc.wantRead {
				t.Fatalf("%s present=%v want %v; all=%v", readCode, codes[readCode], tc.wantRead, codes)
			}
			if codes[writeCode] != tc.wantWrite {
				t.Fatalf("%s present=%v want %v; all=%v", writeCode, codes[writeCode], tc.wantWrite, codes)
			}
		})
	}
}
