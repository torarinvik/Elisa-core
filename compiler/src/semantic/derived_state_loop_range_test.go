package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestDerivedLiteralRangeRepresentations(t *testing.T) {
	for _, row := range []struct {
		name, typ, start, end   string
		addOne                  bool
		known, empty, singleton bool
	}{
		{"empty", "i64", "0", "0", false, true, true, false},
		{"reversed", "i64", "3", "0", false, true, true, false},
		{"singleton", "i64", "0", "1", false, true, false, true},
		{"many", "i64", "0", "3", false, true, false, false},
		{"unsigned-high", "u64", "0", "18446744073709551615", false, true, false, false},
		{"unsigned-high-singleton", "u64", "18446744073709551614", "18446744073709551615", false, true, false, true},
		{"unsigned-reversed", "u64", "18446744073709551615", "0", false, true, true, false},
		{"unsigned-inclusive-overflow", "u64", "0", "18446744073709551615", true, false, false, false},
		{"signed-inclusive-overflow", "i64", "0", "9223372036854775807", true, false, false, false},
		{"narrow-inclusive-overflow", "i8", "0", "127", true, false, false, false},
		{"target-dependent", "usize", "0", "3", false, false, false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			start := &ast.IntLit{Value: row.start, Suffix: row.typ}
			end := &ast.IntLit{Value: row.end, Suffix: row.typ}
			typ := &BuiltinType{Name: row.typ}
			a := &Analyzer{exprTypes: map[ast.Expr]Type{start: typ, end: typ}}
			var endExpr ast.Expr = end
			if row.addOne {
				one := &ast.IntLit{Value: "1", Suffix: row.typ}
				add := &ast.BinaryExpr{Left: end, Right: one, Op: lexer.TOKEN_PLUS}
				a.exprTypes[one], a.exprTypes[add] = typ, typ
				endExpr = add
			}
			known, empty, singleton := a.classifyDerivedLiteralRange(&ast.ForStmt{Start: start, End: endExpr, Op: lexer.TOKEN_RANGE_LT})
			if known != row.known || empty != row.empty || singleton != row.singleton {
				t.Fatalf("got known/empty/singleton %v/%v/%v, want %v/%v/%v", known, empty, singleton, row.known, row.empty, row.singleton)
			}
		})
	}
}
