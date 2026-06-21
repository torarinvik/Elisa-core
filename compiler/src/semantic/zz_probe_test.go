package semantic

import (
	"strings"
	"testing"
)

func TestProbeEmptyLambda(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "probe_empty_lambda.elisa", `
def applyOne[errorset R](f: func(i64) -> i64 error[R], x: i64) -> i64 error[R]:
    return try f(x)

def use() -> i64:
    return applyOne(lambda(n) => n + 1, 3)
`)
	all := allDiagnostics(result)
	if strings.TrimSpace(all) != "" {
		t.Fatalf("PROBE empty lambda failed:\n%s", all)
	}
}

func TestProbeRaisingLambda(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "probe_raising_lambda.elisa", `
error IoErr:
    Bad

def applyOne[errorset R](f: func(i64) -> i64 error[R], x: i64) -> i64 error[R]:
    return try f(x)

def use() -> i64:
    catch applyOne(lambda(n) => raise IoErr.Bad, 3):
        v:
            return v
        IoErr.Bad:
            return 99
`)
	all := allDiagnostics(result)
	if strings.TrimSpace(all) != "" {
		t.Fatalf("PROBE raising lambda failed:\n%s", all)
	}
}
