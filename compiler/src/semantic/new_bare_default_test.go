package semantic

import (
	"strings"
	"testing"
)

// Bare `new T(...)` (no `[...]`) defaults to region inference — identical to
// `new[auto] T(...)`. Inside an inferred region it compiles cleanly and yields a
// region-qualified reference.
func TestBareNewDefaultsToRegionInference(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bare_new_ok.elisa", `struct Box:
    value: i64
def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new Box{value: 7}
        return b.value
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("bare `new` must default to region inference and compile cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Returning a bare-`new` value makes the function region-polymorphic, exactly
// like `new[auto]` — the inferred region is threaded from the caller, so the
// result outlives the call.
func TestBareNewReturnIsRegionPolymorphic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bare_new_ret.elisa", `struct Box:
    value: i64
def f() -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new Box{value: 7}
        return b
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("returning a bare-`new` value must compile (region-polymorphic), got:\n%s", strings.Join(errs, "\n"))
	}
}

// `in auto:` has been removed now that allocations infer their region by
// default. A user-written block is a hard error pointing at the replacement.
func TestInAutoBlockIsRemoved(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "in_auto_removed.elisa", `def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            xs: mutable darray[i64] = []
            xs.push(7)
            return xs[0]
`, AnalyzeOptions{})
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "`in auto:` is no longer supported") {
		t.Fatalf("expected `in auto:` to be rejected as removed, got:\n%s", all)
	}
}

// The compiler's OWN synthesized auto-regions (the implicit function-body wrap
// around bare allocations) must NOT trip the `in auto:` removal error — only
// user-written `in auto:` blocks do. A bare-allocating function with no explicit
// region is the idiomatic form and must stay clean.
func TestImplicitAutoRegionNotRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "implicit_auto_clean.elisa", `def build() -> darray[i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        return xs
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("the implicit function-body auto-region must analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}
