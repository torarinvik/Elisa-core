package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"strings"
	"testing"
)

const retainedBorrowSummaryPrefix = `struct BorrowedBox:
    value: i64&
global root: i64 = 9
`
const retainedBorrowSummaryPack = `
def pack(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: value}
`

func checkRetainedSummaryRevisions(t *testing.T, source string) {
	t.Helper()
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	changed := strings.Replace(source, "BorrowedBox{value: value}", "BorrowedBox{value: &root}", 1)
	if len(source) != len(changed) || source == changed {
		t.Fatal("invalid same-width edit")
	}
	for step, current := range []string{changed, source, changed} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		warm, err := state.Update(retainedParse(t, current), retainedParse(t, current), []byte(current))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, current), AnalyzeOptions{})
		wantEscape := current == source
		if strings.Contains(retainedDiagnostics(cold), "function-local storage") != wantEscape {
			t.Fatalf("cold oracle has wrong escape outcome at revision %d: %s", step, retainedDiagnostics(cold))
		}
		if strings.Contains(retainedDiagnostics(warm), "function-local storage") != wantEscape {
			t.Fatalf("retained escape outcome is stale at revision %d: %s", step, retainedDiagnostics(warm))
		}
		logRetainedFunctionTypeDifferences(t, cold, warm)
		logRetainedFunctionAnalysisDifferences(t, cold, warm)
		logRetainedExpressionFactDifferences(t, cold, warm)
		for label, result := range map[string]*Result{"cold": cold, "retained": warm} {
			for _, decl := range retainedWalkDecls(result.File) {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || (fn.Name != "cycle_a" && fn.Name != "cycle_b" && fn.Name != "relay") {
					continue
				}
				ft := result.analyzer.funcDeclSymbols[fn].Type.(*FuncType)
				if wantEscape && ft.ReturnIsolationKnown && ft.ReturnIsolation.Isolated {
					t.Fatalf("%s falsely claims isolated param-borrow result: %s revision %d", label, fn.Name, step)
				}
			}
		}
		t.Logf("checking revision %d", step)
		assertRetainedLiveResultParity(t, cold, warm)
	}
}

func logRetainedFunctionAnalysisDifferences(t *testing.T, cold, warm *Result) {
	t.Helper()
	functions := func(result *Result) map[string]*ast.FuncDecl {
		out := map[string]*ast.FuncDecl{}
		for _, decl := range retainedWalkDecls(result.File) {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				out[fn.Name] = fn
			}
		}
		return out
	}
	left, right := functions(cold), functions(warm)
	for name, coldFn := range left {
		warmFn := right[name]
		if warmFn == nil {
			continue
		}
		coldAnalysis, warmAnalysis := cold.FunctionAnalyses[coldFn], warm.FunctionAnalyses[warmFn]
		if coldAnalysis == nil || warmAnalysis == nil {
			continue
		}
		coldFields, warmFields := reflect.ValueOf(coldAnalysis).Elem(), reflect.ValueOf(warmAnalysis).Elem()
		for index := 0; index < coldFields.NumField(); index++ {
			if !reflect.DeepEqual(coldFields.Field(index).Interface(), warmFields.Field(index).Interface()) {
				t.Logf("%s FunctionAnalysis.%s differs: cold=%#v warm=%#v", name, coldFields.Type().Field(index).Name, coldFields.Field(index).Interface(), warmFields.Field(index).Interface())
			}
		}
	}
}

func logRetainedFunctionTypeDifferences(t *testing.T, cold, warm *Result) {
	t.Helper()
	functions := func(result *Result) map[string]*FuncType {
		out := map[string]*FuncType{}
		for _, decl := range retainedWalkDecls(result.File) {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			sym := result.analyzer.funcDeclSymbols[fn]
			if sym != nil {
				if functionType, ok := sym.Type.(*FuncType); ok {
					out[fn.Name] = functionType
				}
			}
		}
		return out
	}
	left, right := functions(cold), functions(warm)
	for name, leftType := range left {
		rightType := right[name]
		if leftType == nil || rightType == nil {
			continue
		}
		leftFields, rightFields := reflect.ValueOf(leftType).Elem(), reflect.ValueOf(rightType).Elem()
		for index := 0; index < leftFields.NumField(); index++ {
			if !reflect.DeepEqual(leftFields.Field(index).Interface(), rightFields.Field(index).Interface()) {
				t.Logf("%s FuncType.%s differs: cold=%#v warm=%#v", name, leftFields.Type().Field(index).Name, leftFields.Field(index).Interface(), rightFields.Field(index).Interface())
			}
		}
	}
}

func TestRetainedReturnSummaryChangesInBothDirections(t *testing.T) {
	checkRetainedSummaryRevisions(t, retainedBorrowSummaryPrefix+retainedBorrowSummaryPack+`
def relay(value: i64&) -> BorrowedBox:
    return pack(value)
def escapes() -> BorrowedBox:
    local: i64 = 1
    return relay(&local)
`)
}

func TestRetainedReturnSummaryCycleSourceOrders(t *testing.T) {
	a := `
def cycle_a(value: i64&, count: i64) -> BorrowedBox:
    if count <= 0:
        return pack(value)
    return cycle_b(value, count - 1)
`
	b := `
def cycle_b(value: i64&, count: i64) -> BorrowedBox:
    return cycle_a(value, count)
`
	caller := `
def escapes() -> BorrowedBox:
    local: i64 = 1
    return cycle_a(&local, 2)
`
	for name, parts := range map[string]string{"callee-first": retainedBorrowSummaryPack + a + b + caller, "callee-last": b + a + caller + retainedBorrowSummaryPack, "caller-first": caller + b + retainedBorrowSummaryPack + a} {
		t.Run(name, func(t *testing.T) { checkRetainedSummaryRevisions(t, retainedBorrowSummaryPrefix+parts) })
	}
}

func TestRetainedUnchangedInferredSummaryKeepsCallerAnalysis(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := retainedBorrowSummaryPrefix + strings.Replace(retainedBorrowSummaryPack, "    return BorrowedBox", "    marker: i64 = 1\n    return BorrowedBox", 1) + `
def relay(value: i64&) -> BorrowedBox:
    return pack(value)
def unrelated() -> i64:
    return 42
`
	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	functions := map[string]*ast.FuncDecl{}
	for _, decl := range retainedWalkDecls(state.a.file) {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			functions[fn.Name] = fn
		}
	}
	packCFG := state.a.functionAnalyses[functions["pack"]].CFG
	callerCFG := state.a.functionAnalyses[functions["relay"]].CFG
	otherCFG := state.a.functionAnalyses[functions["unrelated"]].CFG
	changed := strings.Replace(source, "marker: i64 = 1", "marker: i64 = 2", 1)
	warm, err := state.Update(retainedParse(t, changed), retainedParse(t, changed), []byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	if state.LastStats.Changed != 1 || state.a.functionAnalyses[functions["pack"]].CFG == packCFG {
		t.Fatal("edited body was not analyzed")
	}
	if state.a.functionAnalyses[functions["relay"]].CFG != callerCFG || state.a.functionAnalyses[functions["unrelated"]].CFG != otherCFG {
		t.Fatal("unchanged summary reanalyzed a caller or unrelated function")
	}
	assertRetainedLiveResultParity(t, AnalyzeWithOptions(retainedParse(t, changed), AnalyzeOptions{}), warm)
}

// A newly introduced edge closes a cycle that the restored dependency graph did
// not contain. Removing it again must also remove the old caller dependency.
func TestRetainedReturnSummaryCycleTopologyChanges(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	source := retainedBorrowSummaryPrefix + `
def cycle_a(value: i64&, count: i64) -> BorrowedBox:
    if count <= 0:
        return BorrowedBox{value: &root}
    return BorrowedBox{value: &root}
def cycle_b(value: i64&, count: i64) -> BorrowedBox:
    return cycle_a(value, count)
def escapes() -> BorrowedBox:
    local: i64 = 1
    return cycle_b(&local, 2)
`
	changed := strings.Replace(source, "return BorrowedBox{value: &root}", "return BorrowedBox{value: value}", 1)
	changed = strings.Replace(changed, "return BorrowedBox{value: &root}", "return cycle_b(value, count - 1)", 1)
	if len(source) != len(changed) {
		t.Fatal("invalid same-width topology edit")
	}
	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	for step, current := range []string{changed, source, changed} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		warm, err := state.Update(retainedParse(t, current), retainedParse(t, current), []byte(current))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, current), AnalyzeOptions{})
		wantEscape := current == changed
		for label, result := range map[string]*Result{"cold": cold, "retained": warm} {
			if strings.Contains(retainedDiagnostics(result), "function-local storage") != wantEscape {
				t.Fatalf("%s escape outcome wrong after topology revision %d: %s", label, step, retainedDiagnostics(result))
			}
		}
		t.Logf("checking topology revision %d", step)
		logRetainedFunctionTypeDifferences(t, cold, warm)
		logRetainedFunctionAnalysisDifferences(t, cold, warm)
		logRetainedExpressionFactDifferences(t, cold, warm)
		assertRetainedLiveResultParity(t, cold, warm)
	}
}

func logRetainedExpressionFactDifferences(t *testing.T, cold, warm *Result) {
	t.Helper()
	t.Logf("expression fact map counts cold=%d nil=%v warm=%d nil=%v", len(cold.ExprFacts), cold.ExprFacts == nil, len(warm.ExprFacts), warm.ExprFacts == nil)
	mapping := map[PositionedFunctionNode]PositionedFunctionNode{}
	mapIncrementalCurrentAST(t, reflect.ValueOf(cold.File), reflect.ValueOf(warm.File), mapping)
	for node, current := range mapping {
		left, ok := node.(ast.Expr)
		if !ok {
			continue
		}
		right := current.(ast.Expr)
		cf, ch := cold.ExprFacts[left]
		wf, wh := warm.ExprFacts[right]
		cf.base = wf.base // alias identity parity remains checked by the strict helper
		if ch != wh || !reflect.DeepEqual(cf, wf) {
			t.Logf("expression facts differ at %T %v: cold(present=%v)=%#v warm(present=%v)=%#v", left, node.Pos(), ch, cf, wh, wf)
		}
	}
}

func TestRetainedReturnRegionPolymorphismChangesFallBack(t *testing.T) {
	prefix := "struct Box:\n    value: i64\ndef build(seed: Box&) -> Box&:\n"
	borrowed := prefix + "    return seed\n"
	allocated := prefix + `    can Memory.Allocate, Memory.Release, Abort.Panic:
        local: Box& = new[auto] Box{value: 7}
        return local
`
	for _, pair := range []struct {
		name, before, after string
		wantPoly            bool
	}{
		{"borrow-to-auto", borrowed, allocated, true},
		{"auto-to-borrow", allocated, borrowed, false},
	} {
		t.Run(pair.name, func(t *testing.T) {
			t.Setenv("ELISA_PARALLEL_BODIES", "0")
			_, state := AnalyzeRetained(retainedParse(t, pair.before), retainedParse(t, pair.before), []byte(pair.before), AnalyzeOptions{})
			data, err := state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
			_, err = state.Update(retainedParse(t, pair.after), retainedParse(t, pair.after), []byte(pair.after))
			if err == nil || !strings.Contains(err.Error(), "declaration results") {
				t.Fatalf("expected cold fallback for changed implicit region signature, got %v", err)
			}
			cold := AnalyzeWithOptions(retainedParse(t, pair.after), AnalyzeOptions{})
			if errs := cold.Errors(); len(errs) != 0 {
				t.Fatalf("fresh analysis failed: %v", errs)
			}
			if got := regionPolymorphicFuncType(t, cold, "build").RegionPolymorphic; got != pair.wantPoly {
				t.Fatalf("fresh RegionPolymorphic=%v, want %v", got, pair.wantPoly)
			}
		})
	}
}
