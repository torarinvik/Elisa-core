package semantic

import (
	"strings"
	"testing"
)

// This file pins the KNOWN GAPS in error-set polymorphism (docs/64 Phase 5b
// follow-up). Each test asserts the analyzer's CURRENT behavior so the gap is
// visible and intentional; the phase that closes a gap flips its assertion.

// GAP 1 (symbolic sets): a param cannot be unioned with anything — neither a
// concrete set (`error[R, Timeout]`) nor another param (`error[R, S]`). Both
// forms fall through to variant lookup and fail. Blocks `pair`-style
// combinators and combinators that add their own failure mode.
func TestGapErrorSetParamUnionWithConcreteRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "gap_union_concrete.elisa", `
error Timeout:
    Expired

def retry[errorset R](f: func() -> i64 error[R]) -> i64 error[R, Timeout]:
    return try f()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `unknown error set or variant "R"`) {
		t.Fatalf("gap moved: error[R, Timeout] no longer rejected as unknown variant; update Phase 1 plan/tests. got:\n%s", all)
	}
}

func TestGapErrorSetParamUnionWithParamRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "gap_union_params.elisa", `
error IoErr:
    Bad

error NetErr:
    Down

extern reader() -> i64 error[IoErr]
extern fetch() -> i64 error[NetErr]

def pair[errorset R, errorset S](f: func() -> i64 error[R], g: func() -> i64 error[S]) -> i64 error[R, S]:
    return (try f()) + (try g())
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `unknown error set or variant "R"`) {
		t.Fatalf("gap moved: error[R, S] no longer rejected as unknown variant. got:\n%s", all)
	}
}

// GAP 1b: raising a concrete tag inside an `[errorset R]` body is rejected —
// the function cannot declare `error[R, Timeout]` (gap 1), and raising into a
// bare `error[R]` is (correctly) refused since R is opaque.
func TestGapErrorSetParamBodyCannotRaiseConcrete(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "gap_raise_concrete.elisa", `
error Timeout:
    Expired

def giveUp[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    v: i64 = try f() else e:
        raise Timeout.Expired
    return v
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "Timeout.Expired") {
		t.Fatalf("expected the concrete raise into R to be diagnosed, got:\n%s", all)
	}
}

// GAP 2 (join-at-bind): inference is order-dependent. R binds to the FIRST
// argument's set; a later argument with a SUBSET is accepted, but the same
// call with the arguments swapped is rejected (no least-upper-bound join).
func TestGapErrorSetParamBindingOrderDependent(t *testing.T) {
	const prelude = `
error IoErr:
    Bad

error NetErr:
    Down

extern big() -> i64 error[IoErr, NetErr]
extern small() -> i64 error[IoErr]

def both[errorset R](f: func() -> i64 error[R], g: func() -> i64 error[R]) -> i64 error[R]:
    a: i64 = try f()
    b: i64 = try g()
    return a + b
`
	// With a declared return union at the call site, R binds from the EXPECTED
	// type first and both argument orders work — that part is fine today.
	for i, order := range []string{"both(big, small)", "both(small, big)"} {
		ctx := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "gap_order_ctx.elisa", prelude+`
def use`+string(rune('a'+i))+`() -> i64 error[IoErr, NetErr]:
    return `+order+`
`)
		if all := allDiagnostics(ctx); strings.TrimSpace(all) != "" {
			t.Fatalf("declared-return context should bind R for %s, got:\n%s", order, all)
		}
	}
	// WITHOUT that context (catch at the call site), R pins to argument 1:
	// wide-first accepts (arg 2 is a subset), narrow-first rejects arg 2.
	wide := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "gap_order_wide_first.elisa", prelude+`
def use() -> i64:
    catch both(big, small):
        n:
            return n
        IoErr.Bad:
            return 1
        NetErr.Down:
            return 2
`)
	if all := allDiagnostics(wide); strings.TrimSpace(all) != "" {
		t.Fatalf("wide-set-first should bind R and accept the subset second arg, got:\n%s", all)
	}
	narrow := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "gap_order_narrow_first.elisa", prelude+`
def use() -> i64:
    catch both(small, big):
        n:
            return n
        IoErr.Bad:
            return 1
        NetErr.Down:
            return 2
`)
	all := allDiagnostics(narrow)
	if !strings.Contains(all, "argument 2") {
		t.Fatalf("gap moved: narrow-set-first no longer rejects argument 2 (join implemented?). got:\n%s", all)
	}
}

// GAP 3 (catch over opaque R): a catch binder arm cannot match a param set —
// while the equivalent `try ... else e: raise e` recovery form WORKS. The two
// surfaces should agree once the catch arm learns about param sets.
func TestGapCatchBinderArmCannotMatchParamSet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "gap_catch_param.elisa", `
def logged[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    catch f():
        n:
            return n
        e:
            raise e
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "does not match R") {
		t.Fatalf("gap moved: catch binder arm over a param set no longer rejected. got:\n%s", all)
	}
}

func TestTryElseBindingReRaisesOpaqueParamSet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "gap_else_param.elisa", `
def passThrough[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    v: i64 = try f() else e:
        raise e
    return v
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("try/else re-raise of an opaque param-set error should type-check, got:\n%s", all)
	}
}

// GAP 4 (protocol conformance): an impl method with an `[errorset R]` (or any
// generic) signature never conforms, because SameType's canonical-type-ID fast
// path keys generic params by SOURCE POSITION (typeid.go appendGenericParamSlice
// includes param.Position), so structurally identical signatures declared at
// different lines compare unequal — yielding an "expects X, got X" diagnostic.
func TestGapErrorSetParamProtocolConformance(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "gap_protocol.elisa", `
error IoErr:
    Bad

protocol Runner:
    def run[errorset R](f: func() -> i64 error[R]) -> i64 error[R]

struct MyRunner:
    tag: i64

impl Runner for MyRunner:
    def run[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
        return try f()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `impl method "run" for interface "Runner" expects`) {
		t.Fatalf("gap moved: [errorset R] impl method now conforms (Phase 4 done?). got:\n%s", all)
	}
}
