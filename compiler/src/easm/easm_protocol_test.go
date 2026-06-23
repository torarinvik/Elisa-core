package easm

import (
	"strings"
	"testing"
)

// Stage 2 of docs/101: `protocol` + per-platform impls selected by the build target. One
// source collapses the parallel common_x86_64 / common_aarch64 files; the impl that does not
// match the target is dropped BEFORE verification, so two impls may share a name without a
// duplicate-export clash, and an arch-mismatched instruction never reaches the verifier.

func bodyContainsOp(fn *Function, op string) bool {
	if fn == nil {
		return false
	}
	for _, inst := range fn.Instructions {
		if inst.Op == op {
			return true
		}
	}
	return false
}

const cpuHintSource = `module hints
target %TARGET%
protocol CpuHint:
    relax() -> void
export def relax() -> void abi c for x86_64:
    clobbers: cc, memory
    stack: unchanged
    control: returns
    requires: x86_64.sse.pause
    body:
        pause
        ret
export def relax() -> void abi c for aarch64:
    clobbers: memory
    stack: unchanged
    control: returns
    requires: aarch64.yield
    body:
        yield
        ret
`

func TestProtocolSelectsX86ImplOnX86Target(t *testing.T) {
	src := strings.Replace(cpuHintSource, "%TARGET%", "x86_64", 1)
	module, issues := Parse("hints.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected clean x86 selection, got %#v", issues)
	}
	if got := functionNames(module); len(got) != 1 || got[0] != "relax" {
		t.Fatalf("expected single relax export, got %v", got)
	}
	relax := variantNamed(module, "relax")
	if !bodyContainsOp(relax, "pause") || bodyContainsOp(relax, "yield") {
		t.Fatalf("x86 target selected the wrong impl: %#v", relax.Instructions)
	}
}

func TestProtocolSelectsAarch64ImplOnAarch64Target(t *testing.T) {
	src := strings.Replace(cpuHintSource, "%TARGET%", "aarch64", 1)
	module, issues := Parse("hints.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected clean aarch64 selection, got %#v", issues)
	}
	if got := functionNames(module); len(got) != 1 || got[0] != "relax" {
		t.Fatalf("expected single relax export, got %v", got)
	}
	relax := variantNamed(module, "relax")
	if !bodyContainsOp(relax, "yield") || bodyContainsOp(relax, "pause") {
		t.Fatalf("aarch64 target selected the wrong impl: %#v", relax.Instructions)
	}
}

func TestProtocolConformanceMismatchRejected(t *testing.T) {
	src := `module bad_hints
target x86_64
protocol CpuHint:
    relax() -> void
export def relax(x: u64) -> void abi c for x86_64:
    inputs: x = rdi
    clobbers: cc, memory
    stack: unchanged
    control: returns
    requires: x86_64.sse.pause
    body:
        pause
        ret
`
	_, issues := Parse("bad_hints.easm", src)
	if !containsIssue(issues, "protocol-conformance") {
		t.Fatalf("expected protocol-conformance, got %#v", issues)
	}
}

func TestProtocolTupleMatchesArchAndOs(t *testing.T) {
	src := `module tls
target x86_64-apple-darwin
protocol Tls:
    base() -> void
export def base() -> void abi c for (x86_64, darwin):
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        ret
export def base() -> void abi c for (aarch64, darwin):
    clobbers: memory
    stack: unchanged
    control: returns
    body:
        ret
`
	module, issues := Parse("tls.easm", src)
	if len(issues) != 0 {
		t.Fatalf("expected clean tuple selection, got %#v", issues)
	}
	if got := functionNames(module); len(got) != 1 || got[0] != "base" {
		t.Fatalf("expected single base export for the x86_64-darwin target, got %v", got)
	}
}
