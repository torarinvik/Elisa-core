package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestConstArrayAcceptsContextualFullWidthUnsignedLiterals(t *testing.T) {
	_, result := parseAndAnalyzeOptimizationFactsTest(t, "const_unsigned_array.elisa", `const IV: u64[4] = [0x6A09_E667_F3BC_C908, 0xBB67_AE85_84CA_A73B, (0xFFFF_FFFF_FFFF_FFFF), 18446744073709551615]
def use(value: u64) -> u64:
    return IV[1] ^ value ^ 0xFFFF_FFFF_FFFF_FFFF
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("unexpected semantic errors: %s", strings.Join(errors, "\n"))
	}
	got, ok := result.ConstValues["IV"]
	if !ok || got.Kind != ConstList || len(got.Elems) != 4 {
		t.Fatalf("IV = %#v, present=%v", got, ok)
	}
	want := []uint64{0x6a09e667f3bcc908, 0xbb67ae8584caa73b, ^uint64(0), ^uint64(0)}
	for i, elem := range got.Elems {
		if elem.Kind != ConstInt || uint64(elem.Int) != want[i] {
			t.Errorf("IV[%d] = %#v, want %#x", i, elem, want[i])
		}
	}
}

func TestConstArrayElementContextDoesNotLeakToOperands(t *testing.T) {
	array := &ArrayType{Elem: &BuiltinType{Name: "u64"}}
	a := &Analyzer{constEvalExpectedType: array}
	large := &ast.IntLit{Value: "0xffff_ffff_ffff_ffff", IsHex: true}
	expr := &ast.BinaryExpr{Left: large, Op: lexer.TOKEN_PLUS, Right: &ast.IntLit{Value: "1"}}
	if _, ok := a.evalConstArrayElement(expr, array.Elem); ok {
		t.Fatal("array element context leaked into a binary operand")
	}
	if a.constEvalExpectedType != array {
		t.Fatal("array context was not restored")
	}
	if value, ok := a.evalConstArrayElement(&ast.ParenExpr{Inner: large}, array.Elem); !ok || uint64(value.Int) != ^uint64(0) {
		t.Fatalf("parenthesized array element = %#v, valid=%v", value, ok)
	}
	if a.constEvalExpectedType != array {
		t.Fatal("array context was not restored after a successful element")
	}
}

func TestNestedConstArrayAcceptsContextualFullWidthUnsignedLiteral(t *testing.T) {
	_, result := parseAndAnalyzeOptimizationFactsTest(t, "nested_const_unsigned.elisa", "const GRID: u64[1][1] = [[0xffff_ffff_ffff_ffff]]\n")
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("unexpected semantic errors: %s", strings.Join(errors, "\n"))
	}
	outer, ok := result.ConstValues["GRID"]
	if !ok || outer.Kind != ConstList || len(outer.Elems) != 1 || outer.Elems[0].Kind != ConstList || len(outer.Elems[0].Elems) != 1 || uint64(outer.Elems[0].Elems[0].Int) != ^uint64(0) {
		t.Fatalf("GRID has wrong value: %#v", outer)
	}
}

func TestConstArrayUnsignedContextRejectsOverflowAndSignedElements(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"overflow", "const BAD: u64[1] = [0x1_0000_0000_0000_0000]\n"},
		{"signed", "const BAD: i64[1] = [0xFFFF_FFFF_FFFF_FFFF]\n"},
		{"signed_suffix", "const BAD: u64[1] = [0xFFFF_FFFF_FFFF_FFFFi64]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, result := parseAndAnalyzeOptimizationFactsTest(t, tc.name+".elisa", tc.source)
			if len(result.Errors()) == 0 {
				t.Fatal("out-of-range constant unexpectedly accepted")
			}
		})
	}
}
