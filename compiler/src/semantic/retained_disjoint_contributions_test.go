//go:build cgo

// Copy into src/semantic/ when the Core owner schedules these regressions.
// The proof-product oracle deliberately avoids unrelated synthetic ExprTypes
// keys while still comparing every retained disjointness entry and its value.
package semantic

import (
	"fmt"
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

func TestRetainedFuncDisjointAddressTakenOwnerAddedAndRemovedAcrossDiskReload(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = retainedDisjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    axpy(&a, &b)
`
	const addressTaken = retainedDisjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    axpy(&a, &b)
    handler: fn(mutable darray[f64]&, mutable darray[f64]&) -> void = axpy
`
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	for _, step := range []struct {
		name   string
		source string
		want   bool
	}{
		{name: "address taken", source: addressTaken, want: false},
		{name: "address use removed", source: before, want: true},
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

func TestRetainedFuncNameUsageIndexTracksNonFunctionOwnersAndRepeatedEntries(t *testing.T) {
	target := &ast.FuncDecl{Name: "target"}
	targetOverload := &ast.FuncDecl{Name: "target"}
	sharedValue := &ast.Ident{Name: "target"}
	global := &ast.GlobalDecl{Name: "handler", Value: sharedValue}
	otherGlobal := &ast.GlobalDecl{Name: "other", Value: sharedValue}
	decls := []scopedDecl{{Decl: target}, {Decl: targetOverload}, {Decl: global}, {Decl: global}, {Decl: otherGlobal}}
	state := &RetainedAnalysis{}
	if !state.rebuildRetainedFuncNameUsageIndex(decls) {
		t.Fatal("failed to build value-name index")
	}
	if state.funcNameUsageFunctionCounts["target"] != 2 || state.funcNameUsageValueCounts["target"] != 3 {
		t.Fatalf("unexpected initial counts: functions=%v values=%v", state.funcNameUsageFunctionCounts, state.funcNameUsageValueCounts)
	}

	global.Value = &ast.IntLit{Value: "0"}
	state.funcNameUsagePending = true
	state.funcNameUsageChangedOwners = []ast.Decl{global}
	_, used := state.funcNameUsageForPost(decls)
	if !used["target"] || state.funcNameUsageValueCounts["target"] != 1 {
		t.Fatalf("replacing a repeated non-function owner erased another owner's contribution: used=%v counts=%v", used["target"], state.funcNameUsageValueCounts)
	}

	state.funcNameUsagePending = true
	state.funcNameUsageChangedOwners = []ast.Decl{otherGlobal}
	otherGlobal.Value = &ast.IntLit{Value: "1"}
	gotCounts, gotUsed := state.funcNameUsageForPost(decls)
	wantCounts, wantUsed := scanFuncNameUsage(decls)
	if !reflect.DeepEqual(gotCounts, wantCounts) || !reflect.DeepEqual(gotUsed, wantUsed) {
		t.Fatalf("indexed names differ from full scan: counts=%v/%v used=%v/%v", gotCounts, wantCounts, gotUsed, wantUsed)
	}
	if gotUsed["target"] || state.funcNameUsageValueCounts["target"] != 0 {
		t.Fatalf("last non-function contribution was not removed: used=%v counts=%v", gotUsed["target"], state.funcNameUsageValueCounts)
	}
}

func TestRetainedFuncNameUsageIndexHarvestsLateTrackedOwners(t *testing.T) {
	t.Run("late known owner", func(t *testing.T) {
		fn := &ast.FuncDecl{Name: "worker", Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "before"}}}}
		decls := []scopedDecl{{Decl: fn}}
		state := &RetainedAnalysis{}
		if !state.rebuildRetainedFuncNameUsageIndex(decls) {
			t.Fatal("failed to build value-name index")
		}
		fn.Body[0].(*ast.ReturnStmt).Value.(*ast.Ident).Name = "after"
		state.funcNameUsagePending = true
		state.funcNameUsageTracking = true
		state.funcNameUsageOwnerLookup = retainedFuncNameUsageFunctionOwnerMap(decls)
		state.funcNameUsageTouchedOwners = map[ast.Decl]bool{}
		state.noteFuncNameUsageFunctionAnalysis(fn)

		gotCounts, gotUsed := state.funcNameUsageForPost(decls)
		wantCounts, wantUsed := scanFuncNameUsage(decls)
		if !reflect.DeepEqual(gotCounts, wantCounts) || !reflect.DeepEqual(gotUsed, wantUsed) {
			t.Fatalf("late known owner was omitted: counts=%v/%v used=%v/%v", gotCounts, wantCounts, gotUsed, wantUsed)
		}
	})

	t.Run("late unknown owner forces rebuild", func(t *testing.T) {
		fn := &ast.FuncDecl{Name: "worker"}
		global := &ast.GlobalDecl{Name: "value", Value: &ast.Ident{Name: "before"}}
		decls := []scopedDecl{{Decl: fn}, {Decl: global}}
		state := &RetainedAnalysis{}
		if !state.rebuildRetainedFuncNameUsageIndex(decls) {
			t.Fatal("failed to build value-name index")
		}
		global.Value = &ast.Ident{Name: "after"}
		state.funcNameUsagePending = true
		state.funcNameUsageTracking = true
		state.funcNameUsageOwnerLookup = retainedFuncNameUsageFunctionOwnerMap(decls)
		state.funcNameUsageTouchedOwners = map[ast.Decl]bool{}
		state.noteFuncNameUsageFunctionAnalysis(&ast.FuncDecl{Name: "synthetic"})

		gotCounts, gotUsed := state.funcNameUsageForPost(decls)
		wantCounts, wantUsed := scanFuncNameUsage(decls)
		if !reflect.DeepEqual(gotCounts, wantCounts) || !reflect.DeepEqual(gotUsed, wantUsed) {
			t.Fatalf("late unknown owner did not rebuild the index: counts=%v/%v used=%v/%v", gotCounts, wantCounts, gotUsed, wantUsed)
		}
	})
}

func BenchmarkRetainedFuncNameUsageUpdate(b *testing.B) {
	const declarations = 4096
	decls := make([]scopedDecl, declarations)
	for i := range decls {
		name := fmt.Sprintf("worker_%d", i)
		use := fmt.Sprintf("worker_%d", (i+1)%declarations)
		decls[i] = scopedDecl{Decl: &ast.FuncDecl{
			Name: name,
			Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: use}}},
		}}
	}

	b.Run("full_scan", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			scanFuncNameUsage(decls)
		}
	})
	b.Run("one_owner_update", func(b *testing.B) {
		state := &RetainedAnalysis{}
		if !state.rebuildRetainedFuncNameUsageIndex(decls) {
			b.Fatal("failed to build value-name index")
		}
		owner := decls[declarations/2].Decl.(*ast.FuncDecl)
		ident := owner.Body[0].(*ast.ReturnStmt).Value.(*ast.Ident)
		ownerDecl := decls[declarations/2].Decl
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if ident.Name == "worker_1" {
				ident.Name = "worker_2"
			} else {
				ident.Name = "worker_1"
			}
			state.funcNameUsagePending = true
			state.funcNameUsageChangedOwners = []ast.Decl{ownerDecl}
			state.funcNameUsageForPost(decls)
		}
	})
}
