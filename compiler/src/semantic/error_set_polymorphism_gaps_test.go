package semantic

import (
	"strings"
	"testing"
)

// This file pins the KNOWN GAPS in error-set polymorphism (docs/64 Phase 5b
// follow-up). Each test asserts the analyzer's CURRENT behavior so the gap is
// visible and intentional; the phase that closes a gap flips its assertion.

// CLOSED GAP 1 (symbolic sets): a param unions with concrete sets
// (`error[R, Timeout]`) and with other params (`error[R, S]`), so combinators
// can add their own failure mode and `pair`-style combinators are typeable.
func TestErrorSetParamUnionWithConcrete(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "union_concrete.elisa", `
error Timeout:
    Expired

error IoErr:
    Bad

extern reader() -> i64 error[IoErr]

def giveUp[errorset R](f: func() -> i64 error[R]) -> i64 error[R, Timeout]:
    v: i64 = try f() else e:
        raise Timeout.Expired
    return v

def use() -> i64 error[IoErr, Timeout]:
    return giveUp(reader)
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("error[R, Timeout] combinator should type-check end to end, got:\n%s", all)
	}
}

func TestErrorSetParamUnionWithParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "union_params.elisa", `
error IoErr:
    Bad

error NetErr:
    Down

extern reader() -> i64 error[IoErr]
extern fetch() -> i64 error[NetErr]

def pair[errorset R, errorset S](f: func() -> i64 error[R], g: func() -> i64 error[S]) -> i64 error[R, S]:
    return (try f()) + (try g())

def use() -> i64 error[IoErr, NetErr]:
    return pair(reader, fetch)
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("error[R, S] return union should type-check, got:\n%s", all)
	}
}

// Overlap subtraction: when the pattern is `error[R, Timeout]` and the
// argument raises `error[IoErr, Timeout]`, R binds to IoErr only — the
// pattern's own concrete tags are not double-counted into R.
func TestErrorSetParamMixedPatternBindsBySubtraction(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "union_subtract.elisa", `
error IoErr:
    Bad

error Timeout:
    Expired

extern flaky() -> i64 error[IoErr, Timeout]

def giveUp[errorset R](f: func() -> i64 error[R, Timeout]) -> i64 error[R, Timeout]:
    return try f()

def use() -> i64 error[IoErr, Timeout]:
    return giveUp(flaky)
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("overlapping mixed pattern should bind R := IoErr via subtraction, got:\n%s", all)
	}
}

// An unbound R in a mixed return still surfaces as "cannot infer", and a catch
// over a set with an unresolved param component demands a catch-all arm.
func TestErrorSetParamMixedReturnUnboundStillDiagnosed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "union_unbound.elisa", `
error IoErr:
    Bad

def mk[errorset R]() -> i64 error[R, IoErr]:
    return 1

def use() -> i64:
    catch mk():
        n:
            return n
        IoErr.Bad:
            return 1
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `cannot infer error-set parameter "R"`) {
		t.Fatalf("expected unbound R in mixed return to be diagnosed, got:\n%s", all)
	}
	if !strings.Contains(all, "requires a catch-all") {
		t.Fatalf("expected the param-component exhaustiveness guard, got:\n%s", all)
	}
}

// Raising a concrete tag into a BARE `error[R]` return is (correctly) refused
// — R is opaque; declaring `error[R, Timeout]` is the way to add an own
// failure mode (see TestErrorSetParamUnionWithConcrete).
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

// A BARE binder arm is a variant-name arm, not a catch-all — it cannot match
// a param set (nor a concrete set). The catch-all spelling is `error e:`,
// which matches and re-raises an opaque param-set error (next test). Remaining
// polish: this diagnostic could hint at `error e:`.
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

func TestCatchAllErrorBindingMatchesParamSet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "gap_catch_all_param.elisa", `
def logged[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    catch f():
        n:
            return n
        error e:
            raise e
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("`error e:` catch-all should match and re-raise an opaque param-set error, got:\n%s", all)
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
