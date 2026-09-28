package semantic

import (
	"strings"
	"testing"
)

const wholeAssignmentStale = "storage dependency facts were invalidated by reassignment of"

// Replacing a container as a whole (`xs <- [...]`) points its header at a different buffer, so an
// interior reference taken before the store no longer aliases the container. Before, both the
// parameter shape (a store through `mutable darray&`, which the caller sees) and the local shape
// compiled and read the old element (70) instead of the new one.
func TestDarrayInteriorRefInvalidatedByWholeAssignment(t *testing.T) {
	cases := map[string]string{
		"param": `def f(xs: mutable darray[i64]&) -> i64:
    r: i64& = &xs[0]
    xs <- [1, 2, 3]
    return r
`,
		// A local darray gets a reserve_commit backing whose base never moves on growth; that
		// proof must not excuse a replacement.
		"local": `def f() -> i64:
    mutable xs: darray[i64] = [70, 2]
    r: i64& = &xs[0]
    xs <- [1, 2, 3]
    return r
`,
		"struct": `struct Bag:
    items: darray[i64]
    n: i64

def f() -> i64:
    mutable bag: Bag = Bag{items: [70, 2], n: 1}
    r: i64& = &bag.items[0]
    bag <- Bag{items: [1], n: 2}
    return r
`,
		"branch": `def f(c: bool) -> i64:
    mutable xs: darray[i64] = [70, 2]
    r: i64& = &xs[0]
    if c:
        xs <- [1, 2, 3]
    return r
`,
	}
	for name, source := range cases {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "whole_assignment_"+name+".elisa", source, AnalyzeOptions{})
		if !strings.Contains(allDiagnostics(result), wholeAssignmentStale) {
			t.Fatalf("%s: expected the interior ref used after a whole assignment to be rejected, got:\n%s", name, allDiagnostics(result))
		}
	}
	iterated := `def f() -> i64:
    mutable xs: darray[i64] = [70, 2]
    mutable total: i64 = 0
    for v in xs:
        total <- total + v
        xs <- [1]
    return total
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "whole_assignment_iterated.elisa", iterated, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "while it is being iterated: reassignment of xs") {
		t.Fatalf("expected replacing an iterated darray to be rejected, got:\n%s", allDiagnostics(result))
	}
}

// What a whole assignment does NOT invalidate: a scalar copied out before it, a reference taken
// after it, a reference rebound after it, and a rebind of a darray REFERENCE slot (which leaves
// the old referent untouched).
func TestDarrayInteriorRefSurvivesUnrelatedAssignment(t *testing.T) {
	source := `def scalar_copy() -> i64:
    mutable xs: darray[i64] = [70, 2]
    r: i64& = &xs[0]
    t: i64 = r
    xs <- [1, 2, 3]
    return t

def taken_after() -> i64:
    mutable xs: darray[i64] = [70, 2]
    xs <- [1, 2, 3]
    r: i64& = &xs[0]
    return r

def rebound_after() -> i64:
    mutable xs: darray[i64] = [70, 2]
    mutable r: i64& = &xs[0]
    xs <- [1, 2, 3]
    r <- &xs[1]
    return r

def ref_slot_rebind(xs: mutable darray[i64]&, zs: mutable darray[i64]&) -> i64:
    r: i64& = &xs[0]
    mutable ys: mutable darray[i64]& = xs
    ys <- zs
    return r

def self_filter(consumed: mutable darray[sview]&, name: sview) -> usize:
    consumed <- [held for held in consumed if held != name]
    consumed <- [held for held in consumed if held != name]
    return consumed.count

struct Holder:
    p: u8&
    n: usize

def build(p: u8&, n: usize) -> Holder:
    return Holder{p: p, n: n}

def value_built_from() -> usize:
    mutable buf: darray[u8] = [1, 2, 3]
    mutable h: Holder = build(&buf[0], buf.count)
    view: u8& = &buf[0]
    h <- build(&buf[1], 1)
    return view.usize() + h.n

struct Unit:
    items: darray[u8]
    first: u8

def parse_unit(p: u8&, n: usize) -> Unit:
    mutable items: darray[u8] = []
    items.push(p)
    return Unit{items: items, first: p}

def value_rebuilt_twice(file: Unit) -> u8:
    compile_file: mutable Unit = file
    expanded: mutable darray[u8] = [1, 2, 3]
    expanded_ptr: mutable u8& = &expanded[0]
    expanded_file: mutable Unit = parse_unit(expanded_ptr, expanded.count)
    compile_file <- expanded_file
    compile_file <- file
    return expanded_ptr + compile_file.first
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "whole_assignment_survives.elisa", source, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "storage dependency facts were invalidated") {
		t.Fatalf("expected these references to stay valid, got:\n%s", allDiagnostics(result))
	}
}
