package semantic

import (
	"strings"
	"testing"
)

// Regression: comparing an optional against a non-null value (`opt == 5`,
// `opt == "s"`, `opt == opt2`) used to sail through semantic analysis and die
// in the LLVM backend with "Invalid operand types for ICmp instruction".
// Optionals only compare against null; anything else must unwrap first.
func TestOptionalEqualityAgainstValueRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"vs_base_value", `b: bool = opt == 5`},
		{"vs_mixed_type", `b: bool = opt == "s"`},
		{"vs_other_optional", "opt2: i64? = null\n        b: bool = opt == opt2"},
		{"bangeq_vs_base_value", `b: bool = opt != 5`},
		{"value_on_left", `b: bool = 5 == opt`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := "def f() -> void:\n    can Abort.Panic:\n        opt: i64? = null\n        " + tc.body + "\n        _ = b\n"
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "optional_eq_reject.elisa", source, AnalyzeOptions{})
			errs := result.Errors()
			found := false
			for _, err := range errs {
				if strings.Contains(err, "unwrap the optional first") {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected 'unwrap the optional first' diagnostic, got:\n%s", strings.Join(errs, "\n"))
			}
		})
	}
}

// `opt == null` / `null == opt` / `opt != null` stay legal — they lower to a
// present-flag check in the backend.
func TestOptionalEqualityAgainstNullStillAllowed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"eq_null", `b: bool = opt == null`},
		{"null_eq", `b: bool = null == opt`},
		{"neq_null", `b: bool = opt != null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := "def f() -> void:\n    can Abort.Panic:\n        opt: i64? = null\n        " + tc.body + "\n        _ = b\n"
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "optional_eq_null.elisa", source, AnalyzeOptions{})
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("expected clean analysis, got:\n%s", strings.Join(errs, "\n"))
			}
		})
	}
}
