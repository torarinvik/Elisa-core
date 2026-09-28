package semantic

import (
	"strings"
	"testing"
)

const calleeGrowthStale = "storage dependency facts were invalidated by mutable borrow of"

// A callee that grows a darray through a `mutable darray&` parameter relocates the buffer the
// caller's interior reference points into. The callee is analyzed in its own scope, so the call
// site must invalidate the reference: before, `grow(&xs)` left `r` dangling (a runtime probe read
// a freed-buffer value instead of the element).
func TestDarrayInteriorRefInvalidatedByCalleeGrowth(t *testing.T) {
	cases := map[string]string{
		"statement": `def grow(ys: mutable darray[i64]&) -> void:
    ys.push(9)

def f() -> i64:
    mutable xs: darray[i64] = [1, 2]
    r: i64& = &xs[1]
    grow(&xs)
    return r
`,
		"initializer": `def grow_count(ys: mutable darray[i64]&) -> i64:
    ys.push(9)
    return ys.count.i64()

def f(xs: mutable darray[i64]&) -> i64:
    r: i64& = &xs[1]
    n: i64 = grow_count(xs)
    return r + n
`,
		"alias": `def grow(ys: mutable darray[i64]&) -> void:
    ys.push(9)

def f(xs: mutable darray[i64]&) -> i64:
    r: i64& = &xs[1]
    ys: mutable darray[i64]& = xs
    grow(ys)
    return r
`,
		"struct": `struct Bag:
    items: mutable darray[i64]

def grow_bag(bag: mutable Bag&) -> void:
    bag.items.push(9)

def f() -> i64:
    mutable bag: Bag = Bag{items: [1, 2]}
    r: i64& = &bag.items[1]
    grow_bag(&bag)
    return r
`,
	}
	for name, source := range cases {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "callee_growth_"+name+".elisa", source, AnalyzeOptions{})
		if !strings.Contains(allDiagnostics(result), calleeGrowthStale) {
			t.Fatalf("%s: expected the interior ref used after callee growth to be rejected, got:\n%s", name, allDiagnostics(result))
		}
	}
}

// Borrows that cannot relocate the container stay valid: a scalar `mutable i64&` element, a
// read-only darray borrow, and a mutable borrow of a DIFFERENT container.
func TestDarrayInteriorRefSurvivesNonRelocatingCalls(t *testing.T) {
	source := `def bump(value: mutable i64&) -> void:
    value <- value + 1

def total(ys: darray[i64]&) -> i64:
    return ys.count.i64()

def grow(ys: mutable darray[i64]&) -> void:
    ys.push(9)

def scalar_borrow(xs: mutable darray[i64]&) -> i64:
    r: i64& = &xs[1]
    bump(&xs[0])
    return r

def readonly_borrow(xs: mutable darray[i64]&) -> i64:
    r: i64& = &xs[1]
    n: i64 = total(xs)
    return r + n

def derived_copy(xs: mutable darray[sview]&) -> usize:
    copy: darray[sview] = [entry for entry in xs]
    grow_names(xs)
    return copy.count

def grow_names(ys: mutable darray[sview]&) -> void:
    ys.push("x")

def other_container(xs: mutable darray[i64]&, ys: mutable darray[i64]&) -> i64:
    r: i64& = &xs[1]
    grow(ys)
    return r
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "callee_no_growth.elisa", source, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "storage dependency facts were invalidated") {
		t.Fatalf("expected non-relocating calls to keep the interior ref valid, got:\n%s", allDiagnostics(result))
	}
}
