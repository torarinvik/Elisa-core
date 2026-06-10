package semantic_test

import (
	"strings"
	"testing"
)

// `in auto:` is region inference: the compiler synthesizes a scoped region for the
// block's allocations and frees it at block exit. A value that stays inside the block
// (only a plain i64 is returned here) is fine — no explicit region needed.
func TestAnalyzeAcceptsInAutoBlockWithNonEscapingValue(t *testing.T) {
	src := `def f() -> i64:
	in auto:
		xs: mutable darray[i64] = []
		xs.push(7)
		xs.push(8)
		return xs[0] + xs[1]
`
	_, errs := parseAndAnalyze(t, "in_auto_non_escaping_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected `in auto:` with a non-escaping value to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Returning a value built in an `in auto:` block no longer leaks or errors:
// region-return inference makes the function region-polymorphic, threading the
// synthesized region out to the caller instead of freeing it at block exit.
func TestAnalyzeAcceptsRegionPolymorphicReturnFromInAutoBlock(t *testing.T) {
	src := `def f() -> darray[i64]:
	in auto:
		xs: mutable darray[i64] = []
		xs.push(7)
		return xs
`
	_, errs := parseAndAnalyze(t, "in_auto_return_region_poly.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected build-and-return from `in auto:` to be accepted as region-polymorphic, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The safety boundary moves to the call site: a region-polymorphic function
// (one that returns a region-threaded value) may only be called from inside an
// inferred region, so the threaded region always has an owner.
func TestAnalyzeRejectsRegionPolymorphicCallOutsideInferredRegion(t *testing.T) {
	src := `def build() -> darray[i64]:
	in auto:
		xs: mutable darray[i64] = []
		xs.push(7)
		return xs

def use() -> i64:
	ys: darray[i64] = build()
	return ys[0]
`
	_, errs := parseAndAnalyze(t, "region_poly_call_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "region-polymorphic") {
		t.Fatalf("expected calling a region-polymorphic function outside an inferred region to be rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}

// `@r` is only meaningful on types that carry a region (containers, references,
// region-parameterized generics). On a type that can't — a fixed-size array, which is
// stack/inline — it's rejected, so the notation means one thing everywhere rather than
// being silently dropped.
func TestAnalyzeRejectsRegionAnnotationOnNonRegionType(t *testing.T) {
	src := `def f() -> i64:
	region r(64)
	xs: array[i64, 4] @r = zeroed
	destroy r
	return xs[0]
`
	_, errs := parseAndAnalyze(t, "region_on_array_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "cannot carry a region") {
		t.Fatalf("expected `@r` on a fixed array to be rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}
