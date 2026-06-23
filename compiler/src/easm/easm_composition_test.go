package easm

import (
	"sort"
	"strings"
	"testing"
)

// Stage 1 of docs/101: `fragment` + `compose` desugar to a concrete per-target instruction
// stream that the existing verifier certifies unchanged (splice-before-verify). These tests
// pin (a) the variant-matrix collapse — one source routine materializing the four real
// jump-entry variants — and (b) that composition cannot smuggle an unchecked sequence past
// the verifier.

func functionNames(module *Module) []string {
	var names []string
	for _, fn := range module.Functions {
		names = append(names, fn.Name)
	}
	sort.Strings(names)
	return names
}

func TestComposeSplicesAndVerifiesWhole(t *testing.T) {
	src := `module compose_basic
target x86_64
fragment store_zero(dst):
    clobbers: memory
    body:
        movq $0, (<dst>)
export def clear(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        compose store_zero(%rdi)
        ret
`
	module, issues := Parse("compose_basic.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected clean composition, got %#v", issues)
	}
	if got := functionNames(module); len(got) != 1 || got[0] != "clear" {
		t.Fatalf("expected single materialized export clear, got %v", got)
	}
	// the spliced store must be present in the verified body
	found := false
	for _, inst := range module.Functions[0].Instructions {
		if strings.Contains(inst.Text, "movq $0, (%rdi)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("fragment body was not spliced into the host routine: %#v", module.Functions[0].Instructions)
	}
}

func TestComposeMaterializesVariantMatrix(t *testing.T) {
	// One source routine with two independent flags (ZERO, LOAD) materializes 2^2 = 4
	// concrete, individually verified exports — the real JumpMainEntry / WithFs / OnStack /
	// OnStackWithFs matrix collapsed to a single definition.
	src := `module compose_matrix
target x86_64
fragment store_zero(dst):
    clobbers: memory
    body:
        movq $0, (<dst>)
export def touch(a: HostPtr[u64], b: HostPtr[u64]) -> void abi c:
    inputs: a = rdi, b = rsi
    clobbers: rax, memory
    stack: unchanged
    control: returns
    body:
        movq (%rdi), %rax
        movq (%rsi), %rax
        compose ZERO store_zero(%rdi)
        compose LOAD store_zero(%rsi)
        ret
`
	module, issues := Parse("compose_matrix.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected all four variants to verify clean, got %#v", issues)
	}
	want := []string{"touch", "touch__LOAD", "touch__ZERO", "touch__ZERO_LOAD"}
	if got := functionNames(module); !equalStrings(got, want) {
		t.Fatalf("variant matrix mismatch:\n got  %v\n want %v", got, want)
	}
	// conditional splicing: the bare variant has neither store; the full variant has both.
	if bodyContainsStore(variantNamed(module, "touch"), "%rdi") || bodyContainsStore(variantNamed(module, "touch"), "%rsi") {
		t.Fatalf("bare variant wrongly spliced a conditional fragment: %#v", variantNamed(module, "touch").Instructions)
	}
	full := variantNamed(module, "touch__ZERO_LOAD")
	if !bodyContainsStore(full, "%rdi") || !bodyContainsStore(full, "%rsi") {
		t.Fatalf("full variant missing a conditional fragment: %#v", full.Instructions)
	}
}

func variantNamed(module *Module, name string) *Function {
	for i := range module.Functions {
		if module.Functions[i].Name == name {
			return &module.Functions[i]
		}
	}
	return nil
}

func bodyContainsStore(fn *Function, reg string) bool {
	if fn == nil {
		return false
	}
	for _, inst := range fn.Instructions {
		if strings.Contains(inst.Text, "movq $0, ("+reg+")") {
			return true
		}
	}
	return false
}

func TestComposeFragmentReadingUninitializedRegisterRejected(t *testing.T) {
	// A fragment that reads a register the host never establishes is a verify error in the
	// composition: liveness flows across the splice boundary.
	src := `module compose_uninit
target x86_64
fragment read_r11():
    clobbers: rax
    body:
        movq %r11, %rax
export def bad(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        compose read_r11()
        ret
`
	_, issues := Parse("compose_uninit.easm", src)
	if !containsIssue(issues, "register-read-uninitialized") {
		t.Fatalf("expected register-read-uninitialized across the splice, got %#v", issues)
	}
}

func TestComposeFragmentWriteWithoutClobberRejected(t *testing.T) {
	// A fragment that writes a register neither it nor the host declares is rejected: the
	// clobber contract is enforced on the spliced whole, not waived by composition.
	src := `module compose_clobber
target x86_64
fragment touch_r10():
    body:
        movq $0, %r10
export def bad2() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        compose touch_r10()
        ret
`
	_, issues := Parse("compose_clobber.easm", src)
	if !containsIssue(issues, "register-write-without-clobber") {
		t.Fatalf("expected register-write-without-clobber across the splice, got %#v", issues)
	}
}

func TestComposeFragmentSegmentWriteIntentNotBypassed(t *testing.T) {
	// A fragment that writes %fs without declaring segment capability cannot launder the
	// privileged access through composition — the segment intent check still fires.
	src := `module compose_segcap
target x86_64
fragment install_fs(sel):
    clobbers: memory
    body:
        movw <sel>, %fs
        state fs: guest
export def enter(sel: GuestFsSelector) -> void abi c:
    inputs: sel = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        compose install_fs(%di)
        ret
`
	_, issues := Parse("compose_segcap.easm", src)
	hasSegmentIssue := false
	for _, issue := range issues {
		if strings.HasPrefix(issue.Code, "segment") {
			hasSegmentIssue = true
		}
	}
	if !hasSegmentIssue {
		t.Fatalf("expected a segment intent/lifetime issue from the laundered %%fs write, got %#v", issues)
	}
}

func TestComposeUnknownFragmentRejected(t *testing.T) {
	src := `module compose_unknown
target x86_64
export def bad3() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        compose nonexistent(%rdi)
        ret
`
	_, issues := Parse("compose_unknown.easm", src)
	if !containsIssue(issues, "unknown-fragment") {
		t.Fatalf("expected unknown-fragment, got %#v", issues)
	}
}

func TestComposeConditionalInputDroppedFromVariantsThatSkipIt(t *testing.T) {
	// `scratch` is consumed only by the FS-conditional fragment. The bare variant must drop
	// its binding (and verify clean); the FS variant must keep it.
	src := `module compose_condinput
target x86_64
fragment store_zero(dst):
    clobbers: memory
    body:
        movq $0, (<dst>)
export def maybe(p: HostPtr[u64], scratch: HostPtr[u64]) -> void abi c:
    inputs: p = rdi, scratch = rsi
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        movq $0, (%rdi)
        compose FS store_zero(%rsi)
        ret
`
	module, issues := Parse("compose_condinput.easm", src)
	if len(issues) != 0 {
		t.Fatalf("conditional input should not trip input-register-unused, got %#v", issues)
	}
	bare := variantNamed(module, "maybe")
	if bare == nil || inputsBindRegister(bare, "rsi") || len(bare.Params) != 1 {
		t.Fatalf("bare variant should have dropped the conditional scratch param+binding: %#v", bare)
	}
	withFs := variantNamed(module, "maybe__FS")
	if withFs == nil || !inputsBindRegister(withFs, "rsi") || len(withFs.Params) != 2 {
		t.Fatalf("FS variant should keep the scratch param+binding: %#v", withFs)
	}
}

func TestComposeGenuinelyDeadInputStillRejected(t *testing.T) {
	// `never` is read by no variant — it is not conditional, it is dead — so the verifier
	// must still flag it. pruneConditionalInputs must not launder real bugs.
	src := `module compose_deadinput
target x86_64
fragment store_zero(dst):
    clobbers: memory
    body:
        movq $0, (<dst>)
export def dead(p: HostPtr[u64], never: HostPtr[u64]) -> void abi c:
    inputs: p = rdi, never = rsi
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        movq $0, (%rdi)
        compose FS store_zero(%rdi)
        ret
`
	_, issues := Parse("compose_deadinput.easm", src)
	if !containsIssue(issues, "input-register-unused") {
		t.Fatalf("expected dead input to still be flagged, got %#v", issues)
	}
}

func TestComposeVariantsPreserveSignatureForEmission(t *testing.T) {
	// 1b regression guard: materialized variants must carry the source signature (params,
	// return type, ABI) so the backend emits each as a correct extern symbol.
	src := `module compose_emit
target x86_64
fragment store_zero(dst):
    clobbers: memory
    body:
        movq $0, (<dst>)
export def emit(p: HostPtr[u64]) -> void abi c:
    inputs: p = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        movq $0, (%rdi)
        compose Z store_zero(%rdi)
        ret
`
	module, issues := Parse("compose_emit.easm", src)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	for _, name := range []string{"emit", "emit__Z"} {
		fn := variantNamed(module, name)
		if fn == nil {
			t.Fatalf("missing variant %s for emission", name)
		}
		if fn.ReturnType != "void" || fn.ABI != "c" || len(fn.Params) != 1 || fn.Params[0].Type != "HostPtr[u64]" {
			t.Fatalf("variant %s lost its signature: ret=%q abi=%q params=%#v", name, fn.ReturnType, fn.ABI, fn.Params)
		}
	}
}

func inputsBindRegister(fn *Function, reg string) bool {
	for _, input := range fn.Inputs {
		if canonicalX86GPR(registerAfterEquals(input)) == reg {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

