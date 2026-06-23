package easm

import "testing"

// Typed memory layouts (docs/104, TAL increment 1): a `layout` declares the record shape behind a
// carrier, so an access `off(%base)` through `HostPtr[Layout]` is checked to hit a declared field of
// matching width — lifting EASM from "typed pointer" toward "typed assembly".

const layoutPrelude = `module lay
target x86_64
layout ThreadCtx:
    0 entry: u64
    8 arg: u64
    16 ret: u32
`

func TestLayoutParsesFields(t *testing.T) {
	module, issues := Parse("lay.easm", layoutPrelude+`
export def noop() -> void abi c:
    clobbers:
    stack: unchanged
    control: returns
    body:
        ret
`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	if len(module.Layouts) != 1 || module.Layouts[0].Name != "ThreadCtx" || len(module.Layouts[0].Fields) != 3 {
		t.Fatalf("layout not parsed: %#v", module.Layouts)
	}
	if module.Layouts[0].Fields[2].Width != 4 {
		t.Fatalf("ret field width expected 4, got %d", module.Layouts[0].Fields[2].Width)
	}
}

func TestLayoutAcceptsCorrectFieldAccess(t *testing.T) {
	_, issues := Parse("lay.easm", layoutPrelude+`
export def read_ctx(ctx: HostPtr[ThreadCtx]) -> void abi c:
    inputs: ctx = rdi
    reads: ctx
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movq 0(%rdi), %rax
        movq 8(%rdi), %rax
        ret
`)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
}

func TestLayoutRejectsUnknownField(t *testing.T) {
	_, issues := Parse("lay.easm", layoutPrelude+`
export def read_ctx(ctx: HostPtr[ThreadCtx]) -> void abi c:
    inputs: ctx = rdi
    reads: ctx
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movq 24(%rdi), %rax
        ret
`)
	if !containsIssue(issues, "layout-unknown-field") {
		t.Fatalf("expected layout-unknown-field, got %#v", issues)
	}
}

func TestLayoutRejectsWidthMismatch(t *testing.T) {
	_, issues := Parse("lay.easm", layoutPrelude+`
export def read_ctx(ctx: HostPtr[ThreadCtx]) -> void abi c:
    inputs: ctx = rdi
    reads: ctx
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movl 0(%rdi), %eax
        ret
`)
	if !containsIssue(issues, "layout-field-width-mismatch") {
		t.Fatalf("expected layout-field-width-mismatch, got %#v", issues)
	}
}

// The u32 field at offset 16 read with movl (4 bytes) is correct.
func TestLayoutAcceptsNarrowField(t *testing.T) {
	_, issues := Parse("lay.easm", layoutPrelude+`
export def read_ret(ctx: HostPtr[ThreadCtx]) -> void abi c:
    inputs: ctx = rdi
    reads: ctx
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movl 16(%rdi), %eax
        ret
`)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
}

// A plain element carrier (HostPtr[u32]) is not a layout: arbitrary offsets are not field-checked.
func TestLayoutIgnoresNonLayoutCarrier(t *testing.T) {
	_, issues := Parse("lay.easm", layoutPrelude+`
export def read_raw(p: HostPtr[u32]) -> void abi c:
    inputs: p = rdi
    reads: p
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movq 100(%rdi), %rax
        ret
`)
	if containsIssue(issues, "layout-unknown-field") {
		t.Fatalf("non-layout carrier should not be field-checked: %#v", issues)
	}
}

// Provenance propagates: a base derived by moving the carrier into another register is still checked.
func TestLayoutChecksDerivedBaseRegister(t *testing.T) {
	_, issues := Parse("lay.easm", layoutPrelude+`
export def read_via_copy(ctx: HostPtr[ThreadCtx]) -> void abi c:
    inputs: ctx = rdi
    reads: ctx
    clobbers: rax, rsi, memory
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rsi
        movq 24(%rsi), %rax
        ret
`)
	if !containsIssue(issues, "layout-unknown-field") {
		t.Fatalf("expected layout-unknown-field through derived base, got %#v", issues)
	}
}
