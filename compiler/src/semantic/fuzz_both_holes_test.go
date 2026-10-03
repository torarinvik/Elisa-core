package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fuzz findings both compilers accepted (Elisa-compiler agent/fuzz-findings, round 1):
//   - F2: a callee stores a view argument into a holder the caller passed by mutable reference,
//     while the view points into the caller's `in auto:` region (checkCallArgumentHolderStoreEscape);
//   - F3: a call passes a container by mutable reference AND a view of it (validateCallArgAliasAccess
//     now treats view parameters as borrows of the viewed storage; strict, like `&buf[0]`);
//   - F12: a closure moving a captured affine value is one-shot (analyzer_one_shot_closure.go);
//   - F16: `move xs` (a callee or a drain) leaves earlier views/refs into xs stale (strict).
// EXPECT rows: file, mode (default | strict), wanted diagnostic substring.
func fuzzBothHolesAnalyze(t *testing.T, name, mode, source string) []string {
	t.Helper()
	if mode == "strict" {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, source, AnalyzeOptions{EnforceUnsafePermissions: true})
		return strings.Split(allDiagnostics(result), "\n")
	}
	return analyzeTreeTestSourceWithSemanticErrors(t, name, source).Errors()
}

func TestFuzzBothHolesNegative(t *testing.T) {
	dir := filepath.Join("testdata", "fuzz_both_holes")
	expect, err := os.ReadFile(filepath.Join(dir, "EXPECT"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(expect)), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			t.Fatalf("bad EXPECT row %q", line)
		}
		name, mode, want := fields[0], fields[1], fields[2]
		source, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(fuzzBothHolesAnalyze(t, name, mode, string(source)), "\n"); !strings.Contains(got, want) {
			t.Errorf("%s (%s): want %q, got:\n%s", name, mode, want, got)
		}
	}
}

func TestFuzzBothHolesPositive(t *testing.T) {
	name := "both_holes.pos.elisa"
	source, err := os.ReadFile(filepath.Join("testdata", "fuzz_both_holes", name))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"default", "strict"} {
		for _, diag := range fuzzBothHolesAnalyze(t, name, mode, string(source)) {
			if strings.Contains(diag, "escapes") || strings.Contains(diag, "alias") || strings.Contains(diag, "stale") || strings.Contains(diag, "one-shot") || strings.Contains(diag, "closure") || strings.Contains(diag, "cannot be used") {
				t.Errorf("%s (%s) rejected: %s", name, mode, diag)
			}
		}
	}
}
