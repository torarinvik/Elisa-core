package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func retainedGraphFunction(t *testing.T, r *RetainedAnalysis, name string) *ast.FuncDecl {
	t.Helper()
	for fn := range r.a.funcDeclSymbols {
		if fn.Name == name {
			return fn
		}
	}
	t.Fatalf("missing live function %s", name)
	return nil
}

func TestRetainedUpdateRebuildsCallCycles(t *testing.T) {
	const before = "def a() -> i64:\n    return b()\n\ndef b() -> i64:\n    return 0\n"
	const after = "def a() -> i64:\n    return b()\n\ndef b() -> i64:\n    return a()\n"
	_, r := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if r == nil {
		t.Fatal("analysis not retained")
	}
	a, b := retainedGraphFunction(t, r, "a"), retainedGraphFunction(t, r, "b")
	r.a.callEdgesOf(a)
	r.a.callEdgesOf(b)
	if r.a.funcIsCyclic(a) || len(r.a.storeFlowComponent(a)) != 1 {
		t.Fatal("fixture initially contains a cycle")
	}
	_, err := r.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	edges := r.a.callEdgesOf(b)
	if len(edges) != 1 || edges[0] != a {
		t.Fatalf("updated b kept stale call edges: %#v", edges)
	}
	if !r.a.funcIsCyclic(a) || len(r.a.storeFlowComponent(a)) != 2 {
		t.Fatal("updated graph kept the prior acyclic component")
	}
}

func TestRetainedUpdateRebuildsReturnStoreFlow(t *testing.T) {
	const caller = "\ndef wrap(left: sview, right: sview) -> sview:\n    return pick(left, right)\n"
	const before = "def pick(left: sview, right: sview) -> sview:\n    return left\n" + caller
	const after = "def pick(left: sview, right: sview) -> sview:\n    return right\n" + caller
	_, r := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if r == nil {
		t.Fatal("analysis not retained")
	}
	fn := retainedGraphFunction(t, r, "pick")
	wrapper := retainedGraphFunction(t, r, "wrap")
	if oldWrapper := r.a.storeFlowSummaryFor(wrapper); oldWrapper == nil || !reflect.DeepEqual(oldWrapper.retConn, []bool{true, false}) {
		t.Fatalf("unexpected initial caller flow: %#v", oldWrapper)
	}
	old := r.a.storeFlowSummaryFor(fn)
	if old == nil || !reflect.DeepEqual(old.retConn, []bool{true, false}) {
		t.Fatalf("unexpected initial summary: %#v", old)
	}
	_, err := r.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if caller := r.a.storeFlowSummaryFor(wrapper); caller == nil || !reflect.DeepEqual(caller.retConn, []bool{false, true}) {
		t.Fatalf("unchanged caller kept the previous callee flow: %#v", caller)
	}
	got := r.a.storeFlowSummaryFor(fn)
	if got == nil || !reflect.DeepEqual(got.retConn, []bool{false, true}) {
		t.Fatalf("updated body kept the former return flow: %#v", got)
	}
}

func TestRetainedUpdateRefreshesCallNameIndexAfterAddRemove(t *testing.T) {
	const before = "def base() -> i64:\n    return 0\n"
	const after = before + "\ndef added() -> i64:\n    return base()\n"
	_, r := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if r == nil {
		t.Fatal("analysis not retained")
	}
	base := retainedGraphFunction(t, r, "base")
	r.a.callEdgesOf(base)
	_, err := r.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	added := retainedGraphFunction(t, r, "added")
	edges := r.a.callEdgesOf(added)
	if len(edges) != 1 || edges[0] != base || len(r.a.callDeclsByName["added"]) != 1 {
		t.Fatal("added declaration was absent from call resolution")
	}
	_, err = r.Update(retainedParse(t, before), retainedParse(t, before), []byte(before))
	if err != nil {
		t.Fatal(err)
	}
	r.a.callEdgesOf(base)
	if len(r.a.callDeclsByName["added"]) != 0 {
		t.Fatal("removed declaration remained in call resolution")
	}
}

func TestRetainedUpdateInvalidatesUnchangedCallerEdgesOnAddRemove(t *testing.T) {
	const before = "def caller() -> i64:\n    return later()\n"
	const withCallee = before + "\ndef later() -> i64:\n    return 1\n"
	_, r := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if r == nil {
		t.Fatal("analysis not retained")
	}
	caller := retainedGraphFunction(t, r, "caller")
	if edges := r.a.callEdgesOf(caller); len(edges) != 0 {
		t.Fatalf("unresolved call unexpectedly had edges: %#v", edges)
	}
	_, err := r.Update(retainedParse(t, withCallee), retainedParse(t, withCallee), []byte(withCallee))
	if err != nil {
		t.Fatal(err)
	}
	later := retainedGraphFunction(t, r, "later")
	if edges := r.a.callEdgesOf(caller); len(edges) != 1 || edges[0] != later {
		t.Fatalf("unchanged caller kept stale edges after callee addition: %#v", edges)
	}
	_, err = r.Update(retainedParse(t, before), retainedParse(t, before), []byte(before))
	if err != nil {
		t.Fatal(err)
	}
	if edges := r.a.callEdgesOf(caller); len(edges) != 0 {
		t.Fatalf("unchanged caller kept stale edges after callee removal: %#v", edges)
	}
}
