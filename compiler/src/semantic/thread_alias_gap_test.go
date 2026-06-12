package semantic

import (
	"strings"
	"testing"
)

// The thread-share safety net keys on `pool_submit1` (the lowering target of the
// safe `submit` sugar); raw `spawn1` was removed from the public surface, so
// these tests drive the same checker branch through `submit[&pool] worker(arg)`.
const threadSubmitPrelude = `def pool_submit1[A, R](pool: mutable ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
    return zeroed
`

// Regression: a bare growable container (darray) passed by value as a thread data
// argument copies a header that still aliases the shared backing buffer — it is NOT
// structurally shareable, so the submit path must reject it (parity with the
// closure-capture path, which already rejects a captured darray header).
func TestSpawnRejectsBareDarrayDataArg(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "thread_darray_arg.elisa", threadSubmitPrelude+`def dworker(xs: darray[i64]) -> i64:
    return xs[0]
def start(xs: darray[i64]) -> i64:
    pool: mutable ThreadPool = zeroed
    _ = submit[&pool] dworker(xs)
    return xs[0]
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "not structurally shareable") {
		t.Fatalf("expected bare darray thread arg to be rejected as unshareable; got:\n%s", all)
	}
}

// Regression: a closure capturing a GENERIC struct whose (substituted) field is a
// darray shares that backing across threads exactly as a non-generic struct does.
// The generic and non-generic capture paths must agree (both rejected).
func TestSpawnRejectsGenericStructCaptureWithContainerField(t *testing.T) {
	generic := analyzeTreeTestSourceWithSemanticErrors(t, "cap_generic.elisa", threadSubmitPrelude+`struct Holder[T]:
    items: darray[T]
def start(h: Holder[i64]) -> Task[i64, Pending]:
    pool: mutable ThreadPool = zeroed
    return submit[&pool] (lambda (ignored: i64) => h.items[0])(0)
`)
	if all := strings.Join(generic.Errors(), "\n"); !strings.Contains(all, "captures state that cannot be shared") {
		t.Fatalf("expected generic-struct-with-darray capture to be rejected; got:\n%s", all)
	}
	nonGeneric := analyzeTreeTestSourceWithSemanticErrors(t, "cap_plain.elisa", threadSubmitPrelude+`struct HolderC:
    items: darray[i64]
def start(h: HolderC) -> Task[i64, Pending]:
    pool: mutable ThreadPool = zeroed
    return submit[&pool] (lambda (ignored: i64) => h.items[0])(0)
`)
	if all := strings.Join(nonGeneric.Errors(), "\n"); !strings.Contains(all, "captures state that cannot be shared") {
		t.Fatalf("expected non-generic-struct-with-darray capture to be rejected (parity); got:\n%s", all)
	}
}

// Regression: overlapping whole-object vs field mutable borrows alias the same
// storage even though their root strings differ — they must infer Unsafe.Alias.
// Covers both live local bindings and overlapping call arguments; disjoint fields
// must NOT be flagged.
func TestOverlappingMutableBorrowsInferUnsafeAlias(t *testing.T) {
	local := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "alias_overlap_local.elisa", `struct Box:
    value: i64
def f(b: mutable Box&) -> void:
    whole: mutable Box& = &b
    part: mutable i64& = &b.value
    whole.value <- 1
    part <- 2
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(local), "Alias") {
		t.Fatalf("expected overlapping local borrows to infer Unsafe.Alias; got:\n%s", allDiagnostics(local))
	}

	callArg := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "alias_overlap_call.elisa", `struct Box:
    value: i64
def update(whole: mutable Box&, part: mutable i64&) -> void:
    return
def f(b: mutable Box&) -> void:
    update(&b, &b.value)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(callArg), "Alias") {
		t.Fatalf("expected overlapping call args to infer Unsafe.Alias; got:\n%s", allDiagnostics(callArg))
	}

	disjoint := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "alias_overlap_disjoint.elisa", `struct Pair:
    x: i64
    y: i64
def update(a: mutable i64&, b: mutable i64&) -> void:
    return
def f(p: mutable Pair&) -> void:
    update(&p.x, &p.y)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(disjoint), "Alias") {
		t.Fatalf("disjoint field borrows must NOT infer Unsafe.Alias; got:\n%s", allDiagnostics(disjoint))
	}
}
