package semantic

import (
	"strings"
	"testing"
)

// Returning a freshly-taken reference to a function-local is a guaranteed dangle.
func TestReturnRefToStackLocalIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "return_stack_local_ref.elisa", `def f() -> i32&:
    x: mutable i32 = 5
    return x.ref[i32&]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "dangles once the function returns") {
		t.Fatalf("expected dangling-local-return error, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// Returning a reference to a local fixed-array element is also a dangle.
func TestReturnRefToStackArrayElemIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "return_stack_array_ref.elisa", `def f() -> i32&:
    xs: mutable array[i32, 4] = [1, 2, 3, 4]
    return xs[0].ref[i32&]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "dangles once the function returns") {
		t.Fatalf("expected dangling-array-elem-return error, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// Returning a borrow of a by-value parameter dangles (the param copy lives in the
// callee frame).
func TestReturnRefToValueParamIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "return_value_param_ref.elisa", `def f(p: i32) -> i32&:
    return p.ref[i32&]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "dangles once the function returns") {
		t.Fatalf("expected dangling-value-param-return error, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// Storing a freshly-taken reference to a function-local into a destination that
// outlives the function (a struct field reached through a parameter) dangles once
// the frame is gone — even without a lifetime-widening cast. (Emulator audit.)
func TestStoreRefToLocalIntoOutlivingFieldIsRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_local_ref_field.elisa", `struct Box:
    p: mutable u8&?

def f(out: mutable Box&) -> void:
    x: mutable u8 = 65
    out.p <- x.ref[u8&]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "dangles once the function returns") {
		t.Fatalf("expected stored-local-ref escape error, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// Rebinding a LOCAL reference variable to point at another local, then using it
// within the function (e.g. passing it to a call), is safe and must NOT be
// flagged: the local pointer slot dies with the function. (Regression for an
// equeue.elisa false positive.)
func TestRebindLocalRefToLocalIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "rebind_local_ref.elisa", `def use(p: u32&?) -> u32:
    return get p else 0

def f(have: bool) -> u32:
    v: mutable u32 = 7
    r: mutable u32&? = null
    if have:
        r <- v.ref[mutable u32&]
    return use(r)
`)
	if strings.Contains(strings.Join(result.Errors(), "\n"), "dangles once the function returns") {
		t.Fatalf("expected rebinding a local ref to a local to be accepted, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// Storing a static-lived borrow into a field is fine (it does not dangle).
func TestStoreStaticRefIntoFieldIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "store_static_ref_field.elisa", `struct Box:
    p: mutable static u8&?

def f(out: mutable Box&, s: static u8&) -> void:
    out.p <- s
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected static-borrow field store to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Returning a stored reference element (a value load, pointing elsewhere) is NOT
// an escape and must be accepted.
func TestReturnStoredRefElementIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "return_stored_ref_elem.elisa", `def read(xs: darray[u8&], i: usize) -> u8&:
    return get xs[i] else ""
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected stored-ref element return to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}
