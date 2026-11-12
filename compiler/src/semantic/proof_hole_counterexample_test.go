package semantic

import "testing"

func TestProofHoleCounterexampleFiltersBothComparisonDirections(t *testing.T) {
	values := map[string]int64{"x": 2}
	cases := []struct {
		text        string
		contradicts bool
	}{
		{"establish `x > 3`", true},
		{"establish `x >= 3`", true},
		{"establish `x < 1`", true},
		{"establish `x <= 1`", true},
		{"establish `x > 1`", false},
		{"establish `x >= 2`", false},
		{"establish `x < 3`", false},
		{"establish `x <= 2`", false},
	}
	for _, tc := range cases {
		if got := suggestionContradictsCounterexample(tc.text, values); got != tc.contradicts {
			t.Errorf("%s: got %v, want %v", tc.text, got, tc.contradicts)
		}
	}
}

func TestProofComparisonParserAcceptsBothDirections(t *testing.T) {
	for _, op := range []string{"<", "<=", ">", ">="} {
		parsed, ok := parseComparisonFact("(x " + op + " 3)")
		if !ok || parsed.left != "x" || parsed.op != op || parsed.right != "3" {
			t.Errorf("operator %q parsed as %#v, %v", op, parsed, ok)
		}
	}
	if _, ok := parseComparisonFact("x == 3"); ok {
		t.Fatal("accepted unsupported equality")
	}
}
