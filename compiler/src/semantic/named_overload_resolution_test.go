package semantic

import (
	"strings"
	"testing"
)

func TestNamedOverloadSelectsWithoutReorderingArguments(t *testing.T) {
	source := `def choose(code: i64, flag: bool) -> i64:
    return code

def choose(value: u64, flag: bool) -> bool:
    return flag

def selected() -> bool:
    return choose(flag: true, value: 7.u64())

def main() -> i64:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_overload.elisa", source, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("valid named overload: %v", errs)
	}
}

func TestNamedOverloadPreservesUnknownLabelDiagnostic(t *testing.T) {
	source := `def choose(code: i64) -> i64: return code

def choose(value: u64) -> bool: return true

def main() -> i64:
    return choose(missing: 7.u64())
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_overload_invalid.elisa", source, AnalyzeOptions{})
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `has no parameter "missing"`) {
		t.Fatalf("missing named argument diagnostic: %v", result.Errors())
	}
}

func TestNamedOverloadRespectsLocalFunctionShadow(t *testing.T) {
	source := `def choose(code: i64) -> i64: return code

def choose(value: u64) -> bool: return true

def use(choose: fn(u64) -> i64) -> i64:
    return choose(value: 7.u64())
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_overload_shadow.elisa", source, AnalyzeOptions{})
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "does not expose parameter names") {
		t.Fatalf("local function value was replaced by a global overload: %v", result.Errors())
	}
}
