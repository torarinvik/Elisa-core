package semantic

import "testing"

func TestStructBooleanResultConstruction(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		wantError   bool
	}{
		{"same", "flag", false},
		{"negated", "not flag", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "struct Flags:\n    enabled: bool\ndef make_flags(flag: bool) -> Flags:\n    ensure result.enabled == flag\n    return Flags{enabled: " + tc.value + "}\n"
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bool_result.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if (len(r.Errors()) != 0) != tc.wantError {
				t.Fatalf("want error=%v, got:\n%s", tc.wantError, allDiagnostics(r))
			}
		})
	}
}

func TestStructBooleanResultFieldIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		wantError    bool
	}{
		{"mixed", "enabled: flag, disabled: not flag", false},
		{"swapped", "enabled: not flag, disabled: flag", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "struct Flags:\n    enabled: bool\n    disabled: bool\ndef make_flags(flag: bool) -> Flags:\n    ensure result.enabled == flag\n    ensure result.disabled == (not flag)\n    return Flags{" + tc.fields + "}\n"
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bool_fields.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
			if (len(r.Errors()) != 0) != tc.wantError {
				t.Fatalf("want error=%v, got:\n%s", tc.wantError, allDiagnostics(r))
			}
		})
	}
}
