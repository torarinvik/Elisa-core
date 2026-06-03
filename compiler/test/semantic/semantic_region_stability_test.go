package semantic_test

import (
	"strings"
	"testing"
)

// A darray backed by a reserve_commit region never relocates on growth, so an
// interior reference into it stays valid across push (docs/68 §4).
func TestAnalyzeAcceptsInteriorRefIntoReserveCommitDarrayAcrossPush(t *testing.T) {
	src := `def f() -> i32:
	region big(1024) using reserve_commit
	xs: mutable darray[i32] @big = []
	xs.push(100)
	e0: i32& = &xs[0]
	xs.push(1)
	result: i32 = e0[0]
	destroy big
	return result
`
	_, errs := parseAndAnalyze(t, "reserve_commit_stable_interior_ref_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected interior ref into a reserve_commit darray to survive push, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A fixed-backed darray is also single-block and grows in place (until it overflows
// and panics), so an interior reference into it likewise survives push (docs/68 §4).
func TestAnalyzeAcceptsInteriorRefIntoFixedDarrayAcrossPush(t *testing.T) {
	src := `def f() -> i32:
	region buf(1024) using fixed
	xs: mutable darray[i32] @buf = []
	xs.push(100)
	e0: i32& = &xs[0]
	xs.push(1)
	result: i32 = e0[0]
	destroy buf
	return result
`
	_, errs := parseAndAnalyze(t, "fixed_stable_interior_ref_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected interior ref into a fixed darray to survive push, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The same code in a chained (relocatable) region must reject the stale interior
// reference after growth.
func TestAnalyzeRejectsInteriorRefIntoChainedDarrayAcrossPush(t *testing.T) {
	src := `def f() -> i32:
	region tmp(1024) using chained
	xs: mutable darray[i32] @tmp = []
	xs.push(100)
	e0: i32& = &xs[0]
	xs.push(1)
	result: i32 = e0[0]
	destroy tmp
	return result
`
	_, errs := parseAndAnalyze(t, "chained_stale_interior_ref_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "storage dependency facts were invalidated") {
		t.Fatalf("expected a stale interior-ref diagnostic for a chained darray, got:\n%s", strings.Join(errs, "\n"))
	}
}
