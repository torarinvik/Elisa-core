package vcarithmetic

import (
	"math"
	"math/big"
	"testing"
)

func TestFoldArithOverflow(t *testing.T) {
	cases := []struct {
		op   string
		a, b int64
		want int64
		ok   bool
	}{
		{"*", math.MinInt64, -1, 0, false},
		{"*", -1, math.MinInt64, 0, false},
		{"*", math.MinInt64, 1, math.MinInt64, true},
		{"*", math.MaxInt64, 2, 0, false},
		{"+", math.MaxInt64, 1, 0, false},
		{"-", math.MinInt64, 1, 0, false},
		{"*", -7, 6, -42, true},
	}
	for _, tc := range cases {
		got, ok := FoldArith(tc.op, tc.a, tc.b)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%d %s %d: got %d, %v; want %d, %v", tc.a, tc.op, tc.b, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFoldArithMatchesExactIntegers(t *testing.T) {
	values := []int64{math.MinInt64, math.MinInt64 + 1, -3037000500, -2, -1, 0, 1, 2, 3037000500, math.MaxInt64 - 1, math.MaxInt64}
	for _, a := range values {
		for _, b := range values {
			for _, op := range []string{"+", "-", "*"} {
				left, right := big.NewInt(a), big.NewInt(b)
				exact := new(big.Int)
				switch op {
				case "+":
					exact.Add(left, right)
				case "-":
					exact.Sub(left, right)
				case "*":
					exact.Mul(left, right)
				}
				got, ok := FoldArith(op, a, b)
				if ok != exact.IsInt64() || ok && got != exact.Int64() {
					t.Errorf("%d %s %d: got %d, %v; exact %s", a, op, b, got, ok, exact)
				}
			}
		}
	}
}
