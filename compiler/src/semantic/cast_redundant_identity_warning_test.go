package semantic

import (
	"strings"
	"testing"
)

// A reinterpret `.cast[T]` whose operand already HAS type T does nothing and is almost always
// refactor leftover (e.g. the operand was narrowed or its declared type changed). It warns.
func TestRedundantIdentityCastWarns(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_identity.elisa", `def f(x: u32) -> u32:
    return x.cast[u32]
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "redundant `.cast[u32]`") {
		t.Fatalf("expected a redundant-cast warning for u32->u32, got:\n%s", allDiagnostics(result))
	}
}

// A genuine reinterpret to a DIFFERENT type is not redundant and must not be flagged.
func TestRealReinterpretCastNotFlaggedAsRedundant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_reinterpret.elisa", `def f(x: u32) -> i32:
    return x.cast[i32]
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "redundant `.cast") {
		t.Fatalf("u32->i32 reinterpret must not be flagged as redundant, got:\n%s", allDiagnostics(result))
	}
}

// SameType is exact: a lifetime-widening cast (`u8&` -> `static u8&`) changes the type's region,
// so it is a meaningful reinterpret and must NOT be flagged as redundant.
func TestLifetimeWideningCastNotFlaggedAsRedundant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_widen.elisa", `def f(x: u8&) -> static u8&:
    return x.cast[static u8&]
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "redundant `.cast") {
		t.Fatalf("u8&->static u8& lifetime widening must not be flagged as redundant, got:\n%s", allDiagnostics(result))
	}
}
