package easm

import "testing"

// Stage 3: `reference:` + `target <arch> lockstep <ref>:` blocks. This increment lands the surface
// and the structural proof obligations; the runtime equivalence oracle (docs/103 stage 3c) is not
// yet wired. Crucially, every sub-body is verified with the full machinery — an optimized target
// gets no free pass.

func TestLockstepAcceptsMatchingReferenceAndTarget(t *testing.T) {
	src := `module ls
target x86_64
export def copy_one(dst: HostPtr[u64], src: HostPtr[u64]) -> void abi c:
    inputs: dst = rdi, src = rsi
    changes: dst
    reads: src
    clobbers: rax, memory
    stack: unchanged
    control: returns
    reference:
        movq (%rsi), %rax
        movq %rax, (%rdi)
        ret
    target x86_64 lockstep reference:
        movq (%rsi), %rax
        movq %rax, (%rdi)
        ret
`
	module, issues := Parse("ls.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %#v", issues)
	}
	fn := module.Functions[0]
	if len(fn.Reference) == 0 {
		t.Fatalf("reference body not captured: %#v", fn)
	}
	if len(fn.Targets) != 1 || fn.Targets[0].Lockstep != "reference" || fn.Targets[0].Arch != "x86_64" {
		t.Fatalf("target body not captured: %#v", fn.Targets)
	}
	if len(fn.Instructions) != 0 {
		t.Fatalf("primary body should be empty for a reference/target routine, got %#v", fn.Instructions)
	}
}

func TestLockstepRejectsUnknownReference(t *testing.T) {
	src := `module ls
target x86_64
export def copy_one(dst: HostPtr[u64], src: HostPtr[u64]) -> void abi c:
    inputs: dst = rdi, src = rsi
    changes: dst
    reads: src
    clobbers: rax, memory
    stack: unchanged
    control: returns
    reference:
        movq (%rsi), %rax
        movq %rax, (%rdi)
        ret
    target x86_64 lockstep refz:
        movq (%rsi), %rax
        movq %rax, (%rdi)
        ret
`
	_, issues := Parse("ls.easm", src)
	if !containsIssue(issues, "lockstep-unknown-reference") {
		t.Fatalf("expected lockstep-unknown-reference, got %#v", issues)
	}
}

func TestLockstepRejectsMissingReference(t *testing.T) {
	src := `module ls
target x86_64
export def copy_one(dst: HostPtr[u64], src: HostPtr[u64]) -> void abi c:
    inputs: dst = rdi, src = rsi
    changes: dst
    reads: src
    clobbers: rax, memory
    stack: unchanged
    control: returns
    target x86_64 lockstep reference:
        movq (%rsi), %rax
        movq %rax, (%rdi)
        ret
`
	_, issues := Parse("ls.easm", src)
	if !containsIssue(issues, "lockstep-missing-reference") {
		t.Fatalf("expected lockstep-missing-reference, got %#v", issues)
	}
}

// The integrity test: a target body that clobbers an undeclared register must be rejected exactly
// as an ordinary body would be. Translation validation only means something if the bytes are checked.
func TestLockstepVerifiesTargetBodyNoFreePass(t *testing.T) {
	src := `module ls
target x86_64
export def copy_one(dst: HostPtr[u64], src: HostPtr[u64]) -> void abi c:
    inputs: dst = rdi, src = rsi
    changes: dst
    reads: src
    clobbers: rax, memory
    stack: unchanged
    control: returns
    reference:
        movq (%rsi), %rax
        movq %rax, (%rdi)
        ret
    target x86_64 lockstep reference:
        movq (%rsi), %rax
        movq %rax, %rcx
        movq %rcx, (%rdi)
        ret
`
	_, issues := Parse("ls.easm", src)
	if !containsIssue(issues, "register-write-without-clobber") {
		t.Fatalf("expected target body to be verified (register-write-without-clobber), got %#v", issues)
	}
}

// The reference (spec) body is verified too: a frame violation in it is caught.
func TestLockstepVerifiesReferenceBody(t *testing.T) {
	src := `module ls
target x86_64
export def copy_one(dst: HostPtr[u64], src: HostPtr[u64]) -> void abi c:
    inputs: dst = rdi, src = rsi
    changes: dst
    clobbers: rax, memory
    stack: unchanged
    control: returns
    reference:
        movq (%rsi), %rax
        movq %rax, (%rdi)
        ret
    target x86_64 lockstep reference:
        movq (%rsi), %rax
        movq %rax, (%rdi)
        ret
`
	_, issues := Parse("ls.easm", src)
	// `src` is read in both bodies but is not in `reads`/`changes` -> frame violation in each.
	if !containsIssue(issues, "frame-read-outside-reads") {
		t.Fatalf("expected reference/target frame check (frame-read-outside-reads), got %#v", issues)
	}
}
