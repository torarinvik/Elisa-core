//go:build cgo

// Copy into src/semantic/ when the Core owner schedules these regressions.
// The proof-product oracle deliberately avoids unrelated synthetic ExprTypes
// keys while still comparing every retained disjointness entry and its value.
package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

const retainedDisjointKernelSrc = "\ndef axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:\n    return\n"

func assertRetainedFuncPair(t testing.TB, label string, result *Result, want bool) {
	t.Helper()
	info := disjointInfoForFunc(result, "axpy")
	got := info != nil && info.PairDistinct(0, 1)
	if got != want {
		t.Fatalf("%s: axpy params 0,1 distinct = %v, want %v (info=%+v)", label, got, want, info)
	}
}

func assertRetainedNoFuncProof(t testing.TB, label string, result *Result, name string) {
	t.Helper()
	if info := disjointInfoForFunc(result, name); info != nil {
		t.Fatalf("%s: %s must have no retained disjoint proof, got %+v", label, name, info)
	}
}

func retainedUpdateAfterDiskReload(t *testing.T, state *RetainedAnalysis, source string) (*Result, *RetainedAnalysis) {
	t.Helper()
	encoded, err := state.MarshalState()
	if err != nil {
		t.Fatalf("marshal retained state: %v", err)
	}
	state, err = RestoreAnalysisState(encoded)
	if err != nil {
		t.Fatalf("restore retained state: %v", err)
	}
	warm, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
	if err != nil {
		t.Fatalf("retained update: %v", err)
	}
	return warm, state
}

// Compare the full two disjointness products, including their complete key
// sets. mapIncrementalCurrentAST maps cold nodes onto the current warm AST and
// enforces that the mapping is one-to-one.
func assertRetainedDisjointProductParity(t testing.TB, cold, warm *Result) {
	t.Helper()
	mapping := map[PositionedFunctionNode]PositionedFunctionNode{}
	mapIncrementalCurrentAST(t, reflect.ValueOf(cold.File), reflect.ValueOf(warm.File), mapping)

	if (cold.CallArgDisjoint == nil) != (warm.CallArgDisjoint == nil) {
		t.Fatal("CallArgDisjoint nilness differs")
	}
	if len(cold.CallArgDisjoint) != len(warm.CallArgDisjoint) {
		t.Fatalf("CallArgDisjoint key count differs: cold=%d warm=%d", len(cold.CallArgDisjoint), len(warm.CallArgDisjoint))
	}
	for call, want := range cold.CallArgDisjoint {
		mapped, ok := mapping[call]
		if !ok {
			t.Fatalf("unmapped cold CallArgDisjoint key %T", call)
		}
		current, ok := mapped.(*ast.CallExpr)
		if !ok {
			t.Fatalf("CallArgDisjoint key mapped to %T, want *ast.CallExpr", mapped)
		}
		got, ok := warm.CallArgDisjoint[current]
		if !ok {
			t.Fatalf("warm CallArgDisjoint is missing mapped call key %p", current)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("CallArgDisjoint differs at mapped call: cold=%+v warm=%+v", want, got)
		}
	}

	if (cold.FuncDisjointParams == nil) != (warm.FuncDisjointParams == nil) {
		t.Fatal("FuncDisjointParams nilness differs")
	}
	if len(cold.FuncDisjointParams) != len(warm.FuncDisjointParams) {
		t.Fatalf("FuncDisjointParams key count differs: cold=%d warm=%d", len(cold.FuncDisjointParams), len(warm.FuncDisjointParams))
	}
	for fn, want := range cold.FuncDisjointParams {
		mapped, ok := mapping[fn]
		if !ok {
			t.Fatalf("unmapped cold FuncDisjointParams key %T", fn)
		}
		current, ok := mapped.(*ast.FuncDecl)
		if !ok {
			t.Fatalf("FuncDisjointParams key mapped to %T, want *ast.FuncDecl", mapped)
		}
		got, ok := warm.FuncDisjointParams[current]
		if !ok {
			t.Fatalf("warm FuncDisjointParams is missing mapped function %q", current.Name)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("FuncDisjointParams differs for %s: cold=%+v warm=%+v", current.Name, want, got)
		}
	}
}

func TestRetainedFuncDisjointAliasReplacementAcrossDiskReload(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const distinct = retainedDisjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    axpy(&a, &b)
`
	const aliased = retainedDisjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    axpy(&a, &a)
`
	if len(distinct) != len(aliased) {
		t.Fatal("fixture must replace the call with a same-width edit")
	}
	_, state := AnalyzeRetained(retainedParse(t, distinct), retainedParse(t, distinct), []byte(distinct), AnalyzeOptions{})
	for _, step := range []struct {
		name   string
		source string
		want   bool
	}{
		{name: "lose proof while call remains", source: aliased, want: false},
		{name: "regain proof while call remains", source: distinct, want: true},
	} {
		t.Run(step.name, func(t *testing.T) {
			warm, restored := retainedUpdateAfterDiskReload(t, state, step.source)
			state = restored
			cold := AnalyzeWithOptions(retainedParse(t, step.source), AnalyzeOptions{})
			assertRetainedFuncPair(t, "cold "+step.name, cold, step.want)
			assertRetainedFuncPair(t, "warm "+step.name, warm, step.want)
			assertRetainedDisjointProductParity(t, cold, warm)
		})
	}
}

func TestRetainedFuncDisjointReverseDependencyClosureAcrossDiskReload(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = retainedDisjointKernelSrc + `
def forwarder(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    axpy(&y, &x)

def runfreshanchors() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    forwarder(&a, &b)
`
	const removedAnchor = retainedDisjointKernelSrc + `
def forwarder(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    axpy(&y, &x)

def runfreshanchors() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
`
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	warm, _ := retainedUpdateAfterDiskReload(t, state, removedAnchor)
	cold := AnalyzeWithOptions(retainedParse(t, removedAnchor), AnalyzeOptions{})
	assertRetainedNoFuncProof(t, "cold", cold, "forwarder")
	assertRetainedNoFuncProof(t, "cold", cold, "axpy")
	assertRetainedNoFuncProof(t, "warm", warm, "forwarder")
	assertRetainedNoFuncProof(t, "warm", warm, "axpy")
	assertRetainedDisjointProductParity(t, cold, warm)
}

func TestRetainedFuncDisjointProvingCallerRemovedAcrossDiskReload(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = retainedDisjointKernelSrc + `
def runfreshanchors() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    axpy(&a, &b)
`
	const removedCaller = retainedDisjointKernelSrc
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	warm, _ := retainedUpdateAfterDiskReload(t, state, removedCaller)
	cold := AnalyzeWithOptions(retainedParse(t, removedCaller), AnalyzeOptions{})
	assertRetainedNoFuncProof(t, "cold", cold, "axpy")
	assertRetainedNoFuncProof(t, "warm", warm, "axpy")
	assertRetainedDisjointProductParity(t, cold, warm)
}
