package smtquery

import "testing"

func TestIntegerFoldingPreservesSMTSemantics(t *testing.T) {
	for _, tc := range []struct{ name, got, want string }{
		{"addition", Arith("+", "18446744073709551615", "1"), "18446744073709551616"},
		{"negative multiplication", Arith("*", "(- 4)", "6"), "(- 24)"},
		{"symbolic", Arith("+", "x", "1"), "(+ x 1)"},
		{"negative modulo", ModTerm("(- 7)", "3"), "2"},
		{"negative divisor modulo", ModTerm("(- 7)", "(- 3)"), "2"},
		{"negative division", DivTerm("(- 7)", "3"), "(- 3)"},
		{"zero divisor", DivTerm("5", "0"), "(div 5 0)"},
		{"literal conditional", IteTerm("true", "1", "2"), "1"},
		{"symbolic conditional", IteTerm("p", "1", "2"), "(ite p 1 2)"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	for _, input := range []string{"", "x", "-7", "(- x)", "(+ 1 2)"} {
		if _, ok := IntLiteral(input); ok {
			t.Errorf("accepted nonliteral %q", input)
		}
	}
}
