//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// WP (#5, calls): a body that binds a refined helper's result (`y := clamp_byte(x)`, clamp_byte returns
// Bounded[0,255]) can use that contract in WP transport. `return y + 1` proves `ensure result <= 256`
// and `>= 1` from y ∈ [0,255]. Sound: the callee enforces its return refinement on every exit.
func TestWPAssumesRefinedCallResult(t *testing.T) {
	hdr := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp_byte(v: i64) -> i64 is Bounded[0, 255]:
    if v < 0:
        return 0
    if v > 255:
        return 255
    return v
`
	for _, tc := range []struct{ name, ensure string }{
		{"upper", "result <= 256"},
		{"lower", "result >= 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := hdr + "def f(x: i64) -> i64:\n    ensure " + tc.ensure + "\n    y: i64 = clamp_byte(x)\n    return y + 1\n"
			if errs := analyzeContractStrict(t, "wpcall_"+tc.name+".elisa", src).Errors(); len(errs) != 0 {
				t.Fatalf("y in [0,255] so y+1 satisfies %q; WP should prove it, got: %v", tc.ensure, errs)
			}
		})
	}
}

// SOUNDNESS: an over-tight bound (y+1 can reach 256) must decline, and a call WITHOUT a return
// refinement gives WP nothing to assume — also declines.
func TestWPRefinedCallSoundness(t *testing.T) {
	cases := []struct{ name, src string }{
		{"too_tight", `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp_byte(v: i64) -> i64 is Bounded[0, 255]:
    if v < 0:
        return 0
    if v > 255:
        return 255
    return v
def f(x: i64) -> i64:
    ensure result <= 100
    y: i64 = clamp_byte(x)
    return y + 1
`},
		{"unrefined_helper", `
def helper(x: i64) -> i64:
    return x
def f(x: i64) -> i64:
    ensure result <= 256
    y: i64 = helper(x)
    return y + 1
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(analyzeContractStrict(t, "wpcallbad_"+tc.name+".elisa", tc.src).Errors(), "\n")
			if !strings.Contains(errs, "could not be proven statically") {
				t.Fatalf("%s must decline, but it was accepted", tc.name)
			}
		})
	}
}
