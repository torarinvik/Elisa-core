package easm

import (
	"strings"
	"testing"
)

// docs/101 §8: make EASM read like Elisa rather than a bolted-on assembler. These pin the two
// native-surface wins — bare-name holes (unified with the template surface) and the unbracketed
// `can X, Y` effect form — while keeping the legacy `<name>` / `can[...]` forms working.

func TestComposeBareNameHole(t *testing.T) {
	src := `module bare
target x86_64
fragment store_zero(dst):
    clobbers: memory
    body:
        movq $0, (dst)
export def clear(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        compose store_zero(%rdi)
        ret
`
	module, issues := Parse("bare.easm", src)
	if len(issues) != 0 {
		t.Fatalf("bare-name hole should compose cleanly, got %#v", issues)
	}
	fn := variantNamed(module, "clear")
	found := false
	for _, inst := range fn.Instructions {
		if strings.Contains(inst.Text, "movq $0, (%rdi)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("bare-name hole was not substituted: %#v", fn.Instructions)
	}
}

func TestBareNameDoesNotTouchRegisters(t *testing.T) {
	// A hole named like a register fragment must not corrupt a %-prefixed register token.
	src := `module bare2
target x86_64
fragment use(ax):
    clobbers: rax, memory
    body:
        movq ax, %rax
        movq $0, (%rax)
export def go(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        compose use(%rdi)
        ret
`
	module, issues := Parse("bare2.easm", src)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	fn := variantNamed(module, "go")
	for _, inst := range fn.Instructions {
		if strings.Contains(inst.Text, "%r%rdix") || strings.Contains(inst.Text, "(%r%rdix)") {
			t.Fatalf("bare-name substitution corrupted a register token: %q", inst.Text)
		}
	}
	// the bare `ax` operand became %rdi; the %rax register is untouched.
	if !bodyHasText(fn, "movq %rdi, %rax") || !bodyHasText(fn, "movq $0, (%rax)") {
		t.Fatalf("unexpected substitution result: %#v", fn.Instructions)
	}
}

func TestNativeCanEffectSyntax(t *testing.T) {
	src := `module eff
target x86_64
export def touch() -> void abi c can Unsafe.RawExtern, Unsafe.SegmentMutation:
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        ret
`
	module, issues := Parse("eff.easm", src)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	fn := variantNamed(module, "touch")
	if fn == nil || !equalStrings(fn.Effects, []string{"Unsafe.RawExtern", "Unsafe.SegmentMutation"}) {
		t.Fatalf("native can not parsed: %#v", fn)
	}
}

func TestBracketCanStillWorks(t *testing.T) {
	src := `module eff2
target x86_64
export def touch() -> void abi c can[Unsafe.RawExtern]:
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        ret
`
	module, issues := Parse("eff2.easm", src)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	fn := variantNamed(module, "touch")
	if fn == nil || !equalStrings(fn.Effects, []string{"Unsafe.RawExtern"}) {
		t.Fatalf("legacy can[...] regressed: %#v", fn)
	}
}

func TestNativeFunctionFormNoBodyMarker(t *testing.T) {
	// Reads like an Elisa function: inline contracts, instructions that just begin, no `body:`.
	src := `module native
target x86_64
export def read_gs() -> u16 abi c:
    outputs ret = rax
    stack unchanged
    control returns
    requires x86_64.segment.gs
    movw %gs, %ax
    ret
`
	module, issues := Parse("native.easm", src)
	if len(issues) != 0 {
		t.Fatalf("native form should verify clean, got %#v", issues)
	}
	fn := variantNamed(module, "read_gs")
	if fn == nil {
		t.Fatal("missing read_gs")
	}
	if !bodyHasText(fn, "movw %gs, %ax") || !bodyHasText(fn, "ret") {
		t.Fatalf("instructions not parsed without a body: marker: %#v", fn.Instructions)
	}
	if !equalStrings(fn.Requires, []string{"x86_64.segment.gs"}) {
		t.Fatalf("inline requires not captured: %#v", fn.Requires)
	}
}

func TestNativeInlineRequiresIsEnforced(t *testing.T) {
	// The inline `requires` is the real contract: omit it and reading %gs must be flagged, so
	// the native surface lowers to the same verified Function as the legacy section form.
	src := `module native2
target x86_64
export def read_gs() -> u16 abi c:
    outputs ret = rax
    stack unchanged
    control returns
    movw %gs, %ax
    ret
`
	_, issues := Parse("native2.easm", src)
	found := false
	for _, i := range issues {
		if strings.HasPrefix(i.Code, "segment") || i.Code == "missing-capability" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing inline requires should flag the %%gs read, got %#v", issues)
	}
}

func bodyHasText(fn *Function, want string) bool {
	for _, inst := range fn.Instructions {
		if strings.TrimSpace(inst.Text) == want {
			return true
		}
	}
	return false
}
