//go:build cgo

package semantic

import (
	"bytes"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

const retainedClassificationFixture = `packed enum Expr:
    Int(value: i64)
    Pair(left: Expr, right: Expr)

def observe(node: Expr) -> i64:
    return 0

def work() -> i64:
    region r(4096):
        node: Expr = new[auto] Expr.Int(7)
        value: i64 = 0
        return value

def main() -> i64:
    return work()
`

func TestRetainedRejectsBodyStoreClassificationChangeBeforeMutation(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	options := AnalyzeOptions{}
	initial, state := AnalyzeRetained(retainedParse(t, retainedClassificationFixture), retainedParse(t, retainedClassificationFixture), []byte(retainedClassificationFixture), options)
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	work := retainedClassificationDecl(t, state, "work")
	beforeBody := unparse.FormatDecl(work)
	changed := strings.Replace(retainedClassificationFixture, "value: i64 = 0", "value: i64 = observe(node)", 1)
	result, err := state.Update(retainedParse(t, changed), retainedParse(t, changed), []byte(changed))
	if err == nil || result != nil || !strings.Contains(err.Error(), "storeNeeds") {
		t.Fatalf("classification-changing body edit must rebuild immediately: result=%v err=%v stats=%+v", result != nil, err, state.LastStats)
	}
	if state.LastStats.FastPrepared || !strings.Contains(state.LastStats.FastDeclined, "declaration-level facts") {
		t.Fatalf("fixture did not exercise scratch-backed comparison: %+v", state.LastStats)
	}
	if !bytes.Equal(state.src, []byte(retainedClassificationFixture)) || unparse.FormatDecl(work) != beforeBody {
		t.Fatal("classification rejection mutated the live baseline")
	}
	// Update rejected the new source, so the old lease still names its old source.
	// Simulate the driver's fresh fallback, which establishes a new qualified lease.
	fresh, state := AnalyzeRetained(retainedParse(t, changed), retainedParse(t, changed), []byte(changed), options)
	if len(fresh.Errors()) != 0 || state.fastPrepareInvalidated != "" {
		t.Fatalf("fresh fallback invalid: %v lease=%q", fresh.Errors(), state.fastPrepareInvalidated)
	}
	cold := AnalyzeWithOptions(retainedParse(t, changed), options)
	if retainedDiagnostics(fresh) != retainedDiagnostics(cold) {
		t.Fatal("fallback lost the original new[auto] deprecation diagnostic")
	}
	assertRetainedCanonicalLiveResultParity(t, cold, fresh)
	state = reloadRetainedFastPrepareForTest(t, state)
	second := strings.Replace(changed, "return work()", "return work() + 0", 1)
	warm, err := state.Update(retainedParse(t, second), retainedParse(t, second), []byte(second))
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastStats.FastPrepared {
		t.Fatalf("fresh classification did not requalify fast preparation: %+v", state.LastStats)
	}
	cold = AnalyzeWithOptions(retainedParse(t, second), options)
	if retainedDiagnostics(warm) != retainedDiagnostics(cold) {
		t.Fatalf("requalified edit changed diagnostics: warm=%s cold=%s", retainedDiagnostics(warm), retainedDiagnostics(cold))
	}
	assertRetainedCanonicalLiveResultParity(t, cold, warm)
}

func TestRetainedScratchBodyEditPreservesLeaseWhenClassificationUnchanged(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	const pure = `
def pure_a(value: i64) -> i64:
    return value

def pure_b(value: i64) -> i64:
    return value

def calculate() -> i64:
    return pure_a(1)
`
	before := retainedClassificationFixture + pure
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	state = reloadRetainedFastPrepareForTest(t, state)
	changed := strings.Replace(before, "return pure_a(1)", "return pure_b(1)", 1)
	warm, err := state.Update(retainedParse(t, changed), retainedParse(t, changed), []byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	if state.LastStats.FastPrepared || !strings.Contains(state.LastStats.FastDeclined, "declaration-level facts") || state.fastPrepareInvalidated != "" {
		t.Fatalf("unchanged-classification scratch edit lost its lease: %+v lease=%q", state.LastStats, state.fastPrepareInvalidated)
	}
	cold := AnalyzeWithOptions(retainedParse(t, changed), AnalyzeOptions{})
	if retainedDiagnostics(warm) != retainedDiagnostics(cold) {
		t.Fatal("control scratch diagnostics differ")
	}
	assertRetainedCanonicalLiveResultParity(t, cold, warm)
	state = reloadRetainedFastPrepareForTest(t, state)
	next := strings.Replace(changed, "return pure_b(1)", "return pure_b(2)", 1)
	warm, err = state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastStats.FastPrepared {
		t.Fatalf("unchanged classification unnecessarily disabled fast path: %+v", state.LastStats)
	}
	cold = AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{})
	if retainedDiagnostics(warm) != retainedDiagnostics(cold) {
		t.Fatal("control fast diagnostics differ")
	}
	assertRetainedCanonicalLiveResultParity(t, cold, warm)
}

func retainedClassificationDecl(t *testing.T, state *RetainedAnalysis, name string) *ast.FuncDecl {
	t.Helper()
	for _, sd := range state.decls {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("missing function %s", name)
	return nil
}
