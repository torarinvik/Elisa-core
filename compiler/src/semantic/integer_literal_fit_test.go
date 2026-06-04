package semantic

import (
	"strings"
	"testing"
)

// An out-of-range integer literal implicitly adapted to a narrower integer type was
// silently truncated (`y: i8 = 1000` became -24). With no explicit cast, that is
// unambiguously a bug — it must be rejected (cf. Rust's overflowing-literal error).
func TestIntegerLiteralOverflowRejected(t *testing.T) {
	for _, c := range []struct{ ty, val, want string }{
		{"i8", "1000", "integer literal 1000 does not fit in i8"},
		{"i8", "-200", "integer literal -200 does not fit in i8"},
		{"u8", "-1", "integer literal -1 does not fit in u8"},
		{"u16", "70000", "integer literal 70000 does not fit in u16"},
	} {
		result := analyzeTreeTestSourceWithSemanticErrors(t, "lit_overflow.elisa", `def f() -> `+c.ty+`:
    y: `+c.ty+` = `+c.val+`
    return y
`)
		if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, c.want) {
			t.Fatalf("%s = %s: expected %q, got:\n%s", c.ty, c.val, c.want, all)
		}
	}
}

// In-range literals (including signed/unsigned boundaries and a full-width u64 constant
// stored as a negative int64 bit pattern) must NOT be flagged.
func TestInRangeIntegerLiteralsAccepted(t *testing.T) {
	for _, c := range []struct{ ty, val string }{
		{"i8", "-128"}, {"i8", "127"}, {"u8", "255"}, {"u8", "0"},
		{"u16", "65535"}, {"u64", "0xFFFFFFFFFFFFFFFF"}, {"i64", "9223372036854775807"},
	} {
		result := analyzeTreeTestSource(t, "lit_ok.elisa", `def f() -> `+c.ty+`:
    y: `+c.ty+` = `+c.val+`
    return y
`)
		if all := strings.Join(result.Errors(), "\n"); strings.Contains(all, "does not fit") {
			t.Fatalf("%s = %s: must be accepted, got:\n%s", c.ty, c.val, all)
		}
	}
}

// An explicit cast is the truncation mechanism, so it is allowed — but casting a
// compile-time constant that cannot fit the target warns (warn-on-lossy).
func TestConstantTruncatingCastWarns(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "cast_trunc.elisa", `def f() -> u8:
    return 300.u8()
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "truncated by this cast") {
		t.Fatalf("expected truncation warning for 300.u8(); got:\n%s", allDiagnostics(result))
	}
}
