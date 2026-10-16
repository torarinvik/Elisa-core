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

// Step 2: view elements of a stable container are read through per-base element functions, so a
// recursive membership spec unfolds over `xs[index] == w`. Negatives: another container, a
// different index, a read past count (never an in-bounds fact) and a mutable container.
func TestContainerTheoryViewElements(t *testing.T) {
	src := `module M:
    public:
        ghost def pres(xs: darray[sview]&, w: sview, index: usize) -> bool:
            requires index <= xs.count
            decreases xs.count - index
            return false if index == xs.count
            return true if xs[index] == w
            return pres(xs, w, index + 1.usize())

        def two(xs: darray[sview], w: sview) -> bool:
            requires xs.count == 1 and xs[0] == w
            ensure result == pres(xs, w, 0.usize())
            return true

        def later(xs: darray[sview], w: sview) -> bool:
            requires xs.count == 2 and xs[1] == w
            ensure result == pres(xs, w, 0.usize())
            return true

        def other(xs: darray[sview], ys: darray[sview], w: sview) -> bool:
            requires xs.count == 1 and xs[0] == w and ys.count == 1
            ensure result == pres(ys, w, 0.usize())
            return true

        def wrongidx(xs: darray[sview], w: sview) -> bool:
            requires xs.count == 2 and xs[1] == w
            ensure result == (xs[0] == w)
            return true

        def past(xs: darray[sview], w: sview) -> bool:
            requires xs.count == 1 and xs[3] == w
            ensure result == (xs.count > 3)
            return true

        def mutated(xs: mutable darray[sview], w: sview) -> bool:
            requires xs.count == 1 and xs[0] == w
            ensure result == pres(xs, w, 0.usize())
            xs[0] <- "zz"
            return true
`
	result := analyzeContractStrict(t, "container_elems.elisa", src)
	for _, fn := range []string{"two", "later"} {
		if n := ensureFailuresFor(result.Errors(), fn); n != 0 {
			t.Fatalf("%s: expected element unfolding proof, got %v", fn, result.Errors())
		}
	}
	for _, fn := range []string{"other", "wrongidx", "past", "mutated"} {
		if ensureFailuresFor(result.Errors(), fn) == 0 {
			t.Fatalf("%s: must stay unproven; got %v", fn, result.Errors())
		}
	}
}

// Step 3: a membership scan with an inductive invariant against the recursive spec. Ghost bool
// predicates over a stable container and a view are congruent across frames, and a loop without
// `break` exports inv ∧ ¬cond (a `return` leaves the function). Negatives: the negated spec, and
// a loop that can `break` (no exit facts: after a break the guard may still hold).
func TestContainerTheoryMembershipLoop(t *testing.T) {
	src := `module P:
    public:
        ghost def name_present_from(names: darray[sview]&, wanted: sview, index: usize) -> bool:
            requires index <= names.count
            decreases names.count - index
            return false if index == names.count
            return true if names[index] == wanted
            return name_present_from(names, wanted, index + 1.usize())

        ghost def name_present_spec(names: darray[sview]&, wanted: sview) -> bool:
            return name_present_from(names, wanted, 0)

        def name_present(names: darray[sview]&, wanted: sview) -> bool:
            ensure result == name_present_spec(names, wanted)
            index: mutable usize = 0
            while index < names.count |index|:
                decreases names.count - index
                invariant index <= names.count
                invariant name_present_from(names, wanted, index) == name_present_from(names, wanted, 0)
                if names[index] == wanted:
                    return true
                index <- index + 1
            return false

        def wrong(names: darray[sview]&, wanted: sview) -> bool:
            ensure result == (not name_present_spec(names, wanted))
            index: mutable usize = 0
            while index < names.count |index|:
                decreases names.count - index
                invariant index <= names.count
                invariant name_present_from(names, wanted, index) == name_present_from(names, wanted, 0)
                if names[index] == wanted:
                    return true
                index <- index + 1
            return false

        def broke(n: i64) -> i64:
            requires n > 5 and n < 100
            ensure result == n
            i: mutable i64 = 0
            while i < n |i|:
                decreases n - i
                invariant i <= n
                if i == 3:
                    break
                i <- i + 1
            return i
`
	result := analyzeContractStrict(t, "container_scan.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "name_present"); n != 0 {
		t.Fatalf("name_present: expected proof, got %v", result.Errors())
	}
	for _, fn := range []string{"wrong", "broke"} {
		if ensureFailuresFor(result.Errors(), fn) == 0 {
			t.Fatalf("%s: must stay unproven; got %v", fn, result.Errors())
		}
	}
}
