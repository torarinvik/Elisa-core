//go:build cgo

package semantic

import "testing"

const containerCountSpec = `
module M:
    public:
        ghost def cnt(xs: darray[sview]&, index: usize) -> i64:
            requires index <= xs.count
            decreases xs.count - index
            return 0 if index == xs.count
            return 1 + cnt(xs, index + 1.usize())
`

// Step 1 of the container theory: a terminating recursive spec over a darray parameter unfolds at
// a foreign call site (its length reads go through the caller's stable container base).
func TestContainerTheoryLengthUnfolding(t *testing.T) {
	src := containerCountSpec + `
        def one(xs: darray[sview]) -> bool:
            requires xs.count == 1
            ensure result == (cnt(xs, 0.usize()) == 1)
            return true

        def wrong_total(xs: darray[sview]) -> bool:
            requires xs.count == 1
            ensure result == (cnt(xs, 0.usize()) == 2)
            return true

        def distinct(xs: darray[sview], ys: darray[sview]) -> bool:
            requires xs.count == 1
            ensure result == (cnt(xs, 0.usize()) == cnt(ys, 0.usize()))
            return true
`
	result := analyzeContractStrict(t, "container_len.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "one"); n != 0 {
		t.Fatalf("one: expected unfolding proof, got %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "wrong_total") == 0 {
		t.Fatalf("wrong_total: a wrong count must stay unproven; got %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "distinct") == 0 {
		t.Fatalf("distinct: two containers must not share a value; got %v", result.Errors())
	}
}
