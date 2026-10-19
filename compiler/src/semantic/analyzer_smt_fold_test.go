package semantic

import (
	"math/big"
	"os/exec"
	"strings"
	"testing"

	"elisacore/src/lexer"
)

func TestSMTIntLiteralParsesOnlyEmittedLiteralForms(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"0", "0", true},
		{"42", "42", true},
		{"18446744073709551616", "18446744073709551616", true},
		{"(- 7)", "-7", true},
		{"(- 9223372036854775808)", "-9223372036854775808", true},
		{"", "", false},
		{"v_x", "", false},
		{"-7", "", false},
		{"(- v_x)", "", false},
		{"(- 7 1)", "", false},
		{"(- )", "", false},
		{"(+ 1 2)", "", false},
		{"(- (- 7))", "", false},
		{"true", "", false},
	} {
		v, ok := smtIntLiteral(tc.in)
		if ok != tc.ok || (ok && v.String() != tc.want) {
			t.Errorf("smtIntLiteral(%q) = %v, %v; want %s, %v", tc.in, v, ok, tc.want, tc.ok)
		}
	}
}

func TestSMTFoldGroundArithmetic(t *testing.T) {
	p64 := smtPow2(64)
	for _, tc := range []struct{ got, want string }{
		{smtArith("+", "0", "1"), "1"},
		{smtArith("-", "3", "5"), "(- 2)"},
		{smtArith("-", "(- 3)", "(- 5)"), "2"},
		{smtArith("*", "(- 4)", "6"), "(- 24)"},
		{smtArith("*", "4294967296", "4294967296"), p64},
		// Non-ground terms are emitted exactly as before.
		{smtArith("+", "v_x", "1"), "(+ v_x 1)"},
		{smtArith("-", "1", "(- v_x)"), "(- 1 (- v_x))"},
		// SMT-LIB mod is Euclidean: the result is in [0, |divisor|) for every operand sign.
		{smtModTerm("7", "3"), "1"},
		{smtModTerm("(- 7)", "3"), "2"},
		{smtModTerm("7", "(- 3)"), "1"},
		{smtModTerm("(- 7)", "(- 3)"), "2"},
		{smtModTerm("(- 6)", "3"), "0"},
		{smtModTerm("5", "0"), "(mod 5 0)"},
		{smtModTerm("v_x", "256"), "(mod v_x 256)"},
		// Euclidean div pairs with that mod: l = r*q + m.
		{smtDivTerm("7", "3"), "2"},
		{smtDivTerm("(- 7)", "3"), "(- 3)"},
		{smtDivTerm("7", "(- 3)"), "(- 2)"},
		{smtDivTerm("(- 7)", "(- 3)"), "3"},
		{smtDivTerm("5", "0"), "(div 5 0)"},
		// Truncating division rounds toward zero.
		{smtTruncDiv("7", "3"), "2"},
		{smtTruncDiv("(- 7)", "3"), "(- 2)"},
		{smtTruncDiv("7", "(- 3)"), "(- 2)"},
		{smtTruncDiv("(- 7)", "(- 3)"), "2"},
		{smtTruncDiv("5", "0"), "(ite (= (< 5 0) (< 0 0)) (div (ite (< 5 0) (- 5) 5) (ite (< 0 0) (- 0) 0)) (- (div (ite (< 5 0) (- 5) 5) (ite (< 0 0) (- 0) 0))))"},
		{smtIteTerm("true", "1", "2"), "1"},
		{smtIteTerm("false", "1", "2"), "2"},
		{smtIteTerm("b_c", "1", "2"), "(ite b_c 1 2)"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %s, want %s", tc.got, tc.want)
		}
	}
}

func TestSMTFoldMachineWrapBoundaries(t *testing.T) {
	p64 := smtPow2(64)
	maxU64 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1)).String()
	maxI64 := "9223372036854775807"
	minI64 := "(- 9223372036854775808)"
	for _, tc := range []struct{ name, got, want string }{
		// The motivating shape: two nested unsigned increments starting from 0.
		{"nested u64 increments", smtModTerm(smtArith("+", smtModTerm(smtArith("+", "0", "1"), p64), "1"), p64), "2"},
		{"u64 max + 1 wraps", smtModTerm(smtArith("+", maxU64, "1"), p64), "0"},
		{"u64 0 - 1 wraps", smtModTerm(smtArith("-", "0", "1"), p64), maxU64},
		{"i64 max + 1 wraps", smtSignedWrap(smtArith("+", maxI64, "1"), 64), minI64},
		{"i64 min - 1 wraps", smtSignedWrap(smtArith("-", minI64, "1"), 64), maxI64},
		{"i64 in range", smtSignedWrap("(- 5)", 64), "(- 5)"},
		{"i8 127 + 1", smtSignedWrap(smtArith("+", "127", "1"), 8), "(- 128)"},
		{"u8 of -1", smtSameWidthReinterpret("(- 1)", false, 8), "255"},
		{"u64 of -1", smtSameWidthReinterpret("(- 1)", false, 64), maxU64},
		{"i64 of 2^63", smtSameWidthReinterpret(smtPow2(63), true, 64), minI64},
		{"i64 of 2^63-1", smtSameWidthReinterpret(maxI64, true, 64), maxI64},
		{"i64 of 2^64-1", smtSameWidthReinterpret(maxU64, true, 64), "(- 1)"},
		{"i32 of 2^64", smtSameWidthReinterpret(p64, true, 32), "0"},
		// Non-ground wraps keep their exact previous text.
		{"symbolic signed wrap", smtSignedWrap("(+ v_a 1)", 64), "(- (mod (+ (+ v_a 1) 9223372036854775808) 18446744073709551616) 9223372036854775808)"},
		{"symbolic reinterpret", smtSameWidthReinterpret("v_a", true, 8), "(- (mod v_a 256) (ite (>= (mod v_a 256) 128) 256 0))"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, tc.got, tc.want)
		}
	}
}

func TestSMTFoldGroundComparisons(t *testing.T) {
	for _, tc := range []struct {
		op   lexer.TokenKind
		l, r string
		want string
	}{
		{lexer.TOKEN_LT, "(- 1)", "0", "true"},
		{lexer.TOKEN_LTEQ, "3", "3", "true"},
		{lexer.TOKEN_GT, "3", "3", "false"},
		{lexer.TOKEN_GTEQ, "18446744073709551616", "18446744073709551615", "true"},
		{lexer.TOKEN_EQEQ, "(- 0)", "0", "true"},
		{lexer.TOKEN_BANGEQ, "1", "2", "true"},
		{lexer.TOKEN_LT, "v_x", "0", "(< v_x 0)"},
		{lexer.TOKEN_EQEQ, "1", "v_y", "(= 1 v_y)"},
		// Unknown operators keep smtCompare's previous fallback.
		{lexer.TOKEN_PLUS, "1", "2", "false"},
	} {
		if got := smtCompare(tc.op, tc.l, tc.r); got != tc.want {
			t.Errorf("smtCompare(%v, %s, %s) = %s, want %s", tc.op, tc.l, tc.r, got, tc.want)
		}
	}
}

// TestSMTFoldAgreesWithZ3 checks every fold against z3's own evaluation of the unfolded SMT-LIB term,
// over operands that straddle 0 and the 2^63/2^64 boundaries.
func TestSMTFoldAgreesWithZ3(t *testing.T) {
	z3, err := exec.LookPath("z3")
	if err != nil {
		t.Skip("z3 not on PATH")
	}
	operands := []string{"0", "1", "(- 1)", "7", "(- 7)", "3", "(- 3)", "255", "256",
		"9223372036854775807", "(- 9223372036854775808)", "9223372036854775808",
		"18446744073709551615", "18446744073709551616", "(- 18446744073709551616)"}
	var script strings.Builder
	var want []string
	add := func(raw, folded string) {
		script.WriteString("(simplify (= " + raw + " " + folded + "))\n")
		want = append(want, raw+" => "+folded)
	}
	for _, l := range operands {
		for _, r := range operands {
			for _, op := range []string{"+", "-", "*"} {
				add("("+op+" "+l+" "+r+")", smtArith(op, l, r))
			}
			if v, _ := smtIntLiteral(r); v.Sign() != 0 {
				add("(mod "+l+" "+r+")", smtModTerm(l, r))
				add("(div "+l+" "+r+")", smtDivTerm(l, r))
			}
		}
		for _, bits := range []int{8, 63, 64} {
			add("(- (mod (+ "+l+" "+smtPow2(bits-1)+") "+smtPow2(bits)+") "+smtPow2(bits-1)+")", smtSignedWrap(l, bits))
			m := "(mod " + l + " " + smtPow2(bits) + ")"
			add("(- "+m+" (ite (>= "+m+" "+smtPow2(bits-1)+") "+smtPow2(bits)+" 0))", smtSameWidthReinterpret(l, true, bits))
		}
	}
	cmd := exec.Command(z3, "-in")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("z3: %v", err)
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) != len(want) {
		t.Fatalf("z3 returned %d answers for %d checks:\n%s", len(lines), len(want), out)
	}
	for i, line := range lines {
		if line != "true" {
			t.Errorf("fold disagrees with z3: %s (z3: %s)", want[i], line)
		}
	}
}
