//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Dogfood-driven ergonomics: `return callee(...)` where the callee's RETURN TYPE already carries the
// required refinement is discharged by composition (the callee's contract guarantees it), eliding a
// redundant runtime check on the common forward/wrap pattern.
func TestReturnForwardsCalleeRefinement(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Byte = i64 is Bounded[0, 255]
def clamp_byte(v: i64) -> i64 is Bounded[0, 255]:
    if v < 0:
        return 0
    if v > 255:
        return 255
    return v
def blend(a: Byte, b: Byte, w: i64) -> i64 is Bounded[0, 255]:
    requires w >= 0
    requires w <= 256
    return (a * (256 - w) + b * w) / 256
def pipeline(x: i64, y: i64) -> i64 is Bounded[0, 255]:
    return blend(clamp_byte(x), clamp_byte(y), 128)
`
	result := analyzeContractStrict(t, "fwd_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("forwarding a Bounded[0,255] call result as a Bounded[0,255] return should prove, got: %v", errs)
	}
	if len(result.ReturnRefinementChecks) != 0 {
		t.Fatalf("the forwarded return should be statically discharged (no runtime check), got %d", len(result.ReturnRefinementChecks))
	}
}

// SOUNDNESS: forwarding a call whose return refinement does NOT entail the required one (a wider bound
// where a narrower is required) must still decline.
func TestReturnForwardingNonEntailingDeclines(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def widen(x: i64) -> i64 is Bounded[0, 1000]:
    requires x >= 0
    requires x <= 1000
    return x
def narrow(x: i64) -> i64 is Bounded[0, 255]:
    requires x >= 0
    requires x <= 1000
    return widen(x)
`
	errs := strings.Join(analyzeContractStrict(t, "fwd_bad.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("Bounded[0,1000] does not entail Bounded[0,255]; the forward must decline, got: %v", errs)
	}
}
