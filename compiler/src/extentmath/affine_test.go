package extentmath

import "testing"

func TestAffineParseAndOperations(t *testing.T) {
	cases := []struct{ input, formatted string }{
		{"(2 * (x + 3))", "(2 * x) + 6"},
		{"(x + (-1 * x) + 7)", "7"},
		{"(y + x + 2)", "x + y + 2"},
	}
	for _, tc := range cases {
		expr, ok := Parse(tc.input)
		if !ok || String(expr) != tc.formatted {
			t.Errorf("Parse(%q) = %q, %v; want %q", tc.input, String(expr), ok, tc.formatted)
		}
	}
	if _, ok := Parse("(x * y)"); ok {
		t.Fatal("nonlinear expression accepted")
	}
	left, _ := Parse("x + 3")
	right, _ := Parse("x + 5")
	if cmp, ok := Compare(left, right); !ok || cmp != -1 {
		t.Fatalf("compare = %d, %v", cmp, ok)
	}
	if result := right.Sub(left); !IsConst(result) || result.Const != 2 {
		t.Fatalf("difference = %#v", result)
	}
}

func TestAffineComparisonRequiresSameResidual(t *testing.T) {
	left, _ := Parse("(2 * n) + x")
	right, _ := Parse("(3 * n) + x")
	if cmp, ok := CompareByPositiveSymbol(left, right, "n"); !ok || cmp != -1 {
		t.Fatalf("positive compare = %d, %v", cmp, ok)
	}
	other, _ := Parse("(3 * n) + y")
	if _, ok := CompareByPositiveSymbol(left, other, "n"); ok {
		t.Fatal("different residuals compared")
	}
}
