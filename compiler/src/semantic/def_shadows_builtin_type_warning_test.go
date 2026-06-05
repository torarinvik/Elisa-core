package semantic

import (
	"strings"
	"testing"
)

// A `def` whose name is a builtin scalar type (f32, i32, u8, …) is unreachable by call: a call
// site `name(x)` resolves to the type cast, not to the function. The function silently vanishes,
// so we warn at the definition site rather than producing a confusing "miscompile" downstream.
func TestDefNameShadowingBuiltinTypeWarns(t *testing.T) {
	for _, name := range []string{"f32", "i32", "u8", "f64"} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "def_shadow.elisa", `def `+name+`(x: i64) -> i64:
    return x + 100
`, AnalyzeOptions{})
		diags := allDiagnostics(result)
		if !strings.Contains(diags, "shadows the builtin type name") ||
			!strings.Contains(diags, "resolves to the type cast") {
			t.Fatalf("expected a shadowing warning for def %q, got:\n%s", name, diags)
		}
	}
}

// Arbitrary-width bit-int type names (e.g. u7, i24) are builtins too and must be flagged.
func TestDefNameShadowingBitIntTypeWarns(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "def_shadow_bitint.elisa", `def u7(x: i64) -> i64:
    return x
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "shadows the builtin type name") {
		t.Fatalf("expected a shadowing warning for def \"u7\", got:\n%s", allDiagnostics(result))
	}
}

// An ordinary function name that is not a type must not be flagged.
func TestDefNameNotShadowingBuiltinTypeNoWarn(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "def_no_shadow.elisa", `def addHundred(x: i64) -> i64:
    return x + 100
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "shadows the builtin type name") {
		t.Fatalf("a non-type function name must not be flagged, got:\n%s", allDiagnostics(result))
	}
}
