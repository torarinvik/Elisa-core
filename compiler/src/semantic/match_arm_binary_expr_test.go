package semantic

import (
	"strings"
	"testing"
)

// End-to-end (parse + analyze) regression: a match-arm body whose return value is a
// binary expression starting with a pattern-bound identifier (`return v * v`)
// analyzes cleanly. Pins the cascade that a stray `case` keyword used to cause.
func TestAnalyzeMatchArmIdentMultiplication(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "match_arm_mul.elisa", `enum E:
    A(v: i64)

def g(e: E) -> i64:
    match e:
        E.A(v: v):
            return v * v
    return 0
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got:\n%s", strings.Join(errs, "\n"))
	}
}
