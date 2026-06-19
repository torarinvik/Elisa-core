package semantic

import (
	"strings"
	"testing"
)

// A FORWARDED reference (a loaded ref value, e.g. a `mutable T&` parameter, not a
// fresh `&place`) whose pointee storage is unannotated (`Any`) must NOT be stored
// into storage that outlives the function: the caller may have passed `&local`, so
// the global would dangle once the frame returns. This is the param-forward analog
// of the freshly-taken-borrow escape, and the last hole that blocked sound
// `noalias` on mutable reference parameters.
func TestForwardedRefIntoGlobalRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "fwdref_global.elisa", `global mutable g: mutable u8&? = null
def f(x: mutable u8&) -> void:
    g <- x
`)
	if !hasErrorContaining(result.Errors(), "forwarded reference into longer-lived storage") {
		t.Fatalf("expected forwarded-ref-into-global to be rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// The same hole reached through a parameter struct field (write-through a
// caller-owned aggregate) must also be rejected.
func TestForwardedRefIntoParamFieldRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "fwdref_field.elisa", `struct Box:
    p: mutable u8&?
def f(out: mutable Box&, x: mutable u8&) -> void:
    out.p <- x
`)
	if !hasErrorContaining(result.Errors(), "forwarded reference into longer-lived storage") {
		t.Fatalf("expected forwarded-ref-into-param-field to be rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// A forwarded reference whose pointee is provably frame-outliving (`heap`/`static`
// storage class) IS safe to store: the pointee does not die with the frame. This
// is the annotated-lifetime escape, exactly Rust's `&'static`/owned store bound.
func TestForwardedHeapRefIntoGlobalAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "fwdref_heap_ok.elisa", `global mutable g: mutable heap u8&? = null
def f(x: mutable heap u8&) -> void:
    g <- x
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a heap-annotated forwarded ref store to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// RETURNING a forwarded reference is safe and must stay accepted: the reference
// flows back to the caller, who owns the storage. The escape check is store-only;
// it must not regress the return path.
func TestForwardedRefReturnAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "fwdref_return_ok.elisa", `def f(x: mutable u8&) -> mutable u8&:
    return x
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected returning a forwarded ref to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// `trusted Unsafe.StaleRef:` is the explicit escape hatch for the trusted base
// (e.g. the concurrency runtime caching a caller pointer in an async task record):
// the author takes responsibility for a pointee lifetime the type system cannot
// verify, and the store is no longer flagged.
func TestForwardedRefTrustedStaleRefAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "fwdref_trusted_ok.elisa", `global mutable g: mutable u8&? = null
def f(x: mutable u8&) -> void:
    trusted Unsafe.StaleRef:
        g <- x
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a trusted Unsafe.StaleRef-wrapped store to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// `can Unsafe.StaleRef:` is the PREFERRED, tracked escape hatch: like `trusted` it
// discharges the forwarded-ref-store check, but it surfaces Unsafe.StaleRef in the
// function's effect signature so the unsafe store is auditable through the capability
// system (a caller without the grant is flagged), rather than invisibly suppressed.
func TestForwardedRefCanStaleRefAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "fwdref_can_ok.elisa", `global mutable g: mutable u8&? = null
def f(x: mutable u8&) -> void:
    can Unsafe.StaleRef:
        g <- x
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a can Unsafe.StaleRef-wrapped store to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The tracking half of the contract: a `can Unsafe.StaleRef:` store surfaces the
// capability, so a caller that neither grants nor trusts it is flagged for the
// missing Unsafe.StaleRef effect (a `trusted` store, being local, would not).
func TestForwardedRefCanStaleRefPropagatesToCaller(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "fwdref_can_propagate.elisa", `global mutable g: mutable u8&? = null
def store(x: mutable u8&) -> void:
    can Unsafe.StaleRef:
        g <- x
def caller(x: mutable u8&) -> void:
    store(x)
`)
	joined := strings.Join(append(result.Errors(), result.Warnings()...), "\n")
	if !strings.Contains(joined, "StaleRef") {
		t.Fatalf("expected caller of a can-StaleRef store to be flagged for the missing Unsafe.StaleRef capability, got:\n%s", joined)
	}
}

func hasErrorContaining(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}
