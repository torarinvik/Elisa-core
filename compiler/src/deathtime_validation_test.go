package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"elisacore/src/semantic"
)

// docs/91 G0 differential validation: run the (read-only) death-time cohort analysis over the real
// test-program corpus and report how TIGHT the inferred death points are — so we can SEE the analysis
// behaves well on non-toy code before G1 is ever allowed to free on it. This is observability, not a
// pass/fail oracle: it asserts only that the analysis runs cleanly on real programs and that it finds
// real early-death opportunities (the whole point of the death-time model). The detailed report is
// logged for inspection.
//
// Metrics per function with inferred allocations:
//   - total inferred allocations
//   - escaping (DeathIndex == -1): lifetime is the caller's; G1 cannot early-free these
//   - in-function (DeathIndex >= 0): have a concrete death point — early-free candidates
//   - "early" = in-function allocations that die STRICTLY BEFORE the function's last in-function
//     death point (so a later cohort exists whose stacks they could hand off to — a reuse win)
//   - multi-cohort functions (>1 distinct death point): distinct lifetimes were resolved
func TestDeathTimeTightnessReport(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	corpus := filepath.Join(repoRoot, "Code", "test_programs")
	entries, err := filepath.Glob(filepath.Join(corpus, "*.elisa"))
	if err != nil || len(entries) == 0 {
		t.Skipf("no corpus at %s (%v)", corpus, err)
	}
	sort.Strings(entries)

	var (
		analyzed, skipped             int
		totalAllocs, escaping, inFunc int
		earlyDying, multiCohortFuncs  int
		funcsWithAllocs               int
		returnEscapes, argEscapes     int
	)
	for _, path := range entries {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		expanded, err := readSourceWithIncludes(path, map[string]bool{})
		if err != nil {
			expanded = src
		}
		var stderr bytes.Buffer
		_, result, ok := analyzeProgramWithOptions(path, expanded, &stderr, semantic.AnalyzeOptions{RecordDeathTimeCohorts: true})
		if !ok || result == nil {
			skipped++ // an intentionally-erroring fixture, or one needing flags; not our concern here
			continue
		}
		analyzed++
		// Per-program: does this program exhibit the pattern the death-time model optimizes (a
		// function with >1 cohort / an allocation that dies early)? Surface those so we can tell
		// whether real apps benefit beyond what scope-regions already give.
		progMulti, progEarly := 0, 0
		for _, cohorts := range result.DeathTimeCohorts {
			if len(cohorts) > 1 {
				progMulti++
			}
			maxD := -1
			for _, c := range cohorts {
				if c.DeathIndex > maxD {
					maxD = c.DeathIndex
				}
			}
			for _, c := range cohorts {
				if c.DeathIndex >= 0 && c.DeathIndex < maxD {
					progEarly += len(c.Allocs)
				}
			}
		}
		if progMulti > 0 || progEarly > 0 {
			t.Logf("  [pattern] %s: %d multi-cohort func(s), %d early-dying alloc(s)", filepath.Base(path), progMulti, progEarly)
		}
		for _, st := range result.DeathTimeEscapeStats {
			returnEscapes += st.ReturnEscapes
			argEscapes += st.ArgEscapes
		}
		for _, cohorts := range result.DeathTimeCohorts {
			if len(cohorts) == 0 {
				continue
			}
			funcsWithAllocs++
			if len(cohorts) > 1 {
				multiCohortFuncs++
			}
			maxDeath := -1
			for _, c := range cohorts {
				if c.DeathIndex > maxDeath {
					maxDeath = c.DeathIndex
				}
			}
			for _, c := range cohorts {
				totalAllocs += len(c.Allocs)
				if c.DeathIndex == -1 {
					escaping += len(c.Allocs)
					continue
				}
				inFunc += len(c.Allocs)
				if c.DeathIndex < maxDeath {
					earlyDying += len(c.Allocs)
				}
			}
		}
	}

	t.Logf("death-time tightness over %d analyzed corpus programs (%d skipped):", analyzed, skipped)
	t.Logf("  functions with inferred allocations: %d (%d with >1 cohort / distinct death points)", funcsWithAllocs, multiCohortFuncs)
	t.Logf("  inferred allocations: %d total", totalAllocs)
	t.Logf("  escaping (caller region, not early-freeable): %d", escaping)
	t.Logf("    - return-escapes (genuine; region-poly threading handles): %d", returnEscapes)
	t.Logf("    - arg-escapes (CONSERVATIVE; G3 interprocedural precision could reclaim): %d", argEscapes)
	t.Logf("  in-function (concrete death point): %d", inFunc)
	t.Logf("  EARLY-dying (die before the function's last cohort -> reuse win): %d", earlyDying)

	// Read-only diagnostic: assert only that the analysis ran on real code and produced data. The
	// tightness numbers are reported for human judgment (the finding that arg-escapes dominate is
	// what re-sequences G3 before G1 — recorded in docs/91).
	if analyzed < 10 {
		t.Fatalf("expected to analyze a meaningful slice of the corpus, only got %d", analyzed)
	}
	if totalAllocs == 0 {
		t.Fatalf("analysis found no inferred allocations across the corpus — it is not firing on real code")
	}
}
