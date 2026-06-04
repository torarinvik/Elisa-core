package semantic

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// Interleaved lifetimes in an inferred region: `a` is born first, `b` second, `a` dies before
// `b` — their lifetimes cross and cannot be tightened into a LIFO region stack. Flagged (warn).
func TestRegionLifetimeFlagsInterleaved(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rl_inter.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            b: mutable darray[i64] = []
            a.push(1)
            b.push(2)
            a.push(3)
            b.push(4)
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "interleaved object lifetimes") {
		t.Fatalf("expected interleaved-lifetime warning, got:\n%s", allDiagnostics(result))
	}
}

// Nested (`b` fully inside `a`) and disjoint (`a` dead before `b` born) lifetimes both map onto
// a region stack — never flagged.
func TestRegionLifetimeAllowsNestedAndDisjoint(t *testing.T) {
	nested := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rl_nested.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            b: mutable darray[i64] = []
            b.push(1)
            b.push(2)
            a.push(3)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(nested), "interleaved") {
		t.Fatalf("nested lifetimes must not be flagged, got:\n%s", allDiagnostics(nested))
	}
	disjoint := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rl_disjoint.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            a.push(1)
            a.push(2)
            b: mutable darray[i64] = []
            b.push(3)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(disjoint), "interleaved") {
		t.Fatalf("disjoint lifetimes must not be flagged, got:\n%s", allDiagnostics(disjoint))
	}
}

// Both objects used inside the same loop are live to the loop's end (clamped) — equal deaths,
// so they nest rather than cross. Must NOT be a false positive.
func TestRegionLifetimeLoopClampingNoFalsePositive(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rl_loop.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            b: mutable darray[i64] = []
            for i in 0..<10:
                a.push(i.i64())
                b.push(i.i64())
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "interleaved") {
		t.Fatalf("loop-clamped equal lifetimes must not be flagged, got:\n%s", allDiagnostics(result))
	}
}

// Objects in an EXPLICIT region (`region r(...)` with `@r`) are user-managed, not inferred —
// exempt from the analysis even when their lifetimes cross.
func TestRegionLifetimeExemptsExplicitRegion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rl_explicit.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r(4096):
            a: mutable darray[i64] @r = []
            b: mutable darray[i64] @r = []
            a.push(1)
            b.push(2)
            a.push(3)
            b.push(4)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "interleaved") {
		t.Fatalf("explicit-region objects must be exempt, got:\n%s", allDiagnostics(result))
	}
}

// Giving one of the crossing objects an explicit region (the suggested fix) resolves the
// interleaving — only one inferred object remains, so nothing is flagged.
func TestRegionLifetimeFixViaExplicitRegionAnnotation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rl_fixed.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(4096):
            in auto:
                a: mutable darray[i64] = []
                b: mutable darray[i64] @scratch = []
                a.push(1)
                b.push(2)
                a.push(3)
                b.push(4)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "interleaved") {
		t.Fatalf("pinning one object to an explicit region must resolve the warning, got:\n%s", allDiagnostics(result))
	}
}

// Differential fuzz: generate random straight-line `in auto:` scopes with several darrays and
// a random push sequence, compute whether any lifetimes cross with an independent Go oracle
// (statement-index intervals), and assert the analyzer's verdict matches exactly.
func TestRegionLifetimeFuzzMatchesOracle(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		src, crossed := generateInterleaveScope(rand.New(rand.NewSource(seed)))
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, fmt.Sprintf("rl_fuzz_%d.elisa", seed), src, AnalyzeOptions{})
		flagged := strings.Contains(allDiagnostics(result), "interleaved object lifetimes")
		if flagged != crossed {
			t.Fatalf("seed %d: analyzer flagged=%v, oracle crossed=%v\n--- program ---\n%s\n--- diagnostics ---\n%s",
				seed, flagged, crossed, src, allDiagnostics(result))
		}
	}
}

// generateInterleaveScope emits an `in auto:` block declaring 2-4 darrays followed by a random
// straight-line push sequence (one push per statement). It returns the source and whether any
// pair of object lifetimes crosses (birth_i < birth_j < death_i < death_j on statement indices)
// — the exact rule the analyzer applies at statement granularity.
func generateInterleaveScope(rng *rand.Rand) (string, bool) {
	k := 2 + rng.Intn(3)
	var b strings.Builder
	b.WriteString("def f() -> void:\n    can Memory.Allocate, Memory.Release, Abort.Panic:\n        in auto:\n")
	birth := make([]int, k)
	death := make([]int, k)
	for i := 0; i < k; i++ {
		fmt.Fprintf(&b, "            d%d: mutable darray[i64] = []\n", i)
		birth[i] = i
		death[i] = i
	}
	step := k
	for p, m := 0, 4+rng.Intn(9); p < m; p++ {
		r := rng.Intn(k)
		fmt.Fprintf(&b, "            d%d.push(%d)\n", r, p)
		death[r] = step
		step++
	}
	crossed := false
	for i := 0; i < k; i++ {
		for j := i + 1; j < k; j++ {
			if birth[j] < death[i] && death[i] < death[j] {
				crossed = true
			}
		}
	}
	return b.String(), crossed
}

// Under -Wperf (EnforcePerfLints) the warning becomes a hard error.
func TestRegionLifetimeWperfPromotesToError(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rl_wperf.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            b: mutable darray[i64] = []
            a.push(1)
            b.push(2)
            a.push(3)
            b.push(4)
`, AnalyzeOptions{EnforcePerfLints: true})
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "interleaved object lifetimes") {
		t.Fatalf("expected -Wperf to promote interleaving to an error, got:\n%s", all)
	}
}
