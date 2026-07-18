//go:build cgo

package semantic

import "testing"

func TestWhereReturnForwardsDirectCallContract(t *testing.T) {
	src := `
def bounded(x: i32) -> i32 where result >= 1 and result <= 8:
    assert x >= 1 and x <= 8
    return x
def forward(x: i32) -> i32 where result >= 1 and result <= 8:
    return bounded(x)
`
	if errs := analyzeContractStrict(t, "where_return_forwarding.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("an identical direct-call where return contract should forward modularly, got: %v", errs)
	}
}
