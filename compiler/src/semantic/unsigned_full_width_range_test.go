//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestUnsignedFullWidthSaturatingIncrementContract(t *testing.T) {
	src := `
def probe(x: u64) -> u64:
    ensure result >= 1
    ensure result >= x
    ensure x >= 18446744073709551615u64 or result == x + 1
    ensure x < 18446744073709551615u64 or result == x
    return x if x >= 18446744073709551615u64
    return x + 1
`
	r := analyzeContractStrict(t, "u64_saturating_increment.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("unsigned maximum must not seed signed -1 bounds: %v", errs)
	}
}

func TestUnsignedFullWidthWrappedIncrementStillRejected(t *testing.T) {
	src := `
def probe(x: u64) -> u64:
    requires x == 18446744073709551615u64
    ensure result >= 1
    return x + 1
`
	r := analyzeContractStrict(t, "u64_wrapped_increment_negative.elisa", src)
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("u64 maximum plus one wraps to zero and must reject this contract: %v", r.Errors())
	}
}
