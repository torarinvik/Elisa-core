package semantic

import "testing"

// Fix for the shadPS4 "memory-06" finding (and the AddressSpace accessor residuals): a postcondition
// `ensure result == E` whose body is `return E` for a pure place read E holds by reflexivity. The SMT
// lane otherwise fails it because two loads of a reference field are distinct opaque terms.
func TestEnsureResultEqualsReturnedFieldProvesByReflexivity(t *testing.T) {
	src := `
struct S:
    a: u64
    b: u64
def get_a(s: S&) -> u64:
    ensure result == s.a
    return s.a
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refl_ok.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("`ensure result == s.a` with `return s.a` should prove by reflexivity, got:\n%s", allDiagnostics(r))
	}
}

func TestEnsureResultEqualsMismatchedFieldStillFails(t *testing.T) {
	src := `
struct S:
    a: u64
    b: u64
def wrong(s: S&) -> u64:
    ensure result == s.b
    return s.a
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refl_bad.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("`ensure result == s.b` with `return s.a` must NOT be proven by reflexivity, got:\n%s", allDiagnostics(r))
	}
}
