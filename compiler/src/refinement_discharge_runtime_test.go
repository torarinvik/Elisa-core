package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

// buildRunRefinement compiles `program` at `opt` and returns (output, runErr).
func buildRunRefinement(t *testing.T, program string, opt backend.OptimizationLevel) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	if _, err := os.Stat(std); err != nil {
		t.Skipf("std runtime not found: %v", err)
	}
	dir := t.TempDir()
	rel, err := filepath.Rel(dir, std)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	src := "# include \"" + filepath.ToSlash(rel) + "\"\n" + program
	fixture := filepath.Join(dir, "refine.elisa")
	if err := os.WriteFile(fixture, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stderr bytes.Buffer
	expanded, err := readSourceWithIncludes(fixture, map[string]bool{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	_, result, ok := analyzeProgram(fixture, expanded, &stderr)
	if !ok {
		t.Fatalf("analyze failed:\n%s", stderr.String())
	}
	exe, cleanup, err := buildNativeExecutable(result, nil, nil, "", opt, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, stderr.String())
	}
	defer cleanup()
	out, runErr := exec.Command(exe).CombinedOutput()
	return strings.TrimSpace(string(out)), runErr
}

const refinementProgram = `
law Positive(self: i64) = self > 0

def neg() -> i64:
    return 0 - 3

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    x: i64 is Positive = %s
    print(x.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

const callArgRefinementProgram = `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def neg() -> i64:
    return 0 - 3

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    n: i64 = %s
    r: i64 = needs_pos(n)
    print(r.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

// An unproven-but-satisfying call argument passes the debug call-site check and runs normally.
func TestCallArgRefinementSatisfiedPasses(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(callArgRefinementProgram, "%s", "5", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("satisfying call-arg refinement should run cleanly: %v (out=%q)", err, out)
	}
	if out != "5" {
		t.Fatalf("want output 5, got %q", out)
	}
}

// A violating call argument TRAPS at debug — the function-contract boundary is enforced at the call.
func TestCallArgRefinementViolatedTrapsInDebug(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(callArgRefinementProgram, "%s", "neg()", 1), backend.OptimizationLevel0)
	if err == nil {
		t.Fatalf("violating call-arg refinement must trap at debug, but program exited 0 (out=%q)", out)
	}
}

const lawIsNarrowProgram = `
law Positive(self: i64) = self > 0
law Nat(self: i64) = self >= 0

def needs_nat(x: i64 is Nat) -> i64:
    return x

def f(n: i64) -> i64:
    if n is Positive:
        return needs_nat(n)
    return 0

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    print(f(%s).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

// `if n is Positive:` narrows n so `needs_nat(n)` is statically proven (no runtime check) AND the
// program runs correctly end-to-end through the narrowed branch.
func TestLawIsNarrowingRunsEndToEnd(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(lawIsNarrowProgram, "%s", "7", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("narrowed branch should run cleanly: %v (out=%q)", err, out)
	}
	if out != "7" {
		t.Fatalf("want 7 (narrowed branch returns needs_nat(7)), got %q", out)
	}
}

const returnRefinementProgram = `
law Positive(self: i64) = self > 0

def passthru(n: i64) -> i64 is Positive:
    return n

def neg() -> i64:
    return 0 - 3

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    r: i64 = passthru(%s)
    print(r.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

// An unproven-but-satisfying return passes the debug return-check and runs normally.
func TestReturnRefinementSatisfiedPasses(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(returnRefinementProgram, "%s", "5", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("satisfying return refinement should run cleanly: %v (out=%q)", err, out)
	}
	if out != "5" {
		t.Fatalf("want output 5, got %q", out)
	}
}

// A violating return TRAPS at debug — the return half of the function-contract boundary is enforced.
func TestReturnRefinementViolatedTrapsInDebug(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(returnRefinementProgram, "%s", "neg()", 1), backend.OptimizationLevel0)
	if err == nil {
		t.Fatalf("violating return refinement must trap at debug, but program exited 0 (out=%q)", out)
	}
}

// A satisfying init passes the debug discharge check and runs normally.
func TestRefinementDischargeSatisfiedPasses(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(refinementProgram, "%s", "5", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("satisfying refinement should run cleanly: %v (out=%q)", err, out)
	}
	if out != "5" {
		t.Fatalf("want output 5, got %q", out)
	}
}

// A violating init TRAPS at debug — the refinement is enforced.
func TestRefinementDischargeViolatedTrapsInDebug(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(refinementProgram, "%s", "neg()", 1), backend.OptimizationLevel0)
	if err == nil {
		t.Fatalf("violating refinement must trap at debug, but program exited 0 (out=%q)", out)
	}
}

// The same violating init is ELIDED at release — "debug verifies what release assumes."
// -fbounds-check (ELISACORE_FORCE_BOUNDS_CHECK) legitimately forces the check even in release, so
// clear it here to assert the pure-release elision (other tests in the suite may leave it set).
func TestRefinementDischargeElidedInRelease(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"ELISACORE_FORCE_BOUNDS_CHECK", "ELISACORE_NOALIAS_MUTABLE_REFS"} {
		if prev, had := os.LookupEnv(key); had {
			_ = os.Unsetenv(key)
			t.Cleanup(func() { _ = os.Setenv(key, prev) })
		}
	}
	_, err := buildRunRefinement(t, strings.Replace(refinementProgram, "%s", "neg()", 1), backend.OptimizationLevel3)
	if err != nil {
		t.Fatalf("release build should elide the refinement check (no trap), got: %v", err)
	}
}

const ensuresRefinementProgram = `
law Positive(self: i64) = self > 0

def store(p: mutable i64&, v: i64) -> void ensures p is Positive:
    p <- v
    return

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    n: mutable i64 = 5
    store(&n, %s)
    print(n.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

// A satisfying `ensures p is Positive` postcondition passes the debug return-check and runs.
func TestEnsuresRefinementSatisfiedRuns(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(ensuresRefinementProgram, "%s", "1", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("satisfying ensures postcondition should run cleanly: %v (out=%q)", err, out)
	}
	if out != "1" {
		t.Fatalf("want output 1, got %q", out)
	}
}

// A violated `ensures p is Positive` (the body writes 0) TRAPS at debug — the postcondition is
// enforced at the callee's return, backing the caller's gained fact.
func TestEnsuresRefinementViolatedTrapsInDebug(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(ensuresRefinementProgram, "%s", "0", 1), backend.OptimizationLevel0)
	if err == nil {
		t.Fatalf("violated ensures postcondition must trap at debug, but program exited 0 (out=%q)", out)
	}
}

// `old(expr)` in an `ensure` clause captures the value at function ENTRY, so the postcondition can
// relate the final state to the initial one. The body mutates the pointee, so `old(p)` and `p` differ
// at return — a genuine test that the entry snapshot (not the current value) is read.
const oldEnsureProgram = `
def bump(p: mutable i64&) -> void:
    ensure p == old(p) + 1
    p <- p + %s
    return

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    n: mutable i64 = 5
    bump(&n)
    print(n.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

// `ensures p == old(p) + 1` holds when the body increments by exactly 1: old(p)==5 (entry), p==6.
func TestOldEnsureSatisfiedRuns(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(oldEnsureProgram, "%s", "1", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("satisfying `ensures p == old(p) + 1` should run cleanly: %v (out=%q)", err, out)
	}
	if out != "6" {
		t.Fatalf("want output 6, got %q", out)
	}
}

// Incrementing by 2 violates `ensures p == old(p) + 1` — proves the check reads the ENTRY value of p
// (a stale-current-value capture would compare p to itself and never trap).
func TestOldEnsureViolatedTrapsInDebug(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(oldEnsureProgram, "%s", "2", 1), backend.OptimizationLevel0)
	if err == nil {
		t.Fatalf("`ensures p == old(p) + 1` with +2 must trap at debug, but program exited 0 (out=%q)", out)
	}
}

// `old(...)` alongside `result` in a non-void function, relating the return value to an entry-time
// argument. `delta` mutates its param, then returns the change relative to the captured entry value.
const oldResultProgram = `
def delta(n: i64) -> i64:
    ensure result == n - old(n)
    return %s

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    print(delta(7).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

// `n` is not mutated, so `result == n - old(n)` holds exactly when the body returns 0.
func TestOldResultSatisfiedRuns(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(oldResultProgram, "%s", "0", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("`ensure result == n - old(n)` returning 0 should run cleanly: %v (out=%q)", err, out)
	}
	if out != "0" {
		t.Fatalf("want output 0, got %q", out)
	}
}

// Returning a nonzero value violates `result == n - old(n)` (== 0 here) and traps in debug.
func TestOldResultViolatedTrapsInDebug(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(oldResultProgram, "%s", "3", 1), backend.OptimizationLevel0)
	if err == nil {
		t.Fatalf("`ensure result == n - old(n)` returning 3 must trap at debug, but program exited 0 (out=%q)", out)
	}
}

// An in-body `invariant` is a STANDING contract (docs/90 brick 90-14): once declared it is re-checked
// after every later mutation of a variable it reads. The in-place check at the declaration passes
// (x == start >= 0); only the re-check after a decrement can catch a violation. Here the loop drives
// x down by `count` steps — the canonical loop-invariant idiom.
const invariantRecheckProgram = `
def countdown(start: i64, count: i64) -> i64 can[Abort.Panic]:
    x: mutable i64 = start
    invariant x >= 0
    i: mutable i64 = 0
    while i < count:
        x <- x - 1
        i <- i + 1
    return x

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    r: i64 = countdown(%s) can Abort.Panic
    print(r.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

// start=5, count=3: x goes 5→2, the invariant `x >= 0` holds on every re-check, runs cleanly.
func TestInvariantRecheckHoldsRuns(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(invariantRecheckProgram, "%s", "5, 3", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("a standing invariant that holds on every mutation should run cleanly: %v (out=%q)", err, out)
	}
	if out != "2" {
		t.Fatalf("want output 2, got %q", out)
	}
}

// start=1, count=3: x goes 1→0→-1; the re-check after the mutation that drops x to -1 TRAPS. Without
// brick 90-14 the invariant is only checked once at x==1 and the violation slips through.
func TestInvariantRecheckViolatedTrapsInDebug(t *testing.T) {
	t.Parallel()
	out, err := buildRunRefinement(t, strings.Replace(invariantRecheckProgram, "%s", "1, 3", 1), backend.OptimizationLevel0)
	if err == nil {
		t.Fatalf("a standing invariant violated by a later mutation must trap at debug, but program exited 0 (out=%q)", out)
	}
}

// The standing invariant is debug-only: at -O3 the re-checks are elided, so the same violating run
// does NOT trap (debug verifies what release assumes).
func TestInvariantRecheckElidedInRelease(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"ELISACORE_FORCE_BOUNDS_CHECK", "ELISACORE_NOALIAS_MUTABLE_REFS"} {
		if prev, had := os.LookupEnv(key); had {
			_ = os.Unsetenv(key)
			t.Cleanup(func() { _ = os.Setenv(key, prev) })
		}
	}
	_, err := buildRunRefinement(t, strings.Replace(invariantRecheckProgram, "%s", "1, 3", 1), backend.OptimizationLevel3)
	if err != nil {
		t.Fatalf("release build should elide the standing-invariant re-checks (no trap), got: %v", err)
	}
}
