package semantic

import "testing"

// Discharge `ensure self.f == E` over struct fields the void body writes (the SetupRegions finding).
// The body writes `self.f <- V`; an `ensure self.f == V'` proves when the recorded V satisfies the
// clause (V == 0 for `self.f == 0`; both fields tracing to the same value expr for `self.a == self.b`).
// The fact is dropped at every mutation/borrow/mutating-callee of the root, keeping it sound.

func TestEnsureFieldWriteDischarges(t *testing.T) {
	src := `
struct Mgr:
    flexible_usage: mutable usize
    pool_budget: mutable usize
    total_flexible_size: usize
def SetupRegions(self: mutable Mgr&) -> void:
    ensure self.flexible_usage == 0
    ensure self.pool_budget == self.total_flexible_size
    self.flexible_usage <- 0
    self.pool_budget <- self.total_flexible_size
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "esfw_good.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("ensure over written struct fields should discharge, got:\n%s", allDiagnostics(r))
	}
}

func TestEnsureFieldWriteRejectsDifferentValue(t *testing.T) {
	// The body writes `self.flexible_usage <- 7`, but the postcondition claims `== 0`. The last write
	// is what stands, so the strict prover must reject (no false proof from the presence of a write).
	src := `
struct Mgr:
    flexible_usage: mutable usize
def SetupRegions(self: mutable Mgr&) -> void:
    ensure self.flexible_usage == 0
    self.flexible_usage <- 7
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "esfw_bad.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("a field written to a value different from the ensure claim must be rejected, got:\n%s", allDiagnostics(r))
	}
}

func TestEnsureFieldWriteDropsFactAcrossMutatingCallee(t *testing.T) {
	// `self` is passed to a mutating callee between the write and the exit. The callee may change the
	// field, so the written-field fact must drop — the ensure then cannot be proven (no false proof).
	src := `
struct Mgr:
    flexible_usage: mutable usize
def bump(self: mutable Mgr&) -> void:
    self.flexible_usage <- 9
def SetupRegions(self: mutable Mgr&) -> void:
    ensure self.flexible_usage == 0
    self.flexible_usage <- 0
    bump(self)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "esfw_callee.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("passing self to a mutating callee must drop the written-field fact, got:\n%s", allDiagnostics(r))
	}
}
