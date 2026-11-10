package semantic

import (
	"math"
	"testing"
)

func TestConstFloatingEqualityUsesNumericSemantics(t *testing.T) {
	floating := func(value float64) ConstValue { return ConstValue{Kind: ConstFloat, Float: value} }
	negativeZero := floating(math.Copysign(0, -1))
	nan := floating(math.NaN())
	for _, tc := range []struct {
		name        string
		left, right ConstValue
		equal       bool
	}{
		{"signed-zero", floating(0), negativeZero, true},
		{"integer-zero", ConstValue{Kind: ConstInt}, negativeZero, true},
		{"nan-self", nan, nan, false},
		{"nan-number", nan, floating(1), false},
		{"equal-infinities", floating(math.Inf(1)), floating(math.Inf(1)), true},
		{"opposite-infinities", floating(math.Inf(1)), floating(math.Inf(-1)), false},
		{"equal-finite", floating(1.5), floating(1.5), true},
		{"unequal-finite", floating(1.5), floating(2.5), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Analyzer{}
			for _, equality := range []bool{true, false} {
				got, ok := a.evalConstEquality(tc.left, tc.right, equality)
				want := tc.equal == equality
				if !ok || got.Kind != ConstBool || got.Bool != want {
					t.Fatalf("equality=%v: got %+v, known=%v; want %v", equality, got, ok, want)
				}
			}
		})
	}
}

func TestStaticAssertFloatingSignedZeroEquality(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_float_zero.elisa", `
static assert 0.0 == -0.0
static assert -0.0 == 0.0
static assert not (0.0 != -0.0)
static assert 0 == -0.0

def keep() -> void:
    static assert -0.0 == 0.0
`)
}
