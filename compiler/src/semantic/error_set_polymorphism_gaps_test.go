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

// CLOSED GAP 2 (join-at-bind): inference is argument-order independent. R
// joins (set union) across every site it binds from — expected-return context
// and each argument — so `both(small, big)` and `both(big, small)` both bind
// R := IoErr ∪ NetErr regardless of whether the call site declares a return.
func TestErrorSetParamBindingOrderIndependent(t *testing.T) {
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
	uses := []string{
		"def usea() -> i64 error[IoErr, NetErr]:\n    return both(big, small)",
		"def useb() -> i64 error[IoErr, NetErr]:\n    return both(small, big)",
		"def usec() -> i64:\n    catch both(big, small):\n        n:\n            return n\n        IoErr.Bad:\n            return 1\n        NetErr.Down:\n            return 2",
		"def used() -> i64:\n    catch both(small, big):\n        n:\n            return n\n        IoErr.Bad:\n            return 1\n        NetErr.Down:\n            return 2",
	}
	for i, use := range uses {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "join_order.elisa", prelude+use+"\n")
		if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
			t.Fatalf("case %d: argument order must not change typability, got:\n%s", i, all)
		}
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

// CLOSED GAP 4 (protocol conformance): generic impl methods conform to their
// protocol counterparts. The canonical type-ID used by SameType's fast path
// previously keyed generic params by SOURCE POSITION, so structurally
// identical signatures declared at different lines compared unequal (an
// "expects X, got X" diagnostic); the key is structural now (and includes the
// interface bound, which it previously omitted). Param NAMES must still match
// between protocol and impl — there is no alpha-renaming.
func TestErrorSetParamProtocolConformance(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "gap_protocol.elisa", `
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
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("[errorset R] impl method should conform to its protocol, got:\n%s", all)
	}
}

// The same fix covers plain type-param generic methods in protocols.
func TestTypeParamProtocolConformance(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "gap_protocol_typeparam.elisa", `
protocol Mapper:
    def map[T](x: T) -> T

struct Identity:
    tag: i64

impl Mapper for Identity:
    def map[T](x: T) -> T:
        return x
`)
	if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
		t.Fatalf("[T] impl method should conform to its protocol, got:\n%s", all)
	}
}

// Cross-scope propagation: a caller's own error-set param flows through a
// callee's param without degenerating — including the same-name case where the
// caller's R and the callee's R are different params.
func TestErrorSetParamFlowsThroughProtocolMethod(t *testing.T) {
	for _, callerParam := range []string{"R", "Q"} {
		result := analyzeFunctionAnalysisTestSource(t, "proto_flow_"+callerParam+".elisa", `
protocol Runner:
    def run[errorset R](f: func() -> i64 error[R]) -> i64 error[R]

struct AddOne:
    tag: i64

impl Runner for AddOne:
    def run[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
        return try f()

def drive[T: Runner, errorset `+callerParam+`](f: func() -> i64 error[`+callerParam+`]) -> i64 error[`+callerParam+`]:
    return T.run(f)
`)
		if all := allDiagnostics(result); strings.TrimSpace(all) != "" {
			t.Fatalf("caller param %q should flow through the protocol method, got:\n%s", callerParam, all)
		}
	}
}
