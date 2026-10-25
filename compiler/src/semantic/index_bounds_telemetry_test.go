//go:build cgo

package semantic

import (
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func assertLiveBoundsTelemetry(t *testing.T, state *RetainedAnalysis, source string) {
	t.Helper()
	cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if len(cold.Errors()) != 0 || len(state.result.Errors()) != 0 {
		t.Fatalf("cold=%v warm=%v", cold.Errors(), state.result.Errors())
	}
	if !reflect.DeepEqual(cold.ElisionSummary, state.result.ElisionSummary) {
		t.Fatalf("elision summary differs: cold=%+v warm=%+v", cold.ElisionSummary, state.result.ElisionSummary)
	}
	if retainedDiagnostics(cold) != retainedDiagnostics(state.result) {
		t.Fatal("live bounds diagnostics differ from cold")
	}
	if state.result.ElisionSummary.ArrayBounds.Runtime != len(state.a.indexBoundsRuntimeSites) {
		t.Fatal("published runtime count is not the live distinct-site count")
	}
	for expr, site := range state.a.indexBoundsRuntimeSites {
		if expr == nil || site.Owner == nil {
			t.Fatal("runtime site lacks exact lexical owner")
		}
		if state.a.indexBoundsProven[expr] {
			t.Fatal("one site counted both proven and runtime")
		}
	}
}
func updateLiveBoundsTelemetry(t *testing.T, state *RetainedAnalysis, source string) *RetainedAnalysis {
	t.Helper()
	if _, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source)); err != nil {
		t.Fatal(err)
	}
	assertLiveBoundsTelemetry(t, state, source)
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	assertLiveBoundsTelemetry(t, restored, source)
	return restored
}

func TestLiveBoundsTelemetryRetractsAddsRevertsAndRemovesSites(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const first = `def first(xs: darray[i32]&, i: usize) -> i32:
    if i < xs.count:
        return xs[i]
    return 0

`
	const second = `def second(xs: darray[i32]&, i: usize) -> i32:
    if i < xs.count:
        return xs[i]
    return 0
`
	source := first + second
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	assertLiveBoundsTelemetry(t, state, source)
	if result.ElisionSummary.ArrayBounds != (ProofElisionCounts{Elided: 2}) {
		t.Fatalf("fixture bounds=%+v", result.ElisionSummary.ArrayBounds)
	}
	oldFn := retainedProofNamedDecl(t, result, "first").(*ast.FuncDecl)
	oldIndex := oldFn.Body[0].(*ast.IfStmt).Then[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
	changed := strings.Replace(source, "if i < xs.count:", "if i > xs.count:", 1)
	state = updateLiveBoundsTelemetry(t, state, changed)
	if _, exists := state.a.indexBoundsRuntimeSites[oldIndex]; exists {
		t.Fatal("detached old proof became a runtime telemetry site")
	}
	if state.result.ElisionSummary.ArrayBounds != (ProofElisionCounts{Elided: 1, Runtime: 1}) {
		t.Fatalf("guard reversal bounds=%+v", state.result.ElisionSummary.ArrayBounds)
	}
	state = updateLiveBoundsTelemetry(t, state, source)
	if state.result.ElisionSummary.ArrayBounds != (ProofElisionCounts{Elided: 2}) {
		t.Fatal("revert did not retract runtime site")
	}
	state = updateLiveBoundsTelemetry(t, state, strings.Replace(second, "if i < xs.count:", "if i > xs.count:", 1))
	if state.result.ElisionSummary.ArrayBounds != (ProofElisionCounts{Runtime: 1}) {
		t.Fatal("removed declaration left proof/runtime telemetry")
	}
	state = updateLiveBoundsTelemetry(t, state, second)
	if state.result.ElisionSummary.ArrayBounds != (ProofElisionCounts{Elided: 1}) {
		t.Fatal("surviving declaration revert left stale runtime telemetry")
	}
}

func TestLiveBoundsTelemetryParallelSnapshotIsSiteBased(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "4")
	template := `def NAME(xs: darray[i32]&, i: usize) -> i32:
    return xs[i]

`
	source := ""
	for _, name := range []string{"first", "second", "third", "fourth"} {
		source += strings.Replace(template, "NAME", name, 1)
	}
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	assertLiveBoundsTelemetry(t, state, source)
	for _, site := range state.a.indexBoundsRuntimeSites {
		fn, ok := site.Owner.(*ast.FuncDecl)
		if !ok {
			t.Fatal("parallel runtime site lacks a source function")
		}
		symbol, present := result.GlobalScope.Lookup(fn.Name)
		if !present || symbol.Node != fn {
			t.Fatal("parallel runtime owner is a foreign/private shadow")
		}
	}
	if result.ElisionSummary.ArrayBounds.Runtime != 4 {
		t.Fatalf("worker visits counted as sites: %+v", result.ElisionSummary.ArrayBounds)
	}
	state = updateLiveBoundsTelemetry(t, state, strings.Replace(source, "return xs[i]", "if i < xs.count:\n        return xs[i]\n    return 0", 1))
	if state.result.ElisionSummary.ArrayBounds != (ProofElisionCounts{Elided: 1, Runtime: 3}) {
		t.Fatal("restored parallel ledger did not update exactly one owner")
	}
}

func TestLiveBoundsTelemetryDefaultHeaderAndLambdaOwners(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const prefix = `global values: i32[2] = [11, 22]
global cursor: usize = 0

def keep() -> i32:
    return 1

`
	const defaultFn = `def consume(value: i32 = values[cursor]) -> i32:
    return value
`
	source := prefix + defaultFn
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	owner := retainedProofNamedDecl(t, result, "consume")
	found := false
	for _, site := range state.a.indexBoundsRuntimeSites {
		if site.Owner == owner && site.Header && site.Origin == owner {
			found = true
		}
	}
	if !found {
		t.Fatal("actual unproved parameter default has no exact header runtime owner")
	}
	changed := strings.Replace(source, "return 1", "return 2", 1)
	state = updateLiveBoundsTelemetry(t, state, changed)
	state = updateLiveBoundsTelemetry(t, state, strings.Replace(prefix, "return 1", "return 2", 1))
	if state.result.ElisionSummary.ArrayBounds.Runtime != 0 {
		t.Fatal("removed default owner left runtime site")
	}

	lambdaSource := strings.Replace(retainedProofLambdaSource, "if index < values.count", "if index > values.count", 1)
	result, state = AnalyzeRetained(retainedParse(t, lambdaSource), retainedParse(t, lambdaSource), []byte(lambdaSource), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	lambdaOwner := retainedProofNamedDecl(t, result, "read")
	if len(state.a.indexBoundsRuntimeSites) == 0 {
		t.Fatal("actual lambda has no unproved index")
	}
	for _, site := range state.a.indexBoundsRuntimeSites {
		if site.Owner != lambdaOwner || site.Header {
			t.Fatal("lambda runtime site escaped lexical source function")
		}
	}
	state = updateLiveBoundsTelemetry(t, state, retainedProofLambdaSource)
	if state.result.ElisionSummary.ArrayBounds.Runtime != 0 {
		t.Fatal("lambda proof did not retract runtime telemetry")
	}
}

func TestLiveBoundsTelemetryNonBoundsEarlyPathsAndFallbackMeaning(t *testing.T) {
	const source = `struct Flags[T]:
    bits: u64
const enum RoutineFlag of u8:
    External
    Forward

def member(value: Flags[RoutineFlag]&) -> bool:
    return value[RoutineFlag.External]

def lookup(value: dict[i64, i64]&, key: i64) -> i64&?:
    return value[key]

def recovered(xs: darray[i32]&, index: usize) -> i32:
    return get xs[index] else 0
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	assertLiveBoundsTelemetry(t, state, source)
	if result.ElisionSummary.ArrayBounds.Runtime != 0 {
		t.Fatal("flags/dict/fallback counted as ordinary panic guards")
	}
	if result.ElisionSummary.ArrayBounds.Elided == 0 {
		t.Fatal("explicit fallback no longer has its historical proof authorization")
	}
}

func TestLiveBoundsTelemetryGenericIndexIsNotArrayAccess(t *testing.T) {
	const source = `def first[T](value: T) -> T:
    return value

def caller(value: i64) -> i64:
    return first[i64](value)
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	assertLiveBoundsTelemetry(t, state, source)
	if result.ElisionSummary.ArrayBounds != (ProofElisionCounts{}) {
		t.Fatal("function specialization index counted as bounds access")
	}
}

func TestLiveBoundsTelemetryUnknownOwnershipDeclinesUpdate(t *testing.T) {
	const source = "def value() -> i64:\n    return 1\n"
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	// An incompatible/malformed persisted row cannot masquerade as a legitimate
	// global site. This is an admission corruption control, not telemetry input.
	state.a.indexBoundsRuntimeSites = map[*ast.IndexExpr]indexBoundsRuntimeSite{&ast.IndexExpr{}: {}}
	next := strings.Replace(source, "return 1", "return 2", 1)
	if _, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err == nil {
		t.Fatal("runtime site without lexical owner admitted")
	}
	if state.indexProofsUpdating || state.indexProofsByFunc != nil || state.indexProofsByDecl != nil {
		t.Fatal("declined runtime admission retained invocation resources")
	}
}
