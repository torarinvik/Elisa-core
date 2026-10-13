package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS: the constant folder keeps int64 bit patterns, so a u64 constant >= 2^63 arrives
// negative. Emitted verbatim it contradicted the variable's unsigned range and made every
// obligation in the query vacuously provable (here a false `ensure result == x + 1`).
func TestHighU64ConstantDoesNotMakeHypothesesVacuous(t *testing.T) {
	strict := func(name, src string) []string {
		t.Helper()
		return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true}).Errors()
	}
	src := `
const BIG: u64 = 14695981039346656037

def f(x: u64) -> u64:
    ensure result == x + 1
    h: mutable u64 = BIG
    h <- BIG
    return x
`
	if errs := strict("high_u64.elisa", src); !strings.Contains(strings.Join(errs, "\n"), "could not be proven") {
		t.Fatalf("a false ensure next to a high u64 constant must be rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	ok := `
const BIG: u64 = 14695981039346656037

def g() -> u64:
    ensure result == BIG
    ensure result > 9223372036854775807
    h: u64 = BIG
    return h
`
	if errs := strict("high_u64_ok.elisa", ok); len(errs) != 0 {
		t.Fatalf("a high u64 constant should translate at its unsigned value, got:\n%s", strings.Join(errs, "\n"))
	}
}
