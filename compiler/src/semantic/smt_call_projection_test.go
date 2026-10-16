//go:build cgo

package semantic

import "testing"

const smtCallProjectionPrelude = `
struct P:
    valid: bool
    value: i64

def ref1(x: i64) -> P:
    ensure result.valid == (x > 0)
    ensure result.value >= 0
    return P{valid: x > 0, value: 0}
`

// A field read of an aggregate-valued call, or of an immutable local initialized by one, assumes the
// callee's ensures over that call's projections.
func TestSMTAggregateCallProjectionUsesCalleeEnsures(t *testing.T) {
	cases := map[string]string{
		"call_field": `
def call_field(x: i64) -> bool:
    ensure result == (x > 0)
    return ref1(x).valid
`,
		"call_field_pair": `
def call_field_pair(x: i64) -> P:
    ensure result.valid == ref1(x).valid
    return P{valid: x > 0, value: 0}
`,
		"call_local": `
def call_local(x: i64) -> i64:
    ensure result >= 0
    p: P = ref1(x)
    return p.value
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", smtCallProjectionPrelude+src)
			if n := ensureFailuresFor(result.Errors(), name); n != 0 {
				t.Fatalf("%s: expected the callee ensures to discharge the postcondition, got: %v", name, result.Errors())
			}
		})
	}
}

// NEGATIVE: false variants stay unproven; a callee's `result.f` never reads the CALLER's returned
// literal (that once turned `ensure not result.valid` into an inconsistent hypothesis); and a local
// whose call argument was written since no longer inherits ensures that mention the formal.
func TestSMTAggregateCallProjectionRejectsFalseVariants(t *testing.T) {
	cases := map[string]string{
		"call_field_false": `
def call_field_false(x: i64) -> bool:
    ensure result == (x < 0)
    return ref1(x).valid
`,
		"call_local_false": `
def call_local_false(x: i64) -> i64:
    ensure result >= 1
    p: P = ref1(x)
    return p.value
`,
		"caller_literal_leak": `
def never_valid(x: i64) -> P:
    ensure not result.valid
    return P{valid: false, value: x}

def caller_literal_leak(x: i64) -> P:
    ensure result.value == 5
    return P{valid: true, value: never_valid(x).value}
`,
		"stale_local_argument": `
def stale_local_argument(x0: i64) -> bool:
    ensure result == (x0 > 0)
    x: mutable i64 = x0
    p: P = ref1(x)
    x <- 0 - x
    return p.valid == (x > 0)
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", smtCallProjectionPrelude+src)
			if ensureFailuresFor(result.Errors(), name) == 0 {
				t.Fatalf("%s: the ensure is false for some input and must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}
