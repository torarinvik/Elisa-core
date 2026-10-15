//go:build cgo

package semantic

import "testing"

// SOUNDNESS: SMT symbols are keyed by spelling, so a binding that shadows another must not inherit
// the other's facts. Each case returns a value that violates its ensure for some input, and each
// was "proven" before this fix through a contradiction between two bindings sharing one symbol:
//   - guard_leak: the outer guard `y > 0` (a range fact and a flow fact on `v_y`) met the inner
//     `y = 0 - x`;
//   - guard_leak_param: the inner `x = -3` met the whole-body return tree over the PARAMETER x;
//   - self_reference: `y: i64 = y + 1` reads the OUTER y, but was asserted as `v_y = v_y + 1`.
func TestSMTShadowedBindingDoesNotInheritFacts(t *testing.T) {
	cases := map[string]string{
		"guard_leak": `
def guard_leak(x: i64) -> i64:
    ensure result > 0
    y: i64 = x
    if y > 0:
        y: i64 = 0 - x
        return y
    return 1
`,
		"guard_leak_param": `
def guard_leak_param(x: i64) -> i64:
    ensure result > 0
    if x > 0:
        x: i64 = -3
        return x
    return 1
`,
		"self_reference": `
def self_reference(x: i64) -> i64:
    requires x > 0 and x < 100
    ensure result == 0
    y: i64 = x
    if x > 1:
        y: i64 = y + 1
        return y
    return 0
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if ensureFailuresFor(result.Errors(), name) == 0 {
				t.Fatalf("%s: the ensure is false for some input and must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}

// Shadowing does not cost proofs that are about the binding in scope: facts stated about the
// current binding (its own definition, guards on it, facts on unshadowed names) still discharge.
func TestSMTShadowingKeepsFactsAboutCurrentBinding(t *testing.T) {
	src := `
def inner_guard(x: i64) -> i64:
    ensure result > 0
    y: i64 = x
    if x > 0:
        y: i64 = x + 0
        return 1 if y <= 0
        return y
    return 1

def unshadowed_guard(x: i64, z: i64) -> i64:
    ensure result > 0
    if z > 0:
        x: i64 = 4
        return z
    return 1

def sibling_reuse(x: i64) -> i64:
    requires x > 0 and x < 100
    ensure result > 0
    if x > 50:
        t: i64 = x - 50
        return t
    t: i64 = x
    return t
`
	result := analyzeContractStrict(t, "shadowing_keeps.elisa", src)
	for _, fn := range []string{"inner_guard", "unshadowed_guard", "sibling_reuse"} {
		if ensureFailuresFor(result.Errors(), fn) != 0 {
			t.Fatalf("%s must still prove; got: %v", fn, result.Errors())
		}
	}
}
