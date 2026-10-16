//go:build cgo

package semantic

import "testing"

const smtLiteralLocalPrelude = `
const BAD: u32 = 7

struct DeclId:
    value: u32

struct BindingId:
    owner: DeclId
    serial: u32
`

// `result.binding_id.owner.value` for a returned `(false, missing)`, where `missing` is an immutable
// local built from a struct literal, reads the literal's component.
func TestSMTLiteralLocalDeepProjection(t *testing.T) {
	src := `
def lookup(x: i64) -> (known: bool, binding_id: BindingId):
    ensure not result.known implies result.binding_id.owner.value == BAD
    ensure not result.known implies result.binding_id.serial == 1
    missing: BindingId = BindingId{owner: DeclId{value: BAD}, serial: 1}
    return (false, missing) if x < 0
    return (true, BindingId{owner: DeclId{value: 3}, serial: 2})
`
	result := analyzeContractStrict(t, "lookup.elisa", smtLiteralLocalPrelude+src)
	if n := ensureFailuresFor(result.Errors(), "lookup"); n != 0 {
		t.Fatalf("expected the literal projections to prove, got: %v", result.Errors())
	}
	falseCases := map[string]string{
		"lookup_false": `
def lookup_false(x: i64) -> (known: bool, binding_id: BindingId):
    ensure not result.known implies result.binding_id.serial == 2
    missing: BindingId = BindingId{owner: DeclId{value: BAD}, serial: 1}
    return (false, missing) if x < 0
    return (true, BindingId{owner: DeclId{value: 3}, serial: 2})
`,
		// The literal captured s before the write; re-reading s afterwards would be wrong.
		"lookup_stale": `
def lookup_stale(x: u32) -> (known: bool, binding_id: BindingId):
    requires x < 100
    ensure result.binding_id.serial == x + 1
    s: mutable u32 = x
    missing: BindingId = BindingId{owner: DeclId{value: BAD}, serial: s}
    s <- s + 1
    return (false, missing)
`,
	}
	for name, src := range falseCases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", smtLiteralLocalPrelude+src)
			if ensureFailuresFor(result.Errors(), name) == 0 {
				t.Fatalf("%s: the ensure is false and must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}
