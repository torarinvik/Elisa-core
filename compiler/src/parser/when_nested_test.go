package parser

import (
	"elisacore/src/ast"
	"testing"
)

func TestWhenInlineNestedDecisionKeepsFollowingArms(t *testing.T) {
	for _, nested := range []string{"when", "match"} {
		t.Run(nested, func(t *testing.T) {
			separator := " -> "
			if nested == "match" {
				separator = ": "
			}
			source := "def classify(first: i64, second: i64) -> i64:\n" +
				"    return when first:\n" +
				"        1 -> " + nested + " second:\n" +
				"            2" + separator + "12\n" +
				"            _" + separator + "10\n" +
				"        3 -> 30\n" +
				"        _ -> 0\n" +
				"def following() -> i64:\n    return 42\n"
			file, errs := parseSourceFile(t, source)
			if len(errs) != 0 {
				t.Fatalf("nested decision rejected: %v", errs)
			}
			if len(file.Decls) != 2 {
				t.Fatalf("following function lost: %d declarations", len(file.Decls))
			}
			fn := file.Decls[0].(*ast.FuncDecl)
			outer := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.MatchExpr)
			if len(outer.Arms) != 3 {
				t.Fatalf("following outer arms lost: %d arms", len(outer.Arms))
			}
			inner, ok := outer.Arms[0].Body[0].(*ast.ExprStmt).Expr.(*ast.MatchExpr)
			if !ok || len(inner.Arms) != 2 {
				t.Fatalf("nested decision changed: %#v", inner)
			}
		})
	}
}
