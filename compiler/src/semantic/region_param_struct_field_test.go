package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
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

// A hand-written region parameter for one input must not disable inference for an unrelated
// struct field that the function grows. The two region lifetimes are independent and both must
// be threaded through the function boundary.
func TestExplicitRegionParameterComposesWithInferredStructRegion(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "explicit_plus_inferred_region.elisa", `struct Scratch:
    values: mutable darray[u8]

def route[@source](source: darray[u8]& @source, scratch: mutable Scratch&) -> void:
    can Memory.Allocate, Abort.Panic:
        scratch.values.push(source[0])

def caller[@source, @scratch](source: darray[u8]& @source, scratch: mutable Scratch& @scratch) -> void:
    can Memory.Allocate, Abort.Panic:
        route(source, scratch)
	`)
	if joined := strings.Join(result.Errors(), " | "); joined != "" {
		t.Fatalf("explicit and inferred region parameters must compose independently, got: %s", joined)
	}
	routeSymbol, ok := result.GlobalScope.Lookup("route")
	if !ok || routeSymbol == nil {
		t.Fatal("route declaration was not added to the global scope")
	}
	route, ok := routeSymbol.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("route declaration has node type %T", routeSymbol.Node)
	}
	if len(route.RegionParams) != 2 || route.RegionParams[0] != "source" || route.RegionParams[1] != "__rg_scratch" {
		t.Fatalf("expected independent explicit and inferred regions, got %v", route.RegionParams)
	}
}

func TestExplicitRegionParameterComposesWithForwardedStructRegion(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "explicit_plus_forwarded_region.elisa", `struct Scratch:
    values: mutable darray[u8]

def append_into[@owner](scratch: mutable Scratch& @owner, value: u8) -> void:
    can Memory.Allocate, Abort.Panic:
        scratch.values.push(value)

def forward[@source](source: darray[u8]& @source, scratch: mutable Scratch&) -> void:
    can Memory.Allocate, Abort.Panic:
        append_into(scratch, source[0])

def caller[@source, @target](source: darray[u8]& @source, scratch: mutable Scratch& @target) -> void:
    can Memory.Allocate, Abort.Panic:
        forward(source, scratch)
`)
	if joined := strings.Join(result.Errors(), " | "); joined != "" {
		t.Fatalf("an explicit region must compose with a forwarded inferred struct region, got: %s", joined)
	}
	forwardSymbol, ok := result.GlobalScope.Lookup("forward")
	if !ok || forwardSymbol == nil {
		t.Fatal("forward declaration was not added to the global scope")
	}
	forward, ok := forwardSymbol.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("forward declaration has node type %T", forwardSymbol.Node)
	}
	if len(forward.RegionParams) != 2 || forward.RegionParams[0] != "source" || forward.RegionParams[1] != "__rg_scratch" {
		t.Fatalf("expected explicit and forwarded regions to remain independent, got %v", forward.RegionParams)
	}
}

// Forwarding a growable field through a helper must infer the enclosing struct's region just like
// growing that field directly. Otherwise the callee's hidden region argument cannot be derived from
// `&workspace.values`, although the caller owns the lifetime-bearing struct reference.
func TestRegionPolyProjectedStructFieldForwardingInferred(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "projected_struct_field_forward.elisa", `struct Workspace:
    values: mutable darray[i64]

def append_one(values: mutable darray[i64]&, value: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        values.push(value)

def append_from_member(workspace: mutable Workspace&) -> void:
    can Memory.Allocate, Abort.Panic:
        append_one(&workspace.values, 1)

def append_with_other_region[@r](values: mutable darray[i64]& @r, workspace: mutable Workspace&) -> void:
    can Memory.Allocate, Abort.Panic:
        append_one(&workspace.values, 2)
        values.push(3)

def caller[@r](workspace: mutable Workspace& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        append_from_member(workspace)

def caller_with_distinct_regions[@values, @workspace](values: mutable darray[i64]& @values, workspace: mutable Workspace& @workspace) -> void:
    can Memory.Allocate, Abort.Panic:
        append_with_other_region(values, workspace)
`)
	if joined := strings.Join(result.Errors(), " | "); joined != "" {
		t.Fatalf("forwarding a mutable container field must infer its enclosing region, got: %s", joined)
	}
	symbol, ok := result.GlobalScope.Lookup("append_from_member")
	if !ok || symbol == nil {
		t.Fatal("append_from_member declaration was not added to the global scope")
	}
	fn, ok := symbol.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("append_from_member declaration has node type %T", symbol.Node)
	}
	if len(fn.RegionParams) != 1 || fn.RegionParams[0] != "__rg_workspace" {
		t.Fatalf("expected the struct parameter's inferred region, got %v", fn.RegionParams)
	}
	otherSymbol, ok := result.GlobalScope.Lookup("append_with_other_region")
	if !ok || otherSymbol == nil {
		t.Fatal("append_with_other_region declaration was not added to the global scope")
	}
	otherFn, ok := otherSymbol.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("append_with_other_region declaration has node type %T", otherSymbol.Node)
	}
	if len(otherFn.RegionParams) != 2 || otherFn.RegionParams[0] != "r" || otherFn.RegionParams[1] != "__rg_workspace" {
		t.Fatalf("expected explicit and projected-field regions to remain independent, got %v", otherFn.RegionParams)
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
