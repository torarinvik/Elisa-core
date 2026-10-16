//go:build cgo

package semantic

import "testing"

func TestScalarMatchRangeArmValue(t *testing.T) {
	source := `def follows(first: char, second: char) -> bool:
    ensure result == (first in '0'..='9' or ((first == '+' or first == '-') and second in '0'..='9'))
    return when first:
        '+' | '-' -> second in '0'..='9'
        _ -> first in '0'..='9'
`
	result := analyzeContractStrict(t, "match_char_range.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatalf("valid range-valued match: %v", result.Errors())
	}
}

func TestScalarMatchExclusiveRangeArmValue(t *testing.T) {
	for _, inclusive := range []bool{false, true} {
		expected := "x >= 0 and x < 10"
		if inclusive {
			expected = "x >= 0 and x <= 10"
		}
		source := `def bounded(x: i64, selected: i64) -> bool:
    ensure result == ((selected == 1) and (` + expected + `))
    return when selected:
        1 -> x in 0..<10
        _ -> false
`
		result := analyzeContractStrict(t, "match_variable_range.elisa", source)
		got := ensureFailuresFor(result.Errors(), "bounded") > 0
		if got != inclusive {
			t.Fatalf("rejected %v, want %v: %v", got, inclusive, result.Errors())
		}
	}
}
