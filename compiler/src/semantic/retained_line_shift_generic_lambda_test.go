//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

const retainedLineShiftMetadataPrefix = `def calculate(value: i64) -> i64:
    return value + 1

`

func retainedLineShiftAssertCertificateInvalidated(t *testing.T, state *RetainedAnalysis) {
	t.Helper()
	if !state.LastStats.FastPrepared || state.LastStats.ScratchPrepared {
		t.Fatalf("not a fast one-shot admission: %+v", state.LastStats)
	}
	if state.fastPrepareInvalidated == "" || state.classify != nil || state.appendOnly != nil || state.tables != nil || state.digestOf != nil {
		t.Fatal("line movement retained stale declaration certificate authority")
	}
}

func TestRetainedLineShiftFastMovesGenericNestedCallAndDefaultFacts(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	source := retainedLineShiftMetadataPrefix + `def bounce[T](value: T, depth: i64 = 0) -> T:
    return value

def first[T](value: T) -> T:
    return bounce(value)

def use(value: i64) -> i64:
    return first(value)
`
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	first := retainedProofNamedDecl(t, state.result, "first").(*ast.FuncDecl)
	call := first.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if len(call.ResolvedArgs) != 2 || !call.ResolvedArgsValid {
		t.Fatal("fixture has no real nested generic/default argument resolution")
	}
	defaultExpr := call.ResolvedArgs[1]
	if state.result.ExprTypes[call] == nil || state.result.ExprTypes[defaultExpr] == nil {
		t.Fatal("fixture lacks nested call/default type facts")
	}
	if analysis, ok := state.result.FunctionAnalysis(first); !ok || analysis == nil || analysis.CFG == nil {
		t.Fatal("fixture lacks generic function analysis")
	}
	oldCallLine, oldDefaultLine := call.Pos().Line, defaultExpr.Pos().Line
	after := strings.Replace(source, "    return value + 1", "    if value < 3:\n        return 0\n    return value + 1", 1)
	// The shared helper compares every canonical published metadata field and
	// diagnostics against an independent cold analysis, and requires zero scratch
	// parses. Only the earlier plain function changes; all generic text moves.
	warm := retainedLineShiftLazyUpdate(t, state, after, 0)
	retainedLineShiftAssertCertificateInvalidated(t, state)
	if len(call.ResolvedArgs) != 2 || call.ResolvedArgs[1] != defaultExpr || warm.ExprTypes[call] == nil || warm.ExprTypes[defaultExpr] == nil {
		t.Fatal("moved nested call/default facts were detached or dropped")
	}
	if call.Pos().Line != oldCallLine+2 || defaultExpr.Pos().Line != oldDefaultLine+2 {
		t.Fatal("moved call/default coordinates were not relocated exactly once")
	}
}

func TestRetainedLineShiftFastMovesLambdaAndFunctionAnalysisMetadata(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	source := retainedLineShiftMetadataPrefix + `def apply(f: fn(i64) -> i64, value: i64) -> i64:
    return f(value)

def use(offset: i64) -> i64:
    return apply(fn(n) => n + offset, 3)
`
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	use := retainedProofNamedDecl(t, state.result, "use").(*ast.FuncDecl)
	call := use.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	lambda, ok := call.Args[0].(*ast.LambdaExpr)
	if !ok {
		t.Fatal("fixture has no actual lambda argument")
	}
	info := state.result.Lambdas[lambda]
	if info == nil || len(info.Captures) != 1 || info.Captures[0] != "offset" || state.result.ExprTypes[lambda] == nil {
		t.Fatal("fixture lacks real captured lambda metadata/type")
	}
	analysis, ok := state.result.FunctionAnalysis(use)
	if !ok || analysis == nil || analysis.CFG == nil {
		t.Fatal("fixture lacks function-analysis CFG metadata")
	}
	oldLine := lambda.Pos().Line
	after := strings.Replace(source, "    return value + 1", "    if value < 3:\n        return 0\n    return value + 1", 1)
	warm := retainedLineShiftLazyUpdate(t, state, after, 0)
	retainedLineShiftAssertCertificateInvalidated(t, state)
	movedInfo := warm.Lambdas[lambda]
	if movedInfo == nil || len(movedInfo.Captures) != 1 || movedInfo.Captures[0] != "offset" || warm.ExprTypes[lambda] == nil {
		t.Fatal("moved captured lambda metadata/type lost")
	}
	if current, ok := warm.FunctionAnalysis(use); !ok || current == nil || current.CFG == nil {
		t.Fatal("moved function-analysis metadata lost")
	}
	if lambda.Pos().Line != oldLine+2 {
		t.Fatal("lambda coordinates were not relocated exactly once")
	}
}
