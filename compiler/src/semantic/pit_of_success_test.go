package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestPitOfSuccessSynthesizesReserveForCountingFill(t *testing.T) {
	file := analyzeAndGetFile(t, `def build(n: usize) -> usize:
    xs: mutable darray[i64] = []
    gap: i64 = 0
    for i in 0..<n:
        xs.push(i.i64() + gap)
    return xs.count
`)
	loop := firstForStmt(file)
	if loop == nil {
		t.Fatal("expected counting loop")
	}
	if !ast.HasSynthesizedPreReserveStmts(loop) {
		t.Fatal("expected compiler-synthesized reserve prelude on counting fill")
	}
}

func TestPitOfSuccessUninferredReserveWarnsThenPerfStrictErrors(t *testing.T) {
	src := `def build(src: darray[darray[i64]]&) -> usize:
    xs: mutable darray[i64] = []
    for chunk in src:
        for x in chunk:
            xs.push(x)
    return xs.count
`
	warnResult := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "pit_uninferred_reserve_warn.elisa", src, AnalyzeOptions{})
	warnAll := allDiagnostics(warnResult)
	if !strings.Contains(warnAll, "cannot infer a safe reserve bound") {
		t.Fatalf("expected uninferred reserve warning, got:\n%s", warnAll)
	}
	if len(warnResult.Errors()) != 0 {
		t.Fatalf("default uninferred reserve friction should be a warning, got errors:\n%v", warnResult.Errors())
	}

	strictResult := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "pit_uninferred_reserve_error.elisa", src, AnalyzeOptions{EnforcePerfLints: true})
	strictAll := allDiagnostics(strictResult)
	if !strings.Contains(strictAll, "cannot infer a safe reserve bound") {
		t.Fatalf("expected strict uninferred reserve error, got:\n%s", strictAll)
	}
	if len(strictResult.Errors()) == 0 {
		t.Fatal("performance-strict uninferred reserve friction should be an error")
	}
}

func TestPitOfSuccessKeepsUncheckedIndexStrictAsError(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "pit_unchecked_index_error.elisa", `def f(xs: darray[i64]&, i: usize) -> i64:
    return xs[i]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, "unchecked index requires") {
		t.Fatalf("expected unchecked index strict error, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatal("strict unchecked index friction should be an error")
	}
}
