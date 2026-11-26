package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS: a value narrowed with `if make() is found:` inside a `region` block lives in that
// block's region exactly like `found = get make() else ...` does. Returning it must be rejected;
// the `if … is` spelling used to compile and return freed storage (elisa-pkg's lock resolution
// read an empty failure code).
func TestConditionBinderReturnedFromRegionBlockRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "condition_binder_region.elisa", `def failure(code: u8) -> darray[u8]? can[Memory.Allocate]:
    out: mutable darray[u8] = []
    out.push(code)
    return out

def narrowed(code: u8) -> darray[u8]? can[Memory.Allocate, Memory.Release]:
    region scope:
        if failure(code) is found:
            return found
    return null

def unwrapped(code: u8) -> darray[u8]? can[Memory.Allocate, Memory.Release]:
    region scope:
        found = get failure(code) else return null
        return found
    return null
`)
	joined := strings.Join(result.Errors(), "\n")
	for _, line := range []string{":9:", ":15:"} {
		if !strings.Contains(joined, "condition_binder_region.elisa"+line) || !strings.Contains(joined, "escapes via return") {
			t.Errorf("want the return on line %s rejected, got:\n%s", strings.Trim(line, ":"), joined)
		}
	}
}

// Without the region block the same function is fine: the value lives in the caller's region.
func TestConditionBinderReturnedWithoutRegionAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "condition_binder_plain.elisa", `def failure(code: u8) -> darray[u8]? can[Memory.Allocate]:
    out: mutable darray[u8] = []
    out.push(code)
    return out

def narrowed(code: u8) -> darray[u8]? can[Memory.Allocate]:
    if failure(code) is found:
        return found
    return null
`)
	if joined := strings.Join(result.Errors(), "\n"); strings.Contains(joined, "escapes") {
		t.Fatalf("want no escape without a region block, got:\n%s", joined)
	}
}
