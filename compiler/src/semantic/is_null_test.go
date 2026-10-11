package semantic

import (
	"strings"
	"testing"
)

func TestIsNullComparableNullableReference(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "is_null.elisa", `def absent(value: i64&?) -> bool:
    return value is null
def present(value: i64&?) -> bool:
    return value is not null
def alternative(value: i64&?) -> bool:
    return value is [null]
`, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("nullable reference null tests rejected: %s", allDiagnostics(result))
	}
}

func TestIsNullRejectsIncomparableScalar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "is_null_scalar.elisa", `def invalid(value: i64) -> bool:
    return value is null
`, AnalyzeOptions{})
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "is expects a comparable value alternative") {
		t.Fatalf("incomparable null test accepted: %s", allDiagnostics(result))
	}
}
