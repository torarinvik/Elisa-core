package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeConstDictLiteralRejectsDuplicateKeys(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "const_dict_duplicate.elisa", `
const NUMBERS: dict[cstr, u8] = {"one": 1, "one": 2}
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "duplicate const dict key") {
		t.Fatalf("expected duplicate const dict key diagnostic, got:\n%s", allDiagnostics(result))
	}
}

func TestAnalyzeConstDictIndexRejectsMissingLiteralKey(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "const_dict_missing_index.elisa", `
const NUMBERS: dict[cstr, u8] = {"one": 1}

def bad() -> u8:
    return NUMBERS["missing"]
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "const dict has no key") {
		t.Fatalf("expected missing const dict key diagnostic, got:\n%s", allDiagnostics(result))
	}
}
