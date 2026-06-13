package semantic

import (
	"strings"
	"testing"
)

// The genuine silent footgun: an ambient region lets the push COMPILE, so growth lands in the
// callee's local copy and is silently lost to the caller. The lint must flag it (no hard error
// here — only the warning).
func TestByValueGrownContainerParamWarns(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "byval_grown.elisa", `def fill(out: mutable darray[u8]) -> usize:
    region r(64):
        out.push(1u8)
    return out.count
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("by-value grown container with an ambient region should compile (silent loss), got errors: %v", errs)
	}
	warnings := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(warnings, `by-value darray parameter "out" is grown`) {
		t.Fatalf("expected by-value-grown-container warning for `out`, got:\n%s", warnings)
	}
}

// A by-reference param makes growth visible to the caller — correct usage, no warning (and the
// region is inferred, so it compiles cleanly).
func TestByRefGrownContainerParamNoWarn(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "byref_grown.elisa", `def fill(out: mutable darray[u8]&) -> void:
    out.push(1u8)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("by-ref grown container should compile via inferred region, got errors: %v", errs)
	}
	if warnings := strings.Join(result.Warnings(), "\n"); strings.Contains(warnings, "by-value darray parameter") {
		t.Fatalf("by-ref grown container should not warn, got:\n%s", warnings)
	}
}

// Build-and-return: the grown by-value copy reaches the caller via the return value, so nothing is
// lost — the lint must stay quiet for this common builder idiom.
func TestByValueGrownThenReturnedNoWarn(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "byval_grown_returned.elisa", `def build(seed: mutable darray[u8]) -> darray[u8]:
    region r(64):
        seed.push(1u8)
    return seed
`)
	if warnings := strings.Join(result.Warnings(), "\n"); strings.Contains(warnings, "by-value darray parameter") {
		t.Fatalf("build-and-return idiom should not warn, got:\n%s", warnings)
	}
}

// A read-only by-value container param (never grown) is fine — no warning.
func TestByValueReadOnlyContainerParamNoWarn(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "byval_readonly.elisa", `def total(items: darray[u8]) -> usize:
    return items.count
`)
	if warnings := strings.Join(result.Warnings(), "\n"); strings.Contains(warnings, "by-value darray parameter") {
		t.Fatalf("read-only by-value container should not warn, got:\n%s", warnings)
	}
}
