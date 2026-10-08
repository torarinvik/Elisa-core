package semantic

import (
	"strings"
	"testing"
)

func TestInlineCanPreservesContextualDestination(t *testing.T) {
	for _, value := range []string{"[]", "([] can Memory.Allocate)"} {
		source := "def main() -> i32:\n    values: darray[i64] = " + value + "\n    return 0\n"
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "can_destination.elisa", source, AnalyzeOptions{})
		if errs := result.Errors(); len(errs) != 0 {
			t.Errorf("%s: %s", value, strings.Join(errs, "\n"))
		}
	}
}

func TestInlineCanPreservesPackedFileBuilderRegion(t *testing.T) {
	const prefix = `packed enum Expr:
    Number(value: i64)
struct File:
    expressions: mutable darray[Expr]
def parse_file() -> File:
    return File{expressions: []}
def append_file(file: mutable File&) -> void:
    file.expressions.reserve(2)
`
	for _, value := range []string{"parse_file()", "(parse_file() can Memory.Allocate)", "((parse_file() can Memory.Allocate))"} {
		source := prefix + "def main() -> i32:\n    can Memory.Allocate, Abort.Panic:\n        file: mutable File = " + value + "\n        append_file(file)\n    return 0\n"
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "can_file.elisa", source, AnalyzeOptions{})
		if errs := result.Errors(); len(errs) != 0 {
			t.Errorf("%s: %s", value, strings.Join(errs, "\n"))
		}
	}
}

func TestInlineCanRetainsDanglingBorrow(t *testing.T) {
	for _, value := range []string{"&marker", "(&marker can Memory.Allocate)"} {
		source := "def leak() -> usize&:\n    marker: usize = 1\n    return " + value + "\ndef main() -> i32:\n    return 0\n"
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "can_borrow.elisa", source, AnalyzeOptions{})
		if errs := strings.Join(result.Errors(), "\n"); !strings.Contains(errs, "dangles") {
			t.Errorf("%s must retain its local borrow: %s", value, errs)
		}
	}
}

func TestInlineCanRetainsScopeOwnedRegion(t *testing.T) {
	for _, value := range []string{"v", "(v can Memory.Allocate)"} {
		source := "def leak() -> darray[u8]:\n    can Memory.Allocate, Abort.Panic:\n        region a(4096):\n            v: mutable darray[u8] @a = []\n            v.push(65)\n            return " + value + "\n"
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "can_region_escape.elisa", source, AnalyzeOptions{})
		if errs := strings.Join(result.Errors(), "\n"); !strings.Contains(errs, "escapes via return") {
			t.Errorf("%s must retain its dead owner: %s", value, errs)
		}
	}
}
