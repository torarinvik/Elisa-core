package semantic_test

import (
	"strings"
	"testing"
)

// Container `@r` provenance is now tracked like a `new[r]` borrow: using a darray
// after its region is destroyed is a use-after-free and must be rejected (it was
// silently accepted before container deps were threaded into the flow analysis).
func TestAnalyzeRejectsDarrayUseAfterRegionDestroy(t *testing.T) {
	src := `def f() -> i64:
	region scratch(1024)
	xs: mutable darray[i64] @scratch = []
	xs.push(7)
	destroy scratch
	return xs[0]
`
	_, errs := parseAndAnalyze(t, "darray_use_after_destroy_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "region dependency facts were invalidated") {
		t.Fatalf("expected a use-after-destroy diagnostic for a darray, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The `@r` suffix on a generic user type (`Box[i64] @r`, docs/68 §5) carries region
// provenance like a container does, so using the value after its region is destroyed
// is rejected.
func TestAnalyzeRejectsGenericRegionValueUseAfterDestroy(t *testing.T) {
	src := `struct Box[T]:
	value: T

def f() -> i64:
	region scratch(1024)
	b: Box[i64] @scratch = Box[i64]{value: 7}
	destroy scratch
	return b.value
`
	_, errs := parseAndAnalyze(t, "generic_region_use_after_destroy_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "region dependency facts were invalidated") {
		t.Fatalf("expected a use-after-destroy diagnostic for a @r-annotated generic, got:\n%s", strings.Join(errs, "\n"))
	}
}
