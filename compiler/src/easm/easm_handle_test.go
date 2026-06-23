package easm

import "testing"

// Existential handle types (docs/104, TAL upgrade-ladder item 4): a `Handle[K]` carrier is an opaque
// token (`∃α. α`). It may be moved, passed, and returned, but never dereferenced or reinterpreted as
// a different handle kind or a concrete pointer.

const handlePrelude = `module h
target x86_64
`

// (a) Moving/passing/returning a Handle[K] cleanly verifies.
func TestHandleMovePassReturnClean(t *testing.T) {
	_, issues := Parse("h.easm", handlePrelude+`
export def carry(tex: Handle[Texture]) -> Handle[Texture] abi c:
    inputs: tex = rdi
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rax
        ret
`)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
}

// (b) Dereferencing a handle base emits handle-deref-forbidden.
func TestHandleDerefForbidden(t *testing.T) {
	_, issues := Parse("h.easm", handlePrelude+`
export def peek(tex: Handle[Texture]) -> u64 abi c:
    inputs: tex = rdi
    outputs: ret = rax
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movq 0(%rdi), %rax
        ret
`)
	if !containsIssue(issues, "handle-deref-forbidden") {
		t.Fatalf("expected handle-deref-forbidden, got %#v", issues)
	}
}

// (c) Using a Handle[K] where Handle[J] is required emits handle-type-confusion (here: a Texture
// handle returned where a Buffer handle is declared).
func TestHandleTypeConfusionReturn(t *testing.T) {
	_, issues := Parse("h.easm", handlePrelude+`
export def relabel(tex: Handle[Texture]) -> Handle[Buffer] abi c:
    inputs: tex = rdi
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rax
        ret
`)
	if !containsIssue(issues, "handle-type-confusion") {
		t.Fatalf("expected handle-type-confusion, got %#v", issues)
	}
}

// (c') Moving a Handle[K] into the input register of a differently-typed handle parameter is also
// confusion.
func TestHandleTypeConfusionCrossParam(t *testing.T) {
	_, issues := Parse("h.easm", handlePrelude+`
export def swap(tex: Handle[Texture], buf: Handle[Buffer]) -> void abi c:
    inputs: tex = rdi, buf = rsi
    clobbers:
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rsi
        ret
`)
	if !containsIssue(issues, "handle-type-confusion") {
		t.Fatalf("expected handle-type-confusion, got %#v", issues)
	}
}

// (d) A non-handle routine is unaffected (same dereference pattern verifies clean through a real
// carrier).
func TestHandleNonHandleRoutineUnaffected(t *testing.T) {
	_, issues := Parse("h.easm", handlePrelude+`
export def peek(ptr: HostPtr[u64]) -> u64 abi c:
    inputs: ptr = rdi
    outputs: ret = rax
    reads: ptr
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movq 0(%rdi), %rax
        ret
`)
	if len(issues) != 0 {
		t.Fatalf("expected no issues for non-handle routine, got %#v", issues)
	}
}
