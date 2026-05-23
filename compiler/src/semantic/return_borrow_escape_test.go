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

// Returning a stored reference element (a value load, pointing elsewhere) is NOT
// an escape and must be accepted.
func TestReturnStoredRefElementIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "return_stored_ref_elem.elisa", `def read(xs: darray[u8&], i: usize) -> u8&:
    return xs[i] else ""
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected stored-ref element return to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}
