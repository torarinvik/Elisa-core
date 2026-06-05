package semantic

import (
	"strings"
	"testing"
)

// new[auto] heap-allocates a struct into the innermost active inferred region (the native stack
// arena), no explicit region/pool. Inside an `in auto:` it compiles cleanly and the value is used
// within the region.
func TestNewAutoAllocatesInInferredRegionCleanly(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "na_ok.elisa", `struct Box:
    value: i64
def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            b: Box& = new[auto] Box(7)
            return b.value
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("new[auto] inside an in-auto region must compile cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Soundness: a new[auto] reference carries the inferred region's provenance, so letting it escape
// that region (returning it) is rejected exactly like an explicit new[region] escape.
func TestNewAutoEscapeRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "na_esc.elisa", `struct Box:
    value: i64
def f() -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            b: Box& = new[auto] Box(7)
            return b
`, AnalyzeOptions{})
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot return reference") {
		t.Fatalf("a new[auto] reference escaping its region must be rejected, got:\n%s", all)
	}
}

// new[auto] is inference-driven like a container: a function that uses it WITHOUT any explicit
// `in auto:` has the region synthesized for it automatically (the lifetime is detected and the
// allocation placed in the matching inferred region). So it compiles cleanly — no "undefined
// identifier auto", no "needs a region".
func TestNewAutoInfersRegionWithoutInAuto(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "na_inf.elisa", `struct Box:
    value: i64
def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: Box& = new[auto] Box(7)
        return b.value
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("new[auto] must work without an explicit in-auto (region synthesized by inference), got:\n%s", strings.Join(errs, "\n"))
	}
}
