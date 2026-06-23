package easm

import "testing"

// Frame conditions: `changes`/`reads` upgrade the coarse `clobbers: memory` discipline into a
// precise per-buffer guarantee. A routine that declares them must route every memory access
// through a named carrier; a routine that declares neither is unaffected.

func TestFrameAcceptsWriteThroughDeclaredChanges(t *testing.T) {
	src := `module frame
target x86_64
export def store_word(dst: HostPtr[u32], val: u32) -> void abi c:
    inputs: dst = rdi, val = rsi
    changes: dst
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        movl %esi, (%rdi)
        ret
`
	_, issues := Parse("frame.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
}

func TestFrameRejectsWriteOutsideChanges(t *testing.T) {
	src := `module frame
target x86_64
export def store_word(dst: HostPtr[u32], other: HostPtr[u32], val: u32) -> void abi c:
    inputs: dst = rdi, other = rsi, val = rdx
    changes: other
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        movl %edx, (%rdi)
        ret
`
	_, issues := Parse("frame.easm", src)
	if !containsIssue(issues, "frame-write-outside-changes") {
		t.Fatalf("expected frame-write-outside-changes, got %#v", issues)
	}
}

func TestFrameRejectsReadOutsideReads(t *testing.T) {
	src := `module frame
target x86_64
export def copy_word(dst: HostPtr[u32], src: HostPtr[u32]) -> void abi c:
    inputs: dst = rdi, src = rsi
    changes: dst
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movl (%rsi), %eax
        movl %eax, (%rdi)
        ret
`
	_, issues := Parse("frame.easm", src)
	if !containsIssue(issues, "frame-read-outside-reads") {
		t.Fatalf("expected frame-read-outside-reads, got %#v", issues)
	}
}

func TestFrameAcceptsReadCoveredByReads(t *testing.T) {
	src := `module frame
target x86_64
export def copy_word(dst: HostPtr[u32], src: HostPtr[u32]) -> void abi c:
    inputs: dst = rdi, src = rsi
    changes: dst
    reads: src
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movl (%rsi), %eax
        movl %eax, (%rdi)
        ret
`
	_, issues := Parse("frame.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
}

func TestFrameRejectsUnknownCarrier(t *testing.T) {
	src := `module frame
target x86_64
export def store_word(dst: HostPtr[u32], val: u32) -> void abi c:
    inputs: dst = rdi, val = rsi
    changes: bogus
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        movl %esi, (%rdi)
        ret
`
	_, issues := Parse("frame.easm", src)
	if !containsIssue(issues, "frame-unknown-carrier") {
		t.Fatalf("expected frame-unknown-carrier, got %#v", issues)
	}
}

// A routine that declares neither clause keeps the legacy coarse-clobber behaviour: a memory
// write needs only `clobbers: memory`, with no per-buffer obligation.
func TestFrameOptOutWhenUndeclared(t *testing.T) {
	src := `module frame
target x86_64
export def store_word(dst: HostPtr[u32], val: u32) -> void abi c:
    inputs: dst = rdi, val = rsi
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        movl %esi, (%rdi)
        ret
`
	_, issues := Parse("frame.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
	if containsIssue(issues, "frame-write-outside-changes") {
		t.Fatalf("unexpected frame enforcement without a frame clause: %#v", issues)
	}
}

// The inline single-line contract form (`changes dst`, no colon) is accepted too.
func TestFrameInlineContractForm(t *testing.T) {
	src := `module frame
target x86_64
export def store_word(dst: HostPtr[u32], val: u32) -> void abi c:
    inputs dst = rdi, val = rsi
    changes dst
    clobbers memory
    stack unchanged
    control returns
    body:
        movl %esi, (%rdi)
        ret
`
	_, issues := Parse("frame.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected no issues with inline contract form, got %#v", issues)
	}
}
