package semantic

import (
	"strings"
	"testing"
)

// `.cast[T]` is the canonical reinterpret/bitcast: a numeric value conversion through it is rejected
// with a pointer to the constructor forms.
func TestCastRejectsNumericValueConversion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "cast_value.elisa", `def keep(x: i64) -> i32:
    return x.cast[i32]
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "value conversion, not a reinterpret") || !strings.Contains(joined, "i32(x)") {
		t.Fatalf("expected a value-conversion diagnostic pointing at the constructor, got:\n%s", joined)
	}
}

// The constructor `T(x)` and postfix `x.T()` forms ARE the value-conversion path and are accepted.
func TestConstructorAndPostfixValueConversionsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "cast_value_ok.elisa", `def keep(x: i64) -> i32:
    a: i32 = i32(x)
    b: i32 = x.i32()
    return a + b
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected constructor/postfix value conversions to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

// `.cast[T]` still performs genuine reinterprets (pointer/ref), which are NOT value conversions.
func TestCastStillAllowsPointerReinterpret(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "cast_reinterpret_ok.elisa", `def keep(p: u8&) -> void:
    _ = p.cast[void&]
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected pointer reinterpret via .cast to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}
