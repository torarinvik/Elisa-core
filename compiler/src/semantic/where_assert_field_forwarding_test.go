//go:build cgo

package semantic

import "testing"

// A live assertion over a field place must discharge the identical where obligation.  The fact is
// mutation-tracked by smtAssertFacts, so accepting it is as sound as accepting the assertion itself.
func TestWhereStructConstructionUsesIdenticalAssertedFieldFacts(t *testing.T) {
	src := `
struct Source:
    n: i32
struct Target:
    n: i32 where n >= 1 and n <= 8
def copy(source: Source) -> Target:
    assert source.n >= 1 and source.n <= 8
    return Target{n: source.n}
`
	if errs := analyzeContractStrict(t, "where_assert_field_forwarding.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("identical asserted field facts should discharge struct field where refinement, got: %v", errs)
	}
}
