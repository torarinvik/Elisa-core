package semantic

import (
	"strings"
	"testing"
)

func TestExternEnsureAssumedAtCallSite(t *testing.T) {
	src := `
extern sqrt_floor(x: i64) -> i64 requires x >= 0 ensure result >= 0 and result * result <= x

def hypot_lower(a: i64) -> i64:
    requires a >= 0
    r: i64 = sqrt_floor(a)
    assert r >= 0
    return r
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "extern_ensure_assumed.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("extern ensure should be assumed downstream after requires discharge, got:\n%s", strings.Join(errs, "\n"))
	}
	var assumed int
	for _, fact := range result.ProofReport {
		if fact.Outcome == ProofAssumedExtern && fact.Class == ProofClassBoundary && strings.Contains(fact.Subject, "postcondition of extern sqrt_floor") {
			assumed++
		}
	}
	if assumed == 0 {
		t.Fatalf("expected extern ensure to be reported as a boundary assumption, got: %+v", result.ProofReport)
	}
}

func TestExternUsesContractExpandsBoundaryClauses(t *testing.T) {
	src := `
contract NonNegResult(x: i64):
    requires x >= 0
    ensure result >= 0

extern sqrt_floor(x: i64) -> i64 uses NonNegResult(x)

def hypot_lower(a: i64) -> i64:
    requires a >= 0
    r: i64 = sqrt_floor(a)
    assert r >= 0
    return r
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "extern_uses_contract.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("uses Contract on extern should expand requires/ensure boundary clauses, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestExternEnsureNotInjectedWhenRequiresViolated(t *testing.T) {
	src := `
extern sqrt_floor(x: i64) -> i64 requires x >= 0 ensure result >= 0

def bad() -> i64:
    r: i64 = sqrt_floor(-1)
    assert r >= 0
    return r
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "extern_ensure_bad_requires.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, "precondition of \"sqrt_floor\" is violated") {
		t.Fatalf("violated extern requires should be reported, got:\n%s", all)
	}
	for _, fact := range result.ProofReport {
		if fact.Outcome == ProofAssumedExtern {
			t.Fatalf("extern ensure must not be assumed when requires is violated, got report: %+v", result.ProofReport)
		}
	}
}
