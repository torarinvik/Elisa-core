//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestSMTExplicitNarrowIntegerCast(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"signed_to_unsigned", `def check(value: i64) -> u8:
    ensure result.i64() == ((value % 256 + 256) % 256)
    return value.u8()
`, false},
		{"unsigned_to_unsigned", `def check(value: u64) -> u8:
    ensure result.u64() == value % 256u64
    return value.u8()
`, false},
		{"signed_to_signed", `def check(value: i64) -> i8:
    requires value >= 0 and value <= 255
    ensure result.i64() == (value - 256 if value >= 128 else value)
    return value.i8()
`, false},
		{"unsigned_to_signed", `def check(value: u64) -> i8:
    requires value <= 255u64
    ensure result.i64() == (value.i64() - 256 if value >= 128u64 else value.i64())
    return value.i8()
`, false},
		{"unsigned_wrap_is_not_identity", `def check(value: i64) -> u8:
    ensure result.i64() == value
    return value.u8()
`, true},
		{"signed_wrap_is_not_identity", `def check(value: i64) -> i8:
    ensure result.i64() == value
    return value.i8()
`, true},
		{"comparison_narrows_rhs", `def check(value: i64) -> i8:
    ensure result == value
    return value.i8()
`, false},
		{"callee_source_type_does_not_use_caller_shadow", `struct Wide:
    value: u64
struct Narrow:
    value: u8
def low(box: Wide) -> u32:
    return box.value.u32()
def check(actual: Wide) -> u32:
    requires actual.value == 4294967297u64
    ensure result.u64() == actual.value
    box: Narrow = Narrow{value: 0}
    return low(actual)
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "narrow_integer_cast.elisa", tc.source)
			for _, diagnostic := range r.Errors() {
				if !strings.Contains(diagnostic, `ensure postcondition of "check"`) {
					t.Fatalf("unrelated error: %s", allDiagnostics(r))
				}
			}
			if rejected := ensureFailuresFor(r.Errors(), "check") != 0; rejected != tc.reject {
				t.Fatalf("rejected %v, want %v: %s", rejected, tc.reject, allDiagnostics(r))
			}
		})
	}
}

func TestSMTCalleeCastDoesNotUseCallerSourceType(t *testing.T) {
	a := &Analyzer{currentScope: NewScope(nil), exprTypes: map[ast.Expr]Type{}}
	a.currentScope.Define(&Symbol{Name: "value", Kind: SymbolLocal, Type: &BuiltinType{Name: "u8"}})
	value := &ast.Ident{Name: "value"}
	cast := func(operand ast.Expr, target string) *ast.CastExpr {
		return &ast.CastExpr{Operand: operand, Target: &ast.NamedType{Name: target}, Origin: ast.CastExprOriginPostfixShorthand}
	}
	for _, expression := range []ast.Expr{cast(value, "u32"), cast(cast(value, "i8"), "u8")} {
		tr := a.newSMTTranslator(nil)
		tr.calleeBodyDepth = 1
		if term, ok := tr.termEnv(expression, map[string]string{"value": "4294967297"}); ok {
			t.Fatalf("untyped callee source acquired caller u8 type, yielding %s", term)
		}
	}
}
