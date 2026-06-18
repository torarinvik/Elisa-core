package semantic

import (
	"strings"
	"testing"
)

// docs/91 S4: growing a container that is a FIELD of a region-param struct ref param
// (`def fill[@r](m: mutable Mod& @r): m.bits.push(..)`). The struct's region is propagated onto the
// field type so the growth-site gate resolves it; the backend threads the caller's region. These
// tests lock in the SAFE surface: pure growth accepted, every borrow-OUT of the grown field that
// loses the region tie rejected, and the explicit `@r` return accepted (the sound escape hatch).

const s4Hdr = "struct Mod[@owner]:\n    bits: mutable darray[u8]\n"

func s4analyze(t *testing.T, name, body string) string {
	t.Helper()
	res := analyzeTreeTestSourceWithSemanticErrors(t, name+".elisa", s4Hdr+body)
	return strings.Join(res.Errors(), " | ")
}

// Pure field growth via a region-param struct ref param is accepted (the S4 enablement).
func TestS4FieldGrowthAccepted(t *testing.T) {
	errs := s4analyze(t, "s4_growth", `def fill[@r](m: mutable Mod& @r) -> i64:
    can Memory.Allocate, Abort.Panic:
        m.bits.push(65)
        return i64(m.bits[0])
`)
	if errs != "" {
		t.Fatalf("field growth on a region-param struct ref must be accepted, got: %s", errs)
	}
}

// Returning a VIEW into the grown field with a region-LESS return type loses the @r tie → reject
// (this was a confirmed use-after-free before the escape coverage was added).
func TestS4ReturnViewRegionlessRejected(t *testing.T) {
	errs := s4analyze(t, "s4_ret_view", `def leak[@r](m: mutable Mod& @r) -> view[u8]:
    can Memory.Allocate, Abort.Panic:
        m.bits.push(65)
        return m.bits[0:1]
`)
	if !strings.Contains(errs, "region parameter") {
		t.Fatalf("returning a region-less view into a grown @r field must be rejected, got: %s", errs)
	}
}

// Returning the grown darray field itself region-less likewise loses the tie → reject.
func TestS4ReturnDarrayRegionlessRejected(t *testing.T) {
	errs := s4analyze(t, "s4_ret_darray", `def leak[@r](m: mutable Mod& @r) -> darray[u8]:
    can Memory.Allocate, Abort.Panic:
        m.bits.push(65)
        return m.bits
`)
	if !strings.Contains(errs, "region parameter") {
		t.Fatalf("returning a region-less darray of a grown @r field must be rejected, got: %s", errs)
	}
}

// The sound escape hatch: an explicit `@r` return is ACCEPTED at the definition (the caller-side
// escape check then catches a caller that stores it past the region, and allows in-lifetime use).
func TestS4ReturnViewAtRAccepted(t *testing.T) {
	errs := s4analyze(t, "s4_ret_at_r", `def view_of[@r](m: mutable Mod& @r) -> view[u8] @r:
    can Memory.Allocate, Abort.Panic:
        m.bits.push(65)
        return m.bits[0:1]
`)
	if errs != "" {
		t.Fatalf("explicit `-> view[u8] @r` return must be accepted (sound, caller-checked), got: %s", errs)
	}
}

// Caller storing an explicit-@r returned view PAST the region's death is rejected (the borrow-out is
// caught at the call site).
func TestS4ExplicitReturnViewEscapeCaughtAtCaller(t *testing.T) {
	errs := s4analyze(t, "s4_caller_escape", `def view_of[@r](m: mutable Mod& @r) -> view[u8] @r:
    can Memory.Allocate, Abort.Panic:
        m.bits.push(65)
        return m.bits[0:1]

def caller() -> i64:
    can Abort.Panic:
        escaped: mutable view[u8] = zeroed
        region inner(64):
            m: mutable Mod& @inner = new[inner] Mod(bits: [])
            escaped <- view_of(m)
        return i64(escaped[0])
`)
	if !strings.Contains(errs, "outlives") && !strings.Contains(errs, "dangling") {
		t.Fatalf("storing an @r-returned view past the region must be rejected at the caller, got: %s", errs)
	}
}

// S4 Stage 1: ZERO-annotation inference. `def fill(m: mutable Mod&): m.bits.push(..)` over a plain
// (region-param-less) struct is rewritten in place into the `[@r]`/`Mod& @r` form — the caller's
// region threads to the field growth automatically, no annotation required.
func TestS4Stage1ZeroAnnotationGrowthInferred(t *testing.T) {
	errs := analyzeTreeTestSourceWithSemanticErrors(t, "s4s1_growth.elisa",
		"struct Mod:\n    bits: mutable darray[u8]\n"+`def fill(m: mutable Mod&) -> i64:
    can Memory.Allocate, Abort.Panic:
        m.bits.push(65)
        return i64(m.bits[0])
`).Errors()
	if joined := strings.Join(errs, " | "); joined != "" {
		t.Fatalf("zero-annotation struct-field growth must be accepted (region inferred), got: %s", joined)
	}
}

// Stage 1 must NOT widen the safe surface: the inferred region param drives the SAME borrow-out
// escape check as the explicit form, so a region-less view return over the inferred param is still
// rejected (was a use-after-free; the escape coverage is independent of how the region arrived).
func TestS4Stage1InferredReturnViewRegionlessRejected(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "s4s1_leak.elisa",
		"struct Mod:\n    bits: mutable darray[u8]\n"+`def leak(m: mutable Mod&) -> view[u8]:
    can Memory.Allocate, Abort.Panic:
        m.bits.push(65)
        return m.bits[0:1]
`).Errors(), " | ")
	if !strings.Contains(errs, "region parameter") {
		t.Fatalf("region-less view return over an INFERRED region param must be rejected, got: %s", errs)
	}
}

// Returning a REF into the grown field with a region-less type also loses the @r tie (was a confirmed
// use-after-free via `&m.bits[0]`) → reject. The check peels &/index/field to the borrowed region.
func TestS4ReturnRefRegionlessRejected(t *testing.T) {
	errs := s4analyze(t, "s4_ret_ref", `def leak[@r](m: mutable Mod& @r) -> u8&:
    can Memory.Allocate, Abort.Panic:
        m.bits.push(65)
        return &m.bits[0]
`)
	if !strings.Contains(errs, "region parameter") {
		t.Fatalf("returning a region-less ref into a grown @r field must be rejected, got: %s", errs)
	}
}

// Region-poly forwarding inference: a wrapper that forwards a region-less container ref param to a
// callee requiring a region there is itself made region-poly (no "cannot infer region parameter").
func TestRegionPolyForwardingInferred(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "fwd.elisa", `struct Mod:
    bits: mutable darray[u8]
def sink(dst: mutable darray[Mod]&, v: Mod) -> void:
    can Memory.Allocate, Abort.Panic:
        dst.push(v)
def mid(dst: mutable darray[Mod]&, v: Mod) -> void:
    can Memory.Allocate, Abort.Panic:
        sink(dst, v)
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("forwarding a container ref param to a region-requiring callee must infer region-poly, got: %s", errs)
	}
}
