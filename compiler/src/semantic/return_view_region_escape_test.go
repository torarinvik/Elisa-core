package semantic

import (
	"strings"
	"testing"
)

// A returned `view[u8] @r` ties the borrow to the caller's region; storing it past that region's
// death must be a compile error, not a use-after-free. EXPLICIT region-param form. (Before the fix
// the view dropped its `@r` during surface resolution, so the escape was uncaught and segfaulted.)
func TestReturnedViewExplicitRegionEscapeCaught(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "ret_view_explicit_escape.elisa", `def head[@r](out: mutable darray[u8]& @r, n: usize) -> view[u8] @r:
    out.push(65u8)
    return out[0:n]

def caller() -> void:
    can Abort.Panic:
        outer: mutable darray[u8] = []
        outer.push(9u8)
        escaped: mutable view[u8] = outer[0:1]
        region inner(64):
            v: mutable darray[u8] @inner = []
            escaped <- head(&v, 1)
        for b in escaped:
            panic("must not reach")
`)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, `region "inner"`) || !strings.Contains(errs, "dangling reference") {
		t.Fatalf("expected the returned view to be tied to region \"inner\" and the escape rejected, got:\n%s", errs)
	}
}

// Same escape, but ZERO region annotations: inference ties the param to a region param and stamps
// the returned view's region to it (inferReturnViewRegion). The escape must still be caught.
func TestReturnedViewInferredRegionEscapeCaught(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "ret_view_inferred_escape.elisa", `def head(out: mutable darray[u8]&, n: usize) -> view[u8]:
    out.push(65u8)
    return out[0:n]

def caller() -> void:
    can Abort.Panic:
        outer: mutable darray[u8] = []
        outer.push(9u8)
        escaped: mutable view[u8] = outer[0:1]
        region inner(64):
            v: mutable darray[u8] @inner = []
            escaped <- head(&v, 1)
        for b in escaped:
            panic("must not reach")
`)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, `region "inner"`) || !strings.Contains(errs, "dangling reference") {
		t.Fatalf("expected inference to tie the returned view to region \"inner\" and reject the escape, got:\n%s", errs)
	}
}

// The same inferred borrow, but routed through a local alias before return. Region inference must
// still tie the returned view to the grown parameter's region; otherwise the call result appears
// region-less and can escape its backing.
func TestReturnedViewInferredRegionThroughLocalEscapeCaught(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "ret_view_inferred_local_escape.elisa", `def head(out: mutable darray[u8]&, n: usize) -> view[u8]:
    out.push(65u8)
    w: view[u8] = out[0:n]
    return w

def caller() -> void:
    can Abort.Panic:
        outer: mutable darray[u8] = []
        outer.push(9u8)
        escaped: mutable view[u8] = outer[0:1]
        region inner(64):
            v: mutable darray[u8] @inner = []
            escaped <- head(&v, 1)
        for b in escaped:
            panic("must not reach")
`)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, `region "inner"`) || !strings.Contains(errs, "dangling reference") {
		t.Fatalf("expected inference through local view alias to tie the returned view to region \"inner\" and reject the escape, got:\n%s", errs)
	}
}

// Do not over-infer: if a local initially aliases the grown parameter but is later reassigned to
// a different source, returning that local must not be stamped with the grown parameter's region.
func TestReturnedViewInferredRegionInvalidatesReassignedLocalAlias(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "ret_view_reassigned_alias_ok.elisa", `def head(out: mutable darray[u8]&, src: darray[u8]&, n: usize) -> view[u8]:
    out.push(65u8)
    w: mutable view[u8] = out[0:n]
    w <- src[0:n]
    return w

def caller() -> usize:
    can Abort.Panic:
        outer: mutable darray[u8] = []
        outer.push(9u8)
        escaped: mutable view[u8] = outer[0:1]
        region inner(64):
            v: mutable darray[u8] @inner = []
            escaped <- head(&v, &outer, 1)
        return escaped[0].usize()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("returning a reassigned view alias backed by outer storage should remain accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The valid case must NOT be rejected: when the source container outlives the view's use, returning
// and reading the view is sound and must type-check cleanly.
func TestReturnedViewWithinLifetimeAccepted(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "ret_view_ok.elisa", `def head(out: mutable darray[u8]&, n: usize) -> view[u8]:
    out.push(65u8)
    return out[0:n]

def caller() -> usize:
    can Abort.Panic:
        v: mutable darray[u8] = []
        w: view[u8] = head(&v, 1)
        total: mutable usize = 0
        for b in w:
            total <- total + b.usize()
        return total
    return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("returning/reading a view within the source container's lifetime should be sound, got: %v", errs)
	}
}
