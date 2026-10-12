package semantic

import "testing"

func TestNamedTupleResultConstruction(t *testing.T) {
	for _, tc := range []struct {
		name, returned string
		wantError      bool
	}{
		{"identity", "flag, value", false},
		{"negated_bool", "not flag, value", true},
		{"changed_integer", "flag, value + 1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "def flags(flag: bool, value: i64) -> (valid: bool, value: i64):\n    ensure result.valid == flag\n    ensure result.value == value\n    return (" + tc.returned + ")\n"
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "tuple_result.elisa", source, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if (len(result.Errors()) != 0) != tc.wantError {
				t.Fatalf("want error=%v, got:\n%s", tc.wantError, allDiagnostics(result))
			}
		})
	}
}

func TestNamedTupleResultFieldIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, returned string
		wantError      bool
	}{
		{"ordered", "flag, not flag", false},
		{"swapped", "not flag, flag", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "def flags(flag: bool) -> (enabled: bool, disabled: bool):\n    ensure result.enabled == flag\n    ensure result.disabled == (not flag)\n    return (" + tc.returned + ")\n"
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "tuple_fields.elisa", source, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if (len(result.Errors()) != 0) != tc.wantError {
				t.Fatalf("want error=%v, got:\n%s", tc.wantError, allDiagnostics(result))
			}
		})
	}
}
