package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Exercise the real fresh-process driver fallback, not just Update's refusal.
// All revisions use the same pathname and compare unnormalized LLVM bytes.
func TestAnalysisDiskClassificationEditFreshProcessOracle(t *testing.T) {
	const baseline = `packed enum Expr:
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
	changed := strings.Replace(baseline, "value: i64 = 0", "value: i64 = observe(node)", 1)
	next := strings.Replace(changed, "return work()", "return work() + 0", 1)
	for _, optimization := range []string{"-O0", "-O3"} {
		t.Run(optimization, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "classification.elisa")
			warmEnv := map[string]string{
				"ELISAC_BUILD_CACHE_DIR":   filepath.Join(dir, "cache"),
				"ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1",
				"ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": "",
				"ELISACORE_RETAINED_FAST_PREPARE": "on",
			}
			coldEnv := map[string]string{
				"ELISACORE_ANALYSIS_CACHE": "0", "ELISACORE_ANALYSIS_CACHE_DEBUG": "0",
				"ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": "",
				"ELISACORE_RETAINED_FAST_PREPARE": "on",
			}
			for revision, text := range []string{baseline, changed, next, baseline} {
				if err := os.WriteFile(source, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
				cached, cold := filepath.Join(dir, "cached.ll"), filepath.Join(dir, "cold.ll")
				gotDiagnostics := runDiskCLIChild(t, warmEnv, optimization, "-emit", "llvm", "-o", cached, source)
				if revision > 0 && !strings.Contains(gotDiagnostics, "[analysis-disk] restore") {
					t.Fatalf("revision%d did not restore persistent state: %s", revision, gotDiagnostics)
				}
				wantDiagnostics := runDiskCLIChild(t, coldEnv, optimization, "-emit", "llvm", "-o", cold, source)
				got, err := os.ReadFile(cached)
				if err != nil {
					t.Fatal(err)
				}
				want, err := os.ReadFile(cold)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("revision%d exact IR mismatch: %s", revision, firstLineDiff(string(got), string(want)))
				}
				if revision == 1 && !strings.Contains(gotDiagnostics, "classification") {
					t.Fatalf("classification edit did not exercise driver fallback: %s", gotDiagnostics)
				}
				// Remove only the experimental digest observer block, whose grammar is
				// fixed and contains no semantic diagnostic text.
				fastObserver := regexp.MustCompile(`(?m)^\[fast-path\] [^\n]*\n--- before\n[0-9a-f]{64}\n--- after\n[0-9a-f]{64}\n`)
				if diskDiagnosticTranscript(fastObserver.ReplaceAllString(gotDiagnostics, "")) != diskDiagnosticTranscript(wantDiagnostics) {
					t.Fatalf("revision%d diagnostics differ\nwarm:%s\ncold:%s", revision, gotDiagnostics, wantDiagnostics)
				}
			}
		})
	}
}
