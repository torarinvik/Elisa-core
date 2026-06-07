package semantic

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// Interleaved lifetimes (`a` born, `b` born, `a` dies before `b`) used to be a hard error. With
// multi-stack regions the two growables are auto-split into separate parallel stacks, so the
// crossing dissolves — the program now compiles with no interleaved-lifetime error.
func TestRegionLifetimeAutoSplitsInterleaved(t *testing.T) {
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
	if strings.Contains(allDiagnostics(result), "interleaved object lifetimes") {
		t.Fatalf("multi-stack regions must auto-split crossing growables; no error expected, got:\n%s", allDiagnostics(result))
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

// Auto-wrap: a loop-local allocation (no manual `in auto:`) is automatically wrapped in an
// inferred region so it reclaims per iteration. It must compile cleanly — the wrap is gated so it
// never introduces an escape error.
func TestRegionAutoWrapLoopLocalCompilesClean(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "aw_local.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        for i in 0..<100:
            tmp: mutable darray[i64] = []
            tmp.push(i.i64())
            sink: i64 = tmp.count.i64()
            if sink < 0:
                panic("x")
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("auto-wrapped loop-local allocation must compile cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Auto-wrap is suppressed when the loop grows an OUTER container (the accumulator) — wrapping
// there would be a growth-escape error. The function must still compile (left unwrapped).
func TestRegionAutoWrapSkipsAccumulator(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "aw_acc.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        outer: mutable darray[i64] = []
        for i in 0..<100:
            tmp: mutable darray[i64] = []
            tmp.push(i.i64())
            outer.push(tmp.count.i64())
        if outer.count.i64() < 0:
            panic("x")
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("accumulator loop must compile cleanly (auto-wrap suppressed), got:\n%s", strings.Join(errs, "\n"))
	}
}

// Differential fuzz: generate random straight-line `in auto:` scopes with 2-4 darrays and a random
// push sequence. Every darray here is an unreserved growable, and 4 <= the stack cap, so multi-stack
// regions give each its own parallel stack — ALL crossings auto-split. The analyzer must therefore
// never raise an interleaved-lifetime error on these, regardless of how the (oracle-computed)
// lifetimes cross.
func TestRegionLifetimeFuzzAutoSplitsAllSmallScopes(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		src, _ := generateInterleaveScope(rand.New(rand.NewSource(seed)))
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, fmt.Sprintf("rl_fuzz_%d.elisa", seed), src, AnalyzeOptions{})
		if strings.Contains(allDiagnostics(result), "interleaved object lifetimes") {
			t.Fatalf("seed %d: <=cap growables must all auto-split; no interleaved error expected\n--- program ---\n%s\n--- diagnostics ---\n%s",
				seed, src, allDiagnostics(result))
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

// The interleaved-lifetime error survives only when the crossing pair cannot be auto-split: beyond
// the stack cap (4), the overflow growables share one merge stack, so two crossing growables forced
// into it are still rejected. Six growables push the last two (e, g) into the merge stack, and they
// cross — the hard error stands.
func TestRegionLifetimeRejectsCrossingInMergeStack(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "rl_merge.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            b: mutable darray[i64] = []
            c: mutable darray[i64] = []
            d: mutable darray[i64] = []
            e: mutable darray[i64] = []
            e.push(1)
            g: mutable darray[i64] = []
            g.push(2)
            e.push(3)
            g.push(4)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "interleaved object lifetimes") {
		t.Fatalf("a crossing forced into the merge stack must still be rejected, got:\n%s", all)
	}
}

// Two unreserved growables now get separate parallel stacks (multi-stack regions), so each is its
// own tail and interleaved growth no longer reallocates — the compiler auto-resolves what used to
// be a tail-growth warning. The warning must NOT fire here anymore.
func TestRegionGrowthTwoGrowablesAutoSplitNoWarning(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rg_split.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            a.push(1)
            b: mutable darray[i64] = []
            b.push(2)
            a.push(3)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "no longer the arena tail") {
		t.Fatalf("two growables now get separate stacks; the interior-growth warning must not fire, got:\n%s", allDiagnostics(result))
	}
}

// The warning survives only for a genuine same-stack collision: beyond the over-split cap, the
// overflow growables share one merge stack, where interior growth does relocate. Six growables
// (cap 4) push the last two into the merge stack; growing the earlier of that pair after the later
// one is allocated still warns.
func TestRegionGrowthMergeStackStillWarns(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rg_merge.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            b: mutable darray[i64] = []
            c: mutable darray[i64] = []
            d: mutable darray[i64] = []
            e: mutable darray[i64] = []
            e.push(1)
            g: mutable darray[i64] = []
            g.push(2)
            e.push(3)
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "no longer the arena tail") {
		t.Fatalf("merge-stack interior growth must still warn, got:\n%s", allDiagnostics(result))
	}
}

// reserve() pre-sizes a container so it never relocates — the size-then-freeze idiom suppresses
// the warning even when a later sibling sits on top.
func TestRegionGrowthReserveSuppresses(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rg_reserve.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            a.reserve(8)
            a.push(1)
            b: mutable darray[i64] = []
            b.push(2)
            a.push(3)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "no longer the arena tail") {
		t.Fatalf("reserve() must suppress the growth warning, got:\n%s", allDiagnostics(result))
	}
}

// Tail-ordered growth (each container grown fully before the next is born) respects the stack
// discipline and must never warn.
func TestRegionGrowthTailOrderNoWarning(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rg_tail.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            a.push(1)
            a.push(2)
            b: mutable darray[i64] = []
            b.push(3)
            b.push(4)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "no longer the arena tail") {
		t.Fatalf("tail-ordered growth must not warn, got:\n%s", allDiagnostics(result))
	}
}

func TestRegionGrowthResizeWarns(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rg_resize.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: mutable darray[i64] = []
            b: mutable darray[i64] = []
            c: mutable darray[i64] = []
            d: mutable darray[i64] = []
            e: mutable darray[i64] = []
            e.push(1)
            g: mutable darray[i64] = []
            g.push(2)
            e.resize(4)
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "no longer the arena tail") {
		t.Fatalf("resize can grow and must participate in tail-growth diagnostics, got:\n%s", allDiagnostics(result))
	}
}

// When the stack budget is exhausted and two merge-stack residents cross, the error names the
// SPECIFIC remedy. If the later object is not touched until after the earlier one dies (a
// straight-line gap), a concrete disjoint-reorder is provably legal, so the message says exactly
// which declaration to move — modeled on the struct-padding "consider ordering ..." lint.
func TestInterleavedErrorNamesConcreteReorderWhenLegal(t *testing.T) {
	// Six unreserved growables: a..d take stacks 1-4, e and g share the merge stack. g's first use
	// is after e's last use, so their live ranges are disjoint once g's decl moves down.
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "xreorder.elisa", `def prog() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable darray[i64] = []
        b: mutable darray[i64] = []
        c: mutable darray[i64] = []
        d: mutable darray[i64] = []
        e: mutable darray[i64] = []
        g: mutable darray[i64] = []
        a.push(1)
        b.push(1)
        c.push(1)
        d.push(1)
        e.push(1)
        e.push(2)
        g.push(1)
        g.push(2)
`, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !strings.Contains(diag, "interleaved object lifetimes") {
		t.Fatalf("expected an interleaved-lifetime error for the over-budget merge-stack crossing, got:\n%s", diag)
	}
	if !strings.Contains(diag, `Move "g"'s declaration below "e"'s last use`) {
		t.Fatalf("expected the message to name the concrete reorder, got:\n%s", diag)
	}
}

// When the two crossing objects' live ranges genuinely OVERLAP (the later one is used before the
// earlier one dies), no declaration move can separate them, so the message must NOT claim a reorder
// is available — it offers only the always-valid remedies (separate region / reserve).
func TestInterleavedErrorOmitsReorderWhenRangesOverlap(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "xoverlap.elisa", `def prog() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable darray[i64] = []
        b: mutable darray[i64] = []
        c: mutable darray[i64] = []
        d: mutable darray[i64] = []
        e: mutable darray[i64] = []
        g: mutable darray[i64] = []
        a.push(1)
        b.push(1)
        c.push(1)
        d.push(1)
        e.push(1)
        g.push(1)
        e.push(2)
        g.push(2)
`, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !strings.Contains(diag, "interleaved object lifetimes") {
		t.Fatalf("expected an interleaved-lifetime error, got:\n%s", diag)
	}
	if strings.Contains(diag, "declaration below") {
		t.Fatalf("ranges overlap — no reorder is legal, so the message must not suggest one; got:\n%s", diag)
	}
	if !strings.Contains(diag, "their live ranges overlap") || !strings.Contains(diag, "reserve()") {
		t.Fatalf("expected the overlap message to offer the region/reserve remedies, got:\n%s", diag)
	}
}
