//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func invariantFailures(errs []string) int {
	n := 0
	for _, e := range errs {
		if strings.Contains(e, "invariant could not be proven") {
			n++
		}
	}
	return n
}

// A range loop's index lies in [start, end) at the top of every iteration, so an in-body invariant
// over that range proves — for a parameter bound, a container count by value and by immutable
// reference, and a non-integer element container.
func TestSMTForRangeFactProvesIndexInvariants(t *testing.T) {
	src := `
def by_n(n: usize) -> usize:
    total: mutable usize = 0
    for index in 0..<n |index|:
        invariant index <= n
        invariant index < n
        total <- index
    return total

def by_count(names: darray[i64]) -> usize:
    total: mutable usize = 0
    for index in 0..<names.count |index|:
        invariant index <= names.count
        total <- index
    return total

def by_ref_views(names: darray[sview]&) -> usize:
    total: mutable usize = 0
    for index in 1..<names.count |index|:
        invariant 1 <= index and index < names.count
        total <- index
    return total
`
	result := analyzeContractStrict(t, "for_range_fact.elisa", src)
	if n := invariantFailures(result.Errors()); n != 0 {
		t.Fatalf("expected every range invariant to prove; got %d: %v", n, result.Errors())
	}
}

// SOUNDNESS: the body is analyzed once, so the range fact is assumed at the top of every
// iteration and is only valid when the bounds cannot change during the loop.
//   - mutated_end: `n` is reassigned in the body, so `index < n` fails on the next iteration;
//   - stepped: `0..<10..3` visits 9, so `index < 9` is false (steps are not modeled);
//   - field_end: a struct field of a mutable binding is not a stable bound.
func TestSMTForRangeFactRequiresStableBounds(t *testing.T) {
	cases := map[string]string{
		"mutated_end": `
def mutated_end(m: usize) -> usize:
    n: mutable usize = m
    total: mutable usize = 0
    for index in 0..<n |index, n|:
        invariant index < n
        n <- 0
        total <- index
    return total
`,
		"stepped": `
def stepped() -> usize:
    total: mutable usize = 0
    for index in 0..<10..3 |index|:
        invariant index < 9
        total <- index
    return total
`,
		"field_end": `
struct Box:
    n: mutable usize

def field_end(b0: Box) -> usize:
    b: mutable Box = b0
    total: mutable usize = 0
    for index in 0..<b.n |index, b|:
        invariant index < b.n
        b.n <- 0
        total <- index
    return total
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if invariantFailures(result.Errors()) == 0 {
				t.Fatalf("%s: invariant must stay unproven; got: %v", name, result.Errors())
			}
		})
	}
}

// SOUNDNESS: an in-body invariant is proven BEFORE its condition is assumed. Assuming first let any
// invariant over an immutable value prove itself (`for i in 0..<10: invariant i < 3`, and
// `y: i64 = x; invariant y > 5`), and the unproven invariant then discharged a later ensure.
// The stepped case above (`0..<10..3` visits 9; `invariant index < 9`) was the same defect.
func TestSMTInBodyInvariantDoesNotProveItself(t *testing.T) {
	src := `
def circular_index() -> usize:
    total: mutable usize = 0
    for index in 0..<10 |index|:
        invariant index < 3
        total <- index
    return total

def downstream(x: i64) -> i64:
    ensure result > 5
    y: i64 = x
    invariant y > 5
    return y
`
	result := analyzeContractStrict(t, "circular_invariant.elisa", src)
	if n := invariantFailures(result.Errors()); n != 2 {
		t.Fatalf("both false invariants must stay unproven; got %d: %v", n, result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "downstream") == 0 {
		t.Fatalf("an unproven invariant must not discharge a later ensure; got: %v", result.Errors())
	}
}
